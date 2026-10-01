package app

import (
	"strings"

	"github.com/spf13/cobra"
)

// NoArgs rejects positional arguments with a usage error (exit 2); cobra's
// own validators return unclassified errors (exit 1).
func NoArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return Usagef("%s takes no arguments, got %q", cmd.CommandPath(), args[0])
	}
	return nil
}

// GroupRunE runs commands that only group subcommands (root, auth, call).
// Pair it with Args: cobra.ArbitraryArgs so unknown names reach it.
// Without arguments it prints help, except in --json mode, which promises a
// cli.result line and so fails with a usage error. Unknown subcommands are
// usage errors with suggestions.
func (a *App) GroupRunE(cmd *cobra.Command, args []string) error {
	var names []string
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			names = append(names, sub.Name())
		}
	}
	available := strings.Join(names, ", ")
	if len(args) > 0 {
		msg := "unknown command %q for %q (available: %s)"
		if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
			return Usagef(msg+"; did you mean %s?", args[0], cmd.CommandPath(), available, strings.Join(suggestions, " or "))
		}
		return Usagef(msg, args[0], cmd.CommandPath(), available)
	}
	if a.JSON {
		return Usagef("%s requires a subcommand (available: %s)", cmd.CommandPath(), available)
	}
	return cmd.Help()
}
