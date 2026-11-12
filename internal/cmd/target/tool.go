package target

import (
	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// NewTool returns the `tool` command: tools agents use during calls.
func NewTool(a *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool",
		Short: "Manage the tools agents use during calls",
		Args:  cobra.ArbitraryArgs,
		RunE:  a.GroupRunE,
	}
	cmd.AddCommand(newToolList(a))
	cmd.AddCommand(manageCommands(a, kind{res: rest.Tools, noun: "tool"})...)
	return cmd
}

func newToolList(a *app.App) *cobra.Command {
	var q rest.ListTools
	cmd := &cobra.Command{
		Use:   "list [--limit n] [--cursor c]",
		Short: "List the tools of this account by id, one page at a time",
		Long: `List the tools of this account by ascending id, one page at a time. Pass the
printed next cursor as --cursor for the next page.`,
		Args: app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if q.Limit < 0 {
				return app.Usagef("--limit must not be negative")
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			page, err := c.ListTools(cmd.Context(), q)
			if err != nil {
				return err
			}
			if !a.JSON {
				printTools(a, page)
			}
			return a.WriteResult(page)
		},
	}
	f := cmd.Flags()
	f.IntVar(&q.Limit, "limit", 0, "page size, 1 to 100 (default 50)")
	f.StringVar(&q.Cursor, "cursor", "", "next cursor printed by the previous page")
	return cmd
}

func printTools(a *app.App, page rest.Object) {
	items, _ := page["data"].([]any)
	if len(items) == 0 {
		a.Printf("No tools.\n")
	}
	for _, item := range items {
		t, _ := item.(map[string]any)
		a.Printf("%-8v %-16v %v\n", t["id"], t["type"], t["name"])
	}
	if next, _ := page["nextCursor"].(string); next != "" {
		a.Printf("next cursor: %s\n", next)
	}
}
