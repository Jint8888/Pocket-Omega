package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketomega/pocket-omega/internal/tool"
)

const (
	maxPatchFileSize = 5 << 20 // 5MB — file_patch limit
)

// ── file_move ──

type FileMoveTool struct {
	workspaceDir string
}

func NewFileMoveTool(workspaceDir string) *FileMoveTool {
	return &FileMoveTool{workspaceDir: workspaceDir}
}

func (t *FileMoveTool) Name() string { return "file_move" }
func (t *FileMoveTool) Description() string {
	return "移动或重命名文件/目录，支持跨目录移动，自动创建目标父目录。目标路径已存在时拒绝操作。"
}

func (t *FileMoveTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "source", Type: "string", Description: "源路径（相对于工作区）", Required: true},
		tool.SchemaParam{Name: "destination", Type: "string", Description: "目标路径（相对于工作区）", Required: true},
	)
}

func (t *FileMoveTool) Init(_ context.Context) error { return nil }
func (t *FileMoveTool) Close() error                 { return nil }

type fileMoveArgs struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (t *FileMoveTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a fileMoveArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	if strings.TrimSpace(a.Source) == "" {
		return tool.ToolResult{Error: "source 不能为空"}, nil
	}
	if strings.TrimSpace(a.Destination) == "" {
		return tool.ToolResult{Error: "destination 不能为空"}, nil
	}

	srcPath, err := safeResolvePath(a.Source, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("源路径无效: %v", err)}, nil
	}
	dstPath, err := safeResolvePath(a.Destination, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("目标路径无效: %v", err)}, nil
	}

	// Protected file guard: block move of mcp.json etc.
	if msg := checkProtectedFile(srcPath, t.workspaceDir); msg != "" {
		return tool.ToolResult{Error: msg}, nil
	}
	if msg := checkProtectedFile(dstPath, t.workspaceDir); msg != "" {
		return tool.ToolResult{Error: msg}, nil
	}

	// Forbid moving workspace root itself
	absWorkspace, _ := filepath.Abs(t.workspaceDir)
	absSrc, _ := filepath.Abs(srcPath)
	if absSrc == absWorkspace {
		return tool.ToolResult{Error: "安全限制: 禁止移动工作区根目录"}, nil
	}

	// Verify source exists
	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return tool.ToolResult{Error: fmt.Sprintf("源路径不存在: %s — 请先用 file_list 确认路径", a.Source)}, nil
		}
		return tool.ToolResult{Error: fmt.Sprintf("无法访问源路径: %v", err)}, nil
	}

	// Refuse to overwrite an existing destination (no silent overwrite)
	if _, err := os.Stat(dstPath); err == nil {
		return tool.ToolResult{Error: fmt.Sprintf("目标路径已存在: %s — 请先删除或选择其他路径", a.Destination)}, nil
	}

	// Auto-create parent directories
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("创建目标目录失败: %v", err)}, nil
	}

	// Attempt atomic rename first; fall back to copy+delete for cross-device moves
	if err := os.Rename(srcPath, dstPath); err != nil {
		if err2 := crossDeviceMove(srcPath, dstPath); err2 != nil {
			return tool.ToolResult{Error: fmt.Sprintf("移动失败: %v", err2)}, nil
		}
	}

	srcRel := relOrAbs(srcPath, t.workspaceDir)
	dstRel := relOrAbs(dstPath, t.workspaceDir)
	return tool.ToolResult{Output: fmt.Sprintf("已移动: %s → %s", srcRel, dstRel)}, nil
}

// crossDeviceMove copies src to dst (file or directory), then removes src.
// Used as a fallback when os.Rename fails across filesystems.
// On partial failure, the incomplete destination is cleaned up.
func crossDeviceMove(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := copyDir(src, dst); err != nil {
			os.RemoveAll(dst) // best-effort cleanup of incomplete copy
			return err
		}
	} else {
		if err := copyFile(src, dst); err != nil {
			os.Remove(dst) // best-effort cleanup of incomplete copy
			return err
		}
	}
	return os.RemoveAll(src)
}

// copyFile copies a single file from src to dst, preserving the source
// file permissions. Close() is checked explicitly to catch flush errors.
func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	// Preserve source file permissions
	info, err := sf.Stat()
	if err != nil {
		return err
	}

	// O_EXCL prevents accidentally overwriting an existing file
	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(df, sf)
	closeErr := df.Close() // explicit close to catch buffered write errors
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// copyDir recursively copies a directory from src to dst.
// Symlinks are skipped to avoid copying dangling or out-of-workspace links.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Skip symlinks: they may point outside the workspace or be dangling
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else {
			if err := copyFile(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// relOrAbs returns path relative to workspaceDir, falling back to the absolute path.
func relOrAbs(path, workspaceDir string) string {
	if rel, err := filepath.Rel(workspaceDir, path); err == nil {
		return rel
	}
	return path
}

// ── file_delete ──

type FileDeleteTool struct {
	workspaceDir string
}

func NewFileDeleteTool(workspaceDir string) *FileDeleteTool {
	return &FileDeleteTool{workspaceDir: workspaceDir}
}

func (t *FileDeleteTool) Name() string { return "file_delete" }
func (t *FileDeleteTool) Description() string {
	return "删除文件或目录。高危操作，必须传入 confirm=\"yes\" 才会执行。recursive=true 支持递归删除非空目录。"
}

func (t *FileDeleteTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "path", Type: "string", Description: "待删除路径（相对于工作区）", Required: true},
		tool.SchemaParam{Name: "confirm", Type: "string", Description: "必须传入 \"yes\" 才执行删除", Required: true},
		tool.SchemaParam{Name: "recursive", Type: "boolean", Description: "是否递归删除目录（默认 false）", Required: false},
	)
}

func (t *FileDeleteTool) Init(_ context.Context) error { return nil }
func (t *FileDeleteTool) Close() error                 { return nil }

type fileDeleteArgs struct {
	Path      string `json:"path"`
	Confirm   string `json:"confirm"`
	Recursive bool   `json:"recursive"`
}

func (t *FileDeleteTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a fileDeleteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	// Validate required parameters before the safety gate
	if strings.TrimSpace(a.Path) == "" {
		return tool.ToolResult{Error: "path 不能为空"}, nil
	}

	// Safety gate: must explicitly confirm
	if a.Confirm != "yes" {
		return tool.ToolResult{Error: "删除操作已取消：confirm 参数必须为 \"yes\" 才能执行删除。请重新调用并传入 confirm=\"yes\"。"}, nil
	}

	path, err := safeResolvePath(a.Path, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	// Protected file guard: block deletion of mcp.json etc.
	if msg := checkProtectedFile(path, t.workspaceDir); msg != "" {
		return tool.ToolResult{Error: msg}, nil
	}

	// Forbid deleting workspace root
	absWorkspace, _ := filepath.Abs(t.workspaceDir)
	absPath, _ := filepath.Abs(path)
	if absPath == absWorkspace {
		return tool.ToolResult{Error: "安全限制: 禁止删除工作区根目录"}, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tool.ToolResult{Error: fmt.Sprintf("路径不存在: %s — 请先用 file_list 确认路径", a.Path)}, nil
		}
		return tool.ToolResult{Error: fmt.Sprintf("无法访问路径: %v", err)}, nil
	}

	if info.IsDir() && !a.Recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return tool.ToolResult{Error: fmt.Sprintf("读取目录失败: %v", err)}, nil
		}
		if len(entries) > 0 {
			return tool.ToolResult{Error: "目录非空，无法删除。如需递归删除，请传入 recursive=true（再次确认风险）。"}, nil
		}
	}

	relPath := relOrAbs(path, t.workspaceDir)

	if a.Recursive {
		if err := os.RemoveAll(path); err != nil {
			return tool.ToolResult{Error: fmt.Sprintf("删除失败: %v", err)}, nil
		}
	} else {
		if err := os.Remove(path); err != nil {
			return tool.ToolResult{Error: fmt.Sprintf("删除失败: %v", err)}, nil
		}
	}

	return tool.ToolResult{Output: fmt.Sprintf("已删除: %s", relPath)}, nil
}

// ── file_patch ──

type FilePatchTool struct {
	workspaceDir string
}

func NewFilePatchTool(workspaceDir string) *FilePatchTool {
	return &FilePatchTool{workspaceDir: workspaceDir}
}

func (t *FilePatchTool) Name() string { return "file_patch" }
func (t *FilePatchTool) Description() string {
	return "按行号范围替换文件内容（行级编辑），避免修改小段代码时需完整读写整个文件。支持 expected_content 乐观锁防止基于过期内容的编辑。"
}

func (t *FilePatchTool) InputSchema() json.RawMessage {
	return tool.BuildSchema(
		tool.SchemaParam{Name: "path", Type: "string", Description: "文件路径（相对于工作区）", Required: true},
		tool.SchemaParam{Name: "start_line", Type: "integer", Description: "起始行号（从 1 开始，含）", Required: true},
		tool.SchemaParam{Name: "end_line", Type: "integer", Description: "结束行号（含）", Required: true},
		tool.SchemaParam{Name: "content", Type: "string", Description: "替换后的新内容（可多行；传入空字符串 \"\" 表示删除该行范围）", Required: true},
		tool.SchemaParam{Name: "expected_content", Type: "string", Description: "预期被替换的原始内容（可选）；传入时若不匹配则拒绝执行", Required: false},
		tool.SchemaParam{Name: "context_before", Type: "string", Description: "（可选）目标块前 1-3 行的原始内容，用于上下文定位；仅在 expected_content 匹配失败时使用", Required: false},
		tool.SchemaParam{Name: "context_after", Type: "string", Description: "（可选）目标块后 1-3 行的原始内容，用于上下文定位；仅在 expected_content 匹配失败时使用", Required: false},
	)
}

func (t *FilePatchTool) Init(_ context.Context) error { return nil }
func (t *FilePatchTool) Close() error                 { return nil }

type filePatchArgs struct {
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	Content         string `json:"content"`
	ExpectedContent string `json:"expected_content"`
	ContextBefore   string `json:"context_before,omitempty"`
	ContextAfter    string `json:"context_after,omitempty"`
}

func (t *FilePatchTool) Execute(_ context.Context, args json.RawMessage) (tool.ToolResult, error) {
	var a filePatchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}, nil
	}

	if strings.TrimSpace(a.Path) == "" {
		return tool.ToolResult{Error: "path 不能为空"}, nil
	}
	if a.StartLine < 1 {
		return tool.ToolResult{Error: "start_line 必须 >= 1"}, nil
	}
	if a.EndLine < a.StartLine {
		return tool.ToolResult{Error: fmt.Sprintf("end_line (%d) 必须 >= start_line (%d)", a.EndLine, a.StartLine)}, nil
	}

	path, err := safeResolvePath(a.Path, t.workspaceDir)
	if err != nil {
		return tool.ToolResult{Error: err.Error()}, nil
	}

	// Protected file guard: block patching of mcp.json etc.
	if msg := checkProtectedFile(path, t.workspaceDir); msg != "" {
		return tool.ToolResult{Error: msg}, nil
	}

	// Open to read current content
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tool.ToolResult{Error: fmt.Sprintf("文件不存在: %s — 请先用 file_list 确认路径", a.Path)}, nil
		}
		return tool.ToolResult{Error: fmt.Sprintf("无法打开文件: %v", err)}, nil
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return tool.ToolResult{Error: fmt.Sprintf("读取文件信息失败: %v", err)}, nil
	}
	if info.IsDir() {
		f.Close()
		return tool.ToolResult{Error: "指定路径是目录，file_patch 仅支持文件"}, nil
	}
	if info.Size() > maxPatchFileSize {
		f.Close()
		return tool.ToolResult{Error: fmt.Sprintf("文件过大 (%d bytes)，超过 file_patch 上限 %d bytes — 请使用 file_write 替换整个文件", info.Size(), maxPatchFileSize)}, nil
	}

	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("读取文件失败: %v", err)}, nil
	}

	// Split preserving line endings
	lines := splitLines(string(data))
	totalLines := len(lines)

	// Validate line range; hard error (not silent) per design doc
	if a.EndLine > totalLines {
		return tool.ToolResult{Error: fmt.Sprintf("end_line %d 超出文件实际行数 %d — 请重新 file_read 后再编辑", a.EndLine, totalLines)}, nil
	}

	// Three-stage matching when expected_content is provided.
	// Stage 1: exact match (\r\n normalized)
	// Stage 2: whitespace-normalized match (TrimSpace per line)
	// Stage 3: context-based relocation (context_before/context_after)
	if a.ExpectedContent != "" {
		actual := strings.Join(lines[a.StartLine-1:a.EndLine], "")
		normalize := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

		// Stage 1: exact match
		if normalize(actual) != normalize(a.ExpectedContent) {
			// Stage 2: whitespace-normalized match
			if matchStage2(actual, a.ExpectedContent) {
				log.Printf("[file_patch:stage2] whitespace-normalized match: %s L%d-%d", a.Path, a.StartLine, a.EndLine)
			} else {
				// Stage 3: context-based relocation
				if a.ContextBefore != "" || a.ContextAfter != "" {
					expectedLen := a.EndLine - a.StartLine + 1
					newStart, newEnd, locErr := locateByContext(lines, expectedLen, a.ContextBefore, a.ContextAfter)
					if locErr != nil {
						return tool.ToolResult{Error: fmt.Sprintf("内容不匹配，上下文定位也失败: %v", locErr)}, nil
					}
					log.Printf("[file_patch:stage3] context-locate match: %s L%d-%d → L%d-%d", a.Path, a.StartLine, a.EndLine, newStart, newEnd)
					a.StartLine = newStart
					a.EndLine = newEnd
				} else {
					return tool.ToolResult{Error: "内容不匹配（已尝试精确/空白归一化匹配）。建议：1) 重新 file_read 获取最新内容；2) 提供 context_before/context_after 辅助定位"}, nil
				}
			}
		}
	}

	// Build updated line slice
	var newLines []string
	newLines = append(newLines, lines[:a.StartLine-1]...)
	if a.Content != "" {
		newLines = append(newLines, splitLines(a.Content)...)
	}
	// Append lines after the replaced range
	newLines = append(newLines, lines[a.EndLine:]...)

	if err := os.WriteFile(path, []byte(strings.Join(newLines, "")), info.Mode()); err != nil {
		return tool.ToolResult{Error: fmt.Sprintf("写入失败: %v", err)}, nil
	}

	oldCount := a.EndLine - a.StartLine + 1
	newCount := len(splitLines(a.Content)) // 0 when Content is empty
	relPath := relOrAbs(path, t.workspaceDir)

	return tool.ToolResult{
		Output: fmt.Sprintf("已修改: %s 第 %d-%d 行（原 %d 行 → 新 %d 行）", relPath, a.StartLine, a.EndLine, oldCount, newCount),
	}, nil
}

// splitLines splits text into segments preserving line endings.
// Each element includes the trailing '\n' (if present), except possibly the last.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// ── Three-stage matching helpers ─────────────────────────────────────────────

// splitNormalized normalizes line endings and splits into lines.
// Each element does NOT include the trailing newline.
// Empty lines are preserved — they are part of code structure.
func splitNormalized(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n") // strip one trailing newline to avoid phantom empty line
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// matchStage2 compares actual and expected content with whitespace normalization.
// Lines are split preserving empty lines, then each line is TrimSpace'd before comparison.
// Line count must be strictly equal.
func matchStage2(actual, expected string) bool {
	aLines := splitNormalized(actual)
	eLines := splitNormalized(expected)
	if len(aLines) != len(eLines) {
		return false
	}
	for i := range aLines {
		if strings.TrimSpace(aLines[i]) != strings.TrimSpace(eLines[i]) {
			return false
		}
	}
	return true
}

// nonEmptyTrimmed splits text into non-empty trimmed lines.
// Used by Stage 3 context matching where empty lines in context are noise.
func nonEmptyTrimmed(s string) []string {
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			result = append(result, t)
		}
	}
	return result
}

// matchContext checks if the lines starting at position `start` match the context lines
// (TrimSpace comparison). Each context line is already trimmed by nonEmptyTrimmed.
func matchContext(lines []string, start int, ctx []string) bool {
	if len(ctx) == 0 {
		return true
	}
	if start < 0 || start+len(ctx) > len(lines) {
		return false
	}
	for i, c := range ctx {
		if strings.TrimSpace(lines[start+i]) != c {
			return false
		}
	}
	return true
}

// locateByContext searches the file for a unique position where context_before and
// context_after both match, then returns the new line range (1-indexed).
// expectedLen is the original line range size (endLine - startLine + 1).
func locateByContext(lines []string, expectedLen int, ctxBefore, ctxAfter string) (startLine, endLine int, err error) {
	if expectedLen < 1 {
		return 0, 0, fmt.Errorf("expectedLen must be >= 1, got %d", expectedLen)
	}
	beforeLines := nonEmptyTrimmed(ctxBefore)
	afterLines := nonEmptyTrimmed(ctxAfter)
	if len(beforeLines) == 0 && len(afterLines) == 0 {
		return 0, 0, fmt.Errorf("context_before 和 context_after 均为空，无法定位")
	}

	// lines here uses splitLines which preserves line endings in each element.
	// We need to iterate using 0-indexed positions.
	var candidates []int
	for i := len(beforeLines); i <= len(lines)-expectedLen-len(afterLines); i++ {
		if matchContext(lines, i-len(beforeLines), beforeLines) &&
			matchContext(lines, i+expectedLen, afterLines) {
			candidates = append(candidates, i)
		}
	}

	switch len(candidates) {
	case 0:
		return 0, 0, fmt.Errorf("上下文未找到匹配位置")
	case 1:
		s := candidates[0] + 1 // 1-indexed
		return s, s + expectedLen - 1, nil
	default:
		return 0, 0, fmt.Errorf("上下文匹配到 %d 个位置（歧义），请提供更多 context_before/context_after", len(candidates))
	}
}
