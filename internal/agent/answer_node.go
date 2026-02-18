package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
)

// directAnswerMaxRunes is the maximum rune length for answers that pass
// through without an extra LLM synthesis call.
const directAnswerMaxRunes = 200

// AnswerNodeImpl implements BaseNode[AgentState, AnswerPrep, AnswerResult].
// It generates the final answer from all accumulated context.
type AnswerNodeImpl struct {
	llmProvider llm.LLMProvider
}

func NewAnswerNode(provider llm.LLMProvider) *AnswerNodeImpl {
	return &AnswerNodeImpl{llmProvider: provider}
}

// Prep aggregates all step context for answer generation.
func (n *AnswerNodeImpl) Prep(state *AgentState) []AnswerPrep {
	fullContext := buildFullContext(state)

	// If the last decision already has a draft answer, include it as a hint
	// but always provide the full tool context so the synthesis LLM can verify
	if state.LastDecision != nil && state.LastDecision.Answer != "" {
		fullContext = fmt.Sprintf("[初步分析]:\n%s\n\n%s", state.LastDecision.Answer, fullContext)
	}

	return []AnswerPrep{{
		Problem:     state.Problem,
		FullContext: fullContext,
		HasToolUse:  hasToolSteps(state),
		StreamChunk: state.OnStreamChunk,
	}}
}

// Exec calls LLM to synthesize the final answer.
func (n *AnswerNodeImpl) Exec(ctx context.Context, prep AnswerPrep) (AnswerResult, error) {
	// Short direct answers without tool use can skip the synthesis LLM call
	if utf8.RuneCountInString(prep.FullContext) < directAnswerMaxRunes && !prep.HasToolUse {
		return AnswerResult{Answer: prep.FullContext}, nil
	}

	prompt := fmt.Sprintf("用户问题：%s\n\n以下是收集到的信息和分析：\n%s\n\n请综合以上信息，给出简洁明了的最终回答：", prep.Problem, prep.FullContext)

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: `你是一个高效的助手。根据收集到的信息直接回答用户问题。
回答要简洁、准确、有用。用 emoji 标注段落（💡🔍📝✅⚠️），重点关键词用 **加粗**。
保持语言与用户一致。不要添加"以下是答案"之类的前缀，直接作答。`},
		{Role: llm.RoleUser, Content: prompt},
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
			// Include decision context
			if s.Input != "" {
				sb.WriteString(fmt.Sprintf("[决策 → %s]: %s\n", s.Action, s.Input))
			}
		}
	}
	return sb.String()
}
