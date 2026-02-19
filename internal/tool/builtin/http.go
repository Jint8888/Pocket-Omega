package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	httpMaxResponseChars = 8000 // rune limit for response body output
	httpMaxTimeout       = 30   // seconds, hard upper bound
	httpDefaultTimeout   = 10   // seconds
	httpMaxRedirects     = 3
)

// privateNetworks lists all IPv4/IPv6 address ranges considered internal.
// Covers RFC-1918 private ranges, loopback, link-local, ULA, CGNAT, and
// other address blocks that could be used for SSRF bypasses.
// Initialized once at package load time.
var privateNetworks []*net.IPNet

func init() {
	for _, cidr := range []string{
		"0.0.0.0/8",       // "this network"; routes to localhost on many systems
		"10.0.0.0/8",      // RFC-1918 private
		"100.64.0.0/10",   // Carrier-grade NAT (CGNAT); internal in cloud envs
		"127.0.0.0/8",     // IPv4 loopback (belt-and-suspenders with IsLoopback)
		"169.254.0.0/16",  // IPv4 link-local
		"172.16.0.0/12",   // RFC-1918 private
		"192.168.0.0/16",  // RFC-1918 private
		"198.18.0.0/15",   // benchmark / testing range
		"::1/128",         // IPv6 loopback
		"fc00::/7",        // IPv6 unique local (ULA)
		"fe80::/10",       // IPv6 link-local
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			privateNetworks = append(privateNetworks, network)
		}
	}
}

// allowedHTTPMethods is the set of HTTP verbs we permit.
var allowedHTTPMethods = map[string]bool{
	"GET":     true,
	"POST":    true,
	"PUT":     true,
	"PATCH":   true,
	"DELETE":  true,
	"HEAD":    true,
	"OPTIONS": true,
}

// usefulResponseHeaders are the header names we surface to the LLM.
// Omitting Set-Cookie, authentication headers, and server internals.
var usefulResponseHeaders = map[string]bool{
	"Content-Type":           true,
	"Content-Length":         true,
	"Content-Encoding":       true,
	"Location":               true,
	"Cache-Control":          true,
	"Retry-After":            true,
	"X-Ratelimit-Limit":      true,
	"X-Ratelimit-Remaining":  true,
	"X-Ratelimit-Reset":      true,
	"X-Request-Id":           true,
	"X-Correlation-Id":       true,
}

// ── http_request ──

type HTTPRequestTool struct {
	allowInternal bool
}

// NewHTTPRequestTool creates the tool.
// allowInternal is read from TOOL_HTTP_ALLOW_INTERNAL env var by the caller.
func NewHTTPRequestTool(allowInternal bool) *HTTPRequestTool {
	return &HTTPRequestTool{allowInternal: allowInternal}
}

func (t *HTTPRequestTool) Name() string { return "http_request" }
func (t *HTTPRequestTool) Description() string {
	return "发送 HTTP 请求并返回响应，用于 API 调试、Webhook 测试、接口验证。默认禁止访问内网地址（可通过 TOOL_HTTP_ALLOW_INTERNAL=true 开启）。"
}

func (t *HTTPRequestTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "url", Type: "string", Description: "请求 URL（必须 http/https）", Required: true},
		tool.SchemaParam{Name: "method", Type: "string", Description: "请求方法：GET、POST、PUT、PATCH、DELETE、HEAD、OPTIONS（默认 GET）", Required: false},
		tool.SchemaParam{Name: "headers", Type: "object", Description: "请求头键值对", Required: false},
		tool.SchemaParam{Name: "body", Type: "string", Description: "请求体（POST/PUT 时使用）", Required: false},
		tool.SchemaParam{Name: "timeout", Type: "integer", Description: "超时秒数（默认 10，上限 30）", Required: false},
	)
}

func (t *HTTPRequestTool) Init(_ context.Context) error { return nil }
func (t *HTTPRequestTool) Close() error                 { return nil }

type httpRequestArgs struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Timeout int               `json:"timeout"`
}

func (t *HTTPRequestTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a httpRequestArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	if strings.TrimSpace(a.URL) == "" {
		return tool.ToolResult{Error: "url 不能为空"}, nil
	}

	// Protocol whitelist: http and https only
	urlLower := strings.ToLower(a.URL)
	if !strings.HasPrefix(urlLower, "http://") && !strings.HasPrefix(urlLower, "https://") {
		return tool.ToolResult{Error: "仅支持 http:// 和 https:// 协议，不支持 file://、ftp:// 等"}, nil
	}

	// Method whitelist
	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if method == "" {
		method = "GET"
	}
	if !allowedHTTPMethods[method] {
		return tool.ToolResult{Error: fmt.Sprintf("不支持的 HTTP 方法: %s（支持: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS）", method)}, nil
	}

	timeoutSec := a.Timeout
	if timeoutSec <= 0 {
		timeoutSec = httpDefaultTimeout
	}
	if timeoutSec > httpMaxTimeout {
		timeoutSec = httpMaxTimeout
	}
	timeout := time.Duration(timeoutSec) * time.Second

	allowInternal := t.allowInternal

	// Custom dialer that blocks internal IPs at connect time (first line of defense).
	// CheckRedirect below provides a second check for redirect targets before each hop.
	baseDialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if !allowInternal {
				if err := blockInternalHost(host); err != nil {
					return nil, err
				}
			}
			return baseDialer.DialContext(dialCtx, network, addr)
		},
	}

	redirectsDone := 0
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			redirectsDone++
			if redirectsDone > httpMaxRedirects {
				return fmt.Errorf("超过最大重定向次数 %d", httpMaxRedirects)
			}
			// Second line of defense for redirect targets; DialContext also checks,
			// but early rejection here avoids unnecessary DNS resolution.
			if !allowInternal {
				if err := blockInternalHost(req.URL.Hostname()); err != nil {
					return err
				}
			}
			return nil
		},
	}

	// Build request
	var bodyReader io.Reader
	if a.Body != "" {
		bodyReader = strings.NewReader(a.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.URL, bodyReader)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("创建请求失败: %v", err)}, nil
	}
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}

	// Execute
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	// Read response body with a 1MB raw cap to prevent OOM
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("读取响应体失败: %v", err)}, nil
	}

	contentType := resp.Header.Get("Content-Type")

	// Detect binary response
	if isBinaryHTTPResponse(contentType, rawBody) {
		return tool.ToolResult{
			Output: fmt.Sprintf("状态: %s\n耗时: %dms\n\nContent-Type: %s\n响应体: 二进制内容 (%d bytes)，未显示",
				resp.Status, elapsed.Milliseconds(), contentType, len(rawBody)),
		}, nil
	}

	bodyStr := string(rawBody)
	truncated := false
	if utf8.RuneCountInString(bodyStr) > httpMaxResponseChars {
		runes := []rune(bodyStr)
		bodyStr = string(runes[:httpMaxResponseChars])
		truncated = true
	}

	// Build formatted output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("状态: %s\n", resp.Status))
	sb.WriteString(fmt.Sprintf("耗时: %dms\n", elapsed.Milliseconds()))

	// Emit only headers useful to the agent; skip Set-Cookie, auth tokens, etc.
	var headerLines []string
	for k, vs := range resp.Header {
		if usefulResponseHeaders[http.CanonicalHeaderKey(k)] {
			headerLines = append(headerLines, fmt.Sprintf("  %s: %s", k, strings.Join(vs, ", ")))
		}
	}
	if len(headerLines) > 0 {
		sb.WriteString("\nHeaders:\n")
		for _, line := range headerLines {
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\nBody:\n")
	sb.WriteString(bodyStr)
	if truncated {
		sb.WriteString(fmt.Sprintf("\n...[响应体已截断，共 %d bytes]", len(rawBody)))
	}

	return tool.ToolResult{Output: sb.String()}, nil
}

// blockInternalHost resolves host to IPs and returns an error if any IP is internal.
func blockInternalHost(host string) error {
	ips, err := net.LookupHost(host)
	if err != nil {
		// Treat unresolvable host as-is (may be a raw IP)
		ips = []string{host}
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		// IsLoopback covers 127.x (belt-and-suspenders with 127.0.0.0/8 CIDR).
		// IsLinkLocalUnicast covers 169.254.x and fe80::/10.
		// IsUnspecified covers 0.0.0.0 and :: which route to localhost on many systems.
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("安全限制: 禁止访问内网地址 %s（可通过 TOOL_HTTP_ALLOW_INTERNAL=true 开启）", host)
		}
		for _, network := range privateNetworks {
			if network.Contains(ip) {
				return fmt.Errorf("安全限制: 禁止访问内网地址 %s（可通过 TOOL_HTTP_ALLOW_INTERNAL=true 开启）", host)
			}
		}
	}
	return nil
}

// isBinaryHTTPResponse returns true for binary content types or non-text bodies.
func isBinaryHTTPResponse(contentType string, body []byte) bool {
	ct := strings.ToLower(contentType)
	for _, prefix := range []string{
		"image/", "audio/", "video/",
		"application/octet-stream", "application/pdf",
		"application/zip", "application/gzip",
		"application/x-tar", "application/x-binary",
	} {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	if len(body) == 0 {
		return false
	}
	return bytes.IndexByte(body, 0) >= 0 && !utf8.Valid(body)
}
