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
