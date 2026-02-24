package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// dummyTool is a minimal Tool implementation for testing.
type dummyTool struct {
	name string
}

func (d *dummyTool) Name() string                 { return d.name }
func (d *dummyTool) Description() string          { return "test tool" }
func (d *dummyTool) InputSchema() json.RawMessage { return nil }
func (d *dummyTool) Execute(_ context.Context, _ json.RawMessage) (ToolResult, error) {
	return ToolResult{}, nil
}
func (d *dummyTool) Init(_ context.Context) error { return nil }
func (d *dummyTool) Close() error                 { return nil }

func TestRegistry_WithExtra_ContainsBoth(t *testing.T) {
	r := NewRegistry()
	r.Register(&dummyTool{name: "original"})

	extra := &dummyTool{name: "extra"}
	cp := r.WithExtra(extra)

	if _, ok := cp.Get("original"); !ok {
		t.Error("WithExtra copy should contain original tool")
	}
	if _, ok := cp.Get("extra"); !ok {
		t.Error("WithExtra copy should contain extra tool")
	}
}

func TestRegistry_WithExtra_NoMutationOfOriginal(t *testing.T) {
	r := NewRegistry()
	r.Register(&dummyTool{name: "original"})

	r.WithExtra(&dummyTool{name: "extra"})

	if _, ok := r.Get("extra"); ok {
		t.Error("original registry should NOT contain extra tool after WithExtra")
	}
}

func TestRegistry_WithExtra_OverrideExisting(t *testing.T) {
	r := NewRegistry()
	r.Register(&dummyTool{name: "shared"})

	override := &dummyTool{name: "shared"} // same name, different instance
	cp := r.WithExtra(override)

	got, ok := cp.Get("shared")
	if !ok {
		t.Fatal("shared tool should exist")
	}
	// The extra tool should win (be the same pointer as override)
	if got != override {
		t.Error("WithExtra should override existing tool with same name")
	}
}

// TestRegistry_WithExtra_DelegatesUnregister verifies that Unregister on the
// parent registry is immediately visible through a WithExtra view.
// This is the critical fix for the mcp_reload stale-tool bug: the agent holds
// a WithExtra view, and mcp_reload modifies the parent.
func TestRegistry_WithExtra_DelegatesUnregister(t *testing.T) {
	root := NewRegistry()
	root.Register(&dummyTool{name: "mcp_crawl4ai__crawl_url"})
	root.Register(&dummyTool{name: "builtin_tool"})

	view := root.WithExtra(&dummyTool{name: "update_plan"})

	// Before unregister: view sees all tools
	if _, ok := view.Get("mcp_crawl4ai__crawl_url"); !ok {
		t.Fatal("view should see parent tool before unregister")
	}

	// Simulate mcp_reload: unregister from parent
	root.Unregister("mcp_crawl4ai__crawl_url")

	// After unregister: view should NOT see the removed tool
	if _, ok := view.Get("mcp_crawl4ai__crawl_url"); ok {
		t.Error("view should NOT see tool after parent Unregister — stale tool bug!")
	}

	// Other tools should still be visible
	if _, ok := view.Get("builtin_tool"); !ok {
		t.Error("view should still see remaining parent tools")
	}
	if _, ok := view.Get("update_plan"); !ok {
		t.Error("view should still see its own extras")
	}
}

// TestRegistry_WithExtra_DelegatesRegister verifies that Register on the
// parent registry is immediately visible through a WithExtra view.
func TestRegistry_WithExtra_DelegatesRegister(t *testing.T) {
	root := NewRegistry()
	root.Register(&dummyTool{name: "builtin_tool"})

	view := root.WithExtra(&dummyTool{name: "update_plan"})

	// Before register: new tool not visible
	if _, ok := view.Get("mcp_crawl4ai__crawl_url"); ok {
		t.Fatal("tool should not exist before registration")
	}

	// Simulate mcp_reload: register new tool on parent
	root.Register(&dummyTool{name: "mcp_crawl4ai__crawl_url"})

	// After register: view should see the new tool
	if _, ok := view.Get("mcp_crawl4ai__crawl_url"); !ok {
		t.Error("view should see newly registered parent tool")
	}
}

// TestRegistry_WithExtra_ListReflectsParentChanges verifies that List()
// on a view reflects Register/Unregister changes on the parent.
func TestRegistry_WithExtra_ListReflectsParentChanges(t *testing.T) {
	root := NewRegistry()
	root.Register(&dummyTool{name: "old_tool"})
	root.Register(&dummyTool{name: "keep_tool"})

	view := root.WithExtra(&dummyTool{name: "extra"})

	// Initial: 3 tools (old_tool, keep_tool, extra)
	if got := len(view.List()); got != 3 {
		t.Fatalf("expected 3 tools, got %d", got)
	}

	// Remove old_tool from parent
	root.Unregister("old_tool")

	// Now: 2 tools (keep_tool, extra)
	tools := view.List()
	if got := len(tools); got != 2 {
		t.Fatalf("expected 2 tools after unregister, got %d", got)
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	if names["old_tool"] {
		t.Error("List should not include unregistered tool")
	}
	if !names["keep_tool"] || !names["extra"] {
		t.Error("List should include remaining tools")
	}

	// Add new_tool to parent
	root.Register(&dummyTool{name: "new_tool"})

	// Now: 3 tools (keep_tool, new_tool, extra)
	if got := len(view.List()); got != 3 {
		t.Fatalf("expected 3 tools after register, got %d", got)
	}
}

// TestRegistry_WithExtra_ChainedDelegation verifies that chained WithExtra
// calls (root → child → grandchild) properly delegate through the chain.
func TestRegistry_WithExtra_ChainedDelegation(t *testing.T) {
	root := NewRegistry()
	root.Register(&dummyTool{name: "root_tool"})
	root.Register(&dummyTool{name: "mcp_old__search"})

	child := root.WithExtra(&dummyTool{name: "plan_tool"})
	grandchild := child.WithExtra(&dummyTool{name: "walkthrough_tool"})

	// Grandchild sees all tools
	for _, name := range []string{"root_tool", "mcp_old__search", "plan_tool", "walkthrough_tool"} {
		if _, ok := grandchild.Get(name); !ok {
			t.Errorf("grandchild should see %q", name)
		}
	}

	// Unregister from root — should propagate through chain
	root.Unregister("mcp_old__search")

	if _, ok := grandchild.Get("mcp_old__search"); ok {
		t.Error("grandchild should NOT see tool unregistered from root")
	}

	// Register new tool on root — should propagate through chain
	root.Register(&dummyTool{name: "mcp_new__search"})

	if _, ok := grandchild.Get("mcp_new__search"); !ok {
		t.Error("grandchild should see tool registered on root")
	}

	// Extras at each level should still work
	if _, ok := grandchild.Get("plan_tool"); !ok {
		t.Error("grandchild should still see child's extras")
	}
	if _, ok := grandchild.Get("walkthrough_tool"); !ok {
		t.Error("grandchild should still see its own extras")
	}
}
