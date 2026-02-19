package mcp

import (
	"context"
	"encoding/json"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ReloadTool implements tool.Tool and exposes the "mcp_reload" built-in command.
// When invoked by the agent, it triggers a diff-based hot reload of mcp.json:
//   - New servers: scanned (if stdio Python), then connected and registered.
//   - Removed servers: their tools are unregistered and connections closed.
//   - Unchanged servers: left untouched.
//
// The tool takes no input parameters and returns a human-readable summary.
type ReloadTool struct {
	manager  *Manager
	registry *tool.Registry
}

// NewReloadTool creates a ReloadTool wired to the given manager and registry.
func NewReloadTool(manager *Manager, registry *tool.Registry) *ReloadTool {
	return &ReloadTool{manager: manager, registry: registry}
}

func (t *ReloadTool) Name() string { return "mcp_reload" }

func (t *ReloadTool) Description() string {
	return "Reloads the MCP server configuration from mcp.json. " +
		"Connects new servers, disconnects removed servers, and re-registers all tools. " +
		"New stdio Python servers are security-scanned before activation. " +
		"Returns a summary of changes made."
}

// InputSchema returns an empty schema — mcp_reload accepts no arguments.
func (t *ReloadTool) InputSchema() json.RawMessage {
	return tool.BuildSchema()
}

// Execute triggers the hot-reload and returns a change summary.
func (t *ReloadTool) Execute(ctx context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	summary, err := t.manager.Reload(ctx, t.registry)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}
	return tool.ToolResult{Output: summary}, nil
}

// Init is a no-op; ReloadTool has no additional initialisation requirements.
func (t *ReloadTool) Init(_ context.Context) error { return nil }

// Close is a no-op; lifecycle is managed by Manager.
func (t *ReloadTool) Close() error { return nil }
