package agent

import (
	"testing"
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
