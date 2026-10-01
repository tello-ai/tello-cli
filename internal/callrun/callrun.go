// Package callrun runs one call on one gateway connection: it places the
// call, streams gateway frames to stdout and to the answer source, relays
// the answer source's commands, and maps how the call ended to a result
// (docs/design.md section 8).
package callrun

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tello-ai/tello-go/tello"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/handler"
)

const (
	// cancelWait bounds the wait for the terminal event after we sent cancel.
	cancelWait = 10 * time.Second
	// finalWait bounds the wait for the event that ended the call once
	// WaitClosed returned: the SDK closes the call before emitting it.
	finalWait = 2 * time.Second
	// handlerGrace is how long a handler may take to exit after its input
	// closed at the end of the call.
	handlerGrace = 5 * time.Second
)

// eventTypes are every frame type the SDK emits. All but disconnected are
// gateway frames written to stdout and forwarded to the handler.
var eventTypes = []string{
	tello.EventTypeCallCreated,
	tello.EventTypeCallStatusChanged,
	tello.EventTypeUserTurn,
	tello.EventTypeAgentTurn,
	tello.EventTypeAnswerAccepted,
	tello.EventTypeDtmfAccepted,
	tello.EventTypeCallCompleted,
	tello.EventTypeCallNoAnswer,
	tello.EventTypeCallFailed,
	tello.EventTypeCallSummary,
	tello.EventTypeError,
	tello.EventTypeDisconnected,
}

// Options describe one call. Exactly one answer source is set: Input
// (interactive) or Handler (argv of the handler process).
type Options struct {
	To       string
	Prompt   string
	Metadata map[string]any
	Input    *Input
	Handler  []string
	// Timeout cancels the call when it elapses; 0 means none.
	Timeout time.Duration
	// Index is the batch task index, added to the handler's cli.start.
	Index *int
}

// Result identifies the call. Run fills it as far as the call got, also
// when it returns an error.
type Result struct {
	CallID string
	Status string
}

// Run places one call and blocks until it ends. Cancelling ctx is an
// interrupt: the call is cancelled and Run returns an interrupted error.
// A completed call returns nil; every other outcome an error whose Data
// carries the callId and last status when the call got that far.
func Run(ctx context.Context, a *app.App, opts Options) (Result, error) {
	if (opts.Input == nil) == (len(opts.Handler) == 0) {
		return Result{}, app.NewError(app.KindInternal, "internal", "callrun: exactly one answer source is required")
	}
	client, _, err := a.NewClient()
	if err != nil {
		return Result{}, err
	}
	r := &runner{a: a, opts: opts, client: client, queue: newQueue()}
	res, err := r.run(ctx)
	_ = client.Close()
	if r.bridge != nil {
		r.stopHandler()
	}
	return res, err
}

type runner struct {
	a      *app.App
	opts   Options
	client *tello.Client
	queue  *queue
	bridge *handler.Bridge

	callID       string
	status       string
	terminal     *tello.Event
	errorCodes   map[string]bool
	disconnected bool

	handlerExited bool
	sendFailed    bool
	// cancelPending is set when a cancel went out before call.created: the
	// gateway ignores a cancel while no call exists (sdk-ws.v1 section
	// 4.4), so it is sent again once the call does.
	cancelPending bool
}

func (r *runner) run(ctx context.Context) (Result, error) {
	if len(r.opts.Handler) > 0 {
		// Before connecting: a handler that cannot start must not cost a call.
		b, err := handler.Start(r.a, r.opts.Handler)
		if err != nil {
			return Result{}, err
		}
		r.bridge = b
		r.forward(r.startFrame())
	}
	for _, typ := range eventTypes {
		// SDK handlers run on the receive goroutine; only enqueue there.
		r.client.On(typ, func(_ context.Context, ev tello.Event) error {
			r.queue.push(ev)
			return nil
		})
	}
	if err := r.a.Connect(ctx, r.client); err != nil {
		return Result{}, err
	}
	if ctx.Err() != nil {
		return Result{}, interrupted()
	}
	if r.bridge != nil {
		select {
		case <-r.bridge.Exited():
			r.handlerExited = true
			return Result{}, handlerExitedError(r.bridge.Err())
		default:
		}
	}
	if r.opts.Input != nil && !r.a.JSON {
		fmt.Fprintln(r.a.Stderr, "Type a reply and press Enter. /help lists commands.")
	}

	// SDK calls from here on use internal, which outlives an interrupt.
	internal, stop := context.WithCancel(context.Background())
	defer stop()
	if err := r.client.CreateCall(internal, r.opts.To, r.opts.Prompt, r.opts.Metadata, ""); err != nil {
		return Result{}, err
	}
	return r.loop(ctx, internal)
}

func (r *runner) loop(ctx, internal context.Context) (Result, error) {
	waitDone := make(chan error, 1)
	go func() { waitDone <- r.client.WaitClosed(internal) }()

	interrupt := ctx.Done()
	var timeout <-chan time.Time
	if r.opts.Timeout > 0 {
		t := time.NewTimer(r.opts.Timeout)
		defer t.Stop()
		timeout = t.C
	}
	var lines <-chan string
	if r.opts.Input != nil {
		lines = r.opts.Input.lines
	}
	var (
		commands <-chan handler.Command
		exited   <-chan struct{}
	)
	if r.bridge != nil {
		commands = r.bridge.Commands()
		exited = r.bridge.Exited()
	}

	var (
		reason *app.Error
		// fallback is the outcome when the cancel wait expires without a
		// terminal event and no reason is set.
		fallback         *app.Error
		cancelDeadline   <-chan time.Time
		finalDeadline    <-chan time.Time
		waitErr          error
		ended            bool
		handlerCancelled bool
	)
	// cancelFor cancels the call on our side; reason becomes the outcome.
	cancelFor := func(e *app.Error) {
		reason = e
		r.sendCancel(internal)
		cancelDeadline = time.After(cancelWait)
		interrupt, timeout, exited = nil, nil, nil
	}
	drain := func() {
		r.drain()
		if !r.cancelPending || r.callID == "" {
			return
		}
		r.cancelPending = false
		if !ended && r.terminal == nil {
			r.sendCancel(internal)
			if cancelDeadline != nil {
				cancelDeadline = time.After(cancelWait)
			}
		}
	}

loop:
	for !ended || !r.finalSeen(waitErr) {
		select {
		case <-r.queue.ready:
			drain()
		case waitErr = <-waitDone:
			ended = true
			waitDone = nil
			finalDeadline = time.After(finalWait)
			// The call is over: nothing left to cancel or answer.
			interrupt, timeout, exited, commands, lines = nil, nil, nil, nil, nil
		case <-finalDeadline:
			break loop
		case <-cancelDeadline:
			if reason == nil {
				reason = fallback
			}
			break loop
		case <-interrupt:
			cancelFor(interrupted())
		case <-timeout:
			cancelFor(app.NewError(app.KindCallEnded, "timeout", fmt.Sprintf("call cancelled after --timeout %s", r.opts.Timeout)))
		case <-exited:
			if handlerCancelled {
				// The handler hung up, then left: the call ends as
				// cancelled. The bridge delivers every command before
				// reporting the exit, so this order is reliable.
				exited = nil
				fallback = handlerExitedError(r.bridge.Err())
				cancelDeadline = time.After(cancelWait)
				continue
			}
			r.handlerExited = true
			cancelFor(handlerExitedError(r.bridge.Err()))
		case c := <-commands:
			if c.Event == "cancel" {
				handlerCancelled = true
			}
			r.apply(internal, c)
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			r.interactive(internal, line)
		}
	}
	r.drain()
	return r.outcome(waitErr, ended, reason)
}

func interrupted() *app.Error {
	return app.NewError(app.KindInterrupted, "interrupted", "interrupted; call cancelled")
}

func handlerExitedError(exitErr error) *app.Error {
	status := "exit status 0"
	if exitErr != nil {
		status = exitErr.Error()
	}
	return app.NewError(app.KindHandler, "handlerExited", fmt.Sprintf("handler exited before the call ended (%s)", status))
}

// finalSeen reports whether the event that ended the call was processed:
// the terminal event for a normal end, the matching error frame for a
// gateway error, or the disconnect for a lost connection.
func (r *runner) finalSeen(waitErr error) bool {
	if r.disconnected {
		return true
	}
	if waitErr == nil {
		return r.terminal != nil
	}
	return r.errorCodes[app.Classify(waitErr).Code]
}

func (r *runner) outcome(waitErr error, ended bool, reason *app.Error) (Result, error) {
	res := Result{CallID: r.callID, Status: r.status}
	if reason != nil {
		return res, r.withData(reason)
	}
	if waitErr != nil {
		return res, r.withData(waitErr)
	}
	if !ended || r.terminal == nil {
		return res, r.withData(app.NewError(app.KindInternal, "internal", "call ended without a terminal event"))
	}
	ev := r.terminal
	switch ev.Type {
	case tello.EventTypeCallCompleted:
		return res, nil
	case tello.EventTypeCallNoAnswer:
		return res, r.withData(app.NewError(app.KindCallEnded, "noAnswer", orDefault(ev.FailureReason, "the callee did not answer")))
	case tello.EventTypeCallFailed:
		return res, r.withData(app.NewError(app.KindCallEnded, "callFailed", orDefault(ev.FailureReason, "the call failed")))
	default:
		return res, r.withData(app.NewError(app.KindCallEnded, "cancelled", "the call was cancelled"))
	}
}

// withData attaches the callId and last status to err. Errors of calls that
// never got a callId or status are returned unchanged.
func (r *runner) withData(err error) error {
	data := map[string]any{}
	if r.callID != "" {
		data["callId"] = r.callID
	}
	if r.status != "" {
		data["status"] = r.status
	}
	if len(data) == 0 {
		return err
	}
	e := *app.Classify(err)
	e.Data = data
	return &e
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (r *runner) startFrame() map[string]any {
	metadata := r.opts.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	frame := map[string]any{"type": "cli.start", "to": r.opts.To, "prompt": r.opts.Prompt, "metadata": metadata}
	if r.opts.Index != nil {
		frame["index"] = *r.opts.Index
	}
	return frame
}

// drain processes every queued event in arrival order.
func (r *runner) drain() {
	for _, ev := range r.queue.take() {
		r.process(ev)
	}
}

func (r *runner) process(ev tello.Event) {
	if ev.Type == tello.EventTypeDisconnected {
		r.disconnected = true
		return
	}
	switch {
	case ev.Type == tello.EventTypeCallCreated:
		r.callID = ev.CallID
		r.status = "queued"
	case ev.Type == tello.EventTypeError:
		if r.errorCodes == nil {
			r.errorCodes = map[string]bool{}
		}
		r.errorCodes[ev.Code] = true
	case ev.Type == tello.EventTypeCallStatusChanged || tello.IsTerminal(ev):
		if ev.Status != "" {
			r.status = ev.Status
		}
	}
	if tello.IsTerminal(ev) {
		r.terminal = &ev
	}
	r.forward(ev.Raw)
	if r.a.JSON {
		_ = r.a.WriteEvent(ev)
		return
	}
	r.render(ev)
}

func (r *runner) render(ev tello.Event) {
	switch ev.Type {
	case tello.EventTypeCallCreated:
		r.a.Printf("call %s created\n", ev.CallID)
	case tello.EventTypeCallStatusChanged:
		r.a.Printf("status: %s\n", ev.Status)
	case tello.EventTypeUserTurn:
		r.a.Printf("user> %s\n", ev.Text)
	case tello.EventTypeAgentTurn:
		r.a.Printf("agent> %s\n", ev.Text)
	case tello.EventTypeDtmfAccepted:
		r.a.Printf("dtmf> %s\n", ev.Digits)
	case tello.EventTypeCallCompleted:
		r.a.Printf("call %s completed\n", ev.CallID)
	case tello.EventTypeCallNoAnswer, tello.EventTypeCallFailed:
		line := fmt.Sprintf("call %s ended: %s", ev.CallID, ev.Status)
		if ev.FailureReason != "" {
			line += " (" + ev.FailureReason + ")"
		}
		r.a.Printf("%s\n", line)
	case tello.EventTypeError:
		// Diagnostics go to stderr in human mode (docs/design.md section 5).
		r.a.Warnf("gateway error [%s]: %s", ev.Code, ev.Message)
	}
}

// forward sends a frame to the handler, if any. Once the handler stops
// reading, the failure is reported once; its exit is detected separately.
func (r *runner) forward(frame any) {
	if r.bridge == nil || r.sendFailed {
		return
	}
	if err := r.bridge.Send(frame); err != nil {
		r.sendFailed = true
		r.a.Warnf("handler is not reading its input: %v", err)
	}
}

// apply sends a command from the answer source (handler or terminal).
func (r *runner) apply(ctx context.Context, c handler.Command) {
	var err error
	switch c.Event {
	case "answer":
		err = r.client.Answer(ctx, c.Text, c.MessageID, c.RequestID)
	case "sendDtmf":
		err = r.client.SendDtmf(ctx, c.Digits, c.MessageID, c.RequestID)
	case "cancel":
		r.sendCancel(ctx)
	}
	if err != nil {
		r.a.Warnf("send %s: %v", c.Event, err)
	}
}

// sendCancel sends cancel and remembers whether it went out before the
// call existed, so the loop can repeat it after call.created.
func (r *runner) sendCancel(ctx context.Context) {
	r.cancelPending = r.callID == ""
	if err := r.client.Cancel(ctx); err != nil {
		r.a.Warnf("send cancel: %v", err)
	}
}

const interactiveHelp = `Commands:
  <text>          answer with text
  /dtmf <digits>  send DTMF tones (0-9, *, #)
  /cancel         cancel the call
  /help           show this help
`

func (r *runner) interactive(ctx context.Context, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	cmd, arg, _ := strings.Cut(line, " ")
	switch cmd {
	case "/help":
		fmt.Fprint(r.a.Stderr, interactiveHelp)
	case "/cancel":
		r.apply(ctx, handler.Command{Event: "cancel"})
	case "/dtmf":
		digits := strings.TrimSpace(arg)
		if digits == "" {
			fmt.Fprintln(r.a.Stderr, "usage: /dtmf <digits>")
			return
		}
		r.apply(ctx, handler.Command{Event: "sendDtmf", Digits: digits})
	default:
		if strings.HasPrefix(cmd, "/") {
			fmt.Fprintf(r.a.Stderr, "unknown command %s; /help lists commands\n", cmd)
			return
		}
		r.apply(ctx, handler.Command{Event: "answer", Text: line})
	}
}

// stopHandler ends the handler after the call: its exit status no longer
// changes the result, so problems are only warned about.
func (r *runner) stopHandler() {
	killed, err := r.bridge.Stop(handlerGrace)
	switch {
	case killed:
		r.a.Warnf("handler did not exit within %s after its input closed; killed", handlerGrace)
	case err != nil && !r.handlerExited:
		r.a.Warnf("handler exited with %v", err)
	}
}

// queue is the unbounded event queue between the SDK's receive goroutine
// and the run loop, so a slow consumer never stalls ping handling.
type queue struct {
	mu     sync.Mutex
	events []tello.Event
	ready  chan struct{}
}

func newQueue() *queue {
	return &queue{ready: make(chan struct{}, 1)}
}

func (q *queue) push(ev tello.Event) {
	q.mu.Lock()
	q.events = append(q.events, ev)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *queue) take() []tello.Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	events := q.events
	q.events = nil
	return events
}
