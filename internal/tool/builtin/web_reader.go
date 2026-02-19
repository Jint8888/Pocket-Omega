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
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("请求失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain body to allow HTTP connection reuse
		io.Copy(io.Discard, resp.Body)
		return tool.ToolResult{Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)}, nil
	}

	// Limit body read size
	limitedReader := io.LimitReader(resp.Body, webReaderMaxBody)

	// Content-Type dispatch: only parse HTML through extractContent
	contentType := resp.Header.Get("Content-Type")
	ctLower := strings.ToLower(contentType)

	// Handle non-HTML content types
	if strings.Contains(ctLower, "application/json") {
		raw, _ := io.ReadAll(limitedReader)
		var prettyBuf bytes.Buffer
		if err := json.Indent(&prettyBuf, raw, "", "  "); err == nil {
			return tool.ToolResult{Output: truncateContent(prettyBuf.String())}, nil
		}
		return tool.ToolResult{Output: truncateContent(string(raw))}, nil
	}
	if strings.Contains(ctLower, "text/plain") {
		raw, _ := io.ReadAll(limitedReader)
		return tool.ToolResult{Output: truncateContent(string(raw))}, nil
	}
	if !strings.Contains(ctLower, "text/html") && !strings.Contains(ctLower, "application/xhtml") {
		// Unsupported content type (PDF, image, etc.)
		return tool.ToolResult{Error: fmt.Sprintf("不支持的内容类型: %s", contentType)}, nil
	}

	// Auto-detect charset and transcode to UTF-8.
	// charset.NewReader sniffs in priority order:
	//   1. BOM in the byte stream
	//   2. <meta charset="..."> / <meta http-equiv="Content-Type" ...> in HTML
	//   3. charset= parameter in the HTTP Content-Type header
	//   4. Falls back to UTF-8
	utf8Reader, err := charset.NewReader(limitedReader, contentType)
	if err != nil {
		// Fallback to raw reader (assumed UTF-8) on detection failure
		utf8Reader = limitedReader
	}

	// Extract content
	title, description, content, err := extractContent(utf8Reader)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("内容解析失败: %v", err)}, nil
	}

	// Format output
	var sb strings.Builder
	if title != "" {
		sb.WriteString(fmt.Sprintf("📄 标题：%s\n\n", title))
	}
	if description != "" {
		sb.WriteString(fmt.Sprintf("📝 摘要：%s\n\n", description))
	}
	if content == "" {
		sb.WriteString("⚠️ 未能提取到正文内容。")
	} else {
		sb.WriteString(truncateContent(content))
	}

	return tool.ToolResult{Output: sb.String()}, nil
}

// truncateContent limits content to webReaderMaxRunes to avoid LLM context overflow.
func truncateContent(content string) string {
	runes := []rune(content)
	if len(runes) > webReaderMaxRunes {
		return string(runes[:webReaderMaxRunes]) + "\n\n...(内容截断)"
	}
	return content
}

// extractContent parses HTML and extracts the <title>, <meta description>, and body text.
// It skips non-content elements like <script>, <style>, <nav>, <footer>, <form>.
// <header> is only skipped at page level (depth 0), preserved inside <article>.
func extractContent(r io.Reader) (title string, description string, content string, err error) {
	tokenizer := html.NewTokenizer(r)

	var sb strings.Builder
	var inTitle, inSkip bool
	skipDepth := 0
	articleDepth := 0 // tracks nesting inside <article>

	// Tags to skip (non-content areas)
	skipTags := map[string]bool{
		"script": true, "style": true, "noscript": true,
		"nav": true, "footer": true, "form": true,
		"aside": true, "iframe": true, "svg": true,
	}

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			parseErr := tokenizer.Err()
			result := collapseBlankLines(strings.TrimSpace(sb.String()))
			if parseErr == io.EOF {
				return title, description, result, nil
			}
			return title, description, result, parseErr

		case html.StartTagToken, html.SelfClosingTagToken:
			tn, hasAttr := tokenizer.TagName()
			tagName := string(tn)

			// Extract <meta name="description"> and <meta property="og:description">
			if tagName == "meta" && hasAttr && description == "" {
				var nameVal, propertyVal, contentVal string
				for {
					key, val, more := tokenizer.TagAttr()
					switch string(key) {
					case "name":
						nameVal = strings.ToLower(string(val))
					case "property":
						propertyVal = strings.ToLower(string(val))
					case "content":
						contentVal = string(val)
					}
					if !more {
						break
					}
				}
				// Prefer standard <meta name="description">; fall back to Open Graph og:description
				if nameVal == "description" && contentVal != "" {
					description = contentVal
				} else if propertyVal == "og:description" && contentVal != "" {
					description = contentVal
				}
				continue
			}

			if tt == html.SelfClosingTagToken {
				continue
			}

			if tagName == "title" {
				inTitle = true
			}
			if tagName == "article" {
				articleDepth++
			}
			// Skip <header> only at page level (not inside <article>)
			if tagName == "header" && articleDepth == 0 {
				inSkip = true
				skipDepth++
			}
			if skipTags[tagName] {
				inSkip = true
				skipDepth++
			}
			// Add newline before block-level elements, but only outside skip zones
			if !inSkip && isBlockElement(tagName) && sb.Len() > 0 {
				s := sb.String()
				if s[len(s)-1] != '\n' {
					sb.WriteString("\n")
				}
			}
			// Add cell separator for table cells (only outside skip zones)
			if !inSkip && (tagName == "td" || tagName == "th") && sb.Len() > 0 {
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
			if tagName == "article" && articleDepth > 0 {
				articleDepth--
			}
			// Match closing for page-level <header>
			isPageHeader := tagName == "header" && articleDepth == 0
			if (skipTags[tagName] || isPageHeader) && skipDepth > 0 {
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

// collapseBlankLines reduces consecutive blank lines down to at most one.
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
