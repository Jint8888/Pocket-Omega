package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
// NOTE: This is a best-effort blocklist, not a security boundary.
// Determined attackers can bypass it (e.g. base64-encoded payloads, find -delete).
// The primary purpose is preventing accidental damage from LLM-generated commands.
var dangerousPatterns = []string{
	// Linux destructive deletion (various flag combos)
	// "rm -rf /*" is intentionally omitted: "rm -rf /" is already a substring of it.
	"rm -rf /",
	"rm -r -f /",
	"rm --recursive",
	"rm -rf ~",
	"rm -rf $home",
	"rm -rf ${home}",
	// POSIX -- separator bypass (rm -rf -- / is equivalent to rm -rf /)
	"rm -rf -- /",
	"rm -r -f -- /",
	// Filesystem destruction
	"mkfs",
	"dd if=",
	// System control
	"shutdown",
	"reboot",
	"halt",
	"init 0",
	"init 6",
	"systemctl poweroff",
	"systemctl halt",
	// Process killing
	"pkill -9",
	// Permission destruction
	"chmod -r 000 /",
	// Fork bomb
	":(){:|:&};:",
	// Windows destructive commands
	"format c:",
	"format d:",
	"del /s /q c:\\",
	"del /s /q d:\\",
	"rd /s /q c:\\",
	"rd /s /q d:\\",
	"remove-item -recurse c:",
	"remove-item -recurse d:",
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

	// "kill -9 1" requires a word-boundary guard: simple substring matching would
	// also block "kill -9 12345" because "kill -9 1" is a prefix of "kill -9 12345".
	// We block only when the character immediately following "1" is non-alphanumeric
	// (i.e. "1" is the complete PID argument, targeting the init process).
	// We scan ALL occurrences: a compound command like "kill -9 12345; kill -9 1"
	// must not slip through because only the first hit is checked.
	const killInitPattern = "kill -9 1"
	for search := cmdLower; ; {
		idx := strings.Index(search, killInitPattern)
		if idx < 0 {
			break
		}
		end := idx + len(killInitPattern)
		if end >= len(search) || !isDigitOrAlpha(search[end]) {
			return tool.ToolResult{Error: fmt.Sprintf("安全限制: 命令包含危险模式 %q", killInitPattern)}, nil
		}
		// This hit was a false-positive (e.g. "kill -9 12345"); keep searching.
		search = search[idx+1:]
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

	// Filter environment variables: strip secrets, keep essentials
	cmd.Env = filterEnv(os.Environ())

	// Capture stdout + stderr
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	// Truncate if too long (rune-safe)
	outStr = safeRuneTruncate(outStr, maxOutputChars)
	outStr = strings.TrimSpace(outStr)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return tool.ToolResult{Error: fmt.Sprintf("命令超时 (%v): %s", shellTimeout, outStr)}, nil
		}
		if ctx.Err() == context.Canceled {
			return tool.ToolResult{Error: fmt.Sprintf("命令被取消: %s", outStr)}, nil
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
			// s[:i]  → exactly maxRunes runes (the kept prefix)
			// s[i:]  → remaining runes starting at the truncation point
			// Total  = maxRunes + RuneCountInString(s[i:])
			// (using maxRunes, not count, avoids double-counting the rune at i)
			totalRunes := maxRunes + utf8.RuneCountInString(s[i:])
			return s[:i] + fmt.Sprintf("\n... (输出截断，共 %d 字符)", totalRunes)
		}
	}
	return s
}

// sensitiveEnvSuffixes are environment variable name suffixes that indicate secrets.
var sensitiveEnvSuffixes = []string{
	"_KEY", "_SECRET", "_TOKEN", "_PASSWORD", "_PASSWD",
	"_PASSPHRASE", "_CREDENTIALS", "_AUTH", "_DSN",
}

// sensitiveEnvPrefixes are environment variable name prefixes that indicate secrets.
var sensitiveEnvPrefixes = []string{
	"DATABASE_URL", "REDIS_URL", "MONGO_URL",
}

// filterEnv returns a copy of env with sensitive variables removed.
func filterEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) < 2 {
			continue
		}
		nameUpper := strings.ToUpper(parts[0])

		sensitive := false
		for _, suffix := range sensitiveEnvSuffixes {
			if strings.HasSuffix(nameUpper, suffix) {
				sensitive = true
				break
			}
		}
		if !sensitive {
			for _, prefix := range sensitiveEnvPrefixes {
				if strings.HasPrefix(nameUpper, prefix) {
					sensitive = true
					break
				}
			}
		}

		if !sensitive {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// isDigitOrAlpha reports whether b is an ASCII digit or lowercase letter.
// Used for word-boundary checks in the dangerous pattern detector (cmdLower is
// already lowercased, so uppercase letters never appear here).
func isDigitOrAlpha(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z')
}
