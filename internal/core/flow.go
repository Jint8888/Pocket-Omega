package core

import (
	"context"
	"log"
)

// maxFlowIterations is an independent safety cap on the number of node
// transitions per Run call. It guards against misconfigured successor
// graphs that could bypass application-level step limits (e.g. MaxAgentSteps).
const maxFlowIterations = 200

// Flow orchestrates the execution of connected workflows using action-based routing.
// It implements the Workflow interface, allowing flows to be nested.
type Flow[State any] struct {
	startNode  Workflow[State]
	successors map[Action]Workflow[State]
}

// NewFlow creates a new Flow with the given start node.
func NewFlow[State any](startNode Workflow[State]) *Flow[State] {
	return &Flow[State]{
		startNode:  startNode,
		successors: make(map[Action]Workflow[State]),
	}
}

// Run implements Workflow.Run — executes the chain of workflows.
func (f *Flow[State]) Run(ctx context.Context, state *State) Action {
	current := f.startNode
	if current == nil {
		log.Println("[Flow] Warning: started with no start node")
		return ActionFailure
	}

	var lastAction Action = ActionSuccess
	for i := 0; current != nil; i++ {
		if i >= maxFlowIterations {
			log.Printf("[Flow] Warning: maxFlowIterations (%d) reached, aborting to prevent infinite loop", maxFlowIterations)
			return ActionFailure
		}

		// Check context cancellation between node transitions
		if ctx.Err() != nil {
			log.Printf("[Flow] Context cancelled: %v", ctx.Err())
			return ActionFailure
		}

		action := current.Run(ctx, state)
		lastAction = action

		// Look for successor in current node first, then flow-level
		next := current.GetSuccessor(action)
		if next == nil {
			next = f.GetSuccessor(action)
		}
		current = next
	}
	return lastAction
}

// AddSuccessor connects a flow-level successor for a given action.
func (f *Flow[State]) AddSuccessor(successor Workflow[State], action ...Action) Workflow[State] {
	if successor == nil {
		return successor
	}
	if len(action) == 0 {
		f.successors[ActionDefault] = successor
	} else {
		f.successors[action[0]] = successor
	}
	return successor
}

// GetSuccessor returns the flow-level successor for the given action.
func (f *Flow[State]) GetSuccessor(action Action) Workflow[State] {
	return f.successors[action]
}
