package skill

import (
	"context"
	"encoding/json"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// ReloadTool implements tool.Tool and exposes the "skill_reload" built-in command.
// When invoked by the agent, it re-scans <workspace>/skills/, adds new skills,
// removes deleted ones, and recompiles any Go skills whose code has changed.
//
// This tool is always registered, regardless of whether mcp.json exists.
type ReloadTool struct {
	manager  *Manager
	registry *tool.Registry
}

// NewReloadTool creates a ReloadTool wired to the given Manager and Registry.
func NewReloadTool(manager *Manager, registry *tool.Registry) *ReloadTool {
	return &ReloadTool{manager: manager, registry: registry}
}

func (t *ReloadTool) Name() string { return "skill_reload" }

func (t *ReloadTool) Description() string {
	return "重新扫描工作区 skills/ 目录，热加载新增工作台 Skill，卸载已删除的 Skill，" +
		"对 Go 实现的 Skill 自动重新编译。agent 创建或修改 skill.yaml 后调用此工具使其生效。" +
		"返回变更摘要（新增 / 删除 / 重载数量）。"
}

// InputSchema returns an empty schema — skill_reload accepts no arguments.
func (t *ReloadTool) InputSchema() json.RawMessage {
	return tool.BuildSchema()
}

// Execute triggers the skill hot-reload and returns a change summary.
func (t *ReloadTool) Execute(ctx context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	summary := t.manager.Reload(ctx, t.registry)
	return tool.ToolResult{Output: summary}, nil
}

// Init is a no-op.
func (t *ReloadTool) Init(_ context.Context) error { return nil }

// Close is a no-op.
func (t *ReloadTool) Close() error { return nil }
