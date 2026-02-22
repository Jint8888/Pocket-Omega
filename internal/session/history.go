package session

import (
	"fmt"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/util"
)

// ToMessages converts session turns into an LLM message list.
// It trims the oldest turns until the total character count is within budget.
// budget == 0 means no limit (use with caution).
// At least the most recent turn is always included, even when it exceeds the budget.
// If summary is provided, it is prepended as a RoleSystem message.
func ToMessages(turns []Turn, budget int, summary ...string) []llm.Message {
	if len(turns) == 0 && (len(summary) == 0 || summary[0] == "") {
		return nil
	}

	start := 0 // first turn index to include

	if budget > 0 && len(turns) > 0 {
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

	var msgs []llm.Message

	// Prepend summary as system context (not RoleUser — it's historical context)
	if len(summary) > 0 && summary[0] != "" {
		msgs = append(msgs, llm.Message{
			Role:    llm.RoleSystem,
			Content: "[对话历史摘要]\n" + summary[0],
		})
	}

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
// If summary is provided, it is prepended before the turn history.
func ToProblemPrefix(turns []Turn, budget int, summary ...string) string {
	hasSummary := len(summary) > 0 && summary[0] != ""
	if len(turns) == 0 && !hasSummary {
		return ""
	}

	var sb strings.Builder

	// Summary comes before turn history
	if hasSummary {
		sb.WriteString("[对话历史摘要]\n")
		sb.WriteString(summary[0])
		sb.WriteString("\n\n")
	}

	if len(turns) == 0 {
		return sb.String()
	}

	msgs := ToMessages(turns, budget) // no summary here — already injected above
	if len(msgs) == 0 {
		return sb.String()
	}

	sb.WriteString("[对话历史]\n")
	round := 1
	for i := 0; i+1 < len(msgs); i += 2 {
		sb.WriteString(fmt.Sprintf("Round %d - 用户：%s\n", round, util.TruncateRunes(msgs[i].Content, 500)))
		sb.WriteString(fmt.Sprintf("Round %d - 助手：%s\n\n", round, util.TruncateRunes(msgs[i+1].Content, 500)))
		round++
	}
	return sb.String()
}
