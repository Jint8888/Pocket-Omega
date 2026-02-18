package agent

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ExecLogger writes agent execution steps to a markdown file for debugging.
// Thread-safe. The log file is truncated on creation.
type ExecLogger struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewExecLogger creates a logger that writes to the given path.
// The file is created (or truncated) immediately.
func NewExecLogger(path string) (*ExecLogger, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("cannot create exec log: %w", err)
	}
	return &ExecLogger{file: f, path: path}, nil
}

// StartSession writes a session header with the user's question.
func (l *ExecLogger) StartSession(problem string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Truncate file for new session
	l.file.Truncate(0)
	l.file.Seek(0, 0)

	l.writef("# Agent 执行日志\n\n")
	l.writef("**时间**: %s  \n", time.Now().Format("2006-01-02 15:04:05"))
	l.writef("**问题**: %s\n\n", problem)
	l.writef("---\n\n")
}

// LogStep writes a single step record as a markdown section.
func (l *ExecLogger) LogStep(step StepRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.writef("## Step %d — %s\n\n", step.StepNumber, stepTypeLabel(step.Type))

	switch step.Type {
	case "decide":
		l.writef("**动作**: `%s`  \n", step.Action)
		if step.Output != "" {
			l.writef("**理由**: %s\n\n", step.Output)
		}

	case "tool":
		l.writef("**工具**: `%s`  \n", step.ToolName)
		if step.Input != "" {
			l.writef("\n<details>\n<summary>输入参数</summary>\n\n```\n%s\n```\n\n</details>\n\n", step.Input)
		}
		if step.Output != "" {
			output := step.Output
			// Truncate very long outputs
			runes := []rune(output)
			if len(runes) > 4000 {
				output = string(runes[:4000]) + "\n... (truncated)"
			}
			l.writef("\n<details>\n<summary>执行结果</summary>\n\n```\n%s\n```\n\n</details>\n\n", output)
		}

	case "think":
		if step.Output != "" {
			l.writef("\n> %s\n\n", strings.ReplaceAll(step.Output, "\n", "\n> "))
		}

	case "answer":
		if step.Output != "" {
			l.writef("\n%s\n\n", step.Output)
		}
	}

	l.writef("---\n\n")
}

// EndSession writes the final summary.
func (l *ExecLogger) EndSession(state *AgentState) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.writef("## 结果摘要\n\n")
	l.writef("- **总步数**: %d\n", len(state.StepHistory))
	l.writef("- **回答长度**: %d 字符\n", len([]rune(state.Solution)))
	l.writef("- **完成时间**: %s\n", time.Now().Format("2006-01-02 15:04:05"))
}

// Close closes the underlying file.
func (l *ExecLogger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *ExecLogger) writef(format string, args ...interface{}) {
	fmt.Fprintf(l.file, format, args...)
}

func stepTypeLabel(t string) string {
	switch t {
	case "decide":
		return "🧭 决策"
	case "tool":
		return "🔧 工具"
	case "think":
		return "🧠 推理"
	case "answer":
		return "✅ 回答"
	default:
		return t
	}
}
