package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/prompt"
)

// ThinkNodeImpl implements BaseNode[AgentState, ThinkPrep, ThinkResult].
// It performs a single round of LLM reasoning on the accumulated context.
type ThinkNodeImpl struct {
	llmProvider llm.LLMProvider
	loader      *prompt.PromptLoader
}

func NewThinkNode(provider llm.LLMProvider, loader *prompt.PromptLoader) *ThinkNodeImpl {
	return &ThinkNodeImpl{llmProvider: provider, loader: loader}
}

// Prep builds the reasoning context from StepHistory.
func (n *ThinkNodeImpl) Prep(state *AgentState) []ThinkPrep {
	ctxText := buildThinkContext(state)
	return []ThinkPrep{{
		Problem: state.Problem,
		Context: ctxText,
	}}
}

// Exec calls LLM for reasoning.
func (n *ThinkNodeImpl) Exec(ctx context.Context, prep ThinkPrep) (ThinkResult, error) {
	userPrompt := fmt.Sprintf("用户问题：%s\n\n已有上下文：\n%s\n\n请分析以上信息并给出你的推理：", prep.Problem, prep.Context)

	resp, err := n.llmProvider.CallLLM(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: n.buildSystemPrompt()},
		{Role: llm.RoleUser, Content: userPrompt},
	})
	if err != nil {
		return ThinkResult{}, fmt.Errorf("think LLM call failed: %w", err)
	}

	return ThinkResult{Thinking: resp.Content}, nil
}

// ExecFallback returns an error result.
func (n *ThinkNodeImpl) ExecFallback(err error) ThinkResult {
	return ThinkResult{Thinking: fmt.Sprintf("推理失败: %v", err)}
}

// Post records the thinking result and routes back to DecideNode.
func (n *ThinkNodeImpl) Post(state *AgentState, prep []ThinkPrep, results ...ThinkResult) core.Action {
	if len(results) == 0 {
		return core.ActionDefault
	}

	result := results[0]

	step := StepRecord{
		StepNumber: len(state.StepHistory) + 1,
		Type:       "think",
		Output:     result.Thinking,
	}
	state.StepHistory = append(state.StepHistory, step)

	if state.OnStepComplete != nil {
		state.OnStepComplete(step)
	}

	log.Printf("[ThinkNode] Reasoning complete: %s", truncate(result.Thinking, 100))

	return core.ActionDefault // Back to DecideNode
}

// buildThinkContext summarizes step history for reasoning context.
func buildThinkContext(state *AgentState) string {
	var sb strings.Builder
	for _, s := range state.StepHistory {
		switch s.Type {
		case "tool":
			sb.WriteString(fmt.Sprintf("[工具 %s 结果]: %s\n", s.ToolName, truncate(s.Output, perStepOutputBudget(state.ContextWindowTokens, recentWindowSize))))
		case "think":
			sb.WriteString(fmt.Sprintf("[推理]: %s\n", s.Output))
		case "decide":
			// Skip decide steps in context
		}
	}

	// Also include LastDecision thinking if available
	if state.LastDecision != nil && state.LastDecision.Thinking != "" {
		sb.WriteString(fmt.Sprintf("[当前分析方向]: %s\n", state.LastDecision.Thinking))
	}

	return sb.String()
}

// buildSystemPrompt assembles the L2 think guide and optional L3 user background.
func (n *ThinkNodeImpl) buildSystemPrompt() string {
	// L2 think guide is the primary content.
	// Fall back to hardcoded default when no loader or file is absent.
	const thinkL1Default = "你是一个善于分析推理的助手。根据已有信息进行深度分析，给出清晰的推理过程。"

	if n.loader == nil {
		return thinkL1Default
	}

	var sb strings.Builder

	// L2 persona: agent identity (loaded first)
	if persona := n.loader.LoadSoul(); persona != "" {
		sb.WriteString(persona)
		sb.WriteString("\n\n")
	}

	guide := n.loader.Load("think_guide.md")
	if guide != "" {
		sb.WriteString(guide)
	} else {
		sb.WriteString(thinkL1Default)
	}

	// L3: user domain background improves reasoning quality
	if rules := n.loader.LoadUserRules(); rules != "" {
		sb.WriteString("\n\n## 用户背景\n")
		sb.WriteString(rules)
	}

	return sb.String()
}
