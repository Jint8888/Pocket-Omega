//go:build !windows

package builtin

import (
	"context"
	"os/exec"
)

// newShellCmd creates a shell command for non-Windows platforms using sh -c.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}
