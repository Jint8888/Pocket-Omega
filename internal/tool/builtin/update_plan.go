package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		// Dedup: if the new plan is identical to the current plan, return a warning
		// instead of positive feedback. This prevents the LLM from getting stuck in
		// a loop of repeatedly setting the same plan.
		if current := t.store.Get(t.sessionID); plansEqual(current, a.Steps) {
			return tool.ToolResult{Output: "⚠️ 计划未变更（与当前计划相同）。请直接执行任务步骤，不要重复设置计划。"}, nil
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
		// Dedup: if step already has the requested status, return an ERROR
		// (not Output) to strongly signal the LLM to stop calling update_plan.
		// In FC mode, LLMs treat errors more seriously than successful outputs.
		// The error lists available action tools to give the LLM a clear next step.
		if current := t.findStepStatus(a.StepID); current == a.Status {
			return tool.ToolResult{Error: fmt.Sprintf(
				"步骤 %s 已是 %s 状态，禁止重复调用 update_plan。"+
					"请立即调用实际工具执行该步骤，例如: file_read, file_write, file_list, shell_exec, web_search, mcp_server_add。",
				a.StepID, a.Status)}, nil
		}
		if t.store.Update(t.sessionID, a.StepID, a.Status, a.Detail) {
			t.notifyUpdate()
			return tool.ToolResult{Output: fmt.Sprintf("✅ 步骤 %s → %s", a.StepID, a.Status)}, nil
		}
		// Exact match failed — try fuzzy matching (prefix/suffix)
		if corrected := t.fuzzyMatchStepID(a.StepID); corrected != "" {
			if t.store.Update(t.sessionID, corrected, a.Status, a.Detail) {
				t.notifyUpdate()
				return tool.ToolResult{Output: fmt.Sprintf("✅ 步骤 %s → %s（自动纠正: %q → %q）", corrected, a.Status, a.StepID, corrected)}, nil
			}
		}
		// No match — return error with valid IDs for self-correction
		ids := t.validStepIDs()
		return tool.ToolResult{Error: fmt.Sprintf("步骤 %q 不存在，当前计划的步骤 ID: [%s]", a.StepID, strings.Join(ids, ", "))}, nil

	default:
		return tool.ToolResult{Error: fmt.Sprintf("未知操作 %q，支持 set/update", a.Operation)}, nil
	}
}

func (t *UpdatePlanTool) notifyUpdate() {
	if t.onUpdate != nil {
		t.onUpdate(t.store.Get(t.sessionID))
	}
}

// fuzzyMatchStepID attempts prefix-based correction for mistyped step IDs.
// Returns the corrected ID if exactly one candidate matches, empty string otherwise.
// Examples: "check_conflict" → "check_conflicts", "create_srv" → "create_server".
func (t *UpdatePlanTool) fuzzyMatchStepID(input string) string {
	steps := t.store.Get(t.sessionID)
	if steps == nil {
		return ""
	}
	var candidates []string
	for _, s := range steps {
		// Either the input is a prefix of a valid ID, or a valid ID is a prefix of the input
		if strings.HasPrefix(s.ID, input) || strings.HasPrefix(input, s.ID) {
			candidates = append(candidates, s.ID)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// validStepIDs returns all step IDs in the current plan for error messages.
func (t *UpdatePlanTool) validStepIDs() []string {
	steps := t.store.Get(t.sessionID)
	ids := make([]string, len(steps))
	for i, s := range steps {
		ids[i] = s.ID
	}
	return ids
}

// plansEqual returns true if two plan step slices have the same IDs and titles
// (ignoring status/detail, which change during execution).
// Used to detect duplicate set operations where the LLM re-sends the same plan.
func plansEqual(a, b []plan.PlanStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Title != b[i].Title {
			return false
		}
	}
	return true
}

// findStepStatus returns the current status of a step by ID.
// Returns "" if the step or session is not found.
func (t *UpdatePlanTool) findStepStatus(stepID string) string {
	steps := t.store.Get(t.sessionID)
	for _, s := range steps {
		if s.ID == stepID {
			return s.Status
		}
	}
	return ""
}
