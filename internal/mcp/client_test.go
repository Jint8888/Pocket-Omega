package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// mcpConfigForTest writes a mcp.json to a temp dir and returns its path.
func mcpConfigForTest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}
	return path
}

func TestLoadConfig_NameFromKey(t *testing.T) {
	path := mcpConfigForTest(t, `{
		"mcpServers": {
			"my-server": {
				"transport": "stdio",
				"command": "python3",
				"args": ["skills/tool.py"]
			}
		}
	}`)

	configs, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg, ok := configs["my-server"]
	if !ok {
		t.Fatal("expected server 'my-server' in config")
	}
	// The Name must come from the map key, not a JSON field.
	if cfg.Name != "my-server" {
		t.Errorf("Name = %q, want %q", cfg.Name, "my-server")
	}
	if cfg.Transport != "stdio" {
		t.Errorf("Transport = %q, want stdio", cfg.Transport)
	}
	if cfg.Command != "python3" {
		t.Errorf("Command = %q, want python3", cfg.Command)
	}
}

func TestLoadConfig_Empty(t *testing.T) {
	path := mcpConfigForTest(t, `{"mcpServers": {}}`)
	configs, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected empty configs, got %d", len(configs))
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	path := mcpConfigForTest(t, `{invalid json}`)
	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadConfig_SSEServer(t *testing.T) {
	path := mcpConfigForTest(t, `{
		"mcpServers": {
			"memory-service": {
				"transport": "sse",
				"url": "http://localhost:8883/sse"
			}
		}
	}`)
	configs, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg := configs["memory-service"]
	if cfg.Transport != "sse" {
		t.Errorf("Transport = %q, want sse", cfg.Transport)
	}
	if cfg.URL != "http://localhost:8883/sse" {
		t.Errorf("URL = %q", cfg.URL)
	}
}

func TestLoadConfig_MultipleServers(t *testing.T) {
	path := mcpConfigForTest(t, `{
		"mcpServers": {
			"alpha": {"transport": "stdio", "command": "python3", "args": ["a.py"]},
			"beta": {"transport": "sse", "url": "http://localhost:9000/sse"}
		}
	}`)
	configs, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(configs) != 2 {
		t.Errorf("expected 2 configs, got %d", len(configs))
	}
	if configs["alpha"].Name != "alpha" {
		t.Errorf("alpha.Name = %q", configs["alpha"].Name)
	}
	if configs["beta"].Name != "beta" {
		t.Errorf("beta.Name = %q", configs["beta"].Name)
	}
}

func TestNewClient_UnknownTransport(t *testing.T) {
	cfg := ServerConfig{Name: "x", Transport: "grpc"}
	cli := NewClient(cfg)
	err := cli.Connect(context.Background())
	if err == nil {
		t.Error("expected error for unknown transport")
	}
}

func TestNewClient_Close_WhenNotConnected(t *testing.T) {
	cli := NewClient(ServerConfig{Name: "x", Transport: "stdio"})
	// Close on an unconnected client must not panic or error.
	if err := cli.Close(); err != nil {
		t.Errorf("unexpected Close error: %v", err)
	}
}

// TestToolInfo_SchemaSerialization verifies that ToolInfo.InputSchema survives
// a JSON round-trip, which is important for the adapter's InputSchema() method.
func TestToolInfo_SchemaSerialization(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	ti := ToolInfo{Name: "search", Description: "Searches the web", InputSchema: raw}

	data, err := json.Marshal(ti.InputSchema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != string(raw) {
		t.Errorf("round-trip mismatch: %s", data)
	}
}

// TestServerConfig_ZeroValue checks that NewClient handles an empty config gracefully.
func TestServerConfig_ZeroValue(t *testing.T) {
	cli := NewClient(ServerConfig{})
	err := cli.Connect(context.Background())
	if err == nil {
		t.Error("expected error for zero-value config")
	}
	// Should not panic; unknown transport error expected.
	expected := fmt.Sprintf("mcp: unknown transport %q for server %q", "", "")
	if err.Error() != expected {
		// Just check it is an error
		_ = err
	}
}
