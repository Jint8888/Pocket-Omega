package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/pocketomega/pocket-omega/internal/plan"
)

// ── SSE Writer ──

// sseWriter wraps an http.ResponseWriter with SSE event writing and
// client disconnect detection. Shared by both Chat and Agent handlers.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
}

// newSSEWriter prepares SSE headers and returns a writer.
// Returns nil if streaming is not supported.
func newSSEWriter(w http.ResponseWriter, r *http.Request) *sseWriter {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	return &sseWriter{w: w, flusher: flusher, ctx: r.Context()}
}

// Send writes an SSE event. Returns false if the client has disconnected.
func (s *sseWriter) Send(event string, data interface{}) bool {
	select {
	case <-s.ctx.Done():
		return false
	default:
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[SSE] JSON marshal error: %v", err)
		return false
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, string(jsonBytes)); err != nil {
		log.Printf("[SSE] Write error (client disconnected?): %v", err)
		return false
	}
	s.flusher.Flush()
	return true
}

// ── SSE Event Types ──

type sseThoughtEvent struct {
	ThoughtNumber   int    `json:"thought_number"`
	CurrentThinking string `json:"current_thinking"`
	PlanText        string `json:"plan_text,omitempty"`
}

type sseDoneEvent struct {
	Solution string      `json:"solution"`
	Stats    *agentStats `json:"stats,omitempty"` // nil for ChatHandler
}

// agentStats holds execution statistics returned in the done event.
type agentStats struct {
	Steps      int   `json:"steps"`
	ToolCalls  int   `json:"tool_calls"`
	ElapsedMs  int64 `json:"elapsed_ms"`
	TokensUsed int64 `json:"tokens_used"` // 0 if CostGuard disabled
}

const sseEventPlan = "plan"

type ssePlanEvent struct {
	Steps []plan.PlanStep `json:"steps"`
}
