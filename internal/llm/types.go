package llm

import "context"

// Message represents a chat message for LLM communication.
type Message struct {
	Role             string `json:"role"`                        // "user", "assistant", "system"
	Content          string `json:"content"`                     // The message text
	ReasoningContent string `json:"reasoning_content,omitempty"` // Native thinking output (e.g. DeepSeek-R1)
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

	// GetName returns the provider name/identifier.
	GetName() string
}

// Role constants.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)
