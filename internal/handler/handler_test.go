package handler_test

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/handler"
	"github.com/tello-ai/tello-cli/internal/handler/handlertest"
)

func TestMain(m *testing.M) { handlertest.Main(m) }

func newApp() (*app.App, *handlertest.SyncBuffer) {
	stderr := &handlertest.SyncBuffer{}
	return &app.App{Stdout: &bytes.Buffer{}, Stderr: stderr, Getenv: func(string) string { return "" }}, stderr
}

func nextCommand(t *testing.T, b *handler.Bridge) handler.Command {
	t.Helper()
	select {
	case c := <-b.Commands():
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no command from handler")
		return handler.Command{}
	}
}

func waitExited(t *testing.T, b *handler.Bridge) {
	t.Helper()
	select {
	case <-b.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit")
	}
}

func TestSendDeliversFramesAndCommandsComeBack(t *testing.T) {
	t.Parallel()
	a, _ := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Echo))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(time.Second) })

	for _, typ := range []string{"cli.start", "user.turn"} {
		if err := b.Send(map[string]any{"type": typ, "text": "<예약> & 확인"}); err != nil {
			t.Fatal(err)
		}
		got := nextCommand(t, b)
		want := handler.Command{Event: "answer", Text: "got " + typ}
		if got != want {
			t.Fatalf("command = %+v, want %+v", got, want)
		}
	}
}

func TestInvalidLinesAreWarnedAndSkipped(t *testing.T) {
	t.Parallel()
	a, stderr := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Noise))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(time.Second) })

	if got, want := nextCommand(t, b), (handler.Command{Event: "sendDtmf", Digits: "1#", MessageID: "m1"}); got != want {
		t.Fatalf("first command = %+v, want %+v", got, want)
	}
	if got := nextCommand(t, b); got.Event != "cancel" {
		t.Fatalf("second command = %+v, want cancel", got)
	}
	warnings := strings.Count(stderr.String(), "tello: warning: ")
	if warnings != 3 {
		t.Fatalf("got %d warnings, want 3 (malformed, non-object, unknown command):\n%s", warnings, stderr)
	}
	if !strings.Contains(stderr.String(), `"hangup"`) {
		t.Fatalf("unknown command warning should name it:\n%s", stderr)
	}
}

func TestStartFailureIsHandlerError(t *testing.T) {
	t.Parallel()
	a, _ := newApp()
	for _, argv := range [][]string{{"/nonexistent/tello-handler"}, nil} {
		_, err := handler.Start(a, argv)
		var appErr *app.Error
		if !errors.As(err, &appErr) || appErr.Kind != app.KindHandler || appErr.Code != "handlerStartFailed" {
			t.Fatalf("Start(%q) error = %#v, want handler/handlerStartFailed", argv, err)
		}
	}
}

func TestExitIsReportedWithStatusAndStderrPassesThrough(t *testing.T) {
	t.Parallel()
	a, stderr := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Exit3))
	if err != nil {
		t.Fatal(err)
	}
	waitExited(t, b)
	var exitErr *exec.ExitError
	if !errors.As(b.Err(), &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("Err() = %v, want exit status 3", b.Err())
	}
	if !strings.Contains(stderr.String(), "boom\n") {
		t.Fatalf("handler stderr not passed through: %q", stderr)
	}
	if err := b.Send(map[string]any{"type": "user.turn"}); err == nil {
		t.Fatal("Send to an exited handler succeeded")
	}
}

func TestStopClosesInputAndWaitsForExit(t *testing.T) {
	t.Parallel()
	a, _ := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Echo))
	if err != nil {
		t.Fatal(err)
	}
	killed, err := b.Stop(5 * time.Second)
	if killed || err != nil {
		t.Fatalf("Stop = (killed %v, %v), want clean exit", killed, err)
	}
}

func TestStopKillsHandlerThatIgnoresEOF(t *testing.T) {
	t.Parallel()
	a, _ := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Hang))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	killed, _ := b.Stop(100 * time.Millisecond)
	if !killed {
		t.Fatal("Stop did not report the kill")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Stop took %v", elapsed)
	}
	waitExited(t, b)
}

// A handler that stops reading stdin must not stall the caller: once the
// pipe buffer is full, Send still returns at once.
func TestSendDoesNotBlockWhenHandlerStopsReading(t *testing.T) {
	t.Parallel()
	a, _ := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Hang))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(100 * time.Millisecond) })

	big := strings.Repeat("x", 64*1024)
	done := make(chan error, 1)
	go func() {
		for range 32 { // 2 MiB, far beyond any pipe buffer
			if err := b.Send(map[string]any{"type": "user.turn", "text": big}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send to a live handler failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send blocked on a handler that does not read stdin")
	}
}

// The handler never needs the API key; it must not see it in its
// environment. Other variables pass through.
func TestHandlerDoesNotInheritAPIKey(t *testing.T) {
	t.Setenv("TELLO_API_KEY", "secret-key")
	t.Setenv("TELLO_TEST_PASSTHROUGH", "kept")
	a, _ := newApp()
	b, err := handler.Start(a, handlertest.Command(t, handlertest.Env))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(time.Second) })
	if err := b.Send(map[string]any{"type": "cli.start"}); err != nil {
		t.Fatal(err)
	}
	if got, want := nextCommand(t, b).Text, "TELLO_API_KEY=unset TELLO_TEST_PASSTHROUGH=kept"; got != want {
		t.Fatalf("handler environment = %q, want %q", got, want)
	}
}
