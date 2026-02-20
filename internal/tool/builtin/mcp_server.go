package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// mcpConfig mirrors the top-level structure of mcp.json for read/write access.
// This is used by the B-phase management tools (mcp_server_add/remove/list).
// It is a local copy to avoid circular dependency on the mcp package.
type mcpConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// mcpServerEntry is the JSON representation of a single server in mcp.json.
// Fields mirror mcp.ServerConfig. We keep the raw fields here so that unknown
// fields (e.g. _meta) round-trip correctly from existing entries we don't modify.
type mcpServerEntry struct {
	Transport string            `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       []string          `json:"env,omitempty"`
	Lifecycle string            `json:"lifecycle,omitempty"`
	Meta      map[string]string `json:"_meta,omitempty"`
}

// readMCPConfig reads and parses mcp.json. Returns an empty MCPServers map if file
// doesn't exist yet. All callers must hold no locks (pure I/O helper).
func readMCPConfig(path string) (mcpConfig, error) {
	cfg := mcpConfig{MCPServers: make(map[string]mcpServerEntry)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("读取 mcp.json 失败: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 mcp.json 失败: %w", err)
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]mcpServerEntry)
	}
	return cfg, nil
}

// writeMCPConfig serialises cfg to path with indentation.
func writeMCPConfig(path string, cfg mcpConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 mcp.json 失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入 mcp.json 失败: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// mcp_server_add
// ─────────────────────────────────────────────────────────────────────────────

// MCPServerAddTool registers a new MCP server entry in mcp.json.
type MCPServerAddTool struct {
	mcpConfigPath string
}

// NewMCPServerAddTool creates the mcp_server_add tool. mcpConfigPath is the
// absolute path to mcp.json. Typically injected from main.go.
func NewMCPServerAddTool(mcpConfigPath string) *MCPServerAddTool {
	return &MCPServerAddTool{mcpConfigPath: mcpConfigPath}
}

func (t *MCPServerAddTool) Name() string { return "mcp_server_add" }
func (t *MCPServerAddTool) Description() string {
	return "向 mcp.json 注册一个新的 MCP server 条目。注册成功后需调用 mcp_reload 让改动生效。" +
		"若名称已存在则返回错误（不覆盖），请先用 mcp_server_remove 移除旧条目。"
}

func (t *MCPServerAddTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "name", Type: "string", Required: true,
			Description: "Server 名称，全局唯一（mcp.json map key）。示例：excel-tool"},
		tool.SchemaParam{Name: "transport", Type: "string", Required: true,
			Description: `传输协议："stdio"（本地进程）或 "sse"（HTTP SSE）。示例：stdio`,
			Enum:        []string{"stdio", "sse"}},
		tool.SchemaParam{Name: "command", Type: "string", Required: false,
			Description: `stdio 专用：可执行程序路径或名称。示例：node`},
		tool.SchemaParam{Name: "args", Type: "string", Required: false,
			Description: `stdio 专用：命令行参数，JSON 数组格式字符串。示例：["--import","tsx","skills/excel/server.ts"]`},
		tool.SchemaParam{Name: "url", Type: "string", Required: false,
			Description: `sse 专用：SSE 服务器 URL。示例：http://localhost:8080`},
		tool.SchemaParam{Name: "env", Type: "string", Required: false,
			Description: `stdio 专用：额外环境变量，JSON 数组格式字符串，形如 ["KEY=VALUE"]。示例：["API_KEY=abc123"]`},
		tool.SchemaParam{Name: "lifecycle", Type: "string", Required: false,
			Description: `生命周期："persistent"（默认，进程常驻）或 "per_call"（每次调用新起进程）。示例：persistent`,
			Enum:        []string{"persistent", "per_call"}},
	)
}

type mcpServerAddArgs struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Command   string `json:"command"`
	Args      string `json:"args"` // JSON-encoded []string
	URL       string `json:"url"`
	Env       string `json:"env"` // JSON-encoded []string
	Lifecycle string `json:"lifecycle"`
}

func (t *MCPServerAddTool) Execute(_ context.Context, raw json.RawMessage) (tool.ToolResult, error) {
	var a mcpServerAddArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	// Validate required fields.
	if a.Name == "" {
		return tool.ToolResult{Error: "name 不得为空"}, nil
	}
	if a.Transport != "stdio" && a.Transport != "sse" {
		return tool.ToolResult{Error: `transport 必须为 "stdio" 或 "sse"，当前值: ` + a.Transport}, nil
	}

	// Parse optional JSON-array strings.
	var args, env []string
	if a.Args != "" {
		if err := json.Unmarshal([]byte(a.Args), &args); err != nil {
			return tool.ToolResult{Error: fmt.Sprintf(`args 格式错误（需要 JSON 数组字符串，如 ["a","b"]）: %v`, err)}, nil
		}
	}
	if a.Env != "" {
		if err := json.Unmarshal([]byte(a.Env), &env); err != nil {
			return tool.ToolResult{Error: fmt.Sprintf(`env 格式错误（需要 JSON 数组字符串，如 ["KEY=VAL"]）: %v`, err)}, nil
		}
	}

	cfg, err := readMCPConfig(t.mcpConfigPath)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	// Refuse to silently overwrite an existing entry.
	if _, exists := cfg.MCPServers[a.Name]; exists {
		return tool.ToolResult{
			Error: fmt.Sprintf("server %q 已存在 — 请先用 mcp_server_remove 移除旧条目再重新注册", a.Name),
		}, nil
	}

	entry := mcpServerEntry{
		Transport: a.Transport,
		Command:   a.Command,
		Args:      args,
		URL:       a.URL,
		Env:       env,
		Lifecycle: a.Lifecycle,
		Meta:      map[string]string{"origin": "agent"},
	}
	cfg.MCPServers[a.Name] = entry

	if err := writeMCPConfig(t.mcpConfigPath, cfg); err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	return tool.ToolResult{
		Output: fmt.Sprintf(
			"✅ server %q 已写入 mcp.json（transport=%s, lifecycle=%s）。\n请调用 mcp_reload 让改动生效。",
			a.Name, a.Transport, func() string {
				if a.Lifecycle == "" {
					return "persistent（默认）"
				}
				return a.Lifecycle
			}(),
		),
	}, nil
}

func (t *MCPServerAddTool) Init(_ context.Context) error { return nil }
func (t *MCPServerAddTool) Close() error                 { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// mcp_server_remove
// ─────────────────────────────────────────────────────────────────────────────

// MCPServerRemoveTool removes an MCP server entry from mcp.json.
type MCPServerRemoveTool struct {
	mcpConfigPath string
}

func NewMCPServerRemoveTool(mcpConfigPath string) *MCPServerRemoveTool {
	return &MCPServerRemoveTool{mcpConfigPath: mcpConfigPath}
}

func (t *MCPServerRemoveTool) Name() string { return "mcp_server_remove" }
func (t *MCPServerRemoveTool) Description() string {
	return "从 mcp.json 移除一个 MCP server 条目。操作成功后需调用 mcp_reload 让改动生效。" +
		"⚠️ 危险操作：需传入 confirm=\"yes\" 参数方可执行，防止误删。"
}

func (t *MCPServerRemoveTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "name", Type: "string", Required: true,
			Description: "要移除的 server 名称（mcp.json map key）。示例：excel-tool"},
		tool.SchemaParam{Name: "confirm", Type: "string", Required: true,
			Description: `安全确认字段，必须填写 "yes" 才能执行移除，防止误操作。`},
	)
}

type mcpServerRemoveArgs struct {
	Name    string `json:"name"`
	Confirm string `json:"confirm"`
}

func (t *MCPServerRemoveTool) Execute(_ context.Context, raw json.RawMessage) (tool.ToolResult, error) {
	var a mcpServerRemoveArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}
	if a.Name == "" {
		return tool.ToolResult{Error: "name 不得为空"}, nil
	}
	if a.Confirm != "yes" {
		return tool.ToolResult{
			Error: fmt.Sprintf(
				"⚠️ 危险操作：移除 server %q 将卸载其注册的所有工具，需调用 mcp_reload 生效。\n"+
					"确认请将 confirm 参数设为 \"yes\" 重新调用。", a.Name),
		}, nil
	}

	cfg, err := readMCPConfig(t.mcpConfigPath)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	if _, exists := cfg.MCPServers[a.Name]; !exists {
		return tool.ToolResult{
			Error: fmt.Sprintf("server %q 不存在于 mcp.json — 请用 mcp_server_list 查看当前列表", a.Name),
		}, nil
	}

	delete(cfg.MCPServers, a.Name)
	if err := writeMCPConfig(t.mcpConfigPath, cfg); err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	return tool.ToolResult{
		Output: fmt.Sprintf("✅ server %q 已从 mcp.json 移除。\n请调用 mcp_reload 让改动生效（运行中的进程将在 reload 时被关闭）。", a.Name),
	}, nil
}

func (t *MCPServerRemoveTool) Init(_ context.Context) error { return nil }
func (t *MCPServerRemoveTool) Close() error                 { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// mcp_server_list
// ─────────────────────────────────────────────────────────────────────────────

// MCPServerListTool reads mcp.json and returns all registered server entries.
type MCPServerListTool struct {
	mcpConfigPath string
}

func NewMCPServerListTool(mcpConfigPath string) *MCPServerListTool {
	return &MCPServerListTool{mcpConfigPath: mcpConfigPath}
}

func (t *MCPServerListTool) Name() string { return "mcp_server_list" }
func (t *MCPServerListTool) Description() string {
	return "列出 mcp.json 中所有已注册的 MCP server 条目（包含 lifecycle、origin 等元数据）。" +
		"创建新 server 前必须调用此工具确认名称无冲突。"
}

func (t *MCPServerListTool) InputSchema() json.RawMessage {
	return tool.BuildSchema() // no params
}

func (t *MCPServerListTool) Execute(_ context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	cfg, err := readMCPConfig(t.mcpConfigPath)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	if len(cfg.MCPServers) == 0 {
		return tool.ToolResult{Output: "mcp.json 中暂无注册的 server。"}, nil
	}

	// Build a human-readable table.
	type row struct {
		name      string
		transport string
		lifecycle string
		origin    string
		scanRes   string
		scannedAt string
		command   string
	}
	rows := make([]row, 0, len(cfg.MCPServers))
	for name, e := range cfg.MCPServers {
		lc := e.Lifecycle
		if lc == "" {
			lc = "persistent"
		}
		origin := e.Meta["origin"]
		if origin == "" {
			origin = "user"
		}
		scanRes := e.Meta["scan_result"]
		if scanRes == "" {
			scanRes = "—"
		}
		scannedAt := e.Meta["scanned_at"]
		if scannedAt == "" {
			scannedAt = "—"
		}
		cmd := e.Command
		if len(e.Args) > 0 {
			argsBytes, _ := json.Marshal(e.Args)
			cmd += " " + string(argsBytes)
		}
		if e.URL != "" {
			cmd = e.URL
		}
		rows = append(rows, row{
			name:      name,
			transport: e.Transport,
			lifecycle: lc,
			origin:    origin,
			scanRes:   scanRes,
			scannedAt: scannedAt,
			command:   cmd,
		})
	}

	// Sort by name for deterministic output.
	for i := 0; i < len(rows)-1; i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[i].name > rows[j].name {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}

	out := fmt.Sprintf("mcp.json 已注册 %d 个 server（读取时间: %s）:\n\n",
		len(rows), time.Now().Format("2006-01-02 15:04:05"))
	for _, r := range rows {
		out += fmt.Sprintf("▶ %s\n  transport=%s  lifecycle=%s  origin=%s  scan=%s(%s)\n  cmd: %s\n\n",
			r.name, r.transport, r.lifecycle, r.origin, r.scanRes, r.scannedAt, r.command)
	}

	return tool.ToolResult{Output: out}, nil
}

func (t *MCPServerListTool) Init(_ context.Context) error { return nil }
func (t *MCPServerListTool) Close() error                 { return nil }
