package llm

import "testing"

func TestDetectThinkingCapability(t *testing.T) {
	tests := []struct {
		name        string
		modelName   string
		wantSupport bool
		wantParam   string
	}{
		// Known models
		{"DeepSeek-R1", "deepseek-r1", true, "reasoning_effort"},
		{"DeepSeek-R1 with provider prefix", "Pro/deepseek-ai/DeepSeek-R1", true, "reasoning_effort"},
		{"DeepSeek-Reasoner", "deepseek-reasoner", true, "reasoning_effort"},
		{"o1-preview", "o1-preview", true, "reasoning_effort"},
		{"o1-mini", "o1-mini", true, "reasoning_effort"},
		{"o3-mini", "o3-mini", true, "reasoning_effort"},
		{"o3", "o3", true, "reasoning_effort"},
		{"Claude Sonnet 4.5", "claude-sonnet-4-5-20250220", true, "reasoning_effort"},

		// GLM-5 (Zhipu deep thinking)
		{"GLM-5 direct", "glm-5", true, "reasoning_effort"},
		{"GLM-5 via SiliconFlow", "Pro/zai-org/GLM-5", true, "reasoning_effort"},

		// Keyword matches
		{"Custom reasoner model", "my-custom-reasoner-v2", true, "reasoning_effort"},
		{"Thinking model", "model-thinking-v1", true, "reasoning_effort"},

		// Non-thinking models
		{"DeepSeek-V3 chat", "deepseek-chat", false, ""},
		{"DeepSeek-V3.2 via SiliconFlow", "Pro/deepseek-ai/DeepSeek-V3.2", false, ""},
		{"GPT-4o", "gpt-4o", false, ""},
		{"GPT-4.1", "gpt-4.1", false, ""},
		{"Claude Sonnet 4", "claude-sonnet-4-20250514", false, ""},
		{"Qwen-2.5", "qwen-2.5-72b-instruct", false, ""},
		{"GLM-4", "glm-4-plus", false, ""},
		{"Empty model name", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := DetectThinkingCapability(tt.modelName)
			if cap.SupportsNativeThinking != tt.wantSupport {
				t.Errorf("DetectThinkingCapability(%q).SupportsNativeThinking = %v, want %v",
					tt.modelName, cap.SupportsNativeThinking, tt.wantSupport)
			}
			if cap.ReasoningEffortParam != tt.wantParam {
				t.Errorf("DetectThinkingCapability(%q).ReasoningEffortParam = %q, want %q",
					tt.modelName, cap.ReasoningEffortParam, tt.wantParam)
			}
		})
	}
}
