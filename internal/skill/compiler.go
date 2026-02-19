package skill

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// BinaryName returns the platform-appropriate compiled skill binary name.
// Windows: "skill.exe"  |  Other: "skill"
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "skill.exe"
	}
	return "skill"
}

// ensureCompiled compiles a Go skill if the binary is missing.
// If the binary already exists, this is a no-op (fast path for normal execution).
func ensureCompiled(def *SkillDef) error {
	binPath := filepath.Join(def.Dir, BinaryName())
	if _, err := os.Stat(binPath); err == nil {
		return nil // binary already present
	}
	return CompileGoSkill(def.Dir)
}

// CompileGoSkill runs "go build -o skill[.exe] ." in the given directory.
// On success the binary is written into the same directory.
// On failure a human-readable error (including compiler output) is returned.
func CompileGoSkill(dir string) error {
	outputPath := filepath.Join(dir, BinaryName())
	cmd := exec.Command("go", "build", "-o", outputPath, ".")
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build 失败 in %q: %w\n%s", dir, err, string(out))
	}
	return nil
}
