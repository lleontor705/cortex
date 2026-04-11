//go:build windows

package ollama

import (
	"os/exec"
	"syscall"
)

// detachProcess configures the command to run as a detached process on Windows.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
