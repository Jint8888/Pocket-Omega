package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ─────────────────────────────────────────────────────────────────────────────
// config_edit — 突破沙盒的配置编辑工具
//
// Agent 的文件工具被限制在 WORKSPACE_DIR 内，但 .env 等配置文件可能
// 位于项目根目录（workspace 之外）。本工具通过白名单机制允许 agent
// 安全地读写特定配置文件。
//
// 白名单在 main.go 注册时注入，agent 只能通过别名（如 ".env"）引用
// 文件，无法构造任意路径。
// ─────────────────────────────────────────────────────────────────────────────

// ConfigEditTool provides config file editing outside the workspace sandbox.
type ConfigEditTool struct {
	// allowedFiles maps alias → absolute path. e.g. {".env": "E:/proj/.env"}
	allowedFiles map[string]string
}

// NewConfigEditTool creates the config_edit tool.
// allowedFiles maps short aliases to their absolute paths on disk.
func NewConfigEditTool(allowedFiles map[string]string) *ConfigEditTool {
	return &ConfigEditTool{allowedFiles: allowedFiles}
}

func (t *ConfigEditTool) Name() string { return "config_edit" }
func (t *ConfigEditTool) Description() string {
	files := make([]string, 0, len(t.allowedFiles))
	for alias := range t.allowedFiles {
		files = append(files, alias)
	}
	sort.Strings(files)
	return fmt.Sprintf(
		"读写工作区外的配置文件（如 .env）。支持 get/set/list 操作。可编辑文件: %s",
		strings.Join(files, ", "),
	)
}

func (t *ConfigEditTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{
			Name:        "file",
			Type:        "string",
			Description: "配置文件别名（如 \".env\"）",
			Required:    true,
		},
		tool.SchemaParam{
			Name:        "action",
			Type:        "string",
			Description: "操作类型",
			Required:    true,
			Enum:        []string{"get", "set", "list"},
		},
		tool.SchemaParam{
			Name:        "key",
			Type:        "string",
			Description: "配置键名（get/set 必填）",
			Required:    false,
		},
		tool.SchemaParam{
			Name:        "value",
			Type:        "string",
			Description: "配置值（set 必填）",
			Required:    false,
		},
	)
}

func (t *ConfigEditTool) Init(_ context.Context) error { return nil }
func (t *ConfigEditTool) Close() error                 { return nil }

type configEditArgs struct {
	File   string `json:"file"`
	Action string `json:"action"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

func (t *ConfigEditTool) Execute(_ context.Context, raw json.RawMessage) (tool.ToolResult, error) {
	var a configEditArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	// Resolve alias → real path via allowlist.
	realPath, ok := t.allowedFiles[a.File]
	if !ok {
		allowed := make([]string, 0, len(t.allowedFiles))
		for alias := range t.allowedFiles {
			allowed = append(allowed, alias)
		}
		sort.Strings(allowed)
		return tool.ToolResult{
			Error: fmt.Sprintf("文件 %q 不在白名单中。允许的文件: %s", a.File, strings.Join(allowed, ", ")),
		}, nil
	}

	switch a.Action {
	case "get":
		return t.doGet(realPath, a.Key)
	case "set":
		return t.doSet(realPath, a.Key, a.Value)
	case "list":
		return t.doList(realPath)
	default:
		return tool.ToolResult{Error: fmt.Sprintf("未知操作 %q，支持: get, set, list", a.Action)}, nil
	}
}

// ── .env format helpers ──────────────────────────────────────────────────

// doGet reads a single key from a .env-style file.
func (t *ConfigEditTool) doGet(path, key string) (tool.ToolResult, error) {
	if key == "" {
		return tool.ToolResult{Error: "get 操作需要提供 key 参数"}, nil
	}

	entries, err := parseEnvFile(path)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("读取配置文件失败: %v", err)}, nil
	}

	for _, e := range entries {
		if e.key == key {
			return tool.ToolResult{Output: fmt.Sprintf("%s=%s", key, e.value)}, nil
		}
	}

	return tool.ToolResult{Error: fmt.Sprintf("key %q 不存在", key)}, nil
}

// doSet sets a key=value in a .env-style file, preserving comments and blank lines.
func (t *ConfigEditTool) doSet(path, key, value string) (tool.ToolResult, error) {
	if key == "" {
		return tool.ToolResult{Error: "set 操作需要提供 key 参数"}, nil
	}

	data, _ := os.ReadFile(path) // missing file → empty, we'll create it
	lines := strings.Split(string(data), "\n")

	// Normalise CRLF: Split on \n leaves trailing \r on Windows files.
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}

	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eqIdx := strings.Index(trimmed, "=")
		if eqIdx < 0 {
			continue
		}
		lineKey := strings.TrimSpace(trimmed[:eqIdx])
		if lineKey == key {
			lines[i] = key + "=" + value
			found = true
			break
		}
	}

	if !found {
		// Append with a blank separator if the file doesn't end with one.
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, key+"="+value)
	}

	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("写入失败: %v", err)}, nil
	}

	verb := "已更新"
	if !found {
		verb = "已新增"
	}
	return tool.ToolResult{Output: fmt.Sprintf("%s %s=%s (文件: %s)", verb, key, value, path)}, nil
}

// doList returns all key=value pairs in a .env-style file.
func (t *ConfigEditTool) doList(path string) (tool.ToolResult, error) {
	entries, err := parseEnvFile(path)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("读取配置文件失败: %v", err)}, nil
	}

	if len(entries) == 0 {
		return tool.ToolResult{Output: "（配置文件为空或不包含任何键值对）"}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("配置文件共 %d 项:\n", len(entries)))
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("  %s=%s\n", e.key, e.value))
	}
	return tool.ToolResult{Output: sb.String()}, nil
}

// envEntry represents one KEY=VALUE pair parsed from a .env file.
type envEntry struct {
	key   string
	value string
}

// parseEnvFile reads a .env-style file and returns all key=value entries.
// Comments (#) and blank lines are skipped.
func parseEnvFile(path string) ([]envEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entries []envEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eqIdx := strings.Index(trimmed, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eqIdx])
		value := strings.TrimSpace(trimmed[eqIdx+1:])
		entries = append(entries, envEntry{key: key, value: value})
	}
	return entries, nil
}
