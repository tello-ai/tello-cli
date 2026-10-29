package call

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/callrun"
	"github.com/tello-ai/tello-cli/internal/rest"
)

func newCreate(a *app.App) *cobra.Command {
	var (
		to             string
		prompt         string
		metadataArg    string
		timeout        time.Duration
		interactive    bool
		vars           []string
		wait           bool
		idempotencyKey string
		tgt            target
	)
	cmd := &cobra.Command{
		Use:   "create --to <number> (--agent <id> | --crew <id> | -i | -- <handler command>...) [flags]",
		Short: "Place one call",
		Long: `Place one call. Who answers the callee decides how it runs:

--agent / --crew   A portal agent or crew answers (REST API). The command
                   returns once the call is accepted; --wait follows it to the
                   end. --var passes start variables. Reuse the same
                   --idempotency-key on retries; check unknown outcomes first.
-i                 You answer from the terminal (WebSocket gateway). Blocks
                   until the call ends.
-- <command>...    A handler program answers: it reads gateway frames as NDJSON
                   on stdin and prints answer, sendDtmf or cancel commands on
                   stdout. Blocks until the call ends.`,
		Example: `  tello call create --to +821012345678 --agent 12 --var 고객명=홍길동 --wait
  tello call create --to +821012345678 --prompt "Confirm the booking" -i
  tello call create --to +821012345678 --metadata @meta.json -- python3 bot.py`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return app.Usagef("--to is required")
			}
			if timeout < 0 {
				return app.Usagef("--timeout must not be negative")
			}
			if tgt.set() {
				argv, err := targetArgv(cmd, args)
				if err != nil {
					return err
				}
				if err := tgt.validate(argv, interactive); err != nil {
					return err
				}
				switch {
				case prompt != "" || metadataArg != "":
					return app.Usagef("--prompt and --metadata need -i or a handler; --agent/--crew calls take --var")
				case timeout > 0 && !wait:
					return app.Usagef("--timeout needs --wait: without it the command returns while the call runs")
				}
				if err := checkIdempotencyKey(cmd, idempotencyKey); err != nil {
					return err
				}
				variables, err := parseVars(vars)
				if err != nil {
					return err
				}
				return createREST(cmd, a, tgt, tgt.request(to, variables), idempotencyKey, wait, timeout)
			}

			if len(vars) > 0 || wait || idempotencyKey != "" {
				return app.Usagef("--var, --wait and --idempotency-key need --agent or --crew")
			}
			argv, err := handlerArgv(cmd, args, interactive)
			if err != nil {
				return err
			}
			metadata, err := parseMetadata(metadataArg)
			if err != nil {
				return err
			}
			opts := callrun.Options{To: to, Prompt: prompt, Metadata: metadata, Handler: argv, Timeout: timeout}
			if interactive {
				opts.Input = callrun.NewInput(a.Stdin)
			}
			res, err := callrun.Run(cmd.Context(), a, opts)
			if err != nil {
				return err
			}
			return a.WriteResult(map[string]any{"callId": res.CallID, "status": res.Status})
		},
	}
	f := cmd.Flags()
	f.StringVar(&to, "to", "", "phone number to call (E.164, e.g. +821012345678)")
	tgt.bind(f)
	f.StringArrayVar(&vars, "var", nil, "start variable name=value for --agent/--crew (repeatable)")
	f.BoolVar(&wait, "wait", false, "with --agent/--crew: follow the call until it ends")
	f.StringVar(&idempotencyKey, "idempotency-key", "", "with --agent/--crew: key that makes retries safe (default: random)")
	f.StringVar(&prompt, "prompt", "", "with -i or a handler: instructions for the agent on this call")
	f.StringVar(&metadataArg, "metadata", "", "with -i or a handler: JSON object, or @file holding one, attached to the call")
	f.DurationVar(&timeout, "timeout", 0, "end the call after this long (0 = no limit); --agent/--crew need --wait")
	f.BoolVarP(&interactive, "interactive", "i", false, "answer from the terminal")
	return cmd
}

// targetArgv returns the handler command after --, if any, without the
// answer-source checks of handlerArgv: with --agent/--crew there must be none.
func targetArgv(cmd *cobra.Command, args []string) ([]string, error) {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		return args[dash:], nil
	}
	if len(args) > 0 {
		return nil, app.Usagef("unexpected arguments %q", args)
	}
	return nil, nil
}

func createREST(cmd *cobra.Command, a *app.App, tgt target, req rest.CreateCall, key string, wait bool, timeout time.Duration) error {
	c, err := rest.New(a)
	if err != nil {
		return err
	}
	if key == "" {
		if key, err = randomID(); err != nil {
			return err
		}
	}
	started := time.Now()
	w := waiter{a: a, c: c, interval: tgt.pollInterval, timeout: timeout}
	createCtx := cmd.Context()
	if wait && timeout > 0 {
		var cancel context.CancelFunc
		createCtx, cancel = context.WithTimeout(createCtx, timeout)
		defer cancel()
	}
	call, err := createCall(createCtx, c, req, key, w)
	if err != nil {
		return err
	}
	if !wait {
		if !a.JSON {
			a.Printf("Call %s placed (status %s). Follow it with `tello call get %s`.\n", rest.ID(call), rest.Status(call), rest.ID(call))
		}
		if err := a.WriteResult(call); err != nil {
			a.Warnf("output failed for call %s; check it with `tello call get %s`: %v", rest.ID(call), rest.ID(call), err)
			return err
		}
		return nil
	}
	if timeout > 0 {
		w.timeout = timeout - time.Since(started)
		if w.timeout <= 0 {
			_, err := w.stop(call, app.NewError(app.KindCallEnded, "timeout", "--timeout elapsed; requested call termination"))
			return err
		}
	}
	final, err := w.wait(cmd.Context(), call)
	if err != nil {
		return err
	}
	if !a.JSON {
		a.Printf("\n")
		printCall(a, final)
	}
	if err := a.WriteResult(final); err != nil {
		a.Warnf("output failed for call %s; check it with `tello call get %s`: %v", rest.ID(final), rest.ID(final), err)
		return err
	}
	return nil
}
