package agent

import "testing"

func TestEstimateTokens_Chinese(t *testing.T) {
	// 10 CJK chars → ~5 tokens + 1 = 6
	text := "你好世界测试估算中文字"
	got := estimateTokens(text)
	if got < 4 || got > 8 {
		t.Errorf("expected ~5-6 tokens for 10 CJK chars, got %d", got)
	}
}

func TestEstimateTokens_English(t *testing.T) {
	// "hello world" = 11 chars → ~3 tokens + 1 = 4
	text := "hello world"
	got := estimateTokens(text)
	if got < 2 || got > 6 {
		t.Errorf("expected ~3-4 tokens for 'hello world', got %d", got)
	}
}

func TestEstimateTokens_Mixed(t *testing.T) {
	// "你好 world" = 2 CJK + 1 space + 5 ASCII = 2/2 + 6/4 + 1 = 1+1+1 = 3
	text := "你好 world"
	got := estimateTokens(text)
	if got < 2 || got > 6 {
		t.Errorf("expected ~3 tokens for mixed, got %d", got)
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	got := estimateTokens("")
	if got != 1 {
		t.Errorf("expected 1 for empty string (floor), got %d", got)
	}
}
