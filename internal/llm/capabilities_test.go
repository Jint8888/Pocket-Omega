package llm

import "testing"

func TestDetectThinkingCapability(t *testing.T) {
	tests := []struct {
		name        string
		modelName   string
		wantSupport bool
	}{
		// Known models
		{"DeepSeek-R1", "deepseek-r1", true},
		{"DeepSeek-R1 with provider prefix", "Pro/deepseek-ai/DeepSeek-R1", true},
		{"DeepSeek-Reasoner", "deepseek-reasoner", true},
		{"o1-preview", "o1-preview", true},
		{"o1-mini", "o1-mini", true},
		{"o3-mini", "o3-mini", true},
		{"o3", "o3", true},
		{"Claude Sonnet 4.5", "claude-sonnet-4-5-20250220", true},

		// GLM-5 (Zhipu deep thinking)
		{"GLM-5 direct", "glm-5", true},
		{"GLM-5 via SiliconFlow", "Pro/zai-org/GLM-5", true},

		// Gemini 3.1 (Google thinking models)
		{"Gemini 3.1 Pro High", "gemini-3.1-pro-high", true},
		{"Gemini 3.1 Flash", "gemini-3.1-flash", true},

		// Keyword matches
		{"Custom reasoner model", "my-custom-reasoner-v2", true},
		{"Thinking model", "model-thinking-v1", true},

		// Non-thinking models
		{"DeepSeek-V3 chat", "deepseek-chat", false},
		{"DeepSeek-V3.2 via SiliconFlow", "Pro/deepseek-ai/DeepSeek-V3.2", false},
		{"GPT-4o", "gpt-4o", false},
		{"GPT-4.1", "gpt-4.1", false},
		{"Claude Sonnet 4", "claude-sonnet-4-20250514", false},
		{"Qwen-2.5", "qwen-2.5-72b-instruct", false},
		{"GLM-4", "glm-4-plus", false},
		{"Empty model name", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := DetectThinkingCapability(tt.modelName)
			if cap.SupportsNativeThinking != tt.wantSupport {
				t.Errorf("DetectThinkingCapability(%q).SupportsNativeThinking = %v, want %v",
					tt.modelName, cap.SupportsNativeThinking, tt.wantSupport)
			}
		})
	}
}

func TestDetectToolCallingCapability(t *testing.T) {
	tests := []struct {
		name        string
		modelName   string
		wantSupport bool
	}{
		// Blacklisted models (no FC)
		{"o1-mini blocked", "o1-mini", false},
		{"o1-preview blocked", "o1-preview", false},

		// Supported models (most modern models)
		{"GPT-4o", "gpt-4o", true},
		{"GPT-4.1", "gpt-4.1", true},
		{"Kimi K2.5", "kimi-k2.5-0828", true},
		{"GLM-5", "glm-5", true},
		{"DeepSeek-V3", "deepseek-chat", true},
		{"DeepSeek-V3 via SiliconFlow", "Pro/deepseek-ai/DeepSeek-V3.2", true},
		{"Claude Sonnet 4", "claude-sonnet-4-20250514", true},
		{"Qwen 2.5", "qwen-2.5-72b-instruct", true},

		// Edge cases
		{"empty model name", "", true},
		{"o1 (not o1-mini/preview)", "o1", true},
		{"o3-mini", "o3-mini", true},
		{"o1-mini-turbo (hypothetical future variant)", "o1-mini-turbo", true}, // exact match: should NOT be blocked
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectToolCallingCapability(tt.modelName)
			if got != tt.wantSupport {
				t.Errorf("DetectToolCallingCapability(%q) = %v, want %v",
					tt.modelName, got, tt.wantSupport)
			}
		})
	}
}
