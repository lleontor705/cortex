//go:build !windows

package ollama

import (
	"os/exec"
	"syscall"
)

// detachProcess configures the command to run in its own process group on Unix.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}
