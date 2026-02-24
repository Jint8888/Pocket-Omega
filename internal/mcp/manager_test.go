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

// ── findScriptFile unit tests ────────────────────────────────────────────────

func TestFindScriptFile_CommandIsPy(t *testing.T) {
	cfg := ServerConfig{Command: "skills/my_tool.py", Args: []string{"--flag"}}
	got := findScriptFile(cfg)
	if got != "skills/my_tool.py" {
		t.Errorf("findScriptFile() = %q, want %q", got, "skills/my_tool.py")
	}
}

func TestFindScriptFile_ArgIsPy(t *testing.T) {
	cfg := ServerConfig{Command: "python3", Args: []string{"--verbose", "skills/tool.py", "--port=8080"}}
	got := findScriptFile(cfg)
	if got != "skills/tool.py" {
		t.Errorf("findScriptFile() = %q, want %q", got, "skills/tool.py")
	}
}

func TestFindScriptFile_NoPy(t *testing.T) {
	cfg := ServerConfig{Command: "go", Args: []string{"run", "main.go"}}
	got := findScriptFile(cfg)
	if got != "" {
		t.Errorf("findScriptFile() = %q, want empty", got)
	}
}

func TestFindScriptFile_CommandIsTS(t *testing.T) {
	cfg := ServerConfig{Command: "npx", Args: []string{"tsx", "skills/server.ts"}}
	got := findScriptFile(cfg)
	if got != "skills/server.ts" {
		t.Errorf("findScriptFile() = %q, want %q", got, "skills/server.ts")
	}
}

func TestFindScriptFile_ArgIsJS(t *testing.T) {
	cfg := ServerConfig{Command: "node", Args: []string{"skills/server.js"}}
	got := findScriptFile(cfg)
	if got != "skills/server.js" {
		t.Errorf("findScriptFile() = %q, want %q", got, "skills/server.js")
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

// ── CloseAll with nil client (per_call servers) ───────────────────────────

func TestCloseAll_WithNilClient(t *testing.T) {
	m := NewManager("mcp.json")
	// Inject a per_call server: m.clients stores nil for per_call entries.
	m.mu.Lock()
	m.clients["per-call-server"] = nil
	m.mu.Unlock()
	// Must not panic.
	m.CloseAll()
}

// ── updateServerMeta ───────────────────────────────────────────────────────

// readMetaField reads a specific _meta key from mcp.json using raw JSON
// (avoids importing the builtin package's typed struct from the mcp package).
func readMetaField(t *testing.T, mcpPath, serverName, key string) string {
	t.Helper()
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("readMetaField read: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("readMetaField parse: %v", err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	entry, _ := servers[serverName].(map[string]any)
	meta, _ := entry["_meta"].(map[string]any)
	val, _ := meta[key].(string)
	return val
}

func TestUpdateServerMeta_Basic(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	content := `{"mcpServers":{"svc":{"transport":"stdio","command":"node"}}}`
	if err := os.WriteFile(mcpPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	updateServerMeta(mcpPath, "svc", map[string]string{
		"scan_result": "clean",
		"scanned_at":  "2026-02-20",
	})

	if got := readMetaField(t, mcpPath, "svc", "scan_result"); got != "clean" {
		t.Errorf("scan_result = %q, want clean", got)
	}
	if got := readMetaField(t, mcpPath, "svc", "scanned_at"); got != "2026-02-20" {
		t.Errorf("scanned_at = %q, want 2026-02-20", got)
	}
}

func TestUpdateServerMeta_PreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	content := `{"mcpServers":{"svc":{"transport":"stdio","_meta":{"origin":"agent","custom":"value"}}}}`
	if err := os.WriteFile(mcpPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Add scan_result without touching existing keys.
	updateServerMeta(mcpPath, "svc", map[string]string{
		"scan_result": "warning",
	})

	if got := readMetaField(t, mcpPath, "svc", "origin"); got != "agent" {
		t.Errorf("origin key overwritten; got %q, want agent", got)
	}
	if got := readMetaField(t, mcpPath, "svc", "custom"); got != "value" {
		t.Errorf("custom key overwritten; got %q, want value", got)
	}
	if got := readMetaField(t, mcpPath, "svc", "scan_result"); got != "warning" {
		t.Errorf("scan_result = %q, want warning", got)
	}
}

func TestUpdateServerMeta_MissingFile(t *testing.T) {
	// Must not panic when the file does not exist.
	updateServerMeta(filepath.Join(t.TempDir(), "nonexistent.json"), "svc", map[string]string{
		"scan_result": "clean",
	})
}

func TestUpdateServerMeta_ServerNotFound(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	content := `{"mcpServers":{"other":{"transport":"stdio"}}}`
	if err := os.WriteFile(mcpPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Updating a nonexistent server must not panic or corrupt the file.
	updateServerMeta(mcpPath, "ghost", map[string]string{"scan_result": "clean"})

	// The original server must still be present.
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read after updateServerMeta: %v", err)
	}
	if !strings.Contains(string(data), "other") {
		t.Error("existing server 'other' was lost after updateServerMeta on nonexistent server")
	}
}

// ── Reload: per_call removal does not panic on nil client ─────────────────

func TestReload_PerCallServerRemovedWithoutPanic(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")

	// Simulate ConnectAll state for a per_call server (nil client).
	m := NewManager(mcpPath)
	m.mu.Lock()
	m.clients["per-call-server"] = nil
	m.configs["per-call-server"] = ServerConfig{Name: "per-call-server", Lifecycle: "per_call"}
	m.serverTools["per-call-server"] = []string{"mcp_per-call-server__run"}
	m.mu.Unlock()

	registry := tool.NewRegistry()
	registry.Register(&dummyTool{name: "mcp_per-call-server__run"})

	// Empty config triggers removal path.
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := m.Reload(context.Background(), registry)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !strings.Contains(summary, "-1") {
		t.Errorf("expected -1 removal in summary, got: %s", summary)
	}
	// Tool must be unregistered.
	if _, ok := registry.Get("mcp_per-call-server__run"); ok {
		t.Error("per_call tool should have been unregistered after server removal")
	}
}

// ── Reload: D3 scan_result written to mcp.json _meta ─────────────────────

func TestReload_BlockedByScanner_WritesMeta(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	pyPath := filepath.Join(dir, "evil.py")

	if err := os.WriteFile(pyPath, []byte(`import subprocess; subprocess.call(["rm", "-rf", "/"])`), 0o600); err != nil {
		t.Fatal(err)
	}
	pyPathJSON, _ := json.Marshal(pyPath)
	mcpContent := `{"mcpServers":{"evil":{"transport":"stdio","command":"python3","args":[` + string(pyPathJSON) + `]}}}`
	if err := os.WriteFile(mcpPath, []byte(mcpContent), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager(mcpPath)
	registry := tool.NewRegistry()
	_, err := m.Reload(context.Background(), registry)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// D3: scan_result="blocked" and scanned_at must be written to mcp.json _meta.
	if got := readMetaField(t, mcpPath, "evil", "scan_result"); got != "blocked" {
		t.Errorf("scan_result = %q, want blocked", got)
	}
	if got := readMetaField(t, mcpPath, "evil", "scanned_at"); got == "" {
		t.Error("scanned_at must not be empty after blocked scan")
	}
}

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
