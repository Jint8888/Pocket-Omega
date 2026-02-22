package agent

import (
	"strings"
	"testing"
)

func TestContextGuard_OK(t *testing.T) {
	g := NewContextGuard(1000)
	// ~50 tokens worth of content (well below 70%)
	content := strings.Repeat("a", 200)
	if status := g.CheckTokens(estimateTokens(content)); status != ContextOK {
		t.Errorf("expected ContextOK, got %d", status)
	}
}

func TestContextGuard_Warning(t *testing.T) {
	g := NewContextGuard(100)
	// Need ~70-84 tokens: 70 tokens * 4 chars/token = 280 ASCII chars
	content := strings.Repeat("a", 300)
	status := g.CheckTokens(estimateTokens(content))
	if status != ContextWarning {
		t.Errorf("expected ContextWarning for ~75%% usage, got %d", status)
	}
}

func TestContextGuard_Critical(t *testing.T) {
	g := NewContextGuard(100)
	// Need ≥85 tokens: 85 tokens * 4 chars/token = 340 ASCII chars
	content := strings.Repeat("a", 360)
	status := g.CheckTokens(estimateTokens(content))
	if status != ContextCritical {
		t.Errorf("expected ContextCritical for ~90%% usage, got %d", status)
	}
}

func TestContextGuard_Disabled(t *testing.T) {
	g := NewContextGuard(0) // disabled
	content := strings.Repeat("a", 99999)
	if status := g.CheckTokens(estimateTokens(content)); status != ContextOK {
		t.Errorf("disabled guard should always return ContextOK, got %d", status)
	}
}

func TestContextGuard_CJKContent(t *testing.T) {
	g := NewContextGuard(100)
	// 170 CJK chars → ~85 tokens + 1 = 86 → 86% → Critical
	content := strings.Repeat("中", 170)
	status := g.CheckTokens(estimateTokens(content))
	if status != ContextCritical {
		t.Errorf("expected ContextCritical for CJK content at ~86%%, got %d", status)
	}
}
