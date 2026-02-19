package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestTavily creates a TavilySearchTool pointed at a mock server for unit testing.
func newTestTavily(server *httptest.Server) *TavilySearchTool {
	return &TavilySearchTool{
		apiKey:  "test-key",
		baseURL: server.URL,
		client:  server.Client(),
	}
}

func TestTavilySearchTool_Interface(t *testing.T) {
	tool := NewTavilySearchTool("key")
	if tool.Name() != "web_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_search")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	schema := tool.InputSchema()
	if !strings.Contains(string(schema), `"query"`) {
		t.Error("InputSchema() should contain 'query' field")
	}
	if err := tool.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestTavilySearchTool_Init_EmptyKey(t *testing.T) {
	tool := NewTavilySearchTool("")
	if err := tool.Init(context.Background()); err == nil {
		t.Error("Init() should fail with empty API key")
	}
}

func TestTavilySearchTool_Init_ValidKey(t *testing.T) {
	tool := NewTavilySearchTool("my-api-key")
	if err := tool.Init(context.Background()); err != nil {
		t.Errorf("Init() should succeed with valid key: %v", err)
	}
}

func TestTavilySearchTool_EmptyQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make HTTP request for empty query")
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for empty query")
	}
}

func TestTavilySearchTool_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make HTTP request for bad JSON")
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid JSON")
	}
	if !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("error %q should mention parse failure", result.Error)
	}
}

func TestTavilySearchTool_Success(t *testing.T) {
	response := tavilyResponse{
		Results: []tavilyResult{
			{Title: "Go 语言", URL: "https://golang.org", Content: "Go 是一门编程语言"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Content-Type header
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		// Verify API key is sent in request body (Tavily's design)
		var body tavilyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if body.APIKey != "test-key" {
			t.Errorf("APIKey in body = %q, want %q", body.APIKey, "test-key")
		}
		if body.Query != "golang" {
			t.Errorf("Query in body = %q, want %q", body.Query, "golang")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"golang"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Go 语言") {
		t.Errorf("output %q should contain result title", result.Output)
	}
	if !strings.Contains(result.Output, "https://golang.org") {
		t.Errorf("output %q should contain result URL", result.Output)
	}
}

func TestTavilySearchTool_WithAnswer(t *testing.T) {
	response := tavilyResponse{
		Answer:  "Go 语言由 Google 创建",
		Results: []tavilyResult{{Title: "Go", URL: "https://golang.org", Content: "详情"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"golang"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "摘要") {
		t.Errorf("output %q should contain answer summary label", result.Output)
	}
	if !strings.Contains(result.Output, "Go 语言由 Google 创建") {
		t.Errorf("output %q should contain answer text", result.Output)
	}
}

func TestTavilySearchTool_EmptyResults(t *testing.T) {
	response := tavilyResponse{Results: []tavilyResult{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"xyzxyz123"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "未找到") {
		t.Errorf("output %q should mention no results", result.Output)
	}
}

func TestTavilySearchTool_NonOKStatus(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{"401 Unauthorized", http.StatusUnauthorized},
		{"429 Too Many Requests", http.StatusTooManyRequests},
		{"500 Internal Server Error", http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.code)
				fmt.Fprintln(w, "error body")
			}))
			defer server.Close()

			tool := newTestTavily(server)
			result, err := tool.Execute(context.Background(), []byte(`{"query":"test"}`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" {
				t.Errorf("expected error for HTTP %d", tt.code)
			}
			if !strings.Contains(result.Error, fmt.Sprintf("%d", tt.code)) {
				t.Errorf("error %q should contain status code %d", result.Error, tt.code)
			}
		})
	}
}

func TestTavilySearchTool_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, "not valid json at all")
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid JSON response")
	}
	if !strings.Contains(result.Error, "响应解析失败") {
		t.Errorf("error %q should mention parse failure", result.Error)
	}
}

// TestTavilySearchTool_ContentTruncation verifies that long result content is
// truncated to searchDescMaxRunes in the formatted output.
func TestTavilySearchTool_ContentTruncation(t *testing.T) {
	// Use a character that does not appear in any format string or URL so that
	// strings.Count gives an exact measure of the content portion only.
	longContent := strings.Repeat("喵", 400) // exceeds searchDescMaxRunes (300)
	response := tavilyResponse{
		Results: []tavilyResult{
			{Title: "Title", URL: "https://go.dev", Content: longContent},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestTavily(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	// Output should contain ellipsis indicating truncation.
	if !strings.Contains(result.Output, "...") {
		t.Error("long content should be truncated with '...'")
	}
	// The '喵' rune count in output must not exceed the truncation limit.
	if strings.Count(result.Output, "喵") > searchDescMaxRunes {
		t.Errorf("content not truncated to %d runes", searchDescMaxRunes)
	}
}

// TestTavilyRequest_String_MasksAPIKey verifies the API key does not appear
// in the String() output, preventing accidental log exposure.
func TestTavilyRequest_String_MasksAPIKey(t *testing.T) {
	req := tavilyRequest{
		APIKey:     "secret-key-12345",
		Query:      "golang",
		MaxResults: 5,
	}
	s := req.String()
	if strings.Contains(s, "secret-key-12345") {
		t.Errorf("String() %q must not expose API key", s)
	}
	if !strings.Contains(s, "golang") {
		t.Errorf("String() %q should contain query", s)
	}
	if !strings.Contains(s, "5") {
		t.Errorf("String() %q should contain MaxResults", s)
	}
}

// TestTavilySearchTool_String_MasksAPIKey verifies the tool struct itself
// does not expose the API key when printed, consistent with BraveSearchTool.
func TestTavilySearchTool_String_MasksAPIKey(t *testing.T) {
	tool := NewTavilySearchTool("super-secret-tavily-key")
	s := tool.String()
	if strings.Contains(s, "super-secret-tavily-key") {
		t.Errorf("String() %q must not expose API key", s)
	}
	if !strings.Contains(s, "TavilySearchTool") {
		t.Errorf("String() %q should identify the type", s)
	}
}
