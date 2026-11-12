package call

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/callrun"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// endGrace bounds how long a cancelled wait keeps polling for the call to
// settle after asking the service to end it.
const endGrace = 10 * time.Second

// maxPollFailures is how many polls in a row may fail on a transient error
// before waiting gives up (the call itself keeps running).
const maxPollFailures = 3

// target is --agent / --crew: a call answered by a portal agent or crew
// through the REST API instead of by this process.
type target struct {
	agent, crew  string
	pollInterval time.Duration
}

func (t *target) bind(f *pflag.FlagSet) {
	f.StringVar(&t.agent, "agent", "", "agent id (tello agent list); the portal agent answers the call")
	f.StringVar(&t.crew, "crew", "", "crew id (tello crew list); the portal crew answers the call")
	f.DurationVar(&t.pollInterval, "poll-interval", 2*time.Second, "how often to check the call while waiting")
}

func (t *target) set() bool { return t.agent != "" || t.crew != "" }

// validate checks target flags against the WebSocket answer sources. argv
// and interactive are the handler command and -i.
func (t *target) validate(argv []string, interactive bool) error {
	switch {
	case t.agent != "" && t.crew != "":
		return app.Usagef("use either --agent or --crew, not both")
	case interactive || argv != nil:
		return app.Usagef("--agent/--crew calls are answered by the portal; drop -i and the handler command")
	case t.pollInterval <= 0:
		return app.Usagef("--poll-interval must be positive")
	}
	return nil
}

func (t *target) request(to string, vars map[string]any) rest.CreateCall {
	return rest.CreateCall{AgentID: t.agent, CrewID: t.crew, To: to, Variables: vars}
}

// perCall are the per-call settings flags of REST calls and call drafts.
type perCall struct {
	rest.PerCall
	ivrGoal, openingMessage string
}

func (p *perCall) bind(f *pflag.FlagSet) {
	f.StringVar(&p.Instructions, "instructions", "", "--agent only: goal of this call, up to 2000 characters")
	f.IntVar(&p.MaxDurationSeconds, "max-duration-seconds", 0, "maximum call length in seconds, 30 to 1800 (default: the service's)")
	f.StringVar(&p.ivrGoal, "ivr-goal", "", "--agent only: where to go when a phone menu answers, e.g. 상담원 연결 (up to 200 characters)")
	f.StringVar(&p.openingMessage, "ivr-opening-message", "", "--agent only: first sentence once the callee answers (up to 200 characters)")
	f.BoolVar(&p.NotifyOnEnd, "notify-on-end", false, "--agent only: text the account's notification number when the call ends")
}

// set reports whether any setting is given.
func (p *perCall) set() bool {
	return p.Instructions != "" || p.MaxDurationSeconds != 0 || p.ivrGoal != "" || p.openingMessage != "" || p.NotifyOnEnd
}

// settings validates the flags the API documents limits for and returns
// the settings. Crews take only --max-duration-seconds.
func (p *perCall) settings(crew bool) (rest.PerCall, error) {
	if p.MaxDurationSeconds != 0 && (p.MaxDurationSeconds < 30 || p.MaxDurationSeconds > 1800) {
		return rest.PerCall{}, app.Usagef("--max-duration-seconds must be 30 to 1800")
	}
	if crew && (p.Instructions != "" || p.ivrGoal != "" || p.openingMessage != "" || p.NotifyOnEnd) {
		return rest.PerCall{}, app.Usagef("--instructions, --ivr-goal, --ivr-opening-message and --notify-on-end are for --agent calls; crews keep them per member, pass per-call values with --var")
	}
	s := p.PerCall
	if p.ivrGoal != "" || p.openingMessage != "" {
		s.IVR = &rest.IVR{Goal: p.ivrGoal, OpeningMessage: p.openingMessage}
	}
	return s, nil
}

// parseVars turns repeated --var name=value into the variables object.
func parseVars(args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	vars := make(map[string]any, len(args))
	for _, arg := range args {
		name, value, ok := strings.Cut(arg, "=")
		switch {
		case !ok:
			return nil, app.Usagef("--var %q must be name=value", arg)
		case name == "":
			return nil, app.Usagef("--var %q has an empty name", arg)
		}
		if _, dup := vars[name]; dup {
			return nil, app.Usagef("--var %s is given more than once", name)
		}
		vars[name] = value
	}
	return vars, nil
}

// createCall sends exactly one POST. If the caller stops waiting while it is
// in flight, keep that request alive briefly to recover its ID for cleanup.
func createCall(ctx context.Context, c *rest.Client, req rest.CreateCall, key string, w waiter) (rest.Object, error) {
	if err := ctx.Err(); err != nil {
		e := app.Classify(err)
		if err == context.DeadlineExceeded {
			e = app.NewError(app.KindCallEnded, "timeout", "--timeout elapsed before call creation")
		}
		e.Data = map[string]any{"idempotencyKey": key}
		return nil, e
	}
	requestCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	type response struct {
		call rest.Object
		err  error
	}
	done := make(chan response, 1)
	go func() {
		call, err := c.CreateCall(requestCtx, req, key)
		done <- response{call: call, err: err}
	}()

	var r response
	select {
	case r = <-done:
		if ctx.Err() == nil {
			if r.err == nil {
				return r.call, nil
			}
			return nil, app.WithIdempotencyKey(r.err, key)
		}
	case <-ctx.Done():
		timer := time.NewTimer(endGrace)
		defer timer.Stop()
		select {
		case r = <-done:
		case <-timer.C:
		}
	}

	kind, code, reason := app.KindInterrupted, "interrupted", "interrupted"
	if ctx.Err() == context.DeadlineExceeded {
		kind, code, reason = app.KindCallEnded, "timeout", "--timeout elapsed"
	}
	data := map[string]any{"idempotencyKey": key}
	if r.err != nil || rest.ID(r.call) == "" {
		e := app.NewError(kind, "callOutcomeUnknown", reason+" during call creation; the call may have been placed and may still be running. Check the call list before retrying, and reuse the same idempotency key")
		e.Data = data
		return nil, e
	}
	cause := app.NewError(kind, code, reason+" during call creation; requested call termination")
	if !rest.Terminal(rest.Status(r.call)) {
		_, err := w.stop(r.call, cause)
		cause = app.Classify(err)
	} else {
		cause.Data = r.call
	}
	data["call"] = cause.Data
	cause.Data = data
	return nil, cause
}

// waiter polls a REST call until it ends.
type waiter struct {
	a        *app.App
	c        *rest.Client
	interval time.Duration
	timeout  time.Duration
	index    *int // batch task index for cli.status lines
}

// wait reports each status change and returns the final call. A call that
// ends other than completed is a callEnded error carrying the call. On ctx
// cancellation (Ctrl-C) or --timeout it asks the service to end the call.
func (w waiter) wait(ctx context.Context, call rest.Object) (rest.Object, error) {
	id := rest.ID(call)
	w.report(call)
	if err := w.a.OutputError(); err != nil {
		return w.outputFailure(call, err)
	}
	pollCtx := ctx
	if w.timeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, w.timeout)
		defer cancel()
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	failures := 0
	for !rest.Terminal(rest.Status(call)) {
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return w.stop(call, app.NewError(app.KindInterrupted, "interrupted", "interrupted; requested call termination"))
			}
			return w.stop(call, app.NewError(app.KindCallEnded, "timeout", "--timeout elapsed; requested call termination"))
		case <-ticker.C:
		}
		next, err := w.c.GetCall(pollCtx, id)
		if pollCtx.Err() != nil {
			continue // reported by the pollCtx.Done branch
		}
		if err != nil {
			e := app.Classify(err)
			if failures++; failures < maxPollFailures && transient(e) {
				continue
			}
			unknown := app.NewError(app.KindConnection, "callStatusUnknown", "call status could not be confirmed; check with tello call get before retrying")
			unknown.Data = call
			return nil, unknown
		}
		failures = 0
		if rest.Status(next) != rest.Status(call) {
			w.report(next)
		}
		call = next
		if err := w.a.OutputError(); err != nil {
			return w.outputFailure(call, err)
		}
	}
	return call, outcome(call)
}

func (w waiter) outputFailure(call rest.Object, err error) (rest.Object, error) {
	w.a.Warnf("output failed for call %s; check its status with `tello call get %s`: %v", rest.ID(call), rest.ID(call), err)
	cause := app.NewError(app.KindInternal, "outputFailed", "could not write call status")
	cause.Data = call
	if !rest.Terminal(rest.Status(call)) {
		return w.stop(call, cause)
	}
	return nil, cause
}

func transient(e *app.Error) bool {
	return e.Kind == app.KindConnection || e.Kind == app.KindTemporary || e.Kind == app.KindServer
}

// stop asks the service to end the call, polls until it settles (at most
// endGrace) and returns cause carrying the last known call.
func (w waiter) stop(call rest.Object, cause *app.Error) (rest.Object, error) {
	ctx, cancel := context.WithTimeout(context.Background(), endGrace)
	defer cancel()
	id := rest.ID(call)
	if _, err := w.c.EndCall(ctx, id); err != nil {
		if e := app.Classify(err); e.Code != "callNotActive" {
			w.a.Warnf("could not end call %s: %s [%s]; check it with `tello call get %s`", id, e.Message, e.Code, id)
		}
	}
	for !rest.Terminal(rest.Status(call)) {
		if ctx.Err() != nil {
			break
		}
		if next, err := w.c.GetCall(ctx, id); err == nil {
			if rest.Status(next) != rest.Status(call) {
				w.report(next)
			}
			call = next
		}
		if rest.Terminal(rest.Status(call)) {
			break
		}
		timer := time.NewTimer(w.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	cause.Data = call
	return nil, cause
}

func (w waiter) report(call rest.Object) {
	if !w.a.JSON {
		w.a.Printf("%s: %s\n", rest.ID(call), rest.Status(call))
		return
	}
	line := map[string]any{"type": "cli.status", "callId": rest.ID(call), "status": rest.Status(call)}
	if w.index != nil {
		line["index"] = *w.index
	}
	_ = w.a.WriteLine(line)
}

// outcome maps a final status to the result: only completed succeeds.
func outcome(call rest.Object) error {
	var e *app.Error
	switch rest.Status(call) {
	case "completed":
		return nil
	case "noAnswer":
		e = app.NewError(app.KindCallEnded, "noAnswer", "the callee did not answer")
	case "cancelled":
		e = app.NewError(app.KindCallEnded, "cancelled", "the call was cancelled")
	default:
		e = app.NewError(app.KindCallEnded, "callFailed", "the call failed")
	}
	if reason, _ := call["endReason"].(string); reason != "" {
		e.Message += " (" + reason + ")"
	}
	e.Data = call
	return e
}

// result is the batch view of a finished REST call.
func result(call rest.Object) callrun.Result {
	return callrun.Result{CallID: rest.ID(call), Status: rest.Status(call)}
}
