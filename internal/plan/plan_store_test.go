package plan

import (
	"sync"
	"testing"
)

func TestPlanStore_SetAndGet(t *testing.T) {
	ps := NewPlanStore()
	steps := []PlanStep{
		{ID: "s1", Title: "Step 1", Status: "pending"},
		{ID: "s2", Title: "Step 2", Status: "in_progress"},
	}
	ps.Set("sess1", steps)

	got := ps.Get("sess1")
	if len(got) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(got))
	}
	if got[0].ID != "s1" || got[1].ID != "s2" {
		t.Errorf("unexpected step IDs: %v", got)
	}
}

func TestPlanStore_Update(t *testing.T) {
	ps := NewPlanStore()
	ps.Set("sess1", []PlanStep{{ID: "s1", Title: "Step 1"}})

	if !ps.Update("sess1", "s1", "done", "completed ok") {
		t.Fatal("Update should return true for existing step")
	}
	got := ps.Get("sess1")
	if got[0].Status != "done" || got[0].Detail != "completed ok" {
		t.Errorf("unexpected after update: %+v", got[0])
	}

	// Non-existent step
	if ps.Update("sess1", "ghost", "done", "") {
		t.Fatal("Update should return false for non-existent step")
	}

	// Non-existent session
	if ps.Update("no_session", "s1", "done", "") {
		t.Fatal("Update should return false for non-existent session")
	}
}

func TestPlanStore_DefaultPending(t *testing.T) {
	ps := NewPlanStore()
	ps.Set("sess1", []PlanStep{
		{ID: "s1", Title: "No status set"},
		{ID: "s2", Title: "Has status", Status: "in_progress"},
	})
	got := ps.Get("sess1")
	if got[0].Status != "pending" {
		t.Errorf("expected pending for empty status, got %q", got[0].Status)
	}
	if got[1].Status != "in_progress" {
		t.Errorf("expected in_progress preserved, got %q", got[1].Status)
	}
}

func TestPlanStore_SessionIsolation(t *testing.T) {
	ps := NewPlanStore()
	ps.Set("a", []PlanStep{{ID: "1", Title: "A step"}})
	ps.Set("b", []PlanStep{{ID: "2", Title: "B step"}})

	a := ps.Get("a")
	b := ps.Get("b")
	if len(a) != 1 || a[0].ID != "1" {
		t.Errorf("session a contaminated: %v", a)
	}
	if len(b) != 1 || b[0].ID != "2" {
		t.Errorf("session b contaminated: %v", b)
	}
}

func TestPlanStore_SetDefensiveCopy(t *testing.T) {
	ps := NewPlanStore()
	original := []PlanStep{{ID: "s1", Title: "Original"}}
	ps.Set("sess1", original)

	// Mutate the original slice after Set
	original[0].Title = "MUTATED"

	got := ps.Get("sess1")
	if got[0].Title != "Original" {
		t.Errorf("Set should defensively copy; Got title=%q, want 'Original'", got[0].Title)
	}
}

func TestPlanStore_DeleteCleansUp(t *testing.T) {
	ps := NewPlanStore()
	ps.Set("sess1", []PlanStep{{ID: "1", Title: "step"}})
	ps.Delete("sess1")
	if got := ps.Get("sess1"); got != nil {
		t.Errorf("after Delete, Get should return nil, got %v", got)
	}
}

func TestPlanStore_ConcurrentAccess(t *testing.T) {
	ps := NewPlanStore()
	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sid := "sess"
			ps.Set(sid, []PlanStep{{ID: "s1", Title: "step"}})
			ps.Update(sid, "s1", "done", "")
			ps.Get(sid)
		}(i)
	}
	wg.Wait()

	// If we reach here without -race detector panic, mutex is working
}
