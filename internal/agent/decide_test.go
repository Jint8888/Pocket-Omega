package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/tool"
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

	node := NewDecideNode(mock, nil)
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

	node := NewDecideNode(mock, nil)
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

	node := NewDecideNode(mock, nil)
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

	node := NewDecideNode(mock, nil)
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

	node := NewDecideNode(mock, nil)
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

	node := NewDecideNode(mock, nil)
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

	node := NewDecideNode(mock, nil)
	prep := DecidePrep{
		Problem:      "test hallucinated tool",
		ToolCallMode: "fc", // forced mode — should NOT fallback
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

// ── mockTool for buildToolingSection tests ──

type mockTool struct {
	name string
	desc string
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return m.desc }
func (m *mockTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{}`)
}
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	return tool.ToolResult{}, nil
}
func (m *mockTool) Init(_ context.Context) error { return nil }
func (m *mockTool) Close() error                 { return nil }

// ── buildRuntimeLine tests ──

func TestBuildRuntimeLine_AllFields(t *testing.T) {
	state := &AgentState{
		OSName:              "Windows",
		ShellCmd:            "cmd.exe /c",
		ModelName:           "gemini-2.5-pro",
		ContextWindowTokens: 131072,
		ThinkingMode:        "app",
	}
	got := buildRuntimeLine(state)

	checks := []struct {
		field string
		want  string
	}{
		{"os", "os=Windows"},
		{"shell", "shell=cmd.exe /c"},
		{"model", "model=gemini-2.5-pro"},
		{"ctx", "ctx=131072"},
		{"thinking", "thinking=app"},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.want) {
			t.Errorf("buildRuntimeLine() missing %s: want substring %q in %q", c.field, c.want, got)
		}
	}
}

func TestBuildRuntimeLine_EmptyFieldsFallback(t *testing.T) {
	state := &AgentState{} // all zero values
	got := buildRuntimeLine(state)
	for _, want := range []string{"os=unknown", "shell=unknown", "model=unknown"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty fields should produce %q, got: %q", want, got)
		}
	}
}

// ── buildToolingSection tests ──

func TestBuildToolingSection_PriorityOrdering(t *testing.T) {
	reg := tool.NewRegistry()
	// Register in reverse-priority order to confirm sorting overrides insertion order.
	reg.Register(&mockTool{"zzz_external", "External MCP tool\nsome extra line"})
	reg.Register(&mockTool{"mcp_server_add", "MCP: add a new server"})
	reg.Register(&mockTool{"shell_exec", "Execute shell commands"})
	reg.Register(&mockTool{"file_read", "Read file contents"})

	got := buildToolingSection(reg)

	// All four tools must appear
	for _, name := range []string{"file_read", "shell_exec", "mcp_server_add", "zzz_external"} {
		if !strings.Contains(got, name) {
			t.Errorf("buildToolingSection() missing tool %q\noutput:\n%s", name, got)
		}
	}

	// Priority ordering: file_read (core) < shell_exec (core) < mcp_server_add (mgmt) < zzz_external (extra)
	positions := map[string]int{
		"file_read":      strings.Index(got, "file_read"),
		"shell_exec":     strings.Index(got, "shell_exec"),
		"mcp_server_add": strings.Index(got, "mcp_server_add"),
		"zzz_external":   strings.Index(got, "zzz_external"),
	}
	ordered := [][2]string{
		{"file_read", "shell_exec"},
		{"shell_exec", "mcp_server_add"},
		{"mcp_server_add", "zzz_external"},
	}
	for _, pair := range ordered {
		if positions[pair[0]] > positions[pair[1]] {
			t.Errorf("%s (pos %d) should appear before %s (pos %d)",
				pair[0], positions[pair[0]], pair[1], positions[pair[1]])
		}
	}
}

func TestBuildToolingSection_FirstLineOnly(t *testing.T) {
	// Description with multiple lines — only first line should appear in summary.
	reg := tool.NewRegistry()
	reg.Register(&mockTool{"file_read", "Read file contents\nDetailed second line\nThird line"})

	got := buildToolingSection(reg)

	if !strings.Contains(got, "Read file contents") {
		t.Errorf("first line should appear in output, got:\n%s", got)
	}
	if strings.Contains(got, "Detailed second line") {
		t.Errorf("second line should NOT appear in output, got:\n%s", got)
	}
}

func TestBuildToolingSection_EmptyRegistry(t *testing.T) {
	got := buildToolingSection(tool.NewRegistry())
	if got != "" {
		t.Errorf("empty registry: got %q, want empty string", got)
	}
}

func TestBuildToolingSection_NilRegistry(t *testing.T) {
	got := buildToolingSection(nil)
	if got != "" {
		t.Errorf("nil registry: got %q, want empty string", got)
	}
}

// ── containsMCPKeywords tests ──

func TestContainsMCPKeywords_Positive(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"bare mcp", "帮我配置 mcp 工具"},
		{"uppercase MCP", "Can you add an MCP server?"},
		{"技能", "我想新建一个技能"},
		{"skill lowercase", "create a new skill for me"},
		{"SKILL uppercase", "Add a SKILL"},
		{"自定义工具", "我要创建一个自定义工具"},
		{"custom tool", "I need a custom tool"},
		{"mcp embedded in sentence", "how do I set up an mcp-based plugin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !containsMCPKeywords(tc.input) {
				t.Errorf("containsMCPKeywords(%q) = false, want true", tc.input)
			}
		})
	}
}

func TestContainsMCPKeywords_Negative(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"web server unrelated", "deploy my web server"},
		{"database query", "run a database query"},
		{"plain question", "what is the weather today?"},
		{"server without mcp", "restart the server"},
		{"chinese unrelated", "帮我查询一下北京天气"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if containsMCPKeywords(tc.input) {
				t.Errorf("containsMCPKeywords(%q) = true, want false", tc.input)
			}
		})
	}
}

// ── Token Budget Guard tests ──

func TestTokenBudgetGuard_TruncatesAtThreshold(t *testing.T) {
	// Build a loader-less DecideNode (loader nil is safe — buildSystemPrompt guards it)
	node := NewDecideNode(&mockLLMProvider{}, nil)

	// ContextWindowTokens=100 → maxChars = 100 * 2 * 25 / 100 = 50
	prep := DecidePrep{
		ContextWindowTokens: 100,
		ToolCallMode:        "yaml",
		ThinkingMode:        "app",
	}
	result := node.buildSystemPrompt("app", prep)
	maxChars := 100 * charsPerToken * 25 / 100 // 50
	if len([]rune(result)) > maxChars {
		t.Errorf("token budget guard: result has %d runes, want <= %d", len([]rune(result)), maxChars)
	}
}

func TestTokenBudgetGuard_NoTruncationWhenZero(t *testing.T) {
	// ContextWindowTokens=0 → guard disabled; result should be non-empty (L1 constant)
	node := NewDecideNode(&mockLLMProvider{}, nil)
	prep := DecidePrep{
		ContextWindowTokens: 0,
		ToolCallMode:        "yaml",
		ThinkingMode:        "app",
	}
	result := node.buildSystemPrompt("app", prep)
	if result == "" {
		t.Error("buildSystemPrompt() returned empty string when ContextWindowTokens=0")
	}
}

func TestTokenBudgetGuard_UTF8Safe(t *testing.T) {
	// Verify that truncation never produces invalid UTF-8 (i.e. no mid-character cut).
	node := NewDecideNode(&mockLLMProvider{}, nil)
	prep := DecidePrep{
		ContextWindowTokens: 10, // tiny budget → maxChars = 10*2*25/100 = 5
		RuntimeLine:         "测试中文字符截断安全性验证文字",
		ToolCallMode:        "yaml",
		ThinkingMode:        "app",
	}
	result := node.buildSystemPrompt("app", prep)
	// Confirm the result is valid UTF-8 by re-encoding to runes with no replacement chars
	for i, r := range result {
		if r == '\uFFFD' {
			t.Errorf("invalid UTF-8 replacement char at position %d in truncated result", i)
		}
	}
}

// ── FC Reason recovery tests ──

func TestExecWithFC_ReasonFromContent(t *testing.T) {
	// When model returns both Content (reasoning) and ToolCalls, Reason should use Content.
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "需要先列出目录结构了解项目布局",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "file_list", Arguments: []byte(`{"path":"."}`)},
			},
		},
		supportsFC: true,
	}
	node := NewDecideNode(mock, nil)
	prep := DecidePrep{
		Problem:      "帮我分析项目结构",
		ToolCallMode: "fc",
		ToolDefinitions: []llm.ToolDefinition{
			{Name: "file_list", Description: "list files"},
		},
	}
	decision, err := node.Exec(context.Background(), prep)
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	if decision.Reason == "FC: call file_list" {
		t.Error("Reason should use Content text, not hardcoded FC prefix")
	}
	if !strings.Contains(decision.Reason, "列出目录") {
		t.Errorf("Reason should contain Content text, got: %q", decision.Reason)
	}
}

func TestExecWithFC_ReasonFallbackWhenNoContent(t *testing.T) {
	// When model returns ToolCalls without Content, Reason should fallback to "FC: call xxx".
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_2", Name: "file_read", Arguments: []byte(`{"path":"test.txt"}`)},
			},
		},
		supportsFC: true,
	}
	node := NewDecideNode(mock, nil)
	prep := DecidePrep{
		Problem:      "读取文件",
		ToolCallMode: "fc",
		ToolDefinitions: []llm.ToolDefinition{
			{Name: "file_read", Description: "read file"},
		},
	}
	decision, err := node.Exec(context.Background(), prep)
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	if decision.Reason != "FC: call file_read" {
		t.Errorf("Reason should fallback to FC prefix, got: %q", decision.Reason)
	}
}

func TestExecWithFC_ReasonTruncation(t *testing.T) {
	// When Content exceeds 200 chars, Reason should be truncated.
	longContent := strings.Repeat("这是一段很长的推理文字", 30) // ~330 chars
	mock := &mockLLMProvider{
		callLLMWithToolsResp: llm.Message{
			Role:    llm.RoleAssistant,
			Content: longContent,
			ToolCalls: []llm.ToolCall{
				{ID: "call_3", Name: "file_list", Arguments: []byte(`{"path":"."}`)},
			},
		},
		supportsFC: true,
	}
	node := NewDecideNode(mock, nil)
	prep := DecidePrep{
		Problem:      "test truncation",
		ToolCallMode: "fc",
		ToolDefinitions: []llm.ToolDefinition{
			{Name: "file_list", Description: "list files"},
		},
	}
	decision, err := node.Exec(context.Background(), prep)
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	runes := []rune(decision.Reason)
	// truncate(s, 200) produces 200 runes + "..." suffix
	if len(runes) > 210 {
		t.Errorf("Reason should be truncated to ~200 runes, got %d runes", len(runes))
	}
	if !strings.HasSuffix(decision.Reason, "...") {
		t.Errorf("Truncated Reason should end with '...', got: %q", decision.Reason[len(decision.Reason)-10:])
	}
}

// ── StepSummary duplicate detection tests ──

func TestBuildStepSummary_DuplicateWarning(t *testing.T) {
	// Repeated file_list with same path should produce inline ⚠️ warning.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "file_list", Input: `{"path":"."}`, Output: "file1.go\nfile2.go"},
		{StepNumber: 2, Type: "decide", Action: "tool", Input: "FC: call file_read"},
		{StepNumber: 3, Type: "tool", ToolName: "file_read", Input: `{"path":"test.txt"}`, Output: "content"},
		{StepNumber: 4, Type: "tool", ToolName: "file_list", Input: `{"path":"."}`, Output: "file1.go\nfile2.go"},
	}
	summary := buildStepSummary(steps, 0)
	if !strings.Contains(summary, "⚠️") {
		t.Error("summary should contain duplicate warning for repeated file_list(.)")
	}
	if !strings.Contains(summary, "步骤1重复") {
		t.Error("warning should reference step 1 as the first occurrence")
	}
}

func TestBuildStepSummary_NoDuplicateForDifferentParams(t *testing.T) {
	// file_read with different paths should NOT trigger duplicate warning.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "file_read", Input: `{"path":"a.txt"}`, Output: "aaa"},
		{StepNumber: 2, Type: "tool", ToolName: "file_read", Input: `{"path":"b.txt"}`, Output: "bbb"},
	}
	summary := buildStepSummary(steps, 0)
	if strings.Contains(summary, "⚠️") {
		t.Error("different paths should NOT trigger duplicate warning")
	}
}

func TestBuildStepSummary_ShellExecNoDuplicateForDifferentCommands(t *testing.T) {
	// shell_exec with different commands should NOT trigger duplicate warning.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "shell_exec", Input: `{"command":"dir"}`, Output: "listing"},
		{StepNumber: 2, Type: "tool", ToolName: "shell_exec", Input: `{"command":"type test.txt"}`, Output: "content"},
	}
	summary := buildStepSummary(steps, 0)
	if strings.Contains(summary, "⚠️") {
		t.Error("different commands should NOT trigger duplicate warning")
	}
}

func TestBuildStepSummary_NoDuplicateForSearchTool(t *testing.T) {
	// search tools (not in paramDedupTools) called with different queries
	// must NOT produce a duplicate warning — different queries are legitimate.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "search_tavily", Input: `{"query":"golang channel 教程"}`, Output: "result A"},
		{StepNumber: 2, Type: "tool", ToolName: "search_tavily", Input: `{"query":"goroutine 最佳实践"}`, Output: "result B"},
	}
	summary := buildStepSummary(steps, 0)
	if strings.Contains(summary, "⚠️") {
		t.Errorf("different queries on search tool should NOT trigger duplicate warning, got:\n%s", summary)
	}
}

func TestBuildStepSummary_NoDuplicateForWebReader(t *testing.T) {
	// web_reader with different URLs should NOT trigger duplicate warning.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "web_reader", Input: `{"url":"https://go.dev/doc"}`, Output: "page A"},
		{StepNumber: 2, Type: "tool", ToolName: "web_reader", Input: `{"url":"https://pkg.go.dev/fmt"}`, Output: "page B"},
	}
	summary := buildStepSummary(steps, 0)
	if strings.Contains(summary, "⚠️") {
		t.Errorf("different URLs on web_reader should NOT trigger duplicate warning, got:\n%s", summary)
	}
}
