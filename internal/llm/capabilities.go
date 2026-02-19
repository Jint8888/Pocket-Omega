package llm

import "strings"

// ThinkingCapability describes a model's native thinking support.
type ThinkingCapability struct {
	SupportsNativeThinking bool   // Whether the model supports native thinking
	ReasoningEffortParam   string // API parameter name ("reasoning_effort" for OpenAI-compat)
}

// DetectThinkingCapability determines if a model supports native thinking
// based on model name patterns and a known model list.
//
// Detection strategy (priority order):
//  1. Known model list — exact prefix matches for confirmed models
//  2. Keyword matching — model name contains thinking-related keywords
//  3. Default — assume no native thinking support
func DetectThinkingCapability(modelName string) ThinkingCapability {
	lower := strings.ToLower(modelName)

	// Strip common provider prefixes (e.g., "Pro/deepseek-ai/DeepSeek-R1")
	parts := strings.Split(lower, "/")
	baseName := parts[len(parts)-1]

	// 1. Known models with confirmed native thinking support
	knownThinkingModels := []string{
		// DeepSeek reasoning models
		"deepseek-reasoner",
		"deepseek-r1",
		"deepseek-r2",
		// OpenAI reasoning models
		"o1-mini", "o1-preview", "o1",
		"o3-mini", "o3",
		"o4-mini",
		// Anthropic extended thinking
		"claude-sonnet-4-5",
		"claude-3-7-sonnet",
		// Zhipu GLM
		"glm-5",
		// Qwen reasoning models
		"qwq",   // e.g. qwq-32b, qwq-plus
		"qwen3", // Qwen3 series with thinking
		// Google Gemini thinking models
		"gemini-2.5-flash", // has thinking mode
		"gemini-2.5-pro",
		// Moonshot Kimi
		"k1.5", // Kimi K1.5
		"kimi-k1",
		"kimi-k2.5", // Kimi K2.5 (Thinking mode)
	}

	for _, known := range knownThinkingModels {
		if strings.HasPrefix(baseName, known) {
			return ThinkingCapability{
				SupportsNativeThinking: true,
				ReasoningEffortParam:   "reasoning_effort",
			}
		}
	}

	// 2. Keyword-based detection for unknown/new models
	thinkingKeywords := []string{
		"-r1", "-r2", "reasoner", "thinking",
		"-o1", "-o3", "-o4",
		"-qwq", "deepthink",
	}

	for _, kw := range thinkingKeywords {
		if strings.Contains(baseName, kw) {
			return ThinkingCapability{
				SupportsNativeThinking: true,
				ReasoningEffortParam:   "reasoning_effort",
			}
		}
	}

	// 3. Default: no native thinking
	return ThinkingCapability{
		SupportsNativeThinking: false,
	}
}

// GetContextWindow returns the approximate context window in tokens for a known model.
// Returns 0 for unrecognised models; callers should apply their own safe default.
// Ordered from most to least specific prefix to avoid short-prefix false matches.
func GetContextWindow(modelName string) int {
	lower := strings.ToLower(modelName)
	parts := strings.Split(lower, "/")
	baseName := parts[len(parts)-1]

	knownWindows := []struct {
		prefix string
		tokens int
	}{
		// OpenAI
		{"gpt-4o", 128_000},
		{"gpt-4-turbo", 128_000},
		{"gpt-4", 8_192},
		{"gpt-3.5-turbo", 16_385},
		{"o1-mini", 128_000},
		{"o1-preview", 128_000},
		{"o1", 200_000},
		{"o3-mini", 200_000},
		{"o3", 200_000},
		{"o4-mini", 200_000},
		// Anthropic
		{"claude-3-5", 200_000},
		{"claude-3-7", 200_000},
		{"claude-sonnet", 200_000},
		{"claude-opus", 200_000},
		{"claude-haiku", 200_000},
		// DeepSeek
		{"deepseek-r1", 64_000},
		{"deepseek-r2", 64_000},
		{"deepseek-v3", 64_000},
		{"deepseek-v2", 128_000},
		{"deepseek", 64_000},
		// Google Gemini
		{"gemini-2.5", 1_000_000},
		{"gemini-2.0", 1_000_000},
		{"gemini-1.5-pro", 2_000_000},
		{"gemini-1.5-flash", 1_000_000},
		// Alibaba Qwen
		{"qwq", 32_000},
		{"qwen3", 32_000},
		{"qwen2.5", 128_000},
		{"qwen2", 128_000},
		// Moonshot Kimi
		{"kimi-k1", 128_000},
		{"k1.5", 128_000},
		// Zhipu GLM
		{"glm-z1", 128_000},
		{"glm-4", 128_000},
	}

	for _, kw := range knownWindows {
		if strings.HasPrefix(baseName, kw.prefix) {
			return kw.tokens
		}
	}
	return 0 // unknown model — caller should apply a safe default
}

// DetectToolCallingCapability determines if a model supports Function Calling
// based on a blacklist approach: most modern models support FC, so we only
// exclude known unsupported ones.
func DetectToolCallingCapability(modelName string) bool {
	lower := strings.ToLower(modelName)

	// Strip provider prefixes (e.g., "Pro/deepseek-ai/DeepSeek-V3")
	parts := strings.Split(lower, "/")
	baseName := parts[len(parts)-1]

	// Blacklist: models known NOT to support Function Calling.
	// Uses exact match to avoid blocking future variants (e.g. "o1-mini-turbo").
	noFCModels := map[string]bool{
		"o1-mini":    true, // Early o1 models don't support FC
		"o1-preview": true, // Early o1 models don't support FC
	}

	if noFCModels[baseName] {
		return false
	}

	// Default: assume FC support (most modern models do)
	return true
}
