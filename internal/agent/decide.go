package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/thinking"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"gopkg.in/yaml.v3"
)

// DecideNode implements BaseNode[AgentState, DecidePrep, Decision].
// It acts as the central router in the ReAct loop.
type DecideNode struct {
	llmProvider llm.LLMProvider
	loader      *prompt.PromptLoader
}

func NewDecideNode(provider llm.LLMProvider, loader *prompt.PromptLoader) *DecideNode {
	return &DecideNode{llmProvider: provider, loader: loader}
}

// Prep reads the current AgentState and builds context for LLM decision.
func (n *DecideNode) Prep(state *AgentState) []DecidePrep {
	stepSummary := buildStepSummary(state.StepHistory, state.ContextWindowTokens)

	// Only compute what's needed for the selected tool-call mode.
	var toolsPrompt string
	var toolDefs []llm.ToolDefinition
	switch state.ToolCallMode {
	case "fc":
		toolDefs = state.ToolRegistry.GenerateToolDefinitions()
	case "yaml":
		toolsPrompt = state.ToolRegistry.GenerateToolsPrompt()
	default: // "auto" — might need either
		toolsPrompt = state.ToolRegistry.GenerateToolsPrompt()
		toolDefs = state.ToolRegistry.GenerateToolDefinitions()
	}

	// Phase 1: compute tool summary and runtime line at Prep time
	toolingSummary := buildToolingSection(state.ToolRegistry)
	runtimeLine := buildRuntimeLine(state)

	// Phase 2: detect MCP intent for conditional guide loading
	hasMCPIntent := containsMCPKeywords(state.Problem)

	return []DecidePrep{{
		Problem:             state.Problem,
		WorkspaceDir:        state.WorkspaceDir,
		StepSummary:         stepSummary,
		ToolsPrompt:         toolsPrompt,
		ToolDefinitions:     toolDefs,
		StepCount:           len(state.StepHistory),
		ThinkingMode:        state.ThinkingMode,
		ToolCallMode:        state.ToolCallMode,
		ConversationHistory: state.ConversationHistory,
		ToolingSummary:      toolingSummary,
		RuntimeLine:         runtimeLine,
		HasMCPIntent:        hasMCPIntent,
		ContextWindowTokens: state.ContextWindowTokens,
		LoopDetected:        (&LoopDetector{}).Check(state.StepHistory),
	}}
}

// Exec calls LLM to decide the next action.
// Routes to FC or YAML path based on ToolCallMode:
//   - "fc":   forced FC, failure returns error (no downgrade)
//   - "auto": detect capability, FC with auto-downgrade to YAML on failure
//   - "yaml": forced YAML (original behavior)
func (n *DecideNode) Exec(ctx context.Context, prep DecidePrep) (Decision, error) {
	switch prep.ToolCallMode {
	case "fc":
		// Forced FC mode — no fallback
		log.Printf("[Decide] Using FC path (forced)")
		return n.execWithFC(ctx, prep)

	case "auto":
		// Auto mode — use FC if supported, with YAML fallback
		if n.llmProvider.IsToolCallingEnabled() {
			log.Printf("[Decide] Using FC path (auto-detected)")
			decision, err := n.execWithFC(ctx, prep)
			if err != nil {
				log.Printf("[Decide] FC path failed, auto-downgrade to YAML: %v", err)
				return n.execWithYAML(ctx, prep)
			}
			return decision, nil
		}
		log.Printf("[Decide] Model does not support FC, using YAML path")
		return n.execWithYAML(ctx, prep)

	default: // explicit "yaml" or any unrecognised value
		if prep.ToolCallMode != "yaml" {
			log.Printf("[Decide] WARNING: unrecognised ToolCallMode %q, falling back to YAML", prep.ToolCallMode)
		}
		log.Printf("[Decide] Using YAML path")
		return n.execWithYAML(ctx, prep)
	}
}

// execWithFC uses Function Calling to get structured tool calls from the model.
func (n *DecideNode) execWithFC(ctx context.Context, prep DecidePrep) (Decision, error) {
	prompt := buildDecidePromptFC(prep)

	resp, err := n.llmProvider.CallLLMWithTools(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: n.buildSystemPrompt("fc", prep)},
		{Role: llm.RoleUser, Content: prompt},
	}, prep.ToolDefinitions)
	if err != nil {
		return Decision{}, fmt.Errorf("FC call failed: %w", err)
	}

	// Model returned tool calls → extract as Decision
	if len(resp.ToolCalls) > 0 {
		tc := resp.ToolCalls[0] // Use first tool call
		if len(resp.ToolCalls) > 1 {
			log.Printf("[Decide] WARNING: FC returned %d tool calls, only first executed (parallel FC not yet supported)", len(resp.ToolCalls))
		}
		// Validate tool name against known definitions (cheap, before JSON parse)
		if len(prep.ToolDefinitions) > 0 {
			found := false
			for _, td := range prep.ToolDefinitions {
				if td.Name == tc.Name {
					found = true
					break
				}
			}
			if !found {
				return Decision{}, fmt.Errorf("FC returned unknown tool %q (not in %d registered tools)", tc.Name, len(prep.ToolDefinitions))
			}
		}

		var params map[string]any
		if err := json.Unmarshal(tc.Arguments, &params); err != nil {
			return Decision{}, fmt.Errorf("invalid tool params from FC: %w", err)
		}

		return Decision{
			Action:     "tool",
			Reason:     fmt.Sprintf("FC: call %s", tc.Name),
			ToolName:   tc.Name,
			ToolParams: params,
			ToolCallID: tc.ID,
		}, nil
	}

	// Model returned text — check for native FC token format before treating as answer.
	// Some models (e.g. Kimi-K2.5) embed tool calls in Content using special tokens
	// instead of the standard tool_calls field, so we parse them here.
	if content := strings.TrimSpace(resp.Content); len(content) > 0 {
		if strings.Contains(content, "<|tool_calls_section_begin|>") {
			if decision, ok := parseNativeFCContent(content, prep.ToolDefinitions); ok {
				log.Printf("[Decide] Parsed native FC tokens → action=tool name=%s", decision.ToolName)
				return decision, nil
			}
			// Native tokens present but unparseable — trigger auto-downgrade to YAML
			return Decision{}, fmt.Errorf("FC returned unparseable native token format")
		}
		return Decision{Action: "answer", Answer: content}, nil
	}

	// Empty response — neither tool calls nor content
	return Decision{}, fmt.Errorf("FC returned empty response (no tool_calls, no content)")
}

// execWithYAML uses the original YAML text parsing to extract decisions.
func (n *DecideNode) execWithYAML(ctx context.Context, prep DecidePrep) (Decision, error) {
	userPrompt := buildDecidePrompt(prep)

	resp, err := n.llmProvider.CallLLM(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: n.buildSystemPrompt(prep.ThinkingMode, prep)},
		{Role: llm.RoleUser, Content: userPrompt},
	})
	if err != nil {
		return Decision{}, fmt.Errorf("decide LLM call failed: %w", err)
	}

	decision, err := parseDecision(resp.Content)
	if err != nil {
		content := strings.TrimSpace(resp.Content)

		// Model returned native FC tokens (e.g. K2.5's <|tool_calls_section_begin|>)
		// Strip the FC tokens and use the natural language portion as answer
		if strings.Contains(content, "<|tool_calls_section_begin|>") {
			parts := strings.SplitN(content, "<|tool_calls_section_begin|>", 2)
			cleaned := strings.TrimSpace(parts[0])
			if len(cleaned) > 0 {
				log.Printf("[Decide] Stripped native FC tokens, using text as answer: %s", truncate(cleaned, 80))
				return Decision{Action: "answer", Answer: cleaned}, nil
			}
			log.Printf("[Decide] Native FC tokens with no text content, falling back")
			return Decision{}, fmt.Errorf("parse decision failed: model returned native FC tokens without text")
		}

		// If LLM returned natural language instead of YAML, treat it as a direct answer
		if len(content) > 0 && !strings.HasPrefix(content, "```") {
			log.Printf("[Decide] YAML parse failed, treating as direct answer: %s", truncate(content, 80))
			return Decision{Action: "answer", Answer: content}, nil
		}
		return Decision{}, fmt.Errorf("parse decision failed: %w", err)
	}

	return decision, nil
}

// Post writes the decision to state and routes to the next node.
func (n *DecideNode) Post(state *AgentState, prep []DecidePrep, results ...Decision) core.Action {
	if len(results) == 0 {
		return core.ActionAnswer // Fallback
	}

	decision := results[0]

	// Write transient field for downstream nodes
	state.LastDecision = &decision

	// Record step
	step := StepRecord{
		StepNumber: len(state.StepHistory) + 1,
		Type:       "decide",
		Action:     decision.Action,
		Input:      decision.Reason,
	}
	state.StepHistory = append(state.StepHistory, step)

	if state.OnStepComplete != nil {
		state.OnStepComplete(step)
	}

	log.Printf("[Decide] Step %d: action=%s reason=%s", step.StepNumber, decision.Action, decision.Reason)

	// Force termination if too many steps
	if len(state.StepHistory) >= MaxAgentSteps {
		log.Printf("[Decide] Max steps reached (%d), forcing answer", MaxAgentSteps)
		return core.ActionAnswer
	}

	switch decision.Action {
	case "tool":
		// LoopDetector hard override: if loop detected and LLM still chose tool, force answer
		if len(prep) > 0 && prep[0].LoopDetected.Detected {
			log.Printf("[LoopDetector] Hard override: tool → answer (%s)", prep[0].LoopDetected.Rule)
			return core.ActionAnswer
		}
		return core.ActionTool
	case "think":
		// In native mode, model handles thinking internally.
		// If LLM still returns "think", force it to answer instead.
		if state.ThinkingMode == "native" {
			log.Printf("[Decide] Native mode: converting stray 'think' to 'answer'")
			return core.ActionAnswer
		}
		return core.ActionThink
	case "answer":
		return core.ActionAnswer
	default:
		log.Printf("[Decide] Unknown action %q, defaulting to answer", decision.Action)
		return core.ActionAnswer
	}
}

// ExecFallback returns a safe decision on failure.
func (n *DecideNode) ExecFallback(err error) Decision {
	log.Printf("[Decide] ExecFallback triggered: %v", err)
	return Decision{
		Action: "answer",
		Reason: fmt.Sprintf("Decision failed: %v", err),
		Answer: "抱歉，处理过程中遇到问题，请稍后重试。",
	}
}

// ── Prompt construction ──

// buildSystemPrompt assembles the three-layer system prompt:
//   - L1: hardcoded tool-call protocol and constraints (varies by mode)
//   - L2: project behaviour rules from prompts/*.md (decision principles, answer style)
//   - L3: user custom rules from rules.md (language, domain, style preferences)
//
// mode is one of "fc", "native", or anything else (app mode).
func (n *DecideNode) buildSystemPrompt(mode string, prep DecidePrep) string {
	var sb strings.Builder

	// #1 Soul: agent identity (loaded first to establish character)
	if n.loader != nil {
		if persona := n.loader.LoadSoul(); persona != "" {
			sb.WriteString(persona)
			sb.WriteString("\n\n")
		}
	}

	// #2 User Rules: placed early for high LLM attention (above L1 protocol)
	if n.loader != nil {
		if rules := n.loader.LoadUserRules(); rules != "" {
			sb.WriteString("## 用户自定义规则\n")
			sb.WriteString(rules)
			sb.WriteString("\n\n")
		}
	}

	// #3 L1: hardcoded tool-call protocol (cannot be overridden)
	sb.WriteString(decideL1Constraint(mode))

	// #4 Runtime Info: compact single line (Phase 1)
	if prep.RuntimeLine != "" {
		sb.WriteString("\n\n")
		sb.WriteString(prep.RuntimeLine)
	}

	// #5 Tooling Section: auto-generated tool summary (Phase 1)
	if prep.ToolingSummary != "" {
		sb.WriteString("\n\n")
		sb.WriteString(prep.ToolingSummary)
	}

	// #6 Knowledge Dictionary + L2 behaviour rules
	if n.loader != nil {
		if knowledge := n.loader.Load("knowledge.md"); knowledge != "" {
			sb.WriteString("\n\n")
			sb.WriteString(knowledge)
		}
	}

	// #7 Behavior Components
	if n.loader != nil {
		if common := n.loader.Load("decide_common.md"); common != "" {
			sb.WriteString("\n\n")
			sb.WriteString(common)
		}
		if style := n.loader.Load("answer_style.md"); style != "" {
			sb.WriteString("\n\n")
			sb.WriteString(style)
		}
		if ruleGuide := n.loader.Load("rule_guide.md"); ruleGuide != "" {
			sb.WriteString("\n\n")
			sb.WriteString(ruleGuide)
		}
		// think_guide.md — guides DecideNode on when to choose "think" action
		if thinkGuide := n.loader.Load("think_guide.md"); thinkGuide != "" {
			sb.WriteString("\n\n")
			sb.WriteString(thinkGuide)
		}
		// Phase 2: MCP/skill creation guides — conditionally loaded based on Intent detection.
		// Only loaded when user's Problem mentions MCP/skill/custom-tool keywords.
		if prep.HasMCPIntent {
			if mcpGuide := n.loader.Load("mcp_server_guide.md"); mcpGuide != "" {
				sb.WriteString("\n\n")
				sb.WriteString(mcpGuide)
			}
			if skillDocGuide := n.loader.Load("skill_doc_guide.md"); skillDocGuide != "" {
				sb.WriteString("\n\n")
				sb.WriteString(skillDocGuide)
			}
		}
	}

	result := sb.String()

	// Phase 2: Token Budget Guard — temporary character truncation.
	// If context window is known, cap system prompt at 25% of total token budget.
	// This is a safety net; Phase 3 will replace with component-level removal.
	//
	// Rune-safe: use []rune slicing to avoid cutting in the middle of a
	// multi-byte UTF-8 character (e.g. Chinese text is 3 bytes/char).
	if prep.ContextWindowTokens > 0 {
		maxChars := prep.ContextWindowTokens * charsPerToken * 25 / 100
		runes := []rune(result)
		if len(runes) > maxChars {
			log.Printf("[Decide] Token budget guard: system prompt %d chars exceeds %d limit, truncating", len(runes), maxChars)
			result = string(runes[:maxChars])
		}
	}

	return result
}

// decideL1Constraint returns the hardcoded L1 system prompt fragment for DecideNode.
// These constraints define the tool-call protocol and cannot be overridden by L2/L3.
func decideL1Constraint(mode string) string {
	switch mode {
	case "fc":
		return decideL1FC
	case "native":
		return decideL1Native
	default: // "app" mode (extended thinking)
		return decideL1App
	}
}

// L1 constraints — hardcoded, not file-overridable.
// Only the tool-call protocol and action set differ between modes;
// decision strategy and answer format are intentionally kept in L2 files.

const decideL1Native = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你可以选择两种行动：
1. tool — 调用工具获取信息或执行操作
2. answer — 直接回答用户问题`

const decideL1App = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你可以选择三种行动：
1. tool — 调用工具获取信息或执行操作
2. think — 进行深度推理分析
3. answer — 直接回答用户问题`

const decideL1FC = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你有两种选择：
1. 调用工具 — 通过 function calling 调用合适的工具
2. 直接回答 — 如果已有足够信息或问题简单，直接用文本回复`

// buildDecidePromptFC builds the user prompt for FC mode (no YAML template).
func buildDecidePromptFC(prep DecidePrep) string {
	var sb strings.Builder

	if prep.ConversationHistory != "" {
		sb.WriteString(prep.ConversationHistory)
		sb.WriteString("\n[当前问题]\n")
	}
	sb.WriteString(fmt.Sprintf("用户问题：%s\n\n", prep.Problem))
	if prep.WorkspaceDir != "" {
		sb.WriteString(fmt.Sprintf("当前工作目录：%s\n文件工具的路径相对于此目录。用 \".\" 表示当前目录。\n\n", prep.WorkspaceDir))
	}

	if prep.StepSummary != "" {
		sb.WriteString(fmt.Sprintf("已完成步骤：\n%s\n\n", prep.StepSummary))
	}

	// Add urgency when step budget is running low
	remaining := MaxAgentSteps - prep.StepCount
	if remaining <= 3 && prep.StepCount > 0 {
		sb.WriteString(fmt.Sprintf("⚠️ 剩余步骤预算：%d。请尽快用已有信息给出回答。\n\n", remaining))
	}

	sb.WriteString("请通过工具调用或直接文本回复来响应。")

	// LoopDetector: inject warning into FC prompt
	if prep.LoopDetected.Detected {
		sb.WriteString(fmt.Sprintf(
			"\n\n⚠️ 检测到重复操作模式（%s）。请直接用文本回复，不要调用工具。\n",
			prep.LoopDetected.Description,
		))
	}

	return sb.String()
}

func buildDecidePrompt(prep DecidePrep) string {
	var sb strings.Builder

	if prep.ConversationHistory != "" {
		sb.WriteString(prep.ConversationHistory)
		sb.WriteString("\n[当前问题]\n")
	}
	sb.WriteString(fmt.Sprintf("用户问题：%s\n\n", prep.Problem))
	if prep.WorkspaceDir != "" {
		sb.WriteString(fmt.Sprintf("当前工作目录：%s\n文件工具的路径相对于此目录。用 \".\" 表示当前目录。\n\n", prep.WorkspaceDir))
	}
	sb.WriteString(prep.ToolsPrompt)
	sb.WriteString("\n")

	if prep.StepSummary != "" {
		sb.WriteString(fmt.Sprintf("已完成步骤：\n%s\n\n", prep.StepSummary))
	}

	// Add urgency when step budget is running low
	remaining := MaxAgentSteps - prep.StepCount
	if remaining <= 3 && prep.StepCount > 0 {
		sb.WriteString(fmt.Sprintf("⚠️ 剩余步骤预算：%d。请尽快用已有信息给出 answer。\n\n", remaining))
	}

	// LoopDetector: inject warning into YAML prompt
	if prep.LoopDetected.Detected {
		sb.WriteString(fmt.Sprintf(
			"⚠️ 检测到重复操作模式（%s）。请立即用已有信息给出 answer，不要再调用工具。\n\n",
			prep.LoopDetected.Description,
		))
	}

	// Dynamic YAML template based on thinking mode
	if prep.ThinkingMode == "native" {
		sb.WriteString(`请以 YAML 格式回复你的决策：
` + "```yaml" + `
action: "tool"  # 或 "answer"
reason: "决策理由"
headline: "正在执行..."  # 可选：用户可见的一句话操作摘要
tool_name: "工具名"       # action=tool 时必需
tool_params:              # action=tool 时必需
  param1: "value1"
answer: |                 # action=answer 时
  最终回答...
` + "```")
	} else {
		sb.WriteString(`请以 YAML 格式回复你的决策：
` + "```yaml" + `
action: "tool"  # 或 "think" 或 "answer"
reason: "决策理由"
headline: "正在执行..."  # 可选：用户可见的一句话操作摘要
tool_name: "工具名"       # action=tool 时必需
tool_params:              # action=tool 时必需
  param1: "value1"
thinking: |               # action=think 时
  推理内容...
answer: |                 # action=answer 时
  最终回答...
` + "```")
	}

	return sb.String()
}

// recentWindowSize is the number of recent tool steps to keep with full output.
// Older tool steps are compressed to a one-line metadata summary.
const recentWindowSize = 3

// charsPerToken is the approximate character-to-token ratio for mixed Chinese/English.
// Chinese text averages ~1.5 chars/token; ASCII text averages ~4 chars/token.
// 2 is a conservative middle ground that avoids underestimating token cost.
const charsPerToken = 2

// perStepOutputBudget computes the max characters per recent tool step in the decision
// prompt. Allocates toolOutputBudgetPct% of the context window to tool outputs and
// divides evenly across recentWindowSize steps.
// Falls back to 8000 when contextWindowTokens is 0 (unconfigured), preserving
// existing behaviour.
func perStepOutputBudget(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		return 8000 // backward-compatible default
	}
	const toolOutputBudgetPct = 40 // percent of context window reserved for tool outputs
	budget := contextWindowTokens * charsPerToken * toolOutputBudgetPct / 100 / recentWindowSize
	if budget < 1000 {
		budget = 1000 // floor: keep outputs useful even on tiny context windows
	}
	return budget
}

func buildStepSummary(steps []StepRecord, contextWindowTokens int) string {
	if len(steps) == 0 {
		return ""
	}

	// Count tool steps to determine the sliding window boundary
	toolCount := 0
	for _, s := range steps {
		if s.Type == "tool" {
			toolCount++
		}
	}
	// Tool steps with index >= this threshold get full output
	fullOutputThreshold := toolCount - recentWindowSize

	var sb strings.Builder
	toolIdx := 0
	for _, s := range steps {
		switch s.Type {
		case "decide":
			sb.WriteString(fmt.Sprintf("  步骤 %d [决策]: %s → %s\n", s.StepNumber, s.Action, s.Input))
		case "tool":
			if toolIdx >= fullOutputThreshold {
				// Recent tool step — keep full output within model-aware budget
				sb.WriteString(fmt.Sprintf("  步骤 %d [工具 %s]: %s\n", s.StepNumber, s.ToolName, truncate(s.Output, perStepOutputBudget(contextWindowTokens))))
			} else {
				// Old tool step — one-line summary
				sb.WriteString(fmt.Sprintf("  步骤 %d [工具 %s]: 已执行 (%s)，输出 %d bytes\n", s.StepNumber, s.ToolName, truncate(s.Input, 80), len(s.Output)))
			}
			toolIdx++
		case "think":
			sb.WriteString(fmt.Sprintf("  步骤 %d [推理]: %s\n", s.StepNumber, truncate(s.Output, 200)))
		case "answer":
			sb.WriteString(fmt.Sprintf("  步骤 %d [回答]: %s\n", s.StepNumber, truncate(s.Output, 200)))
		}
	}
	return sb.String()
}

func parseDecision(raw string) (Decision, error) {
	yamlStr, err := thinking.ExtractYAML(raw)
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

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

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

	return Decision{
		Action:     "tool",
		Reason:     fmt.Sprintf("native FC: call %s", tc.Name),
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

// containsMCPKeywords checks if the problem text mentions MCP or custom tool creation.
// Used for Phase 2 intent-based conditional loading of MCP/skill guides.
// Design: "server" alone is too broad (matches "web server", "database server"),
// so it is omitted; "mcp" already covers all "mcp server" queries as a substring.
// Prefers false positives over false negatives.
func containsMCPKeywords(problem string) bool {
	lower := strings.ToLower(problem)
	keywords := []string{"mcp", "技能", "skill", "自定义工具", "custom tool"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
