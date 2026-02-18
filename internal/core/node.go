package core

import (
	"context"
	"log"
)

// Node wraps a BaseNode implementation with retry logic and successor routing.
// It implements the Workflow interface.
type Node[State any, PrepResult any, ExecResults any] struct {
	node       BaseNode[State, PrepResult, ExecResults]
	maxRetries int
	successors map[Action]Workflow[State]
}

// NewNode creates a new Node wrapping the given BaseNode implementation.
func NewNode[State any, PrepResult any, ExecResults any](
	basenode BaseNode[State, PrepResult, ExecResults],
	maxRetries int,
) *Node[State, PrepResult, ExecResults] {
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &Node[State, PrepResult, ExecResults]{
		node:       basenode,
		maxRetries: maxRetries,
		successors: make(map[Action]Workflow[State]),
	}
}

// executeWithRetry runs Exec with retry logic.
func (n *Node[State, PrepResult, ExecResults]) executeWithRetry(ctx context.Context, input PrepResult) (ExecResults, error) {
	var result ExecResults
	var err error

	for i := 0; i <= n.maxRetries; i++ {
		// Check context cancellation before each attempt
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result, err = n.node.Exec(ctx, input)
		if err == nil {
			return result, nil
		}
		if i < n.maxRetries {
			log.Printf("[Node] Exec retry %d/%d, error: %v", i+1, n.maxRetries, err)
		}
	}
	return result, err
}

// Run implements Workflow.Run — executes the full Prep → Exec → Post lifecycle.
func (n *Node[State, PrepResult, ExecResults]) Run(ctx context.Context, state *State) Action {
	prepRes := n.node.Prep(state)
	if len(prepRes) == 0 {
		return n.node.Post(state, prepRes)
	}

	execResults := make([]ExecResults, len(prepRes))
	for i, item := range prepRes {
		result, err := n.executeWithRetry(ctx, item)
		if err != nil {
			execResults[i] = n.node.ExecFallback(err)
		} else {
			execResults[i] = result
		}
	}

	return n.node.Post(state, prepRes, execResults...)
}

// AddSuccessor connects a successor workflow for a given action.
func (n *Node[State, PrepResult, ExecResults]) AddSuccessor(
	workflow Workflow[State], action ...Action,
) Workflow[State] {
	if workflow == nil {
		return workflow
	}
	if len(action) == 0 {
		n.successors[ActionDefault] = workflow
	} else {
		n.successors[action[0]] = workflow
	}
	return workflow
}

// GetSuccessor returns the successor for the given action.
func (n *Node[State, PrepResult, ExecResults]) GetSuccessor(action Action) Workflow[State] {
	return n.successors[action]
}
