package llm

import (
	"context"
	"encoding/json"
)

// Message represents a chat message for LLM communication.
type Message struct {
	Role       string     `json:"role"`                   // "user", "assistant", "system", "tool"
	Content    string     `json:"content"`                // The message text
	Name       string     `json:"name,omitempty"`         // FC: function name when role="tool"
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // FC: tool calls returned by model
	ToolCallID string     `json:"tool_call_id,omitempty"` // FC: when role="tool", the ID of the call this responds to
}

// ToolDefinition describes a tool for Function Calling.
// Parameters follows OpenAI JSON Schema format.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// ToolCall represents a single tool call returned by the model.
type ToolCall struct {
	ID        string          `json:"id"` // Required: OpenAI uses this to correlate tool results
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// StreamCallback is invoked for each chunk of streamed text.
// Implementations should be lightweight; heavy work should be deferred.
type StreamCallback func(chunk string)

// LLMProvider defines the interface for all LLM implementations.
// Any OpenAI-compatible endpoint (litellm, Ollama, Azure, vLLM, etc.)
// can be used by implementing this interface.
type LLMProvider interface {
	// CallLLM sends messages to the LLM and returns the complete response.
	CallLLM(ctx context.Context, messages []Message) (Message, error)

	// CallLLMStream sends messages and streams the response token-by-token.
	// Each chunk of text triggers the onChunk callback.
	// Returns the full assembled message once streaming finishes.
	// If the provider does not support streaming, it may fall back to CallLLM.
	CallLLMStream(ctx context.Context, messages []Message, onChunk StreamCallback) (Message, error)

	// CallLLMWithTools sends messages with tool definitions for Function Calling.
	// The model may return tool_calls in the response or a direct text answer.
	// This method always uses non-streaming mode.
	CallLLMWithTools(ctx context.Context, messages []Message, tools []ToolDefinition) (Message, error)

	// IsToolCallingEnabled reports whether Function Calling is currently enabled
	// for this provider. This reflects configuration (ToolCallMode), not just
	// model capability — returns false when mode="yaml" even if model supports FC.
	IsToolCallingEnabled() bool
}

// Role constants.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)
