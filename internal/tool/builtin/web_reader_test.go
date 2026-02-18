package builtin

import (
	"context"
	"strings"
	"testing"
)

func TestExtractContentBasic(t *testing.T) {
	htmlStr := `<html><head><title>测试页面</title></head>
	<body><p>第一段正文</p><p>第二段正文</p></body></html>`

	title, content, err := extractContent(strings.NewReader(htmlStr))
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

	_, content, _ := extractContent(strings.NewReader(htmlStr))

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

	_, content, _ := extractContent(strings.NewReader(htmlStr))

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

	_, content, _ := extractContent(strings.NewReader(htmlStr))

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

	_, content, _ := extractContent(strings.NewReader(htmlStr))

	runes := []rune(content)
	// extractContent itself doesn't truncate; truncation happens in Execute
	if len(runes) == 0 {
		t.Error("content should not be empty for long text")
	}
}

func TestExtractCharset(t *testing.T) {
	tests := []struct {
		contentType string
		want        string
	}{
		{"text/html; charset=gbk", "gbk"},
		{"text/html; charset=GB2312", "gb2312"},
		{"text/html; charset=utf-8", "utf-8"},
		{"text/html; charset=Shift_JIS", "shift_jis"},
		{"text/html", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractCharset(tt.contentType)
		if got != tt.want {
			t.Errorf("extractCharset(%q) = %q, want %q", tt.contentType, got, tt.want)
		}
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
