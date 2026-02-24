package plan

import (
	"fmt"
	"strings"
	"sync"
)

// PlanStep represents a single step in an agent execution plan.
type PlanStep struct {
	ID     string `json:"id"`               // Unique identifier, e.g. "step1", "read_config"
	Title  string `json:"title"`            // Step description
	Status string `json:"status"`           // "pending" | "in_progress" | "done" | "error" | "skipped"
	Detail string `json:"detail,omitempty"` // Optional detail/error message
}

// PlanStore manages execution plans per session.
// Thread-safe via sync.RWMutex.
type PlanStore struct {
	mu    sync.RWMutex
	plans map[string][]PlanStep // sessionID → steps
}

// NewPlanStore creates an empty plan store.
func NewPlanStore() *PlanStore {
	return &PlanStore{plans: make(map[string][]PlanStep)}
}

// Set replaces the entire plan for a session.
// Makes a defensive copy of the input slice (caller's data is never mutated).
func (ps *PlanStore) Set(sessionID string, steps []PlanStep) {
	cp := make([]PlanStep, len(steps))
	copy(cp, steps)
	for i := range cp {
		if cp[i].Status == "" {
			cp[i].Status = "pending"
		}
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.plans[sessionID] = cp
}

// Update changes the status of a single step by ID.
// Returns false if session or step not found.
func (ps *PlanStore) Update(sessionID, stepID, status, detail string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	steps, ok := ps.plans[sessionID]
	if !ok {
		return false
	}
	for i := range steps {
		if steps[i].ID == stepID {
			steps[i].Status = status
			if detail != "" {
				steps[i].Detail = detail
			}
			return true
		}
	}
	return false
}

// Get returns a copy of the current plan for a session.
// Returns nil if no plan exists.
func (ps *PlanStore) Get(sessionID string) []PlanStep {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	steps := ps.plans[sessionID]
	if steps == nil {
		return nil
	}
	cp := make([]PlanStep, len(steps))
	copy(cp, steps)
	return cp
}

// Delete removes the plan for a session (cleanup on session end).
func (ps *PlanStore) Delete(sessionID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.plans, sessionID)
}

// statusIcons maps plan step status to a visual marker for prompt rendering.
var statusIcons = map[string]string{
	"pending":     "[ ]",
	"in_progress": "[→]",
	"done":        "[x]",
	"error":       "[!]",
	"skipped":     "[-]",
}

// Render formats the current plan as a markdown checklist for prompt injection.
// Returns "" if no plan exists for the session.
// Appends a status signal with progress and next-step hint to prevent the LLM
// from re-setting an already-existing plan.
func (ps *PlanStore) Render(sessionID string) string {
	steps := ps.Get(sessionID) // uses defensive copy
	if len(steps) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 执行计划\n")

	done, total := 0, len(steps)
	var nextPending string

	for _, s := range steps {
		icon := statusIcons[s.Status]
		if icon == "" {
			icon = "[ ]"
		}
		sb.WriteString(fmt.Sprintf("- %s %s: %s\n", icon, s.ID, s.Title))
		if s.Status == "done" {
			done++
		}
		if nextPending == "" && (s.Status == "pending" || s.Status == "in_progress") {
			nextPending = s.ID
		}
	}

	// Status signal — prevents LLM from re-setting plan or looping on update_plan
	sb.WriteString(fmt.Sprintf("\n> ⚡ 计划已设置（%d/%d 完成）。", done, total))
	if nextPending != "" {
		sb.WriteString(fmt.Sprintf("下一步：用实际工具执行 %s（不是 update_plan）。", nextPending))
	}
	sb.WriteString("\n")

	return sb.String()
}
