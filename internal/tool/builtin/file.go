package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	maxFileSize  = 1 << 20 // 1MB
	maxListItems = 100
)

// ── file_read ──

type FileReadTool struct {
	workspaceDir string
}

func NewFileReadTool(workspaceDir string) *FileReadTool {
	return &FileReadTool{workspaceDir: workspaceDir}
}

func (t *FileReadTool) Name() string        { return "file_read" }
func (t *FileReadTool) Description() string { return "读取指定文件的内容" }

func (t *FileReadTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "path", Type: "string", Description: "文件路径", Required: true},
	)
}

func (t *FileReadTool) Init(_ context.Context) error { return nil }
func (t *FileReadTool) Close() error                 { return nil }

type filePathArgs struct {
	Path string `json:"path"`
}

func (t *FileReadTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a filePathArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	path, err := safeResolvePath(a.Path, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("文件不存在: %s。请确认路径是否正确，或提供完整的绝对路径。", path)}, nil
	}
	if info.IsDir() {
		return tool.ToolResult{Error: "指定路径是目录，请使用 file_list"}, nil
	}
	if info.Size() > maxFileSize {
		return tool.ToolResult{Error: fmt.Sprintf("文件过大 (%d bytes)，最大 %d bytes", info.Size(), maxFileSize)}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("读取失败: %v", err)}, nil
	}

	return tool.ToolResult{Output: string(data)}, nil
}

// ── file_write ──

type FileWriteTool struct {
	workspaceDir string
}

func NewFileWriteTool(workspaceDir string) *FileWriteTool {
	return &FileWriteTool{workspaceDir: workspaceDir}
}

func (t *FileWriteTool) Name() string { return "file_write" }
func (t *FileWriteTool) Description() string {
	return "将内容写入指定文件（创建或覆盖）"
}

func (t *FileWriteTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "path", Type: "string", Description: "文件路径", Required: true},
		tool.SchemaParam{Name: "content", Type: "string", Description: "要写入的内容", Required: true},
	)
}

func (t *FileWriteTool) Init(_ context.Context) error { return nil }
func (t *FileWriteTool) Close() error                 { return nil }

type fileWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *FileWriteTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a fileWriteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	path, err := safeResolvePath(a.Path, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("创建目录失败: %v", err)}, nil
	}

	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("写入失败: %v", err)}, nil
	}

	return tool.ToolResult{Output: fmt.Sprintf("已写入 %s (%d 字节)", path, len(a.Content))}, nil
}

// ── file_list ──

type FileListTool struct {
	workspaceDir string
}

func NewFileListTool(workspaceDir string) *FileListTool {
	return &FileListTool{workspaceDir: workspaceDir}
}

func (t *FileListTool) Name() string        { return "file_list" }
func (t *FileListTool) Description() string { return "列出指定目录下的文件和子目录" }

func (t *FileListTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "path", Type: "string", Description: "目录路径", Required: true},
	)
}

func (t *FileListTool) Init(_ context.Context) error { return nil }
func (t *FileListTool) Close() error                 { return nil }

func (t *FileListTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a filePathArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	path, err := safeResolvePath(a.Path, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("目录不存在: %s。请确认路径是否正确，用 \".\" 表示工作目录，或提供完整的绝对路径。", path)}, nil
	}

	var sb strings.Builder
	count := 0
	for _, entry := range entries {
		if count >= maxListItems {
			sb.WriteString(fmt.Sprintf("... (共 %d 项，仅显示前 %d 项)\n", len(entries), maxListItems))
			break
		}

		info, _ := entry.Info()
		typeStr := "📄"
		sizeStr := ""
		if entry.IsDir() {
			typeStr = "📁"
		} else if info != nil {
			sizeStr = fmt.Sprintf(" (%d bytes)", info.Size())
		}

		sb.WriteString(fmt.Sprintf("%s %s%s\n", typeStr, entry.Name(), sizeStr))
		count++
	}

	if count == 0 {
		return tool.ToolResult{Output: "（空目录）"}, nil
	}

	return tool.ToolResult{Output: sb.String()}, nil
}

// ── file_find ──

const (
	maxFindResults = 50
)

type FileFindTool struct {
	workspaceDir string
}

func NewFileFindTool(workspaceDir string) *FileFindTool {
	return &FileFindTool{workspaceDir: workspaceDir}
}

func (t *FileFindTool) Name() string { return "find" }
func (t *FileFindTool) Description() string {
	return "在工作目录下递归搜索文件和目录。输入关键词或通配符（如 '*.go'），返回匹配的文件和目录路径。"
}

func (t *FileFindTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "pattern", Type: "string", Description: "搜索关键词（文件名或目录名的一部分，如 'config' 或 '*.go'）", Required: true},
	)
}

func (t *FileFindTool) Init(_ context.Context) error { return nil }
func (t *FileFindTool) Close() error                 { return nil }

// skipDirs contains directory names to skip during recursive search.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".idea": true, ".vscode": true,
	"vendor": true, "__pycache__": true, ".cache": true,
}

func (t *FileFindTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	pattern := strings.TrimSpace(a.Pattern)
	if pattern == "" {
		return tool.ToolResult{Error: "搜索关键词不能为空"}, nil
	}

	root := t.workspaceDir
	if root == "" {
		return tool.ToolResult{Error: "工作目录未设置"}, nil
	}

	var results []string
	lowerPattern := strings.ToLower(pattern)
	// Check if pattern contains glob characters
	isGlob := strings.ContainsAny(pattern, "*?[")

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}

		// Skip hidden/vendor directories for performance
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}

		name := d.Name()
		matched := false

		if isGlob {
			matched, _ = filepath.Match(pattern, name)
		} else {
			matched = strings.Contains(strings.ToLower(name), lowerPattern)
		}

		if matched {
			// Show path relative to workspace
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			prefix := "📄 "
			if d.IsDir() {
				prefix = "📁 "
			}
			results = append(results, prefix+rel)
			if len(results) >= maxFindResults {
				return fmt.Errorf("limit reached")
			}
		}
		return nil
	})

	if len(results) == 0 {
		return tool.ToolResult{Output: fmt.Sprintf("未找到匹配 %q 的文件或目录。", pattern)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 个匹配项：\n", len(results)))
	for _, r := range results {
		sb.WriteString(r + "\n")
	}
	if len(results) >= maxFindResults {
		sb.WriteString(fmt.Sprintf("（结果已截断，最多显示 %d 条）\n", maxFindResults))
	}

	return tool.ToolResult{Output: sb.String()}, nil
}

// ── shared helpers ──

// safeResolvePath resolves a file path and validates it stays within the workspace.
// Prevents path traversal attacks (e.g. ../../etc/passwd) and prefix collisions
// (e.g. workspace="C:\project", path="C:\project-evil\attack.txt").
func safeResolvePath(path, workspaceDir string) (string, error) {
	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else if workspaceDir != "" {
		resolved = filepath.Clean(filepath.Join(workspaceDir, path))
	} else {
		resolved = filepath.Clean(path)
	}

	// Sandbox check: resolved path must be within workspace
	if workspaceDir != "" {
		absWorkspace, err := filepath.Abs(workspaceDir)
		if err != nil {
			return "", fmt.Errorf("无法解析工作目录: %w", err)
		}
		absResolved, err := filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("无法解析目标路径: %w", err)
		}
		// Use separator suffix to prevent prefix collision:
		// "C:\project" vs "C:\project-evil" → must compare "C:\project\"
		if absResolved != absWorkspace &&
			!strings.HasPrefix(absResolved, absWorkspace+string(os.PathSeparator)) {
			return "", fmt.Errorf("安全限制: 路径 %q 超出工作目录 %q", path, workspaceDir)
		}
	}

	return resolved, nil
}
