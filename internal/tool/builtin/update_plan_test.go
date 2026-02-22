package builtin

import (
	"context"
	"encoding/json"
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
