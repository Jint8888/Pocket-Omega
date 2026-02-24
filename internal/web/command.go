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
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// CommandHandlerOptions configures the slash command handler.
type CommandHandlerOptions struct {
	Loader       *prompt.PromptLoader
	MCPReload    func() // nil = no MCP; /reload only reloads prompts
	Store        *session.Store
	LLMProvider  llm.LLMProvider // used by /compact for summary generation
	ToolRegistry *tool.Registry  // used by /stats for tool count
	ModelName    string          // used by /stats
	ThinkingMode string          // used by /stats
	ToolCallMode string          // used by /stats
}

// commandResult is the JSON response from a slash command.
type commandResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"` // optional frontend action (e.g. "clear_chat")
}

// commandFunc handles a single slash command.
type commandFunc func(ctx context.Context, args string, sessionID string) commandResult

// CommandHandler routes slash commands to handlers without involving the LLM.
type CommandHandler struct {
	loader       *prompt.PromptLoader
	mcpReload    func()
	store        *session.Store
	llmProvider  llm.LLMProvider
	toolRegistry *tool.Registry
	modelName    string
	thinkingMode string
	toolCallMode string
	commands     map[string]commandFunc
}

// NewCommandHandler creates a command handler with built-in commands.
func NewCommandHandler(opts CommandHandlerOptions) *CommandHandler {
	h := &CommandHandler{
		loader:       opts.Loader,
		mcpReload:    opts.MCPReload,
		store:        opts.Store,
		llmProvider:  opts.LLMProvider,
		toolRegistry: opts.ToolRegistry,
		modelName:    opts.ModelName,
		thinkingMode: opts.ThinkingMode,
		toolCallMode: opts.ToolCallMode,
	}
	h.commands = map[string]commandFunc{
		"reload":  h.cmdReload,
		"clear":   h.cmdClear,
		"help":    h.cmdHelp,
		"compact": h.cmdCompact,
		"stats":   h.cmdStats,
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
		json.NewEncoder(w).Encode(commandResult{OK: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	fn, ok := h.commands[req.Command]
	if !ok {
		json.NewEncoder(w).Encode(commandResult{
			OK:      false,
			Message: "未知命令 /" + req.Command + "，输入 /help 查看可用命令",
		})
		return
	}

	result := fn(r.Context(), req.Args, req.SessionID)
	json.NewEncoder(w).Encode(result)
}

// ── Built-in commands ──

func (h *CommandHandler) cmdReload(ctx context.Context, args, sessionID string) commandResult {
	if h.loader != nil {
		h.loader.Reload()
	}
	if h.mcpReload != nil {
		h.mcpReload()
	}
	log.Printf("[Command] /reload executed")
	return commandResult{OK: true, Message: "✅ 提示词和 MCP 配置已重载"}
}

func (h *CommandHandler) cmdClear(ctx context.Context, args, sessionID string) commandResult {
	if sessionID != "" && h.store != nil {
		h.store.Delete(sessionID)
	}
	log.Printf("[Command] /clear executed, session=%s", sessionID)
	return commandResult{OK: true, Message: "✅ 对话已清空", Action: "clear_chat"}
}

func (h *CommandHandler) cmdHelp(ctx context.Context, args, sessionID string) commandResult {
	return commandResult{
		OK: true,
		Message: "可用命令:\n" +
			"/reload — 重载提示词和 MCP 配置\n" +
			"/clear — 清空当前对话\n" +
			"/compact [N] — 压缩历史对话为摘要（保留最近 N 轮，默认 2）\n" +
			"/stats — 显示当前会话状态和系统信息\n" +
			"/help — 显示此帮助",
	}
}

func (h *CommandHandler) cmdStats(ctx context.Context, args, sessionID string) commandResult {
	var sb strings.Builder
	sb.WriteString("📊 当前会话状态\n")

	// Session info
	if sessionID != "" && h.store != nil {
		turns, summary := h.store.GetSessionContext(sessionID)
		sb.WriteString(fmt.Sprintf("• 会话轮次：%d 轮", len(turns)))
		if summary != "" {
			sb.WriteString(fmt.Sprintf("（摘要：有，约 %d 字符）", len([]rune(summary))))
		} else {
			sb.WriteString("（摘要：无）")
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("• 会话轮次：无活跃会话\n")
	}

	// Tool info
	if h.toolRegistry != nil {
		tools := h.toolRegistry.List()
		mcpCount := 0
		for _, t := range tools {
			if strings.HasPrefix(t.Name(), "mcp_") {
				mcpCount++
			}
		}
		sb.WriteString(fmt.Sprintf("• 已注册工具：%d 个", len(tools)))
		if mcpCount > 0 {
			sb.WriteString(fmt.Sprintf("（含 MCP: %d 个）", mcpCount))
		}
		sb.WriteString("\n")
	}

	// Model info
	if h.modelName != "" {
		sb.WriteString(fmt.Sprintf("• 模型：%s\n", h.modelName))
	}
	sb.WriteString(fmt.Sprintf("• 思维模式：%s | 工具调用：%s\n", h.thinkingMode, h.toolCallMode))

	return commandResult{OK: true, Message: sb.String()}
}

// defaultCompactKeepN is the number of recent turns to keep after compaction.
const defaultCompactKeepN = 2

func (h *CommandHandler) cmdCompact(ctx context.Context, args, sessionID string) commandResult {
	if sessionID == "" || h.store == nil {
		return commandResult{OK: false, Message: "❌ 无活跃会话"}
	}
	if h.llmProvider == nil {
		return commandResult{OK: false, Message: "❌ LLM 未配置，无法生成摘要"}
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
		return commandResult{OK: true, Message: "ℹ️ 对话轮次过少，无需压缩"}
	}

	// Use shared compact logic
	summary, err := buildCompactSummary(ctx, h.llmProvider, turns, existingSummary, keepN)
	if err != nil {
		log.Printf("[Command] /compact LLM error: %v", err)
		return commandResult{OK: false, Message: "❌ 摘要生成失败: " + err.Error()}
	}

	// Update session
	compacted := h.store.Compact(sessionID, summary, keepN)
	log.Printf("[Command] /compact executed, session=%s compacted=%d keepN=%d summary_len=%d",
		sessionID, compacted, keepN, len([]rune(summary)))

	return commandResult{
		OK: true,
		Message: fmt.Sprintf("✅ 已压缩 %d 轮对话为摘要（约 %d 字符）",
			compacted, len([]rune(summary))),
	}
}
