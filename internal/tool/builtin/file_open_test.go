package builtin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── FileOpenTool Execute tests ────────────────────────────────────────────────

// nopOpenCmd 是测试专用的 no-op 命令工厂：立即退出、不弹任何 GUI 窗口。
// 注入到 openCmdFunc 后，可以验证 Execute 的完整代码路径而无副作用。
func nopOpenCmd(_ string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "exit", "0")
	}
	return exec.Command("sh", "-c", "exit 0")
}

func TestFileOpenTool_EmptyPath(t *testing.T) {
	tool := NewFileOpenTool(t.TempDir())
	args, _ := json.Marshal(fileOpenArgs{Path: ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "path 不能为空") {
		t.Errorf("expected empty path error, got: %+v", result)
	}
}

func TestFileOpenTool_BlockedExtension(t *testing.T) {
	workspace := t.TempDir()
	blocked := []string{
		".exe", ".bat", ".cmd", ".ps1", ".vbs", ".sh", ".jar", ".py", ".msi", ".scr",
	}
	for _, ext := range blocked {
		t.Run(ext, func(t *testing.T) {
			// 先建立文件，确认扩展名检查在 stat 之前触发
			fname := "payload" + ext
			os.WriteFile(filepath.Join(workspace, fname), []byte("x"), 0644)

			tool := NewFileOpenTool(workspace)
			args, _ := json.Marshal(fileOpenArgs{Path: fname})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
				t.Errorf("expected blocked extension error for %s, got: %+v", ext, result)
			}
		})
	}
}

func TestFileOpenTool_FileNotExist(t *testing.T) {
	tool := NewFileOpenTool(t.TempDir())
	args, _ := json.Marshal(fileOpenArgs{Path: "ghost.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "文件不存在") {
		t.Errorf("expected not-exist error, got: %+v", result)
	}
}

func TestFileOpenTool_IsDirectory(t *testing.T) {
	workspace := t.TempDir()
	os.MkdirAll(filepath.Join(workspace, "subdir"), 0755)

	tool := NewFileOpenTool(workspace)
	args, _ := json.Marshal(fileOpenArgs{Path: "subdir"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "目录") {
		t.Errorf("expected directory error, got: %+v", result)
	}
}

func TestFileOpenTool_PathTraversal(t *testing.T) {
	tool := NewFileOpenTool(t.TempDir())
	args, _ := json.Marshal(fileOpenArgs{Path: "../../etc/passwd"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Errorf("expected traversal error, got success")
	}
}

func TestFileOpenTool_BadJSON(t *testing.T) {
	tool := NewFileOpenTool(t.TempDir())
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

func TestFileOpenTool_Success(t *testing.T) {
	// 替换 openCmdFunc 为 no-op，避免真实弹出 GUI 窗口或因临时目录被
	// 清理导致 Windows 报"找不到文件"的弹窗。
	orig := openCmdFunc
	openCmdFunc = nopOpenCmd
	defer func() { openCmdFunc = orig }()

	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("hello"), 0644)

	tool := NewFileOpenTool(workspace)
	args, _ := json.Marshal(fileOpenArgs{Path: "note.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "note.txt") {
		t.Errorf("output should mention file name, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "已使用默认程序打开") {
		t.Errorf("output should confirm open, got: %q", result.Output)
	}
}

// ── openCmd unit test ─────────────────────────────────────────────────────────

func TestOpenCmd_ReturnsCmd(t *testing.T) {
	// 只验证 openCmd 不 panic 且返回非 nil；不实际执行命令
	cmd := openCmd("/tmp/test.txt")
	if cmd == nil {
		t.Error("openCmd returned nil")
	}
	if cmd.Path == "" {
		t.Error("openCmd Path is empty")
	}
}

// ── blockedOpenExts coverage ──────────────────────────────────────────────────

func TestBlockedOpenExts_Completeness(t *testing.T) {
	// 确保常见危险扩展都在名单里
	mustBlock := []string{".exe", ".bat", ".ps1", ".sh", ".py", ".jar", ".msi"}
	for _, ext := range mustBlock {
		if !blockedOpenExts[ext] {
			t.Errorf("extension %s should be in blockedOpenExts", ext)
		}
	}

	// 确保正常媒体类型不在名单里
	shouldAllow := []string{".txt", ".jpg", ".png", ".mp3", ".mp4", ".pdf", ".docx"}
	for _, ext := range shouldAllow {
		if blockedOpenExts[ext] {
			t.Errorf("extension %s should NOT be in blockedOpenExts", ext)
		}
	}
}
