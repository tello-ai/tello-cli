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

func newDraft(a *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Review an agent's call scenario before placing the call",
		Long: `For agents that generate outbound scenarios: create a draft, show its
preview.summary to a person, revise it if needed, then confirm it to place
the call. Confirming places a real, billed call. Drafts expire 10 minutes
after they are created, and an agent with a ready scenario is called at once
by create (status confirmed, callId).`,
		Args: cobra.ArbitraryArgs,
		RunE: a.GroupRunE,
	}
	cmd.AddCommand(newDraftCreate(a), newDraftRevise(a), newDraftConfirm(a))
	return cmd
}

func newDraftCreate(a *app.App) *cobra.Command {
	var (
		agent, to, key string
		vars           []string
		pc             perCall
	)
	cmd := &cobra.Command{
		Use:   "create --agent <id> --to <number> [--instructions <goal>] [--var name=value]... [flags]",
		Short: "Create a call draft: the scenario to review before calling",
		Long: `Create a call draft for an agent that generates outbound scenarios. The
number, variables and settings are stored with the draft; confirm does not
take them again.

status pendingConfirmation: show preview.summary, then confirm or revise.
status needsClarification: answer question in --instructions and create again.
status refused: the AI cannot handle the request; change it.
status confirmed: the agent had a ready scenario and the call was placed at
once; follow callId with tello call get.

draftNotApplicable means the agent does not use drafts: place the call with
tello call create and the same values.`,
		Example: `  tello call draft create --agent 12 --to +821012345678 --instructions "내일 2시 예약을 확인해 주세요" --var 고객명=홍길동`,
		Args:    app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case agent == "":
				return app.Usagef("--agent is required")
			case to == "":
				return app.Usagef("--to is required")
			}
			variables, err := parseVars(vars)
			if err != nil {
				return err
			}
			settings, err := pc.settings(false)
			if err != nil {
				return err
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
			draft, err := c.CreateCallDraft(cmd.Context(), rest.CallDraft{AgentID: agent, To: to, Variables: variables, PerCall: settings}, key)
			if err != nil {
				return app.WithIdempotencyKey(placedMaybe(err, "if the agent had a ready scenario a call may have been placed; check the call list before retrying with the same idempotency key"), key)
			}
			return writeDraft(a, draft, key)
		},
	}
	f := cmd.Flags()
	f.StringVar(&agent, "agent", "", "agent id (tello agent list) that generates outbound scenarios")
	f.StringVar(&to, "to", "", "phone number to call (E.164, e.g. +821012345678)")
	f.StringArrayVar(&vars, "var", nil, "start variable name=value (repeatable)")
	pc.bind(f)
	f.StringVar(&key, "idempotency-key", "", "key that makes resending the same request safe (default: random)")
	return cmd
}

func newDraftRevise(a *app.App) *cobra.Command {
	var request, key string
	cmd := &cobra.Command{
		Use:   "revise <draftId> --request <text> [--idempotency-key <key>]",
		Short: "Ask for a changed scenario; returns a new draft",
		Long: `Ask for a changed scenario. A new draft with a new id comes back and the
revised one can no longer be confirmed (draftSuperseded). Drafts go up to the
5th revision (draftRevisionLimit). If the answer is needsClarification, the
draft is unchanged.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneDraftID(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(request) == "" {
				return app.Usagef("--request is required")
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
			draft, err := c.ReviseCallDraft(cmd.Context(), id, request, key)
			if err != nil {
				return app.WithIdempotencyKey(err, key)
			}
			return writeDraft(a, draft, key)
		},
	}
	f := cmd.Flags()
	f.StringVar(&request, "request", "", "what to change, 1 to 1000 characters")
	f.StringVar(&key, "idempotency-key", "", "key that makes resending the same request safe (default: random)")
	return cmd
}

func newDraftConfirm(a *app.App) *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "confirm <draftId> [--idempotency-key <key>]",
		Short: "Place the call of a draft (a real, billed call)",
		Long: `Place the call of a draft with the values stored in it. This dials a real,
billed call; confirm only what a person approved. A draft is confirmed once:
again is draftAlreadyConfirmed with data.callId. The result is the call when
accepted; follow it with tello call get.

Each run uses a new idempotency key unless --idempotency-key is given. If the
response is lost (connection error, Ctrl-C, callOutcomeUnknown), resend only
with the reported key: the same key replays the placed call.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneDraftID(args)
			if err != nil {
				return err
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
			call, err := c.ConfirmCallDraft(cmd.Context(), id, key)
			if err != nil {
				return app.WithIdempotencyKey(placedMaybe(err, "the call may have been placed; check the call list before retrying with the same idempotency key"), key)
			}
			if !a.JSON {
				a.Printf("Call %s placed (status %s). Follow it with `tello call get %s`.\n", rest.ID(call), rest.Status(call), rest.ID(call))
			}
			if err := a.WriteResult(call); err != nil {
				a.Warnf("output failed for call %s placed from %s (idempotency key %s); check it with `tello call get %s`: %v", rest.ID(call), id, key, rest.ID(call), err)
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "idempotency-key", "", "key that makes resending the same confirm safe (default: random)")
	return cmd
}

// oneDraftID returns the single <draftId> argument: cd_ and the draft
// number as the API prints it, like call ids.
func oneDraftID(args []string) (string, error) {
	if len(args) != 1 {
		return "", app.Usagef("expected exactly one <draftId>")
	}
	digits, ok := strings.CutPrefix(args[0], "cd_")
	if n, err := strconv.ParseInt(digits, 10, 64); !ok || err != nil || n <= 0 || strconv.FormatInt(n, 10) != digits {
		return "", app.Usagef("<draftId> must be a draft id as printed, like cd_481; got %q", args[0])
	}
	return args[0], nil
}

// placedMaybe reports Ctrl-C during a request that may dial as interrupted
// with what may have happened; the service keeps processing it.
func placedMaybe(err error, what string) error {
	if errors.Is(err, context.Canceled) {
		return app.NewError(app.KindInterrupted, "interrupted", "interrupted; "+what)
	}
	return err
}

func writeDraft(a *app.App, draft rest.Object, key string) error {
	if !a.JSON {
		printDraft(a, draft)
	}
	if err := a.WriteResult(draft); err != nil {
		if callID, _ := draft["callId"].(string); callID != "" {
			a.Warnf("output failed after call %s was placed (idempotency key %s); check it with `tello call get %s`: %v", callID, key, callID, err)
		}
		return err
	}
	return nil
}

func printDraft(a *app.App, draft rest.Object) {
	id, _ := draft["id"].(string)
	a.Printf("%-12s %s\n%-12s %s\n", "id:", show(draft["id"]), "status:", rest.Status(draft))
	if draft["revision"] != nil {
		a.Printf("%-12s %s (%s left)\n", "revision:", show(draft["revision"]), show(draft["revisionsLeft"]))
	}
	if draft["expiresAt"] != nil {
		a.Printf("%-12s %s\n", "expiresAt:", show(draft["expiresAt"]))
	}
	if preview, ok := draft["preview"].(map[string]any); ok && preview["summary"] != nil {
		a.Printf("\nsummary:\n%s\n", indent(show(preview["summary"])))
	}
	if q := draft["question"]; q != nil {
		a.Printf("\nquestion:\n%s\n", indent(show(q)))
	}
	if excluded, ok := draft["excludedContent"].([]any); ok && len(excluded) > 0 {
		a.Printf("\nexcluded:\n")
		for _, e := range excluded {
			a.Printf("%s\n", indent(show(e)))
		}
	}
	switch callID, _ := draft["callId"].(string); {
	case callID != "":
		a.Printf("\nCalled at once: call %s. Follow it with `tello call get %s`.\n", callID, callID)
	case rest.Status(draft) == "pendingConfirmation" && id != "":
		a.Printf("\nConfirm to place the call (billed): `tello call draft confirm %s`\nor change it: `tello call draft revise %s --request \"...\"`.\n", id, id)
	}
}
