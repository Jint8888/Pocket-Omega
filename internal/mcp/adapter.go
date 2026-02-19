package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// MCPToolAdapter bridges an MCP server tool to the tool.Tool interface,
// making it indistinguishable from native built-in tools to the agent.
//
// Naming convention: mcp_<serverName>__<toolName>  (double underscore separator)
// The double underscore is unambiguous — it cannot appear within a valid server
// name or tool name and prevents name collisions when either component contains
// single underscores.
//
// Example: server "csv-tool", tool "read_csv" → "mcp_csv-tool__read_csv"
type MCPToolAdapter struct {
	serverName string
	info       ToolInfo
	client     *Client
}

// NewMCPToolAdapter creates an adapter for a single MCP tool.
func NewMCPToolAdapter(serverName string, info ToolInfo, client *Client) *MCPToolAdapter {
	return &MCPToolAdapter{
		serverName: serverName,
		info:       info,
		client:     client,
	}
}

// Name returns the fully-qualified tool name: mcp_<server>__<tool>.
// The double underscore separates server and tool names unambiguously.
func (a *MCPToolAdapter) Name() string {
	return fmt.Sprintf("mcp_%s__%s", a.serverName, a.info.Name)
}

// Description returns the tool description from the MCP server.
func (a *MCPToolAdapter) Description() string {
	return a.info.Description
}

// InputSchema returns the JSON Schema provided by the MCP server.
func (a *MCPToolAdapter) InputSchema() json.RawMessage {
	if len(a.info.InputSchema) == 0 {
		return tool.BuildSchema() // empty schema
	}
	return a.info.InputSchema
}

// Execute deserialises the JSON args and delegates to the MCP server.
// Infrastructure errors and MCP tool-level errors are both returned as
// a ToolResult.Error (nil Go error) so the agent can react gracefully.
func (a *MCPToolAdapter) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var params map[string]any

	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &params); err != nil {
			return tool.ToolResult{
				Error: fmt.Sprintf("mcp adapter: parse args for %q: %v", a.Name(), err),
			}, nil
		}
	}

	text, err := a.client.CallTool(ctx, a.info.Name, params)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}
	return tool.ToolResult{Output: text}, nil
}

// Init satisfies the tool.Tool interface. MCP connections are managed by the
// Manager; individual adapters have no additional initialisation.
func (a *MCPToolAdapter) Init(_ context.Context) error {
	return nil
}

// Close satisfies the tool.Tool interface. Connection lifecycle is managed
// by the Manager; adapters do not close the shared client.
func (a *MCPToolAdapter) Close() error {
	return nil
}
