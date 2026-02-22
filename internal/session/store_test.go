package session

import (
	"testing"
	"time"
)

func TestNewStore_EmptyHistory(t *testing.T) {
	s := NewStore(time.Minute, 10)
	history, _ := s.GetSessionContext("new-session")
	if history != nil {
		t.Errorf("expected nil for unknown session, got %v", history)
	}
}

func TestAppendTurn_Basic(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "test-basic"

	// AppendTurn auto-creates the session on first write
	turn := Turn{UserMsg: "hello", Assistant: "hi", IsAgent: false}
	s.AppendTurn(id, turn)

	history, _ := s.GetSessionContext(id)
	if len(history) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(history))
	}
	if history[0].UserMsg != "hello" || history[0].Assistant != "hi" {
		t.Errorf("unexpected turn: %+v", history[0])
	}
}

func TestAppendTurn_MaxTurns(t *testing.T) {
	const max = 3
	s := NewStore(time.Minute, max)
	id := "test-max"

	// AppendTurn auto-creates session; append max+2 turns, only last max should remain
	for i := 0; i < max+2; i++ {
		s.AppendTurn(id, Turn{
			UserMsg:   string(rune('A' + i)),
			Assistant: string(rune('a' + i)),
		})
	}

	history, _ := s.GetSessionContext(id)
	if len(history) != max {
		t.Fatalf("expected %d turns after trim, got %d", max, len(history))
	}
	// The oldest 2 turns (A,B) should have been evicted; remaining: C,D,E
	if history[0].UserMsg != "C" {
		t.Errorf("expected first retained turn to be 'C', got %q", history[0].UserMsg)
	}
}

func TestGetHistory_UnknownSession(t *testing.T) {
	s := NewStore(time.Minute, 10)
	// Must not panic and must return nil
	got, _ := s.GetSessionContext("nonexistent-id-xyz")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestDelete_Session(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "to-delete"
	s.AppendTurn(id, Turn{UserMsg: "q", Assistant: "a"}) // auto-creates

	s.Delete(id)

	got, _ := s.GetSessionContext(id)
	if got != nil {
		t.Errorf("expected nil after delete, got %v", got)
	}
}

func TestCleanup_TTLEviction(t *testing.T) {
	// Use a very short TTL so eviction triggers quickly
	ttl := 50 * time.Millisecond
	s := NewStore(ttl, 10)
	id := "evict-me"
	s.AppendTurn(id, Turn{UserMsg: "old", Assistant: "old"})

	// Wait for TTL + cleanup interval to pass
	time.Sleep(ttl * 3)

	got, _ := s.GetSessionContext(id)
	if got != nil {
		t.Errorf("expected nil after TTL eviction, got %v", got)
	}
}

func TestAppendTurn_AutoCreate(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "auto-create-session"
	// No GetOrCreate call — AppendTurn must create the session automatically
	s.AppendTurn(id, Turn{UserMsg: "x", Assistant: "y"})
	got, _ := s.GetSessionContext(id)
	if len(got) != 1 || got[0].UserMsg != "x" {
		t.Errorf("expected auto-created session to have 1 turn, got %v", got)
	}
}

func TestClose_Idempotent(t *testing.T) {
	s := NewStore(time.Minute, 10)
	// Multiple Close() calls must not panic
	s.Close()
	s.Close()
	s.Close()
}

// ── Compact tests ──

func TestCompact_Basic(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "compact-basic"
	for i := 0; i < 8; i++ {
		s.AppendTurn(id, Turn{
			UserMsg:   string(rune('A' + i)),
			Assistant: string(rune('a' + i)),
		})
	}

	compacted := s.Compact(id, "summary of 6 old turns", 2)
	if compacted != 6 {
		t.Errorf("expected 6 compacted turns, got %d", compacted)
	}

	turns, summary := s.GetSessionContext(id)
	if len(turns) != 2 {
		t.Fatalf("expected 2 remaining turns, got %d", len(turns))
	}
	// The kept turns should be the newest (G,H)
	if turns[0].UserMsg != "G" || turns[1].UserMsg != "H" {
		t.Errorf("unexpected kept turns: %+v", turns)
	}
	if summary != "summary of 6 old turns" {
		t.Errorf("unexpected summary: %q", summary)
	}
}

func TestCompact_TooFew(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "compact-few"
	s.AppendTurn(id, Turn{UserMsg: "a", Assistant: "b"})
	s.AppendTurn(id, Turn{UserMsg: "c", Assistant: "d"})

	compacted := s.Compact(id, "should not matter", 2)
	if compacted != 0 {
		t.Errorf("expected 0 compacted (too few turns), got %d", compacted)
	}

	turns, summary := s.GetSessionContext(id)
	if len(turns) != 2 {
		t.Errorf("expected 2 turns unchanged, got %d", len(turns))
	}
	if summary != "" {
		t.Errorf("summary should be empty when no compaction, got %q", summary)
	}
}

func TestCompact_OverwritesSummary(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "compact-overwrite"
	for i := 0; i < 6; i++ {
		s.AppendTurn(id, Turn{UserMsg: "q", Assistant: "a"})
	}
	s.Compact(id, "first summary", 2)
	// Add more turns, then compact again
	for i := 0; i < 4; i++ {
		s.AppendTurn(id, Turn{UserMsg: "new", Assistant: "ans"})
	}
	s.Compact(id, "merged summary v2", 2)

	_, summary := s.GetSessionContext(id)
	if summary != "merged summary v2" {
		t.Errorf("expected overwritten summary, got %q", summary)
	}
}

func TestGetSessionContext_Atomic(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "ctx-atomic"
	s.AppendTurn(id, Turn{UserMsg: "q1", Assistant: "a1"})
	s.AppendTurn(id, Turn{UserMsg: "q2", Assistant: "a2"})
	s.AppendTurn(id, Turn{UserMsg: "q3", Assistant: "a3"})
	s.Compact(id, "compact summary", 1)

	turns, summary := s.GetSessionContext(id)
	if len(turns) != 1 || turns[0].UserMsg != "q3" {
		t.Errorf("unexpected turns: %+v", turns)
	}
	if summary != "compact summary" {
		t.Errorf("unexpected summary: %q", summary)
	}
}

func TestGetSessionContext_Unknown(t *testing.T) {
	s := NewStore(time.Minute, 10)
	turns, summary := s.GetSessionContext("nonexistent")
	if turns != nil {
		t.Errorf("expected nil turns, got %v", turns)
	}
	if summary != "" {
		t.Errorf("expected empty summary, got %q", summary)
	}
}
