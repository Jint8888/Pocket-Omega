package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/llm"
)

func TestParseDecisionValid(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAction string
	}{
		{
			"tool action",
			"```yaml\naction: tool\nreason: need time\ntool_name: get_time\ntool_params:\n  timezone: Asia/Shanghai\n```",
			"tool",
		},
		{
			"think action",
			"```yaml\naction: think\nreason: need analysis\nthinking: |\n  Let me analyze this...\n```",
			"think",
		},
		{
			"answer action",
			"```yaml\naction: answer\nreason: simple question\nanswer: |\n  The answer is 42.\n```",
			"answer",
		},
		{
			"bare yaml (no fences)",
			"action: answer\nreason: direct\nanswer: hello",
			"answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := parseDecision(tt.input)
			if err != nil {
				t.Fatalf("parseDecision() error: %v", err)
			}
			if decision.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", decision.Action, tt.wantAction)
			}
		})
	}
}

func TestParseDecisionToolParams(t *testing.T) {
	input := "```yaml\naction: tool\nreason: check file\ntool_name: file_read\ntool_params:\n  path: ./test.txt\n```"

	decision, err := parseDecision(input)
	if err != nil {
		t.Fatalf("parseDecision() error: %v", err)
	}

	if decision.ToolName != "file_read" {
		t.Errorf("tool_name = %q, want %q", decision.ToolName, "file_read")
	}

	path, ok := decision.ToolParams["path"]
	if !ok {
		t.Fatal("tool_params missing 'path' key")
	}
	if path != "./test.txt" {
		t.Errorf("path = %q, want %q", path, "./test.txt")
	}
}

func TestParseDecisionWindowsPath(t *testing.T) {
	// LLM often produces double-quoted Windows paths which break YAML escaping.
	// The parser should recover by replacing backslashes with forward slashes.
	input := "```yaml\naction: tool\nreason: list files\ntool_name: file_list\ntool_params:\n  path: \"E:\\AI\\Pocket-Omega\\docs\"\n```"

	decision, err := parseDecision(input)
	if err != nil {
		t.Fatalf("parseDecision() should recover from backslash issue: %v", err)
	}
	if decision.Action != "tool" {
		t.Errorf("action = %q, want %q", decision.Action, "tool")
	}
	if decision.ToolName != "file_list" {
		t.Errorf("tool_name = %q, want %q", decision.ToolName, "file_list")
	}
	path, ok := decision.ToolParams["path"]
	if !ok {
		t.Fatal("tool_params missing 'path' key")
	}
	// Backslashes should be converted to forward slashes
	expected := "E:/AI/Pocket-Omega/docs"
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
}

func TestParseDecisionInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no action field", "```yaml\nreason: missing action\n```"},
		{"garbage", "this is not yaml at all {{{"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDecision(tt.input)
			if err == nil {
				t.Error("parseDecision() should have returned error")
			}
		})
	}
}

func TestTruncateUTF8Safe(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		check  func(string) bool // returns true if OK
		desc   string
	}{
		{
			"short string unchanged",
			"hello", 10,
			func(s string) bool { return s == "hello" },
			"should return unchanged",
		},
		{
			"exact length unchanged",
			"hello", 5,
			func(s string) bool { return s == "hello" },
			"should return unchanged",
		},
		{
			"truncated ASCII",
			"hello world", 5,
			func(s string) bool { return s == "hello..." },
			"should truncate with ellipsis",
		},
		{
			"Chinese text safe",
			"你好世界测试", 3,
			func(s string) bool { return s == "你好世..." },
			"should not break multi-byte chars",
		},
		{
			"mixed safe",
			"AB你好CD", 4,
			func(s string) bool { return s == "AB你好..." },
			"should handle mixed content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			if !tt.check(result) {
				t.Errorf("truncate(%q, %d) = %q; %s", tt.input, tt.maxLen, result, tt.desc)
			}
		})
	}
}

func TestFixBackslashes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"Windows path in double quotes",
			`path: "E:\AI\Pocket-Omega\docs"`,
			`path: "E:/AI/Pocket-Omega/docs"`,
		},
		{
			"preserve non-path content (\\n not in drive path)",
			`command: "echo hello\nworld"`,
			`command: "echo hello\nworld"`, // no X:\ pattern → untouched
		},
		{
			"preserve non-path content (\\t not in drive path)",
			`value: "col1\tcol2"`,
			`value: "col1\tcol2"`, // no X:\ pattern → untouched
		},
		{
			"no double quotes (bare value) — untouched",
			`path: E:\AI\test`,
			`path: E:\AI\test`, // regex only matches double-quoted strings
		},
		{
			"multiple paths in one line",
			`src: "C:\Users\foo" dst: "D:\tmp\bar"`,
			`src: "C:/Users/foo" dst: "D:/tmp/bar"`,
		},
		{
			"drive path with segments matching YAML escapes",
			`path: "C:\new\test"`,
			`path: "C:/new/test"`, // regex replaces ALL \ in drive paths (including \n, \t)
		},
		{
			"empty string",
			"",
			"",
		},
		{
			"non-path quoted string — untouched",
			`text: "hello world"`,
			`text: "hello world"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fixBackslashes(tt.input)
			if got != tt.want {
				t.Errorf("fixBackslashes(%q) =\n  %q\nwant:\n  %q", tt.input, got, tt.want)
			}
		})
	}
}

// ── Mock LLMProvider for FC path tests ──

type mockLLMProvider struct {
	callLLMResp          llm.Message
	callLLMErr           error
	callLLMWithToolsResp llm.Message
	callLLMWithToolsErr  error
	supportsFC           bool
}

func (m *mockLLMProvider) CallLLM(_ context.Context, _ []llm.Message) (llm.Message, error) {
	return m.callLLMResp, m.callLLMErr
}

func (m *mockLLMProvider) CallLLMStream(_ context.Context, _ []llm.Message, _ llm.StreamCallback) (llm.Message, error) {
	return m.callLLMResp, m.callLLMErr
}

func (m *mockLLMProvider) CallLLMWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition) (llm.Message, error) {
	return m.callLLMWithToolsResp, m.callLLMWithToolsErr
}

func (m *mockLLMProvider) IsToolCallingEnabled() bool {
	return m.supportsFC
}

func (m *mockLLMProvider) GetName() string {
	return "mock"
}

// ── FC path tests ──

func TestExecWithFC_ToolCallReturned(t *testing.T) {
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_123", Name: "brave_search", Arguments: []byte(`{"query":"golang"}`)},
			},
		},
		supportsFC: true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "search for golang",
		ToolCallMode: "fc",
		ToolDefinitions: []llm.ToolDefinition{
			{Name: "brave_search", Description: "web search"},
		},
	}

	decision, err := node.Exec(context.Background(), prep)
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	if decision.Action != "tool" {
		t.Errorf("Action = %q, want %q", decision.Action, "tool")
	}
	if decision.ToolName != "brave_search" {
		t.Errorf("ToolName = %q, want %q", decision.ToolName, "brave_search")
	}
	if decision.ToolCallID != "call_123" {
		t.Errorf("ToolCallID = %q, want %q", decision.ToolCallID, "call_123")
	}
	q, ok := decision.ToolParams["query"]
	if !ok || q != "golang" {
		t.Errorf("ToolParams[query] = %v, want %q", q, "golang")
	}
}

func TestExecWithFC_DirectAnswer(t *testing.T) {
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "The answer is 42.",
		},
		supportsFC: true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "what is 6*7",
		ToolCallMode: "fc",
	}

	decision, err := node.Exec(context.Background(), prep)
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	if decision.Action != "answer" {
		t.Errorf("Action = %q, want %q", decision.Action, "answer")
	}
	if decision.Answer != "The answer is 42." {
		t.Errorf("Answer = %q, want %q", decision.Answer, "The answer is 42.")
	}
}

func TestExecWithFC_EmptyResponse(t *testing.T) {
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role: llm.RoleAssistant,
			// No ToolCalls, no Content
		},
		supportsFC: true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "hello",
		ToolCallMode: "fc",
	}

	_, err := node.Exec(context.Background(), prep)
	if err == nil {
		t.Error("Exec() should return error for empty FC response")
	}
}

func TestDecideNodeExec_AutoFallbackToYAML(t *testing.T) {
	mock := &mockLLMProvider{
		// FC fails
		callLLMWithToolsErr: fmt.Errorf("FC API error"),
		// YAML fallback succeeds with direct answer
		callLLMResp: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "Direct answer via YAML fallback",
		},
		supportsFC: true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "test fallback",
		ToolCallMode: "auto", // auto mode — should fallback
	}

	decision, err := node.Exec(context.Background(), prep)
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	// Should have fallen back to YAML, which treats non-YAML content as direct answer
	if decision.Action != "answer" {
		t.Errorf("Action = %q, want %q", decision.Action, "answer")
	}
}

func TestDecideNodeExec_ForcedFCNoFallback(t *testing.T) {
	mock := &mockLLMProvider{
		callLLMWithToolsErr: fmt.Errorf("FC API error"),
		supportsFC:          true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "test forced fc",
		ToolCallMode: "fc", // forced mode — should NOT fallback
	}

	_, err := node.Exec(context.Background(), prep)
	if err == nil {
		t.Error("Exec() should return error in forced FC mode when FC fails")
	}
}

func TestExecWithFC_InvalidToolParamsJSON(t *testing.T) {
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_bad", Name: "brave_search", Arguments: []byte(`not valid json`)},
			},
		},
		supportsFC: true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "test invalid json",
		ToolCallMode: "fc",
		ToolDefinitions: []llm.ToolDefinition{
			{Name: "brave_search", Description: "web search"},
		},
	}

	_, err := node.Exec(context.Background(), prep)
	if err == nil {
		t.Error("Exec() should return error when FC tool params are invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid tool params") {
		t.Errorf("error should mention 'invalid tool params', got: %v", err)
	}
}

func TestExecWithFC_HallucinatedToolName(t *testing.T) {
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_ghost", Name: "nonexistent_tool", Arguments: []byte(`{"x":1}`)},
			},
		},
		supportsFC: true,
	}

	node := NewDecideNode(mock)
	prep := DecidePrep{
		Problem:      "test hallucinated tool",
		ToolCallMode: "fc",
		ToolDefinitions: []llm.ToolDefinition{
			{Name: "brave_search", Description: "web search"},
			{Name: "file_read", Description: "read file"},
		},
	}

	_, err := node.Exec(context.Background(), prep)
	if err == nil {
		t.Error("Exec() should return error when FC returns unknown tool name")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error should mention 'unknown tool', got: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent_tool") {
		t.Errorf("error should contain the hallucinated name, got: %v", err)
	}
}
