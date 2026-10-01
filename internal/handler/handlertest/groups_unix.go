//go:build unix

package handlertest

import (
	"fmt"
	"os"
	"syscall"
)

func processGroups() string {
	parent, err := syscall.Getpgid(os.Getppid())
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("self=%d parent=%d", syscall.Getpgrp(), parent)
}
