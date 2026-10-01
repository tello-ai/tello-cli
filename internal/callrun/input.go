package callrun

import (
	"bufio"
	"io"
)

// Input is the interactive answer source: lines typed on stdin. One Input
// serves every call of a command, so lines are never lost between batch
// tasks.
type Input struct {
	lines chan string
}

// NewInput starts reading r line by line. EOF ends input but not a call.
// The reader goroutine lives until EOF (stdin reads cannot be cancelled).
func NewInput(r io.Reader) *Input {
	in := &Input{lines: make(chan string)}
	go func() {
		defer close(in.lines)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			in.lines <- scanner.Text()
		}
	}()
	return in
}
