// Package call implements `tello call create | get | list | end | transfer |
// batch | draft` (docs/design.md sections 8 to 11).
package call

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
)

// New returns the `call` command with its subcommands.
func New(a *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "call",
		Short: "Place calls, follow them and read their results",
		Args:  cobra.ArbitraryArgs,
		RunE:  a.GroupRunE,
	}
	cmd.AddCommand(newCreate(a), newGet(a), newList(a), newEnd(a), newTransfer(a), newBatch(a), newDraft(a))
	return cmd
}

// handlerArgv validates the answer source: exactly one of --interactive or
// a handler command after `--`. It returns the handler argv (nil for
// interactive).
func handlerArgv(cmd *cobra.Command, args []string, interactive bool) ([]string, error) {
	dash := cmd.ArgsLenAtDash()
	switch {
	case dash < 0 && len(args) > 0:
		return nil, app.Usagef("unexpected arguments %q; put the handler command after --", args)
	case dash > 0:
		return nil, app.Usagef("unexpected arguments before --: %q", args[:dash])
	case dash == 0 && len(args) == 0:
		return nil, app.Usagef("missing handler command after --")
	}
	var argv []string
	if dash == 0 {
		argv = args
	}
	if interactive && argv != nil {
		return nil, app.Usagef("use either --interactive or a handler command after --, not both")
	}
	if !interactive && argv == nil {
		return nil, app.Usagef("an answer source is required: --agent or --crew, --interactive, or a handler command after --")
	}
	return argv, nil
}

// parseMetadata reads --metadata: a JSON object, or @path of a file holding
// one. Numbers keep their exact text.
func parseMetadata(arg string) (map[string]any, error) {
	if arg == "" {
		return nil, nil
	}
	raw := []byte(arg)
	if path, ok := strings.CutPrefix(arg, "@"); ok {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, app.Usagef("--metadata: %v", err)
		}
		raw = data
	}
	var metadata map[string]any
	if err := app.DecodeJSON(raw, &metadata); err != nil || metadata == nil {
		return nil, app.Usagef("--metadata must be a JSON object")
	}
	return metadata, nil
}
