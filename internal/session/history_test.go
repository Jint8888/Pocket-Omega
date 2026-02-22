package session

import (
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/llm"
)

func TestToMessages_Empty(t *testing.T) {
	msgs := ToMessages(nil, 0)
	if msgs != nil {
		t.Errorf("expected nil for empty turns, got %v", msgs)
	}
	msgs = ToMessages([]Turn{}, 0)
	if msgs != nil {
		t.Errorf("expected nil for empty slice, got %v", msgs)
	}
}

func TestToMessages_NoBudget(t *testing.T) {
	turns := []Turn{
		{UserMsg: "q1", Assistant: "a1"},
		{UserMsg: "q2", Assistant: "a2"},
	}
	msgs := ToMessages(turns, 0) // budget=0 means no limit
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (2 turns × 2), got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser || msgs[0].Content != "q1" {
		t.Errorf("unexpected msg[0]: %+v", msgs[0])
	}
	if msgs[1].Role != llm.RoleAssistant || msgs[1].Content != "a1" {
		t.Errorf("unexpected msg[1]: %+v", msgs[1])
	}
}

func TestToMessages_WithBudget(t *testing.T) {
	// Each turn costs len(UserMsg)+len(Assistant) runes.
	// Turn 1: "AAAA" + "BBBB" = 8 runes
	// Turn 2: "CCCC" + "DDDD" = 8 runes
	// budget=10 → only the newest turn (turn 2) fits
	turns := []Turn{
		{UserMsg: "AAAA", Assistant: "BBBB"},
		{UserMsg: "CCCC", Assistant: "DDDD"},
	}
	msgs := ToMessages(turns, 10)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (1 turn), got %d", len(msgs))
	}
	if msgs[0].Content != "CCCC" {
		t.Errorf("expected newest turn user msg 'CCCC', got %q", msgs[0].Content)
	}
}

func TestToMessages_RoleAssignment(t *testing.T) {
	turns := []Turn{{UserMsg: "u", Assistant: "a"}}
	msgs := ToMessages(turns, 0)
	if msgs[0].Role != llm.RoleUser {
		t.Errorf("expected RoleUser, got %q", msgs[0].Role)
	}
	if msgs[1].Role != llm.RoleAssistant {
		t.Errorf("expected RoleAssistant, got %q", msgs[1].Role)
	}
}

func TestToProblemPrefix_Format(t *testing.T) {
	turns := []Turn{
		{UserMsg: "问题一", Assistant: "答案一"},
		{UserMsg: "问题二", Assistant: "答案二"},
	}
	prefix := ToProblemPrefix(turns, 0)

	if !strings.Contains(prefix, "[对话历史]") {
		t.Error("prefix missing '[对话历史]' header")
	}
	if !strings.Contains(prefix, "Round 1 - 用户：问题一") {
		t.Error("prefix missing 'Round 1 - 用户：问题一'")
	}
	if !strings.Contains(prefix, "Round 1 - 助手：答案一") {
		t.Error("prefix missing 'Round 1 - 助手：答案一'")
	}
	if !strings.Contains(prefix, "Round 2 - 用户：问题二") {
		t.Error("prefix missing 'Round 2 - 用户：问题二'")
	}
}

func TestToProblemPrefix_Truncation(t *testing.T) {
	// Build a message that exceeds 500 runes
	long := strings.Repeat("甲", 600) // 600 runes
	turns := []Turn{{UserMsg: long, Assistant: long}}
	prefix := ToProblemPrefix(turns, 0)

	// The output should contain "..." indicating truncation
	if !strings.Contains(prefix, "...") {
		t.Error("expected truncation marker '...' for >500 rune content")
	}
}

func TestToProblemPrefix_Empty(t *testing.T) {
	prefix := ToProblemPrefix(nil, 0)
	if prefix != "" {
		t.Errorf("expected empty string for nil turns, got %q", prefix)
	}
	prefix = ToProblemPrefix([]Turn{}, 0)
	if prefix != "" {
		t.Errorf("expected empty string for empty turns, got %q", prefix)
	}
}

// ── Summary injection tests ──

func TestToMessages_WithSummary(t *testing.T) {
	turns := []Turn{{UserMsg: "q1", Assistant: "a1"}}
	msgs := ToMessages(turns, 0, "摘要内容")

	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (1 summary + 1 turn × 2), got %d", len(msgs))
	}
	// First message should be summary with RoleSystem
	if msgs[0].Role != llm.RoleSystem {
		t.Errorf("expected summary role to be system, got %q", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "摘要内容") {
		t.Errorf("expected summary content, got %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "[对话历史摘要]") {
		t.Errorf("expected summary header, got %q", msgs[0].Content)
	}
	// Remaining should be the turn
	if msgs[1].Role != llm.RoleUser || msgs[1].Content != "q1" {
		t.Errorf("unexpected msg[1]: %+v", msgs[1])
	}
}

func TestToMessages_WithSummaryNoTurns(t *testing.T) {
	msgs := ToMessages(nil, 0, "orphan summary")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (summary only), got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Errorf("expected RoleSystem, got %q", msgs[0].Role)
	}
}

func TestToProblemPrefix_WithSummary(t *testing.T) {
	turns := []Turn{{UserMsg: "q", Assistant: "a"}}
	prefix := ToProblemPrefix(turns, 0, "历史摘要")

	// Summary should appear before turn history
	summaryIdx := strings.Index(prefix, "[对话历史摘要]")
	historyIdx := strings.Index(prefix, "[对话历史]")
	if summaryIdx < 0 || historyIdx < 0 {
		t.Fatalf("missing headers: summary=%d history=%d", summaryIdx, historyIdx)
	}
	if summaryIdx >= historyIdx {
		t.Errorf("summary should appear before history, got summary=%d history=%d", summaryIdx, historyIdx)
	}
	if !strings.Contains(prefix, "历史摘要") {
		t.Error("prefix missing summary content")
	}
}

func TestToProblemPrefix_SummaryOnly(t *testing.T) {
	prefix := ToProblemPrefix(nil, 0, "只有摘要")
	if !strings.Contains(prefix, "只有摘要") {
		t.Errorf("expected summary content in prefix, got %q", prefix)
	}
	if strings.Contains(prefix, "[对话历史]") {
		t.Error("should not have turn history header with no turns")
	}
}
