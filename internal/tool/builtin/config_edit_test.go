package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────

// writeTempEnv writes a .env file in a temp dir and returns (path, allowedFiles map).
func writeTempEnv(t *testing.T, content string) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTempEnv: %v", err)
	}
	return path, map[string]string{".env": path}
}

func execConfigEdit(t *testing.T, tl *ConfigEditTool, args map[string]any) (string, string) {
	t.Helper()
	raw, _ := json.Marshal(args)
	result, err := tl.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return result.Output, result.Error
}

// ── set ───────────────────────────────────────────────────────────────────

func TestConfigEdit_Set_NewKey(t *testing.T) {
	_, allowed := writeTempEnv(t, "EXISTING=hello\n")
	tl := NewConfigEditTool(allowed)

	output, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "set", "key": "NEW_KEY", "value": "world",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(output, "已新增") {
		t.Errorf("expected '已新增' in output, got: %s", output)
	}

	// Verify file content
	data, _ := os.ReadFile(allowed[".env"])
	if !strings.Contains(string(data), "NEW_KEY=world") {
		t.Errorf("file should contain NEW_KEY=world, got:\n%s", data)
	}
	if !strings.Contains(string(data), "EXISTING=hello") {
		t.Errorf("file should still contain EXISTING=hello, got:\n%s", data)
	}
}

func TestConfigEdit_Set_UpdateExisting(t *testing.T) {
	_, allowed := writeTempEnv(t, "FOO=old\nBAR=keep\n")
	tl := NewConfigEditTool(allowed)

	output, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "set", "key": "FOO", "value": "new",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(output, "已更新") {
		t.Errorf("expected '已更新' in output, got: %s", output)
	}

	data, _ := os.ReadFile(allowed[".env"])
	content := string(data)
	if !strings.Contains(content, "FOO=new") {
		t.Errorf("FOO should be updated to 'new', got:\n%s", content)
	}
	if !strings.Contains(content, "BAR=keep") {
		t.Errorf("BAR should remain unchanged, got:\n%s", content)
	}
}

func TestConfigEdit_Set_PreservesComments(t *testing.T) {
	original := "# This is a comment\nFOO=bar\n\n# Another comment\nBAZ=qux\n"
	_, allowed := writeTempEnv(t, original)
	tl := NewConfigEditTool(allowed)

	execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "set", "key": "FOO", "value": "updated",
	})

	data, _ := os.ReadFile(allowed[".env"])
	content := string(data)
	if !strings.Contains(content, "# This is a comment") {
		t.Error("first comment should be preserved")
	}
	if !strings.Contains(content, "# Another comment") {
		t.Error("second comment should be preserved")
	}
	if !strings.Contains(content, "BAZ=qux") {
		t.Error("BAZ should remain unchanged")
	}
}

func TestConfigEdit_Set_EmptyKey(t *testing.T) {
	_, allowed := writeTempEnv(t, "")
	tl := NewConfigEditTool(allowed)

	_, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "set", "key": "", "value": "x",
	})
	if errMsg == "" {
		t.Error("expected error for empty key")
	}
}

func TestConfigEdit_Set_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	// File does NOT exist yet.
	allowed := map[string]string{".env": path}
	tl := NewConfigEditTool(allowed)

	output, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "set", "key": "BRAND_NEW", "value": "yes",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(output, "已新增") {
		t.Errorf("expected '已新增', got: %s", output)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "BRAND_NEW=yes") {
		t.Errorf("file should contain BRAND_NEW=yes, got:\n%s", data)
	}
}

// ── get ───────────────────────────────────────────────────────────────────

func TestConfigEdit_Get_Exists(t *testing.T) {
	_, allowed := writeTempEnv(t, "MY_KEY=my_value\n")
	tl := NewConfigEditTool(allowed)

	output, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "get", "key": "MY_KEY",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if output != "MY_KEY=my_value" {
		t.Errorf("expected 'MY_KEY=my_value', got: %s", output)
	}
}

func TestConfigEdit_Get_NotExists(t *testing.T) {
	_, allowed := writeTempEnv(t, "OTHER=val\n")
	tl := NewConfigEditTool(allowed)

	_, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "get", "key": "MISSING",
	})
	if errMsg == "" {
		t.Error("expected error for missing key")
	}
}

func TestConfigEdit_Get_EmptyKey(t *testing.T) {
	_, allowed := writeTempEnv(t, "FOO=bar\n")
	tl := NewConfigEditTool(allowed)

	_, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "get", "key": "",
	})
	if errMsg == "" {
		t.Error("expected error for empty key")
	}
}

// ── list ───────────────────────────────────────────────────────────────────

func TestConfigEdit_List(t *testing.T) {
	_, allowed := writeTempEnv(t, "# comment\nA=1\nB=2\n\nC=3\n")
	tl := NewConfigEditTool(allowed)

	output, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "list",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(output, "3 项") {
		t.Errorf("expected '3 项' in output, got: %s", output)
	}
	if !strings.Contains(output, "A=1") || !strings.Contains(output, "B=2") || !strings.Contains(output, "C=3") {
		t.Errorf("output should contain all entries, got: %s", output)
	}
}

func TestConfigEdit_List_Empty(t *testing.T) {
	_, allowed := writeTempEnv(t, "# only a comment\n")
	tl := NewConfigEditTool(allowed)

	output, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "list",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.Contains(output, "空") {
		t.Errorf("expected empty message, got: %s", output)
	}
}

// ── security: allowlist ───────────────────────────────────────────────────

func TestConfigEdit_FileNotInAllowlist(t *testing.T) {
	_, allowed := writeTempEnv(t, "X=1\n")
	tl := NewConfigEditTool(allowed)

	_, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": "secrets.txt", "action": "list",
	})
	if errMsg == "" {
		t.Error("expected error for file not in allowlist")
	}
	if !strings.Contains(errMsg, "白名单") {
		t.Errorf("error should mention allowlist, got: %s", errMsg)
	}
}

// ── edge cases ────────────────────────────────────────────────────────────

func TestConfigEdit_InvalidAction(t *testing.T) {
	_, allowed := writeTempEnv(t, "X=1\n")
	tl := NewConfigEditTool(allowed)

	_, errMsg := execConfigEdit(t, tl, map[string]any{
		"file": ".env", "action": "delete",
	})
	if errMsg == "" {
		t.Error("expected error for invalid action")
	}
}

func TestConfigEdit_InvalidJSON(t *testing.T) {
	_, allowed := writeTempEnv(t, "")
	tl := NewConfigEditTool(allowed)

	result, err := tl.Execute(context.Background(), []byte(`{not valid}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for invalid JSON")
	}
}

func TestConfigEdit_InitClose(t *testing.T) {
	tl := NewConfigEditTool(map[string]string{".env": "/tmp/.env"})
	if err := tl.Init(context.Background()); err != nil {
		t.Errorf("Init() error: %v", err)
	}
	if err := tl.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestConfigEdit_Description_ListsFiles(t *testing.T) {
	tl := NewConfigEditTool(map[string]string{
		".env":     "/a/.env",
		"mcp.json": "/a/mcp.json",
	})
	desc := tl.Description()
	if !strings.Contains(desc, ".env") || !strings.Contains(desc, "mcp.json") {
		t.Errorf("Description should list allowed files, got: %s", desc)
	}
}
