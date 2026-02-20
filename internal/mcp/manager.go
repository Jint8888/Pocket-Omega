package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pocketomega/pocket-omega/internal/prompt"
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ReloadHook is a function called at the end of every Reload invocation.
// It receives the same ctx and registry so hooks can register/unregister tools.
// The returned string (may be empty) is appended to the reload summary.
type ReloadHook func(ctx context.Context, registry *tool.Registry) string

// Manager owns the lifecycle of all MCP server connections.
// It is the single source of truth for which servers are active and which
// tool adapters are registered in the tool.Registry.
//
// Concurrency model: state changes are guarded by mu. Network I/O is always
// performed outside the lock so that a slow or hung server cannot block other
// Manager operations (e.g. CloseAll during shutdown).
type Manager struct {
	configPath       string
	mu               sync.Mutex
	configs          map[string]ServerConfig // last successfully loaded config
	clients          map[string]*Client      // active connections keyed by server name
	serverTools      map[string][]string     // server name → registered tool names
	perCallToolInfos map[string][]ToolInfo   // tool discovery cache for per_call servers (ConnectAll → RegisterTools)
	promptLoader     *prompt.PromptLoader    // optional; when set, Reload also clears prompt cache
	reloadHooks      []ReloadHook            // optional hooks fired at end of every Reload
}

// NewManager creates a Manager for the given mcp.json path.
// No connections are established until ConnectAll is called.
func NewManager(configPath string) *Manager {
	return &Manager{
		configPath:       configPath,
		configs:          make(map[string]ServerConfig),
		clients:          make(map[string]*Client),
		serverTools:      make(map[string][]string),
		perCallToolInfos: make(map[string][]ToolInfo),
	}
}

// SetPromptLoader registers a PromptLoader so that Reload also invalidates
// the prompt cache.  Must be called before the first Reload invocation.
// Safe for concurrent use.
func (m *Manager) SetPromptLoader(l *prompt.PromptLoader) {
	m.mu.Lock()
	m.promptLoader = l
	m.mu.Unlock()
}

// AddReloadHook registers a function that is called at the end of every Reload.
// Hooks are invoked in registration order. Each hook's non-empty return value
// is appended to the reload summary. Safe for concurrent use.
func (m *Manager) AddReloadHook(hook ReloadHook) {
	m.mu.Lock()
	m.reloadHooks = append(m.reloadHooks, hook)
	m.mu.Unlock()
}

// ConnectAll loads the config and connects to all configured servers.
// Network I/O is performed outside the lock; the lock is acquired only for
// final state updates.
//
// Returns the number of successfully connected servers and per-server errors
// (best-effort: failures do not prevent other servers from connecting).
func (m *Manager) ConnectAll(ctx context.Context) (int, []error) {
	configs, err := LoadConfig(m.configPath)
	if err != nil {
		return 0, []error{fmt.Errorf("mcp: load config: %w", err)}
	}

	// Establish connections outside the lock.
	// per_call servers: connect temporarily to discover tools, then close immediately.
	// The client stored in m.clients for per_call is nil; Execute() creates fresh
	// connections on demand using cfg stored in the adapter.
	type connResult struct {
		name  string
		cfg   ServerConfig
		cli   *Client // nil for per_call after tool discovery
		tools []ToolInfo
		err   error
	}
	results := make([]connResult, 0, len(configs))
	for name, cfg := range configs {
		if cfg.Lifecycle == "per_call" {
			// Temporary connection: discover tools then close.
			tmp := NewClient(cfg)
			if err := tmp.Connect(ctx); err != nil {
				results = append(results, connResult{name: name, err: err})
				log.Printf("[MCP] per_call probe failed: %s: %v", name, err)
				continue
			}
			tools, err := tmp.ListTools(ctx)
			_ = tmp.Close() // ephemeral — close immediately after discovery
			if err != nil {
				results = append(results, connResult{name: name, err: err})
				log.Printf("[MCP] per_call list tools failed: %s: %v", name, err)
				continue
			}
			results = append(results, connResult{name: name, cfg: cfg, cli: nil, tools: tools})
			log.Printf("[MCP] per_call discovered: %s (%d tool(s))", name, len(tools))
		} else {
			cli := NewClient(cfg)
			if err := cli.Connect(ctx); err != nil {
				results = append(results, connResult{name: name, err: err})
				log.Printf("[MCP] Connect failed: %s: %v", name, err)
			} else {
				results = append(results, connResult{name: name, cfg: cfg, cli: cli})
				log.Printf("[MCP] Connected: %s (%s)", name, cfg.Transport)
			}
		}
	}

	// Update internal state under the lock.
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	connected := 0
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("server %q: %w", r.name, r.err))
			continue
		}
		m.clients[r.name] = r.cli // nil for per_call
		m.configs[r.name] = r.cfg
		// Cache per_call tool infos so RegisterTools can register adapters
		// without a redundant network round-trip. Persistent servers discover
		// their tools during the RegisterTools call itself.
		if r.cli == nil && len(r.tools) > 0 {
			m.perCallToolInfos[r.name] = r.tools
		}
		connected++
	}
	return connected, errs
}

// RegisterTools lists the tools from all configured servers and registers
// them as MCPToolAdapter instances in the provided registry.
//
// For persistent servers: ListTools is called on the live client (network I/O
// performed outside the lock).
// For per_call servers: tools were already discovered during ConnectAll;
// RegisterTools reads the cached list from serverTools and creates adapters
// directly without any network I/O.
func (m *Manager) RegisterTools(ctx context.Context, registry *tool.Registry) error {
	// Snapshot connected clients and configs under the lock.
	m.mu.Lock()
	snap := make(map[string]*Client, len(m.clients))
	cfgSnap := make(map[string]ServerConfig, len(m.configs))
	for name, cli := range m.clients {
		snap[name] = cli
		cfgSnap[name] = m.configs[name]
	}
	m.mu.Unlock()

	// Fetch tool lists outside the lock (network I/O for persistent servers only).
	type fetchResult struct {
		name  string
		cfg   ServerConfig
		tools []ToolInfo
		err   error
	}
	results := make([]fetchResult, 0, len(snap))
	for name, cli := range snap {
		cfg := cfgSnap[name]
		if cli == nil {
			// per_call: consume the tool list cached by ConnectAll — no network I/O.
			m.mu.Lock()
			cached := m.perCallToolInfos[name]
			delete(m.perCallToolInfos, name) // consume once; re-populated on next Reload
			m.mu.Unlock()
			results = append(results, fetchResult{name: name, cfg: cfg, tools: cached})
			continue
		}
		tools, err := cli.ListTools(ctx)
		results = append(results, fetchResult{name: name, cfg: cfg, tools: tools, err: err})
	}

	// Register adapters and update serverTools under the lock.
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range results {
		if r.err != nil {
			return fmt.Errorf("mcp: list tools for %q: %w", r.name, r.err)
		}
		var toolNames []string
		for _, ti := range r.tools {
			adapter := NewMCPToolAdapter(r.name, ti, m.clients[r.name], r.cfg)
			registry.Register(adapter)
			toolNames = append(toolNames, adapter.Name())
		}
		m.serverTools[r.name] = toolNames
		log.Printf("[MCP] Registered %d tool(s) from server %q", len(r.tools), r.name)
	}
	return nil
}

// Reload re-reads mcp.json and applies a diff:
//   - Added servers: security-scanned (stdio .py), connected, tools registered.
//   - Removed servers: tools unregistered, connections closed.
//   - Unchanged servers: left untouched.
//
// Network I/O is performed outside the lock. Returns a human-readable summary
// and any fatal configuration error. Per-server failures are described in the
// summary but do not cause Reload itself to return an error.
func (m *Manager) Reload(ctx context.Context, registry *tool.Registry) (string, error) {
	// Step 1: Load new config (no lock needed).
	newConfigs, err := LoadConfig(m.configPath)
	if err != nil {
		return "", fmt.Errorf("mcp reload: load config: %w", err)
	}

	// Step 2: Compute diff under the lock.
	m.mu.Lock()
	toRemove := make([]string, 0)
	toAdd := make([]ServerConfig, 0)
	unchanged := 0

	for name := range m.configs {
		if _, exists := newConfigs[name]; !exists {
			toRemove = append(toRemove, name)
		}
	}
	for name, cfg := range newConfigs {
		if _, exists := m.configs[name]; !exists {
			toAdd = append(toAdd, cfg)
		} else {
			unchanged++
		}
	}
	m.mu.Unlock()

	// Step 3: Perform removals (close connections, unregister tools).
	removed := 0
	for _, name := range toRemove {
		m.mu.Lock()
		toolNames := m.serverTools[name]
		cli := m.clients[name]
		delete(m.serverTools, name)
		delete(m.clients, name)
		delete(m.configs, name)
		m.mu.Unlock()

		for _, toolName := range toolNames {
			registry.Unregister(toolName)
		}
		if cli != nil {
			if err := cli.Close(); err != nil {
				log.Printf("[MCP] Close error for %q: %v", name, err)
			}
		}
		removed++
		log.Printf("[MCP] Disconnected: %s", name)
	}

	// Step 4: Security scan and connect new servers (network I/O outside lock).
	type addResult struct {
		name    string
		cfg     ServerConfig
		cli     *Client
		tools   []ToolInfo
		blocked bool
		notice  string
		err     error
	}
	addResults := make([]addResult, 0, len(toAdd))

	for _, cfg := range toAdd {
		res := addResult{name: cfg.Name, cfg: cfg}

		// Security scan for stdio scripts. Persists scan_result + scanned_at to mcp.json _meta.
		if cfg.Transport == "stdio" {
			pyScript := findPyScript(cfg)
			if pyScript != "" {
				findings, scanErr := ScanScript(pyScript)
				today := time.Now().Format("2006-01-02")
				if scanErr != nil {
					res.notice = fmt.Sprintf("[WARNING] scan error for %q: %v", cfg.Name, scanErr)
					// Non-blocking: attempt to connect anyway (read error != malicious).
				} else if HasCritical(findings) {
					LogFindings(cfg.Name, findings)
					var lines []string
					lines = append(lines, fmt.Sprintf("[BLOCKED] server %q: critical security findings in %s", cfg.Name, pyScript))
					for _, f := range findings {
						if f.Severity == SeverityCritical {
							lines = append(lines, fmt.Sprintf("  [%s] line %d: %s", f.Rule, f.Line, f.Snippet))
						}
					}
					res.blocked = true
					res.notice = strings.Join(lines, "\n")
					updateServerMeta(m.configPath, cfg.Name, map[string]string{
						"scan_result": "blocked",
						"scanned_at":  today,
					})
					addResults = append(addResults, res)
					continue
				} else {
					LogFindings(cfg.Name, findings) // logs warnings (if any)
					scanResult := "clean"
					if len(findings) > 0 {
						scanResult = "warning"
					}
					updateServerMeta(m.configPath, cfg.Name, map[string]string{
						"scan_result": scanResult,
						"scanned_at":  today,
					})
				}
			}
		}

		// Connect and list tools (per_call: ephemeral connection; persistent: kept alive).
		if cfg.Lifecycle == "per_call" {
			tmp := NewClient(cfg)
			if err := tmp.Connect(ctx); err != nil {
				res.err = err
				res.notice = fmt.Sprintf("[WARNING] connect %q: %v", cfg.Name, err)
				addResults = append(addResults, res)
				continue
			}
			tools, err := tmp.ListTools(ctx)
			_ = tmp.Close() // ephemeral — close immediately after discovery
			if err != nil {
				res.err = err
				res.notice = fmt.Sprintf("[WARNING] list tools %q: %v", cfg.Name, err)
				addResults = append(addResults, res)
				continue
			}
			// res.cli stays nil; adapters reconnect per Execute() call.
			res.tools = tools
		} else {
			cli := NewClient(cfg)
			if err := cli.Connect(ctx); err != nil {
				res.err = err
				res.notice = fmt.Sprintf("[WARNING] connect %q: %v", cfg.Name, err)
				addResults = append(addResults, res)
				continue
			}
			tools, err := cli.ListTools(ctx)
			if err != nil {
				_ = cli.Close()
				res.err = err
				res.notice = fmt.Sprintf("[WARNING] list tools %q: %v", cfg.Name, err)
				addResults = append(addResults, res)
				continue
			}
			res.cli = cli
			res.tools = tools
		}
		addResults = append(addResults, res)
	}

	// Step 5: Register successful additions under the lock.
	added := 0
	var notices []string

	for _, res := range addResults {
		if res.notice != "" {
			notices = append(notices, res.notice)
		}
		if res.blocked || res.err != nil {
			continue
		}
		// Both persistent (res.cli != nil) and per_call (res.cli == nil) are handled
		// here: per_call adapters carry cfg and reconnect on each Execute().
		var toolNames []string
		for _, ti := range res.tools {
			adapter := NewMCPToolAdapter(res.name, ti, res.cli, res.cfg)
			registry.Register(adapter)
			toolNames = append(toolNames, adapter.Name())
		}
		m.mu.Lock()
		m.clients[res.name] = res.cli // nil for per_call
		m.configs[res.name] = res.cfg
		m.serverTools[res.name] = toolNames
		m.mu.Unlock()

		added++
		log.Printf("[MCP] Connected: %s (%s), %d tool(s)", res.name, res.cfg.Transport, len(res.tools))
	}

	summary := fmt.Sprintf("MCP reload: +%d connected, -%d removed, %d unchanged",
		added, removed, unchanged)
	if len(notices) > 0 {
		summary += "\n" + strings.Join(notices, "\n")
	}

	// Also refresh the prompt cache so updated L2/L3 files take effect
	// without a separate reload command.
	m.mu.Lock()
	pl := m.promptLoader
	m.mu.Unlock()
	if pl != nil {
		pl.Reload()
		summary += "\nPrompt cache cleared."
	}

	// Fire any registered reload hooks (extension point for external integrations).
	m.mu.Lock()
	hooks := make([]ReloadHook, len(m.reloadHooks))
	copy(hooks, m.reloadHooks)
	m.mu.Unlock()

	for _, hook := range hooks {
		if s := hook(ctx, registry); s != "" {
			summary += "\n" + s
		}
	}

	return summary, nil
}

// CloseAll terminates all active MCP server connections.
// It is safe to call multiple times.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	clients := make(map[string]*Client, len(m.clients))
	for name, cli := range m.clients {
		clients[name] = cli
		delete(m.clients, name)
	}
	m.mu.Unlock()

	for name, cli := range clients {
		if cli == nil {
			continue // per_call servers have no persistent connection to close
		}
		if err := cli.Close(); err != nil {
			log.Printf("[MCP] Close error for %q: %v", name, err)
		}
	}
	log.Printf("[MCP] All connections closed")
}

// updateServerMeta merges key-value pairs into the _meta object of a named
// server entry in mcp.json, preserving all other existing fields and keys.
// This is a best-effort operation — failures are logged but do not interrupt Reload.
func updateServerMeta(configPath, serverName string, updates map[string]string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("[MCP] updateServerMeta: read %q: %v", configPath, err)
		return
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		log.Printf("[MCP] updateServerMeta: parse: %v", err)
		return
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return
	}
	entry, ok := servers[serverName].(map[string]any)
	if !ok {
		return
	}
	meta, _ := entry["_meta"].(map[string]any)
	if meta == nil {
		meta = make(map[string]any)
	}
	for k, v := range updates {
		meta[k] = v
	}
	entry["_meta"] = meta
	servers[serverName] = entry
	root["mcpServers"] = servers
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		log.Printf("[MCP] updateServerMeta: marshal: %v", err)
		return
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		log.Printf("[MCP] updateServerMeta: write: %v", err)
	}
}

// findPyScript returns the first .py file referenced in a ServerConfig,
// checking the command itself and then the argument list.
func findPyScript(cfg ServerConfig) string {
	if strings.HasSuffix(cfg.Command, ".py") {
		return cfg.Command
	}
	for _, arg := range cfg.Args {
		if strings.HasSuffix(arg, ".py") {
			return arg
		}
	}
	return ""
}
