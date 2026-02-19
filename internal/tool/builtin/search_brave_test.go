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

// newTestBrave creates a BraveSearchTool pointed at a mock server for unit testing.
func newTestBrave(server *httptest.Server) *BraveSearchTool {
	return &BraveSearchTool{
		apiKey:  "test-brave-key",
		baseURL: server.URL,
		client:  server.Client(),
	}
}

func TestBraveSearchTool_Interface(t *testing.T) {
	tool := NewBraveSearchTool("key")
	if tool.Name() != "brave_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "brave_search")
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

func TestBraveSearchTool_Init_EmptyKey(t *testing.T) {
	tool := NewBraveSearchTool("")
	if err := tool.Init(context.Background()); err == nil {
		t.Error("Init() should fail with empty API key")
	}
}

func TestBraveSearchTool_Init_ValidKey(t *testing.T) {
	tool := NewBraveSearchTool("my-brave-key")
	if err := tool.Init(context.Background()); err != nil {
		t.Errorf("Init() should succeed with valid key: %v", err)
	}
}

func TestBraveSearchTool_EmptyQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make HTTP request for empty query")
	}))
	defer server.Close()

	tool := newTestBrave(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for empty query")
	}
}

func TestBraveSearchTool_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make HTTP request for bad JSON")
	}))
	defer server.Close()

	tool := newTestBrave(server)
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

func TestBraveSearchTool_Success(t *testing.T) {
	response := braveResponse{}
	response.Web.Results = []braveResult{
		{Title: "Brave 搜索", URL: "https://brave.com", Description: "隐私优先的搜索引擎"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify API key is sent via header (Brave's design, not request body)
		if r.Header.Get("X-Subscription-Token") != "test-brave-key" {
			t.Errorf("X-Subscription-Token = %q, want %q",
				r.Header.Get("X-Subscription-Token"), "test-brave-key")
		}
		// Verify Accept header
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		// Verify query param is present
		if r.URL.Query().Get("q") == "" {
			t.Error("expected non-empty 'q' query parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestBrave(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"brave"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Brave 搜索") {
		t.Errorf("output %q should contain result title", result.Output)
	}
	if !strings.Contains(result.Output, "https://brave.com") {
		t.Errorf("output %q should contain result URL", result.Output)
	}
}

func TestBraveSearchTool_EmptyResults(t *testing.T) {
	response := braveResponse{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestBrave(server)
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

func TestBraveSearchTool_NonOKStatus(t *testing.T) {
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

			tool := newTestBrave(server)
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

func TestBraveSearchTool_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, "not valid json at all")
	}))
	defer server.Close()

	tool := newTestBrave(server)
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

// TestBraveSearchTool_QueryEncoding verifies that the query string is correctly
// URL-encoded in the GET request parameters.
func TestBraveSearchTool_QueryEncoding(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		response := braveResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestBrave(server)
	_, _ = tool.Execute(context.Background(), []byte(`{"query":"golang url encoding test"}`))
	if receivedQuery != "golang url encoding test" {
		t.Errorf("received query %q, want %q", receivedQuery, "golang url encoding test")
	}
}

// TestBraveSearchTool_ContentTruncation verifies that long result descriptions
// are truncated to searchDescMaxRunes in the formatted output.
func TestBraveSearchTool_ContentTruncation(t *testing.T) {
	longDesc := strings.Repeat("y", 400) // exceeds searchDescMaxRunes (300)
	response := braveResponse{}
	response.Web.Results = []braveResult{
		{Title: "Title", URL: "https://go.dev", Description: longDesc},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tool := newTestBrave(server)
	result, err := tool.Execute(context.Background(), []byte(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "...") {
		t.Error("long description should be truncated with '...'")
	}
	if strings.Count(result.Output, "y") > searchDescMaxRunes {
		t.Errorf("description not truncated to %d runes", searchDescMaxRunes)
	}
}

// TestBraveSearchTool_String_MasksAPIKey verifies the API key does not appear
// in the String() output, preventing accidental log exposure.
func TestBraveSearchTool_String_MasksAPIKey(t *testing.T) {
	tool := NewBraveSearchTool("super-secret-brave-key")
	s := tool.String()
	if strings.Contains(s, "super-secret-brave-key") {
		t.Errorf("String() %q must not expose API key", s)
	}
	if !strings.Contains(s, "BraveSearchTool") {
		t.Errorf("String() %q should identify the type", s)
	}
}
