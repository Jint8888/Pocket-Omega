package thinking_test

import (
	"strings"
	"testing"

	"github.com/pocketomega/pocket-omega/internal/thinking"
)

// ── ExtractYAML tests ──

func TestExtractYAML_WithYAMLFence(t *testing.T) {
	input := "```yaml\nkey: value\n```"
	got, err := thinking.ExtractYAML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "key: value" {
		t.Errorf("expected %q, got %q", "key: value", got)
	}
}

func TestExtractYAML_WithGenericFence(t *testing.T) {
	input := "```\nkey: value\n```"
	got, err := thinking.ExtractYAML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "key: value" {
		t.Errorf("expected %q, got %q", "key: value", got)
	}
}

func TestExtractYAML_NoFence_ReturnsRaw(t *testing.T) {
	input := "key: value"
	got, err := thinking.ExtractYAML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "key: value" {
		t.Errorf("expected %q, got %q", "key: value", got)
	}
}

func TestExtractYAML_UnclosedYAMLFence_ReturnsError(t *testing.T) {
	input := "```yaml\nkey: value"
	_, err := thinking.ExtractYAML(input)
	if err == nil {
		t.Error("expected error for unclosed ```yaml block, got nil")
	}
}

func TestExtractYAML_UnclosedGenericFence_ReturnsError(t *testing.T) {
	input := "```\nkey: value"
	_, err := thinking.ExtractYAML(input)
	if err == nil {
		t.Error("expected error for unclosed ``` block, got nil")
	}
}

func TestExtractYAML_PrefersYAMLFenceOverGeneric(t *testing.T) {
	// When both ```yaml and ``` appear, the yaml fence should win
	input := "```yaml\nfirst: yaml\n```\n```\nsecond: generic\n```"
	got, err := thinking.ExtractYAML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "first: yaml") {
		t.Errorf("expected yaml fence content, got %q", got)
	}
}

func TestExtractYAML_TrimsWhitespace(t *testing.T) {
	input := "```yaml\n\n  key: value  \n\n```"
	got, err := thinking.ExtractYAML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "key: value" {
		t.Errorf("expected trimmed content, got %q", got)
	}
}

// ── FormatPlan tests ──

func TestFormatPlan_EmptySteps(t *testing.T) {
	got := thinking.FormatPlan(nil, 0)
	if got != "" {
		t.Errorf("expected empty string for nil steps, got %q", got)
	}
}

func TestFormatPlan_SingleStep(t *testing.T) {
	steps := []thinking.PlanStep{
		{Description: "理解问题", Status: "Pending"},
	}
	got := thinking.FormatPlan(steps, 0)
	if !strings.Contains(got, "[Pending]") {
		t.Errorf("expected [Pending] in output, got %q", got)
	}
	if !strings.Contains(got, "理解问题") {
		t.Errorf("expected step description in output, got %q", got)
	}
}

func TestFormatPlan_StepWithResult(t *testing.T) {
	steps := []thinking.PlanStep{
		{Description: "结论", Status: "Done", Result: "答案是42"},
	}
	got := thinking.FormatPlan(steps, 0)
	if !strings.Contains(got, "答案是42") {
		t.Errorf("expected result in output, got %q", got)
	}
}

func TestFormatPlan_StepWithMark(t *testing.T) {
	steps := []thinking.PlanStep{
		{Description: "验证", Status: "Done", Mark: "⚠️"},
	}
	got := thinking.FormatPlan(steps, 0)
	if !strings.Contains(got, "⚠️") {
		t.Errorf("expected mark in output, got %q", got)
	}
}

func TestFormatPlan_IndentedSubSteps(t *testing.T) {
	steps := []thinking.PlanStep{
		{
			Description: "父步骤",
			Status:      "Pending",
			SubSteps: []thinking.PlanStep{
				{Description: "子步骤", Status: "Pending"},
			},
		},
	}
	got := thinking.FormatPlan(steps, 0)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines for parent+child, got: %q", got)
	}
	// Sub-step line should have more leading spaces than parent
	parentIndent := len(lines[0]) - len(strings.TrimLeft(lines[0], " "))
	childIndent := len(lines[1]) - len(strings.TrimLeft(lines[1], " "))
	if childIndent <= parentIndent {
		t.Errorf("expected child indented more than parent (child=%d, parent=%d)", childIndent, parentIndent)
	}
}

func TestFormatPlan_MultipleSteps(t *testing.T) {
	steps := []thinking.PlanStep{
		{Description: "步骤一", Status: "Done"},
		{Description: "步骤二", Status: "Pending"},
		{Description: "步骤三", Status: "Pending"},
	}
	got := thinking.FormatPlan(steps, 0)
	if !strings.Contains(got, "步骤一") || !strings.Contains(got, "步骤二") || !strings.Contains(got, "步骤三") {
		t.Errorf("expected all steps in output, got %q", got)
	}
}

// ── FormatPlanForPrompt tests ──

func TestFormatPlanForPrompt_OmitsResultAndMark(t *testing.T) {
	steps := []thinking.PlanStep{
		{Description: "结论", Status: "Done", Result: "秘密答案", Mark: "🔥"},
	}
	got := thinking.FormatPlanForPrompt(steps, 0)
	if strings.Contains(got, "秘密答案") {
		t.Errorf("FormatPlanForPrompt should omit Result, got %q", got)
	}
	if strings.Contains(got, "🔥") {
		t.Errorf("FormatPlanForPrompt should omit Mark, got %q", got)
	}
}

func TestFormatPlanForPrompt_IncludesStatusAndDescription(t *testing.T) {
	steps := []thinking.PlanStep{
		{Description: "制定方案", Status: "Pending"},
	}
	got := thinking.FormatPlanForPrompt(steps, 0)
	if !strings.Contains(got, "Pending") || !strings.Contains(got, "制定方案") {
		t.Errorf("expected Status and Description in FormatPlanForPrompt output, got %q", got)
	}
}
