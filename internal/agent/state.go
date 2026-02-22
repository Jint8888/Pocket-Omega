package agent

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// AgentState is the shared state for the agent decision loop.
// NOT goroutine-safe: all fields must be accessed from a single goroutine.
// The current Flow.Run implementation guarantees single-goroutine access.
// If parallel node execution is introduced in the future, add sync.Mutex protection.
type AgentState struct {
	Problem      string         // User's original question
	WorkspaceDir string         // Working directory for file/shell tools
	StepHistory  []StepRecord   // Execution records for all steps
	ToolRegistry *tool.Registry // Available tools

	Solution string // Final answer

	ThinkingMode        string // "native" or "app" — controls DecideNode prompt options
	ToolCallMode        string // "auto", "fc", or "yaml" — may be raw unresolved value
	ContextWindowTokens int    // model context window in tokens; 0 = use safe fallback
	ConversationHistory string // formatted conversation prefix, populated by Handler layer

	// Runtime environment info — injected by AgentHandler from AgentHandlerOptions.
	OSName    string // e.g. "Windows", "Linux", "macOS"
	ShellCmd  string // e.g. "cmd.exe /c", "sh -c"
	ModelName string // e.g. "gemini-2.5-pro"

	// Transient field: DecideNode writes, ToolNode/ThinkNode reads.
	// Solves node-to-node state passing.
	LastDecision *Decision `json:"-"`

	// Guardrail fields
	LoopDetectionStreak int                             `json:"-"` // consecutive loop detections without self-correction
	CostGuard           *CostGuard                      `json:"-"` // nil = disabled; enforces token/duration limits
	pendingCompact      bool                            // single-goroutine: set by Post (from Decision.ContextStatus), consumed in Post
	OnContextOverflow   func(ctx context.Context) error `json:"-"` // injected by AgentHandler

	// SSE callbacks
	OnStepComplete func(StepRecord)   `json:"-"`
	OnStreamChunk  func(chunk string) `json:"-"` // LLM streaming token callback
}

// StepRecord records a single step execution.
type StepRecord struct {
	StepNumber int    `json:"step_number"`
	Type       string `json:"type"`                   // "decide", "tool", "think", "answer"
	Action     string `json:"action"`                 // Decision action
	ToolName   string `json:"tool_name"`              // Tool name (when type=tool)
	Input      string `json:"input"`                  // Input content
	Output     string `json:"output"`                 // Output result
	ToolCallID string `json:"tool_call_id,omitempty"` // FC only: correlates with model's tool call
	IsError    bool   `json:"is_error,omitempty"`     // true when tool returned an error
}

// MaxAgentSteps prevents infinite decision loops.
// Configurable via AGENT_MAX_STEPS env var (default: 40, min: 5, max: 200).
var MaxAgentSteps = loadMaxSteps()

// loadMaxSteps reads AGENT_MAX_STEPS from the environment.
// Extracted as a standalone function to allow direct unit testing.
func loadMaxSteps() int {
	const defaultSteps = 40
	v := os.Getenv("AGENT_MAX_STEPS")
	if v == "" {
		return defaultSteps
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 5 || n > 200 {
		log.Printf("[Config] WARNING: invalid AGENT_MAX_STEPS=%q (must be 5-200), using default %d", v, defaultSteps)
		return defaultSteps
	}
	return n
}

// ── DecideNode generic types ──
// BaseNode[AgentState, DecidePrep, Decision]

// DecidePrep is the prepared data for LLM decision-making.
type DecidePrep struct {
	Problem             string
	WorkspaceDir        string               // Working directory context for LLM
	StepSummary         string               // Summary of previous steps
	ToolsPrompt         string               // Available tools description (YAML path)
	ToolDefinitions     []llm.ToolDefinition // Tool definitions (FC path)
	StepCount           int                  // Current step count (for forced termination)
	ThinkingMode        string               // "native" or "app"
	ToolCallMode        string               // "auto", "fc", or "yaml" — may be raw unresolved value
	ConversationHistory string               // formatted conversation prefix from previous turns
	ToolingSummary      string               // Phase 1: auto-generated tool summary from Registry
	RuntimeLine         string               // Phase 1: compact runtime info line
	HasMCPIntent        bool                 // Phase 2: whether Problem mentions MCP/skill keywords
	ContextWindowTokens int                  // Phase 2: model context window for token budget guard
	LoopDetected        DetectionResult      // LoopDetector: repetitive pattern detection result
	CostGuard           *CostGuard           // pointer shared with state for Exec to record tokens
	SystemPromptEst     int                  // estimated system prompt tokens (computed in Prep)
}

// Decision is the LLM's decision output.
// In YAML mode: parsed from YAML text. In FC mode: extracted from tool_calls.
// ToolParams uses map[string]any; converted to json.RawMessage before calling Tool.Execute().
type Decision struct {
	Action        string         `yaml:"action"`      // "tool", "think", "answer"
	Reason        string         `yaml:"reason"`      // Reasoning for this decision
	ToolName      string         `yaml:"tool_name"`   // Required when action=tool
	ToolParams    map[string]any `yaml:"tool_params"` // YAML-friendly, json.Marshal before tool call
	Thinking      string         `yaml:"thinking"`    // Used when action=think
	Answer        string         `yaml:"answer"`      // Used when action=answer
	ToolCallID    string         `yaml:"-"`           // FC only: tool call ID for result correlation
	ContextStatus ContextStatus  `yaml:"-"`           // set by Exec when context window is filling up
}

// ── ToolNode generic types ──
// BaseNode[AgentState, ToolPrep, ToolExecResult]

// ToolPrep is prepared by reading LastDecision and converting ToolParams.
type ToolPrep struct {
	ToolName     string
	Args         []byte    // json.RawMessage from json.Marshal(Decision.ToolParams)
	ToolCallID   string    // FC only: correlates tool result with the model's tool call
	ResolvedTool tool.Tool // resolved in Prep from state.ToolRegistry; nil = not found
}

// ToolExecResult is the result of executing a tool.
type ToolExecResult struct {
	ToolName   string
	Output     string
	Error      string
	ToolCallID string // FC only: passed through for multi-turn conversation history
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
