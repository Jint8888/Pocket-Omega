package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	shellTimeout   = 30 * time.Second
	maxOutputChars = 8000
)

// dangerousPatterns are command patterns that are blocked for safety.
// These are checked case-insensitively against the command string.
var dangerousPatterns = []string{
	"rm -rf /",
	"rm -rf /*",
	"mkfs",
	"dd if=",
	"format c:",
	"format d:",
	":(){:|:&};:", // fork bomb
	"shutdown",
	"reboot",
	"halt",
	"init 0",
	"init 6",
	"del /s /q c:\\",
	"rd /s /q c:\\",
}

// ShellTool executes shell commands with timeout and output limits.
type ShellTool struct {
	workspaceDir string
	enabled      bool
}

// NewShellTool creates a shell tool. Set enabled=false to disable execution.
func NewShellTool(workspaceDir string, enabled bool) *ShellTool {
	return &ShellTool{
		workspaceDir: workspaceDir,
		enabled:      enabled,
	}
}

func (t *ShellTool) Name() string        { return "shell_exec" }
func (t *ShellTool) Description() string { return "执行 Shell 命令并返回输出" }

func (t *ShellTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "command", Type: "string", Description: "要执行的命令", Required: true},
	)
}

func (t *ShellTool) Init(_ context.Context) error { return nil }
func (t *ShellTool) Close() error                 { return nil }

type shellArgs struct {
	Command string `json:"command"`
}

func (t *ShellTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	if !t.enabled {
		return tool.ToolResult{Error: "shell_exec 工具已禁用"}, nil
	}

	var a shellArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	if a.Command == "" {
		return tool.ToolResult{Error: "command 参数不能为空"}, nil
	}

	// Check command against blacklist
	cmdLower := strings.ToLower(a.Command)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, pattern) {
			return tool.ToolResult{Error: fmt.Sprintf("安全限制: 命令包含危险模式 %q", pattern)}, nil
		}
	}

	// Create command with timeout
	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}

	if t.workspaceDir != "" {
		cmd.Dir = t.workspaceDir
	}

	// Capture stdout + stderr
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	// Truncate if too long (rune-safe)
	outStr = safeRuneTruncate(outStr, maxOutputChars)
	outStr = strings.TrimSpace(outStr)

	if err != nil {
		if ctx.Err() != nil {
			return tool.ToolResult{Error: fmt.Sprintf("命令超时 (%v): %s", shellTimeout, outStr)}, nil
		}
		return tool.ToolResult{Output: outStr, Error: fmt.Sprintf("命令退出错误: %v", err)}, nil
	}

	return tool.ToolResult{Output: outStr}, nil
}

// safeRuneTruncate truncates a string to maxRunes runes in a single pass,
// preserving valid UTF-8 without extra allocations for non-truncated strings.
func safeRuneTruncate(s string, maxRunes int) string {
	count := 0
	for i := range s {
		count++
		if count > maxRunes {
			return s[:i] + fmt.Sprintf("\n... (输出截断，共 %d 字符)", utf8.RuneCountInString(s))
		}
	}
	return s
}
