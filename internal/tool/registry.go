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
//
// A Registry can be either a "root" registry (parent == nil) that owns its
// tools map, or a "view" registry (parent != nil) created by WithExtra that
// overlays additional tools on top of a parent. Views delegate Get/List to
// the parent, so changes to the parent (Register/Unregister) are immediately
// visible through the view. This is critical for mcp_reload: the agent holds
// a view (via WithExtra for per-request tools like update_plan), while
// mcp_reload modifies the root registry. Without delegation, unregistered
// tools would remain visible to the agent.
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]Tool
	parent *Registry // non-nil → view mode; tools map holds extras only
}

// NewRegistry creates an empty root tool registry.
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
// For view registries: checks extras first, then delegates to parent.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if ok {
		return t, true
	}
	if r.parent != nil {
		return r.parent.Get(name)
	}
	return nil, false
}

// List returns all registered tools sorted by name.
// For view registries: merges parent tools with extras (extras override parent).
func (r *Registry) List() []Tool {
	if r.parent != nil {
		return r.listView()
	}
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

// listView merges parent tools with this view's extras.
// Extras take precedence over parent tools with the same name.
func (r *Registry) listView() []Tool {
	parentTools := r.parent.List()

	r.mu.RLock()
	extras := make(map[string]Tool, len(r.tools))
	for k, v := range r.tools {
		extras[k] = v
	}
	r.mu.RUnlock()

	// Build merged list: parent tools (excluding overridden) + extras
	result := make([]Tool, 0, len(parentTools)+len(extras))
	for _, t := range parentTools {
		if _, overridden := extras[t.Name()]; !overridden {
			result = append(result, t)
		}
	}
	for _, t := range extras {
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

// WithExtra returns a view of this Registry with additional tools overlaid.
// Used for per-request tool injection (e.g. update_plan with session context).
//
// The returned Registry delegates Get/List to the parent, so changes to the
// parent (via Register/Unregister) are immediately visible through the view.
// Extras take precedence over parent tools with the same name.
//
// Can be chained: root.WithExtra(a).WithExtra(b) creates a view chain where
// lookups check b's extras → a's extras → root's tools.
func (r *Registry) WithExtra(extras ...Tool) *Registry {
	extrasMap := make(map[string]Tool, len(extras))
	for _, t := range extras {
		extrasMap[t.Name()] = t
	}
	return &Registry{
		parent: r,
		tools:  extrasMap,
	}
}
