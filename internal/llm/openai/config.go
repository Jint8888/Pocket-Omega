package openai

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds OpenAI-compatible LLM configuration.
type Config struct {
	APIKey       string   // API key for authentication
	BaseURL      string   // Base URL (default: https://api.openai.com/v1)
	Model        string   // Model name (default: gpt-4o)
	Temperature  *float32 // Response creativity 0.0-2.0 (nil = API default)
	MaxTokens    int      // Max tokens in response, 0 = no limit
	MaxRetries   int      // HTTP-level retry for transient errors only (default: 1)
	ThinkingMode string   // "native" or "app" (default: "native")
}

// NewConfigFromEnv creates Config from environment variables.
// Expected env vars: LLM_API_KEY, LLM_BASE_URL, LLM_MODEL, LLM_TEMPERATURE, LLM_MAX_TOKENS, LLM_MAX_RETRIES, LLM_THINKING_MODE
func NewConfigFromEnv() (*Config, error) {
	config := &Config{
		APIKey:       getEnvOrDefault("LLM_API_KEY", ""),
		BaseURL:      getEnvOrDefault("LLM_BASE_URL", "https://api.openai.com/v1"),
		Model:        getEnvOrDefault("LLM_MODEL", "gpt-4o"),
		Temperature:  getEnvFloat32Ptr("LLM_TEMPERATURE"),
		MaxTokens:    getEnvIntOrDefault("LLM_MAX_TOKENS", 0),
		MaxRetries:   getEnvIntOrDefault("LLM_MAX_RETRIES", 1),
		ThinkingMode: getEnvOrDefault("LLM_THINKING_MODE", "native"),
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
	if c.ThinkingMode != "native" && c.ThinkingMode != "app" {
		return fmt.Errorf("LLM_THINKING_MODE must be 'native' or 'app', got %q", c.ThinkingMode)
	}
	return nil
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
	}
	return nil
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return defaultValue
}
