package call

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/callrun"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// task is one entry of the --tasks array. prompt and metadata go with -i or
// a handler, variables with --agent/--crew.
type task struct {
	To        string         `json:"to"`
	Prompt    string         `json:"prompt"`
	Metadata  map[string]any `json:"metadata"`
	Variables map[string]any `json:"variables"`
}

// abortCodes are failures that hold for the whole account or the batch's
// --agent/--crew, so the remaining tasks would fail the same way.
// callOutcomeUnknown stops too: a call may have been placed, and a human
// should look before more are.
var abortCodes = map[string]bool{
	"insufficientCredit":       true,
	"noRepresentativeNumber":   true,
	"callProviderUnauthorized": true,
	"outboundLimitExceeded":    true,
	"callerNumberUnavailable":  true,
	"agentNotFound":            true,
	"crewNotFound":             true,
	"agentInactive":            true,
	"crewInactive":             true,
	"publicationNotFound":      true,
	"publicationRevoked":       true,
	"callOutcomeUnknown":       true,
}

func aborts(e *app.Error) bool {
	return e.Kind == app.KindAuth || e.Kind == app.KindInterrupted || abortCodes[e.Code]
}

type batchOptions struct {
	tasksArg    string
	maxRetries  int
	retryDelay  time.Duration
	timeout     time.Duration
	interactive bool
	target      target
}

func newBatch(a *app.App) *cobra.Command {
	o := batchOptions{maxRetries: 3, retryDelay: 5 * time.Second}
	cmd := &cobra.Command{
		Use:   "batch --tasks <file|-> (--agent <id> | --crew <id> | -i | -- <handler command>...) [flags]",
		Short: "Place several calls one after another",
		Long: `Place several calls one after another; each call ends before the next
starts.

--agent / --crew   A portal agent or crew answers every call (REST API).
                   Tasks: [{"to":"+8210…","variables":{…}}]. Each task has its
                   own idempotency key, reused on retries.
-i / -- <command>  You or a handler answer (WebSocket gateway), each call on a
                   new connection with a new handler process.
                   Tasks: [{"to":"+8210…","prompt":"…","metadata":{…}}].

--tasks is the JSON array file, or - for stdin. Temporary refusals are
retried; account-wide refusals stop the batch.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				argv []string
				err  error
			)
			if o.target.set() {
				if argv, err = targetArgv(cmd, args); err == nil {
					err = o.target.validate(argv, o.interactive)
				}
			} else {
				argv, err = handlerArgv(cmd, args, o.interactive)
			}
			if err != nil {
				return err
			}
			switch {
			case o.tasksArg == "":
				return app.Usagef("--tasks is required")
			case o.tasksArg == "-" && o.interactive:
				return app.Usagef("--tasks - reads stdin, which --interactive also needs; use a tasks file")
			case o.maxRetries < 0:
				return app.Usagef("--max-retries must not be negative")
			case o.retryDelay < 0:
				return app.Usagef("--retry-delay must not be negative")
			case o.timeout < 0:
				return app.Usagef("--timeout must not be negative")
			}
			tasks, err := loadTasks(a, o.tasksArg, o.target.set())
			if err != nil {
				return err
			}
			b := &batch{a: a, o: o, tasks: tasks}
			if o.target.set() {
				if b.place, err = restPlacer(a, o); err != nil {
					return err
				}
			} else {
				var input *callrun.Input
				if o.interactive {
					input = callrun.NewInput(a.Stdin)
				}
				b.place = func(ctx context.Context, index int, t task) (callrun.Result, error) {
					return callrun.Run(ctx, a, callrun.Options{
						To: t.To, Prompt: t.Prompt, Metadata: t.Metadata,
						Input: input, Handler: argv, Timeout: o.timeout, Index: &index,
					})
				}
			}
			return b.run(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.tasksArg, "tasks", "", "JSON array of tasks: a file path, or - for stdin")
	o.target.bind(f)
	f.IntVar(&o.maxRetries, "max-retries", o.maxRetries, "retries per task for temporary refusals")
	f.DurationVar(&o.retryDelay, "retry-delay", o.retryDelay, "first retry delay, doubled on each retry")
	f.DurationVar(&o.timeout, "timeout", 0, "end each call after this long (0 = no limit)")
	f.BoolVarP(&o.interactive, "interactive", "i", false, "answer from the terminal")
	return cmd
}

// restPlacer places one task through the REST API and follows it to the
// end. The task's idempotency key is fixed per batch run, so a retry after a
// lost response replays the first call instead of dialing again.
func restPlacer(a *app.App, o batchOptions) (func(context.Context, int, task) (callrun.Result, error), error) {
	c, err := rest.New(a)
	if err != nil {
		return nil, err
	}
	runID, err := randomID()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, index int, t task) (callrun.Result, error) {
		key := fmt.Sprintf("%s-%d", runID, index)
		started := time.Now()
		createCtx := ctx
		if o.timeout > 0 {
			var cancel context.CancelFunc
			createCtx, cancel = context.WithTimeout(ctx, o.timeout)
			defer cancel()
		}
		w := waiter{a: a, c: c, interval: o.target.pollInterval, timeout: o.timeout, index: &index}
		call, err := createCall(createCtx, c, o.target.request(t.To, t.Variables), key, w)
		if err != nil {
			if data, ok := app.Classify(err).Data.(map[string]any); ok {
				if last, ok := data["call"].(rest.Object); ok {
					return result(last), err
				}
			}
			return callrun.Result{}, err
		}
		if o.timeout > 0 {
			w.timeout = o.timeout - time.Since(started)
			if w.timeout <= 0 {
				_, err := w.stop(call, app.NewError(app.KindCallEnded, "timeout", "--timeout elapsed; requested call termination"))
				if last, ok := app.Classify(err).Data.(rest.Object); ok {
					return result(last), err
				}
				return result(call), err
			}
		}
		final, err := w.wait(ctx, call)
		if err != nil {
			e := app.Classify(err)
			if last, ok := e.Data.(rest.Object); ok {
				return result(last), err
			}
			return result(call), err
		}
		return result(final), nil
	}, nil
}

// loadTasks reads and checks the --tasks array; rest says whether the
// tasks run through --agent/--crew, which take variables instead of prompt
// and metadata.
func loadTasks(a *app.App, arg string, rest bool) ([]task, error) {
	var (
		raw []byte
		err error
	)
	if arg == "-" {
		raw, err = io.ReadAll(a.Stdin)
	} else {
		raw, err = os.ReadFile(arg)
	}
	if err != nil {
		return nil, app.Usagef("--tasks: %v", err)
	}
	var tasks []task
	if err := decodeJSON(raw, &tasks); err != nil {
		return nil, app.Usagef("--tasks must be a JSON array of task objects: %v", err)
	}
	if len(tasks) == 0 {
		return nil, app.Usagef("--tasks has no tasks")
	}
	for i, t := range tasks {
		switch {
		case t.To == "":
			return nil, app.Usagef("--tasks: task %d has no \"to\"", i)
		case rest && (t.Prompt != "" || t.Metadata != nil):
			return nil, app.Usagef("--tasks: task %d has prompt or metadata, which --agent/--crew calls do not take; use variables", i)
		case !rest && t.Variables != nil:
			return nil, app.Usagef("--tasks: task %d has variables, which need --agent or --crew", i)
		}
	}
	return tasks, nil
}

type batch struct {
	a     *app.App
	o     batchOptions
	tasks []task
	// place runs one task to its end.
	place func(ctx context.Context, index int, t task) (callrun.Result, error)

	completed, failed int
}

func (b *batch) run(ctx context.Context) error {
	for i, t := range b.tasks {
		if ctx.Err() != nil {
			// Interrupted between tasks: this and the remaining tasks never ran.
			return b.abort(app.NewError(app.KindInterrupted, "interrupted", "interrupted"), len(b.tasks)-i)
		}
		res, attempts, err := b.runTask(ctx, i, t)
		b.report(i, t, res, attempts, err)
		if outputErr := b.a.OutputError(); outputErr != nil {
			b.a.Warnf("batch output failed after task %d (call %s, status %s); remaining tasks were not started: %v", i, res.CallID, res.Status, outputErr)
			if err != nil {
				return err
			}
			return app.NewError(app.KindInternal, "outputFailed", "could not write batch results")
		}
		if err == nil {
			b.completed++
			continue
		}
		b.failed++
		if cause := app.Classify(err); aborts(cause) || b.uncertain(res, cause) {
			return b.abort(cause, len(b.tasks)-i-1)
		}
	}
	b.printTotals(0)
	if b.failed > 0 {
		e := app.NewError(app.KindCallEnded, "batchIncomplete", fmt.Sprintf("%d of %d calls did not complete", b.failed, len(b.tasks)))
		e.Data = b.counts(0)
		return e
	}
	return b.a.WriteResult(b.counts(0))
}

// abort ends the batch with cause, skipping the remaining tasks.
func (b *batch) abort(cause *app.Error, skipped int) error {
	b.printTotals(skipped)
	e := *cause
	data := b.counts(skipped)
	if details, ok := cause.Data.(map[string]any); ok {
		for key, value := range details {
			data[key] = value
		}
	}
	e.Data = data
	return &e
}

// runTask runs one task, retrying temporary failures with doubling delays.
func (b *batch) runTask(ctx context.Context, index int, t task) (callrun.Result, int, error) {
	delay := b.o.retryDelay
	for attempt := 1; ; attempt++ {
		res, err := b.place(ctx, index, t)
		if err == nil || attempt > b.o.maxRetries {
			return res, attempt, err
		}
		cause := app.Classify(err)
		if cause.Kind != app.KindTemporary || (b.o.target.set() && (res.CallID != "" || !safeCreateRetry(cause.Code))) {
			return res, attempt, err
		}
		b.reportRetry(index, attempt+1, cause, delay)
		if b.a.OutputError() != nil {
			return res, attempt, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return res, attempt, app.NewError(app.KindInterrupted, "interrupted", "interrupted while waiting to retry")
		}
		delay *= 2
	}
}

// Stop before another task if the previous REST call may still be active.
func (b *batch) uncertain(res callrun.Result, cause *app.Error) bool {
	if !b.o.target.set() {
		return false
	}
	if res.CallID != "" {
		return !rest.Terminal(res.Status)
	}
	return cause.Kind == app.KindConnection || cause.Kind == app.KindServer ||
		(cause.Kind == app.KindTemporary && !safeCreateRetry(cause.Code))
}

// Only documented pre-dial refusals are safe to retry automatically.
func safeCreateRetry(code string) bool {
	switch code {
	case "concurrentLimitExceeded", "duplicateCall", "creditCheckUnavailable", "callRecordUnavailable", "voiceGatewayUnavailable":
		return true
	default:
		return false
	}
}

func (b *batch) counts(skipped int) map[string]any {
	return map[string]any{"total": len(b.tasks), "completed": b.completed, "failed": b.failed, "skipped": skipped}
}

func (b *batch) reportRetry(index, nextAttempt int, cause *app.Error, delay time.Duration) {
	if b.a.JSON {
		_ = b.a.WriteLine(map[string]any{
			"type": "cli.retry", "index": index, "attempt": nextAttempt,
			"code": cause.Code, "delayMs": delay.Milliseconds(),
		})
		return
	}
	b.a.Warnf("task %d: %s [%s]; retrying in %s (attempt %d of %d)",
		index, cause.Message, cause.Code, delay, nextAttempt, b.o.maxRetries+1)
}

func (b *batch) report(index int, t task, res callrun.Result, attempts int, err error) {
	if b.a.JSON {
		line := map[string]any{"type": "cli.task", "index": index, "to": t.To, "ok": err == nil, "attempts": attempts}
		if res.CallID != "" {
			line["callId"] = res.CallID
		}
		if res.Status != "" {
			line["status"] = res.Status
		}
		if err != nil {
			line["error"] = app.ErrorObject(app.Classify(err))
			if data := app.Classify(err).Data; data != nil {
				line["data"] = data
			}
		}
		_ = b.a.WriteLine(line)
		return
	}
	if err == nil {
		b.a.Printf("task %d %s: completed (call %s)\n", index, t.To, res.CallID)
		return
	}
	e := app.Classify(err)
	b.a.Printf("task %d %s: failed [%s] %s\n", index, t.To, e.Code, e.Message)
}

func (b *batch) printTotals(skipped int) {
	if b.a.JSON {
		return
	}
	b.a.Printf("batch: %d total, %d completed, %d failed, %d skipped\n",
		len(b.tasks), b.completed, b.failed, skipped)
}
