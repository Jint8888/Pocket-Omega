package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/agent"
	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/plan"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/session"
	"github.com/pocketomega/pocket-omega/internal/thinking"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/tool/builtin"
)

const (
	maxRequestBody  = 1 << 20         // 1MB max request body
	maxMessageRunes = 8000            // max user message length in runes
	chatTimeout     = 5 * time.Minute // global timeout for chat flow
	agentTimeout    = 5 * time.Minute // global timeout for agent flow
)

// ── SSE Writer ──

// sseWriter wraps an http.ResponseWriter with SSE event writing and
// client disconnect detection. Shared by both Chat and Agent handlers.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
}

// newSSEWriter prepares SSE headers and returns a writer.
// Returns nil if streaming is not supported.
func newSSEWriter(w http.ResponseWriter, r *http.Request) *sseWriter {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	return &sseWriter{w: w, flusher: flusher, ctx: r.Context()}
}

// Send writes an SSE event. Returns false if the client has disconnected.
func (s *sseWriter) Send(event string, data interface{}) bool {
	select {
	case <-s.ctx.Done():
		return false
	default:
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[SSE] JSON marshal error: %v", err)
		return false
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, string(jsonBytes)); err != nil {
		log.Printf("[SSE] Write error (client disconnected?): %v", err)
		return false
	}
	s.flusher.Flush()
	return true
}

// ── Shared Solution Formatter ──

// formatSolutionPromptDefault is the fallback system prompt for the solution
// formatting step used when no loader is available or answer_style.md is absent.
const formatSolutionPromptDefault = `你是一个答案整理助手。将推理结论整理为清晰、友好的最终回答。

## 风格指南
- 步骤/方案用有序列表，要点用无序列表
- 重点关键词用 **加粗**
- 代码/命令用代码块
- 保持语言与用户一致（中文问用中文答）
- 不要添加"以下是答案"之类的前缀，直接作答
- 如果原始结论已足够好，直接保留不要过度修饰

## 示例

用户问题：一个房间里有3盏灯，房间外有3个开关。你只能进入房间一次。如何确定哪个开关控制哪盏灯？

整理后的答案：

💡 **核心思路：** 利用灯泡通电后的 **热惰性** 引入第三个判断维度。

📝 **操作步骤：**

1. **打开开关 1**，保持约 5 分钟，让灯泡充分发热
2. **关闭开关 1**，立即 **打开开关 2**
3. **进入房间**，观察并触摸灯泡

🔍 **判断方法：**

- 💡 **亮着的灯** → 开关 2 控制（当前通电）
- 🔥 **不亮但温热** → 开关 1 控制（刚断电，余温尚在）
- ❄️ **不亮且冰凉** → 开关 3 控制（从未通电）

✅ 关键在于利用灯泡的热惰性，将"只能进一次"的两态判断（亮/灭）扩展为三态判断（亮/暗热/暗冷）。`

// buildFormatPrompt assembles the system prompt for the solution formatting step.
// Uses answer_style.md from loader (L2+L3) when available.
func buildFormatPrompt(loader *prompt.PromptLoader) string {
	if loader == nil {
		return formatSolutionPromptDefault
	}

	style := loader.Load("answer_style.md")
	if style == "" {
		return formatSolutionPromptDefault
	}

	// L2 style + L3 user rules
	var sb strings.Builder
	sb.WriteString("你是一个答案整理助手。将推理结论整理为清晰、友好的最终回答。\n\n")
	sb.WriteString(style)
	if rules := loader.LoadUserRules(); rules != "" {
		sb.WriteString("\n\n## 用户自定义规则\n")
		sb.WriteString(rules)
	}
	return sb.String()
}

// formatSolution makes a lightweight LLM call to clean and organize
// a raw conclusion into a well-structured, user-facing answer.
// Shared by both ChatHandler and AgentHandler.
func formatSolution(ctx context.Context, provider llm.LLMProvider, loader *prompt.PromptLoader, problem, rawSolution string) (string, error) {
	userPrompt := fmt.Sprintf("用户问题：%s\n\n原始推理结论：\n%s\n\n请整理为最终答案：", problem, rawSolution)

	resp, err := provider.CallLLM(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: buildFormatPrompt(loader)},
		{Role: llm.RoleUser, Content: userPrompt},
	})
	if err != nil {
		return "", fmt.Errorf("format LLM call failed: %w", err)
	}

	formatted := strings.TrimSpace(resp.Content)
	if formatted == "" {
		return "", fmt.Errorf("format returned empty response")
	}

	log.Printf("[Format] Formatted solution: %d -> %d chars", len(rawSolution), len(formatted))
	return formatted, nil
}

// ── SSE Event Types ──

type sseThoughtEvent struct {
	ThoughtNumber   int    `json:"thought_number"`
	CurrentThinking string `json:"current_thinking"`
	PlanText        string `json:"plan_text,omitempty"`
}

type sseDoneEvent struct {
	Solution string `json:"solution"`
}

type sseErrorEvent struct {
	Error string `json:"error"`
}

const sseEventPlan = "plan"

type ssePlanEvent struct {
	Steps []plan.PlanStep `json:"steps"`
}

// ── Chat Handler ──

// ChatHandler handles chat requests and runs the CoT flow.
type ChatHandler struct {
	llmProvider         llm.LLMProvider
	maxRetries          int
	contextWindowTokens int
	sessionStore        *session.Store
	loader              *prompt.PromptLoader
}

// NewChatHandler creates a new handler with the given LLM provider.
// loader is optional (nil is valid) — falls back to hardcoded defaults.
func NewChatHandler(provider llm.LLMProvider, maxRetries int, contextWindowTokens int, store *session.Store, loader *prompt.PromptLoader) *ChatHandler {
	return &ChatHandler{
		llmProvider:         provider,
		maxRetries:          maxRetries,
		contextWindowTokens: contextWindowTokens,
		sessionStore:        store,
		loader:              loader,
	}
}

// HandleChat processes chat POST requests using SSE streaming.
func (h *ChatHandler) HandleChat(w http.ResponseWriter, r *http.Request) {
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

	log.Printf("[Chat] Received: %s", userMsg)

	// Session history lookup
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	var historyMsgs []llm.Message
	if sessionID != "" && h.sessionStore != nil {
		turns, summary := h.sessionStore.GetSessionContext(sessionID)
		// Allocate 50% of context window (in chars) to chat history.
		// More generous than Agent's 30% since Chat has no tool output overhead.
		// When contextWindowTokens is 0 (unknown), budget is 0 (no cap).
		budget := h.contextWindowTokens * 2 * 50 / 100
		historyMsgs = session.ToMessages(turns, budget, summary)
	}

	sse := newSSEWriter(w, r)
	if sse == nil {
		return
	}

	// Global timeout for the chat flow
	ctx, cancel := context.WithTimeout(r.Context(), chatTimeout)
	defer cancel()

	// Build and run the CoT flow with streaming callback
	flow := thinking.BuildFlow(h.llmProvider, h.maxRetries)
	state := &thinking.ThinkingState{
		Problem:             userMsg,
		ConversationHistory: historyMsgs,
		OnThoughtComplete: func(thought thinking.ThoughtData) {
			sse.Send("thought", sseThoughtEvent{
				ThoughtNumber:   thought.ThoughtNumber,
				CurrentThinking: strings.TrimSpace(thought.CurrentThinking),
				PlanText:        thinking.FormatPlan(thought.Planning, 0),
			})
		},
	}
	flow.Run(ctx, state)

	solution := strings.TrimSpace(state.Solution)
	if solution == "" {
		solution = "抱歉，未能生成回答。请重试。"
	} else {
		// ChatHandler uses ThinkingFlow which has no AnswerNode — the raw CoT
		// conclusion needs a formatting pass to produce a polished user-facing answer.
		// (AgentHandler skips this step because its AnswerNode already synthesizes
		// the final response with an LLM call, making a second pass redundant.)
		formatted, err := formatSolution(ctx, h.llmProvider, h.loader, userMsg, solution)
		if err != nil {
			log.Printf("[Format] Formatting failed, using raw solution: %v", err)
		} else {
			solution = formatted
		}
	}

	sse.Send("done", sseDoneEvent{Solution: solution})
	log.Printf("[Chat] Done: %d thoughts, solution %d chars", len(state.Thoughts), len(solution))

	// Persist this turn to session history
	if sessionID != "" && h.sessionStore != nil {
		h.sessionStore.AppendTurn(sessionID, session.Turn{
			UserMsg:   userMsg,
			Assistant: solution,
			IsAgent:   false,
		})
	}
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

	sse.Send("done", sseDoneEvent{Solution: solution})
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
