package session

import (
	"testing"
	"time"
)

func TestNewStore_EmptyHistory(t *testing.T) {
	s := NewStore(time.Minute, 10)
	history := s.GetHistory("new-session")
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

	history := s.GetHistory(id)
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

	history := s.GetHistory(id)
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
	got := s.GetHistory("nonexistent-id-xyz")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestDelete_Session(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "to-delete"
	s.AppendTurn(id, Turn{UserMsg: "q", Assistant: "a"}) // auto-creates

	s.Delete(id)

	got := s.GetHistory(id)
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

	got := s.GetHistory(id)
	if got != nil {
		t.Errorf("expected nil after TTL eviction, got %v", got)
	}
}

func TestAppendTurn_AutoCreate(t *testing.T) {
	s := NewStore(time.Minute, 10)
	id := "auto-create-session"
	// No GetOrCreate call — AppendTurn must create the session automatically
	s.AppendTurn(id, Turn{UserMsg: "x", Assistant: "y"})
	got := s.GetHistory(id)
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
