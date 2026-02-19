package builtin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── HTTPRequestTool Execute tests ────────────────────────────────────────────

func TestHTTPRequestTool_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// Must allow internal since httptest binds to 127.0.0.1
	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL, Method: "GET"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "200") {
		t.Errorf("output should contain 200 status, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, `{"status":"ok"}`) {
		t.Errorf("output should contain response body, got: %q", result.Output)
	}
}

func TestHTTPRequestTool_PostWithBody(t *testing.T) {
	var receivedBody string
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{
		URL:    server.URL,
		Method: "POST",
		Body:   `{"name":"test"}`,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if receivedMethod != "POST" {
		t.Errorf("method = %q, want POST", receivedMethod)
	}
	if !strings.Contains(receivedBody, `{"name":"test"}`) {
		t.Errorf("server received body = %q, want JSON payload", receivedBody)
	}
	if !strings.Contains(result.Output, "201") {
		t.Errorf("output should contain 201 status, got: %q", result.Output)
	}
}

func TestHTTPRequestTool_Non200StatusReturned(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"403 Forbidden", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte("error response"))
			}))
			defer server.Close()

			tool := NewHTTPRequestTool(true)
			args, _ := json.Marshal(httpRequestArgs{URL: server.URL})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Non-200 should NOT be a tool error; it should be in the output
			if result.Error != "" {
				t.Errorf("non-200 status should not be a tool error, got: %s", result.Error)
			}
			if !strings.Contains(result.Output, "error response") {
				t.Errorf("output should contain response body, got: %q", result.Output)
			}
		})
	}
}

func TestHTTPRequestTool_EmptyURL(t *testing.T) {
	tool := NewHTTPRequestTool(false)
	args, _ := json.Marshal(httpRequestArgs{URL: ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "url 不能为空") {
		t.Errorf("expected empty url error, got: %+v", result)
	}
}

func TestHTTPRequestTool_InvalidProtocol(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"ftp", "ftp://example.com/file"},
		{"file", "file:///etc/passwd"},
		{"javascript", "javascript:alert(1)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewHTTPRequestTool(false)
			args, _ := json.Marshal(httpRequestArgs{URL: tt.url})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" || !strings.Contains(result.Error, "仅支持 http://") {
				t.Errorf("expected protocol error, got: %+v", result)
			}
		})
	}
}

func TestHTTPRequestTool_BlockInternalIPv4(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1/test"},
		{"private 10.x", "http://10.0.0.1/test"},
		{"private 172.16.x", "http://172.16.0.1/test"},
		{"private 192.168.x", "http://192.168.1.1/test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewHTTPRequestTool(false)
			args, _ := json.Marshal(httpRequestArgs{URL: tt.url})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" || !strings.Contains(result.Error, "内网地址") {
				t.Errorf("expected internal IP block, got: %+v", result)
			}
		})
	}
}

func TestHTTPRequestTool_BlockInternalIPv6(t *testing.T) {
	tool := NewHTTPRequestTool(false)
	args, _ := json.Marshal(httpRequestArgs{URL: "http://[::1]/test"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "内网地址") {
		t.Errorf("expected IPv6 loopback block, got: %+v", result)
	}
}

func TestHTTPRequestTool_AllowInternalWhenEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("internal ok"))
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true) // allowInternal = true
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("internal should be allowed when enabled, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "internal ok") {
		t.Errorf("should return response body, got: %q", result.Output)
	}
}

func TestHTTPRequestTool_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL, Timeout: 1})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Errorf("expected timeout error, got success: %q", result.Output)
	}
}

func TestHTTPRequestTool_DefaultMethod(t *testing.T) {
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL}) // no method specified
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if receivedMethod != "GET" {
		t.Errorf("default method = %q, want GET", receivedMethod)
	}
}

func TestHTTPRequestTool_CustomHeaders(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{
		URL: server.URL,
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if receivedAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want %q", receivedAuth, "Bearer test-token")
	}
}

func TestHTTPRequestTool_BinaryResponseDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A})
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "二进制内容") {
		t.Errorf("binary response should be detected, got: %q", result.Output)
	}
}

func TestHTTPRequestTool_ResponseBodyTruncation(t *testing.T) {
	// Create a response larger than httpMaxResponseChars (8000 runes)
	largeBody := strings.Repeat("x", 10000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeBody))
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "已截断") {
		t.Errorf("large response should be truncated, got output length: %d", len(result.Output))
	}
}

func TestHTTPRequestTool_BadJSON(t *testing.T) {
	tool := NewHTTPRequestTool(false)
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestHTTPRequestTool_TimeoutClamped(t *testing.T) {
	// Verify that timeout > httpMaxTimeout is clamped, not rejected
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	tool := NewHTTPRequestTool(true)
	args, _ := json.Marshal(httpRequestArgs{URL: server.URL, Timeout: 999})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("large timeout should be clamped not rejected, got error: %s", result.Error)
	}
}

// ── blockInternalHost unit tests ─────────────────────────────────────────────

func TestBlockInternalHost(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		wantBlock bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback IPv6", "::1", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 192.168.x", "192.168.1.1", true},
		{"link-local IPv4", "169.254.1.1", true},
		{"public IP", "8.8.8.8", false},
		{"public IP 2", "1.1.1.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := blockInternalHost(tt.host)
			if tt.wantBlock && err == nil {
				t.Errorf("blockInternalHost(%q) should have blocked", tt.host)
			}
			if !tt.wantBlock && err != nil {
				t.Errorf("blockInternalHost(%q) should not have blocked: %v", tt.host, err)
			}
		})
	}
}

// ── isBinaryHTTPResponse unit tests ──────────────────────────────────────────

func TestIsBinaryHTTPResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        bool
	}{
		{"image/png", "image/png", nil, true},
		{"application/pdf", "application/pdf", nil, true},
		{"application/json", "application/json", []byte(`{}`), false},
		{"text/plain", "text/plain", []byte("hello"), false},
		{"empty body text", "text/html", []byte{}, false},
		{"audio/mpeg", "audio/mpeg", nil, true},
		{"video/mp4", "video/mp4", nil, true},
		{"application/zip", "application/zip", nil, true},
		{"application/octet-stream", "application/octet-stream", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBinaryHTTPResponse(tt.contentType, tt.body)
			if got != tt.want {
				t.Errorf("isBinaryHTTPResponse(%q, ...) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

// ── privateNetworks init test ────────────────────────────────────────────────

func TestPrivateNetworksInitialized(t *testing.T) {
	if len(privateNetworks) == 0 {
		t.Error("privateNetworks should be initialized with CIDR ranges")
	}

	// Verify a known private IP is contained
	ip := net.ParseIP("192.168.1.1")
	found := false
	for _, network := range privateNetworks {
		if network.Contains(ip) {
			found = true
			break
		}
	}
	if !found {
		t.Error("192.168.1.1 should be in privateNetworks")
	}
}
