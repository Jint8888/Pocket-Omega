package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pocketomega/pocket-omega/internal/agent"
	"github.com/pocketomega/pocket-omega/internal/llm/openai"
	"github.com/pocketomega/pocket-omega/internal/mcp"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/session"
	"github.com/pocketomega/pocket-omega/internal/skill"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/tool/builtin"
	"github.com/pocketomega/pocket-omega/internal/web"
	"github.com/pocketomega/pocket-omega/pkg/config"
)

func main() {
	// Load .env file
	config.LoadEnv()

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       Pocket-Omega v0.2              ║")
	fmt.Println("║   CoT + Tools · Go + HTMX            ║")
	fmt.Println("╚══════════════════════════════════════╝")

	// Initialize LLM client
	llmClient, err := openai.NewClientFromEnv()
	if err != nil {
		log.Fatalf("❌ Failed to initialize LLM client: %v", err)
	}

	model := os.Getenv("LLM_MODEL")
	baseURL := os.Getenv("LLM_BASE_URL")
	fmt.Printf("🤖 LLM: %s @ %s\n", model, baseURL)

	// Initialize tool registry with built-in tools
	registry := tool.NewRegistry()
	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir, _ = os.Getwd()
	}
	// Validate workspace directory exists
	if info, err := os.Stat(workspaceDir); err != nil || !info.IsDir() {
		log.Fatalf("❌ WORKSPACE_DIR %q does not exist or is not a directory", workspaceDir)
	}
	fmt.Printf("📂 Workspace: %s\n", workspaceDir)

	shellEnabled := os.Getenv("TOOL_SHELL_ENABLED") != "false"
	registry.Register(builtin.NewShellTool(workspaceDir, shellEnabled))
	registry.Register(builtin.NewFileReadTool(workspaceDir))
	registry.Register(builtin.NewFileWriteTool(workspaceDir))
	registry.Register(builtin.NewFileListTool(workspaceDir))
	registry.Register(builtin.NewFileFindTool(workspaceDir))
	registry.Register(builtin.NewTimeTool())
	registry.Register(builtin.NewWebReaderTool())

	// P1 — core file operations (unconditional)
	registry.Register(builtin.NewFileGrepTool(workspaceDir))
	registry.Register(builtin.NewFileMoveTool(workspaceDir))
	registry.Register(builtin.NewFileOpenTool(workspaceDir))

	// P2 — extended file operations (unconditional)
	registry.Register(builtin.NewFileDeleteTool(workspaceDir))
	registry.Register(builtin.NewFilePatchTool(workspaceDir))

	// P2 — HTTP request tool (enabled by default, disable via TOOL_HTTP_ENABLED=false)
	if os.Getenv("TOOL_HTTP_ENABLED") != "false" {
		allowInternal := os.Getenv("TOOL_HTTP_ALLOW_INTERNAL") == "true"
		registry.Register(builtin.NewHTTPRequestTool(allowInternal))
		if allowInternal {
			fmt.Println("🌐 HTTP request tool enabled (internal addresses allowed)")
		} else {
			fmt.Println("🌐 HTTP request tool enabled")
		}
	}

	// Conditional search tools — auto-enable when API key is configured
	if key := os.Getenv("TAVILY_API_KEY"); key != "" {
		registry.Register(builtin.NewTavilySearchTool(key))
		fmt.Println("🔍 Tavily web search enabled")
	}
	if key := os.Getenv("BRAVE_API_KEY"); key != "" {
		registry.Register(builtin.NewBraveSearchTool(key))
		fmt.Println("🔍 Brave search enabled")
	}

	if err := registry.InitAll(context.Background()); err != nil {
		log.Fatalf("❌ Failed to initialize tools: %v", err)
	}
	defer registry.CloseAll()

	// Load workspace skills from <workspaceDir>/skills/
	skillMgr := skill.NewManager(workspaceDir)
	if n, skillErrs := skillMgr.LoadAll(context.Background(), registry); n > 0 || len(skillErrs) > 0 {
		fmt.Printf("🧩 Workspace skills: %d loaded\n", n)
		for _, e := range skillErrs {
			log.Printf("⚠️  Skill load: %v", e)
		}
	}
	// skill_reload is always available so the agent can hot-reload skills
	// even when mcp.json is absent.
	registry.Register(skill.NewReloadTool(skillMgr, registry))

	fmt.Printf("🛠️  Tools: %d registered\n", len(registry.List()))

	// Initialize the three-layer prompt loader (L2 embed defaults + L3 user rules).
	// Created before MCP so that mcpMgr.SetPromptLoader can wire Reload integration.
	promptsDir := os.Getenv("PROMPTS_DIR")
	if promptsDir == "" {
		promptsDir = filepath.Join(workspaceDir, "prompts")
	}
	rulesPath := os.Getenv("USER_RULES_PATH")
	if rulesPath == "" {
		rulesPath = filepath.Join(workspaceDir, "rules.md")
	}
	soulPath := os.Getenv("SOUL_PATH")
	if soulPath == "" {
		soulPath = filepath.Join(workspaceDir, "soul.md")
	}
	promptLoader := prompt.NewPromptLoader(promptsDir, rulesPath, soulPath)
	fmt.Printf("📋 Prompt loader: L2=%s L3=%s Soul=%s\n", promptsDir, rulesPath, soulPath)

	// Initialize MCP client manager (optional — only when mcp.json exists)
	mcpConfigPath := os.Getenv("MCP_CONFIG")
	if mcpConfigPath == "" {
		mcpConfigPath = "mcp.json"
	}
	if _, statErr := os.Stat(mcpConfigPath); statErr == nil {
		mcpMgr := mcp.NewManager(mcpConfigPath)
		// Wire prompt cache invalidation into mcp_reload so hot-reloading
		// prompts and MCP config both happen with a single tool call.
		mcpMgr.SetPromptLoader(promptLoader)
		// Wire skill reload into mcp_reload so that calling mcp_reload also
		// reloads workspace skills — one command covers everything.
		mcpMgr.AddReloadHook(skillMgr.Reload)
		// Always register the reload tool so the agent can fix connection issues
		// even if the initial ConnectAll fails partially or completely.
		registry.Register(mcp.NewReloadTool(mcpMgr, registry))

		n, mcpErrs := mcpMgr.ConnectAll(context.Background())
		for _, e := range mcpErrs {
			log.Printf("⚠️  MCP connect: %v", e)
		}
		if n > 0 {
			if err := mcpMgr.RegisterTools(context.Background(), registry); err != nil {
				log.Printf("⚠️  MCP register tools: %v", err)
			}
			fmt.Printf("🔌 MCP: %d server(s) connected\n", n)
		}
		defer mcpMgr.CloseAll()
	}

	// Create execution logger for development debugging
	logDir := filepath.Join(workspaceDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Printf("⚠️ Failed to create log directory %q: %v", logDir, err)
	}
	execLogger, err := agent.NewExecLogger(filepath.Join(logDir, "agent_exec.md"))
	if err != nil {
		log.Printf("⚠️ Exec logger disabled: %v", err)
	} else {
		defer execLogger.Close()
		fmt.Printf("📝 Exec log: logs/agent_exec.md\n")
	}

	// Initialize session store for multi-turn conversation
	sessionTTL := 30 * time.Minute
	sessionMaxTurns := 10
	if v := os.Getenv("SESSION_TTL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sessionTTL = time.Duration(n) * time.Minute
		} else {
			log.Printf("⚠️ Invalid SESSION_TTL_MINUTES=%q, using default 30m", v)
		}
	}
	if v := os.Getenv("SESSION_MAX_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sessionMaxTurns = n
		} else {
			log.Printf("⚠️ Invalid SESSION_MAX_TURNS=%q, using default 10", v)
		}
	}
	sessionStore := session.NewStore(sessionTTL, sessionMaxTurns)
	defer sessionStore.Close()
	fmt.Printf("💬 Session: TTL=%v MaxTurns=%d\n", sessionTTL, sessionMaxTurns)

	// Create handlers
	thinkingMode := llmClient.GetConfig().ResolveThinkingMode()
	toolCallMode := llmClient.GetConfig().ToolCallMode // raw value: "auto", "fc", or "yaml"
	contextWindow := llmClient.GetConfig().ResolveContextWindow()
	chatHandler := web.NewChatHandler(llmClient, 3, contextWindow, sessionStore, promptLoader)
	agentHandler := web.NewAgentHandler(llmClient, registry, workspaceDir, execLogger, thinkingMode, toolCallMode, contextWindow, sessionStore, promptLoader)
	fmt.Printf("🧠 Thinking: %s\n", thinkingMode)
	fmt.Printf("🔧 ToolCall: %s (resolved: %s)\n", toolCallMode, llmClient.GetConfig().ResolveToolCallMode())
	fmt.Printf("📐 ContextWindow: %d tokens\n", contextWindow)

	// Create and start web server
	server, err := web.NewServer(chatHandler, agentHandler)
	if err != nil {
		log.Fatalf("❌ Failed to create web server: %v", err)
	}

	if err := server.Start(); err != nil {
		log.Fatalf("❌ Server error: %v", err)
	}
}
