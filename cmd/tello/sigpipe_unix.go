//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

// A closed stdout pipe must be reported as EPIPE so call cleanup can run.
func ignoreBrokenPipe() { signal.Ignore(syscall.SIGPIPE) }
