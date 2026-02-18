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
		"deepseek-reasoner",
		"deepseek-r1",
		"deepseek-r2",
		"o1-mini",
		"o1-preview",
		"o1",
		"o3-mini",
		"o3",
		"o4-mini",
		"claude-sonnet-4-5", // Claude with extended thinking
		"claude-3-7-sonnet", // Claude 3.7 Sonnet extended thinking
		"glm-5",             // Zhipu GLM-5 with deep thinking (reasoning_content)
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
