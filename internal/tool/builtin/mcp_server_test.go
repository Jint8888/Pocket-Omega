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

// writeTempMCPFile writes a mcp.json to a temp dir and returns its path.
func writeTempMCPFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTempMCPFile: %v", err)
	}
	return path
}

// readMCPEntry reads a specific server entry from mcp.json for assertions.
func readMCPEntry(t *testing.T, path, serverName string) mcpServerEntry {
	t.Helper()
	cfg, err := readMCPConfig(path)
	if err != nil {
		t.Fatalf("readMCPConfig: %v", err)
	}
	entry, ok := cfg.MCPServers[serverName]
	if !ok {
		t.Fatalf("server %q not found in mcp.json", serverName)
	}
	return entry
}

// ── mcp_server_add ────────────────────────────────────────────────────────

func TestMCPServerAdd_Success(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "my-server",
		"transport": "stdio",
		"command":   "python3",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected ToolResult.Error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "my-server") {
		t.Errorf("output should mention server name, got: %s", result.Output)
	}

	entry := readMCPEntry(t, path, "my-server")
	if entry.Transport != "stdio" {
		t.Errorf("transport = %q, want stdio", entry.Transport)
	}
	if entry.Command != "python3" {
		t.Errorf("command = %q, want python3", entry.Command)
	}
}

func TestMCPServerAdd_InjectsOriginAgent(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "origin-test",
		"transport": "stdio",
		"command":   "node",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || result.Error != "" {
		t.Fatalf("Execute failed: err=%v / ToolResult.Error=%s", err, result.Error)
	}

	entry := readMCPEntry(t, path, "origin-test")
	if entry.Meta["origin"] != "agent" {
		t.Errorf("_meta.origin = %q, want \"agent\"", entry.Meta["origin"])
	}
}

func TestMCPServerAdd_DuplicateName(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{"existing":{"transport":"stdio"}}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "existing",
		"transport": "stdio",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for duplicate name")
	}
}

func TestMCPServerAdd_EmptyName(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "",
		"transport": "stdio",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for empty name")
	}
}

func TestMCPServerAdd_InvalidTransport(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "test",
		"transport": "grpc",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for invalid transport")
	}
}

func TestMCPServerAdd_ParsesArgsJSON(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "ts-server",
		"transport": "stdio",
		"command":   "node",
		"args":      `["--import","tsx","skills/server.ts"]`,
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || result.Error != "" {
		t.Fatalf("Execute failed: err=%v / ToolResult.Error=%s", err, result.Error)
	}

	entry := readMCPEntry(t, path, "ts-server")
	if len(entry.Args) != 3 {
		t.Errorf("args len = %d, want 3; got %v", len(entry.Args), entry.Args)
	}
	if entry.Args[0] != "--import" || entry.Args[2] != "skills/server.ts" {
		t.Errorf("args content mismatch: %v", entry.Args)
	}
}

func TestMCPServerAdd_InvalidArgsJSON(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "test",
		"transport": "stdio",
		"args":      `not-a-json-array`,
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for malformed args JSON")
	}
}

func TestMCPServerAdd_SSETransport(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "sse-server",
		"transport": "sse",
		"url":       "http://localhost:8080",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || result.Error != "" {
		t.Fatalf("Execute failed: err=%v / ToolResult.Error=%s", err, result.Error)
	}

	entry := readMCPEntry(t, path, "sse-server")
	if entry.URL != "http://localhost:8080" {
		t.Errorf("URL = %q, want http://localhost:8080", entry.URL)
	}
}

func TestMCPServerAdd_LifecyclePerCall(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":      "pc-server",
		"transport": "stdio",
		"command":   "node",
		"lifecycle": "per_call",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil || result.Error != "" {
		t.Fatalf("Execute failed: err=%v / ToolResult.Error=%s", err, result.Error)
	}

	entry := readMCPEntry(t, path, "pc-server")
	if entry.Lifecycle != "per_call" {
		t.Errorf("lifecycle = %q, want per_call", entry.Lifecycle)
	}
}

func TestMCPServerAdd_InvalidParamsJSON(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerAddTool(path)

	result, err := tool.Execute(context.Background(), []byte(`{not valid}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for invalid JSON params")
	}
}

// ── mcp_server_remove ─────────────────────────────────────────────────────

func TestMCPServerRemove_NoConfirm(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{"target":{"transport":"stdio"}}}`)
	tool := NewMCPServerRemoveTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name": "target",
		// confirm intentionally omitted
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error when confirm is missing")
	}

	// Server must NOT have been deleted.
	cfg, _ := readMCPConfig(path)
	if _, ok := cfg.MCPServers["target"]; !ok {
		t.Error("server was deleted despite missing confirm")
	}
}

func TestMCPServerRemove_WrongConfirm(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{"target":{"transport":"stdio"}}}`)
	tool := NewMCPServerRemoveTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":    "target",
		"confirm": "ok", // wrong value
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for wrong confirm value")
	}

	cfg, _ := readMCPConfig(path)
	if _, ok := cfg.MCPServers["target"]; !ok {
		t.Error("server was deleted despite wrong confirm value")
	}
}

func TestMCPServerRemove_Success(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{"target":{"transport":"stdio"}}}`)
	tool := NewMCPServerRemoveTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":    "target",
		"confirm": "yes",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected ToolResult.Error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "target") {
		t.Errorf("output should mention removed server name, got: %s", result.Output)
	}

	cfg, _ := readMCPConfig(path)
	if _, ok := cfg.MCPServers["target"]; ok {
		t.Error("server still present in mcp.json after successful remove")
	}
}

func TestMCPServerRemove_EmptyName(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerRemoveTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":    "",
		"confirm": "yes",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for empty name")
	}
}

func TestMCPServerRemove_NotExists(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerRemoveTool(path)

	raw, _ := json.Marshal(map[string]any{
		"name":    "ghost",
		"confirm": "yes",
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error when server does not exist")
	}
}

func TestMCPServerRemove_InvalidParamsJSON(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerRemoveTool(path)

	result, err := tool.Execute(context.Background(), []byte(`{not valid}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for invalid JSON params")
	}
}

// ── mcp_server_list ───────────────────────────────────────────────────────

func TestMCPServerList_Empty(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)
	tool := NewMCPServerListTool(path)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected ToolResult.Error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "暂无") {
		t.Errorf("expected empty-list message containing '暂无', got: %s", result.Output)
	}
}

func TestMCPServerList_WithEntries(t *testing.T) {
	content := `{"mcpServers":{` +
		`"alpha":{"transport":"stdio","command":"python3","args":["server.py"],"lifecycle":"persistent"},` +
		`"beta":{"transport":"sse","url":"http://localhost:9090"}` +
		`}}`
	path := writeTempMCPFile(t, content)
	tool := NewMCPServerListTool(path)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected ToolResult.Error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "alpha") {
		t.Errorf("output missing 'alpha': %s", result.Output)
	}
	if !strings.Contains(result.Output, "beta") {
		t.Errorf("output missing 'beta': %s", result.Output)
	}
	if !strings.Contains(result.Output, "http://localhost:9090") {
		t.Errorf("output missing SSE URL: %s", result.Output)
	}
}

func TestMCPServerList_ShowsMeta(t *testing.T) {
	content := `{"mcpServers":{` +
		`"scanned":{"transport":"stdio","command":"node","_meta":{"origin":"agent","scan_result":"clean","scanned_at":"2026-02-20"}}` +
		`}}`
	path := writeTempMCPFile(t, content)
	tool := NewMCPServerListTool(path)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result.Output, "clean") {
		t.Errorf("output missing scan_result 'clean': %s", result.Output)
	}
	if !strings.Contains(result.Output, "agent") {
		t.Errorf("output missing origin 'agent': %s", result.Output)
	}
	if !strings.Contains(result.Output, "2026-02-20") {
		t.Errorf("output missing scanned_at date: %s", result.Output)
	}
}

func TestMCPServerList_DefaultLifecycle(t *testing.T) {
	// A server without explicit lifecycle should display as "persistent".
	content := `{"mcpServers":{"nolife":{"transport":"stdio","command":"node"}}}`
	path := writeTempMCPFile(t, content)
	tool := NewMCPServerListTool(path)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result.Output, "persistent") {
		t.Errorf("expected default lifecycle 'persistent' in output, got: %s", result.Output)
	}
}

func TestMCPServerList_DefaultOrigin(t *testing.T) {
	// A server without _meta.origin should display as "user".
	content := `{"mcpServers":{"noorigin":{"transport":"stdio","command":"node"}}}`
	path := writeTempMCPFile(t, content)
	tool := NewMCPServerListTool(path)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result.Output, "user") {
		t.Errorf("expected default origin 'user' in output, got: %s", result.Output)
	}
}

func TestMCPServerList_SortedOutput(t *testing.T) {
	// Output should be sorted by server name for deterministic results.
	content := `{"mcpServers":{` +
		`"zzz":{"transport":"stdio","command":"a"},` +
		`"aaa":{"transport":"stdio","command":"b"},` +
		`"mmm":{"transport":"stdio","command":"c"}` +
		`}}`
	path := writeTempMCPFile(t, content)
	tool := NewMCPServerListTool(path)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	posAAA := strings.Index(result.Output, "aaa")
	posMMM := strings.Index(result.Output, "mmm")
	posZZZ := strings.Index(result.Output, "zzz")
	if posAAA == -1 || posMMM == -1 || posZZZ == -1 {
		t.Fatalf("not all servers found in output: %s", result.Output)
	}
	if !(posAAA < posMMM && posMMM < posZZZ) {
		t.Errorf("output not sorted alphabetically: aaa@%d mmm@%d zzz@%d", posAAA, posMMM, posZZZ)
	}
}

// ── Init / Close ──────────────────────────────────────────────────────────

func TestMCPServerTools_InitClose(t *testing.T) {
	path := writeTempMCPFile(t, `{"mcpServers":{}}`)

	tools := []interface {
		Init(context.Context) error
		Close() error
	}{
		NewMCPServerAddTool(path),
		NewMCPServerRemoveTool(path),
		NewMCPServerListTool(path),
	}
	for _, tool := range tools {
		if err := tool.Init(context.Background()); err != nil {
			t.Errorf("Init() error: %v", err)
		}
		if err := tool.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	}
}

// ── readMCPConfig / writeMCPConfig round-trip ─────────────────────────────

func TestReadMCPConfig_FileNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	cfg, err := readMCPConfig(path)
	if err != nil {
		t.Errorf("readMCPConfig on missing file should return empty config, got error: %v", err)
	}
	if cfg.MCPServers == nil {
		t.Error("MCPServers map should be initialized (not nil) for missing file")
	}
	if len(cfg.MCPServers) != 0 {
		t.Errorf("expected empty MCPServers, got %d entries", len(cfg.MCPServers))
	}
}

func TestReadMCPConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readMCPConfig(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReadWriteMCPConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")

	original := mcpConfig{
		MCPServers: map[string]mcpServerEntry{
			"server1": {
				Transport: "stdio",
				Command:   "python3",
				Args:      []string{"--port", "8080"},
				Lifecycle: "per_call",
				Meta:      map[string]string{"origin": "agent"},
			},
		},
	}
	if err := writeMCPConfig(path, original); err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}

	read, err := readMCPConfig(path)
	if err != nil {
		t.Fatalf("readMCPConfig: %v", err)
	}

	entry, ok := read.MCPServers["server1"]
	if !ok {
		t.Fatal("server1 not found after round-trip")
	}
	if entry.Transport != "stdio" {
		t.Errorf("transport = %q, want stdio", entry.Transport)
	}
	if entry.Lifecycle != "per_call" {
		t.Errorf("lifecycle = %q, want per_call", entry.Lifecycle)
	}
	if entry.Meta["origin"] != "agent" {
		t.Errorf("_meta.origin = %q, want agent", entry.Meta["origin"])
	}
	if len(entry.Args) != 2 || entry.Args[1] != "8080" {
		t.Errorf("args mismatch: %v", entry.Args)
	}
}
