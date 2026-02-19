package builtin

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// searchDescMaxRunes is the maximum number of runes to show per result description.
	searchDescMaxRunes = 300
	// searchQueryMaxRunes is the maximum length of a search query string.
	// Prevents abnormally large HTTP requests from being sent to search APIs.
	searchQueryMaxRunes = 1000
)

// searchResult is a single result entry shared between search tools.
type searchResult struct {
	Title       string
	URL         string
	Description string
}

// parseSearchQuery parses a JSON args blob and returns the trimmed query string.
// Returns an error if the JSON is malformed, the query is empty/whitespace,
// or the query exceeds searchQueryMaxRunes characters.
func parseSearchQuery(args json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("参数解析失败: %v", err)
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		return "", fmt.Errorf("搜索关键词不能为空")
	}
	if len([]rune(q)) > searchQueryMaxRunes {
		return "", fmt.Errorf("搜索关键词过长（最多 %d 字符）", searchQueryMaxRunes)
	}
	return q, nil
}

// truncateRunes truncates s to at most maxRunes Unicode code points,
// appending "..." if truncation occurred.
// If maxRunes <= 0, s is returned unchanged.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// formatSearchResults formats a slice of searchResult into a human-readable string.
func formatSearchResults(results []searchResult) string {
	if len(results) == 0 {
		return "未找到相关结果。"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条结果：\n\n", len(results)))
	for i, r := range results {
		desc := truncateRunes(r.Description, searchDescMaxRunes)
		sb.WriteString(fmt.Sprintf("[%d] %s\n    %s\n    %s\n\n", i+1, r.Title, r.URL, desc))
	}
	return sb.String()
}
