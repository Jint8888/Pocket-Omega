package agent

import (
	"fmt"
	"strings"
)

// recentWindowSize is the number of recent tool steps to keep with full output.
// Older tool steps are compressed to a one-line metadata summary.
const recentWindowSize = 3

// recentWindowForSteps returns the dynamic window size based on total non-meta tool count.
// Long tasks (20+ tool steps) get a larger window to maintain coherence.
func recentWindowForSteps(toolCount int) int {
	if toolCount >= 20 {
		return 5
	}
	return recentWindowSize
}

// perStepOutputBudget computes the max characters per recent tool step in the decision
// prompt. Allocates toolOutputBudgetPct% of the context window to tool outputs and
// divides evenly across windowSize steps.
// Falls back to 8000 when contextWindowTokens is 0 (unconfigured), preserving
// existing behaviour.
func perStepOutputBudget(contextWindowTokens int, windowSize int) int {
	if contextWindowTokens <= 0 {
		return 8000 // backward-compatible default
	}
	if windowSize <= 0 {
		windowSize = recentWindowSize
	}
	const toolOutputBudgetPct = 40 // percent of context window reserved for tool outputs
	budget := contextWindowTokens * charsPerToken * toolOutputBudgetPct / 100 / windowSize
	if budget < 1000 {
		budget = 1000 // floor: keep outputs useful even on tiny context windows
	}
	return budget
}

// stepDedupKey is used for duplicate detection in step summaries.
type stepDedupKey struct {
	name  string
	param string
}

func makeStepDedupKey(s StepRecord) stepDedupKey {
	if paramName, ok := paramDedupTools[s.ToolName]; ok {
		return stepDedupKey{name: s.ToolName, param: extractParam(s.Input, paramName)}
	}
	return stepDedupKey{name: s.ToolName, param: s.Input}
}

func buildDupWarning(s StepRecord, seen map[stepDedupKey]int) string {
	key := makeStepDedupKey(s)
	if firstStep, exists := seen[key]; exists && firstStep != s.StepNumber {
		return fmt.Sprintf(" ⚠️[与步骤%d重复，可复用其结果]", firstStep)
	}
	return ""
}

func buildStepSummary(steps []StepRecord, contextWindowTokens int) string {
	if len(steps) == 0 {
		return ""
	}

	// Phase 1: collect tool steps + build dedup map
	seen := make(map[stepDedupKey]int)
	var toolSteps []StepRecord
	for _, s := range steps {
		if s.Type != "tool" {
			continue
		}
		key := makeStepDedupKey(s)
		if _, exists := seen[key]; !exists {
			seen[key] = s.StepNumber
		}
		toolSteps = append(toolSteps, s)
	}

	if len(toolSteps) == 0 {
		// Only think/answer steps — render them directly
		var sb strings.Builder
		for _, s := range steps {
			switch s.Type {
			case "think":
				sb.WriteString(fmt.Sprintf("  步骤 %d [推理]: %s\n", s.StepNumber, truncate(s.Output, 200)))
			case "answer":
				sb.WriteString(fmt.Sprintf("  步骤 %d [回答]: %s\n", s.StepNumber, truncate(s.Output, 200)))
			}
		}
		return sb.String()
	}

	// Phase 2: select Zone A candidates (non-meta tool steps, newest N)
	var nonMeta []StepRecord
	for _, s := range toolSteps {
		if !skipAutoSummaryTools[s.ToolName] {
			nonMeta = append(nonMeta, s)
		}
	}
	windowSize := recentWindowForSteps(len(nonMeta))
	budget := perStepOutputBudget(contextWindowTokens, windowSize)

	zoneAStart := len(nonMeta) - windowSize
	if zoneAStart < 0 {
		zoneAStart = 0
	}
	zoneASteps := nonMeta[zoneAStart:]
	zoneASet := make(map[int]bool, len(zoneASteps))
	for _, s := range zoneASteps {
		zoneASet[s.StepNumber] = true
	}

	// Phase 3: render
	var sb strings.Builder
	hasZoneB := len(toolSteps) > len(zoneASteps)

	// Zone A: recent tool results (newest-first, full output)
	if len(zoneASteps) > 0 && hasZoneB {
		sb.WriteString("--- 最近工具结果 ---\n")
	}
	for i := len(zoneASteps) - 1; i >= 0; i-- {
		s := zoneASteps[i]
		dup := buildDupWarning(s, seen)
		sb.WriteString(fmt.Sprintf("  步骤 %d [工具 %s]: %s%s\n",
			s.StepNumber, s.ToolName, truncate(s.Output, budget), dup))
	}

	// Zone B: older steps (chronological, compressed)
	if hasZoneB {
		sb.WriteString("--- 执行历史 ---\n")
		for _, s := range toolSteps {
			if zoneASet[s.StepNumber] {
				continue
			}
			if skipAutoSummaryTools[s.ToolName] {
				// Meta-tools: ultra-compact one-liner (no output detail)
				sb.WriteString(fmt.Sprintf("  步骤 %d [%s]: ✓ 已调用\n", s.StepNumber, s.ToolName))
				continue
			}
			dup := buildDupWarning(s, seen)
			sb.WriteString(fmt.Sprintf("  步骤 %d [工具 %s]: 已执行 (%s)，输出 %d bytes%s\n",
				s.StepNumber, s.ToolName, truncate(s.Input, 80), len(s.Output), dup))
		}
	}

	// Think/Answer steps (rare, append at end)
	for _, s := range steps {
		switch s.Type {
		case "think":
			sb.WriteString(fmt.Sprintf("  步骤 %d [推理]: %s\n", s.StepNumber, truncate(s.Output, 200)))
		case "answer":
			sb.WriteString(fmt.Sprintf("  步骤 %d [回答]: %s\n", s.StepNumber, truncate(s.Output, 200)))
		}
	}

	return sb.String()
}
