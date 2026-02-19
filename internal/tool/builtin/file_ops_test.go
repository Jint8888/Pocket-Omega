package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── FileMoveTool Execute tests ───────────────────────────────────────────────

func TestFileMoveTool_Success(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "src.txt"), []byte("hello"), 0644)

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "src.txt", Destination: "dst.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	// Source should no longer exist
	if _, statErr := os.Stat(filepath.Join(workspace, "src.txt")); !os.IsNotExist(statErr) {
		t.Error("source file should have been removed after move")
	}
	// Destination should exist with correct content
	got, readErr := os.ReadFile(filepath.Join(workspace, "dst.txt"))
	if readErr != nil {
		t.Fatalf("destination file should exist: %v", readErr)
	}
	if string(got) != "hello" {
		t.Errorf("destination content = %q, want %q", got, "hello")
	}
}

func TestFileMoveTool_MoveDirectory(t *testing.T) {
	workspace := t.TempDir()
	srcDir := filepath.Join(workspace, "srcdir")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "inner.txt"), []byte("data"), 0644)

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "srcdir", Destination: "dstdir"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, readErr := os.ReadFile(filepath.Join(workspace, "dstdir", "inner.txt"))
	if readErr != nil {
		t.Fatalf("inner file should exist after directory move: %v", readErr)
	}
	if string(got) != "data" {
		t.Errorf("inner content = %q, want %q", got, "data")
	}
}

func TestFileMoveTool_AutoCreateParentDirs(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("content"), 0644)

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "file.txt", Destination: "a/b/c/file.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, readErr := os.ReadFile(filepath.Join(workspace, "a", "b", "c", "file.txt"))
	if readErr != nil {
		t.Fatalf("file should exist at nested destination: %v", readErr)
	}
	if string(got) != "content" {
		t.Errorf("content = %q, want %q", got, "content")
	}
}

func TestFileMoveTool_DestinationExists(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "src.txt"), []byte("src"), 0644)
	os.WriteFile(filepath.Join(workspace, "dst.txt"), []byte("dst"), 0644)

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "src.txt", Destination: "dst.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "目标路径已存在") {
		t.Errorf("expected destination-exists error, got: %+v", result)
	}

	// Original files should be untouched
	got, _ := os.ReadFile(filepath.Join(workspace, "dst.txt"))
	if string(got) != "dst" {
		t.Errorf("destination content should be unchanged, got %q", got)
	}
}

func TestFileMoveTool_SourceNotExist(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "nonexistent.txt", Destination: "dst.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "源路径不存在") {
		t.Errorf("expected source-not-found error, got: %+v", result)
	}
}

func TestFileMoveTool_EmptySource(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "", Destination: "dst.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "source 不能为空") {
		t.Errorf("expected empty source error, got: %+v", result)
	}
}

func TestFileMoveTool_EmptyDestination(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "src.txt", Destination: ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "destination 不能为空") {
		t.Errorf("expected empty destination error, got: %+v", result)
	}
}

func TestFileMoveTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "src.txt"), []byte("data"), 0644)

	tests := []struct {
		name string
		src  string
		dst  string
	}{
		{"source traversal", "../../etc/passwd", "dst.txt"},
		{"destination traversal", "src.txt", "../../evil.txt"},
		{"both traversal", "../../src", "../../dst"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewFileMoveTool(workspace)
			args, _ := json.Marshal(fileMoveArgs{Source: tt.src, Destination: tt.dst})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" {
				t.Errorf("expected safety error for traversal, got success")
			}
		})
	}
}

func TestFileMoveTool_MoveWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: ".", Destination: "somewhere"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "禁止移动工作区根目录") {
		t.Errorf("expected workspace root error, got: %+v", result)
	}
}

func TestFileMoveTool_BadJSON(t *testing.T) {
	tool := NewFileMoveTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileMoveTool_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated permissions on Windows")
	}

	workspace := t.TempDir()
	outside := t.TempDir()

	link := filepath.Join(workspace, "escape_link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("os.Symlink failed: %v", err)
	}

	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("data"), 0644)

	tool := NewFileMoveTool(workspace)
	args, _ := json.Marshal(fileMoveArgs{Source: "file.txt", Destination: "escape_link/stolen.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("expected safety error for symlink escape, got: %+v", result)
	}
}

// ── FileDeleteTool Execute tests ─────────────────────────────────────────────

func TestFileDeleteTool_Success(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "to_delete.txt")
	os.WriteFile(target, []byte("bye"), 0644)

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "to_delete.txt", Confirm: "yes"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("file should have been deleted")
	}
}

func TestFileDeleteTool_ConfirmNotYes(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "protected.txt")
	os.WriteFile(target, []byte("safe"), 0644)

	tests := []struct {
		name    string
		confirm string
	}{
		{"empty confirm", ""},
		{"no", "no"},
		{"YES uppercase", "YES"},
		{"Yes mixed", "Yes"},
		{"random", "maybe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewFileDeleteTool(workspace)
			args, _ := json.Marshal(fileDeleteArgs{Path: "protected.txt", Confirm: tt.confirm})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" || !strings.Contains(result.Error, "confirm 参数必须为") {
				t.Errorf("expected confirm rejection, got: %+v", result)
			}
		})
	}

	// Verify file was not deleted
	if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
		t.Error("file should still exist after rejected confirm")
	}
}

func TestFileDeleteTool_NonEmptyDirWithoutRecursive(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "nonempty")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "child.txt"), []byte("x"), 0644)

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "nonempty", Confirm: "yes", Recursive: false})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "目录非空") {
		t.Errorf("expected non-empty dir error, got: %+v", result)
	}

	// Directory should still exist
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		t.Error("non-empty directory should not have been deleted")
	}
}

func TestFileDeleteTool_RecursiveDeleteNonEmptyDir(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "tree")
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0644)

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "tree", Confirm: "yes", Recursive: true})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("directory tree should have been fully deleted")
	}
}

func TestFileDeleteTool_DeleteEmptyDir(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "empty")
	os.MkdirAll(dir, 0755)

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "empty", Confirm: "yes", Recursive: false})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("empty directory should have been deleted")
	}
}

func TestFileDeleteTool_PathNotExist(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "ghost.txt", Confirm: "yes"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "路径不存在") {
		t.Errorf("expected not-found error, got: %+v", result)
	}
}

func TestFileDeleteTool_EmptyPath(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "", Confirm: "yes"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "path 不能为空") {
		t.Errorf("expected empty path error, got: %+v", result)
	}
}

func TestFileDeleteTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: "../../etc/passwd", Confirm: "yes"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Errorf("expected safety error for traversal, got success")
	}
}

func TestFileDeleteTool_DeleteWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFileDeleteTool(workspace)
	args, _ := json.Marshal(fileDeleteArgs{Path: ".", Confirm: "yes", Recursive: true})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "禁止删除工作区根目录") {
		t.Errorf("expected workspace root error, got: %+v", result)
	}
}

func TestFileDeleteTool_BadJSON(t *testing.T) {
	tool := NewFileDeleteTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

// ── FilePatchTool Execute tests ──────────────────────────────────────────────

func TestFilePatchTool_ReplaceLines(t *testing.T) {
	workspace := t.TempDir()
	original := "line1\nline2\nline3\nline4\n"
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte(original), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 2,
		EndLine:   3,
		Content:   "replaced2\nreplaced3\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "test.txt"))
	expected := "line1\nreplaced2\nreplaced3\nline4\n"
	if string(got) != expected {
		t.Errorf("file content = %q, want %q", got, expected)
	}
}

func TestFilePatchTool_DeleteLines(t *testing.T) {
	workspace := t.TempDir()
	original := "line1\nline2\nline3\nline4\n"
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte(original), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 2,
		EndLine:   3,
		Content:   "",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "test.txt"))
	expected := "line1\nline4\n"
	if string(got) != expected {
		t.Errorf("file content = %q, want %q", got, expected)
	}
}

func TestFilePatchTool_EndLineOutOfBounds(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("line1\nline2\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 1,
		EndLine:   10,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "超出文件实际行数") {
		t.Errorf("expected out-of-bounds error, got: %+v", result)
	}
}

func TestFilePatchTool_ExpectedContentMismatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("line1\nline2\nline3\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:            "test.txt",
		StartLine:       2,
		EndLine:         2,
		Content:         "new\n",
		ExpectedContent: "wrong content",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "内容不匹配") {
		t.Errorf("expected content mismatch error, got: %+v", result)
	}

	// File should be unchanged
	got, _ := os.ReadFile(filepath.Join(workspace, "test.txt"))
	if string(got) != "line1\nline2\nline3\n" {
		t.Errorf("file should be unchanged after mismatch, got %q", got)
	}
}

func TestFilePatchTool_ExpectedContentMatch(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("line1\nline2\nline3\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:            "test.txt",
		StartLine:       2,
		EndLine:         2,
		Content:         "replaced\n",
		ExpectedContent: "line2\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "test.txt"))
	expected := "line1\nreplaced\nline3\n"
	if string(got) != expected {
		t.Errorf("file content = %q, want %q", got, expected)
	}
}

func TestFilePatchTool_StartLineLessThanOne(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("line1\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 0,
		EndLine:   1,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "start_line 必须 >= 1") {
		t.Errorf("expected start_line validation error, got: %+v", result)
	}
}

func TestFilePatchTool_EndLineLessThanStartLine(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("line1\nline2\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 3,
		EndLine:   1,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "end_line") {
		t.Errorf("expected end_line < start_line error, got: %+v", result)
	}
}

func TestFilePatchTool_EmptyPath(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "",
		StartLine: 1,
		EndLine:   1,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "path 不能为空") {
		t.Errorf("expected empty path error, got: %+v", result)
	}
}

func TestFilePatchTool_PathTraversal(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "../../etc/passwd",
		StartLine: 1,
		EndLine:   1,
		Content:   "hacked\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Errorf("expected safety error for traversal, got success")
	}
}

func TestFilePatchTool_FileNotExist(t *testing.T) {
	workspace := t.TempDir()

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "nonexistent.txt",
		StartLine: 1,
		EndLine:   1,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "文件不存在") {
		t.Errorf("expected not-found error, got: %+v", result)
	}
}

func TestFilePatchTool_IsDirectory(t *testing.T) {
	workspace := t.TempDir()
	os.MkdirAll(filepath.Join(workspace, "subdir"), 0755)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "subdir",
		StartLine: 1,
		EndLine:   1,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "目录") {
		t.Errorf("expected directory error, got: %+v", result)
	}
}

func TestFilePatchTool_FileTooLarge(t *testing.T) {
	workspace := t.TempDir()
	bigFile := filepath.Join(workspace, "big.txt")
	data := make([]byte, maxPatchFileSize+1)
	os.WriteFile(bigFile, data, 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "big.txt",
		StartLine: 1,
		EndLine:   1,
		Content:   "new\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "文件过大") {
		t.Errorf("expected file-too-large error, got: %+v", result)
	}
}

func TestFilePatchTool_BadJSON(t *testing.T) {
	tool := NewFilePatchTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFilePatchTool_ReplaceSingleLine(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("aaa\nbbb\nccc\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 2,
		EndLine:   2,
		Content:   "BBB\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "test.txt"))
	if string(got) != "aaa\nBBB\nccc\n" {
		t.Errorf("file content = %q, want %q", got, "aaa\nBBB\nccc\n")
	}
}

func TestFilePatchTool_InsertMoreLinesThanRemoved(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("a\nb\nc\n"), 0644)

	tool := NewFilePatchTool(workspace)
	args, _ := json.Marshal(filePatchArgs{
		Path:      "test.txt",
		StartLine: 2,
		EndLine:   2,
		Content:   "x\ny\nz\n",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "test.txt"))
	expected := "a\nx\ny\nz\nc\n"
	if string(got) != expected {
		t.Errorf("file content = %q, want %q", got, expected)
	}
}

// ── splitLines unit tests ────────────────────────────────────────────────────

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty string", "", nil},
		{"single line no newline", "hello", []string{"hello"}},
		{"single line with newline", "hello\n", []string{"hello\n"}},
		{"two lines", "a\nb\n", []string{"a\n", "b\n"}},
		{"trailing content no newline", "a\nb", []string{"a\n", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitLines(%q) = %v (len %d), want %v (len %d)", tt.input, got, len(got), tt.want, len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitLines(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ── relOrAbs unit tests ──────────────────────────────────────────────────────

func TestRelOrAbs(t *testing.T) {
	workspace := t.TempDir()
	absFile := filepath.Join(workspace, "sub", "file.txt")

	rel := relOrAbs(absFile, workspace)
	if rel != filepath.Join("sub", "file.txt") {
		t.Errorf("relOrAbs = %q, want %q", rel, filepath.Join("sub", "file.txt"))
	}
}
