package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/walkthrough"
)

func TestWalkthrough_Add(t *testing.T) {
	store := walkthrough.NewStore()
	tool := NewWalkthroughTool(store, "s1")
	args, _ := json.Marshal(walkthroughArgs{Operation: "add", Content: "key finding"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "📌") {
		t.Errorf("expected pinned confirmation, got: %s", result.Output)
	}
	entries := store.Get("s1")
	if len(entries) != 1 || entries[0].Content != "key finding" || entries[0].Source != walkthrough.SourceManual {
		t.Errorf("unexpected entry: %+v", entries)
	}
}

func TestWalkthrough_AddEmpty(t *testing.T) {
	store := walkthrough.NewStore()
	tool := NewWalkthroughTool(store, "s1")
	args, _ := json.Marshal(walkthroughArgs{Operation: "add", Content: ""})
	result, _ := tool.Execute(context.Background(), args)
	if result.Error == "" {
		t.Error("expected error for empty content")
	}
}

func TestWalkthrough_List(t *testing.T) {
	store := walkthrough.NewStore()
	store.Append("s1", walkthrough.Entry{StepNumber: 1, Source: walkthrough.SourceAuto, Content: "found config"})
	tool := NewWalkthroughTool(store, "s1")
	args, _ := json.Marshal(walkthroughArgs{Operation: "list"})
	result, _ := tool.Execute(context.Background(), args)
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "备忘录") {
		t.Errorf("expected rendered output, got: %s", result.Output)
	}
}

func TestWalkthrough_ListEmpty(t *testing.T) {
	store := walkthrough.NewStore()
	tool := NewWalkthroughTool(store, "s1")
	args, _ := json.Marshal(walkthroughArgs{Operation: "list"})
	result, _ := tool.Execute(context.Background(), args)
	if !strings.Contains(result.Output, "备忘录为空") {
		t.Errorf("expected empty message, got: %s", result.Output)
	}
}

func TestWalkthrough_InvalidOp(t *testing.T) {
	store := walkthrough.NewStore()
	tool := NewWalkthroughTool(store, "s1")
	args, _ := json.Marshal(walkthroughArgs{Operation: "remove"})
	result, _ := tool.Execute(context.Background(), args)
	if result.Error == "" {
		t.Error("expected error for invalid operation")
	}
}

func TestWalkthrough_Truncation(t *testing.T) {
	store := walkthrough.NewStore()
	tool := NewWalkthroughTool(store, "s1")
	longContent := strings.Repeat("中", 250) // 250 runes > 200 limit
	args, _ := json.Marshal(walkthroughArgs{Operation: "add", Content: longContent})
	result, _ := tool.Execute(context.Background(), args)
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	entries := store.Get("s1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	runes := []rune(entries[0].Content)
	if len(runes) != 201 { // 200 + "…"
		t.Errorf("expected 201 runes after truncation, got %d", len(runes))
	}
}
