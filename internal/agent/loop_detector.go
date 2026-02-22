package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ── Loop Detection Constants ──

const (
	loopWindowSize          = 8   // recent tool steps to analyze
	loopSameToolLimit       = 3   // Rule 1: same tool call limit
	loopConsecErrorLimit    = 3   // Rule 3: consecutive error limit
	loopSimilarityThreshold = 0.6 // Rule 2: bigram Jaccard threshold
)

// paramDedupTools maps tool names to the JSON key used for deduplication.
// These tools are exempt from pure frequency counting in Rule 1 —
// they only count if the specified param is also identical.
var paramDedupTools = map[string]string{
	"file_read":   "path",
	"file_write":  "path",
	"file_patch":  "path",
	"file_list":   "path",
	"file_move":   "path",
	"file_delete": "path",
	"file_grep":   "path",
	"shell_exec":  "command",
}

// LoopDetector analyzes StepHistory to detect repetitive agent behavior.
// Stateless: all detection is based on the StepHistory slice passed in.
type LoopDetector struct{}

// DetectionResult describes a detected loop pattern.
type DetectionResult struct {
	Detected    bool   // whether a loop was detected
	Rule        string // which rule triggered: "same_tool_freq", "similar_params", "consecutive_errors"
	Description string // human-readable description for prompt injection
}

// Check analyzes the step history and returns detection result.
// Rules are evaluated in order; first match wins.
func (d *LoopDetector) Check(steps []StepRecord) DetectionResult {
	toolSteps := filterToolSteps(steps)
	if len(toolSteps) < 2 {
		return DetectionResult{}
	}

	// Rule 1: same tool frequency
	if r := d.checkSameToolFrequency(toolSteps); r.Detected {
		return r
	}

	// Rule 2: similar params on consecutive calls
	if r := d.checkSimilarParams(toolSteps); r.Detected {
		return r
	}

	// Rule 3: consecutive errors
	if r := d.checkConsecutiveErrors(toolSteps); r.Detected {
		return r
	}

	return DetectionResult{}
}

// ── Rule 1: Same Tool Frequency ──

func (d *LoopDetector) checkSameToolFrequency(toolSteps []StepRecord) DetectionResult {
	window := recentWindow(toolSteps, loopWindowSize)

	// Count per-tool frequency, respecting paramDedupTools exemptions.
	type toolKey struct {
		name  string
		param string // empty for non-dedup tools
	}
	freq := make(map[toolKey]int)

	for _, s := range window {
		key := toolKey{name: s.ToolName}
		if paramKey, ok := paramDedupTools[s.ToolName]; ok {
			key.param = extractParam(s.Input, paramKey)
		}
		freq[key]++
	}

	for k, count := range freq {
		if count >= loopSameToolLimit {
			desc := k.name + " 被调用了 " + strconv.Itoa(count) + " 次"
			if k.param != "" {
				desc += "（参数: " + truncate(k.param, 60) + "）"
			}
			return DetectionResult{
				Detected:    true,
				Rule:        "same_tool_freq",
				Description: desc,
			}
		}
	}
	return DetectionResult{}
}

// ── Rule 2: Similar Params ──

func (d *LoopDetector) checkSimilarParams(toolSteps []StepRecord) DetectionResult {
	if len(toolSteps) < 2 {
		return DetectionResult{}
	}

	last := toolSteps[len(toolSteps)-1]
	prev := toolSteps[len(toolSteps)-2]

	if last.ToolName != prev.ToolName {
		return DetectionResult{}
	}

	similar := false
	switch {
	case isSearchTool(last.ToolName):
		q1 := extractParam(prev.Input, "query")
		q2 := extractParam(last.Input, "query")
		if q1 != "" && q2 != "" {
			similar = jaccardSimilarity(bigrams(q1), bigrams(q2)) > loopSimilarityThreshold
		}
	case paramDedupTools[last.ToolName] == "path":
		p1 := extractParam(prev.Input, "path")
		p2 := extractParam(last.Input, "path")
		similar = p1 != "" && p1 == p2
	default:
		similar = prev.Input == last.Input
	}

	if similar {
		return DetectionResult{
			Detected:    true,
			Rule:        "similar_params",
			Description: last.ToolName + " 连续调用且参数相似",
		}
	}
	return DetectionResult{}
}

// ── Rule 3: Consecutive Errors ──

func (d *LoopDetector) checkConsecutiveErrors(toolSteps []StepRecord) DetectionResult {
	if len(toolSteps) < loopConsecErrorLimit {
		return DetectionResult{}
	}

	// Check last K tool steps
	tail := toolSteps[len(toolSteps)-loopConsecErrorLimit:]
	for _, s := range tail {
		if !s.IsError {
			return DetectionResult{}
		}
	}

	return DetectionResult{
		Detected:    true,
		Rule:        "consecutive_errors",
		Description: "最近 " + strconv.Itoa(loopConsecErrorLimit) + " 次工具调用均失败",
	}
}

// ── Helpers ──

// filterToolSteps extracts only type="tool" steps from history.
func filterToolSteps(steps []StepRecord) []StepRecord {
	var result []StepRecord
	for _, s := range steps {
		if s.Type == "tool" {
			result = append(result, s)
		}
	}
	return result
}

// recentWindow returns the last n items from a slice.
func recentWindow(steps []StepRecord, n int) []StepRecord {
	if len(steps) <= n {
		return steps
	}
	return steps[len(steps)-n:]
}

// extractParam parses JSON input and extracts a string parameter by key.
// Returns "" on any failure (invalid JSON, missing key, non-string value).
func extractParam(jsonInput string, key string) string {
	var params map[string]any
	if err := json.Unmarshal([]byte(jsonInput), &params); err != nil {
		return ""
	}
	v, ok := params[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// isSearchTool returns true for tools where query similarity matters.
func isSearchTool(name string) bool {
	return name == "web_search" || name == "search_tavily" || name == "search_brave" ||
		(strings.HasPrefix(name, "mcp_") && strings.Contains(name, "search"))
}

// bigrams splits a string into character bigram set.
// Works for both English and Chinese (rune-based).
func bigrams(s string) map[string]bool {
	runes := []rune(s)
	set := make(map[string]bool)
	for i := 0; i < len(runes)-1; i++ {
		set[string(runes[i:i+2])] = true
	}
	return set
}

// jaccardSimilarity computes |A∩B| / |A∪B|.
// Guard: two empty sets are treated as fully similar (avoids 0/0 = NaN).
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}

	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}

	// |A∪B| = |A| + |B| - |A∩B|
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}
