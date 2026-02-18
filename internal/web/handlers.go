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
	"github.com/pocketomega/pocket-omega/internal/thinking"
	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	maxRequestBody = 1 << 20         // 1MB max request body
	chatTimeout    = 5 * time.Minute // global timeout for chat flow
	agentTimeout   = 5 * time.Minute // global timeout for agent flow
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

// formatSolutionPrompt is the system prompt for the solution formatting step.
const formatSolutionPrompt = `你是一个答案整理助手。将推理结论整理为清晰、友好的最终回答。

## 风格指南
- 用 emoji 图标标注不同段落（如 💡🔍📝✅⚠️🎯📌），让内容更生动
- 步骤/方案用有序列表，要点用无序列表
- 重点关键词用 **加粗**
- 代码/命令用代码块
- 保持自然的对话语气，像一个专业但友善的助手
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

// formatSolution makes a lightweight LLM call to clean and organize
// a raw conclusion into a well-structured, user-facing answer.
// Shared by both ChatHandler and AgentHandler.
func formatSolution(ctx context.Context, provider llm.LLMProvider, problem, rawSolution string) (string, error) {
	userPrompt := fmt.Sprintf("用户问题：%s\n\n原始推理结论：\n%s\n\n请整理为最终答案：", problem, rawSolution)

	resp, err := provider.CallLLM(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: formatSolutionPrompt},
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

// ── Chat Handler ──

// ChatHandler handles chat requests and runs the CoT flow.
type ChatHandler struct {
	llmProvider llm.LLMProvider
	maxRetries  int
}

// NewChatHandler creates a new handler with the given LLM provider.
func NewChatHandler(provider llm.LLMProvider, maxRetries int) *ChatHandler {
	return &ChatHandler{
		llmProvider: provider,
		maxRetries:  maxRetries,
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

	log.Printf("[Chat] Received: %s", userMsg)

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
		Problem: userMsg,
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
		formatted, err := formatSolution(ctx, h.llmProvider, userMsg, solution)
		if err != nil {
			log.Printf("[Format] Formatting failed, using raw solution: %v", err)
		} else {
			solution = formatted
		}
	}

	sse.Send("done", sseDoneEvent{Solution: solution})
	log.Printf("[Chat] Done: %d thoughts, solution %d chars", len(state.Thoughts), len(solution))
}

// ── Agent Handler (Phase 2) ──

// AgentHandler handles agent requests with tool usage capability.
type AgentHandler struct {
	llmProvider  llm.LLMProvider
	agentFlow    core.Workflow[agent.AgentState]
	toolRegistry *tool.Registry
	workspaceDir string
	execLogger   *agent.ExecLogger
	thinkingMode string
}

// NewAgentHandler creates a new agent handler.
func NewAgentHandler(provider llm.LLMProvider, registry *tool.Registry, workspaceDir string, execLogger *agent.ExecLogger, thinkingMode string) *AgentHandler {
	return &AgentHandler{
		llmProvider:  provider,
		agentFlow:    agent.BuildAgentFlow(provider, registry, thinkingMode),
		toolRegistry: registry,
		workspaceDir: workspaceDir,
		execLogger:   execLogger,
		thinkingMode: thinkingMode,
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

	log.Printf("[Agent] Received: %s", userMsg)

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

	// Build agent state with SSE callback
	state := &agent.AgentState{
		Problem:      userMsg,
		WorkspaceDir: h.workspaceDir,
		ToolRegistry: h.toolRegistry,
		ThinkingMode: h.thinkingMode,
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
}
