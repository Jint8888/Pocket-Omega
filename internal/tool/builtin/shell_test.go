package builtin

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestDangerousPatternBlocking(t *testing.T) {
	tests := []struct {
		command     string
		shouldBlock bool
	}{
		// Safe commands
		{"ls -la", false},
		{"echo hello", false},
		{"cat file.txt", false},
		{"go build ./...", false},
		{"rm file.txt", false},         // removing a specific file is fine
		{"pkill myprocess", false},     // no -9
		{"kill 12345", false},          // no -9 1
		{"chmod 755 script.sh", false}, // not 000

		// Linux destructive
		{"rm -rf /", true},
		{"rm -rf /*", true},
		{"RM -RF /", true},                  // case insensitive
		{"sudo rm -rf /home", true},         // substring match
		{"rm -r -f /etc", true},             // flags split
		{"rm --recursive /important", true}, // long flag
		{"rm -rf ~", true},                  // home dir
		{"rm -rf $HOME", true},              // env var
		{"rm -rf ${HOME}", true},            // brace expansion (new)
		{"rm -rf -- /", true},               // POSIX -- separator bypass (new)
		{"rm -r -f -- /tmp/../..", true},    // POSIX -- separator, split flags (new)

		// System control
		{"shutdown -h now", true},
		{"reboot", true},
		{"halt", true},
		{"init 0", true},
		{"init 6", true},
		{"systemctl poweroff", true},
		{"systemctl halt", true},

		// Process killing
		{"pkill -9 -1", true},
		// Note: "kill -9 1" blocking is tested via TestExecute_KillInit (word-boundary
		// check lives in Execute, not dangerousPatterns — to avoid the "kill -9 12345"
		// false-positive). At the pattern level alone it is not blocked.
		{"kill -9 12345", false}, // must NOT be blocked at pattern level

		// Permission destruction
		{"chmod -R 000 /", true},

		// Filesystem
		{"mkfs.ext4 /dev/sda1", true},
		{"dd if=/dev/zero of=/dev/sda", true},

		// Fork bomb
		{":(){:|:&};:", true},

		// Windows destructive
		{"format c:", true},
		{"FORMAT C:", true},
		{"format d:", true},
		{"del /s /q c:\\", true},
		{"del /s /q d:\\", true},
		{"rd /s /q c:\\", true},
		{"rd /s /q d:\\", true},
		{"Remove-Item -Recurse C:\\", true},
		{"Remove-Item -Recurse D:\\Users", true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			cmdLower := strings.ToLower(tt.command)
			blocked := false
			for _, pattern := range dangerousPatterns {
				if strings.Contains(cmdLower, pattern) {
					blocked = true
					break
				}
			}
			if blocked != tt.shouldBlock {
				t.Errorf("command %q: blocked=%v, want %v", tt.command, blocked, tt.shouldBlock)
			}
		})
	}
}

func TestSafeRuneTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
	}{
		{"short ASCII", "hello", 10},
		{"exact limit", "hello", 5},
		{"truncate ASCII", "hello world", 5},
		{"Chinese text short", "你好世界", 10},
		{"Chinese text truncate", "你好世界测试文本", 4},
		{"mixed text", "hello你好", 6},
		{"empty string", "", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeRuneTruncate(tt.input, tt.maxRunes)

			if len([]rune(tt.input)) <= tt.maxRunes {
				// Should not be truncated
				if result != tt.input {
					t.Errorf("should not truncate: got %q, want %q", result, tt.input)
				}
			} else {
				// Should be truncated with "..." suffix
				if !strings.Contains(result, "...") {
					t.Errorf("truncated result should contain '...': %q", result)
				}
				// The prefix (before "...") should be valid UTF-8
				prefix := result[:strings.Index(result, "\n...")]
				if len([]rune(prefix)) != tt.maxRunes {
					t.Errorf("prefix rune count = %d, want %d", len([]rune(prefix)), tt.maxRunes)
				}
			}
		})
	}
}

// TestSafeRuneTruncateCount verifies that the reported total rune count
// in the truncation message is accurate (was off by 1 before fix).
func TestSafeRuneTruncateCount(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxRunes  int
		wantTotal int
	}{
		{"ASCII 11 chars, limit 5", "hello world", 5, 11},
		{"Chinese 8 chars, limit 4", "你好世界测试文本", 4, 8},
		{"mixed 7 runes, limit 3", "ab你cd好e", 3, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeRuneTruncate(tt.input, tt.maxRunes)
			if !strings.Contains(result, "\n...") {
				t.Fatalf("expected truncation, got %q", result)
			}
			// Verify total rune count matches actual input length
			actualTotal := len([]rune(tt.input))
			if actualTotal != tt.wantTotal {
				t.Fatalf("test setup error: input has %d runes, want %d", actualTotal, tt.wantTotal)
			}
			// Extract the reported count from "\n... (输出截断，共 N 字符)"
			marker := "共 "
			idx := strings.Index(result, marker)
			if idx < 0 {
				t.Fatalf("truncation marker not found in %q", result)
			}
			numStr := result[idx+len(marker):]
			numStr = numStr[:strings.Index(numStr, " ")]
			var got int
			for _, ch := range numStr {
				if ch < '0' || ch > '9' {
					t.Fatalf("unexpected char %q in number %q", ch, numStr)
				}
				got = got*10 + int(ch-'0')
			}
			if got != tt.wantTotal {
				t.Errorf("reported total = %d, want %d (input runes = %d)",
					got, tt.wantTotal, actualTotal)
			}
		})
	}
}

// --- Execute() integration tests (via real shell) ---

func TestExecute_Disabled(t *testing.T) {
	st := NewShellTool("", false)
	args, _ := json.Marshal(shellArgs{Command: "echo hi"})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "已禁用") {
		t.Errorf("expected disabled error, got: %+v", result)
	}
}

func TestExecute_EmptyCommand(t *testing.T) {
	st := NewShellTool("", true)
	args, _ := json.Marshal(shellArgs{Command: ""})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "不能为空") {
		t.Errorf("expected empty command error, got: %+v", result)
	}
}

func TestExecute_DangerousBlocked(t *testing.T) {
	st := NewShellTool("", true)
	args, _ := json.Marshal(shellArgs{Command: "rm -rf /"})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("expected safety error, got: %+v", result)
	}
}

// TestExecute_KillInit verifies the word-boundary check for "kill -9 1":
//   - "kill -9 1"     must be blocked (targeting init / PID 1)
//   - "kill -9 12345" must NOT be blocked (arbitrary PID that starts with '1')
func TestExecute_KillInit(t *testing.T) {
	st := NewShellTool("", true)

	// Should be blocked: kill -9 1 (targeting init process)
	args, _ := json.Marshal(shellArgs{Command: "kill -9 1"})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "安全限制") {
		t.Errorf("kill -9 1 should be blocked, got: %+v", result)
	}

	// Should NOT be blocked by safety limit: kill -9 12345 (valid PID)
	// It may fail due to "no such process", but that is a runtime error, not
	// a safety block — so result.Error must not contain "安全限制".
	args2, _ := json.Marshal(shellArgs{Command: "kill -9 12345"})
	result2, err := st.Execute(context.Background(), args2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result2.Error, "安全限制") {
		t.Errorf("kill -9 12345 should NOT be blocked by safety limit, got: %+v", result2)
	}

	// Compound command: first hit is a false-positive ("kill -9 12345"),
	// the second hit is the real danger ("kill -9 1"). Must be blocked.
	args3, _ := json.Marshal(shellArgs{Command: "echo kill -9 12345; kill -9 1"})
	result3, err := st.Execute(context.Background(), args3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result3.Error == "" || !strings.Contains(result3.Error, "安全限制") {
		t.Errorf("compound 'kill -9 12345; kill -9 1' should be blocked, got: %+v", result3)
	}
}

func TestExecute_SuccessfulCommand(t *testing.T) {
	st := NewShellTool("", true)
	cmd := "echo hello_omega"
	args, _ := json.Marshal(shellArgs{Command: cmd})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "hello_omega") {
		t.Errorf("expected output to contain 'hello_omega', got: %q", result.Output)
	}
}

func TestExecute_NonZeroExit(t *testing.T) {
	st := NewShellTool("", true)
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "cmd /c exit 1"
	} else {
		cmd = "exit 1"
	}
	args, _ := json.Marshal(shellArgs{Command: cmd})
	result, err := st.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "退出错误") {
		t.Errorf("expected exit error, got: %+v", result)
	}
}

func TestExecute_BadJSON(t *testing.T) {
	st := NewShellTool("", true)
	result, err := st.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "参数解析失败") {
		t.Errorf("expected parse error, got: %+v", result)
	}
}

// --- filterEnv tests ---

func TestFilterEnv(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"OPENAI_API_KEY=sk-1234",
		"DATABASE_URL=postgres://...",
		"TAVILY_API_KEY=tvly-xxx",
		"MY_SECRET=hidden",
		"MY_TOKEN=abc",
		"MY_PASSWORD=xyz",
		"GOPATH=/go",
		"REDIS_URL=redis://...",
		"NORMAL_VAR=hello",
	}

	filtered := filterEnv(input)
	filteredStr := strings.Join(filtered, "\n")

	// Should keep safe vars
	if !strings.Contains(filteredStr, "PATH=/usr/bin") {
		t.Error("PATH should be kept")
	}
	if !strings.Contains(filteredStr, "HOME=/home/user") {
		t.Error("HOME should be kept")
	}
	if !strings.Contains(filteredStr, "GOPATH=/go") {
		t.Error("GOPATH should be kept")
	}
	if !strings.Contains(filteredStr, "NORMAL_VAR=hello") {
		t.Error("NORMAL_VAR should be kept")
	}

	// Should strip sensitive vars
	if strings.Contains(filteredStr, "OPENAI_API_KEY") {
		t.Error("OPENAI_API_KEY should be filtered")
	}
	if strings.Contains(filteredStr, "DATABASE_URL") {
		t.Error("DATABASE_URL should be filtered")
	}
	if strings.Contains(filteredStr, "TAVILY_API_KEY") {
		t.Error("TAVILY_API_KEY should be filtered")
	}
	if strings.Contains(filteredStr, "MY_SECRET") {
		t.Error("MY_SECRET should be filtered")
	}
	if strings.Contains(filteredStr, "MY_TOKEN") {
		t.Error("MY_TOKEN should be filtered")
	}
	if strings.Contains(filteredStr, "MY_PASSWORD") {
		t.Error("MY_PASSWORD should be filtered")
	}
	if strings.Contains(filteredStr, "REDIS_URL") {
		t.Error("REDIS_URL should be filtered")
	}
}
