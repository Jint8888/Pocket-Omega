package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/walkthrough"
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
		ReadCache:    state.ReadCache,
	}}
}

// Exec executes the pre-resolved tool carried in ToolPrep.
func (n *ToolNodeImpl) Exec(ctx context.Context, prep ToolPrep) (ToolExecResult, error) {
	start := time.Now()

	if prep.ResolvedTool == nil {
		return ToolExecResult{
			ToolName:   prep.ToolName,
			Error:      fmt.Sprintf("工具 %q 未找到", prep.ToolName),
			ToolCallID: prep.ToolCallID,
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	// ReadCache: intercept duplicate calls for cacheable tools
	if prep.ReadCache != nil && isCacheable(prep.ToolName) {
		key := CacheKey(prep.ToolName, string(prep.Args))
		if cached, ok := prep.ReadCache.Get(key); ok {
			return ToolExecResult{
				ToolName:   prep.ToolName,
				Output:     fmt.Sprintf("⚠️ 此内容与步骤 %d 相同（已缓存），请直接复用之前的结果。\n\n%s", cached.StepNumber, cached.Output),
				ToolCallID: prep.ToolCallID,
				DurationMs: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	result, err := prep.ResolvedTool.Execute(ctx, json.RawMessage(prep.Args))
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return ToolExecResult{
			ToolName:   prep.ToolName,
			Error:      fmt.Sprintf("执行失败: %v", err),
			ToolCallID: prep.ToolCallID,
			DurationMs: elapsed,
		}, nil // Don't propagate as error; record the failure
	}

	return ToolExecResult{
		ToolName:   prep.ToolName,
		Output:     result.Output,
		Error:      result.Error,
		ToolCallID: prep.ToolCallID,
		DurationMs: elapsed,
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
		DurationMs: result.DurationMs,
	}
	state.StepHistory = append(state.StepHistory, step)

	// ReadCache: cache results for cacheable tools + invalidate on writes
	isCacheHit := false
	if state.ReadCache != nil {
		if isCacheable(p.ToolName) && result.Error == "" {
			key := CacheKey(p.ToolName, string(p.Args))
			// Check if this was a cache hit (output starts with ⚠️)
			if strings.HasPrefix(result.Output, "⚠️") {
				isCacheHit = true
			} else {
				// First call: cache the result with step number
				state.ReadCache.Put(key, ReadCacheEntry{
					StepNumber: step.StepNumber,
					Output:     result.Output,
				})
			}
		}
		if isWriteTool(p.ToolName) {
			path := extractParam(string(p.Args), "path")
			if path != "" {
				state.ReadCache.Invalidate(FileReadCacheKey(path))
			}
		}
	}

	// Auto-write walkthrough entry (skip for cache hits — avoids memo noise)
	if !isCacheHit && state.WalkthroughStore != nil && state.WalkthroughSID != "" {
		if summary := buildAutoSummary(p.ToolName, string(p.Args), output, result.Error != ""); summary != "" {
			state.WalkthroughStore.Append(state.WalkthroughSID, walkthrough.Entry{
				StepNumber: step.StepNumber,
				Source:     walkthrough.SourceAuto,
				Content:    summary,
			})
		}
	}

	if state.OnStepComplete != nil {
		state.OnStepComplete(step)
	}

	log.Printf("[ToolNode] Executed %s: %s", p.ToolName, truncate(output, 100))

	return core.ActionDefault // Back to DecideNode
}

// skipAutoSummaryTools are meta-tools whose execution is not worth recording.
// ⚠️ Update this list when adding new meta-tools.
var skipAutoSummaryTools = map[string]bool{
	"walkthrough": true,
	"update_plan": true,
}

// autoSummaryParamKeys maps tool names to the JSON key for the "key parameter".
// Built from baseToolKeyParams (tool_params.go) + summary-specific extras.
var autoSummaryParamKeys = mergeToolKeyParams(map[string]string{
	"web_search": "query",
	"web_reader": "url",
})

// buildAutoSummary creates a one-line summary for walkthrough auto-write.
// Format: tool_name("key_param"): first_line_of_output — max 150 chars.
// Returns "" for meta-tools or empty output.
func buildAutoSummary(toolName, argsJSON, output string, isError bool) string {
	if skipAutoSummaryTools[toolName] {
		return ""
	}

	// Extract key parameter
	keyParam := ""
	if paramKey, ok := autoSummaryParamKeys[toolName]; ok {
		var params map[string]interface{}
		if json.Unmarshal([]byte(argsJSON), &params) == nil {
			if v, ok := params[paramKey]; ok {
				keyParam = fmt.Sprintf("%v", v)
			}
		}
	}

	// Build summary
	var sb strings.Builder
	sb.WriteString(toolName)
	if keyParam != "" {
		// Truncate key param to 60 runes (UTF-8 safe)
		if runes := []rune(keyParam); len(runes) > 60 {
			keyParam = string(runes[:57]) + "..."
		}
		sb.WriteString(fmt.Sprintf("(%q)", keyParam))
	}
	sb.WriteString(": ")

	if isError {
		sb.WriteString("❌ 失败")
	} else {
		// First non-empty line of output
		firstLine := output
		if idx := strings.IndexByte(output, '\n'); idx >= 0 {
			firstLine = output[:idx]
		}
		firstLine = strings.TrimSpace(firstLine)
		if firstLine == "" {
			firstLine = "(无输出)"
		}
		sb.WriteString(firstLine)
	}

	result := sb.String()
	// Truncate to 150 chars
	runes := []rune(result)
	if len(runes) > 150 {
		result = string(runes[:147]) + "..."
	}
	return result
}
