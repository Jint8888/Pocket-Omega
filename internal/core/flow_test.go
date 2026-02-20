package core_test

import (
	"context"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/core"
)

// ── stub node for testing ──

type stubState struct {
	visited []string
}

type stubBaseNode struct {
	name     string
	execErr  error
	action   core.Action
}

func (s *stubBaseNode) Prep(state *stubState) []string {
	state.visited = append(state.visited, s.name+":prep")
	return []string{"item"}
}

func (s *stubBaseNode) Exec(_ context.Context, _ string) (string, error) {
	return "result", s.execErr
}

func (s *stubBaseNode) Post(state *stubState, _ []string, _ ...string) core.Action {
	state.visited = append(state.visited, s.name+":post")
	return s.action
}

func (s *stubBaseNode) ExecFallback(_ error) string {
	return "fallback"
}

func newStubNode(name string, action core.Action) *core.Node[stubState, string, string] {
	return core.NewNode[stubState, string, string](&stubBaseNode{name: name, action: action}, 0)
}

// ── Flow tests ──

func TestFlow_RunSingleNode(t *testing.T) {
	state := &stubState{}
	n := newStubNode("A", core.ActionEnd)
	flow := core.NewFlow[stubState](n)

	action := flow.Run(context.Background(), state)

	if action != core.ActionEnd {
		t.Errorf("expected ActionEnd, got %q", action)
	}
	if len(state.visited) != 2 {
		t.Errorf("expected 2 visited phases, got %v", state.visited)
	}
}

func TestFlow_RunChainTwoNodes(t *testing.T) {
	state := &stubState{}
	a := newStubNode("A", core.ActionContinue)
	b := newStubNode("B", core.ActionEnd)
	a.AddSuccessor(b, core.ActionContinue)

	flow := core.NewFlow[stubState](a)
	action := flow.Run(context.Background(), state)

	if action != core.ActionEnd {
		t.Errorf("expected ActionEnd, got %q", action)
	}
	// A:prep, A:post, B:prep, B:post
	if len(state.visited) != 4 {
		t.Errorf("expected 4 visited phases, got %v", state.visited)
	}
}

func TestFlow_NilStartNode(t *testing.T) {
	state := &stubState{}
	flow := core.NewFlow[stubState](nil)
	action := flow.Run(context.Background(), state)

	if action != core.ActionFailure {
		t.Errorf("expected ActionFailure for nil start node, got %q", action)
	}
}

func TestFlow_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run

	state := &stubState{}
	n := newStubNode("A", core.ActionContinue)
	flow := core.NewFlow[stubState](n)
	action := flow.Run(ctx, state)

	if action != core.ActionFailure {
		t.Errorf("expected ActionFailure on cancelled context, got %q", action)
	}
}

func TestFlow_FlowLevelSuccessor(t *testing.T) {
	state := &stubState{}
	a := newStubNode("A", core.ActionContinue)
	b := newStubNode("B", core.ActionEnd)

	flow := core.NewFlow[stubState](a)
	flow.AddSuccessor(b, core.ActionContinue)

	action := flow.Run(context.Background(), state)

	if action != core.ActionEnd {
		t.Errorf("expected ActionEnd via flow-level successor, got %q", action)
	}
}

func TestFlow_NoSuccessor_StopsAfterFirstNode(t *testing.T) {
	state := &stubState{}
	a := newStubNode("A", core.ActionContinue) // no successor registered
	flow := core.NewFlow[stubState](a)

	action := flow.Run(context.Background(), state)

	// No successor → loop ends after A; last action is ActionContinue
	if action != core.ActionContinue {
		t.Errorf("expected ActionContinue (no successor stops loop), got %q", action)
	}
}

func TestFlow_DefaultSuccessor(t *testing.T) {
	state := &stubState{}
	a := newStubNode("A", core.ActionSuccess)
	b := newStubNode("B", core.ActionEnd)

	a.AddSuccessor(b) // no action arg → ActionDefault

	flow := core.NewFlow[stubState](a)
	action := flow.Run(context.Background(), state)

	// A returns ActionSuccess; default successor is not matched by ActionSuccess
	// so successor lookup returns nil and flow stops.
	if action != core.ActionSuccess {
		t.Errorf("expected ActionSuccess (ActionDefault != ActionSuccess), got %q", action)
	}
}
