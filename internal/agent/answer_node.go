package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/prompt"
)

// directAnswerMaxRunes is the maximum rune length for answers that pass
// through without an extra LLM synthesis call.
const directAnswerMaxRunes = 500

// AnswerNodeImpl implements BaseNode[AgentState, AnswerPrep, AnswerResult].
// It generates the final answer from all accumulated context.
type AnswerNodeImpl struct {
	llmProvider llm.LLMProvider
	loader      *prompt.PromptLoader
}

func NewAnswerNode(provider llm.LLMProvider, loader *prompt.PromptLoader) *AnswerNodeImpl {
	return &AnswerNodeImpl{llmProvider: provider, loader: loader}
}

// Prep aggregates all step context for answer generation.
func (n *AnswerNodeImpl) Prep(state *AgentState) []AnswerPrep {
	fullContext := buildFullContext(state)
	hasTools := hasToolSteps(state)

	// Simple direct answer: no tools used, LLM gave a direct response
	// Pass it through cleanly without "[初步分析]" wrapper
	if state.LastDecision != nil && state.LastDecision.Answer != "" && !hasTools {
		return []AnswerPrep{{
			Problem:     state.Problem,
			FullContext: state.LastDecision.Answer,
			HasToolUse:  false,
			StreamChunk: state.OnStreamChunk,
		}}
	}

	// Tool-based answer: include draft answer as hint alongside full tool context
	if state.LastDecision != nil && state.LastDecision.Answer != "" {
		fullContext = fmt.Sprintf("[初步分析]:\n%s\n\n%s", state.LastDecision.Answer, fullContext)
	}

	return []AnswerPrep{{
		Problem:     state.Problem,
		FullContext: fullContext,
		HasToolUse:  hasTools,
		StreamChunk: state.OnStreamChunk,
	}}
}

// Exec calls LLM to synthesize the final answer.
func (n *AnswerNodeImpl) Exec(ctx context.Context, prep AnswerPrep) (AnswerResult, error) {
	// Short direct answers without tool use can skip the synthesis LLM call
	if utf8.RuneCountInString(prep.FullContext) < directAnswerMaxRunes && !prep.HasToolUse {
		return AnswerResult{Answer: prep.FullContext}, nil
	}

	userPrompt := fmt.Sprintf("用户问题：%s\n\n以下是收集到的信息和分析：\n%s\n\n请综合以上信息，给出简洁明了的最终回答：", prep.Problem, prep.FullContext)

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: n.buildSystemPrompt()},
		{Role: llm.RoleUser, Content: userPrompt},
	}

	// Use streaming when callback is available
	if prep.StreamChunk != nil {
		resp, err := n.llmProvider.CallLLMStream(ctx, msgs, llm.StreamCallback(prep.StreamChunk))
		if err != nil {
			return AnswerResult{}, fmt.Errorf("answer LLM stream call failed: %w", err)
		}
		return AnswerResult{Answer: resp.Content}, nil
	}

	// Fallback to synchronous call
	resp, err := n.llmProvider.CallLLM(ctx, msgs)
	if err != nil {
		return AnswerResult{}, fmt.Errorf("answer LLM call failed: %w", err)
	}

	return AnswerResult{Answer: resp.Content}, nil
}

// ExecFallback returns an error answer.
func (n *AnswerNodeImpl) ExecFallback(err error) AnswerResult {
	return AnswerResult{Answer: fmt.Sprintf("抱歉，生成答案时出错：%v", err)}
}

// Post writes the solution to AgentState and ends the flow.
func (n *AnswerNodeImpl) Post(state *AgentState, prep []AnswerPrep, results ...AnswerResult) core.Action {
	if len(results) > 0 {
		state.Solution = results[0].Answer
	}

	step := StepRecord{
		StepNumber: len(state.StepHistory) + 1,
		Type:       "answer",
		Output:     state.Solution,
	}
	state.StepHistory = append(state.StepHistory, step)

	if state.OnStepComplete != nil {
		state.OnStepComplete(step)
	}

	log.Printf("[AnswerNode] Final answer generated: %s", truncate(state.Solution, 100))

	return core.ActionEnd
}

// buildSystemPrompt assembles the answer L2 style rules and optional L3 user rules.
func (n *AnswerNodeImpl) buildSystemPrompt() string {
	const answerL1Default = "你是一个高效的助手。根据收集到的信息直接回答用户问题。\n根据已有信息直接作答，不要添加\"以下是答案\"之类的前缀。"

	if n.loader == nil {
		return answerL1Default
	}

	var sb strings.Builder

	// L2 persona: agent identity (loaded first to establish character)
	if persona := n.loader.LoadSoul(); persona != "" {
		sb.WriteString(persona)
		sb.WriteString("\n\n")
	} else {
		// Fallback identity when no persona file
		sb.WriteString("你是一个高效的助手。根据收集到的信息直接回答用户问题。\n\n")
	}

	// L2: answer style rules
	if style := n.loader.Load("answer_style.md"); style != "" {
		sb.WriteString(style)
	}

	// L3: user custom rules
	if rules := n.loader.LoadUserRules(); rules != "" {
		sb.WriteString("\n\n## 用户自定义规则\n")
		sb.WriteString(rules)
	}

	return sb.String()
}

// buildFullContext creates a comprehensive context from all steps.
func buildFullContext(state *AgentState) string {
	var sb strings.Builder
	for _, s := range state.StepHistory {
		switch s.Type {
		case "tool":
			sb.WriteString(fmt.Sprintf("[工具 %s 结果]:\n%s\n\n", s.ToolName, s.Output))
		case "think":
			sb.WriteString(fmt.Sprintf("[分析推理]:\n%s\n\n", s.Output))
		case "decide":
			// Only include tool-routing decisions, skip "answer" decisions
			// to avoid leaking internal reasoning into the final output
			if s.Input != "" && s.Action != "answer" {
				sb.WriteString(fmt.Sprintf("[决策 → %s]: %s\n", s.Action, s.Input))
			}
		}
	}
	return sb.String()
}
