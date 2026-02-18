package core

import "context"

// BaseNode defines the core interface for all nodes in the workflow.
// It follows the three-phase execution model: Prep -> Exec -> Post.
//
// Type parameters:
//   - State: the shared state passed through the workflow
//   - PrepResult: the type returned by Prep and consumed by Exec
//   - ExecResults: the type returned by Exec and consumed by Post
type BaseNode[State any, PrepResult any, ExecResults any] interface {
	// Prep reads from shared state and generates work items for Exec.
	Prep(state *State) []PrepResult

	// Exec performs the core logic on a single work item.
	Exec(ctx context.Context, prepResult PrepResult) (ExecResults, error)

	// Post handles results from Exec and determines the next action.
	Post(state *State, prepRes []PrepResult, execResults ...ExecResults) Action

	// ExecFallback provides a default result if Exec fails after all retries.
	ExecFallback(err error) ExecResults
}

// Workflow represents a unit of execution that can be connected to other workflows.
// Both Node and Flow implement this interface, enabling composition.
type Workflow[State any] interface {
	// Run executes the workflow and returns an action for routing.
	Run(ctx context.Context, state *State) Action

	// GetSuccessor returns the successor workflow for a given action.
	GetSuccessor(action Action) Workflow[State]

	// AddSuccessor connects a successor workflow for a specific action.
	// Returns the successor for chaining.
	AddSuccessor(successor Workflow[State], action ...Action) Workflow[State]
}
