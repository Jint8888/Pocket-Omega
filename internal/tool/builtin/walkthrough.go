package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/pocketomega/pocket-omega/internal/tool"
	"github.com/pocketomega/pocket-omega/internal/walkthrough"
)

// WalkthroughTool allows the agent to record or view execution memos.
// Each request gets its own instance (via NewWalkthroughTool) with session context.
type WalkthroughTool struct {
	store     *walkthrough.Store
	sessionID string
}

// NewWalkthroughTool creates a per-request instance with session context.
func NewWalkthroughTool(store *walkthrough.Store, sessionID string) *WalkthroughTool {
	return &WalkthroughTool{store: store, sessionID: sessionID}
}

func (t *WalkthroughTool) Name() string { return "walkthrough" }
func (t *WalkthroughTool) Description() string {
	return "记录或查看执行备忘录。add: 记录关键发现（将被保留不会被自动淘汰）；list: 查看当前备忘录"
}

func (t *WalkthroughTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "operation", Type: "string", Description: "操作：add 记录关键发现，list 查看备忘录", Required: true},
		tool.SchemaParam{Name: "content", Type: "string", Description: "备忘内容（operation=add 时必填，最多 200 字符）", Required: false},
	)
}

func (t *WalkthroughTool) Init(_ context.Context) error { return nil }
func (t *WalkthroughTool) Close() error                 { return nil }

const maxContentRunes = 200

type walkthroughArgs struct {
	Operation string `json:"operation"`
	Content   string `json:"content"`
}

func (t *WalkthroughTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a walkthroughArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	switch a.Operation {
	case "add":
		if a.Content == "" {
			return tool.ToolResult{Error: "add 操作需要非空 content"}, nil
		}
		// Truncate to maxContentRunes
		content := a.Content
		if utf8.RuneCountInString(content) > maxContentRunes {
			runes := []rune(content)
			content = string(runes[:maxContentRunes]) + "…"
		}
		t.store.Append(t.sessionID, walkthrough.Entry{
			Source:  walkthrough.SourceManual,
			Content: content,
		})
		return tool.ToolResult{Output: "📌 已记录"}, nil

	case "list":
		rendered := t.store.Render(t.sessionID)
		if rendered == "" {
			return tool.ToolResult{Output: "备忘录为空"}, nil
		}
		return tool.ToolResult{Output: rendered}, nil

	default:
		return tool.ToolResult{Error: fmt.Sprintf("未知操作 %q，支持 add/list", a.Operation)}, nil
	}
}
