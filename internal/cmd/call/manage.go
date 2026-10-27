package call

import (
	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

func oneCallID(args []string) (string, error) {
	if len(args) != 1 || args[0] == "" {
		return "", app.Usagef("expected exactly one <callId>")
	}
	return args[0], nil
}

func newGet(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <callId>",
		Short: "Show a call: status, timing, summary, transcript and credit",
		Long: `Show a call of this account, live or ended, by its c_<digits> id.
summary, transcript and creditCharged may still be null right after a call
ends; use status to tell whether it is over.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneCallID(args)
			if err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			call, err := c.GetCall(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !a.JSON {
				printCall(a, call)
			}
			return a.WriteResult(call)
		},
	}
}

func newEnd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "end <callId>",
		Short: "Ask the service to end a live call",
		Long: `Ask the service to end a live call (source api, a2a or sdk). The result is
the call when the request was accepted; check the outcome with tello call get.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneCallID(args)
			if err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			call, err := c.EndCall(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !a.JSON {
				a.Printf("End requested for %s (status %s). Check it with `tello call get %s`.\n", id, rest.Status(call), id)
			}
			return a.WriteResult(call)
		},
	}
}

func newList(a *app.App) *cobra.Command {
	var q rest.ListCalls
	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List calls of this account, newest first",
		Long: `List calls of this account, newest first, one page at a time. Pass the
printed next cursor as --cursor, keeping the other filters, for the next page.
--created-after/--created-before take a date (UTC midnight) or a time with a
zone, e.g. 2026-10-20T09:00:00+09:00.`,
		Args: app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if q.Limit < 0 {
				return app.Usagef("--limit must not be negative")
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			page, err := c.ListCalls(cmd.Context(), q)
			if err != nil {
				return err
			}
			if !a.JSON {
				printPage(a, page)
			}
			return a.WriteResult(page)
		},
	}
	f := cmd.Flags()
	f.StringVar(&q.Status, "status", "", "dialing, inProgress, transferring, completed, noAnswer, failed or cancelled")
	f.StringVar(&q.AgentID, "agent", "", "only calls of this agent id")
	f.StringVar(&q.CrewID, "crew", "", "only calls of this crew id")
	f.StringVar(&q.CreatedAfter, "created-after", "", "only calls created at or after this time")
	f.StringVar(&q.CreatedBefore, "created-before", "", "only calls created before this time")
	f.IntVar(&q.Limit, "limit", 0, "page size, 1 to 100 (default 20)")
	f.StringVar(&q.Cursor, "cursor", "", "next cursor printed by the previous page")
	return cmd
}

func printPage(a *app.App, page rest.Object) {
	calls, _ := page["data"].([]any)
	if len(calls) == 0 {
		a.Printf("No calls.\n")
	}
	for _, item := range calls {
		call, _ := item.(map[string]any)
		target := "agent " + show(call["agentId"])
		if call["crewId"] != nil {
			target = "crew " + show(call["crewId"])
		}
		a.Printf("%-14s %-12s %-26s %-8s %s\n",
			show(call["id"]), show(call["status"]), show(call["createdAt"]), show(call["source"]), target)
	}
	if next, _ := page["nextCursor"].(string); next != "" {
		a.Printf("next cursor: %s\n", next)
	}
}
