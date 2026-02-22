package agent

import (
	"os"
	"testing"
)

func TestLoadMaxSteps_Default(t *testing.T) {
	os.Unsetenv("AGENT_MAX_STEPS")
	if got := loadMaxSteps(); got != 40 {
		t.Errorf("expected default 40, got %d", got)
	}
}

func TestLoadMaxSteps_Custom(t *testing.T) {
	os.Setenv("AGENT_MAX_STEPS", "60")
	defer os.Unsetenv("AGENT_MAX_STEPS")
	if got := loadMaxSteps(); got != 60 {
		t.Errorf("expected 60, got %d", got)
	}
}

func TestLoadMaxSteps_BelowMin(t *testing.T) {
	os.Setenv("AGENT_MAX_STEPS", "3")
	defer os.Unsetenv("AGENT_MAX_STEPS")
	if got := loadMaxSteps(); got != 40 {
		t.Errorf("expected fallback 40, got %d", got)
	}
}

func TestLoadMaxSteps_AboveMax(t *testing.T) {
	os.Setenv("AGENT_MAX_STEPS", "999")
	defer os.Unsetenv("AGENT_MAX_STEPS")
	if got := loadMaxSteps(); got != 40 {
		t.Errorf("expected fallback 40, got %d", got)
	}
}

func TestLoadMaxSteps_Invalid(t *testing.T) {
	os.Setenv("AGENT_MAX_STEPS", "abc")
	defer os.Unsetenv("AGENT_MAX_STEPS")
	if got := loadMaxSteps(); got != 40 {
		t.Errorf("expected fallback 40, got %d", got)
	}
}
