package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pocketomega/pocket-omega/internal/plan"
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// UpdatePlanTool manages structured execution plans for agent tasks.
// Each request gets its own instance (via NewUpdatePlanTool) to avoid data races
// on the sessionID and callback fields.
type UpdatePlanTool struct {
	store     *plan.PlanStore
	sessionID string
	onUpdate  func(steps []plan.PlanStep)
}

// NewUpdatePlanTool creates a per-request instance with session context and SSE callback.
func NewUpdatePlanTool(store *plan.PlanStore, sessionID string, onUpdate func([]plan.PlanStep)) *UpdatePlanTool {
	return &UpdatePlanTool{store: store, sessionID: sessionID, onUpdate: onUpdate}
}

func (t *UpdatePlanTool) Name() string { return "update_plan" }
func (t *UpdatePlanTool) Description() string {
	return "管理任务执行计划。set：设置完整计划；update：更新单步状态。多步任务(≥3步)应先 set 计划再执行"
}

// InputSchema returns hand-crafted JSON Schema because BuildSchema doesn't support
// array types with item definitions needed for the steps parameter.
func (t *UpdatePlanTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"operation": {
				"type": "string",
				"enum": ["set", "update"],
				"description": "操作类型：set 设置完整计划，update 更新单步状态"
			},
			"steps": {
				"type": "array",
				"description": "步骤列表（operation=set 时必须）",
				"items": {
					"type": "object",
					"properties": {
						"id":    {"type": "string", "description": "步骤唯一 ID"},
						"title": {"type": "string", "description": "步骤描述"}
					},
					"required": ["id", "title"]
				}
			},
			"step_id": {"type": "string", "description": "步骤 ID（operation=update 时必须）"},
			"status":  {"type": "string", "enum": ["pending","in_progress","done","error","skipped"], "description": "新状态（operation=update 时必须）"},
			"detail":  {"type": "string", "description": "可选备注/错误信息"}
		},
		"required": ["operation"]
	}`)
}

func (t *UpdatePlanTool) Init(_ context.Context) error { return nil }
func (t *UpdatePlanTool) Close() error                 { return nil }

// validStatuses mirrors the JSON Schema enum for runtime validation.
// LLMs may hallucinate invalid status values (e.g. "completed" instead of "done").
var validStatuses = map[string]bool{
	"pending": true, "in_progress": true, "done": true,
	"error": true, "skipped": true,
}

type updatePlanArgs struct {
	Operation string          `json:"operation"`
	Steps     []plan.PlanStep `json:"steps"`
	StepID    string          `json:"step_id"`
	Status    string          `json:"status"`
	Detail    string          `json:"detail"`
}

func (t *UpdatePlanTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a updatePlanArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	switch a.Operation {
	case "set":
		if len(a.Steps) == 0 {
			return tool.ToolResult{Error: "set 操作需要非空 steps 列表"}, nil
		}
		t.store.Set(t.sessionID, a.Steps)
		t.notifyUpdate()
		return tool.ToolResult{Output: fmt.Sprintf("✅ 计划已设置，共 %d 步", len(a.Steps))}, nil

	case "update":
		if a.StepID == "" || a.Status == "" {
			return tool.ToolResult{Error: "update 操作需要 step_id 和 status"}, nil
		}
		if !validStatuses[a.Status] {
			return tool.ToolResult{Error: fmt.Sprintf("无效状态 %q，支持: pending/in_progress/done/error/skipped", a.Status)}, nil
		}
		if !t.store.Update(t.sessionID, a.StepID, a.Status, a.Detail) {
			return tool.ToolResult{Error: fmt.Sprintf("步骤 %q 不存在", a.StepID)}, nil
		}
		t.notifyUpdate()
		return tool.ToolResult{Output: fmt.Sprintf("✅ 步骤 %s → %s", a.StepID, a.Status)}, nil

	default:
		return tool.ToolResult{Error: fmt.Sprintf("未知操作 %q，支持 set/update", a.Operation)}, nil
	}
}

func (t *UpdatePlanTool) notifyUpdate() {
	if t.onUpdate != nil {
		t.onUpdate(t.store.Get(t.sessionID))
	}
}
