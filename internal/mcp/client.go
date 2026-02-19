package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	sdk_client "github.com/mark3labs/mcp-go/client"
	sdk_mcp "github.com/mark3labs/mcp-go/mcp"
)

// mcpConfigFile mirrors the top-level structure of mcp.json.
type mcpConfigFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig reads and parses mcp.json from path.
// The Name field of each ServerConfig is populated from the map key,
// not from any JSON field (the JSON value does not include a "name" key).
func LoadConfig(path string) (map[string]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcp: read config %q: %w", path, err)
	}

	var file mcpConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("mcp: parse config %q: %w", path, err)
	}

	if file.MCPServers == nil {
		return map[string]ServerConfig{}, nil
	}

	// Populate Name from the map key.
	for key, cfg := range file.MCPServers {
		cfg.Name = key
		file.MCPServers[key] = cfg
	}
	return file.MCPServers, nil
}

// ServerConfig describes a single MCP server connection.
// The Name field is populated from the map key in mcp.json, not from a JSON field.
type ServerConfig struct {
	Name      string   // derived from the map key in mcp.json
	Transport string   `json:"transport"`           // "stdio" | "sse"
	Command   string   `json:"command,omitempty"`   // stdio: executable path
	Args      []string `json:"args,omitempty"`      // stdio: command arguments
	URL       string   `json:"url,omitempty"`       // sse: base URL
	Env       []string `json:"env,omitempty"`       // stdio: extra environment variables
}

// ToolInfo captures the metadata of a single tool exposed by an MCP server.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Client wraps the mcp-go SDK client for a single MCP server.
// It is safe for concurrent use by multiple goroutines.
type Client struct {
	mu    sync.RWMutex
	cfg   ServerConfig
	inner sdk_client.MCPClient
}

// NewClient creates an uninitialised Client for the given server config.
// Call Connect to establish the connection and complete the MCP handshake.
func NewClient(cfg ServerConfig) *Client {
	return &Client{cfg: cfg}
}

// Connect establishes the transport connection and performs the MCP
// initialize handshake. It must be called before ListTools or CallTool.
func (c *Client) Connect(ctx context.Context) error {
	var inner sdk_client.MCPClient

	switch c.cfg.Transport {
	case "stdio":
		cli, err := sdk_client.NewStdioMCPClient(c.cfg.Command, c.cfg.Env, c.cfg.Args...)
		if err != nil {
			return fmt.Errorf("mcp: start stdio server %q: %w", c.cfg.Name, err)
		}
		inner = cli

	case "sse":
		cli, err := sdk_client.NewSSEMCPClient(c.cfg.URL)
		if err != nil {
			return fmt.Errorf("mcp: create SSE client %q: %w", c.cfg.Name, err)
		}
		if err := cli.Start(ctx); err != nil {
			return fmt.Errorf("mcp: start SSE client %q: %w", c.cfg.Name, err)
		}
		inner = cli

	default:
		return fmt.Errorf("mcp: unknown transport %q for server %q", c.cfg.Transport, c.cfg.Name)
	}

	// MCP initialize handshake; clean up if it fails.
	_, err := inner.Initialize(ctx, sdk_mcp.InitializeRequest{
		Params: sdk_mcp.InitializeParams{
			ProtocolVersion: sdk_mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: sdk_mcp.Implementation{
				Name:    "pocket-omega",
				Version: "0.1.0",
			},
		},
	})
	if err != nil {
		_ = inner.Close() // release resources on handshake failure
		return fmt.Errorf("mcp: initialize server %q: %w", c.cfg.Name, err)
	}

	c.mu.Lock()
	c.inner = inner
	c.mu.Unlock()
	return nil
}

// ListTools returns metadata for all tools exposed by this MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.RLock()
	inner := c.inner
	c.mu.RUnlock()

	if inner == nil {
		return nil, fmt.Errorf("mcp: client %q not connected", c.cfg.Name)
	}

	result, err := inner.ListTools(ctx, sdk_mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools %q: %w", c.cfg.Name, err)
	}

	tools := make([]ToolInfo, 0, len(result.Tools))
	for _, t := range result.Tools {
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			// Non-fatal: use empty schema
			schema = json.RawMessage("{}")
		}
		tools = append(tools, ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return tools, nil
}

// CallTool invokes the named tool on the MCP server with the given arguments
// and returns the concatenated text content.
//
// If the server reports IsError=true, CallTool returns a non-nil error wrapping
// the server-supplied message so callers can distinguish tool errors from
// infrastructure errors.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.RLock()
	inner := c.inner
	c.mu.RUnlock()

	if inner == nil {
		return "", fmt.Errorf("mcp: client %q not connected", c.cfg.Name)
	}

	req := sdk_mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := inner.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("mcp: call tool %q on %q: %w", name, c.cfg.Name, err)
	}

	// Collect text content from the response
	var parts []string
	for _, content := range result.Content {
		if tc, ok := content.(sdk_mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	text := strings.Join(parts, "\n")

	if result.IsError {
		return "", fmt.Errorf("mcp: tool %q returned error: %s", name, text)
	}
	return text, nil
}

// Close terminates the connection to the MCP server and releases resources.
func (c *Client) Close() error {
	c.mu.Lock()
	inner := c.inner
	c.inner = nil
	c.mu.Unlock()

	if inner == nil {
		return nil
	}
	return inner.Close()
}
