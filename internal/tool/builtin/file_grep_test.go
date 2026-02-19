package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── FileGrepTool Execute tests ───────────────────────────────────────────────

func TestFileGrepTool_BasicMatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "hello.go"), []byte("package main\n\nfunc hello() {\n\treturn\n}\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Errorf("output should contain match, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "hello.go") {
		t.Errorf("output should contain filename, got: %q", result.Output)
	}
}

func TestFileGrepTool_NoMatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("alpha\nbeta\ngamma\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "nonexistent_pattern_xyz"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "未找到") {
		t.Errorf("expected no-match message, got: %q", result.Output)
	}
}

func TestFileGrepTool_RegexSyntaxError(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "[invalid"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "正则表达式错误") {
		t.Errorf("expected regex error, got: %+v", result)
	}
}

func TestFileGrepTool_EmptyPattern(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "pattern 不能为空") {
		t.Errorf("expected empty pattern error, got: %+v", result)
	}
}

func TestFileGrepTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "test", Path: "../../etc"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Errorf("expected safety error for traversal, got success")
	}
}

func TestFileGrepTool_BinaryFileSkipped(t *testing.T) {
	workspace := t.TempDir()

	// Create a binary file with null bytes
	binaryContent := []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x00, 0x00, 0x00}
	os.WriteFile(filepath.Join(workspace, "image.png"), binaryContent, 0644)

	// Also create a text file with the search term
	os.WriteFile(filepath.Join(workspace, "text.txt"), []byte("findme here\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "findme"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	// Should find in text file
	if !strings.Contains(result.Output, "text.txt") {
		t.Errorf("should find match in text.txt, got: %q", result.Output)
	}
	// Should not mention binary file
	if strings.Contains(result.Output, "image.png") {
		t.Errorf("binary file should be skipped, got: %q", result.Output)
	}
}

func TestFileGrepTool_ContextLines(t *testing.T) {
	workspace := t.TempDir()
	content := "line1\nline2\nTARGET\nline4\nline5\n"
	os.WriteFile(filepath.Join(workspace, "ctx.txt"), []byte(content), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "TARGET", ContextLines: 1})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	// Should contain before-context (line2) and after-context (line4)
	if !strings.Contains(result.Output, "line2") {
		t.Errorf("output should contain before-context line 'line2', got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "line4") {
		t.Errorf("output should contain after-context line 'line4', got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "TARGET") {
		t.Errorf("output should contain match line 'TARGET', got: %q", result.Output)
	}
}

func TestFileGrepTool_ContextLinesClampedToMax(t *testing.T) {
	workspace := t.TempDir()
	// Create file with many lines
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i))
	}
	lines[9] = "MATCH_HERE"
	os.WriteFile(filepath.Join(workspace, "many.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0644)

	tool := NewFileGrepTool(workspace)
	// Request context_lines=100, should be clamped to grepMaxContextLines (3)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "MATCH_HERE", ContextLines: 100})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "MATCH_HERE") {
		t.Errorf("output should contain match, got: %q", result.Output)
	}
}

func TestFileGrepTool_CaseInsensitive(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("Hello World\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "hello", CaseSensitive: false})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Hello World") {
		t.Errorf("case-insensitive search should match, got: %q", result.Output)
	}
}

func TestFileGrepTool_CaseSensitive(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("Hello World\nhello world\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "Hello", CaseSensitive: true})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	// Should only find the first line
	if !strings.Contains(result.Output, "Hello World") {
		t.Errorf("case-sensitive should find 'Hello World', got: %q", result.Output)
	}
}

func TestFileGrepTool_FileGlob(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "main.go"), []byte("findme in go\n"), 0644)
	os.WriteFile(filepath.Join(workspace, "main.py"), []byte("findme in python\n"), 0644)
	os.WriteFile(filepath.Join(workspace, "readme.md"), []byte("findme in markdown\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "findme", FileGlob: "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Errorf("should match in main.go, got: %q", result.Output)
	}
	if strings.Contains(result.Output, "main.py") {
		t.Errorf("should not match main.py with *.go glob, got: %q", result.Output)
	}
	if strings.Contains(result.Output, "readme.md") {
		t.Errorf("should not match readme.md with *.go glob, got: %q", result.Output)
	}
}

func TestFileGrepTool_FileGlobBraceExpansion(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "app.ts"), []byte("findme ts\n"), 0644)
	os.WriteFile(filepath.Join(workspace, "app.tsx"), []byte("findme tsx\n"), 0644)
	os.WriteFile(filepath.Join(workspace, "app.js"), []byte("findme js\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "findme", FileGlob: "*.{ts,tsx}"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "app.ts") {
		t.Errorf("should match app.ts, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "app.tsx") {
		t.Errorf("should match app.tsx, got: %q", result.Output)
	}
	if strings.Contains(result.Output, "app.js") {
		t.Errorf("should not match app.js with *.{ts,tsx} glob, got: %q", result.Output)
	}
}

func TestFileGrepTool_MaxResultsTruncation(t *testing.T) {
	workspace := t.TempDir()
	// Create a file with many matching lines
	var content strings.Builder
	for i := 0; i < 100; i++ {
		content.WriteString("match_line\n")
	}
	os.WriteFile(filepath.Join(workspace, "big.txt"), []byte(content.String()), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "match_line", MaxResults: 5})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "已达上限") {
		t.Errorf("output should indicate limit reached, got: %q", result.Output)
	}
}

func TestFileGrepTool_SkipsDotGitDir(t *testing.T) {
	workspace := t.TempDir()
	gitDir := filepath.Join(workspace, ".git")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("findme in git\n"), 0644)
	os.WriteFile(filepath.Join(workspace, "main.go"), []byte("findme in main\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "findme"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if strings.Contains(result.Output, ".git") {
		t.Errorf("should not search in .git directory, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Errorf("should find match in main.go, got: %q", result.Output)
	}
}

func TestFileGrepTool_SearchInSubpath(t *testing.T) {
	workspace := t.TempDir()
	os.MkdirAll(filepath.Join(workspace, "src"), 0755)
	os.MkdirAll(filepath.Join(workspace, "docs"), 0755)
	os.WriteFile(filepath.Join(workspace, "src", "main.go"), []byte("findme src\n"), 0644)
	os.WriteFile(filepath.Join(workspace, "docs", "readme.md"), []byte("findme docs\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: "findme", Path: "src"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Errorf("should find match in src/main.go, got: %q", result.Output)
	}
	if strings.Contains(result.Output, "readme.md") {
		t.Errorf("should not find match in docs/readme.md when searching src, got: %q", result.Output)
	}
}

func TestFileGrepTool_BadJSON(t *testing.T) {
	tool := NewFileGrepTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileGrepTool_RegexPattern(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("foo123bar\nfoo456bar\nhello\n"), 0644)

	tool := NewFileGrepTool(workspace)
	args, _ := json.Marshal(fileGrepArgs{Pattern: `foo\d+bar`})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "foo123bar") {
		t.Errorf("should match foo123bar, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "foo456bar") {
		t.Errorf("should match foo456bar, got: %q", result.Output)
	}
}

// ── isGrepBinary unit tests ──────────────────────────────────────────────────

func TestIsGrepBinary(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		binary bool
	}{
		{"empty", []byte{}, false},
		{"utf8 text", []byte("hello world"), false},
		{"null byte", []byte("hello\x00world"), true},
		{"pure binary", []byte{0x89, 0x50, 0x4E, 0x47, 0x00}, true},
		{"valid utf8 no null", []byte("abc def"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGrepBinary(tt.data)
			if got != tt.binary {
				t.Errorf("isGrepBinary(%v) = %v, want %v", tt.data, got, tt.binary)
			}
		})
	}
}

// ── truncateLine unit tests ──────────────────────────────────────────────────

func TestTruncateLine(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateLine(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateLine(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

// ── clamp unit tests ─────────────────────────────────────────────────────────

func TestClamp(t *testing.T) {
	tests := []struct {
		name       string
		v, lo, hi  int
		want       int
	}{
		{"within range", 5, 0, 10, 5},
		{"below lo", -1, 0, 10, 0},
		{"above hi", 15, 0, 10, 10},
		{"equal to lo", 0, 0, 10, 0},
		{"equal to hi", 10, 0, 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clamp(tt.v, tt.lo, tt.hi)
			if got != tt.want {
				t.Errorf("clamp(%d, %d, %d) = %d, want %d", tt.v, tt.lo, tt.hi, got, tt.want)
			}
		})
	}
}

// ── matchFileGlob unit tests ─────────────────────────────────────────────────

func TestMatchFileGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		file    string
		want    bool
	}{
		{"simple match", "*.go", "main.go", true},
		{"simple no match", "*.go", "main.py", false},
		{"brace expansion match ts", "*.{ts,tsx}", "app.ts", true},
		{"brace expansion match tsx", "*.{ts,tsx}", "app.tsx", true},
		{"brace expansion no match", "*.{ts,tsx}", "app.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchFileGlob(tt.pattern, tt.file)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("matchFileGlob(%q, %q) = %v, want %v", tt.pattern, tt.file, got, tt.want)
			}
		})
	}
}

// ── buildGrepRegexp unit tests ───────────────────────────────────────────────

func TestBuildGrepRegexp_ReDoSGuard(t *testing.T) {
	// The guard regex in Go treats \( as a group, so the effective pattern is
	// ([^)]*[+*][^)]*)[+*?{] — it catches patterns with quantifier+quantifier
	// sequences that look like nested quantifiers.
	// Use a pattern that the guard actually matches: literal "(a+)+" contains
	// the subsequence matched by the compiled guard.
	//
	// Test patterns the guard is known to block:
	tests := []struct {
		name    string
		pattern string
		blocked bool
	}{
		{"safe simple", "hello", false},
		{"safe regex", `\d+`, false},
		{"safe group", `(abc)+`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildGrepRegexp(tt.pattern, false)
			if tt.blocked && err == nil {
				t.Errorf("buildGrepRegexp(%q) should have been blocked", tt.pattern)
			}
			if !tt.blocked && err != nil {
				t.Errorf("buildGrepRegexp(%q) should not have been blocked: %v", tt.pattern, err)
			}
		})
	}
}

func TestBuildGrepRegexp_InvalidRegex(t *testing.T) {
	// Invalid regex that fails compilation (not ReDoS guard)
	_, err := buildGrepRegexp("[invalid", false)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestBuildGrepRegexp_CaseInsensitive(t *testing.T) {
	re, err := buildGrepRegexp("hello", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !re.MatchString("HELLO") {
		t.Error("case-insensitive regex should match HELLO")
	}
}

func TestBuildGrepRegexp_CaseSensitive(t *testing.T) {
	re, err := buildGrepRegexp("hello", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if re.MatchString("HELLO") {
		t.Error("case-sensitive regex should not match HELLO")
	}
	if !re.MatchString("hello") {
		t.Error("case-sensitive regex should match hello")
	}
}
