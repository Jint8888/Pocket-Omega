package agent

import (
	"fmt"
	"strings"
)

const explorationWindow = 5 // recent tool steps to check

// ExplorationResult describes exploration phase overrun detection.
type ExplorationResult struct {
	Detected    bool
	Description string
}

// ExplorationDetector checks if the agent is stuck in exploration phase.
// Stateless: all detection is based on StepHistory + MaxAgentSteps.
type ExplorationDetector struct{}

// Check returns detection result.
// Triggers when StepCount > MaxAgentSteps/3 AND recent N tool steps are all info-gathering.
// Meta-tools (update_plan, walkthrough) are excluded from the window — they don't
// count as "execution has started" since they're just bookkeeping overhead.
func (d *ExplorationDetector) Check(steps []StepRecord, maxSteps int) ExplorationResult {
	if len(steps) <= maxSteps/3 {
		return ExplorationResult{}
	}
	toolSteps := filterNonMetaToolSteps(steps)
	if len(toolSteps) < explorationWindow {
		return ExplorationResult{}
	}
	recent := recentWindow(toolSteps, explorationWindow)
	for _, s := range recent {
		if !isInfoGatheringTool(s) {
			return ExplorationResult{}
		}
	}
	return ExplorationResult{
		Detected:    true,
		Description: fmt.Sprintf("已用 %d/%d 步，最近 %d 步均为信息收集，请开始执行", len(steps), maxSteps, explorationWindow),
	}
}

// isInfoGatheringTool returns true for read-only information gathering tools.
func isInfoGatheringTool(s StepRecord) bool {
	switch s.ToolName {
	case "file_read", "file_list", "file_grep", "file_find":
		return true
	case "shell_exec":
		return isReadOnlyShellCommand(extractParam(s.Input, "command"))
	}
	return false
}

// readOnlyCommands are shell commands considered read-only (info gathering).
// Bare command names only — prefix matching with " " separator is handled in code.
var readOnlyCommands = []string{"dir", "ls", "type", "cat", "find", "head", "tail", "tree"}

// isReadOnlyShellCommand checks if a shell command is read-only (info gathering).
// Matches both bare commands ("ls") and commands with arguments ("ls -la").
func isReadOnlyShellCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if lower == "" {
		return false
	}
	for _, name := range readOnlyCommands {
		if lower == name || strings.HasPrefix(lower, name+" ") {
			return true
		}
	}
	return false
}

// metaTools are bookkeeping tools that don't represent real exploration or execution.
// They are excluded from the ExplorationDetector's analysis window.
var metaTools = map[string]bool{
	"update_plan":  true,
	"walkthrough":  true,
}

// filterNonMetaToolSteps extracts type="tool" steps excluding meta-tools.
func filterNonMetaToolSteps(steps []StepRecord) []StepRecord {
	var result []StepRecord
	for _, s := range steps {
		if s.Type == "tool" && !metaTools[s.ToolName] {
			result = append(result, s)
		}
	}
	return result
}
