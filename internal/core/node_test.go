package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/core"
)

// ── errBaseNode: simulates Exec failures for retry testing ──

type errState struct{ calls int }

type retryBaseNode struct {
	failUntil int // fail the first N Exec calls
	calls     int
}

func (r *retryBaseNode) Prep(_ *errState) []string   { return []string{"work"} }
func (r *retryBaseNode) Post(_ *errState, _ []string, results ...string) core.Action {
	if len(results) > 0 && results[0] == "fallback" {
		return core.ActionFailure
	}
	return core.ActionSuccess
}
func (r *retryBaseNode) ExecFallback(_ error) string { return "fallback" }
func (r *retryBaseNode) Exec(_ context.Context, _ string) (string, error) {
	r.calls++
	if r.calls <= r.failUntil {
		return "", errors.New("transient error")
	}
	return "ok", nil
}

// ── Node tests ──

func TestNode_Run_SucceedsFirstAttempt(t *testing.T) {
	state := &errState{}
	impl := &retryBaseNode{failUntil: 0}
	node := core.NewNode[errState, string, string](impl, 2)
	node.Run(context.Background(), state)

	if impl.calls != 1 {
		t.Errorf("expected 1 Exec call, got %d", impl.calls)
	}
}

func TestNode_Run_RetriesOnError(t *testing.T) {
	state := &errState{}
	impl := &retryBaseNode{failUntil: 2} // fail first 2, succeed on 3rd
	node := core.NewNode[errState, string, string](impl, 3)
	action := node.Run(context.Background(), state)

	if impl.calls != 3 {
		t.Errorf("expected 3 Exec calls, got %d", impl.calls)
	}
	if action != core.ActionSuccess {
		t.Errorf("expected ActionSuccess after retries, got %q", action)
	}
}

func TestNode_Run_FallbackAfterAllRetriesExhausted(t *testing.T) {
	state := &errState{}
	impl := &retryBaseNode{failUntil: 99} // always fail
	node := core.NewNode[errState, string, string](impl, 2)
	action := node.Run(context.Background(), state)

	// maxRetries=2 → 3 total attempts
	if impl.calls != 3 {
		t.Errorf("expected 3 Exec calls (1 + 2 retries), got %d", impl.calls)
	}
	if action != core.ActionFailure {
		t.Errorf("expected ActionFailure from fallback path, got %q", action)
	}
}

func TestNode_Run_ContextCancelledDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run — Node should abort early

	state := &errState{}
	innerImpl := &retryBaseNode{failUntil: 99}
	node := core.NewNode[errState, string, string](innerImpl, 5)

	// Should not panic and should stop early due to cancelled context
	node.Run(ctx, state)
}

func TestNode_AddSuccessor_Chaining(t *testing.T) {
	a := core.NewNode[errState, string, string](&retryBaseNode{failUntil: 0}, 0)
	b := core.NewNode[errState, string, string](&retryBaseNode{failUntil: 0}, 0)

	// AddSuccessor returns the successor for chaining
	returned := a.AddSuccessor(b, core.ActionSuccess)
	if returned != b {
		t.Error("AddSuccessor should return the added successor")
	}
}

func TestNode_GetSuccessor_UnknownAction(t *testing.T) {
	a := core.NewNode[errState, string, string](&retryBaseNode{failUntil: 0}, 0)
	result := a.GetSuccessor(core.ActionTool) // not registered
	if result != nil {
		t.Errorf("expected nil for unregistered action, got %v", result)
	}
}

func TestNewNode_NegativeRetriesClampedToZero(t *testing.T) {
	state := &errState{}
	impl := &retryBaseNode{failUntil: 99}
	node := core.NewNode[errState, string, string](impl, -5) // negative → clamped to 0
	node.Run(context.Background(), state)

	// Should only attempt once (0 retries)
	if impl.calls != 1 {
		t.Errorf("negative maxRetries should clamp to 0, got %d calls", impl.calls)
	}
}
