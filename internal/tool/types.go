package tool

import (
	"context"
	"encoding/json"
)

// Tool is the unified interface for all tools.
// Both native built-in tools and MCP tool adapters implement this interface.
type Tool interface {
	// Name returns the tool identifier (LLM uses this name to invoke the tool).
	Name() string

	// Description returns a natural-language description for LLM prompt injection.
	Description() string

	// InputSchema returns a standard JSON Schema defining the tool's parameters.
	// Compatible with MCP protocol and OpenAI Function Calling.
	InputSchema() json.RawMessage

	// Execute runs the tool with JSON-encoded arguments.
	Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)

	// Init initializes tool resources (e.g. MCP client connections).
	// Native tools may return nil.
	Init(ctx context.Context) error

	// Close releases tool resources.
	Close() error
}

// ToolResult encapsulates a tool execution result.
type ToolResult struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// SchemaParam describes a single parameter for the SchemaBuilder helper.
type SchemaParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // "string", "integer", "boolean", "number"
	Description string   `json:"description"`
	Required    bool     `json:"-"`
	Enum        []string `json:"enum,omitempty"`
}

// BuildSchema generates a standard JSON Schema object from a list of SchemaParams.
// This helper lets native tools avoid hand-writing JSON strings.
//
// Output example:
//
//	{"type":"object","properties":{"command":{"type":"string","description":"要执行的命令"}},"required":["command"]}
func BuildSchema(params ...SchemaParam) json.RawMessage {
	properties := make(map[string]any)
	var required []string

	for _, p := range params {
		prop := map[string]any{
			"type":        p.Type,
			"description": p.Description,
		}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		properties[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	data, _ := json.Marshal(schema)
	return data
}
