package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/session"
)

// mockLLMProvider implements llm.LLMProvider for testing cmdCompact.
type mockLLMProvider struct {
	response llm.Message
	err      error
	lastMsgs []llm.Message
}

func (m *mockLLMProvider) CallLLM(ctx context.Context, messages []llm.Message) (llm.Message, error) {
	m.lastMsgs = messages
	return m.response, m.err
}
func (m *mockLLMProvider) CallLLMStream(ctx context.Context, messages []llm.Message, onChunk llm.StreamCallback) (llm.Message, error) {
	return m.CallLLM(ctx, messages)
}
func (m *mockLLMProvider) CallLLMWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (llm.Message, error) {
	return m.CallLLM(ctx, messages)
}
func (m *mockLLMProvider) IsToolCallingEnabled() bool { return false }

func newTestCommandHandler(t *testing.T) *CommandHandler {
	t.Helper()
	h := NewCommandHandler(CommandHandlerOptions{
		Store: session.NewStore(time.Minute, 10),
	})
	t.Cleanup(func() { h.store.Close() })
	return h
}

func doCommand(t *testing.T, h *CommandHandler, method string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, "/api/command", &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleCommand(w, req)
	return w
}

func TestHandleCommand_Reload_OK(t *testing.T) {
	h := newTestCommandHandler(t)
	w := doCommand(t, h, http.MethodPost, commandRequest{Command: "reload"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result commandResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !result.OK {
		t.Errorf("expected ok=true, got %+v", result)
	}
}

func TestHandleCommand_Clear_DeletesSession(t *testing.T) {
	h := newTestCommandHandler(t)
	sid := "test-session-123"
	h.store.AppendTurn(sid, session.Turn{UserMsg: "hello", Assistant: "hi"})
	if turns, _ := h.store.GetSessionContext(sid); len(turns) == 0 {
		t.Fatal("session should exist before clear")
	}

	w := doCommand(t, h, http.MethodPost, commandRequest{Command: "clear", SessionID: sid})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result commandResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !result.OK || result.Action != "clear_chat" {
		t.Errorf("expected ok=true action=clear_chat, got %+v", result)
	}

	if turns, _ := h.store.GetSessionContext(sid); len(turns) != 0 {
		t.Error("session should be deleted after clear")
	}
}

func TestHandleCommand_Help(t *testing.T) {
	h := newTestCommandHandler(t)
	w := doCommand(t, h, http.MethodPost, commandRequest{Command: "help"})
	var result commandResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !result.OK {
		t.Errorf("expected ok=true, got %+v", result)
	}
	for _, keyword := range []string{"/reload", "/clear", "/help", "/compact"} {
		if !strings.Contains(result.Message, keyword) {
			t.Errorf("help message missing %q", keyword)
		}
	}
}

func TestHandleCommand_Unknown(t *testing.T) {
	h := newTestCommandHandler(t)
	w := doCommand(t, h, http.MethodPost, commandRequest{Command: "nonexistent"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result commandResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.OK {
		t.Error("unknown command should return ok=false")
	}
}

func TestHandleCommand_MethodNotAllowed(t *testing.T) {
	h := newTestCommandHandler(t)
	w := doCommand(t, h, http.MethodGet, nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ── /compact command tests ──

func TestCmdCompact_TooFewTurns(t *testing.T) {
	store := session.NewStore(time.Minute, 10)
	defer store.Close()
	sid := "test-few"
	store.AppendTurn(sid, session.Turn{UserMsg: "q1", Assistant: "a1"})

	h := NewCommandHandler(CommandHandlerOptions{
		Store:       store,
		LLMProvider: &mockLLMProvider{},
	})

	result := h.cmdCompact(context.Background(), "", sid)
	if !result.OK {
		t.Errorf("expected OK, got %+v", result)
	}
	if !strings.Contains(result.Message, "无需压缩") {
		t.Errorf("unexpected message: %s", result.Message)
	}
}

func TestCmdCompact_ArgsParseKeepN(t *testing.T) {
	store := session.NewStore(time.Minute, 10)
	defer store.Close()
	sid := "test-args"
	for i := 0; i < 5; i++ {
		store.AppendTurn(sid, session.Turn{UserMsg: "q", Assistant: "a"})
	}

	mock := &mockLLMProvider{response: llm.Message{Content: "compact summary"}}
	h := NewCommandHandler(CommandHandlerOptions{
		Store:       store,
		LLMProvider: mock,
	})

	result := h.cmdCompact(context.Background(), "3", sid)
	if !result.OK {
		t.Fatalf("expected OK, got %+v", result)
	}

	turns, _ := store.GetSessionContext(sid)
	if len(turns) != 3 {
		t.Errorf("expected 3 remaining turns after /compact 3, got %d", len(turns))
	}
}

func TestCmdCompact_MergesExistingSummary(t *testing.T) {
	store := session.NewStore(time.Minute, 10)
	defer store.Close()
	sid := "test-merge"

	for i := 0; i < 5; i++ {
		store.AppendTurn(sid, session.Turn{UserMsg: "old", Assistant: "ans"})
	}
	store.Compact(sid, "first summary", 2)

	for i := 0; i < 3; i++ {
		store.AppendTurn(sid, session.Turn{UserMsg: "new", Assistant: "resp"})
	}

	mock := &mockLLMProvider{response: llm.Message{Content: "merged summary v2"}}
	h := NewCommandHandler(CommandHandlerOptions{
		Store:       store,
		LLMProvider: mock,
	})

	result := h.cmdCompact(context.Background(), "", sid)
	if !result.OK {
		t.Fatalf("expected OK, got %+v", result)
	}

	if len(mock.lastMsgs) == 0 {
		t.Fatal("expected LLM to be called")
	}
	prompt := mock.lastMsgs[0].Content
	if !strings.Contains(prompt, "first summary") {
		t.Error("LLM prompt should contain existing summary for merging")
	}
	if !strings.Contains(prompt, "已有历史摘要") {
		t.Error("LLM prompt should contain merge instruction")
	}
}

func TestCmdCompact_NoLLMProvider(t *testing.T) {
	store := session.NewStore(time.Minute, 10)
	defer store.Close()
	sid := "test-nollm"
	for i := 0; i < 5; i++ {
		store.AppendTurn(sid, session.Turn{UserMsg: "q", Assistant: "a"})
	}

	h := NewCommandHandler(CommandHandlerOptions{Store: store})

	result := h.cmdCompact(context.Background(), "", sid)
	if result.OK {
		t.Errorf("expected NOT OK when LLMProvider is nil, got %+v", result)
	}
	if !strings.Contains(result.Message, "LLM 未配置") {
		t.Errorf("expected LLM error message, got: %s", result.Message)
	}
}

func TestCmdCompact_NoSession(t *testing.T) {
	h := NewCommandHandler(CommandHandlerOptions{})
	result := h.cmdCompact(context.Background(), "", "")
	if result.OK {
		t.Errorf("expected NOT OK for empty session, got %+v", result)
	}
}

func TestCmdCompact_KeepZero(t *testing.T) {
	store := session.NewStore(time.Minute, 10)
	defer store.Close()
	sid := "test-keep0"
	for i := 0; i < 4; i++ {
		store.AppendTurn(sid, session.Turn{UserMsg: "q", Assistant: "a"})
	}

	mock := &mockLLMProvider{response: llm.Message{Content: "all turns summarized"}}
	h := NewCommandHandler(CommandHandlerOptions{
		Store:       store,
		LLMProvider: mock,
	})

	result := h.cmdCompact(context.Background(), "0", sid)
	if !result.OK {
		t.Fatalf("expected OK, got %+v", result)
	}

	turns, summary := store.GetSessionContext(sid)
	if len(turns) != 0 {
		t.Errorf("expected 0 remaining turns after /compact 0, got %d", len(turns))
	}
	if summary != "all turns summarized" {
		t.Errorf("unexpected summary: %q", summary)
	}
}
