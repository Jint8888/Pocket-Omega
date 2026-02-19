package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── safeResolvePath unit tests ──────────────────────────────────────────────

func TestSafeResolvePathNormal(t *testing.T) {
	workspace := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative file", "hello.txt", false},
		{"nested relative", "sub/dir/file.txt", false},
		{"dot path", "./test.txt", false},
		{"workspace root", ".", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := safeResolvePath(tt.path, workspace)
			if (err != nil) != tt.wantErr {
				t.Errorf("safeResolvePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
				return
			}
			if !tt.wantErr && resolved == "" {
				t.Error("resolved path should not be empty")
			}
		})
	}
}

func TestSafeResolvePathTraversal(t *testing.T) {
	workspace := t.TempDir()

	tests := []struct {
		name string
		path string
	}{
		{"dot-dot traversal", "../../etc/passwd"},
		{"dot-dot absolute", filepath.Join(workspace, "..", "evil.txt")},
		{"triple dot-dot", "../../../root/.ssh/id_rsa"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := safeResolvePath(tt.path, workspace)
			if err == nil {
				t.Errorf("safeResolvePath(%q) should have returned error for traversal", tt.path)
			}
		})
	}
}

func TestSafeResolvePathPrefixCollision(t *testing.T) {
	// Create two directories that share a prefix
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	evilDir := filepath.Join(base, "project-evil")
	os.MkdirAll(workspace, 0755)
	os.MkdirAll(evilDir, 0755)

	// Write a file in the evil directory
	evilFile := filepath.Join(evilDir, "attack.txt")
	os.WriteFile(evilFile, []byte("malicious"), 0644)

	// This is the critical test: absolute path with prefix collision
	_, err := safeResolvePath(evilFile, workspace)
	if err == nil {
		t.Errorf("safeResolvePath(%q, %q) should have blocked prefix collision", evilFile, workspace)
	}
}

func TestSafeResolvePathExactWorkspace(t *testing.T) {
	workspace := t.TempDir()

	// The workspace directory itself should be allowed
	resolved, err := safeResolvePath(workspace, workspace)
	if err != nil {
		t.Errorf("safeResolvePath(workspace, workspace) should be allowed: %v", err)
	}
	absWorkspace, _ := filepath.Abs(workspace)
	absResolved, _ := filepath.Abs(resolved)
	if absResolved != absWorkspace {
		t.Errorf("resolved %q != workspace %q", absResolved, absWorkspace)
	}
}

func TestSafeResolvePathAbsolute(t *testing.T) {
	workspace := t.TempDir()

	// Absolute path inside workspace
	insidePath := filepath.Join(workspace, "sub", "file.txt")
	_, err := safeResolvePath(insidePath, workspace)
	if err != nil {
		t.Errorf("absolute path inside workspace should be allowed: %v", err)
	}

	// Absolute path outside workspace
	var outsidePath string
	if runtime.GOOS == "windows" {
		outsidePath = "C:\\Windows\\System32\\evil.dll"
	} else {
		outsidePath = "/etc/passwd"
	}
	_, err = safeResolvePath(outsidePath, workspace)
	if err == nil {
		t.Errorf("absolute path outside workspace should be blocked")
	}
}

func TestSafeResolvePathNoWorkspace(t *testing.T) {
	// When workspaceDir is empty, any path is allowed (no sandbox)
	resolved, err := safeResolvePath("any/path.txt", "")
	if err != nil {
		t.Errorf("with empty workspace, all paths should be allowed: %v", err)
	}
	if resolved == "" {
		t.Error("resolved should not be empty")
	}
}

// TestSafeResolvePathSymlink verifies that a symlink inside the workspace
// pointing outside is blocked (C-1 symlink-escape fix).
// Skipped on Windows where symlink creation requires elevated permissions.
func TestSafeResolvePathSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated permissions on Windows")
	}

	workspace := t.TempDir()
	outside := t.TempDir()

	// Create a symlink inside workspace → outside
	link := filepath.Join(workspace, "escape_link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("os.Symlink failed: %v", err)
	}

	// Attempting to resolve a path through the symlink should be blocked
	escapePath := filepath.Join(link, "secret.txt")
	_, err := safeResolvePath(escapePath, workspace)
	if err == nil {
		t.Errorf("symlink escape should be blocked: %q → %q", escapePath, outside)
	}
}

// ── FileReadTool Execute tests ───────────────────────────────────────────────

func TestFileReadTool_Success(t *testing.T) {
	workspace := t.TempDir()
	content := "hello, omega!"
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte(content), 0644)

	tool := NewFileReadTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "test.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if result.Output != content {
		t.Errorf("output = %q, want %q", result.Output, content)
	}
}

func TestFileReadTool_FileNotFound(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileReadTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "nonexistent.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "文件不存在") {
		t.Errorf("expected not-found error, got: %+v", result)
	}
}

func TestFileReadTool_IsDirectory(t *testing.T) {
	workspace := t.TempDir()
	os.MkdirAll(filepath.Join(workspace, "subdir"), 0755)

	tool := NewFileReadTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "subdir"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "目录") {
		t.Errorf("expected directory error, got: %+v", result)
	}
}

func TestFileReadTool_FileTooLarge(t *testing.T) {
	workspace := t.TempDir()
	bigFile := filepath.Join(workspace, "big.bin")
	// Write maxFileSize+1 bytes
	data := make([]byte, maxFileSize+1)
	os.WriteFile(bigFile, data, 0644)

	tool := NewFileReadTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "big.bin"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "文件过大") {
		t.Errorf("expected size error, got: %+v", result)
	}
}

func TestFileReadTool_BadJSON(t *testing.T) {
	tool := NewFileReadTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileReadTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileReadTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "../../etc/passwd"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("expected safety error for traversal, got: %+v", result)
	}
}

// ── FileWriteTool Execute tests ──────────────────────────────────────────────

func TestFileWriteTool_Success(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileWriteTool(workspace)
	args, _ := json.Marshal(fileWriteArgs{Path: "out.txt", Content: "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "out.txt"))
	if string(got) != "hello" {
		t.Errorf("file content = %q, want %q", got, "hello")
	}
}

func TestFileWriteTool_Overwrite(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "file.txt")
	os.WriteFile(target, []byte("old content"), 0644)

	tool := NewFileWriteTool(workspace)
	args, _ := json.Marshal(fileWriteArgs{Path: "file.txt", Content: "new content"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(target)
	if string(got) != "new content" {
		t.Errorf("file content = %q, want %q", got, "new content")
	}
}

func TestFileWriteTool_CreateParentDirs(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileWriteTool(workspace)
	args, _ := json.Marshal(fileWriteArgs{Path: "a/b/c/deep.txt", Content: "deep"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, readErr := os.ReadFile(filepath.Join(workspace, "a", "b", "c", "deep.txt"))
	if readErr != nil {
		t.Fatalf("file should have been created: %v", readErr)
	}
	if string(got) != "deep" {
		t.Errorf("content = %q, want %q", got, "deep")
	}
}

func TestFileWriteTool_ContentTooLarge(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileWriteTool(workspace)
	bigContent := strings.Repeat("x", maxWriteSize+1)
	args, _ := json.Marshal(fileWriteArgs{Path: "big.txt", Content: bigContent})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "内容过大") {
		t.Errorf("expected size error, got: %+v", result)
	}
	// File must NOT have been created (check is before filesystem access)
	if _, statErr := os.Stat(filepath.Join(workspace, "big.txt")); !os.IsNotExist(statErr) {
		t.Error("oversized file should not have been created on disk")
	}
}

func TestFileWriteTool_BadJSON(t *testing.T) {
	tool := NewFileWriteTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileWriteTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileWriteTool(workspace)
	args, _ := json.Marshal(fileWriteArgs{Path: "../../evil.txt", Content: "evil"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("expected safety error for traversal, got: %+v", result)
	}
}

// ── FileListTool Execute tests ───────────────────────────────────────────────

func TestFileListTool_Success(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "alpha.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(workspace, "beta.txt"), []byte("bb"), 0644)
	os.MkdirAll(filepath.Join(workspace, "subdir"), 0755)

	tool := NewFileListTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "."})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "alpha.txt") {
		t.Error("output should contain alpha.txt")
	}
	if !strings.Contains(result.Output, "beta.txt") {
		t.Error("output should contain beta.txt")
	}
	if !strings.Contains(result.Output, "subdir") {
		t.Error("output should contain subdir")
	}
	if !strings.Contains(result.Output, "📁") {
		t.Error("directory should be marked with 📁")
	}
}

func TestFileListTool_EmptyDir(t *testing.T) {
	workspace := t.TempDir()
	emptyDir := filepath.Join(workspace, "empty")
	os.MkdirAll(emptyDir, 0755)

	tool := NewFileListTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "empty"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "空目录") {
		t.Errorf("empty dir output = %q, want mention of 空目录", result.Output)
	}
}

func TestFileListTool_NotDirectory(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("x"), 0644)

	tool := NewFileListTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "file.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ReadDir on a file returns an error; we surface it as ToolResult.Error
	if result.Error == "" {
		t.Error("expected error when path is a file, not a directory")
	}
}

func TestFileListTool_Truncation(t *testing.T) {
	workspace := t.TempDir()
	// Create maxListItems+1 files
	for i := 0; i <= maxListItems; i++ {
		os.WriteFile(filepath.Join(workspace, fmt.Sprintf("f%03d.txt", i)), nil, 0644)
	}

	tool := NewFileListTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "."})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "仅显示前") {
		t.Errorf("output should contain truncation notice, got: %q", result.Output)
	}
}

func TestFileListTool_BadJSON(t *testing.T) {
	tool := NewFileListTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileListTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()
	tool := NewFileListTool(workspace)
	args, _ := json.Marshal(filePathArgs{Path: "../../"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("expected safety error for traversal, got: %+v", result)
	}
}

// ── FileFindTool Execute tests ───────────────────────────────────────────────

func TestFileFindTool_KeywordMatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "main.go"), nil, 0644)
	os.WriteFile(filepath.Join(workspace, "helper.go"), nil, 0644)
	os.WriteFile(filepath.Join(workspace, "readme.md"), nil, 0644)

	tool := NewFileFindTool(workspace)
	args, _ := json.Marshal(map[string]string{"pattern": "main"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Error("output should contain main.go")
	}
	if strings.Contains(result.Output, "readme.md") {
		t.Error("output should not contain readme.md")
	}
}

func TestFileFindTool_GlobMatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "main.go"), nil, 0644)
	os.WriteFile(filepath.Join(workspace, "helper.go"), nil, 0644)
	os.WriteFile(filepath.Join(workspace, "readme.md"), nil, 0644)

	tool := NewFileFindTool(workspace)
	args, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Error("output should contain main.go")
	}
	if !strings.Contains(result.Output, "helper.go") {
		t.Error("output should contain helper.go")
	}
	if strings.Contains(result.Output, "readme.md") {
		t.Error("output should not contain readme.md for *.go pattern")
	}
}

func TestFileFindTool_GlobCaseInsensitive(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "main.go"), nil, 0644)
	os.WriteFile(filepath.Join(workspace, "README.MD"), nil, 0644)

	tool := NewFileFindTool(workspace)
	// Uppercase glob pattern should still match lowercase filename
	args, _ := json.Marshal(map[string]string{"pattern": "*.GO"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "main.go") {
		t.Errorf("*.GO should match main.go (case-insensitive), output: %q", result.Output)
	}
}

func TestFileFindTool_NoMatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "main.go"), nil, 0644)

	tool := NewFileFindTool(workspace)
	args, _ := json.Marshal(map[string]string{"pattern": "nonexistent_xyz"})
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

func TestFileFindTool_EmptyPattern(t *testing.T) {
	tool := NewFileFindTool(t.TempDir())
	args, _ := json.Marshal(map[string]string{"pattern": ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "不能为空") {
		t.Errorf("expected empty pattern error, got: %+v", result)
	}
}

func TestFileFindTool_NoWorkspace(t *testing.T) {
	tool := NewFileFindTool("") // empty workspace
	args, _ := json.Marshal(map[string]string{"pattern": "main"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "工作目录") {
		t.Errorf("expected workspace error, got: %+v", result)
	}
}

func TestFileFindTool_BadJSON(t *testing.T) {
	tool := NewFileFindTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileFindTool_SkipsHiddenDirs(t *testing.T) {
	workspace := t.TempDir()
	// Create .git directory with a file inside
	gitDir := filepath.Join(workspace, ".git")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0644)
	// Also create a normal file
	os.WriteFile(filepath.Join(workspace, "main.go"), nil, 0644)

	tool := NewFileFindTool(workspace)
	// Search for "config" — should NOT find .git/config
	args, _ := json.Marshal(map[string]string{"pattern": "config"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, ".git") {
		t.Errorf("output should not contain .git directory contents, got: %q", result.Output)
	}
}

func TestFileFindTool_Truncation(t *testing.T) {
	workspace := t.TempDir()
	// Create maxFindResults+1 matching files to trigger the truncation path
	for i := 0; i <= maxFindResults; i++ {
		os.WriteFile(filepath.Join(workspace, fmt.Sprintf("match_%03d.go", i)), nil, 0644)
	}

	tool := NewFileFindTool(workspace)
	args, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "已截断") {
		t.Errorf("output should contain truncation notice, got: %q", result.Output)
	}
}

// TestFileWriteTool_SymlinkEscape verifies that writing through a symlink that
// points outside the workspace is blocked (C-1 symlink-escape fix for writes).
// Skipped on Windows where symlink creation requires elevated permissions.
func TestFileWriteTool_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated permissions on Windows")
	}

	workspace := t.TempDir()
	outside := t.TempDir()

	// Create a symlink inside workspace → outside directory
	link := filepath.Join(workspace, "escape_link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("os.Symlink failed: %v", err)
	}

	tool := NewFileWriteTool(workspace)
	args, _ := json.Marshal(fileWriteArgs{
		Path:    filepath.Join("escape_link", "evil.txt"),
		Content: "should not be written outside workspace",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("symlink escape write should be blocked, got: %+v", result)
	}

	// Verify the file was NOT created outside the workspace
	if _, statErr := os.Stat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(statErr) {
		t.Error("file should not have been created outside workspace via symlink")
	}
}
