package tool

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/pocketomega/pocket-omega/internal/llm"
)

// Registry manages all registered tools with thread-safe access.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry. If a tool with the same name already
// exists, it is overwritten and a warning is logged.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		log.Printf("[Registry] WARNING: overwriting existing tool %q", t.Name())
	}
	r.tools[t.Name()] = t
}

// Unregister removes a tool from the registry (for hot-reload).
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	log.Printf("[Registry] Unregistered tool: %s", name)
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

// GenerateToolsPrompt creates a detailed description of all tools
// including their parameter schemas for injection into LLM prompts.
func (r *Registry) GenerateToolsPrompt() string {
	tools := r.List()
	if len(tools) == 0 {
		return "（无可用工具）"
	}

	var sb strings.Builder
	sb.WriteString("可用工具：\n")
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("\n### %s\n%s\n", t.Name(), t.Description()))
		schema := t.InputSchema()
		if len(schema) > 0 {
			sb.WriteString(fmt.Sprintf("参数 Schema: %s\n", string(schema)))
		}
	}
	return sb.String()
}

// GenerateToolDefinitions creates FC-compatible tool definitions.
// Used by the FC path in DecideNode. The YAML path uses GenerateToolsPrompt instead.
func (r *Registry) GenerateToolDefinitions() []llm.ToolDefinition {
	tools := r.List()
	defs := make([]llm.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.InputSchema(),
		}
	}
	return defs
}

// InitAll initializes all registered tools.
func (r *Registry) InitAll(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, t := range r.tools {
		if err := t.Init(ctx); err != nil {
			return fmt.Errorf("init tool %q: %w", name, err)
		}
	}
	log.Printf("[Registry] Initialized %d tools", len(r.tools))
	return nil
}

// CloseAll closes all registered tools, logging errors but not failing.
func (r *Registry) CloseAll() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, t := range r.tools {
		if err := t.Close(); err != nil {
			log.Printf("[Registry] Error closing tool %s: %v", name, err)
		}
	}
}

// WithExtra returns a shallow copy of this Registry with additional tools appended.
// Used for per-request tool injection (e.g. update_plan with session context).
// The returned Registry has an independent map; Tool instances themselves are shared
// by reference but are designed to be stateless or self-synchronized.
func (r *Registry) WithExtra(extras ...Tool) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := &Registry{tools: make(map[string]Tool, len(r.tools)+len(extras))}
	for k, v := range r.tools {
		cp.tools[k] = v
	}
	for _, t := range extras {
		cp.tools[t.Name()] = t
	}
	return cp
}
