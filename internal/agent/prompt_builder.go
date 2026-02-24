package agent

import (
	"fmt"
	"log"
	"strings"
)

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
		// think_guide.md — guides DecideNode on when to choose "think" action.
		// Only loaded in app mode where "think" is a valid action choice.
		// Native/FC modes handle thinking internally, loading this would confuse the LLM.
		if mode != "native" && mode != "fc" {
			if thinkGuide := n.loader.Load("think_guide.md"); thinkGuide != "" {
				sb.WriteString("\n\n")
				sb.WriteString(thinkGuide)
			}
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
2. answer — 直接回答用户问题

## 核心行为准则
- **禁止重复**：已完成步骤中出现过的相同工具+参数调用不再执行
- **先规划**：多步任务在首次回复中简述执行计划
- **及时结束**：任务完成后立即文本回复，不做多余验证
- **合并操作**：shell 命令可组合时优先组合执行`

const decideL1App = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你可以选择三种行动：
1. tool — 调用工具获取信息或执行操作
2. think — 进行深度推理分析
3. answer — 直接回答用户问题

## 核心行为准则
- **禁止重复**：已完成步骤中出现过的相同工具+参数调用不再执行
- **先规划**：多步任务在首次回复中简述执行计划
- **及时结束**：任务完成后立即文本回复，不做多余验证
- **合并操作**：shell 命令可组合时优先组合执行`

const decideL1FC = `你是一个智能助手，根据用户问题和当前上下文，决定下一步行动。

你有两种选择：
1. 调用工具 — 通过 function calling 调用合适的工具
2. 直接回答 — 如果已有足够信息或问题简单，直接用文本回复

## 核心行为准则
- **禁止重复**：已完成步骤中出现过的相同工具+参数调用不再执行
- **先规划**：多步任务在首次回复中简述执行计划
- **及时结束**：任务完成后立即文本回复，不做多余验证
- **合并操作**：shell 命令可组合时优先组合执行`

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

	if prep.WalkthroughText != "" {
		sb.WriteString(prep.WalkthroughText)
		sb.WriteString("\n")
	}

	if prep.PlanText != "" {
		sb.WriteString(prep.PlanText)
		sb.WriteString("\n")
	}

	if prep.StepSummary != "" {
		sb.WriteString(fmt.Sprintf("已完成步骤：\n%s\n\n", prep.StepSummary))
	}

	// When task is long, remind LLM of available tool names
	if prep.StepCount > 3 && len(prep.ToolDefinitions) > 0 {
		sb.WriteString("可用工具：")
		for i, td := range prep.ToolDefinitions {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(td.Name)
		}
		sb.WriteString("\n\n")
	}

	// Add urgency when step budget is running low
	remaining := MaxAgentSteps - prep.StepCount
	if remaining <= 5 && prep.StepCount > 0 {
		sb.WriteString(fmt.Sprintf("⚠️ 剩余步骤预算：%d。请尽快用已有信息给出回答。\n\n", remaining))
	}

	sb.WriteString("请通过工具调用或直接文本回复来响应。")

	// LoopDetector: inject warning into FC prompt
	if prep.LoopDetected.Detected {
		sb.WriteString(fmt.Sprintf(
			"\n\n⚠️ 检测到重复操作模式（%s）。请避免重复该操作，换一种方式继续推进任务。\n",
			prep.LoopDetected.Description,
		))
	}

	// ExplorationDetector: inject warning into FC prompt
	if prep.ExplorationDetected.Detected {
		sb.WriteString(fmt.Sprintf(
			"\n⚠️ 探索阶段超标（%s）。请立即用已收集的信息开始执行操作，不要继续读取文件。\n",
			prep.ExplorationDetected.Description,
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

	if prep.WalkthroughText != "" {
		sb.WriteString("\n")
		sb.WriteString(prep.WalkthroughText)
		sb.WriteString("\n")
	}

	if prep.PlanText != "" {
		sb.WriteString("\n")
		sb.WriteString(prep.PlanText)
		sb.WriteString("\n")
	}

	if prep.StepSummary != "" {
		sb.WriteString(fmt.Sprintf("已完成步骤：\n%s\n\n", prep.StepSummary))
	}

	// Add urgency when step budget is running low
	remaining := MaxAgentSteps - prep.StepCount
	if remaining <= 5 && prep.StepCount > 0 {
		sb.WriteString(fmt.Sprintf("⚠️ 剩余步骤预算：%d。请尽快用已有信息给出 answer。\n\n", remaining))
	}

	// LoopDetector: inject warning into YAML prompt
	if prep.LoopDetected.Detected {
		sb.WriteString(fmt.Sprintf(
			"⚠️ 检测到重复操作模式（%s）。请避免重复该操作，换一种方式继续推进任务。\n\n",
			prep.LoopDetected.Description,
		))
	}

	// ExplorationDetector: inject warning into YAML prompt
	if prep.ExplorationDetected.Detected {
		sb.WriteString(fmt.Sprintf(
			"⚠️ 探索阶段超标（%s）。请立即用已收集的信息开始执行操作，不要继续读取文件。\n\n",
			prep.ExplorationDetected.Description,
		))
	}

	// Dynamic YAML template based on thinking mode
	if prep.ThinkingMode == "native" {
		sb.WriteString(`请以 YAML 格式回复你的决策：
` + "```yaml" + `
action: "tool"  # 或 "answer"
reason: "本步具体做什么（不要重复之前说过的话）"
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
reason: "本步具体做什么（不要重复之前说过的话）"
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

// charsPerToken is the approximate character-to-token ratio for mixed Chinese/English.
// Chinese text averages ~1.5 chars/token; ASCII text averages ~4 chars/token.
// 2 is a conservative middle ground that avoids underestimating token cost.
const charsPerToken = 2
