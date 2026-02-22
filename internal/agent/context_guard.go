package agent

// ContextStatus indicates the context window usage level.
type ContextStatus int

const (
	ContextOK       ContextStatus = iota
	ContextWarning                // ≥ 70%: log warning
	ContextCritical               // ≥ 85%: trigger auto-compact
)

// ContextGuard monitors context window usage and signals when
// the agent should consider compacting conversation history.
type ContextGuard struct {
	windowTokens int // model's context window size in tokens
}

// NewContextGuard creates a context guard for the given window size.
// windowTokens <= 0 disables the guard (Check always returns ContextOK).
func NewContextGuard(windowTokens int) *ContextGuard {
	return &ContextGuard{windowTokens: windowTokens}
}

// CheckTokens returns the context status for a pre-computed token count.
// Use this when the caller has already estimated tokens (e.g. with SystemPromptEst).
func (g *ContextGuard) CheckTokens(tokens int) ContextStatus {
	if g.windowTokens <= 0 {
		return ContextOK
	}
	ratio := float64(tokens) / float64(g.windowTokens)
	switch {
	case ratio >= 0.85:
		return ContextCritical
	case ratio >= 0.70:
		return ContextWarning
	default:
		return ContextOK
	}
}
