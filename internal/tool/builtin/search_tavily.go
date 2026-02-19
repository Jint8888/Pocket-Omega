package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	tavilyAPIURL      = "https://api.tavily.com/search"
	tavilyMaxResults  = 5
	tavilyHTTPTimeout = 15 * time.Second
	tavilyMaxBody     = 5 << 20 // 5MB success response limit
	tavilyErrMaxBody  = 1 << 20 // 1MB error response limit
	tavilyErrBodyShow = 200     // max chars of error body shown to caller
)

// TavilySearchTool provides web search via Tavily API.
type TavilySearchTool struct {
	apiKey  string
	baseURL string       // injectable for tests; defaults to tavilyAPIURL
	client  *http.Client // dedicated client to avoid shared http.DefaultClient
}

// String returns a log-safe representation with the API key omitted,
// preventing accidental key exposure if the struct is printed.
func (t *TavilySearchTool) String() string {
	return fmt.Sprintf("TavilySearchTool{baseURL: %q}", t.baseURL)
}

func NewTavilySearchTool(apiKey string) *TavilySearchTool {
	return &TavilySearchTool{
		apiKey:  apiKey,
		baseURL: tavilyAPIURL,
		// No client-level Timeout: request lifetime is controlled exclusively
		// via context.WithTimeout in Execute so that callers can impose
		// shorter deadlines and the two timeouts do not conflict.
		client: &http.Client{},
	}
}

func (t *TavilySearchTool) Name() string { return "web_search" }
func (t *TavilySearchTool) Description() string {
	return "在互联网上搜索信息。适用于获取实时新闻、技术文档、事实查询等。"
}

func (t *TavilySearchTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "query", Type: "string", Description: "搜索关键词", Required: true},
	)
}

// Init validates that the API key is configured before the tool is used.
func (t *TavilySearchTool) Init(_ context.Context) error {
	if t.apiKey == "" {
		return fmt.Errorf("tavily API key 未配置")
	}
	return nil
}

func (t *TavilySearchTool) Close() error { return nil }

// tavilyRequest is the Tavily API request body.
type tavilyRequest struct {
	APIKey     string `json:"api_key"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// String returns a log-safe representation with the API key masked,
// preventing accidental key exposure in fmt.Print / log output.
func (r tavilyRequest) String() string {
	return fmt.Sprintf("tavilyRequest{Query: %q, MaxResults: %d}", r.Query, r.MaxResults)
}

// tavilyResponse is the Tavily API response.
type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
	Answer  string         `json:"answer,omitempty"`
}

type tavilyResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

func (t *TavilySearchTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	query, err := parseSearchQuery(args)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	// Build request body (API key goes in body per Tavily's API design).
	reqBody := tavilyRequest{
		APIKey:     t.apiKey,
		Query:      query,
		MaxResults: tavilyMaxResults,
	}
	// SECURITY: bodyBytes contains the plaintext API key.
	// Do NOT log or expose bodyBytes in error messages or debug output.
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求构建失败: %v", err)}, nil
	}

	// Single timeout via context so the caller's deadline is always respected.
	httpCtx, cancel := context.WithTimeout(ctx, tavilyHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, t.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求创建失败: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("搜索请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// LimitReader prevents OOM from unexpectedly large error bodies;
		// further truncated before returning to avoid exposing internal details.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, tavilyErrMaxBody))
		bodyStr := truncateRunes(strings.TrimSpace(string(body)), tavilyErrBodyShow)
		return tool.ToolResult{Error: fmt.Sprintf("Tavily API 错误 (HTTP %d): %s",
			resp.StatusCode, bodyStr)}, nil
	}

	// Decode with LimitReader to prevent OOM from unbounded success response bodies.
	var tavilyResp tavilyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, tavilyMaxBody)).Decode(&tavilyResp); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("响应解析失败: %v", err)}, nil
	}

	// Format results using shared helpers.
	var sb strings.Builder
	if tavilyResp.Answer != "" {
		sb.WriteString(fmt.Sprintf("摘要：%s\n\n", tavilyResp.Answer))
	}

	results := make([]searchResult, len(tavilyResp.Results))
	for i, r := range tavilyResp.Results {
		results[i] = searchResult{Title: r.Title, URL: r.URL, Description: r.Content}
	}
	sb.WriteString(formatSearchResults(results))

	return tool.ToolResult{Output: sb.String()}, nil
}
