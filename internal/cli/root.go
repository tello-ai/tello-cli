// Package cli assembles the `tello` command tree.
package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/auth"
	"github.com/tello-ai/tello-cli/internal/cmd/call"
	"github.com/tello-ai/tello-cli/internal/cmd/doctor"
	guidecmd "github.com/tello-ai/tello-cli/internal/cmd/guide"
	"github.com/tello-ai/tello-cli/internal/cmd/initcmd"
	versioncmd "github.com/tello-ai/tello-cli/internal/cmd/version"
	"github.com/tello-ai/tello-cli/internal/version"
)

// NewRootCmd builds the command tree. Global flags write into a.
func NewRootCmd(a *app.App) *cobra.Command {
	root := &cobra.Command{
		Use:   "tello",
		Short: "Place and answer Tello phone calls from the terminal",
		Long: `tello places phone calls through the Tello gateway and lets you, a program,
or a coding agent answer each turn in real time.

Every command supports --json: stdout becomes one JSON object per line and the
last line is always {"type":"cli.result",...}. Exit codes are listed by
` + "`tello guide coding-agent`" + `.`,
		Version:       version.Version,
		Args:          cobra.ArbitraryArgs,
		RunE:          a.GroupRunE,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	// The generated completion command does not follow the --json contract.
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return app.Usagef("%v", err)
	})
	flags := root.PersistentFlags()
	flags.BoolVar(&a.JSON, "json", false, `write NDJSON to stdout; the last line is {"type":"cli.result",...}`)
	flags.StringVar(&a.URL, "url", "", "gateway URL (default $TELLO_URL, then wss://api.telloai.io/sdk)")

	root.AddCommand(
		auth.New(a),
		call.New(a),
		doctor.New(a),
		guidecmd.New(a),
		initcmd.New(a),
		versioncmd.New(a),
	)
	return root
}

// Main runs the CLI and returns the process exit code. Cancelling ctx is
// the first Ctrl-C: running calls are cancelled gracefully.
func Main(ctx context.Context, a *app.App, args []string) int {
	root := NewRootCmd(a)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		return a.ReportError(err)
	}
	return 0
}
