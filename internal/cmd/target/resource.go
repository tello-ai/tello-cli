package target

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// kind names a managed collection in commands and messages.
type kind struct {
	res  rest.Resource
	noun string // "tool", "agent" or "crew"
}

// writeNote is shared by the help of every write: the API deploys and
// publishes in the same request.
const writeNote = `The change goes live at once: the next call uses it. There is no draft or
separate deploy step.`

// manageCommands returns get, create, update and delete for k.
func manageCommands(a *app.App, k kind) []*cobra.Command {
	return []*cobra.Command{newGet(a, k), newCreate(a, k), newUpdate(a, k), newDelete(a, k)}
}

func newGet(a *app.App, k kind) *cobra.Command {
	return &cobra.Command{
		Use:   "get <" + k.noun + "Id>",
		Short: "Show a " + k.noun + " with all its settings",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneID(args, k.noun+"Id")
			if err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			obj, err := c.GetResource(cmd.Context(), k.res, id)
			if err != nil {
				return err
			}
			if !a.JSON {
				printObject(a, obj)
			}
			return a.WriteResult(obj)
		},
	}
}

func newCreate(a *app.App, k kind) *cobra.Command {
	var bodyArg, key string
	cmd := &cobra.Command{
		Use:   "create --body <JSON|@file|-> [--idempotency-key <key>]",
		Short: "Create a " + k.noun + " and put it live",
		Long: "Create a " + k.noun + " from a JSON object (see the REST API docs for its fields).\n" + writeNote + `

Each run uses a new idempotency key unless --idempotency-key is given. If the
outcome is unknown (writeOutcomeUnknown, a lost response, Ctrl-C), check the
list before creating again; resend only with the reported key.`,
		Args: app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := requiredBody(a, cmd, bodyArg)
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
			obj, err := c.CreateResource(cmd.Context(), k.res, body, key)
			if err != nil {
				return app.WithIdempotencyKey(maybeApplied(err, "the "+k.noun+" may have been created; check the list before retrying with the same idempotency key"), key)
			}
			if !a.JSON {
				printObject(a, obj)
			}
			if err := a.WriteResult(obj); err != nil {
				a.Warnf("output failed after %s %v was created (idempotency key %s): %v", k.noun, obj["id"], key, err)
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&bodyArg, "body", "", "the "+k.noun+" as a JSON object, @file holding one, or - for stdin")
	f.StringVar(&key, "idempotency-key", "", "key that makes resending the same create safe (default: random)")
	return cmd
}

func newUpdate(a *app.App, k kind) *cobra.Command {
	var bodyArg string
	cmd := &cobra.Command{
		Use:   "update <" + k.noun + "Id> --body <JSON|@file|->",
		Short: "Change fields of a " + k.noun + " and put it live",
		Long:  "Change only the fields in the JSON object; lists and nested settings the\nAPI replaces whole are replaced whole.\n" + writeNote,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneID(args, k.noun+"Id")
			if err != nil {
				return err
			}
			body, err := requiredBody(a, cmd, bodyArg)
			if err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			obj, err := c.UpdateResource(cmd.Context(), k.res, id, body)
			if err != nil {
				return maybeApplied(err, "the "+k.noun+" may have been changed; check it with `tello "+k.noun+" get "+id+"`")
			}
			if !a.JSON {
				printObject(a, obj)
			}
			return a.WriteResult(obj)
		},
	}
	cmd.Flags().StringVar(&bodyArg, "body", "", "fields to change as a JSON object, @file holding one, or - for stdin")
	return cmd
}

func newDelete(a *app.App, k kind) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <" + k.noun + "Id>",
		Short: "Delete a " + k.noun,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneID(args, k.noun+"Id")
			if err != nil {
				return err
			}
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			if err := c.DeleteResource(cmd.Context(), k.res, id); err != nil {
				return maybeApplied(err, "the "+k.noun+" may have been deleted; check it with `tello "+k.noun+" get "+id+"`")
			}
			if !a.JSON {
				a.Printf("Deleted %s %s.\n", k.noun, id)
			}
			return a.WriteResult(map[string]any{"id": id, "deleted": true})
		},
	}
}

// oneID returns the single <name> argument: a resource id as the API prints
// it, a positive integer without sign or leading zeros. Anything else is
// refused before a request.
func oneID(args []string, name string) (string, error) {
	if len(args) != 1 {
		return "", app.Usagef("expected exactly one <%s>", name)
	}
	if n, err := strconv.ParseInt(args[0], 10, 64); err != nil || n <= 0 || strconv.FormatInt(n, 10) != args[0] {
		return "", app.Usagef("<%s> must be an id as printed, like 41; got %q", name, args[0])
	}
	return args[0], nil
}

func requiredBody(a *app.App, cmd *cobra.Command, arg string) (map[string]any, error) {
	if !cmd.Flags().Changed("body") {
		return nil, app.Usagef("--body is required")
	}
	return a.ObjectArg("--body", arg)
}

// maybeApplied reports Ctrl-C during a write as interrupted with what may
// have happened; the service keeps processing a request it received.
func maybeApplied(err error, what string) error {
	if errors.Is(err, context.Canceled) {
		return app.NewError(app.KindInterrupted, "interrupted", "interrupted; "+what)
	}
	return err
}

// printObject writes obj as indented JSON in human mode.
func printObject(a *app.App, obj rest.Object) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		a.Printf("%v\n", obj)
		return
	}
	a.Printf("%s", buf.String())
}
