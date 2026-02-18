package builtin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSafeResolvePathNormal(t *testing.T) {
	workspace := t.TempDir()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative file", "hello.txt", false},
		{"nested relative", "sub/dir/file.txt", false},
		{"dot path", "./test.txt", false},
		{"workspace root", ".", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := safeResolvePath(tt.path, workspace)
			if (err != nil) != tt.wantErr {
				t.Errorf("safeResolvePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
				return
			}
			if !tt.wantErr && resolved == "" {
				t.Error("resolved path should not be empty")
			}
		})
	}
}

func TestSafeResolvePathTraversal(t *testing.T) {
	workspace := t.TempDir()

	tests := []struct {
		name string
		path string
	}{
		{"dot-dot traversal", "../../etc/passwd"},
		{"dot-dot absolute", filepath.Join(workspace, "..", "evil.txt")},
		{"triple dot-dot", "../../../root/.ssh/id_rsa"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := safeResolvePath(tt.path, workspace)
			if err == nil {
				t.Errorf("safeResolvePath(%q) should have returned error for traversal", tt.path)
			}
		})
	}
}

func TestSafeResolvePathPrefixCollision(t *testing.T) {
	// Create two directories that share a prefix
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	evilDir := filepath.Join(base, "project-evil")
	os.MkdirAll(workspace, 0755)
	os.MkdirAll(evilDir, 0755)

	// Write a file in the evil directory
	evilFile := filepath.Join(evilDir, "attack.txt")
	os.WriteFile(evilFile, []byte("malicious"), 0644)

	// This is the critical test: absolute path with prefix collision
	_, err := safeResolvePath(evilFile, workspace)
	if err == nil {
		t.Errorf("safeResolvePath(%q, %q) should have blocked prefix collision", evilFile, workspace)
	}
}

func TestSafeResolvePathExactWorkspace(t *testing.T) {
	workspace := t.TempDir()

	// The workspace directory itself should be allowed
	resolved, err := safeResolvePath(workspace, workspace)
	if err != nil {
		t.Errorf("safeResolvePath(workspace, workspace) should be allowed: %v", err)
	}
	absWorkspace, _ := filepath.Abs(workspace)
	absResolved, _ := filepath.Abs(resolved)
	if absResolved != absWorkspace {
		t.Errorf("resolved %q != workspace %q", absResolved, absWorkspace)
	}
}

func TestSafeResolvePathAbsolute(t *testing.T) {
	workspace := t.TempDir()

	// Absolute path inside workspace
	insidePath := filepath.Join(workspace, "sub", "file.txt")
	_, err := safeResolvePath(insidePath, workspace)
	if err != nil {
		t.Errorf("absolute path inside workspace should be allowed: %v", err)
	}

	// Absolute path outside workspace
	var outsidePath string
	if runtime.GOOS == "windows" {
		outsidePath = "C:\\Windows\\System32\\evil.dll"
	} else {
		outsidePath = "/etc/passwd"
	}
	_, err = safeResolvePath(outsidePath, workspace)
	if err == nil {
		t.Errorf("absolute path outside workspace should be blocked")
	}
}

func TestSafeResolvePathNoWorkspace(t *testing.T) {
	// When workspaceDir is empty, any path is allowed (no sandbox)
	resolved, err := safeResolvePath("any/path.txt", "")
	if err != nil {
		t.Errorf("with empty workspace, all paths should be allowed: %v", err)
	}
	if resolved == "" {
		t.Error("resolved should not be empty")
	}
}
