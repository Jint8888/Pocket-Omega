package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	webReaderTimeout      = 15 * time.Second
	webReaderMaxBody      = 2 << 20 // 2MB
	webReaderMaxRunes     = 8000    // 截断到 8000 字符，避免 LLM context 溢出
	webReaderUserAgent    = "PocketOmega/0.2 (Web Reader Bot)"
	webReaderMaxRedirects = 10
)

// httpClient is a dedicated HTTP client for WebReaderTool.
// Safer than http.DefaultClient: explicit timeout + redirect limit.
var httpClient = &http.Client{
	Timeout: webReaderTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= webReaderMaxRedirects {
			return fmt.Errorf("超过最大重定向次数 (%d)", webReaderMaxRedirects)
		}
		return nil
	},
}

// WebReaderTool reads and extracts text content from web pages.
type WebReaderTool struct{}

func NewWebReaderTool() *WebReaderTool { return &WebReaderTool{} }

func (t *WebReaderTool) Name() string { return "web_reader" }
func (t *WebReaderTool) Description() string {
	return "读取指定 URL 的网页正文内容。适用于阅读文章、文档、新闻页面等。返回页面标题和主要文字内容。"
}

func (t *WebReaderTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{
			Name:        "url",
			Type:        "string",
			Description: "要读取的网页 URL（必须以 http:// 或 https:// 开头）",
			Required:    true,
		},
	)
}

func (t *WebReaderTool) Init(_ context.Context) error { return nil }
func (t *WebReaderTool) Close() error                 { return nil }

// Execute fetches the given URL, extracts the page title and main text content.
func (t *WebReaderTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	url := strings.TrimSpace(a.URL)

	// URL format validation
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return tool.ToolResult{Error: "URL 必须以 http:// 或 https:// 开头"}, nil
	}

	// HTTP request using custom client (explicit timeout + redirect limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求创建失败: %v", err)}, nil
	}
	req.Header.Set("User-Agent", webReaderUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := httpClient.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return tool.ToolResult{Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)}, nil
	}

	// Limit body read size
	limitedReader := io.LimitReader(resp.Body, webReaderMaxBody)

	// Auto-detect charset and transcode to UTF-8
	contentType := resp.Header.Get("Content-Type")
	utf8Reader, err := charset.NewReaderLabel(
		extractCharset(contentType), limitedReader,
	)
	if err != nil {
		// Charset conversion failed, fallback to raw reader (assume UTF-8)
		utf8Reader = limitedReader
	}

	// Extract content
	title, content, err := extractContent(utf8Reader)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("内容解析失败: %v", err)}, nil
	}

	// Format output
	var sb strings.Builder
	if title != "" {
		sb.WriteString(fmt.Sprintf("📄 标题：%s\n\n", title))
	}
	if content == "" {
		sb.WriteString("⚠️ 未能提取到正文内容。")
	} else {
		// Truncate to avoid LLM context overflow
		runes := []rune(content)
		if len(runes) > webReaderMaxRunes {
			content = string(runes[:webReaderMaxRunes]) + "\n\n...(内容截断)"
		}
		sb.WriteString(content)
	}

	return tool.ToolResult{Output: sb.String()}, nil
}

// extractCharset extracts the charset value from a Content-Type header.
// Example: "text/html; charset=gbk" → "gbk"
// Returns empty string if no charset found (charset.NewReaderLabel will default to UTF-8).
func extractCharset(contentType string) string {
	for _, part := range strings.Split(contentType, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "charset=") {
			return strings.TrimPrefix(strings.ToLower(part), "charset=")
		}
	}
	return ""
}

// extractContent parses HTML and extracts the <title> and body text.
// It skips non-content elements like <script>, <style>, <nav>, <footer>.
func extractContent(r io.Reader) (title string, content string, err error) {
	tokenizer := html.NewTokenizer(r)

	var sb strings.Builder
	var inTitle, inSkip bool
	skipDepth := 0

	// Tags to skip (non-content areas)
	skipTags := map[string]bool{
		"script": true, "style": true, "noscript": true,
		"nav": true, "footer": true, "header": true,
		"aside": true, "iframe": true, "svg": true,
	}

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			err := tokenizer.Err()
			result := collapseBlankLines(strings.TrimSpace(sb.String()))
			if err == io.EOF {
				return title, result, nil
			}
			return title, result, err

		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			tagName := string(tn)

			if tagName == "title" {
				inTitle = true
			}
			if skipTags[tagName] {
				inSkip = true
				skipDepth++
			}
			// Add newline before block-level elements, but avoid consecutive newlines
			if isBlockElement(tagName) && sb.Len() > 0 {
				s := sb.String()
				if s[len(s)-1] != '\n' {
					sb.WriteString("\n")
				}
			}
			// Add cell separator for table cells
			if (tagName == "td" || tagName == "th") && sb.Len() > 0 {
				s := sb.String()
				if s[len(s)-1] != '\n' && s[len(s)-1] != '|' {
					sb.WriteString(" | ")
				}
			}

		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tagName := string(tn)

			if tagName == "title" {
				inTitle = false
			}
			if skipTags[tagName] && skipDepth > 0 {
				skipDepth--
				if skipDepth == 0 {
					inSkip = false
				}
			}

		case html.TextToken:
			text := strings.TrimSpace(string(tokenizer.Text()))
			if text == "" {
				continue
			}
			if inTitle && title == "" {
				title = text
				continue
			}
			if !inSkip {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
	}
}

// collapseBlankLines reduces 3+ consecutive newlines down to 2 (one blank line).
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	blankCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankCount++
			if blankCount <= 1 {
				result = append(result, line)
			}
		} else {
			blankCount = 0
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// isBlockElement returns true for HTML block-level elements
// that should have line breaks between them.
func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "tr", "br", "hr", "blockquote", "pre",
		"article", "section", "main",
		"table", "thead", "tbody", "tfoot":
		return true
	}
	return false
}
