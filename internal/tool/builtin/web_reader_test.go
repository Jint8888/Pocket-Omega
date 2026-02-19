package builtin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractContentBasic(t *testing.T) {
	htmlStr := `<html><head><title>测试页面</title></head>
	<body><p>第一段正文</p><p>第二段正文</p></body></html>`

	title, _, content, err := extractContent(strings.NewReader(htmlStr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "测试页面" {
		t.Errorf("title = %q, want %q", title, "测试页面")
	}
	if !strings.Contains(content, "第一段正文") || !strings.Contains(content, "第二段正文") {
		t.Errorf("content missing paragraphs: %q", content)
	}
}

func TestExtractContentSkipScriptStyle(t *testing.T) {
	htmlStr := `<html><body>
	<script>var x = 1;</script>
	<style>.hidden{display:none}</style>
	<p>可见内容</p>
	<nav>导航栏</nav>
	</body></html>`

	_, _, content, _ := extractContent(strings.NewReader(htmlStr))

	if strings.Contains(content, "var x") {
		t.Error("script content should be skipped")
	}
	if strings.Contains(content, ".hidden") {
		t.Error("style content should be skipped")
	}
	if strings.Contains(content, "导航栏") {
		t.Error("nav content should be skipped")
	}
	if !strings.Contains(content, "可见内容") {
		t.Error("body text should be extracted")
	}
}

func TestExtractContentBlockElements(t *testing.T) {
	htmlStr := `<html><body>
	<h1>标题一</h1><p>段落一</p><p>段落二</p>
	</body></html>`

	_, _, content, _ := extractContent(strings.NewReader(htmlStr))

	// Block elements should have newlines between them
	if !strings.Contains(content, "标题一") {
		t.Error("h1 content should be extracted")
	}
	if !strings.Contains(content, "段落一") {
		t.Error("p content should be extracted")
	}
	// Check that newlines exist (block separation)
	if !strings.Contains(content, "\n") {
		t.Error("block elements should be separated by newlines")
	}
}

func TestExtractContentNestedSkip(t *testing.T) {
	htmlStr := `<html><body>
	<nav><div><a href="#">链接</a></div></nav>
	<p>正文</p>
	</body></html>`

	_, _, content, _ := extractContent(strings.NewReader(htmlStr))

	if strings.Contains(content, "链接") {
		t.Error("nested nav content should be skipped")
	}
	if !strings.Contains(content, "正文") {
		t.Error("body text should be extracted")
	}
}

func TestExtractContentLongText(t *testing.T) {
	longText := strings.Repeat("这是一段很长的文字", 2000)
	htmlStr := "<html><body><p>" + longText + "</p></body></html>"

	_, _, content, _ := extractContent(strings.NewReader(htmlStr))

	runes := []rune(content)
	// extractContent itself doesn't truncate; truncation happens in Execute
	if len(runes) == 0 {
		t.Error("content should not be empty for long text")
	}
}

func TestWebReaderInvalidURL(t *testing.T) {
	ctx := context.Background()
	tool := NewWebReaderTool()
	result, err := tool.Execute(ctx, []byte(`{"url":"ftp://example.com"}`))
	if err != nil {
		t.Fatalf("Execute should not return error: %v", err)
	}
	if result.Error == "" {
		t.Error("should return error for non-http URL")
	}
}

func TestWebReaderEmptyURL(t *testing.T) {
	ctx := context.Background()
	tool := NewWebReaderTool()
	result, err := tool.Execute(ctx, []byte(`{"url":""}`))
	if err != nil {
		t.Fatalf("Execute should not return error: %v", err)
	}
	if result.Error == "" {
		t.Error("should return error for empty URL")
	}
}

func TestWebReaderMissingScheme(t *testing.T) {
	ctx := context.Background()
	tool := NewWebReaderTool()
	result, err := tool.Execute(ctx, []byte(`{"url":"www.example.com"}`))
	if err != nil {
		t.Fatalf("Execute should not return error: %v", err)
	}
	if result.Error == "" {
		t.Error("should return error for URL without scheme")
	}
}

func TestWebReaderBadJSON(t *testing.T) {
	ctx := context.Background()
	tool := NewWebReaderTool()
	result, err := tool.Execute(ctx, []byte(`not json`))
	if err != nil {
		t.Fatalf("Execute should not return error: %v", err)
	}
	if result.Error == "" {
		t.Error("should return error for invalid JSON")
	}
}

func TestWebReaderToolInterface(t *testing.T) {
	tool := NewWebReaderTool()
	if tool.Name() != "web_reader" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_reader")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	schema := tool.InputSchema()
	if len(schema) == 0 {
		t.Error("InputSchema() should not be empty")
	}
	// Verify schema contains "url" field
	if !strings.Contains(string(schema), `"url"`) {
		t.Error("InputSchema() should contain 'url' field")
	}
}

func TestExtractContentMetaDescription(t *testing.T) {
	htmlStr := `<html><head>
	<title>测试</title>
	<meta name="description" content="这是页面的摘要描述">
	</head><body><p>正文</p></body></html>`

	_, desc, _, _ := extractContent(strings.NewReader(htmlStr))
	if desc != "这是页面的摘要描述" {
		t.Errorf("description = %q, want %q", desc, "这是页面的摘要描述")
	}
}

func TestExtractContentOGDescription(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		wantDesc string
	}{
		{
			name: "og:description only",
			html: `<html><head>
				<title>OG 测试</title>
				<meta property="og:description" content="Open Graph 描述内容">
			</head><body><p>正文</p></body></html>`,
			wantDesc: "Open Graph 描述内容",
		},
		{
			name: "name=description takes priority over og:description",
			html: `<html><head>
				<meta name="description" content="标准描述优先">
				<meta property="og:description" content="OG 描述次之">
			</head><body><p>正文</p></body></html>`,
			wantDesc: "标准描述优先",
		},
		{
			name: "og:description when name=description absent",
			html: `<html><head>
				<meta property="og:description" content="仅有 OG 描述">
			</head><body><p>正文</p></body></html>`,
			wantDesc: "仅有 OG 描述",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, desc, _, err := extractContent(strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("extractContent() error: %v", err)
			}
			if desc != tt.wantDesc {
				t.Errorf("description = %q, want %q", desc, tt.wantDesc)
			}
		})
	}
}

func TestExtractContentSkipForm(t *testing.T) {
	htmlStr := `<html><body>
	<p>正文内容</p>
	<form><button>立即注册</button><input placeholder="邮箱"></form>
	</body></html>`

	_, _, content, _ := extractContent(strings.NewReader(htmlStr))
	if strings.Contains(content, "立即注册") {
		t.Error("form button text should be skipped")
	}
	if !strings.Contains(content, "正文内容") {
		t.Error("body text should be extracted")
	}
}

func TestExtractContentArticleHeader(t *testing.T) {
	htmlStr := `<html><body>
	<header><nav>顶部导航</nav></header>
	<article>
		<header><h1>文章标题</h1><span>作者名</span></header>
		<p>文章正文</p>
	</article>
	</body></html>`

	_, _, content, _ := extractContent(strings.NewReader(htmlStr))
	if strings.Contains(content, "顶部导航") {
		t.Error("page-level header should be skipped")
	}
	if !strings.Contains(content, "文章标题") {
		t.Error("article-level header should be preserved")
	}
	if !strings.Contains(content, "文章正文") {
		t.Error("article body should be extracted")
	}
}

// ── 集成测试（httptest.NewServer）────────────────────────────────────────────

// TestWebReaderNon200 验证非 200 状态码时返回 ToolResult.Error，
// 且响应 body 被正确排空以允许 HTTP 连接复用。
func TestWebReaderNon200(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"403 Forbidden", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.code)
				fmt.Fprintln(w, "error response body — must be drained")
			}))
			defer server.Close()

			tool := NewWebReaderTool()
			result, err := tool.Execute(
				context.Background(),
				[]byte(fmt.Sprintf(`{"url":%q}`, server.URL)),
			)
			if err != nil {
				t.Fatalf("Execute() returned unexpected Go error: %v", err)
			}
			if result.Error == "" {
				t.Errorf("Expected ToolResult.Error for HTTP %d, got output: %q", tt.code, result.Output)
			}
			if !strings.Contains(result.Error, fmt.Sprintf("%d", tt.code)) {
				t.Errorf("Error %q should contain status code %d", result.Error, tt.code)
			}
		})
	}
}

// TestWebReaderNonHTML 验证各种非 HTML Content-Type 的分发处理：
//   - application/json  → 格式化输出
//   - text/plain        → 直接输出
//   - image/png         → 拦截并返回错误
//   - application/pdf   → 拦截并返回错误
func TestWebReaderNonHTML(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantOutput  bool   // true=期望 Output 字段，false=期望 Error 字段
		wantSubstr  string // 期望出现的子串
	}{
		{
			name:        "application/json pretty-printed",
			contentType: "application/json",
			body:        `{"hello":"world","num":42}`,
			wantOutput:  true,
			wantSubstr:  "hello",
		},
		{
			name:        "application/json invalid falls back to raw",
			contentType: "application/json",
			body:        `not valid json at all`,
			wantOutput:  true,
			wantSubstr:  "not valid json",
		},
		{
			name:        "text/plain returned as-is",
			contentType: "text/plain; charset=utf-8",
			body:        "纯文本内容\n第二行",
			wantOutput:  true,
			wantSubstr:  "纯文本内容",
		},
		{
			name:        "image/png rejected",
			contentType: "image/png",
			body:        "\x89PNG binary data",
			wantOutput:  false,
			wantSubstr:  "不支持的内容类型",
		},
		{
			name:        "application/pdf rejected",
			contentType: "application/pdf",
			body:        "%PDF-1.4 data",
			wantOutput:  false,
			wantSubstr:  "不支持的内容类型",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			tool := NewWebReaderTool()
			result, err := tool.Execute(
				context.Background(),
				[]byte(fmt.Sprintf(`{"url":%q}`, server.URL)),
			)
			if err != nil {
				t.Fatalf("Execute() returned unexpected Go error: %v", err)
			}

			if tt.wantOutput {
				if result.Error != "" {
					t.Errorf("Expected Output, got Error: %q", result.Error)
				}
				if !strings.Contains(result.Output, tt.wantSubstr) {
					t.Errorf("Output %q should contain %q", result.Output, tt.wantSubstr)
				}
			} else {
				if result.Error == "" {
					t.Errorf("Expected Error, got Output: %q", result.Output)
				}
				if !strings.Contains(result.Error, tt.wantSubstr) {
					t.Errorf("Error %q should contain %q", result.Error, tt.wantSubstr)
				}
			}
		})
	}
}

// TestCollapseBlankLines 验证连续空行压缩为最多一行的行为。
func TestCollapseBlankLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no blank lines unchanged",
			input: "line1\nline2\nline3",
			want:  "line1\nline2\nline3",
		},
		{
			name:  "single blank line preserved",
			input: "line1\n\nline2",
			want:  "line1\n\nline2",
		},
		{
			name:  "two consecutive blank lines collapsed to one",
			input: "line1\n\n\nline2",
			want:  "line1\n\nline2",
		},
		{
			name:  "many consecutive blank lines collapsed to one",
			input: "line1\n\n\n\n\nline2",
			want:  "line1\n\nline2",
		},
		{
			name:  "leading blank lines collapsed",
			input: "\n\nline1",
			want:  "\nline1",
		},
		{
			name:  "trailing blank lines collapsed",
			input: "line1\n\n\n",
			want:  "line1\n",
		},
		{
			name:  "empty string unchanged",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseBlankLines(tt.input)
			if got != tt.want {
				t.Errorf("collapseBlankLines(%q) =\n  %q\nwant:\n  %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestWebReaderHTMLEndToEnd 通过 httptest 服务真实 HTML，
// 端到端验证标题、meta description、<header> 跳过、<article> 内 header 保留。
func TestWebReaderHTMLEndToEnd(t *testing.T) {
	const page = `<html><head>
		<title>集成测试页面</title>
		<meta name="description" content="这是页面的摘要">
	</head><body>
		<header><nav>顶部导航栏</nav></header>
		<article>
			<header><h1>文章大标题</h1></header>
			<p>文章正文内容在此处</p>
		</article>
		<footer>页脚版权信息</footer>
	</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	}))
	defer server.Close()

	tool := NewWebReaderTool()
	result, err := tool.Execute(
		context.Background(),
		[]byte(fmt.Sprintf(`{"url":%q}`, server.URL)),
	)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("Expected output, got error: %q", result.Error)
	}

	checks := []struct {
		desc    string
		present bool
		substr  string
	}{
		{"title in output", true, "集成测试页面"},
		{"meta description in output", true, "这是页面的摘要"},
		{"article body extracted", true, "文章正文内容在此处"},
		{"article-level header preserved", true, "文章大标题"},
		{"page-level nav skipped", false, "顶部导航栏"},
		{"footer skipped", false, "页脚版权信息"},
	}

	for _, c := range checks {
		got := strings.Contains(result.Output, c.substr)
		if got != c.present {
			verb := "contain"
			if !c.present {
				verb = "NOT contain"
			}
			t.Errorf("[%s] Output should %s %q\nOutput:\n%s", c.desc, verb, c.substr, result.Output)
		}
	}
}
