package agent

import (
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// AgentState is the shared state for the agent decision loop.
type AgentState struct {
	Problem      string         // User's original question
	WorkspaceDir string         // Working directory for file/shell tools
	StepHistory  []StepRecord   // Execution records for all steps
	ToolRegistry *tool.Registry // Available tools

	Solution string // Final answer

	ThinkingMode string // "native" or "app" — controls DecideNode prompt options

	// Transient field: DecideNode writes, ToolNode/ThinkNode reads.
	// Solves node-to-node state passing.
	LastDecision *Decision `json:"-"`

	// SSE callbacks
	OnStepComplete func(StepRecord)   `json:"-"`
	OnStreamChunk  func(chunk string) `json:"-"` // LLM streaming token callback
}

// StepRecord records a single step execution.
type StepRecord struct {
	StepNumber int    `json:"step_number"`
	Type       string `json:"type"`      // "decide", "tool", "think", "answer"
	Action     string `json:"action"`    // Decision action
	ToolName   string `json:"tool_name"` // Tool name (when type=tool)
	Input      string `json:"input"`     // Input content
	Output     string `json:"output"`    // Output result
}

// MaxAgentSteps prevents infinite decision loops.
const MaxAgentSteps = 16

// ── DecideNode generic types ──
// BaseNode[AgentState, DecidePrep, Decision]

// DecidePrep is the prepared data for LLM decision-making.
type DecidePrep struct {
	Problem      string
	WorkspaceDir string // Working directory context for LLM
	StepSummary  string // Summary of previous steps
	ToolsPrompt  string // Available tools description
	StepCount    int    // Current step count (for forced termination)
	ThinkingMode string // "native" or "app"
}

// Decision is the LLM's decision output, parsed from YAML.
// ToolParams uses map[string]any (YAML-friendly); converted to json.RawMessage
// via json.Marshal before calling Tool.Execute().
type Decision struct {
	Action     string         `yaml:"action"`      // "tool", "think", "answer"
	Reason     string         `yaml:"reason"`      // Reasoning for this decision
	ToolName   string         `yaml:"tool_name"`   // Required when action=tool
	ToolParams map[string]any `yaml:"tool_params"` // YAML-friendly, json.Marshal before tool call
	Thinking   string         `yaml:"thinking"`    // Used when action=think
	Answer     string         `yaml:"answer"`      // Used when action=answer
}

// ── ToolNode generic types ──
// BaseNode[AgentState, ToolPrep, ToolExecResult]

// ToolPrep is prepared by reading LastDecision and converting ToolParams.
type ToolPrep struct {
	ToolName string
	Args     []byte // json.RawMessage from json.Marshal(Decision.ToolParams)
}

// ToolExecResult is the result of executing a tool.
type ToolExecResult struct {
	ToolName string
	Output   string
	Error    string
}

// ── ThinkNode generic types ──
// BaseNode[AgentState, ThinkPrep, ThinkResult]

// ThinkPrep provides context for reasoning.
type ThinkPrep struct {
	Problem string
	Context string // Accumulated context from steps
}

// ThinkResult holds the reasoning output.
type ThinkResult struct {
	Thinking string
}

// ── AnswerNode generic types ──
// BaseNode[AgentState, AnswerPrep, AnswerResult]

// AnswerPrep aggregates all context for final answer generation.
type AnswerPrep struct {
	Problem     string
	FullContext string             // Complete context from all steps
	HasToolUse  bool               // Whether any tool was used (skip shortcut if true)
	StreamChunk func(chunk string) `json:"-"` // Optional streaming callback
}

// AnswerResult holds the final answer.
type AnswerResult struct {
	Answer string
}

// hasToolSteps checks if any step in the history is a tool execution.
func hasToolSteps(state *AgentState) bool {
	for _, s := range state.StepHistory {
		if s.Type == "tool" {
			return true
		}
	}
	return false
}
