package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

// blockedOpenExts 阻止通过 file_open 启动可执行或脚本类文件。
// 目的是防止 agent 被诱导执行恶意载荷，file_open 仅用于查看媒体/文档。
var blockedOpenExts = map[string]bool{
	// Windows 可执行 / 安装包
	".exe": true, ".com": true, ".msi": true, ".msp": true,
	".scr": true, ".pif": true,
	// 脚本
	".bat": true, ".cmd": true,
	".ps1": true, ".ps2": true,
	".vbs": true, ".vbe": true,
	".js":  true, ".jse": true,
	".wsf": true, ".wsh": true,
	".sh":  true, ".bash": true, ".zsh": true,
	// 跨平台运行时脚本
	".jar": true,
	".py":  true, ".pyw": true,
	".rb":  true,
	".pl":  true,
	".php": true,
}

// ── file_open ──

type FileOpenTool struct {
	workspaceDir string
}

func NewFileOpenTool(workspaceDir string) *FileOpenTool {
	return &FileOpenTool{workspaceDir: workspaceDir}
}

func (t *FileOpenTool) Name() string { return "file_open" }
func (t *FileOpenTool) Description() string {
	return "用系统默认程序打开文件（图片、音乐、视频、文档等），操作系统自动选择对应应用。仅支持媒体/文档类文件，禁止打开可执行或脚本文件。"
}

func (t *FileOpenTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "path", Type: "string", Description: "要打开的文件路径（相对于工作区）", Required: true},
	)
}

func (t *FileOpenTool) Init(_ context.Context) error { return nil }
func (t *FileOpenTool) Close() error                 { return nil }

type fileOpenArgs struct {
	Path string `json:"path"`
}

func (t *FileOpenTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a fileOpenArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	if strings.TrimSpace(a.Path) == "" {
		return tool.ToolResult{Error: "path 不能为空"}, nil
	}

	// 安全：阻止可执行/脚本类扩展名
	ext := strings.ToLower(filepath.Ext(a.Path))
	if blockedOpenExts[ext] {
		return tool.ToolResult{Error: fmt.Sprintf("安全限制: 不允许打开可执行或脚本文件 (%s)", ext)}, nil
	}

	absPath, err := safeResolvePath(a.Path, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return tool.ToolResult{Error: fmt.Sprintf("文件不存在: %s — 请先用 file_list 确认路径", a.Path)}, nil
		}
		return tool.ToolResult{Error: fmt.Sprintf("无法访问文件: %v", err)}, nil
	}
	if info.IsDir() {
		return tool.ToolResult{Error: "指定路径是目录，file_open 仅支持文件"}, nil
	}

	cmd := openCmdFunc(absPath)
	if err := cmd.Start(); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("启动默认程序失败: %v", err)}, nil
	}
	// 异步回收子进程，避免产生僵尸进程（zombie）
	go func() { _ = cmd.Wait() }()

	relPath := relOrAbs(absPath, t.workspaceDir)
	return tool.ToolResult{Output: fmt.Sprintf("已使用默认程序打开: %s", relPath)}, nil
}

// openCmdFunc 是实际构造"用默认程序打开"命令的函数。
// 使用包级变量而非直接调用，使测试可以将其替换为 no-op 以避免弹出真实 GUI 窗口。
var openCmdFunc = openCmd

// openCmd 根据操作系统返回对应的"用默认程序打开"命令。
//
//   - Windows: cmd /c start "" "<path>"
//     （start 后跟空字符串是窗口标题占位，防止带空格的路径被误解析为标题）
//   - macOS:   open "<path>"
//   - Linux:   xdg-open "<path>"
func openCmd(absPath string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", absPath)
	case "darwin":
		return exec.Command("open", absPath)
	default:
		return exec.Command("xdg-open", absPath)
	}
}
