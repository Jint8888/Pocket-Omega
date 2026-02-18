package agent

import (
	"context"
	"fmt"
	"log"
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
	toolsPrompt := state.ToolRegistry.GenerateToolsPrompt()

	return []DecidePrep{{
		Problem:      state.Problem,
		WorkspaceDir: state.WorkspaceDir,
		StepSummary:  stepSummary,
		ToolsPrompt:  toolsPrompt,
		StepCount:    len(state.StepHistory),
		ThinkingMode: state.ThinkingMode,
	}}
}

// Exec calls LLM to decide the next action.
func (n *DecideNode) Exec(ctx context.Context, prep DecidePrep) (Decision, error) {
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
		// If LLM returned natural language instead of YAML, treat it as a direct answer
		// rather than an error. This avoids wasting a step through ExecFallback.
		content := strings.TrimSpace(resp.Content)
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
	return Decision{
		Action: "answer",
		Reason: fmt.Sprintf("Decision failed: %v", err),
		Answer: fmt.Sprintf("抱歉，决策过程出错：%v", err),
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

搜索 + 阅读策略：
- 搜索获取概览后，用 web_reader 深入阅读最相关的页面
- web_reader 单次只读一个 URL，选择最关键的那个
- 如果用户直接给了 URL，优先用 web_reader 而非搜索`

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

// fixBackslashes replaces backslashes with forward slashes inside
// double-quoted YAML values to fix Windows path escape issues.
func fixBackslashes(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	inDoubleQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inDoubleQuote = !inDoubleQuote
			result.WriteByte(ch)
		} else if ch == '\\' && inDoubleQuote {
			// Replace backslash with forward slash inside double quotes
			result.WriteByte('/')
		} else {
			result.WriteByte(ch)
		}
	}
	return result.String()
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
