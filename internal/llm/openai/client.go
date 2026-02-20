package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/llm"
	openailib "github.com/sashabaranov/go-openai"
)

// Client implements llm.LLMProvider using the OpenAI-compatible protocol.
// Works with any endpoint that supports the OpenAI chat completions API.
type Client struct {
	client *openailib.Client
	config *Config
}

// GetConfig returns the client's configuration.
func (c *Client) GetConfig() *Config {
	return c.config
}

// NewClient creates a new OpenAI-compatible client.
func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	clientConfig := openailib.DefaultConfig(config.APIKey)
	if config.BaseURL != "" {
		clientConfig.BaseURL = config.BaseURL
	}
	// Prevent indefinite hangs when the API is unresponsive.
	// Timeout is configurable via LLM_HTTP_TIMEOUT (seconds); default 300s to
	// accommodate slow reasoning models (e.g. Kimi-K2.5, DeepSeek-R1).
	httpTimeout := time.Duration(config.HTTPTimeout) * time.Second
	clientConfig.HTTPClient = &http.Client{Timeout: httpTimeout}

	return &Client{
		client: openailib.NewClientWithConfig(clientConfig),
		config: config,
	}, nil
}

// NewClientFromEnv creates a client using environment variables.
func NewClientFromEnv() (*Client, error) {
	config, err := NewConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load config from env: %w", err)
	}
	return NewClient(config)
}

// CallLLM sends messages to the LLM and returns the response.
func (c *Client) CallLLM(ctx context.Context, messages []llm.Message) (llm.Message, error) {
	if len(messages) == 0 {
		return llm.Message{}, fmt.Errorf("no messages to send")
	}

	// Convert to OpenAI format
	openaiMsgs := make([]openailib.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		openaiMsgs[i] = openailib.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// Build request
	req := openailib.ChatCompletionRequest{
		Model:    c.config.Model,
		Messages: openaiMsgs,
	}
	if c.config.Temperature != nil {
		req.Temperature = *c.config.Temperature
	}
	if c.config.MaxTokens > 0 {
		req.MaxTokens = c.config.MaxTokens
	}
	// Enable native thinking for supported models
	if c.config.ResolveThinkingMode() == "native" {
		req.ReasoningEffort = "medium"
	}

	// Execute with retries
	var resp openailib.ChatCompletionResponse
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		resp, lastErr = c.client.CreateChatCompletion(ctx, req)
		if lastErr == nil {
			break
		}
		if attempt < c.config.MaxRetries {
			wait := time.Duration(attempt+1) * time.Second
			log.Printf("[LLM] Retry %d/%d after %v, error: %v", attempt+1, c.config.MaxRetries, wait, lastErr)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return llm.Message{}, ctx.Err()
			}
		}
	}

	if lastErr != nil {
		return llm.Message{}, fmt.Errorf("LLM call failed after %d retries: %w", c.config.MaxRetries, lastErr)
	}

	if len(resp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices returned from LLM")
	}

	return llm.Message{
		Role:             llm.RoleAssistant,
		Content:          resp.Choices[0].Message.Content,
		ReasoningContent: resp.Choices[0].Message.ReasoningContent,
	}, nil
}

// CallLLMStream sends messages and streams the response token-by-token.
// Each delta chunk triggers the onChunk callback.
// Returns the full assembled message once streaming finishes.
func (c *Client) CallLLMStream(ctx context.Context, messages []llm.Message, onChunk llm.StreamCallback) (llm.Message, error) {
	// Fallback to synchronous call when no callback is provided
	if onChunk == nil {
		return c.CallLLM(ctx, messages)
	}

	if len(messages) == 0 {
		return llm.Message{}, fmt.Errorf("no messages to send")
	}

	// Convert to OpenAI format
	openaiMsgs := make([]openailib.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		openaiMsgs[i] = openailib.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	req := openailib.ChatCompletionRequest{
		Model:    c.config.Model,
		Messages: openaiMsgs,
		Stream:   true,
	}
	if c.config.Temperature != nil {
		req.Temperature = *c.config.Temperature
	}
	if c.config.MaxTokens > 0 {
		req.MaxTokens = c.config.MaxTokens
	}
	// Enable native thinking for supported models
	if c.config.ResolveThinkingMode() == "native" {
		req.ReasoningEffort = "medium"
	}

	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		// Fallback to synchronous call on stream creation failure
		log.Printf("[LLM] Stream creation failed, falling back to sync: %v", err)
		return c.CallLLM(ctx, messages)
	}
	defer stream.Close()

	var sb strings.Builder
	var reasoningSB strings.Builder
	for {
		chunkResp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// If we have partial content, return it
			if sb.Len() > 0 {
				log.Printf("[LLM] Stream interrupted after %d chars: %v", sb.Len(), err)
				break
			}
			return llm.Message{}, fmt.Errorf("stream recv error: %w", err)
		}

		if len(chunkResp.Choices) > 0 {
			// Collect reasoning content (native thinking)
			if rc := chunkResp.Choices[0].Delta.ReasoningContent; rc != "" {
				reasoningSB.WriteString(rc)
			}
			// Collect normal content
			if delta := chunkResp.Choices[0].Delta.Content; delta != "" {
				sb.WriteString(delta)
				onChunk(delta)
			}
		}
	}

	return llm.Message{
		Role:             llm.RoleAssistant,
		Content:          sb.String(),
		ReasoningContent: reasoningSB.String(),
	}, nil
}

// CallLLMWithTools sends messages with tool definitions for Function Calling.
// Always uses non-streaming mode. The model may return tool_calls or direct text.
func (c *Client) CallLLMWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (llm.Message, error) {
	if len(messages) == 0 {
		return llm.Message{}, fmt.Errorf("no messages to send")
	}

	// Convert messages to OpenAI format
	openaiMsgs := make([]openailib.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		openaiMsgs[i] = openailib.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
		// Handle tool result messages (role="tool")
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			openaiMsgs[i].ToolCallID = msg.ToolCallID
			if msg.Name != "" {
				openaiMsgs[i].Name = msg.Name
			}
		}
		// Handle assistant messages with tool calls (role="assistant" + tool_calls)
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			openaiTCs := make([]openailib.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				openaiTCs[j] = openailib.ToolCall{
					ID:   tc.ID,
					Type: openailib.ToolTypeFunction,
					Function: openailib.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				}
			}
			openaiMsgs[i].ToolCalls = openaiTCs
		}
	}

	// Convert tool definitions to OpenAI format
	openaiTools := make([]openailib.Tool, len(tools))
	for i, t := range tools {
		openaiTools[i] = openailib.Tool{
			Type: openailib.ToolTypeFunction,
			Function: &openailib.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}

	// Build request (non-streaming)
	req := openailib.ChatCompletionRequest{
		Model:    c.config.Model,
		Messages: openaiMsgs,
		Tools:    openaiTools,
	}
	if c.config.Temperature != nil {
		req.Temperature = *c.config.Temperature
	}
	if c.config.MaxTokens > 0 {
		req.MaxTokens = c.config.MaxTokens
	}

	// Execute with retries
	var resp openailib.ChatCompletionResponse
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		resp, lastErr = c.client.CreateChatCompletion(ctx, req)
		if lastErr == nil {
			break
		}
		if attempt < c.config.MaxRetries {
			wait := time.Duration(attempt+1) * time.Second
			log.Printf("[LLM] FC retry %d/%d after %v, error: %v", attempt+1, c.config.MaxRetries, wait, lastErr)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return llm.Message{}, ctx.Err()
			}
		}
	}

	if lastErr != nil {
		return llm.Message{}, fmt.Errorf("FC call failed after %d retries: %w", c.config.MaxRetries, lastErr)
	}

	if len(resp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices returned from LLM (FC)")
	}

	choice := resp.Choices[0].Message

	// Build result message
	result := llm.Message{
		Role:             llm.RoleAssistant,
		Content:          choice.Content,
		ReasoningContent: choice.ReasoningContent,
	}

	// Extract tool calls if present
	if len(choice.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, len(choice.ToolCalls))
		for i, tc := range choice.ToolCalls {
			result.ToolCalls[i] = llm.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			}
		}
		names := make([]string, len(result.ToolCalls))
		for i, tc := range result.ToolCalls {
			names[i] = tc.Name
		}
		log.Printf("[LLM] FC returned %d tool call(s): %s", len(result.ToolCalls), strings.Join(names, ", "))
	}

	return result, nil
}

// IsToolCallingEnabled reports whether Function Calling is enabled for this client.
func (c *Client) IsToolCallingEnabled() bool {
	mode := c.config.ResolveToolCallMode()
	return mode == "fc"
}

// GetName returns the provider name.
func (c *Client) GetName() string {
	return fmt.Sprintf("openai-compatible (%s)", c.config.Model)
}
