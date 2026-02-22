package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/session"
)

// CommandHandlerOptions configures the slash command handler.
type CommandHandlerOptions struct {
	Loader      *prompt.PromptLoader
	MCPReload   func() // nil = no MCP; /reload only reloads prompts
	Store       *session.Store
	LLMProvider llm.LLMProvider // used by /compact for summary generation
}

// CommandResult is the JSON response from a slash command.
type CommandResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"` // optional frontend action (e.g. "clear_chat")
}

// CommandFunc handles a single slash command.
type CommandFunc func(ctx context.Context, args string, sessionID string) CommandResult

// CommandHandler routes slash commands to handlers without involving the LLM.
type CommandHandler struct {
	loader      *prompt.PromptLoader
	mcpReload   func()
	store       *session.Store
	llmProvider llm.LLMProvider
	commands    map[string]CommandFunc
}

// NewCommandHandler creates a command handler with built-in commands.
func NewCommandHandler(opts CommandHandlerOptions) *CommandHandler {
	h := &CommandHandler{
		loader:      opts.Loader,
		mcpReload:   opts.MCPReload,
		store:       opts.Store,
		llmProvider: opts.LLMProvider,
	}
	h.commands = map[string]CommandFunc{
		"reload":  h.cmdReload,
		"clear":   h.cmdClear,
		"help":    h.cmdHelp,
		"compact": h.cmdCompact,
	}
	return h
}

type commandRequest struct {
	Command   string `json:"command"`
	Args      string `json:"args"`
	SessionID string `json:"session_id"`
}

// HandleCommand is the HTTP handler for POST /api/command.
func (h *CommandHandler) HandleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	w.Header().Set("Content-Type", "application/json")

	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(CommandResult{OK: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	fn, ok := h.commands[req.Command]
	if !ok {
		json.NewEncoder(w).Encode(CommandResult{
			OK:      false,
			Message: "未知命令 /" + req.Command + "，输入 /help 查看可用命令",
		})
		return
	}

	result := fn(r.Context(), req.Args, req.SessionID)
	json.NewEncoder(w).Encode(result)
}

// ── Built-in commands ──

func (h *CommandHandler) cmdReload(ctx context.Context, args, sessionID string) CommandResult {
	if h.loader != nil {
		h.loader.Reload()
	}
	if h.mcpReload != nil {
		h.mcpReload()
	}
	log.Printf("[Command] /reload executed")
	return CommandResult{OK: true, Message: "✅ 提示词和 MCP 配置已重载"}
}

func (h *CommandHandler) cmdClear(ctx context.Context, args, sessionID string) CommandResult {
	if sessionID != "" && h.store != nil {
		h.store.Delete(sessionID)
	}
	log.Printf("[Command] /clear executed, session=%s", sessionID)
	return CommandResult{OK: true, Message: "✅ 对话已清空", Action: "clear_chat"}
}

func (h *CommandHandler) cmdHelp(ctx context.Context, args, sessionID string) CommandResult {
	return CommandResult{
		OK: true,
		Message: "可用命令:\n" +
			"/reload — 重载提示词和 MCP 配置\n" +
			"/clear — 清空当前对话\n" +
			"/compact [N] — 压缩历史对话为摘要（保留最近 N 轮，默认 2）\n" +
			"/help — 显示此帮助",
	}
}

// defaultCompactKeepN is the number of recent turns to keep after compaction.
const defaultCompactKeepN = 2

func (h *CommandHandler) cmdCompact(ctx context.Context, args, sessionID string) CommandResult {
	if sessionID == "" || h.store == nil {
		return CommandResult{OK: false, Message: "❌ 无活跃会话"}
	}
	if h.llmProvider == nil {
		return CommandResult{OK: false, Message: "❌ LLM 未配置，无法生成摘要"}
	}

	// Support /compact 3 to specify keepN
	keepN := defaultCompactKeepN
	if args != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(args)); err == nil && n >= 0 {
			keepN = n
		}
	}

	// Atomically fetch history + existing summary
	turns, existingSummary := h.store.GetSessionContext(sessionID)
	if len(turns) <= keepN {
		return CommandResult{OK: true, Message: "ℹ️ 对话轮次过少，无需压缩"}
	}

	// Use shared compact logic
	summary, err := buildCompactSummary(ctx, h.llmProvider, turns, existingSummary, keepN)
	if err != nil {
		log.Printf("[Command] /compact LLM error: %v", err)
		return CommandResult{OK: false, Message: "❌ 摘要生成失败: " + err.Error()}
	}

	// Update session
	compacted := h.store.Compact(sessionID, summary, keepN)
	log.Printf("[Command] /compact executed, session=%s compacted=%d keepN=%d summary_len=%d",
		sessionID, compacted, keepN, len([]rune(summary)))

	return CommandResult{
		OK: true,
		Message: fmt.Sprintf("✅ 已压缩 %d 轮对话为摘要（约 %d 字符）",
			compacted, len([]rune(summary))),
	}
}
