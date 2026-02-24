package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/plan"
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
		{"自定义工具", "我要创建一个自定义工具"},
		{"custom tool", "I need a custom tool"},
		{"mcp embedded in sentence", "how do I set up an mcp-based plugin"},
		{"exact 创建工具", "帮我创建工具"},
		{"exact 新建工具", "新建工具来处理数据"},
		// Word-bag matches (words separated by other tokens)
		{"word-bag build+tool", "build a tool for excel"},
		{"word-bag create+tool", "create new tool please"},
		{"word-bag custom+tool", "I need a custom data processing tool"},
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
		// "skill" alone no longer triggers (too broad)
		{"skill without action context", "create a new skill for me"},
		{"SKILL uppercase alone", "Add a SKILL"},
		{"coding skill", "improve my coding skill"},
		{"what skills", "what skills do you have"},
		// Only one word from intent phrase
		{"build without tool", "use the build command"},
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

// ── Dual-Zone Layout tests ──

func TestBuildStepSummary_ZoneLayout(t *testing.T) {
	// With more tool steps than window size, Zone A header should appear before Zone B header.
	steps := make([]StepRecord, 0, 10)
	for i := 1; i <= 6; i++ {
		steps = append(steps, StepRecord{
			StepNumber: i, Type: "tool", ToolName: "file_read",
			Input:  fmt.Sprintf(`{"path":"file%d.go"}`, i),
			Output: fmt.Sprintf("content of file%d", i),
		})
	}
	summary := buildStepSummary(steps, 0)

	zoneAPos := strings.Index(summary, "--- 最近工具结果 ---")
	zoneBPos := strings.Index(summary, "--- 执行历史 ---")
	if zoneAPos < 0 {
		t.Fatal("Zone A header '--- 最近工具结果 ---' not found in summary")
	}
	if zoneBPos < 0 {
		t.Fatal("Zone B header '--- 执行历史 ---' not found in summary")
	}
	if zoneAPos >= zoneBPos {
		t.Errorf("Zone A (pos %d) should appear before Zone B (pos %d)", zoneAPos, zoneBPos)
	}
}

func TestBuildStepSummary_ZoneANewestFirst(t *testing.T) {
	// Zone A should render steps in newest-first order.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "file_read", Input: `{"path":"old.go"}`, Output: "old"},
		{StepNumber: 2, Type: "tool", ToolName: "file_read", Input: `{"path":"mid.go"}`, Output: "mid"},
		{StepNumber: 3, Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`, Output: "a"},
		{StepNumber: 4, Type: "tool", ToolName: "file_read", Input: `{"path":"b.go"}`, Output: "b"},
		{StepNumber: 5, Type: "tool", ToolName: "file_read", Input: `{"path":"c.go"}`, Output: "c"},
	}
	summary := buildStepSummary(steps, 0)

	// Zone A should contain steps 3, 4, 5 (last 3) in newest-first order: 5, 4, 3
	pos5 := strings.Index(summary, "步骤 5")
	pos4 := strings.Index(summary, "步骤 4")
	pos3 := strings.Index(summary, "步骤 3")
	if pos5 < 0 || pos4 < 0 || pos3 < 0 {
		t.Fatalf("Zone A steps not found in summary:\n%s", summary)
	}
	if !(pos5 < pos4 && pos4 < pos3) {
		t.Errorf("Zone A should be newest-first: step5(pos %d) < step4(pos %d) < step3(pos %d)", pos5, pos4, pos3)
	}
}

func TestBuildStepSummary_DecideStepsOmitted(t *testing.T) {
	// Decide steps should not appear in the summary.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`, Output: "content"},
		{StepNumber: 2, Type: "decide", Action: "tool", Input: "FC: call file_read"},
		{StepNumber: 3, Type: "tool", ToolName: "file_read", Input: `{"path":"b.go"}`, Output: "content2"},
	}
	summary := buildStepSummary(steps, 0)
	if strings.Contains(summary, "[决策]") {
		t.Errorf("decide steps should be omitted from summary, got:\n%s", summary)
	}
	if strings.Contains(summary, "步骤 2") {
		t.Errorf("step 2 (decide) should not appear in summary, got:\n%s", summary)
	}
}

func TestBuildStepSummary_MetaToolNotInZoneA(t *testing.T) {
	// Meta-tools (update_plan, walkthrough) should not appear in Zone A,
	// but should appear in Zone B as ultra-compact one-liners for LLM memory.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`, Output: "old content"},
		{StepNumber: 2, Type: "tool", ToolName: "file_read", Input: `{"path":"b.go"}`, Output: "old content 2"},
		{StepNumber: 3, Type: "tool", ToolName: "update_plan", Input: `{"plan":"step1"}`, Output: "plan updated"},
		{StepNumber: 4, Type: "tool", ToolName: "walkthrough", Input: `{"content":"note"}`, Output: "noted"},
		{StepNumber: 5, Type: "tool", ToolName: "file_read", Input: `{"path":"c.go"}`, Output: "new content"},
		{StepNumber: 6, Type: "tool", ToolName: "file_read", Input: `{"path":"d.go"}`, Output: "new content 2"},
		{StepNumber: 7, Type: "tool", ToolName: "file_read", Input: `{"path":"e.go"}`, Output: "newest content"},
	}
	summary := buildStepSummary(steps, 0)

	// Zone A should contain the 3 most recent non-meta tool steps: 5, 6, 7
	zoneAHeader := strings.Index(summary, "--- 最近工具结果 ---")
	zoneBHeader := strings.Index(summary, "--- 执行历史 ---")
	if zoneAHeader < 0 || zoneBHeader < 0 {
		t.Fatalf("zone headers not found in summary:\n%s", summary)
	}

	zoneA := summary[zoneAHeader:zoneBHeader]
	// Meta-tools should NOT be in Zone A
	if strings.Contains(zoneA, "update_plan") {
		t.Error("update_plan should not be in Zone A")
	}
	if strings.Contains(zoneA, "walkthrough") {
		t.Error("walkthrough should not be in Zone A")
	}
	// Steps 5, 6, 7 should be in Zone A
	if !strings.Contains(zoneA, "步骤 7") || !strings.Contains(zoneA, "步骤 6") || !strings.Contains(zoneA, "步骤 5") {
		t.Errorf("Zone A should contain steps 5, 6, 7, got:\n%s", zoneA)
	}

	// Meta-tools SHOULD appear in Zone B as ultra-compact one-liners
	zoneB := summary[zoneBHeader:]
	if !strings.Contains(zoneB, "update_plan") || !strings.Contains(zoneB, "✓ 已调用") {
		t.Errorf("update_plan should appear in Zone B as compact one-liner, got:\n%s", zoneB)
	}
	if !strings.Contains(zoneB, "walkthrough") {
		t.Errorf("walkthrough should appear in Zone B as compact one-liner, got:\n%s", zoneB)
	}
	// Meta-tools in Zone B should NOT contain output details
	if strings.Contains(zoneB, "plan updated") || strings.Contains(zoneB, "noted") {
		t.Errorf("meta-tool Zone B entries should not contain output details, got:\n%s", zoneB)
	}
}

func TestBuildStepSummary_DynamicWindow(t *testing.T) {
	// With 20+ non-meta tool steps, window should expand to 5.
	steps := make([]StepRecord, 0, 22)
	for i := 1; i <= 22; i++ {
		steps = append(steps, StepRecord{
			StepNumber: i, Type: "tool", ToolName: "file_read",
			Input:  fmt.Sprintf(`{"path":"file%d.go"}`, i),
			Output: fmt.Sprintf("content %d", i),
		})
	}
	summary := buildStepSummary(steps, 0)

	// Zone A should contain the last 5 steps (18-22) with full output
	for i := 18; i <= 22; i++ {
		marker := fmt.Sprintf("步骤 %d [工具 file_read]: content %d", i, i)
		if !strings.Contains(summary, marker) {
			t.Errorf("Zone A should contain step %d with full output, got:\n%s", i, summary)
		}
	}
	// Step 17 should be in Zone B (compressed)
	if !strings.Contains(summary, "步骤 17 [工具 file_read]: 已执行") {
		t.Errorf("Step 17 should be in Zone B (compressed), got:\n%s", summary)
	}
}

func TestBuildStepSummary_FewStepsNoHeaders(t *testing.T) {
	// When all tool steps fit in the window, no zone headers should appear.
	steps := []StepRecord{
		{StepNumber: 1, Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`, Output: "content a"},
		{StepNumber: 2, Type: "tool", ToolName: "file_read", Input: `{"path":"b.go"}`, Output: "content b"},
	}
	summary := buildStepSummary(steps, 0)
	if strings.Contains(summary, "---") {
		t.Errorf("few steps should not produce zone headers, got:\n%s", summary)
	}
	// Both steps should have full output (not compressed)
	if strings.Contains(summary, "已执行") {
		t.Errorf("all steps should have full output when within window, got:\n%s", summary)
	}
}

// ── LoopDetector streak self-correction tests ──

func TestLoopDetector_SelfCorrectionResetsStreak(t *testing.T) {
	// When LoopDetector fires but LLM switches to a different tool,
	// Post should reset streak (self-correction) instead of hard override.
	node := NewDecideNode(&mockLLMProvider{}, nil)
	state := &AgentState{
		LoopDetectionStreak: 1, // already had one soft warning
	}

	// LLM chose shell_exec, but LoopDetector flagged file_read as the loop tool
	decision := Decision{Action: "tool", ToolName: "shell_exec"}
	prep := []DecidePrep{{
		LoopDetected: DetectionResult{
			Detected: true,
			Rule:     "same_tool_freq",
			ToolName: "file_read", // different from decision.ToolName
		},
	}}

	action := node.Post(state, prep, decision)

	if action != core.ActionTool {
		t.Errorf("self-corrected tool should be allowed, got action=%s", action)
	}
	if state.LoopDetectionStreak != 0 {
		t.Errorf("streak should reset on self-correction, got %d", state.LoopDetectionStreak)
	}
}

func TestLoopDetector_SameToolHardOverride(t *testing.T) {
	// When LoopDetector fires and LLM picks the SAME tool again (streak=2),
	// Post should hard override to answer.
	node := NewDecideNode(&mockLLMProvider{}, nil)
	state := &AgentState{
		LoopDetectionStreak: 1, // already had one soft warning
	}

	decision := Decision{Action: "tool", ToolName: "file_read"}
	prep := []DecidePrep{{
		LoopDetected: DetectionResult{
			Detected: true,
			Rule:     "same_tool_freq",
			ToolName: "file_read", // same as decision.ToolName
		},
	}}

	action := node.Post(state, prep, decision)

	if action != core.ActionAnswer {
		t.Errorf("same tool on streak=2 should hard override to answer, got action=%s", action)
	}
}

func TestCountTrailingMetaTools(t *testing.T) {
	tests := []struct {
		name  string
		steps []StepRecord
		want  int
	}{
		{
			"empty",
			nil,
			0,
		},
		{
			"no meta tools",
			[]StepRecord{
				{Type: "tool", ToolName: "file_read"},
				{Type: "tool", ToolName: "shell_exec"},
			},
			0,
		},
		{
			"trailing meta tools",
			[]StepRecord{
				{Type: "tool", ToolName: "file_read"},
				{Type: "tool", ToolName: "update_plan"},
				{Type: "tool", ToolName: "update_plan"},
				{Type: "tool", ToolName: "update_plan"},
			},
			3,
		},
		{
			"meta tools with decide steps interleaved",
			[]StepRecord{
				{Type: "tool", ToolName: "file_read"},
				{Type: "decide", Action: "tool"},
				{Type: "tool", ToolName: "update_plan"},
				{Type: "decide", Action: "tool"},
				{Type: "tool", ToolName: "update_plan"},
			},
			2,
		},
		{
			"non-meta tool breaks streak",
			[]StepRecord{
				{Type: "tool", ToolName: "update_plan"},
				{Type: "tool", ToolName: "file_read"},
				{Type: "tool", ToolName: "update_plan"},
			},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countTrailingMetaTools(tt.steps)
			if got != tt.want {
				t.Errorf("countTrailingMetaTools() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMetaToolGuard_ForcesAnswerOnConsecutiveMetaTools(t *testing.T) {
	node := NewDecideNode(&mockLLMProvider{}, nil)

	// Build history with 4 consecutive update_plan tool steps (hard limit = 4)
	var steps []StepRecord
	steps = append(steps, StepRecord{Type: "tool", ToolName: "file_read", StepNumber: 1})
	for i := 2; i <= 5; i++ {
		steps = append(steps, StepRecord{Type: "decide", Action: "tool", StepNumber: i})
		steps = append(steps, StepRecord{Type: "tool", ToolName: "update_plan", StepNumber: i})
	}

	state := &AgentState{StepHistory: steps}
	decision := Decision{Action: "tool", ToolName: "update_plan"}
	prep := []DecidePrep{{}} // no loop detected (meta-tools excluded from LoopDetector)

	action := node.Post(state, prep, decision)

	if action != core.ActionAnswer {
		t.Errorf("4 consecutive meta-tool calls should force answer, got action=%v", action)
	}
}

func TestMetaToolGuard_SoftRedirectOnConsecutiveMetaTools(t *testing.T) {
	node := NewDecideNode(&mockLLMProvider{}, nil)

	// Build history with 2 consecutive update_plan tool steps (soft threshold = 2)
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_read", StepNumber: 1},
		{Type: "tool", ToolName: "update_plan", StepNumber: 2},
		{Type: "tool", ToolName: "update_plan", StepNumber: 3},
	}

	state := &AgentState{StepHistory: steps}
	decision := Decision{Action: "tool", ToolName: "update_plan"}
	prep := []DecidePrep{{}}

	action := node.Post(state, prep, decision)

	// Should allow the tool call (not force answer)
	if action != core.ActionTool {
		t.Errorf("2 consecutive meta-tool calls should allow tool (soft redirect), got action=%v", action)
	}
	// Should set redirect message for next Prep
	if state.MetaToolRedirectMsg == "" {
		t.Error("expected MetaToolRedirectMsg to be set for soft redirect")
	}
	if !strings.Contains(state.MetaToolRedirectMsg, "file_read") {
		t.Errorf("redirect should list actual tool names, got: %s", state.MetaToolRedirectMsg)
	}
}

func TestMetaToolGuard_AllowsNormalMetaToolUsage(t *testing.T) {
	node := NewDecideNode(&mockLLMProvider{}, nil)

	// Normal pattern: update_plan, real_tool, update_plan (non-consecutive, count resets)
	steps := []StepRecord{
		{Type: "tool", ToolName: "update_plan", StepNumber: 1},
		{Type: "tool", ToolName: "file_read", StepNumber: 2},
		{Type: "tool", ToolName: "update_plan", StepNumber: 3},
	}

	state := &AgentState{StepHistory: steps}
	decision := Decision{Action: "tool", ToolName: "update_plan"}
	prep := []DecidePrep{{}}

	action := node.Post(state, prep, decision)

	if action != core.ActionTool {
		t.Errorf("1 consecutive meta-tool call should be allowed, got action=%v", action)
	}
	// No redirect needed for single consecutive meta-tool
	if state.MetaToolRedirectMsg != "" {
		t.Errorf("should not set redirect for 1 consecutive meta-tool, got: %s", state.MetaToolRedirectMsg)
	}
	if state.SuppressMetaTools {
		t.Error("should not suppress meta-tools for 1 consecutive call")
	}
}

func TestMetaToolGuard_SuppressAndRestore(t *testing.T) {
	node := NewDecideNode(&mockLLMProvider{}, nil)

	// 2 consecutive meta-tools → suppress
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_read", StepNumber: 1},
		{Type: "tool", ToolName: "update_plan", StepNumber: 2},
		{Type: "tool", ToolName: "update_plan", StepNumber: 3},
	}
	state := &AgentState{StepHistory: steps}
	decision := Decision{Action: "tool", ToolName: "update_plan"}
	prep := []DecidePrep{{}}

	node.Post(state, prep, decision)
	if !state.SuppressMetaTools {
		t.Error("expected SuppressMetaTools=true after 2 consecutive meta-tool calls")
	}

	// Now LLM calls a real tool → restore
	state.StepHistory = append(state.StepHistory, StepRecord{Type: "tool", ToolName: "update_plan", StepNumber: 4})
	decision2 := Decision{Action: "tool", ToolName: "file_read"}
	node.Post(state, prep, decision2)
	if state.SuppressMetaTools {
		t.Error("expected SuppressMetaTools=false after non-meta tool call")
	}
}

func TestFilterOutMetaToolDefs(t *testing.T) {
	defs := []llm.ToolDefinition{
		{Name: "file_read"},
		{Name: "update_plan"},
		{Name: "shell_exec"},
		{Name: "walkthrough"},
		{Name: "file_write"},
	}
	filtered := filterOutMetaToolDefs(defs)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 non-meta tools, got %d", len(filtered))
	}
	for _, d := range filtered {
		if metaTools[d.Name] {
			t.Errorf("meta-tool %s should have been filtered out", d.Name)
		}
	}
}

func TestLastToolStep(t *testing.T) {
	t.Run("empty history", func(t *testing.T) {
		if got := lastToolStep(nil); got != nil {
			t.Errorf("expected nil for empty history, got %+v", got)
		}
	})

	t.Run("only decide steps", func(t *testing.T) {
		steps := []StepRecord{
			{Type: "decide", StepNumber: 1},
			{Type: "decide", StepNumber: 2},
		}
		if got := lastToolStep(steps); got != nil {
			t.Errorf("expected nil when no tool steps, got %+v", got)
		}
	})

	t.Run("returns last tool step", func(t *testing.T) {
		steps := []StepRecord{
			{Type: "tool", ToolName: "file_read", StepNumber: 1},
			{Type: "decide", StepNumber: 2},
			{Type: "tool", ToolName: "update_plan", StepNumber: 3, IsError: true},
			{Type: "decide", StepNumber: 4},
		}
		got := lastToolStep(steps)
		if got == nil || got.ToolName != "update_plan" || !got.IsError {
			t.Errorf("expected last tool step to be update_plan with error, got %+v", got)
		}
	})
}

func TestMetaToolGuard_ProactiveSuppressOnError(t *testing.T) {
	// When the last tool step is a meta-tool that returned an error,
	// Prep should proactively set SuppressMetaTools = true.
	reg := tool.NewRegistry()
	reg.Register(&mockTool{"file_read", "Read files"})
	reg.Register(&mockTool{"update_plan", "Update plan"})
	reg.Register(&mockTool{"shell_exec", "Run commands"})

	state := &AgentState{
		ToolCallMode: "fc",
		ToolRegistry: reg,
		StepHistory: []StepRecord{
			{Type: "tool", ToolName: "update_plan", StepNumber: 1, IsError: true},
		},
	}

	node := NewDecideNode(&mockLLMProvider{}, nil)
	preps := node.Prep(state)

	if !state.SuppressMetaTools {
		t.Fatal("expected SuppressMetaTools=true after meta-tool error")
	}

	// Verify meta-tools are filtered from tool definitions
	for _, d := range preps[0].ToolDefinitions {
		if metaTools[d.Name] {
			t.Errorf("meta-tool %s should be filtered from ToolDefinitions", d.Name)
		}
	}
}

func TestMetaToolGuard_NoProactiveSuppressOnSuccess(t *testing.T) {
	// When the last tool step is a meta-tool that succeeded,
	// Prep should NOT proactively suppress.
	reg := tool.NewRegistry()
	reg.Register(&mockTool{"file_read", "Read files"})
	reg.Register(&mockTool{"update_plan", "Update plan"})

	state := &AgentState{
		ToolCallMode: "fc",
		ToolRegistry: reg,
		StepHistory: []StepRecord{
			{Type: "tool", ToolName: "update_plan", StepNumber: 1, IsError: false},
		},
	}

	node := NewDecideNode(&mockLLMProvider{}, nil)
	node.Prep(state)

	if state.SuppressMetaTools {
		t.Error("expected SuppressMetaTools=false when meta-tool succeeded")
	}
}

func TestMetaToolGuard_NoProactiveSuppressOnNonMetaError(t *testing.T) {
	// When the last tool step is a non-meta tool that returned an error,
	// Prep should NOT proactively suppress meta-tools.
	reg := tool.NewRegistry()
	reg.Register(&mockTool{"file_read", "Read files"})
	reg.Register(&mockTool{"update_plan", "Update plan"})

	state := &AgentState{
		ToolCallMode: "fc",
		ToolRegistry: reg,
		StepHistory: []StepRecord{
			{Type: "tool", ToolName: "file_read", StepNumber: 1, IsError: true},
		},
	}

	node := NewDecideNode(&mockLLMProvider{}, nil)
	node.Prep(state)

	if state.SuppressMetaTools {
		t.Error("expected SuppressMetaTools=false when non-meta tool had error")
	}
}

func TestGenerateToolsPromptExcluding(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockTool{"file_read", "Read files"})
	reg.Register(&mockTool{"update_plan", "Update plan"})
	reg.Register(&mockTool{"shell_exec", "Run commands"})
	reg.Register(&mockTool{"walkthrough", "Walkthrough memo"})

	prompt := generateToolsPromptExcluding(reg, metaTools)

	if !strings.Contains(prompt, "file_read") {
		t.Error("prompt should contain file_read")
	}
	if !strings.Contains(prompt, "shell_exec") {
		t.Error("prompt should contain shell_exec")
	}
	if strings.Contains(prompt, "update_plan") {
		t.Error("prompt should NOT contain update_plan")
	}
	if strings.Contains(prompt, "walkthrough") {
		t.Error("prompt should NOT contain walkthrough")
	}
}

// ── Plan Sideband Tests ──

func TestParsePlanSideband_Valid(t *testing.T) {
	tests := []struct {
		reason     string
		wantStep   string
		wantStatus string
	}{
		{"创建 server.ts [plan:create_server:in_progress]", "create_server", "in_progress"},
		{"安装依赖完成 [plan:install_deps:done]", "install_deps", "done"},
		{"[plan:verify:done] 验证通过", "verify", "done"},
		{"中间 [plan:step_3:in_progress] 文本", "step_3", "in_progress"},
	}
	for _, tt := range tests {
		step, status := parsePlanSideband(tt.reason)
		if step != tt.wantStep || status != tt.wantStatus {
			t.Errorf("parsePlanSideband(%q) = (%q, %q), want (%q, %q)",
				tt.reason, step, status, tt.wantStep, tt.wantStatus)
		}
	}
}

func TestParsePlanSideband_Invalid(t *testing.T) {
	tests := []string{
		"普通 reason 没有标记",
		"[plan:create_server]",           // 缺少 status
		"[plan:create_server:pending]",   // invalid status
		"[plan::done]",                   // 缺少 step_id
		"plan:create_server:in_progress", // 缺少方括号
	}
	for _, reason := range tests {
		step, status := parsePlanSideband(reason)
		if step != "" || status != "" {
			t.Errorf("parsePlanSideband(%q) = (%q, %q), want empty", reason, step, status)
		}
	}
}

func TestPlanSideband_YAMLMode(t *testing.T) {
	yamlText := `action: tool
tool_name: file_write
tool_params:
  path: server.ts
reason: 创建文件
plan_step: create_server
plan_status: in_progress`

	decision, err := parseDecision(yamlText)
	if err != nil {
		t.Fatalf("parseDecision failed: %v", err)
	}
	if decision.PlanStep != "create_server" {
		t.Errorf("PlanStep = %q, want %q", decision.PlanStep, "create_server")
	}
	if decision.PlanStatus != "in_progress" {
		t.Errorf("PlanStatus = %q, want %q", decision.PlanStatus, "in_progress")
	}
}

func TestPlanSideband_PostUpdatesPlanStore(t *testing.T) {
	ps := plan.NewPlanStore()
	sid := "test-session"
	ps.Set(sid, []plan.PlanStep{
		{ID: "create_server", Title: "创建服务器", Status: "pending"},
		{ID: "install_deps", Title: "安装依赖", Status: "pending"},
	})

	node := &DecideNode{}
	state := &AgentState{
		PlanStore: ps,
		PlanSID:   sid,
	}

	// YAML mode: PlanStep/PlanStatus directly on Decision
	decision := Decision{
		Action:     "tool",
		ToolName:   "file_write",
		Reason:     "创建文件",
		PlanStep:   "create_server",
		PlanStatus: "in_progress",
	}

	node.Post(state, nil, decision)

	steps := ps.Get(sid)
	if steps[0].Status != "in_progress" {
		t.Errorf("step[0].Status = %q, want %q", steps[0].Status, "in_progress")
	}

	// FC mode: sideband in reason text
	decision2 := Decision{
		Action:   "tool",
		ToolName: "shell_exec",
		Reason:   "安装依赖 [plan:install_deps:done]",
	}

	node.Post(state, nil, decision2)

	steps = ps.Get(sid)
	if steps[1].Status != "done" {
		t.Errorf("step[1].Status = %q, want %q", steps[1].Status, "done")
	}
}

func TestPlanSideband_SSECallback(t *testing.T) {
	ps := plan.NewPlanStore()
	sid := "test-session"
	ps.Set(sid, []plan.PlanStep{
		{ID: "step_a", Title: "Step A", Status: "pending"},
	})

	var callbackSteps []plan.PlanStep
	node := &DecideNode{}
	state := &AgentState{
		PlanStore: ps,
		PlanSID:   sid,
		OnPlanUpdate: func(steps []plan.PlanStep) {
			callbackSteps = steps
		},
	}

	decision := Decision{
		Action:     "tool",
		ToolName:   "file_write",
		PlanStep:   "step_a",
		PlanStatus: "done",
	}

	node.Post(state, nil, decision)

	if callbackSteps == nil {
		t.Fatal("OnPlanUpdate callback was not called")
	}
	if len(callbackSteps) != 1 || callbackSteps[0].Status != "done" {
		t.Errorf("callback received wrong steps: %+v", callbackSteps)
	}
}
