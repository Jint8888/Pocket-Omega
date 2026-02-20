//go:build windows

package builtin

import (
	"context"
	"os/exec"
	"syscall"
)

// newShellCmd creates a shell command for Windows using SysProcAttr.CmdLine
// to bypass Go's argument escaping, which uses backslash-escaped quotes (\")
// that cmd.exe does not recognise (cmd.exe uses "" doubling, not \").
// Passing the raw command string via CmdLine ensures that PowerShell and other
// tools receive arguments exactly as the caller intended.
//
// chcp 65001 switches the console code page to UTF-8 before running the user
// command, so that PowerShell (and other tools) emit UTF-8 output instead of
// the system OEM code page (e.g. GBK on Chinese Windows). Without this, Chinese
// characters in PowerShell -Verbose / error messages appear as mojibake.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: "cmd /c chcp 65001 >nul & " + command,
	}
	return cmd
}
