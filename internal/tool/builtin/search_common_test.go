package builtin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSearchQuery_Valid(t *testing.T) {
	query, err := parseSearchQuery([]byte(`{"query":"golang testing"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "golang testing" {
		t.Errorf("got %q, want %q", query, "golang testing")
	}
}

func TestParseSearchQuery_TrimsWhitespace(t *testing.T) {
	query, err := parseSearchQuery([]byte(`{"query":"  hello  "}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "hello" {
		t.Errorf("got %q, want %q (should be trimmed)", query, "hello")
	}
}

func TestParseSearchQuery_Empty(t *testing.T) {
	_, err := parseSearchQuery([]byte(`{"query":""}`))
	if err == nil {
		t.Error("expected error for empty query")
	}
	if !strings.Contains(err.Error(), "不能为空") {
		t.Errorf("error %q should mention empty", err.Error())
	}
}

func TestParseSearchQuery_WhitespaceOnly(t *testing.T) {
	_, err := parseSearchQuery([]byte(`{"query":"   "}`))
	if err == nil {
		t.Error("expected error for whitespace-only query")
	}
	if !strings.Contains(err.Error(), "不能为空") {
		t.Errorf("error %q should mention empty", err.Error())
	}
}

func TestParseSearchQuery_BadJSON(t *testing.T) {
	_, err := parseSearchQuery([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "参数解析失败") {
		t.Errorf("error %q should mention parse failure", err.Error())
	}
}

// ── truncateRunes ─────────────────────────────────────────────────────────────

func TestTruncateRunes_NoTruncation(t *testing.T) {
	s := "hello"
	got := truncateRunes(s, 10)
	if got != s {
		t.Errorf("should not truncate: got %q, want %q", got, s)
	}
}

func TestTruncateRunes_ExactLimit(t *testing.T) {
	s := "hello"
	got := truncateRunes(s, 5)
	if got != s {
		t.Errorf("at exact limit should not truncate: got %q, want %q", got, s)
	}
}

func TestTruncateRunes_Truncated(t *testing.T) {
	got := truncateRunes("hello world", 5)
	if !strings.HasPrefix(got, "hello") {
		t.Errorf("got %q, should start with 'hello'", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("got %q, should end with '...'", got)
	}
}

func TestTruncateRunes_Chinese(t *testing.T) {
	got := truncateRunes("你好世界测试", 4)
	prefix := strings.TrimSuffix(got, "...")
	if len([]rune(prefix)) != 4 {
		t.Errorf("prefix should have 4 runes, got %d in %q", len([]rune(prefix)), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("got %q, should end with '...'", got)
	}
}

func TestTruncateRunes_EmptyString(t *testing.T) {
	got := truncateRunes("", 5)
	if got != "" {
		t.Errorf("empty string should return empty, got %q", got)
	}
}

// ── formatSearchResults ───────────────────────────────────────────────────────

func TestFormatSearchResults_Empty(t *testing.T) {
	got := formatSearchResults([]searchResult{})
	if !strings.Contains(got, "未找到") {
		t.Errorf("got %q, should mention no results", got)
	}
}

func TestFormatSearchResults_WithResults(t *testing.T) {
	results := []searchResult{
		{Title: "标题一", URL: "https://example.com/1", Description: "描述一"},
		{Title: "标题二", URL: "https://example.com/2", Description: "描述二"},
	}
	got := formatSearchResults(results)
	if !strings.Contains(got, "标题一") {
		t.Error("should contain first result title")
	}
	if !strings.Contains(got, "标题二") {
		t.Error("should contain second result title")
	}
	if !strings.Contains(got, "https://example.com/1") {
		t.Error("should contain first result URL")
	}
	if !strings.Contains(got, "找到 2 条结果") {
		t.Errorf("should mention result count, got: %q", got)
	}
}

func TestFormatSearchResults_Numbered(t *testing.T) {
	results := []searchResult{
		{Title: "A", URL: "https://a.com", Description: "a desc"},
		{Title: "B", URL: "https://b.com", Description: "b desc"},
		{Title: "C", URL: "https://c.com", Description: "c desc"},
	}
	got := formatSearchResults(results)
	if !strings.Contains(got, "[1]") || !strings.Contains(got, "[2]") || !strings.Contains(got, "[3]") {
		t.Errorf("results should be numbered, got: %q", got)
	}
}

func TestFormatSearchResults_TruncatesLongDescription(t *testing.T) {
	// Use a character that does not appear in any format string or URL so that
	// strings.Count gives an exact measure of the description portion only.
	longDesc := strings.Repeat("喵", 400)
	results := []searchResult{
		{Title: "标题", URL: "https://go.dev", Description: longDesc},
	}
	got := formatSearchResults(results)
	if !strings.Contains(got, "...") {
		t.Error("long description should be truncated with '...'")
	}
	// The '喵' rune count in output must not exceed the truncation limit.
	if strings.Count(got, "喵") > searchDescMaxRunes {
		t.Errorf("description not properly truncated to %d runes", searchDescMaxRunes)
	}
}

// ── parseSearchQuery length limit ─────────────────────────────────────────────

func TestParseSearchQuery_TooLong(t *testing.T) {
	// Build a query exceeding searchQueryMaxRunes.
	longQuery := strings.Repeat("搜", searchQueryMaxRunes+1)
	args, _ := json.Marshal(map[string]string{"query": longQuery})
	_, err := parseSearchQuery(args)
	if err == nil {
		t.Error("expected error for query exceeding max length")
	}
	if !strings.Contains(err.Error(), "过长") {
		t.Errorf("error %q should mention length limit", err.Error())
	}
}

func TestParseSearchQuery_AtLimit(t *testing.T) {
	// Exactly at the limit should succeed.
	query := strings.Repeat("a", searchQueryMaxRunes)
	args, _ := json.Marshal(map[string]string{"query": query})
	got, err := parseSearchQuery(args)
	if err != nil {
		t.Fatalf("unexpected error at limit: %v", err)
	}
	if len([]rune(got)) != searchQueryMaxRunes {
		t.Errorf("expected %d runes, got %d", searchQueryMaxRunes, len([]rune(got)))
	}
}

// ── truncateRunes boundary cases ──────────────────────────────────────────────

func TestTruncateRunes_ZeroLimit(t *testing.T) {
	// maxRunes <= 0 should return s unchanged (no panic).
	s := "hello"
	if got := truncateRunes(s, 0); got != s {
		t.Errorf("truncateRunes(s, 0) = %q, want %q (unchanged)", got, s)
	}
}

func TestTruncateRunes_NegativeLimit(t *testing.T) {
	// Negative maxRunes should return s unchanged (no panic).
	s := "hello"
	if got := truncateRunes(s, -5); got != s {
		t.Errorf("truncateRunes(s, -5) = %q, want %q (unchanged)", got, s)
	}
}
