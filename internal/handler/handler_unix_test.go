//go:build unix

package handler_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/handler"
	"github.com/tello-ai/tello-cli/internal/handler/handlertest"
)

// A terminal Ctrl-C signals the whole foreground process group. The handler
// must not be in it: the CLI cancels the call and ends the handler itself
// (stdin EOF, then kill), so the handler still sees the terminal event.
func TestHandlerRunsInItsOwnProcessGroup(t *testing.T) {
	t.Parallel()
	a, _ := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Groups))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(time.Second) })

	if err := b.Send(map[string]any{"type": "cli.start"}); err != nil {
		t.Fatal(err)
	}
	var self, parent int
	got := nextCommand(t, b).Text
	if _, err := fmt.Sscanf(got, "self=%d parent=%d", &self, &parent); err != nil {
		t.Fatalf("unexpected probe answer %q", got)
	}
	if self == parent {
		t.Fatalf("handler shares the CLI's process group %d", self)
	}
}
