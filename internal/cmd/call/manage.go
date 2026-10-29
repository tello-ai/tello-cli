package call

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// oneCallID returns the single <callId> argument. A call id is c_ and the
// call record number as the API prints it: no sign, no leading zeros, within
// int64. Anything else is refused before a request; the API would answer 404,
// or echo a normalized id that fails the response check after the request
// already took effect.
func oneCallID(args []string) (string, error) {
	if len(args) != 1 {
		return "", app.Usagef("expected exactly one <callId>")
	}
	digits, ok := strings.CutPrefix(args[0], "c_")
	if n, err := strconv.ParseInt(digits, 10, 64); !ok || err != nil || n <= 0 || strconv.FormatInt(n, 10) != digits {
		return "", app.Usagef("<callId> must be a call id as printed, like c_12345; got %q", args[0])
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

func newTransfer(a *app.App) *cobra.Command {
	var to, key string
	cmd := &cobra.Command{
		Use:   "transfer <callId> --to <number> [--idempotency-key <key>]",
		Short: "Ask the service to transfer a live call to a phone number",
		Long: `Ask the service to transfer a live call of this account to a phone number
(immediate transfer). Any answered call can be transferred, inbound calls
included. The result is the call when the request was accepted, not the
outcome: the call stays inProgress while the number is dialed and also when
the transfer fails. Once it succeeds the AI leaves and a phone call ends; a
browser call (web call) can stay inProgress until that conversation ends.
Check it with tello call get.

Accepted is not done: a request sent while the call is ending, while another
transfer is still running, or after a counselor took the call over is
accepted but has no effect. Before trying again, give the previous transfer
time to finish.

Each run uses a new idempotency key unless --idempotency-key is given. Reuse
a key only to resend a request whose response was lost; resending an accepted
request with its key only replays the acceptance, so a new attempt after a
failed transfer needs a new key.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneCallID(args)
			if err != nil {
				return err
			}
			if to == "" {
				return app.Usagef("--to is required")
			}
			if err := checkIdempotencyKey(cmd, key); err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			if key == "" {
				if key, err = randomID(); err != nil {
					return err
				}
			}
			call, err := c.TransferCall(cmd.Context(), id, to, key)
			if errors.Is(err, context.Canceled) {
				err = app.NewError(app.KindInterrupted, "interrupted", "interrupted; the transfer may have been requested, check the call before retrying with the same idempotency key")
			}
			if err != nil {
				return withIdempotencyKey(err, key)
			}
			if !a.JSON {
				a.Printf("Transfer of %s to %s requested (status %s). Check it with `tello call get %s`.\n", id, to, rest.Status(call), id)
			}
			if err := a.WriteResult(call); err != nil {
				a.Warnf("output failed after the transfer of %s was accepted (idempotency key %s); check it with `tello call get %s`: %v", id, key, id, err)
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&to, "to", "", "phone number to transfer the call to (e.g. +821012345678)")
	f.StringVar(&key, "idempotency-key", "", "key that makes resending the same request safe (default: random)")
	return cmd
}

// withIdempotencyKey classifies err and adds the key the request was sent
// with to its data, so the caller can resend the same request.
func withIdempotencyKey(err error, key string) *app.Error {
	e := app.Classify(err)
	data, _ := e.Data.(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	data["idempotencyKey"] = key
	e.Data = data
	return e
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
