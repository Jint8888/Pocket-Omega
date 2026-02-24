package walkthrough

import (
	"fmt"
	"strings"
	"sync"
)

// MaxEntries is the maximum number of walkthrough entries per session.
// FIFO eviction removes the oldest auto entry when exceeded.
const MaxEntries = 20

// EntrySource distinguishes auto-generated vs manually added entries.
type EntrySource string

const (
	SourceAuto   EntrySource = "auto"   // ToolNode.Post auto-write
	SourceManual EntrySource = "manual" // Agent via walkthrough tool (pinned)
)

// Entry represents a single walkthrough memo item.
type Entry struct {
	StepNumber int         `json:"step_number"` // 0 for manual entries
	Source     EntrySource `json:"source"`
	Content    string      `json:"content"`
}

// Store manages walkthrough entries per session.
// Thread-safe via sync.RWMutex — same pattern as plan.PlanStore.
type Store struct {
	mu      sync.RWMutex
	entries map[string][]Entry // sessionID → entries
}

// NewStore creates an empty walkthrough store.
func NewStore() *Store {
	return &Store{entries: make(map[string][]Entry)}
}

// Append adds an entry for the given session, applying FIFO eviction if needed.
// Eviction priority: oldest auto (non-manual) first; if all manual, oldest overall.
func (s *Store) Append(sessionID string, entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.entries[sessionID]
	if len(entries) >= MaxEntries {
		// Find oldest auto entry to evict
		evicted := -1
		for i := range entries {
			if entries[i].Source != SourceManual {
				evicted = i
				break
			}
		}
		if evicted == -1 {
			// All manual — evict the oldest
			evicted = 0
		}
		entries = append(entries[:evicted], entries[evicted+1:]...)
	}
	s.entries[sessionID] = append(entries, entry)
}

// Get returns a defensive copy of entries for a session.
// Returns nil if no entries exist.
func (s *Store) Get(sessionID string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.entries[sessionID]
	if entries == nil {
		return nil
	}
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	return cp
}

// Delete removes all entries for a session (cleanup on request end).
func (s *Store) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, sessionID)
}

// Render formats entries as a markdown section for prompt injection.
// Returns "" if no entries exist.
func (s *Store) Render(sessionID string) string {
	entries := s.Get(sessionID) // uses defensive copy
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 备忘录\n")
	for _, e := range entries {
		if e.Source == SourceManual {
			sb.WriteString(fmt.Sprintf("- 📌 %s\n", e.Content))
		} else {
			sb.WriteString(fmt.Sprintf("- [步骤%d] %s\n", e.StepNumber, e.Content))
		}
	}
	return sb.String()
}
