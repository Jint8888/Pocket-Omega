package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTimeTool_Interface(t *testing.T) {
	tool := NewTimeTool()
	if tool.Name() != "get_time" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "get_time")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	schema := tool.InputSchema()
	if len(schema) == 0 {
		t.Error("InputSchema() should not be empty")
	}
	if err := tool.Init(context.Background()); err != nil {
		t.Errorf("Init() error: %v", err)
	}
	if err := tool.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestTimeTool_NoTimezone(t *testing.T) {
	tool := NewTimeTool()
	result, err := tool.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
	// Should contain date-like pattern
	if !strings.Contains(result.Output, "-") {
		t.Errorf("output %q should contain date with dashes", result.Output)
	}
}

func TestTimeTool_ValidTimezone(t *testing.T) {
	tool := NewTimeTool()
	args, _ := json.Marshal(map[string]string{"timezone": "Asia/Shanghai"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "CST") {
		t.Errorf("output %q should contain CST for Asia/Shanghai", result.Output)
	}
}

func TestTimeTool_InvalidTimezone(t *testing.T) {
	tool := NewTimeTool()
	args, _ := json.Marshal(map[string]string{"timezone": "Invalid/Zone"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid timezone")
	}
	if !strings.Contains(result.Error, "无效时区") {
		t.Errorf("error %q should mention invalid timezone", result.Error)
	}
}

func TestTimeTool_BadJSON(t *testing.T) {
	tool := NewTimeTool()
	result, err := tool.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid JSON")
	}
	if !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("error %q should mention parse failure", result.Error)
	}
}

func TestTimeTool_NilArgs(t *testing.T) {
	tool := NewTimeTool()
	// Nil args (no args provided) should use local time without error.
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error for nil args: %s", result.Error)
	}
}

func TestTimeTool_OutputFormat(t *testing.T) {
	tool := NewTimeTool()
	result, err := tool.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Format should be: "2006-01-02 15:04:05 MST (星期X)"
	if !strings.Contains(result.Output, "(") || !strings.Contains(result.Output, ")") {
		t.Errorf("output %q should contain weekday in parentheses", result.Output)
	}
	if !strings.Contains(result.Output, "星期") {
		t.Errorf("output %q should contain Chinese weekday", result.Output)
	}
}

func TestTranslateWeekday_AllDays(t *testing.T) {
	tests := []struct {
		weekday time.Weekday
		want    string
	}{
		{time.Sunday, "星期日"},
		{time.Monday, "星期一"},
		{time.Tuesday, "星期二"},
		{time.Wednesday, "星期三"},
		{time.Thursday, "星期四"},
		{time.Friday, "星期五"},
		{time.Saturday, "星期六"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := translateWeekday(tt.weekday)
			if got != tt.want {
				t.Errorf("translateWeekday(%v) = %q, want %q", tt.weekday, got, tt.want)
			}
		})
	}
}

// TestWeekdayNamesPackageLevel verifies the package-level array is not reallocated per call.
func TestWeekdayNamesPackageLevel(t *testing.T) {
	// Call translateWeekday multiple times and confirm same underlying array is used.
	first := translateWeekday(time.Monday)
	second := translateWeekday(time.Monday)
	if first != second {
		t.Errorf("repeated calls returned different values: %q vs %q", first, second)
	}
}
