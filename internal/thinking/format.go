package thinking

import (
	"fmt"
	"strings"
)

// FormatPlan recursively formats plan steps for display output.
func FormatPlan(steps []PlanStep, indentLevel int) string {
	indent := strings.Repeat("  ", indentLevel)
	var lines []string

	for _, step := range steps {
		line := fmt.Sprintf("%s- [%s] %s", indent, step.Status, step.Description)
		if step.Result != "" {
			line += fmt.Sprintf(": %s", step.Result)
		}
		if step.Mark != "" {
			line += fmt.Sprintf(" (%s)", step.Mark)
		}
		lines = append(lines, line)

		if len(step.SubSteps) > 0 {
			lines = append(lines, FormatPlan(step.SubSteps, indentLevel+1))
		}
	}

	return strings.Join(lines, "\n")
}

// FormatPlanForPrompt creates a simplified plan view for the LLM prompt.
func FormatPlanForPrompt(steps []PlanStep, indentLevel int) string {
	indent := strings.Repeat("  ", indentLevel)
	var lines []string

	for _, step := range steps {
		line := fmt.Sprintf("%s- [%s] %s", indent, step.Status, step.Description)
		lines = append(lines, line)

		if len(step.SubSteps) > 0 {
			lines = append(lines, FormatPlanForPrompt(step.SubSteps, indentLevel+1))
		}
	}

	return strings.Join(lines, "\n")
}
