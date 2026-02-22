// Package prompt implements a three-layer prompt loading system:
//
//   - L1: Hardcoded constraints in Go source (format requirements, safety rules)
//   - L2: Project behaviour rules in prompts/*.md (embedded by default, overridable at runtime)
//   - L3: User custom rules in rules.md (runtime only, never committed)
//
// The PromptLoader is safe for concurrent use.
package prompt

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// defaultPrompts embeds the L2 prompt files shipped with the binary.
// The prompts/ directory must exist at compile time (relative to this file's package).
//
//go:embed prompts/*
var defaultPrompts embed.FS

// promptInjectionPatterns contains lowercased substrings that indicate prompt injection attempts.
// Lines matching any pattern are dropped from L3 user rules with a warning.
var promptInjectionPatterns = []string{
	"ignore previous",
	"ignore above",
	"ignore all previous",
	"disregard all",
	"disregard previous",
	"forget previous",
	"forget all previous",
	"override instructions",
	"override previous",
	"new instructions:",
	"from now on",
}

// PromptLoader reads L2 prompt files and the L3 user rules file.
// It caches file contents after the first read; call Reload to invalidate the cache.
type PromptLoader struct {
	promptsDir string // runtime override directory (may be empty)
	rulesPath  string // path to L3 rules.md
	soulPath   string // path to user soul.md (workspace root)
	cache      map[string]string
	patchHooks []patchEntry // recorded PatchFile calls, reapplied after Reload
	mu         sync.RWMutex
}

// patchEntry records a single PatchFile call for reapplication after Reload.
type patchEntry struct {
	Name, OldStr, NewStr string
}

// NewPromptLoader creates a PromptLoader that reads L2 files from promptsDir
// (falling back to embedded defaults), L3 rules from rulesPath, and the user
// soul file from soulPath.
//
// All paths may be empty strings — the loader degrades gracefully:
//   - empty promptsDir:  only embedded defaults are used
//   - empty / non-existent rulesPath: LoadUserRules returns ""
//   - empty / non-existent soulPath:  LoadSoul falls back to embedded soul.md
func NewPromptLoader(promptsDir, rulesPath, soulPath string) *PromptLoader {
	return &PromptLoader{
		promptsDir: promptsDir,
		rulesPath:  rulesPath,
		soulPath:   soulPath,
		cache:      make(map[string]string),
	}
}

// Load returns the content of the named prompt file (e.g. "decide_common.md").
//
// Priority:
//  1. Disk file at promptsDir/name (runtime override)
//  2. Embedded default at prompts/name
//  3. Empty string (silent, file simply absent)
//
// A disk read error (permission denied, etc.) logs a warning and falls back
// to the embedded default.  Cache hit avoids repeated disk reads.
func (l *PromptLoader) Load(name string) string {
	cacheKey := "l2:" + name

	// Fast path: cache hit under read lock
	l.mu.RLock()
	if val, ok := l.cache[cacheKey]; ok {
		l.mu.RUnlock()
		return val
	}
	l.mu.RUnlock()

	// Load without any lock (pure computation / I/O)
	content := l.loadUncached(name)

	// Double-check under write lock to avoid duplicate entries when two
	// goroutines race through the read-lock miss at the same time.
	l.mu.Lock()
	if val, ok := l.cache[cacheKey]; ok {
		l.mu.Unlock()
		return val
	}
	l.cache[cacheKey] = content
	l.mu.Unlock()

	return content
}

// loadUncached does the actual file read without touching the cache.
func (l *PromptLoader) loadUncached(name string) string {
	embedPath := "prompts/" + name

	// Try disk file first (runtime override)
	if l.promptsDir != "" {
		diskPath := filepath.Join(l.promptsDir, name)
		data, err := os.ReadFile(diskPath)
		if err == nil {
			return string(data)
		}
		if !os.IsNotExist(err) {
			// File exists but unreadable — warn and fall through to embed
			log.Printf("[Prompt] Warning: read %q failed: %v; falling back to embedded default", diskPath, err)
		}
		// os.IsNotExist: silently fall through to embed
	}

	// Try embedded default
	data, err := fs.ReadFile(defaultPrompts, embedPath)
	if err == nil {
		return string(data)
	}

	// Neither disk nor embed — return empty string silently
	return ""
}

// LoadUserRules reads the L3 rules.md file and filters dangerous injection patterns.
//
// Lines containing known jailbreak phrases (case-insensitive) are dropped and
// logged as warnings.  The remaining content is returned as-is.
// Returns "" if the file does not exist or rulesPath is empty.
func (l *PromptLoader) LoadUserRules() string {
	cacheKey := "l3:rules"

	// Fast path: cache hit under read lock
	l.mu.RLock()
	if val, ok := l.cache[cacheKey]; ok {
		l.mu.RUnlock()
		return val
	}
	l.mu.RUnlock()

	// Load without any lock
	content := l.loadUserRulesUncached()

	// Double-check under write lock to avoid duplicate I/O on concurrent miss
	l.mu.Lock()
	if val, ok := l.cache[cacheKey]; ok {
		l.mu.Unlock()
		return val
	}
	l.cache[cacheKey] = content
	l.mu.Unlock()

	return content
}

func (l *PromptLoader) loadUserRulesUncached() string {
	if l.rulesPath == "" {
		return ""
	}

	data, err := os.ReadFile(l.rulesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[Prompt] Warning: read user rules %q failed: %v", l.rulesPath, err)
		}
		return ""
	}

	raw := string(data)
	filtered := filterDangerousLines(raw)
	return filtered
}

// LoadSoul returns the agent soul/persona definition.
//
// Priority:
//  1. User soul file at soulPath (workspace root) — only if non-empty
//  2. Embedded default at prompts/soul.md
//  3. Empty string (silent, file simply absent)
func (l *PromptLoader) LoadSoul() string {
	cacheKey := "soul"

	l.mu.RLock()
	if val, ok := l.cache[cacheKey]; ok {
		l.mu.RUnlock()
		return val
	}
	l.mu.RUnlock()

	content := l.loadSoulUncached()

	l.mu.Lock()
	if val, ok := l.cache[cacheKey]; ok {
		l.mu.Unlock()
		return val
	}
	l.cache[cacheKey] = content
	l.mu.Unlock()

	return content
}

func (l *PromptLoader) loadSoulUncached() string {
	// User soul file takes priority — skip if file is empty (placeholder).
	if l.soulPath != "" {
		data, err := os.ReadFile(l.soulPath)
		if err == nil {
			if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
				return string(data)
			}
			// Empty file: fall through to embedded default
		} else if !os.IsNotExist(err) {
			log.Printf("[Prompt] Warning: read soul file %q failed: %v; falling back to embedded default", l.soulPath, err)
		}
	}

	// Fall back to embedded default
	return l.loadUncached("soul.md")
}

// filterDangerousLines drops lines that match known prompt-injection patterns.
// Remaining lines are preserved including their original line endings.
func filterDangerousLines(content string) string {
	lines := strings.Split(content, "\n")
	safe := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(line)
		dropped := false
		for _, pattern := range promptInjectionPatterns {
			if strings.Contains(lower, pattern) {
				log.Printf("[Prompt] Warning: user rules line dropped (injection pattern %q detected): %q", pattern, line)
				dropped = true
				break
			}
		}
		if !dropped {
			safe = append(safe, line)
		}
	}
	return strings.Join(safe, "\n")
}

// Reload clears the internal cache so that subsequent Load and LoadUserRules
// calls re-read files from disk.  Safe for concurrent use.
// Typically triggered by mcp_reload or a /reload command.
func (l *PromptLoader) Reload() {
	l.mu.Lock()
	l.cache = make(map[string]string)
	l.mu.Unlock()

	// Reapply all recorded patches so template variables survive hot-reloads.
	// Uses reapplyPatch (not PatchFile) to avoid re-recording duplicates.
	for _, p := range l.patchHooks {
		l.reapplyPatch(p)
	}
}

// reapplyPatch re-patches a single file without recording another patchHooks
// entry (avoids infinite growth on repeated Reloads).
//
// Cache-first read: an earlier reapplyPatch in the same Reload() call may have
// already written a partially-patched version of this file into the cache.
// We must read that version so patches accumulate correctly.  Only fall back to
// loadUncached on a cache miss (first patch for this file in the current Reload).
func (l *PromptLoader) reapplyPatch(p patchEntry) {
	cacheKey := "l2:" + p.Name
	l.mu.RLock()
	content, ok := l.cache[cacheKey]
	l.mu.RUnlock()
	if !ok {
		content = l.loadUncached(p.Name)
	}
	patched := strings.ReplaceAll(content, p.OldStr, p.NewStr)
	l.mu.Lock()
	l.cache[cacheKey] = patched
	l.mu.Unlock()
}

// PatchFile loads the named prompt file (via the normal priority chain), replaces
// oldStr with newStr, and stores the result in the cache so that subsequent Load
// calls return the patched version without re-reading the file.
//
// This is used at startup to inject live environment data (e.g. runtime probe
// results) into prompt templates that contain placeholder strings like
// "{{RUNTIME_ENV}}". If oldStr is not found in the file content the cache is
// still populated with the unmodified content (no-op replacement).
//
// Thread-safe.  A call to Reload() clears the patch; re-apply after reload if needed.
func (l *PromptLoader) PatchFile(name, oldStr, newStr string) {
	cacheKey := "l2:" + name

	// Load through the normal chain (may hit cache or read from disk/embed).
	content := l.Load(name)

	// Apply the string replacement.
	patched := strings.ReplaceAll(content, oldStr, newStr)

	// Store the patched version, overwriting any previously cached entry.
	l.mu.Lock()
	l.cache[cacheKey] = patched
	l.mu.Unlock()

	// Record for reapplication after Reload.
	l.patchHooks = append(l.patchHooks, patchEntry{Name: name, OldStr: oldStr, NewStr: newStr})
}
