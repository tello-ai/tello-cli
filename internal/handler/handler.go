// Package handler bridges a handler child process over NDJSON
// (docs/design.md section 8.2): gateway frames go to its stdin one JSON
// object per line, and the commands it prints on stdout come back as
// Command values. Its stderr is passed through to the CLI's stderr.
package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tello-ai/tello-go/tello"

	"github.com/tello-ai/tello-cli/internal/app"
)

// maxLine bounds one handler stdout line.
const maxLine = 1 << 20

// drainGrace is how long exit detection waits for the handler's stdout to
// reach EOF after the process exited, so commands printed right before
// exiting are delivered first. A grandchild that inherited stdout can keep
// it open; exit is then reported anyway.
const drainGrace = time.Second

// Command is one command the handler printed: "answer", "sendDtmf" or
// "cancel".
type Command struct {
	Event     string
	Text      string
	Digits    string
	MessageID string
	RequestID string
}

// Bridge is a running handler process.
type Bridge struct {
	a     *app.App
	proc  *exec.Cmd
	stdin io.WriteCloser

	// Input lines wait in an unbounded queue for the writer goroutine, so
	// Send never blocks on a handler that stops reading stdin.
	inMu     sync.Mutex
	inQueue  [][]byte
	inClosed bool
	inErr    error
	inReady  chan struct{}

	commands   chan Command
	stop       chan struct{}
	stopOnce   sync.Once
	readerDone chan struct{}
	exited     chan struct{}
	exitErr    error
}

var (
	errInputClosed = errors.New("handler input is closed")
	errExited      = errors.New("handler has exited")
)

// Start runs argv (no shell). Failures are handlerStartFailed errors.
func Start(a *app.App, argv []string) (*Bridge, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, startFailed("no handler command given")
	}
	proc := exec.Command(argv[0], argv[1:]...)
	proc.Stderr = a.Stderr
	proc.Env = childEnv(os.Environ())
	isolate(proc)
	stdin, err := proc.StdinPipe()
	if err != nil {
		return nil, startFailed(fmt.Sprintf("start handler %q: %v", argv[0], err))
	}
	// A plain pipe instead of StdoutPipe: Wait then returns when the process
	// exits even if a grandchild keeps stdout open.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, startFailed(fmt.Sprintf("start handler %q: %v", argv[0], err))
	}
	proc.Stdout = stdoutW
	if err := proc.Start(); err != nil {
		stdin.Close()
		stdoutR.Close()
		stdoutW.Close()
		return nil, startFailed(fmt.Sprintf("start handler %q: %v", argv[0], err))
	}
	stdoutW.Close()

	b := &Bridge{
		a:          a,
		proc:       proc,
		stdin:      stdin,
		inReady:    make(chan struct{}, 1),
		commands:   make(chan Command),
		stop:       make(chan struct{}),
		readerDone: make(chan struct{}),
		exited:     make(chan struct{}),
	}
	go b.read(stdoutR)
	go b.write()
	go b.wait()
	return b, nil
}

// childEnv is the CLI's environment without the API key: the handler never
// needs it, and a crash dump of its environment would end up in logs.
func childEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		// Windows variable names are case-insensitive.
		if strings.EqualFold(key, tello.EnvAPIKey) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func startFailed(message string) *app.Error {
	return app.NewError(app.KindHandler, "handlerStartFailed", message)
}

// Send queues frame as one JSON line (UTF-8, no HTML escaping) for the
// handler's stdin and returns without waiting for the handler to read it.
// It fails once the handler exited, its input was closed, or an earlier
// write failed.
func (b *Bridge) Send(frame any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(frame); err != nil {
		return err
	}
	select {
	case <-b.exited:
		return errExited
	default:
	}
	b.inMu.Lock()
	defer b.inMu.Unlock()
	switch {
	case b.inErr != nil:
		return b.inErr
	case b.inClosed:
		return errInputClosed
	}
	b.inQueue = append(b.inQueue, buf.Bytes())
	b.signalInput()
	return nil
}

// Commands delivers the handler's valid commands in order.
func (b *Bridge) Commands() <-chan Command { return b.commands }

// Exited is closed when the handler process has exited.
func (b *Bridge) Exited() <-chan struct{} { return b.exited }

// Err is the process exit error (nil for exit 0). Valid once Exited is
// closed.
func (b *Bridge) Err() error {
	<-b.exited
	return b.exitErr
}

// CloseInput closes the handler's stdin, its signal to finish, once the
// lines already sent are written.
func (b *Bridge) CloseInput() {
	b.inMu.Lock()
	defer b.inMu.Unlock()
	b.inClosed = true
	b.signalInput()
}

// signalInput wakes the writer. Callers hold inMu.
func (b *Bridge) signalInput() {
	select {
	case b.inReady <- struct{}{}:
	default:
	}
}

// write copies queued lines to the handler's stdin until the input is
// closed or a write fails. A handler that stops reading blocks only this
// goroutine; it is released when the handler exits or is killed.
func (b *Bridge) write() {
	defer b.stdin.Close()
	for range b.inReady {
		b.inMu.Lock()
		pending, closed := b.inQueue, b.inClosed
		b.inQueue = nil
		b.inMu.Unlock()
		for _, line := range pending {
			if _, err := b.stdin.Write(line); err != nil {
				b.inMu.Lock()
				b.inErr = err
				b.inQueue = nil
				b.inMu.Unlock()
				return
			}
		}
		if closed {
			return
		}
	}
}

// Stop stops delivering commands, closes the handler's stdin and waits up
// to grace for it to exit before killing it. It returns whether the handler
// was killed and its exit error.
func (b *Bridge) Stop(grace time.Duration) (killed bool, err error) {
	b.stopOnce.Do(func() { close(b.stop) })
	b.CloseInput()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-b.exited:
		return false, b.exitErr
	case <-timer.C:
	}
	if err := kill(b.proc.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		b.a.Warnf("kill handler: %v", err)
	}
	<-b.exited
	return true, b.exitErr
}

func (b *Bridge) wait() {
	err := b.proc.Wait()
	b.CloseInput() // release the writer
	select {
	case <-b.readerDone:
	case <-time.After(drainGrace):
	}
	b.exitErr = err
	close(b.exited)
}

func (b *Bridge) read(stdout *os.File) {
	defer close(b.readerDone)
	defer stdout.Close()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		cmd, ok := b.parse(line)
		if !ok {
			continue
		}
		select {
		case b.commands <- cmd:
		case <-b.stop:
		}
	}
	if err := scanner.Err(); err != nil {
		b.a.Warnf("handler stdout: %v; ignoring further output", err)
		// Keep draining so the handler never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, stdout)
	}
}

func (b *Bridge) parse(line []byte) (Command, bool) {
	var envelope struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		b.a.Warnf("handler printed a line that is not a JSON command object, ignored: %s", clip(line))
		return Command{}, false
	}
	switch envelope.Event {
	case "answer", "sendDtmf", "cancel":
	default:
		b.a.Warnf("handler printed unsupported command %q, ignored (allowed: answer, sendDtmf, cancel)", envelope.Event)
		return Command{}, false
	}
	var data struct {
		Text      string `json:"text"`
		Digits    string `json:"digits"`
		MessageID string `json:"messageId"`
		RequestID string `json:"requestId"`
	}
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			b.a.Warnf("handler printed %s with invalid data, ignored: %s", envelope.Event, clip(line))
			return Command{}, false
		}
	}
	return Command{
		Event:     envelope.Event,
		Text:      data.Text,
		Digits:    data.Digits,
		MessageID: data.MessageID,
		RequestID: data.RequestID,
	}, true
}

func clip(line []byte) string {
	const max = 200
	if len(line) > max {
		return string(line[:max]) + "…"
	}
	return string(line)
}
