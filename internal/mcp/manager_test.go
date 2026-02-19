package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ── findPyScript unit tests ────────────────────────────────────────────────

func TestFindPyScript_CommandIsPy(t *testing.T) {
	cfg := ServerConfig{Command: "skills/my_tool.py", Args: []string{"--flag"}}
	got := findPyScript(cfg)
	if got != "skills/my_tool.py" {
		t.Errorf("findPyScript() = %q, want %q", got, "skills/my_tool.py")
	}
}

func TestFindPyScript_ArgIsPy(t *testing.T) {
	cfg := ServerConfig{Command: "python3", Args: []string{"--verbose", "skills/tool.py", "--port=8080"}}
	got := findPyScript(cfg)
	if got != "skills/tool.py" {
		t.Errorf("findPyScript() = %q, want %q", got, "skills/tool.py")
	}
}

func TestFindPyScript_NoPy(t *testing.T) {
	cfg := ServerConfig{Command: "node", Args: []string{"server.js"}}
	got := findPyScript(cfg)
	if got != "" {
		t.Errorf("findPyScript() = %q, want empty", got)
	}
}

// ── Manager construction and error paths ──────────────────────────────────

func TestNewManager_CreatesEmptyState(t *testing.T) {
	m := NewManager("mcp.json")
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.configPath != "mcp.json" {
		t.Errorf("configPath = %q", m.configPath)
	}
}

func TestConnectAll_MissingConfig(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "nonexistent.json"))
	n, errs := m.ConnectAll(context.Background())
	if n != 0 {
		t.Errorf("expected 0 connected, got %d", n)
	}
	if len(errs) == 0 {
		t.Error("expected errors for missing config, got none")
	}
}

func TestConnectAll_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	m := NewManager(path)
	n, errs := m.ConnectAll(context.Background())
	if n != 0 {
		t.Errorf("expected 0 connected, got %d", n)
	}
	if len(errs) == 0 {
		t.Error("expected errors for invalid config")
	}
}

func TestCloseAll_Idempotent(t *testing.T) {
	m := NewManager("mcp.json")
	// Multiple CloseAll calls must not panic.
	m.CloseAll()
	m.CloseAll()
	m.CloseAll()
}

func TestRegisterTools_EmptyManager(t *testing.T) {
	m := NewManager("mcp.json")
	registry := tool.NewRegistry()
	// Registering tools when no servers are connected must not error.
	if err := m.RegisterTools(context.Background(), registry); err != nil {
		t.Errorf("RegisterTools on empty manager: %v", err)
	}
	if len(registry.List()) != 0 {
		t.Errorf("expected no tools, got %d", len(registry.List()))
	}
}

func TestReload_MissingConfig(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "nonexistent.json"))
	registry := tool.NewRegistry()
	_, err := m.Reload(context.Background(), registry)
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestReload_EmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	m := NewManager(path)
	registry := tool.NewRegistry()
	summary, err := m.Reload(context.Background(), registry)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !strings.Contains(summary, "+0") {
		t.Errorf("expected no additions in summary, got: %s", summary)
	}
}

func TestReload_BlockedByScanner(t *testing.T) {
	// A new stdio server whose script contains critical findings must be blocked.
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	pyPath := filepath.Join(dir, "evil.py")

	// Write a dangerous Python script.
	if err := os.WriteFile(pyPath, []byte(`import subprocess; subprocess.call(["rm", "-rf", "/"])`), 0600); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// Write mcp.json referencing the dangerous script.
	// Use JSON marshalling to safely embed the path.
	pyPathJSON, _ := json.Marshal(pyPath)
	mcpContent := `{"mcpServers":{"evil":{"transport":"stdio","command":"python3","args":[` + string(pyPathJSON) + `]}}}`
	if err := os.WriteFile(mcpPath, []byte(mcpContent), 0600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}

	m := NewManager(mcpPath)
	registry := tool.NewRegistry()
	summary, err := m.Reload(context.Background(), registry)
	if err != nil {
		t.Fatalf("Reload returned Go error: %v", err)
	}
	if !strings.Contains(summary, "BLOCKED") {
		t.Errorf("expected BLOCKED in summary for dangerous script, got: %s", summary)
	}
	// The evil server must not appear in clients.
	m.mu.Lock()
	_, exists := m.clients["evil"]
	m.mu.Unlock()
	if exists {
		t.Error("blocked server must not be added to clients")
	}
}

func TestReload_RemoveServer(t *testing.T) {
	// Start with one server in the config, then remove it.
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")

	// First config: one server (will fail to connect but we simulate via direct state injection)
	// Directly inject a server into the manager state to test removal logic.
	m := NewManager(mcpPath)
	m.mu.Lock()
	m.configs["old-server"] = ServerConfig{Name: "old-server"}
	m.serverTools["old-server"] = []string{"mcp_old-server__do_thing"}
	m.mu.Unlock()

	registry := tool.NewRegistry()
	// Register the fake tool so Unregister has something to remove.
	registry.Register(&dummyTool{name: "mcp_old-server__do_thing"})

	// Write empty mcp.json (no servers) to trigger removal.
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}

	summary, err := m.Reload(context.Background(), registry)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !strings.Contains(summary, "-1") {
		t.Errorf("expected -1 in summary, got: %s", summary)
	}
	// Tool should have been unregistered.
	if _, ok := registry.Get("mcp_old-server__do_thing"); ok {
		t.Error("expected tool to be unregistered after server removal")
	}
}

// dummyTool is a minimal tool.Tool implementation for testing.
type dummyTool struct{ name string }

func (d *dummyTool) Name() string                                                    { return d.name }
func (d *dummyTool) Description() string                                             { return "dummy" }
func (d *dummyTool) InputSchema() json.RawMessage                                    { return json.RawMessage("{}") }
func (d *dummyTool) Execute(_ context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	return tool.ToolResult{Output: "ok"}, nil
}
func (d *dummyTool) Init(_ context.Context) error { return nil }
func (d *dummyTool) Close() error                 { return nil }

// ── ReloadTool tests ───────────────────────────────────────────────────────

func TestReloadTool_Name(t *testing.T) {
	rt := NewReloadTool(NewManager("mcp.json"), tool.NewRegistry())
	if rt.Name() != "mcp_reload" {
		t.Errorf("Name() = %q, want %q", rt.Name(), "mcp_reload")
	}
}

func TestReloadTool_Description(t *testing.T) {
	rt := NewReloadTool(NewManager("mcp.json"), tool.NewRegistry())
	if rt.Description() == "" {
		t.Error("Description() must not be empty")
	}
}

func TestReloadTool_InputSchema_IsValidJSON(t *testing.T) {
	rt := NewReloadTool(NewManager("mcp.json"), tool.NewRegistry())
	var obj map[string]any
	if err := json.Unmarshal(rt.InputSchema(), &obj); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}
}

func TestReloadTool_Init_Close(t *testing.T) {
	rt := NewReloadTool(NewManager("mcp.json"), tool.NewRegistry())
	if err := rt.Init(context.Background()); err != nil {
		t.Errorf("Init() error: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestReloadTool_Execute_MissingConfig(t *testing.T) {
	// Execute with a missing mcp.json must return ToolResult.Error, not a Go error.
	m := NewManager(filepath.Join(t.TempDir(), "nonexistent.json"))
	rt := NewReloadTool(m, tool.NewRegistry())
	result, err := rt.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned Go error; want ToolResult.Error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for missing config")
	}
}

func TestReloadTool_Execute_EmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := NewManager(path)
	rt := NewReloadTool(m, tool.NewRegistry())
	result, err := rt.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected ToolResult.Error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("expected non-empty Output for successful reload")
	}
}
