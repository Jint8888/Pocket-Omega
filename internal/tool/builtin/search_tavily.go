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
)

// TavilySearchTool provides web search via Tavily API.
type TavilySearchTool struct {
	apiKey string
}

func NewTavilySearchTool(apiKey string) *TavilySearchTool {
	return &TavilySearchTool{apiKey: apiKey}
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

func (t *TavilySearchTool) Init(_ context.Context) error { return nil }
func (t *TavilySearchTool) Close() error                 { return nil }

// tavilyRequest is the Tavily API request body.
type tavilyRequest struct {
	APIKey     string `json:"api_key"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
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

	// Build request
	reqBody := tavilyRequest{
		APIKey:     t.apiKey,
		Query:      query,
		MaxResults: tavilyMaxResults,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求构建失败: %v", err)}, nil
	}

	// HTTP call with timeout
	httpCtx, cancel := context.WithTimeout(ctx, tavilyHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, tavilyAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求创建失败: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("搜索请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return tool.ToolResult{Error: fmt.Sprintf("Tavily API 错误 (HTTP %d): %s", resp.StatusCode, string(body))}, nil
	}

	var tavilyResp tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("响应解析失败: %v", err)}, nil
	}

	// Format results
	var sb strings.Builder
	if tavilyResp.Answer != "" {
		sb.WriteString(fmt.Sprintf("💡 摘要：%s\n\n", tavilyResp.Answer))
	}

	if len(tavilyResp.Results) == 0 {
		sb.WriteString("未找到相关结果。")
	} else {
		sb.WriteString(fmt.Sprintf("🔍 找到 %d 条结果：\n\n", len(tavilyResp.Results)))
		for i, r := range tavilyResp.Results {
			content := r.Content
			// Truncate long content
			runes := []rune(content)
			if len(runes) > 300 {
				content = string(runes[:300]) + "..."
			}
			sb.WriteString(fmt.Sprintf("[%d] %s\n    %s\n    %s\n\n", i+1, r.Title, r.URL, content))
		}
	}

	return tool.ToolResult{Output: sb.String()}, nil
}
