package core

// Action represents the result of a node execution that determines flow control.
type Action string

// Common actions used throughout the framework.
const (
	ActionContinue Action = "continue"
	ActionEnd      Action = "end"
	ActionSuccess  Action = "success"
	ActionFailure  Action = "failure"
	ActionDefault  Action = "default"

	// Agent routing actions (Phase 2)
	ActionTool   Action = "tool"
	ActionThink  Action = "think"
	ActionAnswer Action = "answer"
)
