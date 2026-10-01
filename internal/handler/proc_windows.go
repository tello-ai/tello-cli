//go:build windows

package handler

import (
	"os"
	"os/exec"
	"syscall"
)

// isolate starts the handler in a new process group, which disables console
// Ctrl-C for it: the CLI cancels the call and then ends the handler itself.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func kill(proc *os.Process) error {
	return proc.Kill()
}
