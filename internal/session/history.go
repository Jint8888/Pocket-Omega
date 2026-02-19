package session

import (
	"fmt"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/llm"
)

// ToMessages converts session turns into an LLM message list.
// It trims the oldest turns until the total character count is within budget.
// budget == 0 means no limit (use with caution).
// At least the most recent turn is always included, even when it exceeds the budget.
func ToMessages(turns []Turn, budget int) []llm.Message {
	if len(turns) == 0 {
		return nil
	}

	start := 0 // first turn index to include

	if budget > 0 {
		// Walk newest-to-oldest, accumulating char count
		total := 0
		for i := len(turns) - 1; i >= 0; i-- {
			cost := len([]rune(turns[i].UserMsg)) + len([]rune(turns[i].Assistant))
			if total+cost > budget {
				start = i + 1
				break
			}
			total += cost
		}
		// Always include at least the newest turn, even when it alone exceeds budget
		if start >= len(turns) {
			start = len(turns) - 1
		}
	}

	msgs := make([]llm.Message, 0, (len(turns)-start)*2)
	for _, t := range turns[start:] {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: t.UserMsg},
			llm.Message{Role: llm.RoleAssistant, Content: t.Assistant},
		)
	}
	return msgs
}

// ToProblemPrefix formats history as a plain-text context preamble,
// used by Agent mode to prepend conversation context to the Problem field.
func ToProblemPrefix(turns []Turn, budget int) string {
	if len(turns) == 0 {
		return ""
	}
	msgs := ToMessages(turns, budget)
	if len(msgs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[对话历史]\n")
	round := 1
	for i := 0; i+1 < len(msgs); i += 2 {
		sb.WriteString(fmt.Sprintf("Round %d - 用户：%s\n", round, truncateRunes(msgs[i].Content, 500)))
		sb.WriteString(fmt.Sprintf("Round %d - 助手：%s\n\n", round, truncateRunes(msgs[i+1].Content, 500)))
		round++
	}
	return sb.String()
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
