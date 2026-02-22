package web

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/session"
	"github.com/pocketomega/pocket-omega/internal/util"
)

// buildCompactSummary generates a summary of older turns using the LLM.
// Merges existing summary if present. Shared by cmdCompact and OnContextOverflow.
func buildCompactSummary(
	ctx context.Context,
	provider llm.LLMProvider,
	turns []session.Turn,
	existingSummary string,
	keepN int,
) (string, error) {
	if len(turns) <= keepN {
		return existingSummary, nil
	}

	oldTurns := turns[:len(turns)-keepN]

	var sb strings.Builder
	sb.WriteString("请将以下对话内容压缩为一段简洁的摘要（200字以内），")
	sb.WriteString("保留关键事实、决策和未完成事项：\n\n")

	if existingSummary != "" {
		sb.WriteString("## 已有历史摘要\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n\n## 需要合并的新对话\n\n")
	}

	for i, t := range oldTurns {
		sb.WriteString(fmt.Sprintf("Round %d:\n用户: %s\n助手: %s\n\n",
			i+1,
			util.TruncateRunes(t.UserMsg, 500),
			util.TruncateRunes(t.Assistant, 500)))
	}

	// Apply 60s timeout for LLM call. When called from OnContextOverflow the outer
	// ctx already has a 60s timeout (set in Post()), but cmdCompact passes r.Context()
	// which may have no deadline — so this inner timeout is the primary safeguard.
	llmCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := provider.CallLLM(llmCtx, []llm.Message{
		{Role: llm.RoleUser, Content: sb.String()},
	})
	if err != nil {
		return "", fmt.Errorf("summary generation failed: %w", err)
	}

	return resp.Content, nil
}
