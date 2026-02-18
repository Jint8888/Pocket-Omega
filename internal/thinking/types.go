package thinking

import "regexp"

// Supervisor constants — invisible quality gate for the CoT chain.
const (
	// MaxThoughts prevents infinite thinking loops.
	MaxThoughts = 25

	// MaxSupervisorRetries is the max number of silent retries before force-accepting.
	MaxSupervisorRetries = 2

	// MinSolutionLength is the minimum acceptable solution length (in runes).
	MinSolutionLength = 5
)

// rejectPatterns detects refusal responses (e.g. "sorry, I cannot...").
var rejectPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(sorry|抱歉|对不起)[,，]?\s*(i\s*)?(cannot|can't|couldn't|无法|不能)`),
	regexp.MustCompile(`(?i)^(i\s*)?(cannot|can't|couldn't|无法|不能).{0,30}(sorry|抱歉)`),
	regexp.MustCompile(`(?i)^(unable|无法)\s+to\s+`),
}

// ThinkingState is the shared state for the Chain of Thought flow.
type ThinkingState struct {
	Problem           string        `json:"problem"`
	Thoughts          []ThoughtData `json:"thoughts"`
	CurrentThoughtNum int           `json:"current_thought_num"`
	Solution          string        `json:"solution"`

	// OnThoughtComplete is called after each thought step completes.
	// Used for SSE streaming to push thoughts to the client in real-time.
	OnThoughtComplete func(ThoughtData) `json:"-"`

	// supervisorRetryCount tracks silent quality-gate retries (invisible to user).
	supervisorRetryCount int
}

// ThoughtData represents a single thought step from the LLM.
type ThoughtData struct {
	ThoughtNumber     int        `json:"thought_number"     yaml:"thought_number"`
	CurrentThinking   string     `json:"current_thinking"   yaml:"current_thinking"`
	Planning          []PlanStep `json:"planning"           yaml:"planning"`
	NextThoughtNeeded bool       `json:"next_thought_needed" yaml:"next_thought_needed"`
}

// PlanStep represents a step in the structured plan.
type PlanStep struct {
	Description string     `json:"description" yaml:"description"`
	Status      string     `json:"status"      yaml:"status"` // "Pending", "Done", "Verification Needed"
	Result      string     `json:"result,omitempty"  yaml:"result,omitempty"`
	Mark        string     `json:"mark,omitempty"    yaml:"mark,omitempty"`
	SubSteps    []PlanStep `json:"sub_steps,omitempty" yaml:"sub_steps,omitempty"`
}

// PrepData holds prepared data for the Exec phase.
type PrepData struct {
	Problem          string
	ThoughtsText     string
	LastPlanText     string
	CurrentThoughtNo int
	IsFirstThought   bool
}
