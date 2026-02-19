package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/thinking"
	"gopkg.in/yaml.v3"
)

// DecideNode implements BaseNode[AgentState, DecidePrep, Decision].
// It acts as the central router in the ReAct loop.
type DecideNode struct {
	llmProvider llm.LLMProvider
}

func NewDecideNode(provider llm.LLMProvider) *DecideNode {
	return &DecideNode{llmProvider: provider}
}

// Prep reads the current AgentState and builds context for LLM decision.
func (n *DecideNode) Prep(state *AgentState) []DecidePrep {
	stepSummary := buildStepSummary(state.StepHistory)

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

	return []DecidePrep{{
		Problem:         state.Problem,
		WorkspaceDir:    state.WorkspaceDir,
		StepSummary:     stepSummary,
		ToolsPrompt:     toolsPrompt,
		ToolDefinitions: toolDefs,
		StepCount:       len(state.StepHistory),
		ThinkingMode:    state.ThinkingMode,
		ToolCallMode:    state.ToolCallMode,
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

	default: // "yaml" or unknown
		log.Printf("[Decide] Using YAML path")
		return n.execWithYAML(ctx, prep)
	}
}

// execWithFC uses Function Calling to get structured tool calls from the model.
func (n *DecideNode) execWithFC(ctx context.Context, prep DecidePrep) (Decision, error) {
	prompt := buildDecidePromptFC(prep)

	resp, err := n.llmProvider.CallLLMWithTools(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: decideSystemPromptFC},
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

	// Model returned text → treat as direct answer
	if content := strings.TrimSpace(resp.Content); len(content) > 0 {
		return Decision{Action: "answer", Answer: content}, nil
	}

	// Empty response — neither tool calls nor content
	return Decision{}, fmt.Errorf("FC returned empty response (no tool_calls, no content)")
}

// execWithYAML uses the original YAML text parsing to extract decisions.
func (n *DecideNode) execWithYAML(ctx context.Context, prep DecidePrep) (Decision, error) {
	prompt := buildDecidePrompt(prep)

	resp, err := n.llmProvider.CallLLM(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: getDecideSystemPrompt(prep.ThinkingMode)},
		{Role: llm.RoleUser, Content: prompt},
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

// getDecideSystemPrompt returns the appropriate system prompt based on thinking mode.
func getDecideSystemPrompt(thinkingMode string) string {
	if thinkingMode == "native" {
		return decideSystemPromptNative
	}
	return decideSystemPromptApp
}

const decideSystemPromptNative = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你可以选择两种行动：
1. tool — 调用工具获取信息或执行操作
2. answer — 直接回答用户问题

决策原则：
- 如果问题需要实时信息（时间、文件内容等），使用 tool
- 如果已有足够信息或问题简单，直接 answer
- 每个工具最多调用 2 次。禁止用不同关键词反复搜索同一个话题
- 搜索结果不完美也没关系，用已有信息综合回答即可
- 尽可能高效，尽早给出答案

答案格式：
- 回答时用 emoji 标注段落（💡🔍📝✅⚠️），重点关键词用 **加粗**
- 保持语言与用户一致，不要添加“以下是答案”之类的前缀，直接作答

搜索 + 阅读策略：
- 搜索获取概览后，用 web_reader 深入阅读最相关的页面
- web_reader 单次只读一个 URL，选择最关键的那个
- 如果用户直接给了 URL，优先用 web_reader 而非搜索`

const decideSystemPromptApp = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你可以选择三种行动：
1. tool — 调用工具获取信息或执行操作
2. think — 进行深度推理分析
3. answer — 直接回答用户问题

决策原则：
- 如果问题需要实时信息（时间、文件内容等），使用 tool
- 如果问题需要深度分析或多步推理，使用 think
- 如果已有足够信息或问题简单，直接 answer
- 每个工具最多调用 2 次。禁止用不同关键词反复搜索同一个话题
- 搜索结果不完美也没关系，用已有信息综合回答即可
- 尽可能高效，尽早给出答案

答案格式：
- 回答时用 emoji 标注段落（💡🔍📝✅⚠️），重点关键词用 **加粗**
- 保持语言与用户一致，不要添加“以下是答案”之类的前缀，直接作答

搜索 + 阅读策略：
- 搜索获取概览后，用 web_reader 深入阅读最相关的页面
- web_reader 单次只读一个 URL，选择最关键的那个
- 如果用户直接给了 URL，优先用 web_reader 而非搜索`

// FC 专用 system prompt: 无 YAML 格式要求，无 think action
const decideSystemPromptFC = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你有两种选择：
1. 调用工具 — 通过 function calling 调用合适的工具
2. 直接回答 — 如果已有足够信息或问题简单，直接用文本回复

决策原则：
- 需要实时信息时，调用工具
- 已有足够信息时，直接回答
- 每个工具最多调用 2 次，尽可能高效

搜索 + 阅读策略：
- 搜索获取概览后，用 web_reader 深入阅读最相关的页面
- web_reader 单次只读一个 URL，选择最关键的那个
- 如果用户直接给了 URL，优先用 web_reader 而非搜索

答案格式：
- 回答时用 emoji 标注段落（💡🔍📝✅⚠️），重点关键词用 **加粗**
- 保持语言与用户一致，不要添加“以下是答案”之类的前缀，直接作答`

// buildDecidePromptFC builds the user prompt for FC mode (no YAML template).
func buildDecidePromptFC(prep DecidePrep) string {
	var sb strings.Builder

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

	return sb.String()
}

func buildDecidePrompt(prep DecidePrep) string {
	var sb strings.Builder

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

	// Dynamic YAML template based on thinking mode
	if prep.ThinkingMode == "native" {
		sb.WriteString(`请以 YAML 格式回复你的决策：
` + "```yaml" + `
action: "tool"  # 或 "answer"
reason: "决策理由"
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

func buildStepSummary(steps []StepRecord) string {
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
				// Recent tool step — keep full output
				sb.WriteString(fmt.Sprintf("  步骤 %d [工具 %s]: %s\n", s.StepNumber, s.ToolName, truncate(s.Output, 8000)))
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
