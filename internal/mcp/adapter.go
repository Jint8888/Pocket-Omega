package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// mcpToolTimeout caps a single MCP tool call so that a hung MCP server
// (e.g. a Python process with a blocking HTTP call) fails quickly and
// returns control to the agent, which still has the remainder of the
// overall agentTimeout to generate a meaningful answer.
const mcpToolTimeout = 60 * time.Second

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
	// client is the shared persistent connection. For per_call lifecycle it is
	// nil — Execute() creates a fresh Client per invocation using cfg.
	client    *Client
	cfg       ServerConfig // used by per_call Execute to rebuild the connection
	lifecycle string       // "persistent" (default) | "per_call"
}

// NewMCPToolAdapter creates an adapter for a single MCP tool.
// cfg is stored so that Execute can rebuild a transient connection for
// per_call lifecycle servers. For persistent servers client must be non-nil.
func NewMCPToolAdapter(serverName string, info ToolInfo, client *Client, cfg ServerConfig) *MCPToolAdapter {
	lc := cfg.Lifecycle
	if lc == "" {
		lc = "persistent"
	}
	return &MCPToolAdapter{
		serverName: serverName,
		info:       info,
		client:     client,
		cfg:        cfg,
		lifecycle:  lc,
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
//
// For persistent lifecycle: reuses the shared client connection.
// For per_call lifecycle: creates a fresh Client, runs the tool, then
// closes the process. This guarantees no residual processes are left running.
//
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

	if a.lifecycle == "per_call" {
		return a.executePerCall(ctx, params)
	}
	return a.executePersistent(ctx, params)
}

// executePersistent delegates to the long-lived shared client.
// A per-call timeout (mcpToolTimeout) is applied so that a hung MCP server
// does not consume the entire agent budget; the error is returned promptly
// and the agent can still generate a final answer.
func (a *MCPToolAdapter) executePersistent(ctx context.Context, params map[string]any) (tool.ToolResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, mcpToolTimeout)
	defer cancel()
	text, err := a.client.CallTool(callCtx, a.info.Name, params)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}
	return tool.ToolResult{Output: text}, nil
}

// executePerCall creates an ephemeral Client, connects, calls the tool, then
// closes the connection. The child process is terminated by Close().
// mcpToolTimeout bounds the full connect+call sequence.
func (a *MCPToolAdapter) executePerCall(ctx context.Context, params map[string]any) (tool.ToolResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, mcpToolTimeout)
	defer cancel()
	c := NewClient(a.cfg)
	if err := c.Connect(callCtx); err != nil {
		return tool.ToolResult{
			Error: fmt.Sprintf("mcp per_call: connect to %q: %v", a.cfg.Name, err),
		}, nil
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup

	text, err := c.CallTool(callCtx, a.info.Name, params)
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
// For per_call adapters, there is no persistent connection to close.
func (a *MCPToolAdapter) Close() error {
	return nil
}
