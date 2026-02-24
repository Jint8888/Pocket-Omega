package session

import (
	"sync"
	"time"
)

// minCleanupInterval is the smallest allowed TTL to prevent degenerate ticker intervals.
const minCleanupInterval = time.Millisecond

// Turn represents one complete exchange (user question + assistant answer).
type Turn struct {
	UserMsg   string
	Assistant string // final answer, excluding intermediate reasoning steps
	IsAgent   bool   // true = Agent mode response
}

// Session holds all state for a single browser tab session.
type Session struct {
	ID       string
	History  []Turn
	Summary  string // compact summary of older turns (accumulated across multiple /compact calls)
	LastUsed time.Time
}

// Store is a thread-safe in-memory session registry with TTL eviction.
// NOT designed for multi-replica deployments; matches the single-process
// architecture of Pocket-Omega v0.x.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration // inactivity TTL, e.g. 30 minutes
	maxTurns int           // max turns retained per session, e.g. 10
	done     chan struct{} // closed by Close() to stop the cleanup goroutine
}

// NewStore creates a new Store with the given TTL and maxTurns limit.
// A background goroutine is started to periodically evict expired sessions.
// Call Close() when the store is no longer needed to stop the goroutine.
func NewStore(ttl time.Duration, maxTurns int) *Store {
	if ttl < minCleanupInterval {
		ttl = minCleanupInterval
	}
	s := &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		maxTurns: maxTurns,
		done:     make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// AppendTurn adds a completed exchange to the session, enforcing maxTurns.
// If the session does not yet exist it is created automatically, so callers
// do not need to call GetOrCreate separately before the first AppendTurn.
func (s *Store) AppendTurn(id string, turn Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		// Auto-create on first write so the initial turn is never silently dropped.
		sess = &Session{ID: id, LastUsed: time.Now()}
		s.sessions[id] = sess
	}
	sess.History = append(sess.History, turn)
	// Trim oldest turns to stay within maxTurns
	if len(sess.History) > s.maxTurns {
		sess.History = sess.History[len(sess.History)-s.maxTurns:]
	}
	sess.LastUsed = time.Now()
}

// GetSessionContext atomically returns both turn history and compact summary.
// Prefer this over separate GetHistory + GetSummary calls to avoid TOCTOU issues.
func (s *Store) GetSessionContext(id string) ([]Turn, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, ""
	}
	result := make([]Turn, len(sess.History))
	copy(result, sess.History)
	return result, sess.Summary
}

// Compact replaces old turns with a summary, keeping the newest keepN turns.
// The caller is responsible for merging any existing summary into the new one
// before calling this method (see cmdCompact).
func (s *Store) Compact(id string, summary string, keepN int) (compacted int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || len(sess.History) <= keepN {
		return 0
	}
	compacted = len(sess.History) - keepN
	sess.Summary = summary
	sess.History = sess.History[len(sess.History)-keepN:]
	sess.LastUsed = time.Now()
	return compacted
}

// Delete explicitly removes a session (e.g., user clicks "Clear Chat").
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Count returns the number of active sessions.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Close stops the background cleanup goroutine. Safe to call multiple times.
func (s *Store) Close() {
	select {
	case <-s.done:
		// already closed
	default:
		close(s.done)
	}
}

// cleanupLoop periodically removes sessions that have exceeded the TTL.
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(s.ttl / 2)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			cutoff := time.Now().Add(-s.ttl)
			for id, sess := range s.sessions {
				if sess.LastUsed.Before(cutoff) {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
