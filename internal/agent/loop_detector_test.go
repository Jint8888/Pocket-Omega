package agent

import (
	"testing"
)

// ── Rule 1: Same Tool Frequency ──

func TestCheck_SameToolFrequency_Triggered(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "web_search", Input: `{"query":"rust"}`, StepNumber: 1},
		{Type: "decide", StepNumber: 2},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"rust lang"}`, StepNumber: 3},
		{Type: "decide", StepNumber: 4},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"rust features"}`, StepNumber: 5},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection")
	}
	if r.Rule != "same_tool_freq" {
		t.Fatalf("expected rule same_tool_freq, got %s", r.Rule)
	}
}

func TestCheck_SameToolFrequency_NotTriggered(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Go concurrency patterns"}`, StepNumber: 1},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Python async tutorial"}`, StepNumber: 2},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatalf("expected no detection for 2 calls with different queries, got rule=%s", r.Rule)
	}
}

func TestCheck_SameToolFrequency_DifferentTools(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "web_search", Input: `{"query":"a"}`, StepNumber: 1},
		{Type: "tool", ToolName: "file_read", Input: `{"path":"a.txt"}`, StepNumber: 2},
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"ls"}`, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatal("expected no detection for different tools")
	}
}

func TestCheck_SameToolFrequency_FileToolDiffPath(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_read", Input: `{"path":"a.txt"}`, StepNumber: 1},
		{Type: "tool", ToolName: "file_read", Input: `{"path":"b.txt"}`, StepNumber: 2},
		{Type: "tool", ToolName: "file_read", Input: `{"path":"c.txt"}`, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatal("expected no detection: 3 file_reads with different paths is legitimate")
	}
}

func TestCheck_SameToolFrequency_FileToolSamePath(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_read", Input: `{"path":"config.yaml"}`, StepNumber: 1},
		{Type: "tool", ToolName: "file_read", Input: `{"path":"config.yaml"}`, StepNumber: 2},
		{Type: "tool", ToolName: "file_read", Input: `{"path":"config.yaml"}`, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection: same file read 3 times")
	}
	if r.Rule != "same_tool_freq" {
		t.Fatalf("expected same_tool_freq, got %s", r.Rule)
	}
}

func TestCheck_SameToolFrequency_ShellExecDiffCommands(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"go build"}`, StepNumber: 1},
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"go test"}`, StepNumber: 2},
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"go vet"}`, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatal("expected no detection: 3 different shell commands is legitimate")
	}
}

func TestCheck_SameToolFrequency_ShellExecSameCommand(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"go build ./..."}`, StepNumber: 1},
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"go build ./..."}`, StepNumber: 2},
		{Type: "tool", ToolName: "shell_exec", Input: `{"command":"go build ./..."}`, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection: same shell command run 3 times")
	}
	if r.Rule != "same_tool_freq" {
		t.Fatalf("expected same_tool_freq, got %s", r.Rule)
	}
}

// ── Rule 2: Similar Params ──

func TestCheck_SimilarParams_SearchQueryChinese(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Rust 最新特性介绍总结"}`, StepNumber: 1},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Rust 最新特性介绍汇总"}`, StepNumber: 2},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection: similar Chinese queries")
	}
	if r.Rule != "similar_params" {
		t.Fatalf("expected similar_params, got %s", r.Rule)
	}
}

func TestCheck_SimilarParams_SearchQueryEnglish(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Rust features 2025"}`, StepNumber: 1},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Rust latest features 2025"}`, StepNumber: 2},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection: similar English queries")
	}
}

func TestCheck_SimilarParams_SameFilePath(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_read", Input: `{"path":"main.go"}`, StepNumber: 1},
		{Type: "tool", ToolName: "file_read", Input: `{"path":"main.go"}`, StepNumber: 2},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection: same file read twice consecutively")
	}
}

func TestCheck_SimilarParams_DifferentParams(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Go concurrency"}`, StepNumber: 1},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Python async await"}`, StepNumber: 2},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatal("expected no detection: completely different queries")
	}
}

// ── Rule 3: Consecutive Errors ──

func TestCheck_ConsecutiveErrors_Triggered(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_patch", Input: `{}`, IsError: true, StepNumber: 1},
		{Type: "tool", ToolName: "file_patch", Input: `{}`, IsError: true, StepNumber: 2},
		{Type: "tool", ToolName: "file_read", Input: `{}`, IsError: true, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if !r.Detected {
		t.Fatal("expected detection: 3 consecutive errors")
	}
	if r.Rule != "consecutive_errors" {
		t.Fatalf("expected consecutive_errors, got %s", r.Rule)
	}
}

func TestCheck_ConsecutiveErrors_Interrupted(t *testing.T) {
	steps := []StepRecord{
		{Type: "tool", ToolName: "file_patch", Input: `{}`, IsError: true, StepNumber: 1},
		{Type: "tool", ToolName: "file_read", Input: `{}`, IsError: false, StepNumber: 2},
		{Type: "tool", ToolName: "file_patch", Input: `{}`, IsError: true, StepNumber: 3},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatal("expected no detection: success interrupted the streak")
	}
}

// ── Edge Cases ──

func TestCheck_NoSteps(t *testing.T) {
	d := LoopDetector{}
	r := d.Check(nil)
	if r.Detected {
		t.Fatal("expected no detection on empty history")
	}
}

func TestCheck_NormalFlow(t *testing.T) {
	steps := []StepRecord{
		{Type: "decide", StepNumber: 1},
		{Type: "tool", ToolName: "web_search", Input: `{"query":"Go 1.22"}`, StepNumber: 2},
		{Type: "decide", StepNumber: 3},
		{Type: "tool", ToolName: "web_reader", Input: `{"url":"https://go.dev"}`, StepNumber: 4},
		{Type: "decide", StepNumber: 5},
		{Type: "answer", Output: "Go 1.22 features...", StepNumber: 6},
	}
	d := LoopDetector{}
	r := d.Check(steps)
	if r.Detected {
		t.Fatal("expected no detection: normal 2-tool flow")
	}
}

// ── Bigrams + Jaccard ──

func TestBigrams_English(t *testing.T) {
	b := bigrams("hello")
	expected := map[string]bool{"he": true, "el": true, "ll": true, "lo": true}
	if len(b) != len(expected) {
		t.Fatalf("expected %d bigrams, got %d", len(expected), len(b))
	}
	for k := range expected {
		if !b[k] {
			t.Fatalf("missing bigram %q", k)
		}
	}
}

func TestBigrams_Chinese(t *testing.T) {
	b := bigrams("你好世界")
	expected := map[string]bool{"你好": true, "好世": true, "世界": true}
	if len(b) != len(expected) {
		t.Fatalf("expected %d bigrams, got %d", len(expected), len(b))
	}
	for k := range expected {
		if !b[k] {
			t.Fatalf("missing bigram %q", k)
		}
	}
}

func TestJaccardSimilarity(t *testing.T) {
	// Identical sets → 1.0
	a := map[string]bool{"ab": true, "bc": true}
	if j := jaccardSimilarity(a, a); j != 1.0 {
		t.Fatalf("expected 1.0, got %f", j)
	}

	// Disjoint sets → 0.0
	b := map[string]bool{"xy": true, "yz": true}
	if j := jaccardSimilarity(a, b); j != 0.0 {
		t.Fatalf("expected 0.0, got %f", j)
	}

	// Partial overlap
	c := map[string]bool{"ab": true, "cd": true}
	j := jaccardSimilarity(a, c)
	// intersection=1 (ab), union=3 (ab,bc,cd), j=1/3≈0.333
	if j < 0.3 || j > 0.4 {
		t.Fatalf("expected ~0.333, got %f", j)
	}
}

func TestJaccardSimilarity_BothEmpty(t *testing.T) {
	j := jaccardSimilarity(map[string]bool{}, map[string]bool{})
	if j != 1.0 {
		t.Fatalf("expected 1.0 for empty sets, got %f", j)
	}
	// Also test with nil-like empty from bigrams("")
	j2 := jaccardSimilarity(bigrams(""), bigrams(""))
	if j2 != 1.0 {
		t.Fatalf("expected 1.0 for bigrams of empty strings, got %f", j2)
	}
}

// ── isSearchTool tests ──

func TestIsSearchTool_IncludesBrave(t *testing.T) {
	tests := []struct {
		name string
		tool string
		want bool
	}{
		{"web_search", "web_search", true},
		{"search_tavily", "search_tavily", true},
		{"search_brave", "search_brave", true},
		{"mcp_search_tool", "mcp_google_search", true},
		{"file_read", "file_read", false},
		{"shell_exec", "shell_exec", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSearchTool(tt.tool)
			if got != tt.want {
				t.Errorf("isSearchTool(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}
