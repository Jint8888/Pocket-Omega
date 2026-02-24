package web

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/agent"
	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/plan"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/session"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/tool/builtin"
	"github.com/pocketomega/pocket-omega/internal/walkthrough"
)

const (
	maxRequestBody  = 1 << 20         // 1MB max request body
	maxMessageRunes = 8000            // max user message length in runes
	chatTimeout     = 5 * time.Minute // global timeout for chat flow
)

// agentTimeout is the global timeout for agent flow.
// Configurable via AGENT_TIMEOUT_MINUTES env var (default: 10, min: 1, max: 30).
var agentTimeout = loadAgentTimeout()

func loadAgentTimeout() time.Duration {
	const defaultMinutes = 10
	v := os.Getenv("AGENT_TIMEOUT_MINUTES")
	if v == "" {
		return time.Duration(defaultMinutes) * time.Minute
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 30 {
		log.Printf("[Config] WARNING: invalid AGENT_TIMEOUT_MINUTES=%q (must be 1-30), using default %d", v, defaultMinutes)
		return time.Duration(defaultMinutes) * time.Minute
	}
	return time.Duration(n) * time.Minute
}

// ── Agent Handler (Phase 2) ──

// AgentHandlerOptions groups all configuration for AgentHandler.
// Use this instead of positional parameters to keep NewAgentHandler maintainable
// as new options are added over time.
type AgentHandlerOptions struct {
	Provider            llm.LLMProvider
	Registry            *tool.Registry
	WorkspaceDir        string
	ExecLogger          *agent.ExecLogger
	ThinkingMode        string
	ToolCallMode        string
	ContextWindowTokens int
	Store               *session.Store
	Loader              *prompt.PromptLoader // optional — falls back to hardcoded defaults
	OSName              string               // e.g. "Windows" — for runtime info line
	ShellCmd            string               // e.g. "cmd.exe /c" — for runtime info line
	ModelName           string               // e.g. "gemini-2.5-pro" — for runtime info line
	PlanStore           *plan.PlanStore      // optional — enables update_plan tool
	MaxAgentTokens      int64                // 0 = disabled; CostGuard token budget
	MaxAgentDuration    time.Duration        // 0 = disabled; CostGuard time limit
	WalkthroughStore    *walkthrough.Store   // optional — enables walkthrough tool + auto-write
}

// AgentHandler handles agent requests with tool usage capability.
type AgentHandler struct {
	llmProvider         llm.LLMProvider
	agentFlow           core.Workflow[agent.AgentState]
	toolRegistry        *tool.Registry
	workspaceDir        string
	execLogger          *agent.ExecLogger
	thinkingMode        string
	toolCallMode        string
	contextWindowTokens int
	sessionStore        *session.Store
	loader              *prompt.PromptLoader
	osName              string
	shellCmd            string
	modelName           string
	planStore           *plan.PlanStore
	maxAgentTokens      int64
	maxAgentDuration    time.Duration
	walkthroughStore    *walkthrough.Store
}

// NewAgentHandler creates a new agent handler from AgentHandlerOptions.
func NewAgentHandler(opts AgentHandlerOptions) *AgentHandler {
	return &AgentHandler{
		llmProvider:         opts.Provider,
		agentFlow:           agent.BuildAgentFlow(opts.Provider, opts.Registry, opts.ThinkingMode, opts.Loader),
		toolRegistry:        opts.Registry,
		workspaceDir:        opts.WorkspaceDir,
		execLogger:          opts.ExecLogger,
		thinkingMode:        opts.ThinkingMode,
		toolCallMode:        opts.ToolCallMode,
		contextWindowTokens: opts.ContextWindowTokens,
		sessionStore:        opts.Store,
		loader:              opts.Loader,
		osName:              opts.OSName,
		shellCmd:            opts.ShellCmd,
		modelName:           opts.ModelName,
		planStore:           opts.PlanStore,
		maxAgentTokens:      opts.MaxAgentTokens,
		maxAgentDuration:    opts.MaxAgentDuration,
		walkthroughStore:    opts.WalkthroughStore,
	}
}

// HandleAgent processes agent requests using SSE streaming with tool calls.
func (h *AgentHandler) HandleAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

	userMsg := strings.TrimSpace(r.FormValue("message"))
	if userMsg == "" {
		http.Error(w, "Empty message", http.StatusBadRequest)
		return
	}
	if len([]rune(userMsg)) > maxMessageRunes {
		http.Error(w, "Message too long", http.StatusRequestEntityTooLarge)
		return
	}

	log.Printf("[Agent] Received: %s", userMsg)
	startTime := time.Now()

	// Session history lookup
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	var historyPrefix string
	if sessionID != "" && h.sessionStore != nil {
		turns, summary := h.sessionStore.GetSessionContext(sessionID)
		// allocate 30% of context window (in chars) to conversation history
		budget := h.contextWindowTokens * 2 * 30 / 100
		historyPrefix = session.ToProblemPrefix(turns, budget, summary)
	}

	sse := newSSEWriter(w, r)
	if sse == nil {
		return
	}

	// Global timeout for the entire agent flow
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()

	// Send immediate status so user sees instant feedback
	sse.Send("status", map[string]string{"message": "🤔 正在分析问题..."})

	// Start execution log session
	if h.execLogger != nil {
		h.execLogger.StartSession(userMsg)
	}

	// Per-request: create update_plan tool with session context + SSE callback.
	// Uses WithExtra to create a request-scoped registry copy — no mutation of global registry.
	reqRegistry := h.toolRegistry
	if h.planStore != nil {
		planTool := builtin.NewUpdatePlanTool(h.planStore, sessionID, func(steps []plan.PlanStep) {
			sse.Send(sseEventPlan, ssePlanEvent{Steps: steps})
		})
		reqRegistry = h.toolRegistry.WithExtra(planTool)
		// Clean up plan data after agent completes (synchronous — safe with current design).
		// If agent is ever moved to goroutine, move Delete to agent completion callback.
		defer h.planStore.Delete(sessionID)
	}

	// Walkthrough: same per-request lifecycle as PlanStore.
	// defer Delete ensures cleanup when request ends.
	if h.walkthroughStore != nil {
		wtTool := builtin.NewWalkthroughTool(h.walkthroughStore, sessionID)
		reqRegistry = reqRegistry.WithExtra(wtTool)
		defer h.walkthroughStore.Delete(sessionID)
	}

	// Build agent state with SSE callback
	state := &agent.AgentState{
		Problem:             userMsg,
		ConversationHistory: historyPrefix,
		WorkspaceDir:        h.workspaceDir,
		ToolRegistry:        reqRegistry,
		ThinkingMode:        h.thinkingMode,
		ToolCallMode:        h.toolCallMode,
		ContextWindowTokens: h.contextWindowTokens,
		OSName:              h.osName,
		ShellCmd:            h.shellCmd,
		ModelName:           h.modelName,
		WalkthroughStore:    h.walkthroughStore,
		WalkthroughSID:      sessionID,
		PlanStore:           h.planStore,
		PlanSID:             sessionID,
		ReadCache:           agent.NewReadCache(),
		OnStepComplete: func(step agent.StepRecord) {
			// Write to execution log
			if h.execLogger != nil {
				h.execLogger.LogStep(step)
			}
			switch step.Type {
			case "decide":
				sse.Send("step", step)
			case "tool":
				sse.Send("tool", step)
			case "think":
				sse.Send("step", step)
			}
		},
		OnStreamChunk: func(chunk string) {
			sse.Send("chunk", map[string]string{"text": chunk})
		},
		OnPlanUpdate: func(steps []plan.PlanStep) {
			sse.Send(sseEventPlan, ssePlanEvent{Steps: steps})
		},
	}

	// CostGuard: inject if configured
	if h.maxAgentTokens > 0 || h.maxAgentDuration > 0 {
		state.CostGuard = agent.NewCostGuard(h.maxAgentTokens, h.maxAgentDuration)
	}

	// ContextGuard: inject OnContextOverflow callback for auto-compact
	if sessionID != "" && h.sessionStore != nil && h.llmProvider != nil {
		sessID := sessionID // capture for closure
		state.OnContextOverflow = func(ctx context.Context) error {
			turns, existing := h.sessionStore.GetSessionContext(sessID)
			if len(turns) <= defaultCompactKeepN {
				return nil
			}
			summary, err := buildCompactSummary(ctx, h.llmProvider, turns, existing, defaultCompactKeepN)
			if err != nil {
				return err
			}
			h.sessionStore.Compact(sessID, summary, defaultCompactKeepN)
			log.Printf("[ContextGuard] Auto-compact done for session=%s", sessID)
			return nil
		}
	}

	// Run the agent flow with timeout context
	h.agentFlow.Run(ctx, state)

	// AnswerNode already synthesizes a polished answer with LLM.
	// Skip formatSolution here to avoid a redundant LLM round-trip
	// that adds 3-5s of latency with no visible benefit.
	solution := strings.TrimSpace(state.Solution)
	if solution == "" {
		solution = "抱歉，未能生成回答。请重试。"
	}

	// Build execution stats for done event
	stats := &agentStats{
		Steps:     len(state.StepHistory),
		ToolCalls: countToolSteps(state.StepHistory),
		ElapsedMs: time.Since(startTime).Milliseconds(),
	}
	if state.CostGuard != nil {
		stats.TokensUsed = state.CostGuard.UsedTokens()
	}

	sse.Send("done", sseDoneEvent{Solution: solution, Stats: stats})
	log.Printf("[Agent] Done: %d steps, solution %d chars", len(state.StepHistory), len(solution))

	// Write execution log summary
	if h.execLogger != nil {
		h.execLogger.EndSession(state)
	}

	// Persist this turn to session history
	if sessionID != "" && h.sessionStore != nil {
		h.sessionStore.AppendTurn(sessionID, session.Turn{
			UserMsg:   userMsg,
			Assistant: solution,
			IsAgent:   true,
		})
	}
}

// countToolSteps counts the number of tool execution steps in the history.
func countToolSteps(steps []agent.StepRecord) int {
	n := 0
	for _, s := range steps {
		if s.Type == "tool" {
			n++
		}
	}
	return n
}
