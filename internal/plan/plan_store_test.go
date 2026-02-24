package plan

import (
	"strings"
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

func TestPlanStore_Render(t *testing.T) {
	ps := NewPlanStore()
	ps.Set("sess1", []PlanStep{
		{ID: "read_env", Title: "读取环境变量", Status: "done"},
		{ID: "create_server", Title: "创建 MCP Server", Status: "in_progress"},
		{ID: "install_deps", Title: "安装依赖"},
		{ID: "verify", Title: "验证功能", Status: "error"},
		{ID: "cleanup", Title: "清理临时文件", Status: "skipped"},
	})

	rendered := ps.Render("sess1")

	// Header
	if !strings.Contains(rendered, "## 执行计划") {
		t.Error("missing header")
	}
	// Status icons
	if !strings.Contains(rendered, "[x] read_env") {
		t.Error("missing done icon [x]")
	}
	if !strings.Contains(rendered, "[→] create_server") {
		t.Error("missing in_progress icon [→]")
	}
	if !strings.Contains(rendered, "[ ] install_deps") {
		t.Error("missing pending icon [ ]")
	}
	if !strings.Contains(rendered, "[!] verify") {
		t.Error("missing error icon [!]")
	}
	if !strings.Contains(rendered, "[-] cleanup") {
		t.Error("missing skipped icon [-]")
	}
	// Title preserved
	if !strings.Contains(rendered, "读取环境变量") {
		t.Error("missing step title")
	}
	// Status signal
	if !strings.Contains(rendered, "计划已设置") {
		t.Error("missing status signal")
	}
	if !strings.Contains(rendered, "1/5 完成") {
		t.Errorf("wrong progress count, got: %s", rendered)
	}
}

func TestPlanStore_RenderStatusSignal(t *testing.T) {
	ps := NewPlanStore()

	t.Run("next step points to first pending", func(t *testing.T) {
		ps.Set("s1", []PlanStep{
			{ID: "done_step", Title: "Done", Status: "done"},
			{ID: "next_step", Title: "Next", Status: "pending"},
			{ID: "later_step", Title: "Later", Status: "pending"},
		})
		rendered := ps.Render("s1")
		if !strings.Contains(rendered, "用实际工具执行 next_step") {
			t.Errorf("should point to first pending step, got: %s", rendered)
		}
		if !strings.Contains(rendered, "1/3 完成") {
			t.Errorf("wrong progress, got: %s", rendered)
		}
	})

	t.Run("next step points to in_progress over pending", func(t *testing.T) {
		ps.Set("s2", []PlanStep{
			{ID: "active", Title: "Active", Status: "in_progress"},
			{ID: "waiting", Title: "Waiting", Status: "pending"},
		})
		rendered := ps.Render("s2")
		if !strings.Contains(rendered, "用实际工具执行 active") {
			t.Errorf("should point to in_progress step, got: %s", rendered)
		}
	})

	t.Run("all done omits next step", func(t *testing.T) {
		ps.Set("s3", []PlanStep{
			{ID: "a", Title: "A", Status: "done"},
			{ID: "b", Title: "B", Status: "done"},
		})
		rendered := ps.Render("s3")
		if strings.Contains(rendered, "下一步") {
			t.Errorf("all done should not have next step hint, got: %s", rendered)
		}
		if !strings.Contains(rendered, "2/2 完成") {
			t.Errorf("wrong progress, got: %s", rendered)
		}
	})

	t.Run("contains anti-repeat warning", func(t *testing.T) {
		ps.Set("s4", []PlanStep{
			{ID: "x", Title: "X", Status: "pending"},
		})
		rendered := ps.Render("s4")
		if !strings.Contains(rendered, "不是 update_plan") {
			t.Errorf("missing anti-repeat warning, got: %s", rendered)
		}
	})
}

func TestPlanStore_RenderEmpty(t *testing.T) {
	ps := NewPlanStore()
	if got := ps.Render("nonexistent"); got != "" {
		t.Errorf("expected empty string for no plan, got %q", got)
	}
}

func TestPlanStore_RenderUnknownStatus(t *testing.T) {
	ps := NewPlanStore()
	// Directly set a step with an unknown status via internal manipulation
	ps.Set("sess1", []PlanStep{
		{ID: "s1", Title: "Unknown status step", Status: "unknown_status"},
	})

	rendered := ps.Render("sess1")
	// Unknown status should fall back to [ ]
	if !strings.Contains(rendered, "[ ] s1") {
		t.Errorf("expected fallback [ ] for unknown status, got: %s", rendered)
	}
}
