package agent

import (
	"testing"
	"time"
)

func TestCostGuard_TokenBudget_Exceeded(t *testing.T) {
	g := NewCostGuard(100, 0) // 100 token limit, no duration limit
	if err := g.RecordTokens(60); err != nil {
		t.Fatalf("unexpected error at 60: %v", err)
	}
	if g.IsExceeded() {
		t.Error("should not be exceeded at 60/100")
	}
	if err := g.RecordTokens(50); err == nil {
		t.Error("expected error at 110/100")
	}
	if !g.IsExceeded() {
		t.Error("should be exceeded at 110/100")
	}
}

func TestCostGuard_TokenBudget_NotExceeded(t *testing.T) {
	g := NewCostGuard(200, 0)
	g.RecordTokens(50)
	g.RecordTokens(50)
	g.RecordTokens(50)
	if g.IsExceeded() {
		t.Error("should not be exceeded at 150/200")
	}
	if got := g.usedTokens.Load(); got != 150 {
		t.Errorf("expected 150 used tokens, got %d", got)
	}
}

func TestCostGuard_TokenBudget_Disabled(t *testing.T) {
	g := NewCostGuard(0, 0) // disabled
	for i := 0; i < 100; i++ {
		if err := g.RecordTokens(99999); err != nil {
			t.Fatalf("disabled guard should never error: %v", err)
		}
	}
	if g.IsExceeded() {
		t.Error("disabled guard should never be exceeded")
	}
}

func TestCostGuard_Duration_Exceeded(t *testing.T) {
	g := NewCostGuard(0, 50*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	if err := g.CheckDuration(); err == nil {
		t.Error("expected duration exceeded error")
	}
	if !g.IsExceeded() {
		t.Error("should be exceeded after timeout")
	}
}

func TestCostGuard_Duration_Disabled(t *testing.T) {
	g := NewCostGuard(0, 0) // disabled
	if err := g.CheckDuration(); err != nil {
		t.Fatalf("disabled guard should never error: %v", err)
	}
	if g.IsExceeded() {
		t.Error("disabled guard should never be exceeded")
	}
}

func TestCostGuard_IsExceeded_SetOnOverflow(t *testing.T) {
	g := NewCostGuard(10, 0)
	if g.IsExceeded() {
		t.Error("should start false")
	}
	if err := g.RecordTokens(20); err == nil {
		t.Error("expected error on overflow")
	}
	if !g.IsExceeded() {
		t.Error("should be true after overflow")
	}
}
