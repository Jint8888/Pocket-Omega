package agent

import "testing"

func TestExploration_NotTriggered_EarlyPhase(t *testing.T) {
	// StepCount <= MaxSteps/3 should not trigger
	steps := make([]StepRecord, 10)
	for i := range steps {
		steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30) // 10 <= 30/3=10, not triggered
	if result.Detected {
		t.Error("should not trigger when StepCount <= MaxSteps/3")
	}
}

func TestExploration_NotTriggered_MixedTools(t *testing.T) {
	// Recent steps include a write tool — should not trigger
	steps := make([]StepRecord, 15)
	for i := range steps {
		steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	}
	// Insert a write tool in the recent window
	steps[13] = StepRecord{Type: "tool", ToolName: "file_write", Input: `{"path":"b.go","content":"x"}`}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30) // 15 > 10, but recent 5 has file_write
	if result.Detected {
		t.Error("should not trigger when recent steps include write tools")
	}
}

func TestExploration_Triggered(t *testing.T) {
	// Over 1/3 and recent 5 are all file_read
	steps := make([]StepRecord, 15)
	for i := range steps {
		steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30) // 15 > 10, recent 5 all file_read
	if !result.Detected {
		t.Error("should trigger when over 1/3 and recent steps are all info-gathering")
	}
	if result.Description == "" {
		t.Error("description should not be empty")
	}
}

func TestExploration_ShellExec_ReadOnly(t *testing.T) {
	// shell_exec("dir .") should be recognized as info-gathering
	steps := make([]StepRecord, 15)
	for i := range steps {
		steps[i] = StepRecord{Type: "tool", ToolName: "shell_exec", Input: `{"command":"dir ."}`}
	}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30)
	if !result.Detected {
		t.Error("shell_exec with read-only command should be detected as info-gathering")
	}
}

func TestExploration_ShellExec_Write(t *testing.T) {
	// shell_exec("npm install") should NOT be recognized as info-gathering
	steps := make([]StepRecord, 15)
	for i := range steps {
		steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	}
	// Replace last step with a non-read-only shell command
	steps[14] = StepRecord{Type: "tool", ToolName: "shell_exec", Input: `{"command":"npm install"}`}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30)
	if result.Detected {
		t.Error("shell_exec with write command should not be detected as info-gathering")
	}
}

func TestIsReadOnlyShellCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"dir .", true},
		{"ls -la", true},
		{"type file.txt", true},
		{"cat README.md", true},
		{"find . -name '*.go'", true},
		{"head -n 10 file.go", true},
		{"tail -f log.txt", true},
		{"tree", true},
		{"DIR /s", true},       // case insensitive
		{"LS", true},           // bare command
		{"npm install", false},
		{"go build ./...", false},
		{"rm -rf /tmp", false},
		{"echo hello", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isReadOnlyShellCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("isReadOnlyShellCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestExploration_NotTriggered_TooFewToolSteps(t *testing.T) {
	// Enough total steps but fewer than explorationWindow tool steps
	steps := make([]StepRecord, 15)
	for i := range steps {
		steps[i] = StepRecord{Type: "decide", Action: "tool"}
	}
	// Only 3 tool steps (< explorationWindow=5)
	steps[12] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	steps[13] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"b.go"}`}
	steps[14] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"c.go"}`}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30)
	if result.Detected {
		t.Error("should not trigger with fewer than explorationWindow tool steps")
	}
}

func TestExploration_MetaToolsExcluded(t *testing.T) {
	// update_plan steps should be excluded from the analysis window.
	// Without exclusion, the recent window would include update_plan (non-info-gathering)
	// and detection would NOT trigger. With exclusion, only file_read steps remain.
	steps := make([]StepRecord, 20)
	for i := 0; i < 10; i++ {
		steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	}
	// Interleave update_plan among file_read
	for i := 10; i < 20; i++ {
		if i%2 == 0 {
			steps[i] = StepRecord{Type: "tool", ToolName: "update_plan", Input: `{"operation":"update","step_id":"s1","status":"done"}`}
		} else {
			steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"b.go"}`}
		}
	}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30) // 20 > 10; non-meta tool steps are all file_read
	if !result.Detected {
		t.Error("should trigger: meta-tools excluded, remaining recent steps are all info-gathering")
	}
}

func TestExploration_WalkthroughExcluded(t *testing.T) {
	// walkthrough is also a meta-tool and should be excluded
	steps := make([]StepRecord, 15)
	for i := range steps {
		steps[i] = StepRecord{Type: "tool", ToolName: "file_read", Input: `{"path":"a.go"}`}
	}
	// Replace some with walkthrough — should not break detection
	steps[11] = StepRecord{Type: "tool", ToolName: "walkthrough", Input: `{"action":"add"}`}
	steps[13] = StepRecord{Type: "tool", ToolName: "walkthrough", Input: `{"action":"add"}`}
	d := &ExplorationDetector{}
	result := d.Check(steps, 30)
	if !result.Detected {
		t.Error("should trigger: walkthrough excluded, remaining recent steps are all info-gathering")
	}
}
