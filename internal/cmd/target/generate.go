package target

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// maxPollFailures is how many polls in a row may fail on a transient error
// before waiting gives up (the job itself keeps running).
const maxPollFailures = 3

func newGenerate(a *app.App) *cobra.Command {
	var (
		goal     string
		key      string
		wait     bool
		interval time.Duration
		timeout  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "generate --goal <text> [--wait] [--poll-interval 2s] [--timeout <dur>] [--idempotency-key <key>]",
		Short: "Have Tello build a crew from a goal and put it live",
		Long: `Have Tello design a crew for a business goal, then save, deploy and publish it.
This takes tens of seconds to minutes, so the result is a job: without --wait
the command returns it as accepted; follow it with tello crew generation <id>.
--wait polls the job until it ends and returns it, with the crew on success.

The account builds one crew at a time. Another goal while a job runs is
crewGenerationInProgress (data.id is that job). The same goal while it runs,
or within 10 minutes of its success, returns that job instead of building a
second crew; change the wording to build another one.

A failed job exits with its error.code (crewGenerationFailed,
crewGenerationTimeout, crewDeployFailed, crewPromoteFailed); when data.crew is
set the crew was left in place. --timeout and Ctrl-C stop waiting only: the
job keeps running.`,
		Example: `  tello crew generate --goal "치과 예약 고객에게 전화해 내일 예약을 확인한다" --wait`,
		Args:    app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case strings.TrimSpace(goal) == "":
				return app.Usagef("--goal is required")
			case timeout < 0:
				return app.Usagef("--timeout must not be negative")
			case timeout > 0 && !wait:
				return app.Usagef("--timeout needs --wait: without it the command returns while the job runs")
			case interval <= 0:
				return app.Usagef("--poll-interval must be positive")
			}
			if err := app.CheckIdempotencyKey(cmd, key); err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			if key == "" {
				if key, err = app.RandomID(); err != nil {
					return err
				}
			}
			job, err := c.GenerateCrew(cmd.Context(), goal, key)
			if err != nil {
				return app.WithIdempotencyKey(maybeApplied(err, "the crew generation may have started; the same goal returns the running job"), key)
			}
			if wait {
				if job, err = waitGeneration(cmd.Context(), a, c, job, interval, timeout); err != nil {
					return err
				}
			}
			if !a.JSON {
				if wait {
					a.Printf("\n")
				}
				printGeneration(a, job)
			}
			return a.WriteResult(job)
		},
	}
	f := cmd.Flags()
	f.StringVar(&goal, "goal", "", "the business goal, 1 to 2000 characters")
	f.BoolVar(&wait, "wait", false, "follow the job until it ends")
	f.DurationVar(&interval, "poll-interval", 2*time.Second, "how often to check the job while waiting")
	f.DurationVar(&timeout, "timeout", 0, "stop waiting after this long (0 = no limit); needs --wait")
	f.StringVar(&key, "idempotency-key", "", "key that makes resending the same request safe (default: random)")
	return cmd
}

func newGeneration(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "generation <generationId>",
		Short: "Show a crew generation job: status, crew or error",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneID(args, "generationId")
			if err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			job, err := c.GetCrewGeneration(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !a.JSON {
				printGeneration(a, job)
			}
			return a.WriteResult(job)
		},
	}
}

// waitGeneration polls the job until it is no longer running, reporting
// each status change. A failed job is an error carrying the job; so are
// --timeout, Ctrl-C and polls that keep failing, which leave it running.
func waitGeneration(ctx context.Context, a *app.App, c *rest.Client, job rest.Object, interval, timeout time.Duration) (rest.Object, error) {
	reportGeneration(a, job)
	pollCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	for rest.Status(job) == "running" {
		select {
		case <-pollCtx.Done():
			return nil, stoppedWaiting(ctx, job)
		case <-ticker.C:
		}
		next, err := c.GetCrewGeneration(pollCtx, rest.ID(job))
		if pollCtx.Err() != nil {
			return nil, stoppedWaiting(ctx, job)
		}
		if err != nil {
			e := app.Classify(err)
			if failures++; failures < maxPollFailures && transient(e) {
				continue
			}
			unknown := app.NewError(app.KindConnection, "crewGenerationStatusUnknown", "crew generation status could not be checked; the job may still be running, check it with tello crew generation "+rest.ID(job))
			unknown.Data = job
			return nil, unknown
		}
		failures = 0
		if rest.Status(next) != rest.Status(job) {
			reportGeneration(a, next)
		}
		job = next
	}
	return job, rest.GenerationOutcome(job)
}

func stoppedWaiting(ctx context.Context, job rest.Object) error {
	e := app.NewError(app.KindTemporary, "crewGenerationRunning", "--timeout elapsed; the crew generation keeps running, check it with tello crew generation "+rest.ID(job))
	if ctx.Err() != nil {
		e = app.NewError(app.KindInterrupted, "interrupted", "interrupted; the crew generation keeps running, check it with tello crew generation "+rest.ID(job))
	}
	e.Data = job
	return e
}

func transient(e *app.Error) bool {
	return e.Kind == app.KindConnection || e.Kind == app.KindTemporary || e.Kind == app.KindServer
}

func reportGeneration(a *app.App, job rest.Object) {
	if !a.JSON {
		a.Printf("%s: %s\n", rest.ID(job), rest.Status(job))
		return
	}
	_ = a.WriteLine(map[string]any{"type": "cli.status", "generationId": rest.ID(job), "status": rest.Status(job)})
}

func printGeneration(a *app.App, job rest.Object) {
	a.Printf("%-8s %s\n%-8s %s\n%-8s %v\n", "id:", rest.ID(job), "status:", rest.Status(job), "goal:", job["goal"])
	if crew, ok := job["crew"].(map[string]any); ok {
		a.Printf("%-8s %v (%v)\n", "crew:", crew["id"], crew["name"])
	}
	if jobErr, ok := job["error"].(map[string]any); ok {
		a.Printf("%-8s %v: %v\n", "error:", jobErr["code"], jobErr["message"])
	}
	if rest.Status(job) == "running" {
		a.Printf("Follow it with `tello crew generation %s`.\n", rest.ID(job))
	}
}
