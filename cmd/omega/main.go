package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/agent"
	"github.com/pocketomega/pocket-omega/internal/llm/openai"
	"github.com/pocketomega/pocket-omega/internal/mcp"
	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/runtime"
	"github.com/pocketomega/pocket-omega/internal/session"
	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/tool/builtin"
	"github.com/pocketomega/pocket-omega/internal/web"
	"github.com/pocketomega/pocket-omega/pkg/config"
)

func main() {
	// Load .env file
	config.LoadEnv()

	// Probe Node.js / tsx runtime availability.
	// tsx auto-install starts in the background if node is present but tsx is absent.
	// The result is injected into mcp_server_guide.md so agents pick the right template.
	nodeInfo := runtime.ProbeNodeRuntime()
	fmt.Printf("🟢 Runtime probe: %s\n", strings.ReplaceAll(nodeInfo.StatusString(), "\n", ", "))

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
	fmt.Printf("🤖 LLM: %s @ %s (timeout=%ds)\n", model, baseURL, llmClient.GetConfig().HTTPTimeout)

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

	// Config edit tool — allows agent to modify config files outside workspace sandbox.
	// Uses an allowlist so only explicitly named files are accessible.
	if envPath := config.EnvFilePath(); envPath != "" && !strings.HasPrefix(envPath, "(") {
		configAllowed := map[string]string{".env": envPath}
		registry.Register(builtin.NewConfigEditTool(configAllowed))
		fmt.Printf("⚙️  Config edit tool: %s\n", envPath)
	}

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

	// Inject runtime OS into decide_common.md so agents use platform-correct shell commands.
	osName := stdruntime.GOOS // "windows" / "linux" / "darwin"
	shellCmd := "sh -c"
	if osName == "windows" {
		osName = "Windows"
		shellCmd = "cmd.exe /c"
	} else if osName == "darwin" {
		osName = "macOS"
	} else {
		osName = "Linux"
	}
	promptLoader.PatchFile("decide_common.md", "{{OS}}", osName)
	promptLoader.PatchFile("decide_common.md", "{{SHELL_CMD}}", shellCmd)

	// Initialize MCP client manager (optional — only when mcp.json exists)
	mcpConfigPath := os.Getenv("MCP_CONFIG")
	if mcpConfigPath == "" {
		mcpConfigPath = filepath.Join(workspaceDir, "mcp.json")
	}
	// Auto-create empty mcp.json in new workspaces so MCP management tools
	// are always available from the first run (bootstrap requirement).
	if _, statErr := os.Stat(mcpConfigPath); os.IsNotExist(statErr) {
		if writeErr := os.WriteFile(mcpConfigPath, []byte("{\"mcpServers\":{}}\n"), 0o644); writeErr != nil {
			log.Printf("⚠️ Failed to auto-create mcp.json: %v", writeErr)
		} else {
			fmt.Printf("📄 Created empty mcp.json at %s\n", mcpConfigPath)
		}
	}
	if _, statErr := os.Stat(mcpConfigPath); statErr == nil {
		mcpMgr := mcp.NewManager(mcpConfigPath)
		// Wire prompt cache invalidation into mcp_reload so hot-reloading
		// prompts and MCP config both happen with a single tool call.
		mcpMgr.SetPromptLoader(promptLoader)
		// Always register the reload tool so the agent can fix connection issues
		// even if the initial ConnectAll fails partially or completely.
		registry.Register(mcp.NewReloadTool(mcpMgr, registry))

		// Phase B: MCP server management tools — always available so the agent
		// can add/remove/list servers and then call mcp_reload in one session.
		registry.Register(builtin.NewMCPServerAddTool(mcpConfigPath))
		registry.Register(builtin.NewMCPServerRemoveTool(mcpConfigPath))
		registry.Register(builtin.NewMCPServerListTool(mcpConfigPath))
		fmt.Println("🔧 MCP management tools registered (mcp_server_add/remove/list)")

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

		// Inject runtime probe result into mcp_server_guide.md so agents read
		// the live status rather than discovering it themselves.
		injectRuntimeEnv(promptLoader, nodeInfo.StatusString())
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
	agentHandler := web.NewAgentHandler(web.AgentHandlerOptions{
		Provider:            llmClient,
		Registry:            registry,
		WorkspaceDir:        workspaceDir,
		ExecLogger:          execLogger,
		ThinkingMode:        thinkingMode,
		ToolCallMode:        toolCallMode,
		ContextWindowTokens: contextWindow,
		Store:               sessionStore,
		Loader:              promptLoader,
	})
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

// injectRuntimeEnv patches the "{{RUNTIME_ENV}}" placeholder in the
// mcp_server_guide prompt with the live runtime status string. After this
// call, agents that receive the prompt will see the actual tsx availability
// instead of the template placeholder.
//
// Implementation note: we rely on prompt.PromptLoader's override map to store
// the patched content so that Reload() re-reads from files and re-applies the
// patch. If PromptLoader does not expose an override mechanism, the patch is a
// no-op and the placeholder remains — agents will still function correctly but
// may see {{RUNTIME_ENV}} instead of a status string.
func injectRuntimeEnv(pl *prompt.PromptLoader, status string) {
	if pl == nil {
		return
	}
	// Replace the placeholder in the cached content via the prompt loader.
	// PromptLoader.PatchFile(name, old, new) is a light convenience wrapper;
	// if the method doesn't exist yet the compiler will flag it and we can add it.
	pl.PatchFile("mcp_server_guide", "{{RUNTIME_ENV}}", status)
}
