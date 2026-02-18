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
)

// BraveSearchTool provides web search via Brave Search API.
type BraveSearchTool struct {
	apiKey string
}

func NewBraveSearchTool(apiKey string) *BraveSearchTool {
	return &BraveSearchTool{apiKey: apiKey}
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

func (t *BraveSearchTool) Init(_ context.Context) error { return nil }
func (t *BraveSearchTool) Close() error                 { return nil }

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
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	query := strings.TrimSpace(a.Query)
	if query == "" {
		return tool.ToolResult{Error: "搜索关键词不能为空"}, nil
	}

	// Build request URL
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", braveMaxResults))
	requestURL := braveAPIURL + "?" + params.Encode()

	// HTTP call with timeout
	httpCtx, cancel := context.WithTimeout(ctx, braveHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求创建失败: %v", err)}, nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("搜索请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return tool.ToolResult{Error: fmt.Sprintf("Brave API 错误 (HTTP %d): %s", resp.StatusCode, string(body))}, nil
	}

	var braveResp braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("响应解析失败: %v", err)}, nil
	}

	// Format results
	results := braveResp.Web.Results
	if len(results) == 0 {
		return tool.ToolResult{Output: "未找到相关结果。"}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 找到 %d 条结果：\n\n", len(results)))
	for i, r := range results {
		desc := r.Description
		runes := []rune(desc)
		if len(runes) > 300 {
			desc = string(runes[:300]) + "..."
		}
		sb.WriteString(fmt.Sprintf("[%d] %s\n    %s\n    %s\n\n", i+1, r.Title, r.URL, desc))
	}

	return tool.ToolResult{Output: sb.String()}, nil
}
