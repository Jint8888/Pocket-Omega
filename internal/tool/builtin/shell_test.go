package builtin

import (
	"strings"
	"testing"
)

func TestDangerousPatternBlocking(t *testing.T) {
	tests := []struct {
		command     string
		shouldBlock bool
	}{
		{"ls -la", false},
		{"echo hello", false},
		{"cat file.txt", false},
		{"go build ./...", false},
		{"rm -rf /", true},
		{"rm -rf /*", true},
		{"RM -RF /", true},          // case insensitive
		{"sudo rm -rf /home", true}, // blocked because "rm -rf /" is a substring
		{"mkfs.ext4 /dev/sda1", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"format c:", true},
		{"FORMAT C:", true},
		{"shutdown -h now", true},
		{"reboot", true},
		{":(){:|:&};:", true}, // fork bomb
		{"del /s /q c:\\", true},
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
		wantLen  int // rune count of result (excluding "..." suffix)
	}{
		{"short ASCII", "hello", 10, 5},
		{"exact limit", "hello", 5, 5},
		{"truncate ASCII", "hello world", 5, 5},
		{"Chinese text short", "你好世界", 10, 4},
		{"Chinese text truncate", "你好世界测试文本", 4, 4},
		{"mixed text", "hello你好", 6, 6},
		{"empty string", "", 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeRuneTruncate(tt.input, tt.maxRunes)
			resultRunes := []rune(result)

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
			_ = resultRunes // appease unused
		})
	}
}
