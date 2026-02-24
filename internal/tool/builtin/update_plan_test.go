package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/plan"
)

func newTestPlanTool() (*UpdatePlanTool, *plan.PlanStore, *[][]plan.PlanStep) {
	store := plan.NewPlanStore()
	var callbacks [][]plan.PlanStep
	tool := NewUpdatePlanTool(store, "test-session", func(steps []plan.PlanStep) {
		callbacks = append(callbacks, steps)
	})
	return tool, store, &callbacks
}

func TestUpdatePlan_SetOperation(t *testing.T) {
	pt, store, _ := newTestPlanTool()
	args := `{"operation":"set","steps":[{"id":"s1","title":"Step One"},{"id":"s2","title":"Step Two"}]}`
	result, err := pt.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	steps := store.Get("test-session")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps stored, got %d", len(steps))
	}
	if steps[0].Status != "pending" {
		t.Errorf("expected default pending status, got %q", steps[0].Status)
	}
}

func TestUpdatePlan_UpdateOperation(t *testing.T) {
	pt, store, _ := newTestPlanTool()
	// Set plan first
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[{"id":"s1","title":"Step"}]}`))

	// Update step
	result, err := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"done","detail":"completed"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	steps := store.Get("test-session")
	if steps[0].Status != "done" || steps[0].Detail != "completed" {
		t.Errorf("unexpected step after update: %+v", steps[0])
	}
}

func TestUpdatePlan_InvalidOperation(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	result, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"delete"}`))
	if result.Error == "" {
		t.Error("expected error for invalid operation")
	}
}

func TestUpdatePlan_InvalidStatus(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[{"id":"s1","title":"Step"}]}`))

	result, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"completed"}`))
	if result.Error == "" {
		t.Error("expected error for invalid status 'completed'")
	}
}

func TestUpdatePlan_SetEmptySteps(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	result, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[]}`))
	if result.Error == "" {
		t.Error("expected error for empty steps")
	}
}

func TestUpdatePlan_UpdateMissingFields(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	result, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"","status":"done"}`))
	if result.Error == "" {
		t.Error("expected error for empty step_id")
	}
	result, _ = pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":""}`))
	if result.Error == "" {
		t.Error("expected error for empty status")
	}
}

func TestUpdatePlan_UpdateNonexistentStep(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[{"id":"s1","title":"Step"}]}`))

	result, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"ghost","status":"done"}`))
	if result.Error == "" {
		t.Error("expected error for non-existent step")
	}
	// Error message should include valid step IDs for self-correction
	if !strings.Contains(result.Error, "s1") {
		t.Errorf("error should list valid step IDs, got: %s", result.Error)
	}
}

func TestUpdatePlan_FuzzyMatchPrefix(t *testing.T) {
	pt, store, _ := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[
		{"id":"check_conflicts","title":"Check conflicts"},
		{"id":"create_server","title":"Create server"}
	]}`))

	// "check_conflict" (missing 's') should fuzzy-match to "check_conflicts"
	result, err := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"check_conflict","status":"done"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("expected fuzzy match to succeed, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "自动纠正") {
		t.Errorf("expected auto-correction note in output, got: %s", result.Output)
	}

	steps := store.Get("test-session")
	for _, s := range steps {
		if s.ID == "check_conflicts" && s.Status != "done" {
			t.Errorf("expected check_conflicts to be done, got %q", s.Status)
		}
	}
}

func TestUpdatePlan_FuzzyMatchAmbiguous(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[
		{"id":"create_server","title":"Create server"},
		{"id":"create_service","title":"Create service"}
	]}`))

	// "create_serv" is a prefix of both — ambiguous, should fail
	result, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"create_serv","status":"done"}`))
	if result.Error == "" {
		t.Error("expected error for ambiguous fuzzy match")
	}
	// Should list valid IDs
	if !strings.Contains(result.Error, "create_server") || !strings.Contains(result.Error, "create_service") {
		t.Errorf("error should list valid step IDs, got: %s", result.Error)
	}
}

func TestUpdatePlan_SSECallback(t *testing.T) {
	pt, _, callbacks := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[{"id":"s1","title":"Step"}]}`))

	if len(*callbacks) != 1 {
		t.Fatalf("expected 1 callback after set, got %d", len(*callbacks))
	}
	if len((*callbacks)[0]) != 1 || (*callbacks)[0][0].ID != "s1" {
		t.Errorf("callback received wrong data: %v", (*callbacks)[0])
	}

	pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"done"}`))
	if len(*callbacks) != 2 {
		t.Fatalf("expected 2 callbacks total, got %d", len(*callbacks))
	}
}

func TestUpdatePlan_SetDedupIdenticalPlan(t *testing.T) {
	pt, _, callbacks := newTestPlanTool()
	args := `{"operation":"set","steps":[{"id":"s1","title":"Step One"},{"id":"s2","title":"Step Two"}]}`

	// First set — should succeed normally
	r1, _ := pt.Execute(context.Background(), json.RawMessage(args))
	if !strings.Contains(r1.Output, "✅") {
		t.Fatalf("first set should succeed, got: %s", r1.Output)
	}

	// Second set with identical plan — should return dedup warning
	r2, _ := pt.Execute(context.Background(), json.RawMessage(args))
	if !strings.Contains(r2.Output, "⚠️") {
		t.Fatalf("duplicate set should return warning, got: %s", r2.Output)
	}
	if !strings.Contains(r2.Output, "计划未变更") {
		t.Errorf("warning should mention plan unchanged, got: %s", r2.Output)
	}

	// Only 1 callback (first set), dedup should NOT trigger callback
	if len(*callbacks) != 1 {
		t.Errorf("expected 1 callback (dedup should skip), got %d", len(*callbacks))
	}
}

func TestUpdatePlan_SetDifferentPlanAllowed(t *testing.T) {
	pt, store, _ := newTestPlanTool()
	args1 := `{"operation":"set","steps":[{"id":"s1","title":"Step One"}]}`
	args2 := `{"operation":"set","steps":[{"id":"s1","title":"Step One"},{"id":"s2","title":"Step Two"}]}`

	pt.Execute(context.Background(), json.RawMessage(args1))
	r2, _ := pt.Execute(context.Background(), json.RawMessage(args2))

	if !strings.Contains(r2.Output, "✅") {
		t.Fatalf("different plan should succeed, got: %s", r2.Output)
	}
	steps := store.Get("test-session")
	if len(steps) != 2 {
		t.Errorf("expected 2 steps after second set, got %d", len(steps))
	}
}

func TestUpdatePlan_UpdateDedupSameStatus(t *testing.T) {
	pt, _, _ := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[{"id":"s1","title":"Step"}]}`))

	// First update to in_progress — should succeed
	r1, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"in_progress"}`))
	if !strings.Contains(r1.Output, "✅") {
		t.Fatalf("first update should succeed, got: %s", r1.Output)
	}

	// Second update with same status — should return dedup ERROR (not Output)
	r2, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"in_progress"}`))
	if r2.Error == "" {
		t.Fatalf("duplicate update should return error, got output: %s", r2.Output)
	}
	if !strings.Contains(r2.Error, "禁止重复调用") {
		t.Errorf("error should say '禁止重复调用', got: %s", r2.Error)
	}
	if !strings.Contains(r2.Error, "file_read") {
		t.Errorf("error should list actual tool names, got: %s", r2.Error)
	}
}

func TestUpdatePlan_UpdateDifferentStatusAllowed(t *testing.T) {
	pt, store, _ := newTestPlanTool()
	pt.Execute(context.Background(), json.RawMessage(`{"operation":"set","steps":[{"id":"s1","title":"Step"}]}`))

	pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"in_progress"}`))
	r2, _ := pt.Execute(context.Background(), json.RawMessage(`{"operation":"update","step_id":"s1","status":"done"}`))

	if !strings.Contains(r2.Output, "✅") {
		t.Fatalf("different status update should succeed, got: %s", r2.Output)
	}
	steps := store.Get("test-session")
	if steps[0].Status != "done" {
		t.Errorf("expected done, got %q", steps[0].Status)
	}
}
