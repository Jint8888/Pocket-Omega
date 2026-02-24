package walkthrough

import (
	"strings"
	"sync"
	"testing"
)

func TestStore_AppendAndGet(t *testing.T) {
	s := NewStore()
	s.Append("s1", Entry{StepNumber: 1, Source: SourceAuto, Content: "found config"})
	s.Append("s1", Entry{StepNumber: 2, Source: SourceAuto, Content: "read file"})
	entries := s.Get("s1")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Content != "found config" || entries[1].Content != "read file" {
		t.Errorf("unexpected content: %+v", entries)
	}
}

func TestStore_DefensiveCopy(t *testing.T) {
	s := NewStore()
	s.Append("s1", Entry{StepNumber: 1, Source: SourceAuto, Content: "original"})
	got := s.Get("s1")
	got[0].Content = "mutated"
	again := s.Get("s1")
	if again[0].Content != "original" {
		t.Errorf("defensive copy failed: internal data was mutated to %q", again[0].Content)
	}
}

func TestStore_SessionIsolation(t *testing.T) {
	s := NewStore()
	s.Append("s1", Entry{Content: "session1"})
	s.Append("s2", Entry{Content: "session2"})
	if len(s.Get("s1")) != 1 || s.Get("s1")[0].Content != "session1" {
		t.Error("session isolation failed for s1")
	}
	if len(s.Get("s2")) != 1 || s.Get("s2")[0].Content != "session2" {
		t.Error("session isolation failed for s2")
	}
}

func TestStore_Delete(t *testing.T) {
	s := NewStore()
	s.Append("s1", Entry{Content: "data"})
	s.Delete("s1")
	if got := s.Get("s1"); got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestStore_FIFOEviction(t *testing.T) {
	s := NewStore()
	for i := 1; i <= MaxEntries+5; i++ {
		s.Append("s1", Entry{StepNumber: i, Source: SourceAuto, Content: "entry"})
	}
	entries := s.Get("s1")
	if len(entries) != MaxEntries {
		t.Fatalf("expected %d entries after eviction, got %d", MaxEntries, len(entries))
	}
	// Oldest should be evicted: first entry should be step 6
	if entries[0].StepNumber != 6 {
		t.Errorf("expected first entry step 6, got %d", entries[0].StepNumber)
	}
}

func TestStore_ManualProtection(t *testing.T) {
	s := NewStore()
	// Add manual entries first
	for i := 1; i <= 5; i++ {
		s.Append("s1", Entry{StepNumber: i, Source: SourceManual, Content: "manual"})
	}
	// Fill remaining with auto entries
	for i := 6; i <= MaxEntries; i++ {
		s.Append("s1", Entry{StepNumber: i, Source: SourceAuto, Content: "auto"})
	}
	// Add one more — should evict oldest auto (step 6), not manual
	s.Append("s1", Entry{StepNumber: 99, Source: SourceAuto, Content: "new"})

	entries := s.Get("s1")
	if len(entries) != MaxEntries {
		t.Fatalf("expected %d entries, got %d", MaxEntries, len(entries))
	}
	// All 5 manual should survive
	manualCount := 0
	for _, e := range entries {
		if e.Source == SourceManual {
			manualCount++
		}
	}
	if manualCount != 5 {
		t.Errorf("expected 5 manual entries to survive, got %d", manualCount)
	}
	// Step 6 (first auto) should be evicted
	for _, e := range entries {
		if e.StepNumber == 6 {
			t.Error("step 6 should have been evicted but was found")
		}
	}
}

func TestStore_AllManualEviction(t *testing.T) {
	s := NewStore()
	for i := 1; i <= MaxEntries; i++ {
		s.Append("s1", Entry{StepNumber: i, Source: SourceManual, Content: "manual"})
	}
	s.Append("s1", Entry{StepNumber: 99, Source: SourceManual, Content: "newest"})

	entries := s.Get("s1")
	if len(entries) != MaxEntries {
		t.Fatalf("expected %d entries, got %d", MaxEntries, len(entries))
	}
	// Oldest (step 1) should be evicted
	if entries[0].StepNumber != 2 {
		t.Errorf("expected oldest manual (step 1) evicted, first entry is step %d", entries[0].StepNumber)
	}
}

func TestStore_Render(t *testing.T) {
	s := NewStore()
	s.Append("s1", Entry{StepNumber: 6, Source: SourceAuto, Content: "shell_exec: found 28 files"})
	s.Append("s1", Entry{Source: SourceManual, Content: "WORKSPACE_DIR=/home/user"})

	rendered := s.Render("s1")
	if !strings.Contains(rendered, "## 备忘录") {
		t.Error("missing header")
	}
	if !strings.Contains(rendered, "[步骤6]") {
		t.Error("missing auto entry step number")
	}
	if !strings.Contains(rendered, "📌 WORKSPACE_DIR") {
		t.Error("missing pinned marker")
	}
	// Manual entries should NOT have step number
	if strings.Contains(rendered, "[步骤0]") {
		t.Error("manual entry should not have step number")
	}
}

func TestStore_RenderEmpty(t *testing.T) {
	s := NewStore()
	if got := s.Render("nonexistent"); got != "" {
		t.Errorf("expected empty string for empty store, got %q", got)
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.Append("s1", Entry{StepNumber: n, Source: SourceAuto, Content: "concurrent"})
			s.Get("s1")
			s.Render("s1")
		}(i)
	}
	wg.Wait()
	entries := s.Get("s1")
	if len(entries) > MaxEntries {
		t.Errorf("exceeded max entries: %d", len(entries))
	}
}
