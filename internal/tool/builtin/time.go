package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// TimeTool returns the current time with optional timezone support.
type TimeTool struct{}

func NewTimeTool() *TimeTool { return &TimeTool{} }

func (t *TimeTool) Name() string        { return "get_time" }
func (t *TimeTool) Description() string { return "获取当前时间，可指定时区" }

func (t *TimeTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "timezone", Type: "string", Description: "IANA 时区名，如 Asia/Shanghai（可选）", Required: false},
	)
}

func (t *TimeTool) Init(_ context.Context) error { return nil }
func (t *TimeTool) Close() error                 { return nil }

type timeArgs struct {
	Timezone string `json:"timezone"`
}

func (t *TimeTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a timeArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
		}
	}

	now := time.Now()

	if a.Timezone != "" {
		loc, err := time.LoadLocation(a.Timezone)
		if err != nil {
			return tool.ToolResult{Error: fmt.Sprintf("无效时区 %q: %v", a.Timezone, err)}, nil
		}
		now = now.In(loc)
	}

	weekday := translateWeekday(now.Weekday())
	output := fmt.Sprintf("%s (%s)", now.Format("2006-01-02 15:04:05 MST"), weekday)

	return tool.ToolResult{Output: output}, nil
}

// weekdayNames maps time.Weekday (Sunday=0) to Chinese names.
// Defined at package level to avoid per-call slice allocation.
var weekdayNames = [7]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}

func translateWeekday(w time.Weekday) string {
	return weekdayNames[w]
}
