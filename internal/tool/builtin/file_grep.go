package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	grepTimeout         = 15 * time.Second
	grepDefaultMax      = 50
	grepHardMax         = 200
	grepMaxLineLen      = 200 // truncate long lines to keep output tidy
	grepMaxContextLines = 3
)

// ── file_grep ──

type FileGrepTool struct {
	workspaceDir string
}

func NewFileGrepTool(workspaceDir string) *FileGrepTool {
	return &FileGrepTool{workspaceDir: workspaceDir}
}

func (t *FileGrepTool) Name() string { return "file_grep" }
func (t *FileGrepTool) Description() string {
	return "在工作区内按正则或字面量模式搜索文件内容，返回文件路径、行号和匹配行。支持文件名过滤和上下文行显示。"
}

func (t *FileGrepTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "pattern", Type: "string", Description: "搜索模式（支持正则表达式）", Required: true},
		tool.SchemaParam{Name: "path", Type: "string", Description: "搜索目录或文件，默认工作区根目录", Required: false},
		tool.SchemaParam{Name: "case_sensitive", Type: "boolean", Description: "是否大小写敏感（默认 false）", Required: false},
		tool.SchemaParam{Name: "file_glob", Type: "string", Description: "文件名过滤，如 *.go 或 *.{ts,tsx}", Required: false},
		tool.SchemaParam{Name: "context_lines", Type: "integer", Description: "匹配行前后各显示 N 行（默认 0，上限 3）", Required: false},
		tool.SchemaParam{Name: "max_results", Type: "integer", Description: "最大返回条数（默认 50，上限 200）", Required: false},
	)
}

func (t *FileGrepTool) Init(_ context.Context) error { return nil }
func (t *FileGrepTool) Close() error                 { return nil }

type fileGrepArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	CaseSensitive bool   `json:"case_sensitive"`
	FileGlob      string `json:"file_glob"`
	ContextLines  int    `json:"context_lines"`
	MaxResults    int    `json:"max_results"`
}

type grepMatch struct {
	File        string
	LineNum     int    // 1-based
	Line        string // the matched line
	BeforeStart int    // 1-based line number of first before-context line
	Before      []string
	After       []string // starts at LineNum+1
}

func (t *FileGrepTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a fileGrepArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	if strings.TrimSpace(a.Pattern) == "" {
		return tool.ToolResult{Error: "pattern 不能为空"}, nil
	}

	// Clamp context_lines and max_results
	contextLines := clamp(a.ContextLines, 0, grepMaxContextLines)
	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = grepDefaultMax
	}
	if maxResults > grepHardMax {
		maxResults = grepHardMax
	}

	// Compile regexp
	re, err := buildGrepRegexp(a.Pattern, a.CaseSensitive)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("正则表达式错误: %v", err)}, nil
	}

	// Resolve search root
	searchRoot := t.workspaceDir
	if a.Path != "" {
		resolved, err := safeResolvePath(a.Path, t.workspaceDir)
		if err != nil {
			return tool.ToolResult{Error: err.Error()}, nil
		}
		searchRoot = resolved
	}

	// Apply timeout to the walk
	walkCtx, cancel := context.WithTimeout(ctx, grepTimeout)
	defer cancel()

	// Verify the search root exists before starting the walk;
	// WalkDir would silently return no results for a non-existent path.
	if _, err := os.Stat(searchRoot); err != nil {
		if os.IsNotExist(err) {
			return tool.ToolResult{Error: fmt.Sprintf("搜索路径不存在: %s — 请先用 file_list 确认路径", a.Path)}, nil
		}
		return tool.ToolResult{Error: fmt.Sprintf("无法访问搜索路径: %v", err)}, nil
	}

	var matches []grepMatch
	limitReached := false

	_ = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		select {
		case <-walkCtx.Done():
			return walkCtx.Err()
		default:
		}

		if err != nil {
			return nil // skip inaccessible paths
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// File glob filter
		if a.FileGlob != "" {
			matched, _ := matchFileGlob(a.FileGlob, d.Name())
			if !matched {
				return nil
			}
		}

		fileMatches, err := searchInFile(walkCtx, path, re, contextLines)
		if err != nil {
			return nil // skip files that can't be read
		}
		for _, m := range fileMatches {
			if len(matches) >= maxResults {
				limitReached = true
				return fmt.Errorf("limit reached")
			}
			matches = append(matches, m)
		}
		return nil
	})

	if len(matches) == 0 {
		return tool.ToolResult{Output: "未找到匹配内容"}, nil
	}

	output := formatGrepResults(matches, t.workspaceDir, limitReached, maxResults)
	return tool.ToolResult{Output: output}, nil
}

// buildGrepRegexp compiles the search pattern.
// Go's regexp package uses the RE2 engine which guarantees linear-time
// execution, so ReDoS is not a concern and no special guard is needed.
func buildGrepRegexp(pattern string, caseSensitive bool) (*regexp.Regexp, error) {
	prefix := "(?i)"
	if caseSensitive {
		prefix = ""
	}
	return regexp.Compile(prefix + pattern)
}

// matchFileGlob supports simple glob patterns and brace expansion like *.{ts,tsx}.
func matchFileGlob(pattern, name string) (bool, error) {
	if strings.Contains(pattern, "{") && strings.Contains(pattern, "}") {
		start := strings.Index(pattern, "{")
		end := strings.Index(pattern, "}")
		if start < end {
			prefix := pattern[:start]
			suffix := pattern[end+1:]
			alternatives := strings.Split(pattern[start+1:end], ",")
			for _, alt := range alternatives {
				m, err := filepath.Match(prefix+strings.TrimSpace(alt)+suffix, name)
				if err != nil {
					return false, err
				}
				if m {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return filepath.Match(pattern, name)
}

// searchInFile reads a file and returns all regex matches with optional context.
// Returns nil without error for binary files or files larger than 10MB (silently skipped).
func searchInFile(ctx context.Context, path string, re *regexp.Regexp, contextLines int) ([]grepMatch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Skip files larger than 10MB to prevent OOM on huge log files
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 10<<20 {
		return nil, nil // silently skip oversized files
	}

	// Binary detection: sample first 512 bytes
	sample := make([]byte, 512)
	n, err := f.Read(sample)
	if err != nil && n == 0 {
		return nil, err
	}
	if isGrepBinary(sample[:n]) {
		return nil, nil // skip binary
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	// Read all lines into memory (needed for context window)
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var matches []grepMatch
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}

		m := grepMatch{
			File:    path,
			LineNum: i + 1,
			Line:    truncateLine(line, grepMaxLineLen),
		}

		// Before context
		if contextLines > 0 {
			beforeStart := i - contextLines
			if beforeStart < 0 {
				beforeStart = 0
			}
			m.BeforeStart = beforeStart + 1
			for j := beforeStart; j < i; j++ {
				m.Before = append(m.Before, truncateLine(lines[j], grepMaxLineLen))
			}
		}

		// After context
		if contextLines > 0 {
			end := i + contextLines + 1
			if end > len(lines) {
				end = len(lines)
			}
			for j := i + 1; j < end; j++ {
				m.After = append(m.After, truncateLine(lines[j], grepMaxLineLen))
			}
		}

		matches = append(matches, m)
	}
	return matches, nil
}

// isGrepBinary returns true when the byte slice looks like binary content.
func isGrepBinary(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	if utf8.Valid(data) {
		return false
	}
	// Non-UTF-8: count non-printable control bytes
	nonPrintable := 0
	for _, b := range data {
		if b < 0x08 || (b >= 0x0E && b < 0x20 && b != 0x1B) {
			nonPrintable++
		}
	}
	return len(data) > 0 && nonPrintable*10 > len(data)
}

// truncateLine truncates a string to maxLen runes, appending "..." if truncated.
func truncateLine(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// formatGrepResults renders matches in a compact, annotated format.
// Match lines are prefixed with "> "; context lines with "  ".
func formatGrepResults(matches []grepMatch, workspaceDir string, limitReached bool, maxResults int) string {
	var sb strings.Builder
	currentFile := ""
	fileCount := 0
	totalMatches := 0

	for _, m := range matches {
		relFile := m.File
		if rel, err := filepath.Rel(workspaceDir, m.File); err == nil {
			relFile = rel
		}

		if relFile != currentFile {
			if currentFile != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("文件: %s\n", relFile))
			currentFile = relFile
			fileCount++
		}

		// Before-context lines
		for i, line := range m.Before {
			sb.WriteString(fmt.Sprintf("  行 %d:   %s\n", m.BeforeStart+i, line))
		}
		// Match line (marked with >)
		sb.WriteString(fmt.Sprintf("  行 %d: > %s\n", m.LineNum, m.Line))
		// After-context lines
		for i, line := range m.After {
			sb.WriteString(fmt.Sprintf("  行 %d:   %s\n", m.LineNum+1+i, line))
		}

		totalMatches++
	}

	suffix := ""
	if limitReached {
		suffix = fmt.Sprintf("（已达上限 %d 条）", maxResults)
	}
	sb.WriteString(fmt.Sprintf("---\n共 %d 个文件，%d 处匹配%s（`>` 标记匹配行，其余为上下文）", fileCount, totalMatches, suffix))

	return sb.String()
}

// clamp returns v clamped to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
