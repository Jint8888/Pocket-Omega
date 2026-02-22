package openai

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/pocketomega/pocket-omega/internal/llm"
)

// Config holds OpenAI-compatible LLM configuration.
type Config struct {
	APIKey          string   // API key for authentication
	BaseURL         string   // Base URL (default: https://api.openai.com/v1)
	Model           string   // Model name (default: gpt-4o)
	Temperature     *float32 // Response creativity 0.0-2.0 (nil = API default)
	MaxTokens       int      // Max tokens in response, 0 = no limit
	MaxRetries      int      // HTTP-level retry for transient errors only (default: 1)
	HTTPTimeout     int      // HTTP client timeout in seconds (default: 300)
	ThinkingMode    string   // "auto", "native", or "app" (default: "auto")
	ToolCallMode    string   // "auto", "fc", or "yaml" (default: "auto")
	ContextWindow   int      // context window in tokens (0 = auto-detect from model name)
	ReasoningEffort string   // "low", "medium", or "high" (default: "medium"); only used in native thinking mode
}

// NewConfigFromEnv creates Config from environment variables.
// Expected env vars: LLM_API_KEY, LLM_BASE_URL, LLM_MODEL, LLM_TEMPERATURE, LLM_MAX_TOKENS, LLM_MAX_RETRIES, LLM_THINKING_MODE, LLM_REASONING_EFFORT, LLM_TOOL_CALL_MODE
func NewConfigFromEnv() (*Config, error) {
	config := &Config{
		APIKey:          getEnvOrDefault("LLM_API_KEY", ""),
		BaseURL:         getEnvOrDefault("LLM_BASE_URL", "https://api.openai.com/v1"),
		Model:           getEnvOrDefault("LLM_MODEL", "gpt-4o"),
		Temperature:     getEnvFloat32Ptr("LLM_TEMPERATURE"),
		MaxTokens:       getEnvIntOrDefault("LLM_MAX_TOKENS", 0),
		MaxRetries:      getEnvIntOrDefault("LLM_MAX_RETRIES", 1),
		HTTPTimeout:     getEnvIntOrDefault("LLM_HTTP_TIMEOUT", 300),
		ThinkingMode:    getEnvOrDefault("LLM_THINKING_MODE", "auto"),
		ToolCallMode:    getEnvOrDefault("LLM_TOOL_CALL_MODE", "auto"),
		ContextWindow:   getEnvIntOrDefault("LLM_CONTEXT_WINDOW", 0),
		ReasoningEffort: getEnvOrDefault("LLM_REASONING_EFFORT", "medium"),
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("LLM_API_KEY is required. Set it in .env or environment")
	}
	if c.Model == "" {
		return fmt.Errorf("LLM_MODEL cannot be empty")
	}
	if c.Temperature != nil && (*c.Temperature < 0.0 || *c.Temperature > 2.0) {
		return fmt.Errorf("LLM_TEMPERATURE must be between 0.0 and 2.0, got %f", *c.Temperature)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("LLM_MAX_RETRIES cannot be negative, got %d", c.MaxRetries)
	}
	if c.ThinkingMode != "auto" && c.ThinkingMode != "native" && c.ThinkingMode != "app" {
		return fmt.Errorf("LLM_THINKING_MODE must be 'auto', 'native', or 'app', got %q", c.ThinkingMode)
	}
	if c.ToolCallMode != "auto" && c.ToolCallMode != "fc" && c.ToolCallMode != "yaml" {
		return fmt.Errorf("LLM_TOOL_CALL_MODE must be 'auto', 'fc', or 'yaml', got %q", c.ToolCallMode)
	}
	if c.ReasoningEffort != "low" && c.ReasoningEffort != "medium" && c.ReasoningEffort != "high" {
		return fmt.Errorf("LLM_REASONING_EFFORT must be 'low', 'medium', or 'high', got %q", c.ReasoningEffort)
	}
	return nil
}

// ResolveThinkingMode returns the effective thinking mode.
// When set to "auto", it detects based on the model name.
func (c *Config) ResolveThinkingMode() string {
	if c.ThinkingMode == "native" || c.ThinkingMode == "app" {
		return c.ThinkingMode
	}
	// auto: detect from model name
	cap := llm.DetectThinkingCapability(c.Model)
	if cap.SupportsNativeThinking {
		log.Printf("[Config] Auto-detected native thinking for model %q", c.Model)
		return "native"
	}
	log.Printf("[Config] Model %q does not support native thinking, using app mode", c.Model)
	return "app"
}

// ResolveToolCallMode returns the effective tool call mode.
// When set to "auto", it detects based on the model name.
func (c *Config) ResolveToolCallMode() string {
	if c.ToolCallMode == "fc" || c.ToolCallMode == "yaml" {
		return c.ToolCallMode
	}
	// auto: detect from model name
	if llm.DetectToolCallingCapability(c.Model) {
		log.Printf("[Config] Auto-detected FC support for model %q", c.Model)
		return "fc"
	}
	log.Printf("[Config] Model %q does not support FC, using yaml mode", c.Model)
	return "yaml"
}

// ResolveContextWindow returns the effective context window in tokens.
// Priority: explicit LLM_CONTEXT_WINDOW > auto-detect from model name > 32K safe default.
func (c *Config) ResolveContextWindow() int {
	if c.ContextWindow > 0 {
		return c.ContextWindow
	}
	if w := llm.GetContextWindow(c.Model); w > 0 {
		log.Printf("[Config] Auto-detected context window %d tokens for model %q", w, c.Model)
		return w
	}
	const defaultContextWindow = 32_000
	log.Printf("[Config] Unknown model %q, using default context window %d tokens", c.Model, defaultContextWindow)
	return defaultContextWindow
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvFloat32Ptr(key string) *float32 {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseFloat(v, 32); err == nil {
			f := float32(parsed)
			return &f
		}
		log.Printf("[Config] WARNING: invalid value for %s=%q, ignoring", key, v)
	}
	return nil
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
		log.Printf("[Config] WARNING: invalid value for %s=%q, using default %d", key, v, defaultValue)
	}
	return defaultValue
}
