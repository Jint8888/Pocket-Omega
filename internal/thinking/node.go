package thinking

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"gopkg.in/yaml.v3"
)

// ChainOfThoughtNode implements core.BaseNode for multi-step reasoning.
// It produces a single PrepData, calls the LLM, parses YAML, and determines
// whether to continue or end the thinking chain.
type ChainOfThoughtNode struct {
	llmProvider llm.LLMProvider
}

// NewChainOfThoughtNode creates a node with the given LLM provider.
func NewChainOfThoughtNode(provider llm.LLMProvider) *ChainOfThoughtNode {
	return &ChainOfThoughtNode{llmProvider: provider}
}

// Prep reads the shared state and prepares context for the LLM call.
func (n *ChainOfThoughtNode) Prep(state *ThinkingState) []PrepData {
	state.CurrentThoughtNum++

	// Format previous thoughts
	thoughtsText := "No previous thoughts yet."
	var lastPlanSteps []PlanStep

	if len(state.Thoughts) > 0 {
		var parts []string
		for i, t := range state.Thoughts {
			block := fmt.Sprintf("Thought %d:\n  Thinking:\n    %s\n  Plan Status After Thought %d:\n%s",
				t.ThoughtNumber,
				strings.ReplaceAll(strings.TrimSpace(t.CurrentThinking), "\n", "\n    "),
				t.ThoughtNumber,
				FormatPlan(t.Planning, 2),
			)
			parts = append(parts, block)
			if i == len(state.Thoughts)-1 {
				lastPlanSteps = t.Planning
			}
		}
		thoughtsText = strings.Join(parts, "\n--------------------\n")
	} else {
		lastPlanSteps = []PlanStep{
			{Description: "理解问题", Status: "Pending"},
			{Description: "制定方案", Status: "Pending"},
			{Description: "结论", Status: "Pending"},
		}
	}

	lastPlanText := "# No previous plan available."
	if len(lastPlanSteps) > 0 {
		lastPlanText = FormatPlanForPrompt(lastPlanSteps, 0)
	}

	return []PrepData{{
		Problem:          state.Problem,
		ThoughtsText:     thoughtsText,
		LastPlanText:     lastPlanText,
		CurrentThoughtNo: state.CurrentThoughtNum,
		IsFirstThought:   len(state.Thoughts) == 0,
	}}
}

// Exec calls the LLM with the constructed prompt and parses the YAML response.
func (n *ChainOfThoughtNode) Exec(ctx context.Context, prep PrepData) (ThoughtData, error) {
	prompt := buildPrompt(prep)

	resp, err := n.llmProvider.CallLLM(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	})
	if err != nil {
		return ThoughtData{}, fmt.Errorf("LLM call failed: %w", err)
	}

	// Extract YAML from response
	yamlStr, err := ExtractYAML(resp.Content)
	if err != nil {
		return ThoughtData{}, fmt.Errorf("YAML extraction failed: %w", err)
	}

	var thought ThoughtData
	if err := yaml.Unmarshal([]byte(yamlStr), &thought); err != nil {
		return ThoughtData{}, fmt.Errorf("YAML parse failed: %w\nRaw YAML:\n%s", err, yamlStr)
	}

	// Validate required fields
	if thought.CurrentThinking == "" {
		return ThoughtData{}, fmt.Errorf("LLM response missing 'current_thinking'")
	}
	if thought.Planning == nil {
		return ThoughtData{}, fmt.Errorf("LLM response missing 'planning'")
	}

	thought.ThoughtNumber = prep.CurrentThoughtNo
	return thought, nil
}

// Post appends the thought to state and returns continue or end action.
// Includes a silent supervisor that validates solution quality invisibly.
func (n *ChainOfThoughtNode) Post(state *ThinkingState, prepRes []PrepData, execResults ...ThoughtData) core.Action {
	if len(execResults) == 0 {
		log.Println("[CoT] No exec results, ending")
		return core.ActionEnd
	}

	thought := execResults[0]
	state.Thoughts = append(state.Thoughts, thought)

	// ── Silent Supervisor: loop protection ──
	if len(state.Thoughts) >= MaxThoughts {
		log.Printf("[Supervisor] Max thoughts (%d) reached, forcing conclusion", MaxThoughts)
		state.Solution = extractBestSolution(state)
		// Still invoke callback so UI sees this final thought
		if state.OnThoughtComplete != nil {
			state.OnThoughtComplete(thought)
		}
		return core.ActionEnd
	}

	// Invoke the streaming callback if registered
	if state.OnThoughtComplete != nil {
		state.OnThoughtComplete(thought)
	}

	thinking := strings.TrimSpace(thought.CurrentThinking)
	planFormatted := FormatPlan(thought.Planning, 1)

	if !thought.NextThoughtNeeded {
		// Extract clean user-facing answer from Conclusion step's result
		if conclusionResult := extractConclusionResult(thought.Planning); conclusionResult != "" {
			state.Solution = conclusionResult
		} else {
			// Fallback: use CurrentThinking if no conclusion result found
			state.Solution = thinking
		}

		// ── Silent Supervisor: solution quality gate ──
		if reason := validateSolution(state.Solution); reason != "" {
			if state.supervisorRetryCount < MaxSupervisorRetries {
				state.supervisorRetryCount++
				log.Printf("[Supervisor] Solution rejected (retry %d/%d): %s",
					state.supervisorRetryCount, MaxSupervisorRetries, reason)
				// Silently force another thinking round — user never sees this
				state.Solution = ""
				return core.ActionContinue
			}
			// Max retries exhausted — force accept
			log.Printf("[Supervisor] Force accepted after %d retries: %s", MaxSupervisorRetries, reason)
		}

		fmt.Printf("\n💡 Thought %d (Conclusion):\n  %s\n\nFinal Plan:\n%s\n\n✅ SOLUTION:\n%s\n\n",
			thought.ThoughtNumber,
			strings.ReplaceAll(thinking, "\n", "\n  "),
			planFormatted,
			state.Solution,
		)
		return core.ActionEnd
	}

	fmt.Printf("\n🤔 Thought %d:\n  %s\n\nPlan:\n%s\n%s\n",
		thought.ThoughtNumber,
		strings.ReplaceAll(thinking, "\n", "\n  "),
		planFormatted,
		strings.Repeat("-", 50),
	)
	return core.ActionContinue
}

// validateSolution silently checks solution quality.
// Returns empty string if valid, or a reason string if rejected.
func validateSolution(solution string) string {
	s := strings.TrimSpace(solution)

	// Check 1: empty or too short
	if len([]rune(s)) < MinSolutionLength {
		return "solution too short"
	}

	// Check 2: refusal pattern detection (only on short responses)
	if len([]rune(s)) < 120 {
		for _, re := range rejectPatterns {
			if re.MatchString(s) {
				return "refusal response detected"
			}
		}
	}

	// Check 3: error marker detection
	lower := strings.ToLower(s)
	errorMarkers := []string{"[error]", "[错误]", "error:", "错误:", "failed:", "失败:"}
	for _, marker := range errorMarkers {
		if strings.HasPrefix(lower, marker) {
			return "error marker in solution"
		}
	}

	return ""
}

// extractBestSolution attempts to build a usable solution when forced to terminate.
func extractBestSolution(state *ThinkingState) string {
	// Try the latest thought's conclusion result
	if len(state.Thoughts) > 0 {
		last := state.Thoughts[len(state.Thoughts)-1]
		if result := extractConclusionResult(last.Planning); result != "" {
			return result
		}
		// Fallback to last thinking content
		return strings.TrimSpace(last.CurrentThinking)
	}
	return "抱歉，思考过程超时。请重新尝试。"
}

// extractConclusionResult recursively searches plan steps for the
// Conclusion step (supports both "Conclusion" and "结论") and returns its result field.
func extractConclusionResult(steps []PlanStep) string {
	for _, step := range steps {
		desc := strings.TrimSpace(step.Description)
		isConclusion := strings.EqualFold(desc, "Conclusion") || desc == "结论"
		if isConclusion && step.Result != "" {
			return strings.TrimSpace(step.Result)
		}
		if len(step.SubSteps) > 0 {
			if result := extractConclusionResult(step.SubSteps); result != "" {
				return result
			}
		}
	}
	return ""
}

// ExecFallback returns a default ThoughtData when all retries fail.
func (n *ChainOfThoughtNode) ExecFallback(err error) ThoughtData {
	log.Printf("[CoT] ExecFallback triggered: %v", err)
	return ThoughtData{
		CurrentThinking:   fmt.Sprintf("Error: %v", err),
		NextThoughtNeeded: false,
		Planning: []PlanStep{
			{Description: "结论", Status: "Done", Result: fmt.Sprintf("处理失败：%v", err)},
		},
	}
}

// ExtractYAML extracts YAML content from a ```yaml ... ``` code block.
func ExtractYAML(content string) (string, error) {
	// Try ```yaml ... ``` first
	if idx := strings.Index(content, "```yaml"); idx >= 0 {
		rest := content[idx+7:]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end]), nil
		}
	}
	// Try ``` ... ``` as fallback
	if idx := strings.Index(content, "```"); idx >= 0 {
		rest := content[idx+3:]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end]), nil
		}
	}
	// Try the whole content as YAML
	return strings.TrimSpace(content), nil
}

// BuildFlow creates the self-looping CoT flow.
func BuildFlow(provider llm.LLMProvider, maxRetries int) *core.Flow[ThinkingState] {
	cotNode := core.NewNode[ThinkingState, PrepData, ThoughtData](
		NewChainOfThoughtNode(provider),
		maxRetries,
	)
	// Self-loop: continue -> cotNode
	cotNode.AddSuccessor(cotNode, core.ActionContinue)

	return core.NewFlow[ThinkingState](cotNode)
}
