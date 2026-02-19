package skill

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// Manager owns the lifecycle of all workspace skill tools.
// It scans <workspaceDir>/skills/, registers tools on startup, and supports
// diff-based hot reload so newly created or modified skills take effect without restart.
//
// Concurrency: all state changes are guarded by mu.
type Manager struct {
	workspaceDir string
	mu           sync.Mutex
	skills       map[string]*SkillDef // tool name → SkillDef
}

// NewManager creates a Manager for the given workspace directory.
// No scanning is performed until LoadAll or Reload is called.
func NewManager(workspaceDir string) *Manager {
	return &Manager{
		workspaceDir: workspaceDir,
		skills:       make(map[string]*SkillDef),
	}
}

// LoadAll scans the workspace skills directory and registers all valid skills.
// Go skills are compiled eagerly so build errors surface at startup rather than
// at first invocation.
//
// Returns the count of successfully loaded skills and any per-skill errors.
// Per-skill errors are non-fatal — other skills continue to load.
func (m *Manager) LoadAll(_ context.Context, registry *tool.Registry) (int, []error) {
	defs, errs := ScanDir(m.workspaceDir)

	m.mu.Lock()
	defer m.mu.Unlock()

	loaded := 0
	for _, def := range defs {
		if def.Runtime == "go" {
			if err := ensureCompiled(def); err != nil {
				errs = append(errs, fmt.Errorf("skill %q: compile: %w", def.Name, err))
				log.Printf("[Skill] Compile error: %q: %v", def.Name, err)
				continue
			}
		}
		registry.Register(NewSkillTool(def))
		m.skills[def.Name] = def
		loaded++
		log.Printf("[Skill] Loaded: %s (runtime: %s)", def.Name, def.Runtime)
	}

	return loaded, errs
}

// Reload re-scans the workspace skills directory and applies a diff:
//   - Added skills:   compiled (if Go) and registered.
//   - Removed skills: unregistered.
//   - Existing skills: re-compiled (if Go) and re-registered to pick up code changes.
//
// Returns a human-readable summary of changes. Per-skill errors are described in
// the summary but do not cause Reload to return an error — partial success is valid.
func (m *Manager) Reload(_ context.Context, registry *tool.Registry) string {
	defs, scanErrs := ScanDir(m.workspaceDir)

	// Build the new skills map, compiling Go skills as needed.
	newDefs := make(map[string]*SkillDef, len(defs))
	var warnings []string

	for _, def := range defs {
		if def.Runtime == "go" {
			// Always recompile on reload so code changes take effect.
			if err := CompileGoSkill(def.Dir); err != nil {
				warnings = append(warnings, fmt.Sprintf("[ERROR] compile %q: %v", def.Name, err))
				log.Printf("[Skill] Reload compile error: %q: %v", def.Name, err)
				continue // skip this skill — keep the old version if any
			}
		}
		newDefs[def.Name] = def
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Unregister skills that have been removed.
	removed := 0
	for name := range m.skills {
		if _, exists := newDefs[name]; !exists {
			registry.Unregister(name)
			delete(m.skills, name)
			removed++
			log.Printf("[Skill] Unloaded: %s", name)
		}
	}

	// Register new skills and re-register updated ones.
	added := 0
	for name, def := range newDefs {
		_, existed := m.skills[name]
		registry.Register(NewSkillTool(def))
		m.skills[name] = def
		if !existed {
			added++
			log.Printf("[Skill] Loaded: %s (runtime: %s)", def.Name, def.Runtime)
		} else {
			log.Printf("[Skill] Reloaded: %s (runtime: %s)", def.Name, def.Runtime)
		}
	}
	updated := len(newDefs) - added

	// Compose human-readable summary.
	var parts []string
	parts = append(parts, fmt.Sprintf("Skill reload: +%d added, -%d removed, %d reloaded", added, removed, updated))
	for _, e := range scanErrs {
		parts = append(parts, fmt.Sprintf("[WARNING] %v", e))
	}
	parts = append(parts, warnings...)
	return strings.Join(parts, "\n")
}
