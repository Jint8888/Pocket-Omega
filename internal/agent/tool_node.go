package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ToolNodeImpl implements BaseNode[AgentState, ToolPrep, ToolExecResult].
// It reads LastDecision, executes the requested tool, and returns results.
type ToolNodeImpl struct {
	registry *tool.Registry
}

func NewToolNode(registry *tool.Registry) *ToolNodeImpl {
	return &ToolNodeImpl{registry: registry}
}

// Prep reads LastDecision, resolves the tool from state.ToolRegistry (per-request),
// and converts ToolParams (map[string]any) to json.RawMessage.
// Using state.ToolRegistry instead of n.registry ensures per-request tools
// (e.g. update_plan injected via Registry.WithExtra) are accessible.
func (n *ToolNodeImpl) Prep(state *AgentState) []ToolPrep {
	if state.LastDecision == nil {
		return nil
	}

	// Convert map[string]any → json.RawMessage
	argsJSON, err := json.Marshal(state.LastDecision.ToolParams)
	if err != nil {
		log.Printf("[ToolNode] Failed to marshal tool params: %v", err)
		argsJSON = []byte("{}")
	}

	// Resolve tool from per-request registry; fall back to build-time registry if nil.
	reg := state.ToolRegistry
	if reg == nil {
		reg = n.registry
	}
	resolved, _ := reg.Get(state.LastDecision.ToolName)

	return []ToolPrep{{
		ToolName:     state.LastDecision.ToolName,
		Args:         argsJSON,
		ToolCallID:   state.LastDecision.ToolCallID,
		ResolvedTool: resolved,
	}}
}

// Exec executes the pre-resolved tool carried in ToolPrep.
func (n *ToolNodeImpl) Exec(ctx context.Context, prep ToolPrep) (ToolExecResult, error) {
	if prep.ResolvedTool == nil {
		return ToolExecResult{
			ToolName:   prep.ToolName,
			Error:      fmt.Sprintf("工具 %q 未找到", prep.ToolName),
			ToolCallID: prep.ToolCallID,
		}, nil
	}

	result, err := prep.ResolvedTool.Execute(ctx, json.RawMessage(prep.Args))
	if err != nil {
		return ToolExecResult{
			ToolName:   prep.ToolName,
			Error:      fmt.Sprintf("执行失败: %v", err),
			ToolCallID: prep.ToolCallID,
		}, nil // Don't propagate as error; record the failure
	}

	return ToolExecResult{
		ToolName:   prep.ToolName,
		Output:     result.Output,
		Error:      result.Error,
		ToolCallID: prep.ToolCallID,
	}, nil
}

// ExecFallback returns an error result.
func (n *ToolNodeImpl) ExecFallback(err error) ToolExecResult {
	return ToolExecResult{
		Error: fmt.Sprintf("工具执行失败: %v", err),
	}
}

// Post records the tool result and routes back to DecideNode.
func (n *ToolNodeImpl) Post(state *AgentState, prep []ToolPrep, results ...ToolExecResult) core.Action {
	if len(results) == 0 || len(prep) == 0 {
		return core.ActionDefault
	}

	result := results[0]
	p := prep[0]

	// Merge output and error — preserve partial output when tools fail
	output := result.Output
	if result.Error != "" {
		if output != "" {
			output = fmt.Sprintf("%s\n\n错误: %s", output, result.Error)
		} else {
			output = fmt.Sprintf("错误: %s", result.Error)
		}
	}

	step := StepRecord{
		StepNumber: len(state.StepHistory) + 1,
		Type:       "tool",
		ToolName:   p.ToolName,
		Input:      string(p.Args),
		Output:     output,
		ToolCallID: p.ToolCallID,
		IsError:    result.Error != "",
	}
	state.StepHistory = append(state.StepHistory, step)

	if state.OnStepComplete != nil {
		state.OnStepComplete(step)
	}

	log.Printf("[ToolNode] Executed %s: %s", p.ToolName, truncate(output, 100))

	return core.ActionDefault // Back to DecideNode
}
