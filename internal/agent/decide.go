package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/prompt"
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

	// Proactive meta-tool suppression: if the last tool step was a meta-tool
	// that returned an error (e.g., dedup), suppress meta-tools immediately
	// so the LLM cannot repeat the same mistake on the very next round.
	// This eliminates the 1-step delay of the Post-based MetaToolGuard.
	if !state.SuppressMetaTools {
		if last := lastToolStep(state.StepHistory); last != nil {
			if metaTools[last.ToolName] && last.IsError {
				state.SuppressMetaTools = true
				log.Printf("[MetaToolGuard] Proactive suppress: last meta-tool %s returned error", last.ToolName)
			}
		}
	}

	// SuppressMetaTools: when the LLM is stuck calling meta-tools repeatedly,
	// physically remove them from the tool list so the LLM cannot select them.
	// This is the nuclear option — prompt-level interventions failed for weaker models.
	if state.SuppressMetaTools {
		toolDefs = filterOutMetaToolDefs(toolDefs)
		toolsPrompt = generateToolsPromptExcluding(state.ToolRegistry, metaTools)
		log.Printf("[MetaToolGuard] Meta-tools suppressed from tool list for this round")
	}

	// Phase 2: detect MCP intent for conditional guide loading
	hasMCPIntent := containsMCPKeywords(state.Problem)

	// CostGuard: check duration limit (Prep runs every step, ideal for time checks)
	if state.CostGuard != nil {
		if err := state.CostGuard.CheckDuration(); err != nil {
			log.Printf("[CostGuard] %v", err)
		}
	}

	prep := DecidePrep{
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
		ExplorationDetected: (&ExplorationDetector{}).Check(state.StepHistory, MaxAgentSteps),
		CostGuard:           state.CostGuard, // pointer shared for Exec to record tokens
	}

	// Read walkthrough memo for prompt injection
	if state.WalkthroughStore != nil && state.WalkthroughSID != "" {
		prep.WalkthroughText = state.WalkthroughStore.Render(state.WalkthroughSID)
	}

	// Read plan status for prompt injection
	if state.PlanStore != nil && state.PlanSID != "" {
		prep.PlanText = state.PlanStore.Render(state.PlanSID)
	}

	// MetaToolGuard redirect: consume the redirect message set by Post and
	// append it to PlanText so the LLM sees it alongside the plan status.
	// This is a one-shot injection — consumed immediately after reading.
	if state.MetaToolRedirectMsg != "" {
		prep.PlanText += "\n" + state.MetaToolRedirectMsg + "\n"
		state.MetaToolRedirectMsg = ""
	}

	// Estimate system prompt size for CostGuard + ContextGuard accuracy.
	// buildSystemPrompt needs the full prep, so we compute after construction.
	// Use the mode that will be used in Exec ("fc" for FC, thinkingMode for YAML).
	mode := state.ThinkingMode
	isFC := state.ToolCallMode == "fc" || (state.ToolCallMode == "auto" && n.llmProvider.IsToolCallingEnabled())
	if isFC {
		mode = "fc"
	}
	prep.SystemPromptEst = estimateTokens(n.buildSystemPrompt(mode, prep))

	// FC mode: tool definitions are sent as structured JSON alongside messages,
	// adding ~5-15% to actual token usage. Estimate from serialized form.
	if isFC && len(prep.ToolDefinitions) > 0 {
		if toolDefBytes, err := json.Marshal(prep.ToolDefinitions); err == nil {
			prep.SystemPromptEst += estimateTokens(string(toolDefBytes))
		}
	}

	return []DecidePrep{prep}
}

// Exec calls LLM to decide the next action.
// Routes to FC or YAML path based on ToolCallMode:
//   - "fc":   forced FC, failure returns error (no downgrade)
//   - "auto": detect capability, FC with auto-downgrade to YAML on failure
//   - "yaml": forced YAML (original behavior)
func (n *DecideNode) Exec(ctx context.Context, prep DecidePrep) (Decision, error) {
	var decision Decision
	var err error

	switch prep.ToolCallMode {
	case "fc":
		log.Printf("[Decide] Using FC path (forced)")
		decision, err = n.execWithFC(ctx, prep)

	case "auto":
		if n.llmProvider.IsToolCallingEnabled() {
			log.Printf("[Decide] Using FC path (auto-detected)")
			decision, err = n.execWithFC(ctx, prep)
			if err != nil {
				log.Printf("[Decide] FC path failed, auto-downgrade to YAML: %v", err)
				decision, err = n.execWithYAML(ctx, prep)
			}
		} else {
			log.Printf("[Decide] Model does not support FC, using YAML path")
			decision, err = n.execWithYAML(ctx, prep)
		}

	default: // explicit "yaml" or any unrecognised value
		if prep.ToolCallMode != "yaml" {
			log.Printf("[Decide] WARNING: unrecognised ToolCallMode %q, falling back to YAML", prep.ToolCallMode)
		}
		log.Printf("[Decide] Using YAML path")
		decision, err = n.execWithYAML(ctx, prep)
	}

	if err != nil {
		return decision, err
	}

	// CostGuard: estimate and record tokens (input + output)
	if prep.CostGuard != nil {
		// Input estimate includes system prompt (computed in Prep) + step context
		inputEst := prep.SystemPromptEst +
			estimateTokens(prep.StepSummary+prep.ToolsPrompt+prep.ConversationHistory)
		outputEst := estimateTokens(decision.Answer + decision.Thinking + decision.Reason)
		if recErr := prep.CostGuard.RecordTokens(inputEst + outputEst); recErr != nil {
			log.Printf("[CostGuard] %v", recErr)
		}
	}

	// ContextGuard: check context window usage including system prompt estimate
	if prep.ContextWindowTokens > 0 {
		guard := NewContextGuard(prep.ContextWindowTokens)
		// Include SystemPromptEst to avoid underestimating by ~20-25%
		contentTokens := prep.SystemPromptEst +
			estimateTokens(prep.StepSummary+prep.ToolsPrompt+prep.ConversationHistory+
				prep.Problem+prep.ToolingSummary+prep.WalkthroughText+prep.PlanText)
		switch guard.CheckTokens(contentTokens) {
		case ContextWarning:
			log.Printf("[ContextGuard] Context at ~70%%, consider /compact")
		case ContextCritical:
			log.Printf("[ContextGuard] Context at ~85%%, scheduling auto-compact")
			decision.ContextStatus = ContextCritical
		}
	}

	return decision, nil
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

		// Extract reasoning from Content if model provided it alongside tool calls
		reason := strings.TrimSpace(resp.Content)
		if reason == "" {
			reason = fmt.Sprintf("FC: call %s", tc.Name)
		} else {
			reason = truncate(reason, 200)
		}

		return Decision{
			Action:     "tool",
			Reason:     reason,
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

	// Plan sideband: extract plan status update piggybacked on Decision.
	// YAML mode: PlanStep/PlanStatus are auto-parsed from yaml tags.
	// FC mode: parse [plan:step_id:status] marker from reason text.
	if state.PlanStore != nil && state.PlanSID != "" {
		planStep, planStatus := decision.PlanStep, decision.PlanStatus
		if planStep == "" && decision.Reason != "" {
			planStep, planStatus = parsePlanSideband(decision.Reason)
		}
		if planStep != "" && planStatus != "" {
			state.PlanStore.Update(state.PlanSID, planStep, planStatus, "")
			log.Printf("[PlanSideband] %s → %s", planStep, planStatus)
			if state.OnPlanUpdate != nil {
				state.OnPlanUpdate(state.PlanStore.Get(state.PlanSID))
			}
		}
	}

	// Force termination if too many steps
	if len(state.StepHistory) >= MaxAgentSteps {
		log.Printf("[Decide] Max steps reached (%d), forcing answer", MaxAgentSteps)
		return core.ActionAnswer
	}

	// CostGuard: force answer if budget/duration exceeded (highest priority)
	if state.CostGuard != nil && state.CostGuard.IsExceeded() {
		log.Printf("[CostGuard] Budget/duration exceeded, forcing answer")
		return core.ActionAnswer
	}

	// ContextGuard: transfer Decision.ContextStatus → state.pendingCompact,
	// then execute auto-compact if needed (non-fatal on failure).
	if decision.ContextStatus == ContextCritical {
		state.pendingCompact = true
	}
	if state.pendingCompact && state.OnContextOverflow != nil {
		state.pendingCompact = false
		compactCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := state.OnContextOverflow(compactCtx); err != nil {
			log.Printf("[ContextGuard] Auto-compact failed: %v", err)
		}
	}

	switch decision.Action {
	case "tool":
		// Meta-tool consecutive guard: three-tier intervention.
		// Tier 1 (soft, ≥2): inject redirect message + suppress meta-tools from next tool list.
		// Tier 2 (hard, ≥4): force answer to prevent infinite loop.
		// Non-meta-tool call: clear suppression (LLM broke out of the loop).
		if metaTools[decision.ToolName] {
			consecMeta := countTrailingMetaTools(state.StepHistory)
			if consecMeta >= 4 {
				log.Printf("[MetaToolGuard] Hard limit: %d consecutive meta-tool calls (%s), forcing answer",
					consecMeta, decision.ToolName)
				return core.ActionAnswer
			}
			if consecMeta >= 2 {
				log.Printf("[MetaToolGuard] Soft redirect + suppress: %d consecutive meta-tool calls (%s)",
					consecMeta, decision.ToolName)
				state.SuppressMetaTools = true
				state.MetaToolRedirectMsg = "[SYSTEM] ⚠️ 你已连续多次调用 update_plan，但 update_plan 只是状态标记工具，不会执行任何实际操作。" +
					"请立即调用实际工具来执行当前步骤，例如: file_read, file_write, file_list, shell_exec, web_search, mcp_server_add。"
			}
		} else {
			// Non-meta-tool: LLM broke out of the loop, restore meta-tools
			if state.SuppressMetaTools {
				log.Printf("[MetaToolGuard] LLM called non-meta tool %s, restoring meta-tools", decision.ToolName)
				state.SuppressMetaTools = false
			}
		}

		// LoopDetector: soft intervention first, hard override on streak ≥ 2
		if len(prep) > 0 && prep[0].LoopDetected.Detected {
			// Self-correction check: if LLM switched to a different tool than
			// the one that triggered the loop, treat it as self-corrected.
			loopTool := prep[0].LoopDetected.ToolName
			if loopTool != "" && decision.ToolName != loopTool {
				log.Printf("[LoopDetector] Self-corrected: %s → %s, resetting streak",
					loopTool, decision.ToolName)
				state.LoopDetectionStreak = 0
			} else {
				state.LoopDetectionStreak++
				if state.LoopDetectionStreak >= 2 {
					log.Printf("[LoopDetector] Hard override (streak=%d): tool → answer (%s)",
						state.LoopDetectionStreak, prep[0].LoopDetected.Rule)
					return core.ActionAnswer
				}
				// First detection: warning already injected in Prep, let LLM self-correct
				log.Printf("[LoopDetector] Soft warning (streak=1), allowing tool call")
			}
		} else {
			state.LoopDetectionStreak = 0 // reset on clean step
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

// ── Plan sideband ──

// planSidebandRe matches [plan:step_id:status] markers in FC mode reason text.
var planSidebandRe = regexp.MustCompile(`\[plan:(\w+):(in_progress|done)\]`)

// parsePlanSideband extracts plan step and status from a reason string.
// Returns ("", "") if no sideband marker is found.
func parsePlanSideband(reason string) (step, status string) {
	m := planSidebandRe.FindStringSubmatch(reason)
	if len(m) == 3 {
		return m[1], m[2]
	}
	return "", ""
}
