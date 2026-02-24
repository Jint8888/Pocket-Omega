package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/util"
	"gopkg.in/yaml.v3"
)

// ── YAML parsing ──

func parseDecision(raw string) (Decision, error) {
	yamlStr, err := extractYAML(raw)
	if err != nil {
		yamlStr = raw
	}

	var decision Decision
	if err := yaml.Unmarshal([]byte(yamlStr), &decision); err != nil {
		// Retry with backslash fix: LLMs often produce Windows paths like
		// path: "E:\AI\Pocket-Omega\docs" which breaks YAML double-quoted
		// string escaping. Replace backslashes with forward slashes in
		// double-quoted values as a recovery strategy.
		fixed := fixBackslashes(yamlStr)
		if err2 := yaml.Unmarshal([]byte(fixed), &decision); err2 != nil {
			return Decision{}, fmt.Errorf("YAML parse error: %w", err)
		}
		log.Printf("[Decide] Recovered from YAML backslash issue")
	}

	if decision.Action == "" {
		return Decision{}, fmt.Errorf("decision missing 'action' field")
	}

	return decision, nil
}

// extractYAML extracts YAML content from a ```yaml ... ``` code block.
// Returns an error only when a code block opening is found but no closing marker.
//
// This is a package-local copy of thinking.ExtractYAML to decouple the agent
// package from the thinking package (R3: the function is a generic string utility
// with no semantic relationship to the thinking/CoT subsystem).
func extractYAML(content string) (string, error) {
	// Try ```yaml ... ``` first
	if idx := strings.Index(content, "```yaml"); idx >= 0 {
		rest := content[idx+7:]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end]), nil
		}
		return "", fmt.Errorf("unclosed ```yaml code block")
	}
	// Try ``` ... ``` as fallback
	if idx := strings.Index(content, "```"); idx >= 0 {
		rest := content[idx+3:]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end]), nil
		}
		return "", fmt.Errorf("unclosed ``` code block")
	}
	// No code block found — try the whole content as YAML
	return strings.TrimSpace(content), nil
}

// fixBackslashes replaces Windows-path backslashes with forward slashes inside
// double-quoted YAML values to fix Windows path escape issues.
//
// Strategy: Use regex to find Windows drive-path patterns (e.g. "E:\AI\docs")
// inside double-quoted strings and replace their backslashes with forward slashes.
// This avoids the character-by-character ambiguity where \f could be a YAML
// escape (form-feed) or a path segment (\foo).
var windowsPathInQuotes = regexp.MustCompile(`"([A-Za-z]:\\[^"]*)"`)

func fixBackslashes(s string) string {
	return windowsPathInQuotes.ReplaceAllStringFunc(s, func(match string) string {
		// match includes surrounding quotes: "E:\AI\docs"
		// Replace all backslashes between the quotes with forward slashes
		inner := match[1 : len(match)-1] // strip quotes
		inner = strings.ReplaceAll(inner, `\`, `/`)
		return `"` + inner + `"`
	})
}

func truncate(s string, maxLen int) string { return util.TruncateRunes(s, maxLen) }

// ── MetaToolGuard helpers ──

// countTrailingMetaTools counts how many consecutive meta-tool steps are at the
// end of the step history. Used by MetaToolGuard to detect bookkeeping loops.
func countTrailingMetaTools(steps []StepRecord) int {
	count := 0
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Type == "tool" && metaTools[s.ToolName] {
			count++
		} else if s.Type == "tool" {
			break // non-meta tool step breaks the streak
		}
		// skip decide/think/answer steps — they don't break the meta-tool streak
	}
	return count
}

// lastToolStep returns the most recent type="tool" step, or nil if none.
// Used by proactive MetaToolGuard to check if the last tool returned an error.
func lastToolStep(steps []StepRecord) *StepRecord {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Type == "tool" {
			return &steps[i]
		}
	}
	return nil
}

// filterOutMetaToolDefs removes meta-tools from FC tool definitions.
// Used by SuppressMetaTools to physically prevent the LLM from calling meta-tools
// when it's stuck in a loop — the nuclear option for weaker models that ignore errors.
func filterOutMetaToolDefs(defs []llm.ToolDefinition) []llm.ToolDefinition {
	filtered := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if !metaTools[d.Name] {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// generateToolsPromptExcluding rebuilds the YAML tools prompt excluding meta-tools.
// Used instead of clearing toolsPrompt entirely, so the LLM still sees non-meta tools.
func generateToolsPromptExcluding(reg *tool.Registry, exclude map[string]bool) string {
	tools := reg.List()
	var sb strings.Builder
	sb.WriteString("可用工具：\n")
	count := 0
	for _, t := range tools {
		if exclude[t.Name()] {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n### %s\n%s\n", t.Name(), t.Description()))
		schema := t.InputSchema()
		if len(schema) > 0 {
			sb.WriteString(fmt.Sprintf("参数 Schema: %s\n", string(schema)))
		}
		count++
	}
	if count == 0 {
		return "（无可用工具）"
	}
	return sb.String()
}

// ── FC content parsing ──

// parseNativeFCContent extracts a tool call from models (e.g. Kimi-K2.5) that
// embed FC intent in the Content field using special tokens rather than the
// standard tool_calls field.
//
// Expected format in Content:
//
//	<|tool_calls_section_begin|>[{"name":"tool","parameters":{...}}]<|tool_call_end|>
//
// The function also tolerates "arguments" as an alias for "parameters" to handle
// minor format variations across model versions.
//
// Returns the parsed Decision and true on success; zero-value Decision and false
// when the format doesn't match, JSON is malformed, or the tool name is unknown.
func parseNativeFCContent(content string, toolDefs []llm.ToolDefinition) (Decision, bool) {
	const startMark = "<|tool_calls_section_begin|>"
	const endMark = "<|tool_call_end|>"

	startIdx := strings.Index(content, startMark)
	endIdx := strings.Index(content, endMark)
	if startIdx < 0 || endIdx <= startIdx {
		return Decision{}, false
	}

	jsonStr := strings.TrimSpace(content[startIdx+len(startMark) : endIdx])

	// Kimi format: array of objects with "name" and "parameters" (or "arguments")
	var calls []struct {
		Name       string         `json:"name"`
		Parameters map[string]any `json:"parameters"`
		Arguments  map[string]any `json:"arguments"` // fallback alias
	}
	if err := json.Unmarshal([]byte(jsonStr), &calls); err != nil || len(calls) == 0 {
		log.Printf("[Decide] Native FC tokens: JSON parse failed (json=%s): %v", truncate(jsonStr, 120), err)
		return Decision{}, false
	}

	tc := calls[0]
	params := tc.Parameters
	if params == nil {
		params = tc.Arguments
	}
	if params == nil {
		params = make(map[string]any)
	}

	// Validate tool name against registered definitions
	if len(toolDefs) > 0 {
		found := false
		for _, td := range toolDefs {
			if td.Name == tc.Name {
				found = true
				break
			}
		}
		if !found {
			log.Printf("[Decide] Native FC tokens: unknown tool %q", tc.Name)
			return Decision{}, false
		}
	}

	// Extract reasoning text before FC tokens (content before <|tool_calls_section_begin|>)
	reason := strings.TrimSpace(content[:startIdx])
	if reason == "" {
		reason = fmt.Sprintf("native FC: call %s", tc.Name)
	} else {
		reason = truncate(reason, 200)
	}

	return Decision{
		Action:     "tool",
		Reason:     reason,
		ToolName:   tc.Name,
		ToolParams: params,
	}, true
}

// ── Phase 1: Tool Summary + Runtime Line ──

// coreToolOrder defines display priority for core tools (most used first).
var coreToolOrder = []string{
	"file_read", "file_write", "file_grep", "file_find", "file_list",
	"file_patch", "file_move", "file_delete", "file_open",
	"shell_exec",
	"web_reader", "search_tavily", "search_brave", "http_request",
	"time_get", "config_edit",
}

// mgmtToolOrder defines display priority for management tools.
var mgmtToolOrder = []string{
	"mcp_server_add", "mcp_server_remove", "mcp_server_list", "mcp_reload",
}

// buildToolingSection generates a compact tool summary section from Registry.
// Tools are ordered by priority: core → management → external MCP (alphabetical).
func buildToolingSection(registry *tool.Registry) string {
	if registry == nil {
		return ""
	}

	tools := registry.List()
	if len(tools) == 0 {
		return ""
	}

	// Build lookup map: name → tool
	toolMap := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		toolMap[t.Name()] = t
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n")

	emitted := make(map[string]bool, len(tools))

	// Emit in priority order
	for _, name := range coreToolOrder {
		if t, ok := toolMap[name]; ok {
			sb.WriteString("- **")
			sb.WriteString(name)
			sb.WriteString("** — ")
			sb.WriteString(firstLine(t.Description()))
			sb.WriteByte('\n')
			emitted[name] = true
		}
	}
	for _, name := range mgmtToolOrder {
		if t, ok := toolMap[name]; ok {
			sb.WriteString("- **")
			sb.WriteString(name)
			sb.WriteString("** — ")
			sb.WriteString(firstLine(t.Description()))
			sb.WriteByte('\n')
			emitted[name] = true
		}
	}

	// Remaining tools (external MCP etc.) in alphabetical order
	var extras []string
	for _, t := range tools {
		if !emitted[t.Name()] {
			extras = append(extras, t.Name())
		}
	}
	sort.Strings(extras)
	for _, name := range extras {
		t := toolMap[name]
		sb.WriteString("- **")
		sb.WriteString(name)
		sb.WriteString("** — ")
		sb.WriteString(firstLine(t.Description()))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// firstLine returns the first line of s (up to the first newline).
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// buildRuntimeLine generates a compact one-line runtime environment summary.
func buildRuntimeLine(state *AgentState) string {
	osName := state.OSName
	if osName == "" {
		osName = "unknown"
	}
	shellCmd := state.ShellCmd
	if shellCmd == "" {
		shellCmd = "unknown"
	}
	modelName := state.ModelName
	if modelName == "" {
		modelName = "unknown"
	}

	return fmt.Sprintf(
		"Runtime: os=%s | shell=%s | model=%s | ctx=%d | thinking=%s",
		osName, shellCmd, modelName,
		state.ContextWindowTokens,
		state.ThinkingMode,
	)
}

// ── MCP Intent Detection ──

// containsMCPKeywords checks if the problem text mentions MCP or custom tool creation.
// Uses two matching strategies:
//   - Exact substring: for compact terms like "mcp", "技能"
//   - Word-bag: for multi-word intents like "create"+"tool" (matches even with words in between)
//
// Design: "server" alone is too broad (matches "web server", "database server"),
// so it is omitted; "mcp" already covers all "mcp server" queries as a substring.
// "skill" alone is too broad (matches "coding skill", "what skills do you have").
// Prefers false positives over false negatives.
//
// Known limitation: strings.Contains(lower, "tool") will match "tooltip" etc.
// Under the "prefer false positives" principle this is acceptable, and the probability
// of "build"/"create"/"custom" co-occurring with "tooltip" is negligible.
func containsMCPKeywords(problem string) bool {
	lower := strings.ToLower(problem)

	// Layer 1: exact substring match (compact terms)
	exactKeywords := []string{
		"mcp",
		"技能",
		"自定义工具",
		"创建工具",
		"新建工具",
	}
	for _, kw := range exactKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	// Layer 2: word-bag match (all words must appear, order irrelevant)
	// Catches "build a tool", "create new tool", "custom data tool" etc.
	intentPhrases := [][]string{
		{"build", "tool"},
		{"create", "tool"},
		{"custom", "tool"},
	}
	for _, words := range intentPhrases {
		allFound := true
		for _, w := range words {
			if !strings.Contains(lower, w) {
				allFound = false
				break
			}
		}
		if allFound {
			return true
		}
	}

	return false
}
