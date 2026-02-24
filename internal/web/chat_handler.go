package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/session"
	"github.com/pocketomega/pocket-omega/internal/thinking"
)

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
// Only used by ChatHandler (AgentHandler's AnswerNode already synthesizes).
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
