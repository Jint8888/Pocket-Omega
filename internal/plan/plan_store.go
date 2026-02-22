package plan

import "sync"

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
