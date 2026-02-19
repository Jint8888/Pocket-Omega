package skill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
)

// runRequest is the JSON envelope sent to the skill process via stdin.
type runRequest struct {
	Arguments map[string]any `json:"arguments"`
}

// runResponse is the JSON envelope received from the skill process via stdout.
type runResponse struct {
	Output string `json:"output"`
	Error  string `json:"error"`
}

// Run executes a skill via the stdio JSON protocol.
// Returns (output, errorMsg). errorMsg is non-empty on failure; both are never non-empty simultaneously.
//
// For "go" runtime, compilation is attempted automatically if the binary is missing.
func Run(ctx context.Context, def *SkillDef, args map[string]any) (string, string) {
	// Auto-compile Go skills on first run.
	if def.Runtime == "go" {
		if err := ensureCompiled(def); err != nil {
			return "", fmt.Sprintf("skill 编译失败: %v — 请检查 Go 代码是否可编译", err)
		}
	}

	cmd, err := buildCmd(ctx, def)
	if err != nil {
		return "", fmt.Sprintf("skill 启动失败: %v", err)
	}

	// Encode the request.
	req := runRequest{Arguments: args}
	reqData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Sprintf("参数序列化失败: %v", err)
	}
	reqData = append(reqData, '\n') // protocol requires a trailing newline

	cmd.Stdin = bytes.NewReader(reqData)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			log.Printf("[Skill] %s stderr: %s", def.Name, stderr.String())
		}
		return "", fmt.Sprintf("skill 执行失败: %v — 请检查入口文件是否正确", err)
	}

	// Log any stderr output (non-fatal, used for skill-side debug logging).
	if stderr.Len() > 0 {
		log.Printf("[Skill] %s stderr: %s", def.Name, stderr.String())
	}

	// Parse the first line of stdout as JSON.
	scanner := bufio.NewScanner(&stdout)
	if !scanner.Scan() {
		return "", "skill 无响应输出 — 请确认入口文件向 stdout 输出了单行 JSON"
	}

	var resp runResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return "", fmt.Sprintf("skill 响应解析失败: %v — 实际输出: %q", err, scanner.Text())
	}

	return resp.Output, resp.Error
}

// buildCmd constructs the exec.Cmd appropriate for the skill's runtime.
// The working directory is always set to the skill directory.
func buildCmd(ctx context.Context, def *SkillDef) (*exec.Cmd, error) {
	var cmd *exec.Cmd

	switch def.Runtime {
	case "python":
		entryPath := filepath.Join(def.Dir, def.Entry)
		cmd = exec.CommandContext(ctx, "python", entryPath)
	case "node":
		entryPath := filepath.Join(def.Dir, def.Entry)
		cmd = exec.CommandContext(ctx, "node", entryPath)
	case "go":
		binPath := filepath.Join(def.Dir, BinaryName())
		cmd = exec.CommandContext(ctx, binPath)
	case "binary":
		binPath := filepath.Join(def.Dir, def.Entry)
		cmd = exec.CommandContext(ctx, binPath)
	default:
		return nil, fmt.Errorf("未知 runtime: %q — 支持: python | node | go | binary", def.Runtime)
	}

	cmd.Dir = def.Dir // ensure relative paths in skill code resolve correctly
	return cmd, nil
}
