//go:build unix

package handler

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// isolate starts the handler in its own process group, so a terminal Ctrl-C
// (sent to the CLI's foreground group) does not reach it: the CLI cancels
// the call and then ends the handler itself.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// kill ends the handler's whole process group, including grandchildren a
// wrapper (sh -c, npm run) may have started.
func kill(proc *os.Process) error {
	err := syscall.Kill(-proc.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
