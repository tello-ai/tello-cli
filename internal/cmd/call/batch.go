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
)

// task is one entry of the --tasks array.
type task struct {
	To       string         `json:"to"`
	Prompt   string         `json:"prompt"`
	Metadata map[string]any `json:"metadata"`
}

// abortCodes are failures that hold for the whole account, so the
// remaining tasks would fail the same way.
var abortCodes = map[string]bool{
	"insufficientCredit":       true,
	"noRepresentativeNumber":   true,
	"callProviderUnauthorized": true,
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
}

func newBatch(a *app.App) *cobra.Command {
	o := batchOptions{maxRetries: 3, retryDelay: 5 * time.Second}
	cmd := &cobra.Command{
		Use:   "batch --tasks <file|-> [flags] (-i | -- <handler command>...)",
		Short: "Place several calls one after another",
		Long: `Place several calls one after another, each on a new connection with a
new handler process.

--tasks is a JSON array: [{"to":"+8210…","prompt":"…","metadata":{…}}], or - for
stdin. Temporary refusals are retried; account-wide refusals stop the batch.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv, err := handlerArgv(cmd, args, o.interactive)
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
			tasks, err := loadTasks(a, o.tasksArg)
			if err != nil {
				return err
			}
			b := &batch{a: a, o: o, argv: argv, tasks: tasks}
			if o.interactive {
				b.input = callrun.NewInput(a.Stdin)
			}
			return b.run(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.tasksArg, "tasks", "", "JSON array of tasks: a file path, or - for stdin")
	f.IntVar(&o.maxRetries, "max-retries", o.maxRetries, "retries per task for temporary refusals")
	f.DurationVar(&o.retryDelay, "retry-delay", o.retryDelay, "first retry delay, doubled on each retry")
	f.DurationVar(&o.timeout, "timeout", 0, "cancel each call after this long (0 = no limit)")
	f.BoolVarP(&o.interactive, "interactive", "i", false, "answer from the terminal")
	return cmd
}

func loadTasks(a *app.App, arg string) ([]task, error) {
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
		return nil, app.Usagef("--tasks must be a JSON array of {\"to\", \"prompt\", \"metadata\"} objects: %v", err)
	}
	if len(tasks) == 0 {
		return nil, app.Usagef("--tasks has no tasks")
	}
	for i, t := range tasks {
		if t.To == "" {
			return nil, app.Usagef("--tasks: task %d has no \"to\"", i)
		}
	}
	return tasks, nil
}

type batch struct {
	a     *app.App
	o     batchOptions
	argv  []string
	input *callrun.Input
	tasks []task

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
		if err == nil {
			b.completed++
			continue
		}
		b.failed++
		if cause := app.Classify(err); aborts(cause) {
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
	e.Data = b.counts(skipped)
	return &e
}

// runTask runs one task, retrying temporary failures with doubling delays.
func (b *batch) runTask(ctx context.Context, index int, t task) (callrun.Result, int, error) {
	opts := callrun.Options{
		To: t.To, Prompt: t.Prompt, Metadata: t.Metadata,
		Input: b.input, Handler: b.argv, Timeout: b.o.timeout, Index: &index,
	}
	delay := b.o.retryDelay
	for attempt := 1; ; attempt++ {
		res, err := callrun.Run(ctx, b.a, opts)
		if err == nil || attempt > b.o.maxRetries {
			return res, attempt, err
		}
		cause := app.Classify(err)
		if cause.Kind != app.KindTemporary {
			return res, attempt, err
		}
		b.reportRetry(index, attempt+1, cause, delay)
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
