package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const gitTimeout = 10 * time.Second

// allowedGitCommands is the whitelist of read-only git subcommands.
var allowedGitCommands = map[string]bool{
	"status": true, "diff": true, "log": true,
	"branch": true, "stash": true, "show": true,
}

// dangerousGitArgs — git-level write/escape parameters.
// Shell metacharacters (|;&`) are NOT listed — exec.Command doesn't use a shell,
// so they are passed as literal strings to git and pose no injection risk.
var dangerousGitArgs = []string{
	"--exec",         // code execution
	"--upload-pack",  // remote execution
	"--receive-pack", // remote execution
	"--output",       // git diff --output=file writes to disk
	"--output-directory",
	"--no-index",  // can read arbitrary files outside repo
	"--work-tree", // bypasses workspaceDir constraint
	"--git-dir",   // same
}

// GitInfoTool provides safe, read-only Git queries.
type GitInfoTool struct {
	workspaceDir string
}

// NewGitInfoTool creates a git_info tool scoped to the given workspace.
func NewGitInfoTool(workspaceDir string) *GitInfoTool {
	return &GitInfoTool{workspaceDir: workspaceDir}
}

func (t *GitInfoTool) Name() string { return "git_info" }
func (t *GitInfoTool) Description() string {
	return "只读 Git 查询（status/diff/log/branch/stash/show）"
}

func (t *GitInfoTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "command", Type: "string", Description: "Git 子命令",
			Required: true, Enum: []string{"status", "diff", "log", "branch", "stash", "show"}},
		tool.SchemaParam{Name: "path", Type: "string", Description: "可选：限定路径（如 internal/agent/）", Required: false},
		tool.SchemaParam{Name: "args", Type: "string", Description: "可选：附加参数（空白分割）", Required: false},
	)
}

func (t *GitInfoTool) Init(_ context.Context) error { return nil }
func (t *GitInfoTool) Close() error                 { return nil }

type gitInfoArgs struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Args    string `json:"args"`
}

// isDangerousArg checks a single token against the blacklist using prefix matching
// to catch --output=file.txt, --work-tree=/foo, -ckey=val etc.
func isDangerousArg(token string) bool {
	lower := strings.ToLower(token)
	// -c special handling: -c can be followed directly by key=val without separator
	// (e.g. git -chttp.sslVerify=false). Conservative: block anything starting with "-c"
	// that isn't a long option ("--"). Trade-off: blocks legitimate "git log -c"
	// (combined merge mode), but security > functionality.
	if strings.HasPrefix(lower, "-c") && !strings.HasPrefix(lower, "--") {
		return true
	}
	for _, bad := range dangerousGitArgs {
		if lower == bad || strings.HasPrefix(lower, bad+"=") {
			return true
		}
	}
	return false
}

// splitArgs splits args by whitespace. Does not support quoted values with spaces —
// this is an intentional trade-off for simplicity; LLMs rarely pass quoted args.
func splitArgs(args string) []string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return nil
	}
	return strings.Fields(trimmed)
}

func (t *GitInfoTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a gitInfoArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	// Whitelist check (Schema enum enforces this, but double-check at runtime)
	if !allowedGitCommands[a.Command] {
		return tool.ToolResult{Error: fmt.Sprintf("不支持的命令 %q，允许: status/diff/log/branch/stash/show", a.Command)}, nil
	}

	// Parse and validate user args
	userArgs := splitArgs(a.Args)
	for _, token := range userArgs {
		if isDangerousArg(token) {
			return tool.ToolResult{Error: fmt.Sprintf("安全限制: 参数 %q 被禁止", token)}, nil
		}
	}

	// Build command args based on subcommand + user args
	var cmdArgs []string
	path := strings.TrimSpace(a.Path)

	switch a.Command {
	case "status":
		if len(userArgs) > 0 {
			cmdArgs = append([]string{"status"}, userArgs...)
		} else {
			cmdArgs = []string{"status", "--short"}
		}
		if path != "" {
			cmdArgs = append(cmdArgs, "--", path)
		}

	case "diff":
		if len(userArgs) > 0 {
			cmdArgs = append([]string{"diff"}, userArgs...)
		} else {
			cmdArgs = []string{"diff", "--stat"}
		}
		if path != "" {
			cmdArgs = append(cmdArgs, "--", path)
		}

	case "log":
		if len(userArgs) > 0 {
			cmdArgs = append([]string{"log"}, userArgs...)
		} else {
			cmdArgs = []string{"log", "--oneline", "-20"}
		}
		if path != "" {
			cmdArgs = append(cmdArgs, "--", path)
		}

	case "branch":
		if len(userArgs) > 0 {
			cmdArgs = append([]string{"branch"}, userArgs...)
		} else {
			cmdArgs = []string{"branch", "-a"}
		}
		if path != "" {
			log.Printf("[GitInfo] branch does not support path param (ignored); use args for filtering")
		}

	case "stash":
		if len(userArgs) > 0 {
			log.Printf("[GitInfo] stash ignores args=%v, always runs 'stash list'", userArgs)
		}
		cmdArgs = []string{"stash", "list"}

	case "show":
		if path != "" {
			log.Printf("[GitInfo] show does not support path param (ignored); use args=\"<commit>:<path>\" instead")
		}
		cmdArgs = append([]string{"show"}, userArgs...)
	}

	// Execute git command
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = t.workspaceDir
	cmd.Env = filterEnv(os.Environ())

	output, err := cmd.CombinedOutput()
	outStr := safeRuneTruncate(strings.TrimSpace(string(output)), maxOutputChars)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return tool.ToolResult{Error: fmt.Sprintf("git 命令超时 (%v): %s", gitTimeout, outStr)}, nil
		}
		return tool.ToolResult{Output: outStr, Error: fmt.Sprintf("git 命令错误: %v", err)}, nil
	}

	return tool.ToolResult{Output: outStr}, nil
}
