package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMCPToolAdapter_Name(t *testing.T) {
	tests := []struct {
		serverName string
		toolName   string
		wantName   string
	}{
		// Double underscore (__) separates server and tool names unambiguously.
		// This prevents collisions when either component contains underscores.
		{"csv-tool", "read_csv", "mcp_csv-tool__read_csv"},
		{"memory", "store", "mcp_memory__store"},
		{"my_server", "get_weather", "mcp_my_server__get_weather"},
	}
	for _, tc := range tests {
		t.Run(tc.wantName, func(t *testing.T) {
			adapter := NewMCPToolAdapter(
				tc.serverName,
				ToolInfo{Name: tc.toolName},
				nil, // client not needed for Name()
				ServerConfig{},
			)
			if got := adapter.Name(); got != tc.wantName {
				t.Errorf("Name() = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestMCPToolAdapter_InputSchema_Passthrough(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)
	adapter := NewMCPToolAdapter("svc", ToolInfo{Name: "search", InputSchema: schema}, nil, ServerConfig{})

	got := adapter.InputSchema()
	if string(got) != string(schema) {
		t.Errorf("InputSchema() = %s, want %s", got, schema)
	}
}

func TestMCPToolAdapter_InputSchema_EmptyFallback(t *testing.T) {
	// When the MCP server provides no schema, we return a valid empty schema.
	adapter := NewMCPToolAdapter("svc", ToolInfo{Name: "noop"}, nil, ServerConfig{})
	schema := adapter.InputSchema()

	var obj map[string]any
	if err := json.Unmarshal(schema, &obj); err != nil {
		t.Fatalf("empty fallback schema is not valid JSON: %v", err)
	}
}

func TestMCPToolAdapter_Description(t *testing.T) {
	adapter := NewMCPToolAdapter("svc", ToolInfo{Name: "t", Description: "Does things"}, nil, ServerConfig{})
	if got := adapter.Description(); got != "Does things" {
		t.Errorf("Description() = %q", got)
	}
}

func TestMCPToolAdapter_Execute_InvalidJSON(t *testing.T) {
	// Invalid JSON args should return a ToolResult.Error, not a Go error.
	adapter := NewMCPToolAdapter("svc", ToolInfo{Name: "t"}, NewClient(ServerConfig{}), ServerConfig{})
	result, err := adapter.Execute(context.Background(), json.RawMessage(`{bad json}`))
	if err != nil {
		t.Fatalf("Execute returned Go error; want ToolResult.Error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected ToolResult.Error for invalid JSON args")
	}
}

func TestMCPToolAdapter_Execute_NullArgs(t *testing.T) {
	// "null" args are valid (no-arg tools) — should not panic or error during unmarshal.
	// Since there's no real server, we expect a connection error, not an unmarshal error.
	adapter := NewMCPToolAdapter("svc", ToolInfo{Name: "noop"}, NewClient(ServerConfig{}), ServerConfig{})
	result, err := adapter.Execute(context.Background(), json.RawMessage(`null`))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	// We expect a connection error in ToolResult.Error (client not connected),
	// but no unmarshal error.
	if result.Error == "" {
		t.Error("expected some ToolResult.Error (client not connected)")
	}
}

func TestMCPToolAdapter_Init_Close(t *testing.T) {
	// Init and Close on adapter must always be no-ops.
	adapter := NewMCPToolAdapter("svc", ToolInfo{Name: "t"}, nil, ServerConfig{})
	if err := adapter.Init(context.Background()); err != nil {
		t.Errorf("Init() error: %v", err)
	}
	if err := adapter.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}
