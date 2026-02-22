package agent

import (
	"fmt"
	"sync/atomic"
	"time"
)

// CostGuard enforces token budget and runtime duration limits.
// usedTokens uses atomic operations (safe for concurrent reads).
// exceeded is read/written only within the single-goroutine ReAct loop (AgentState).
type CostGuard struct {
	maxTokens   int64         // 0 = disabled
	maxDuration time.Duration // 0 = disabled
	usedTokens  atomic.Int64
	startTime   time.Time
	exceeded    bool // single-goroutine: set by Exec/Prep, read by Post
}

// NewCostGuard creates a cost guard with optional token and duration limits.
// Set maxTokens=0 and/or maxDuration=0 to disable the respective guard.
func NewCostGuard(maxTokens int64, maxDuration time.Duration) *CostGuard {
	return &CostGuard{
		maxTokens:   maxTokens,
		maxDuration: maxDuration,
		startTime:   time.Now(),
	}
}

// RecordTokens adds n tokens (input + output combined) to the running total.
// Returns error if budget is exceeded after this addition.
// Sets exceeded flag so Post() can force ActionAnswer.
func (g *CostGuard) RecordTokens(n int) error {
	if g.maxTokens <= 0 {
		return nil
	}
	total := g.usedTokens.Add(int64(n))
	if total > g.maxTokens {
		g.exceeded = true
		return fmt.Errorf("token budget exceeded: used %d / limit %d", total, g.maxTokens)
	}
	return nil
}

// CheckDuration returns error if the agent has been running too long.
// Sets exceeded flag so Post() can force ActionAnswer.
func (g *CostGuard) CheckDuration() error {
	if g.maxDuration <= 0 {
		return nil
	}
	if elapsed := time.Since(g.startTime); elapsed > g.maxDuration {
		g.exceeded = true
		return fmt.Errorf("agent runtime exceeded: %v / limit %v",
			elapsed.Round(time.Second), g.maxDuration)
	}
	return nil
}

// IsExceeded returns true if any budget/duration limit has been exceeded.
func (g *CostGuard) IsExceeded() bool { return g.exceeded }
