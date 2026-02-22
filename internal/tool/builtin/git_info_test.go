package builtin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// setupTempRepo creates a temporary Git repo with user config for CI safety.
func setupTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "initial commit")
	return dir
}

func execGitInfo(t *testing.T, tool *GitInfoTool, argsJSON string) (string, string) {
	t.Helper()
	result, err := tool.Execute(context.Background(), json.RawMessage(argsJSON))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	return result.Output, result.Error
}

func TestGitInfo_Status(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	_, errMsg := execGitInfo(t, tool, `{"command":"status"}`)
	if errMsg != "" {
		t.Errorf("status should succeed, got error: %s", errMsg)
	}
}

func TestGitInfo_Log(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	out, errMsg := execGitInfo(t, tool, `{"command":"log"}`)
	if errMsg != "" {
		t.Errorf("log error: %s", errMsg)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("log should contain 'initial commit', got: %s", out)
	}
}

func TestGitInfo_Branch(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	out, errMsg := execGitInfo(t, tool, `{"command":"branch"}`)
	if errMsg != "" {
		t.Errorf("branch error: %s", errMsg)
	}
	if !strings.Contains(out, "main") && !strings.Contains(out, "master") {
		t.Errorf("branch should contain 'main' or 'master', got: %s", out)
	}
}

func TestGitInfo_Show(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	out, errMsg := execGitInfo(t, tool, `{"command":"show"}`)
	if errMsg != "" {
		t.Errorf("show error: %s", errMsg)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("show should contain commit info, got: %s", out)
	}
}

func TestGitInfo_Stash(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	_, errMsg := execGitInfo(t, tool, `{"command":"stash"}`)
	if errMsg != "" {
		t.Errorf("stash list should succeed on clean repo, got error: %s", errMsg)
	}
}

func TestGitInfo_DiffWithPath(t *testing.T) {
	dir := setupTempRepo(t)
	// Create an unstaged file so diff has output
	if err := os.WriteFile(dir+"/test.txt", []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stage, commit, then modify to create a tracked-file diff
	if out, err := exec.Command("git", "-C", dir, "add", "test.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "add test.txt").CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}
	if err := os.WriteFile(dir+"/test.txt", []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGitInfoTool(dir)
	out, errMsg := execGitInfo(t, tool, `{"command":"diff","path":"test.txt"}`)
	if errMsg != "" {
		t.Errorf("diff error: %s", errMsg)
	}
	if out == "" {
		t.Error("diff with path should produce output for modified file")
	}
}

func TestGitInfo_InvalidCommand(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	_, errMsg := execGitInfo(t, tool, `{"command":"push"}`)
	if errMsg == "" {
		t.Error("push should be rejected")
	}
}

func TestGitInfo_DangerousArgs(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)
	_, errMsg := execGitInfo(t, tool, `{"command":"log","args":"--exec foo"}`)
	if errMsg == "" {
		t.Error("--exec should be rejected")
	}
}

func TestGitInfo_DangerousArgsPrefix(t *testing.T) {
	dir := setupTempRepo(t)
	tool := NewGitInfoTool(dir)

	tests := []struct {
		args string
		desc string
	}{
		{`{"command":"diff","args":"--output=file.txt"}`, "--output=value"},
		{`{"command":"diff","args":"--no-index"}`, "--no-index"},
		{`{"command":"log","args":"--work-tree=/tmp"}`, "--work-tree=value"},
		{`{"command":"log","args":"-ckey=val"}`, "-c prefix"},
	}
	for _, tc := range tests {
		_, errMsg := execGitInfo(t, tool, tc.args)
		if errMsg == "" {
			t.Errorf("%s should be rejected", tc.desc)
		}
	}
}

func TestGitInfo_OutputTruncation(t *testing.T) {
	dir := setupTempRepo(t)
	// Create 27 commits × 300-char message ≈ 8,100 chars > 8,000 threshold
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = os.Environ()
		cmd.Run()
	}
	longMsg := strings.Repeat("x", 300)
	for i := 0; i < 27; i++ {
		run("commit", "--allow-empty", "-m", longMsg)
	}

	tool := NewGitInfoTool(dir)
	// Use args="--oneline" to bypass the default -20 limit
	out, errMsg := execGitInfo(t, tool, `{"command":"log","args":"--oneline"}`)
	if errMsg != "" {
		t.Errorf("log error: %s", errMsg)
	}
	if !strings.Contains(out, "输出截断") {
		t.Errorf("output should be truncated, got %d chars", len(out))
	}
}
