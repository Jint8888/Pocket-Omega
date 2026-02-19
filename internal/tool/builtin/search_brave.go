package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	braveAPIURL      = "https://api.search.brave.com/res/v1/web/search"
	braveMaxResults  = 5
	braveHTTPTimeout = 15 * time.Second
	braveMaxBody     = 5 << 20 // 5MB success response limit
	braveErrMaxBody  = 1 << 20 // 1MB error response limit
	braveErrBodyShow = 200     // max chars of error body shown to caller
)

// BraveSearchTool provides web search via Brave Search API.
type BraveSearchTool struct {
	apiKey  string
	baseURL string       // injectable for tests; defaults to braveAPIURL
	client  *http.Client // dedicated client to avoid shared http.DefaultClient
}

// String returns a log-safe representation with the API key omitted,
// preventing accidental key exposure if the struct is printed.
func (t *BraveSearchTool) String() string {
	return fmt.Sprintf("BraveSearchTool{baseURL: %q}", t.baseURL)
}

func NewBraveSearchTool(apiKey string) *BraveSearchTool {
	return &BraveSearchTool{
		apiKey:  apiKey,
		baseURL: braveAPIURL,
		// No client-level Timeout: request lifetime is controlled exclusively
		// via context.WithTimeout in Execute so that callers can impose
		// shorter deadlines and the two timeouts do not conflict.
		client: &http.Client{},
	}
}

func (t *BraveSearchTool) Name() string { return "brave_search" }
func (t *BraveSearchTool) Description() string {
	return "使用 Brave 搜索引擎在互联网上搜索信息。"
}

func (t *BraveSearchTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "query", Type: "string", Description: "搜索关键词", Required: true},
	)
}

// Init validates that the API key is configured before the tool is used.
func (t *BraveSearchTool) Init(_ context.Context) error {
	if t.apiKey == "" {
		return fmt.Errorf("brave API key 未配置")
	}
	return nil
}

func (t *BraveSearchTool) Close() error { return nil }

// braveResponse is the Brave Search API response (simplified).
type braveResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
}

type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func (t *BraveSearchTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	query, err := parseSearchQuery(args)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	// Build request URL using url.Parse to handle any existing query parameters
	// in baseURL safely (avoids double-? if baseURL already contains a query string).
	u, err := url.Parse(t.baseURL)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("无效的请求地址: %v", err)}, nil
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", braveMaxResults))
	u.RawQuery = q.Encode()
	requestURL := u.String()

	// Single timeout via context so the caller's deadline is always respected.
	httpCtx, cancel := context.WithTimeout(ctx, braveHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求创建失败: %v", err)}, nil
	}
	req.Header.Set("Accept", "application/json")
	// API key is sent via header (not body) per Brave's API design.
	req.Header.Set("X-Subscription-Token", t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("搜索请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// LimitReader prevents OOM from unexpectedly large error bodies;
		// further truncated before returning to avoid exposing internal details.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, braveErrMaxBody))
		bodyStr := truncateRunes(strings.TrimSpace(string(body)), braveErrBodyShow)
		return tool.ToolResult{Error: fmt.Sprintf("Brave API 错误 (HTTP %d): %s",
			resp.StatusCode, bodyStr)}, nil
	}

	// Decode with LimitReader to prevent OOM from unbounded success response bodies.
	var braveResp braveResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, braveMaxBody)).Decode(&braveResp); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("响应解析失败: %v", err)}, nil
	}

	results := make([]searchResult, len(braveResp.Web.Results))
	for i, r := range braveResp.Web.Results {
		results[i] = searchResult{Title: r.Title, URL: r.URL, Description: r.Description}
	}

	return tool.ToolResult{Output: formatSearchResults(results)}, nil
}
