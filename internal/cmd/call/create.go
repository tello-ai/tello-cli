package call

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/callrun"
)

func newCreate(a *app.App) *cobra.Command {
	var (
		to          string
		prompt      string
		metadataArg string
		timeout     time.Duration
		interactive bool
	)
	cmd := &cobra.Command{
		Use:   "create --to <number> [flags] (-i | -- <handler command>...)",
		Short: "Place one call and block until it ends",
		Long: `Place one call and block until it ends.

An answer source is required: --interactive answers from the terminal, or the
command after -- runs as a handler that reads gateway frames as NDJSON on stdin
and prints answer, sendDtmf or cancel commands on stdout.`,
		Example: `  tello call create --to +821012345678 --prompt "Confirm the booking" -i
  tello call create --to +821012345678 --metadata @meta.json -- python3 bot.py`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return app.Usagef("--to is required")
			}
			argv, err := handlerArgv(cmd, args, interactive)
			if err != nil {
				return err
			}
			metadata, err := parseMetadata(metadataArg)
			if err != nil {
				return err
			}
			if timeout < 0 {
				return app.Usagef("--timeout must not be negative")
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
	f.StringVar(&prompt, "prompt", "", "instructions for the agent on this call")
	f.StringVar(&metadataArg, "metadata", "", "JSON object, or @file holding one, attached to the call")
	f.DurationVar(&timeout, "timeout", 0, "cancel the call after this long (0 = no limit)")
	f.BoolVarP(&interactive, "interactive", "i", false, "answer from the terminal")
	return cmd
}
