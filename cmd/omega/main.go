package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/pocketomega/pocket-omega/internal/agent"
	"github.com/pocketomega/pocket-omega/internal/llm/openai"
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

	// Create execution logger for development debugging
	logDir := filepath.Join(workspaceDir, "logs")
	os.MkdirAll(logDir, 0o755)
	execLogger, err := agent.NewExecLogger(filepath.Join(logDir, "agent_exec.md"))
	if err != nil {
		log.Printf("⚠️ Exec logger disabled: %v", err)
	} else {
		defer execLogger.Close()
		fmt.Printf("📝 Exec log: logs/agent_exec.md\n")
	}

	// Create handlers
	chatHandler := web.NewChatHandler(llmClient, 3)
	thinkingMode := llmClient.GetConfig().ResolveThinkingMode()
	toolCallMode := llmClient.GetConfig().ToolCallMode // raw value: "auto", "fc", or "yaml"
	contextWindow := llmClient.GetConfig().ResolveContextWindow()
	agentHandler := web.NewAgentHandler(llmClient, registry, workspaceDir, execLogger, thinkingMode, toolCallMode, contextWindow)
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
