// Package target implements `tello agent`, `tello crew` and `tello tool`:
// the portal agents and crews a call can be placed with (--agent / --crew)
// and the tools they use, listed and managed through the REST API
// (docs/design.md sections 4 and 12).
package target

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// NewAgent returns the `agent` command.
func NewAgent(a *app.App) *cobra.Command {
	k := kind{
		res: rest.Agents, noun: "agent", an: "an agent", exampleID: "12",
		updateNote: fmt.Sprintf(webhookNote, "agent"),
		updateExample: `  tello agent update 12 --body '{"toolIds":["41","7"]}'
  tello agent update 12 --body '{"webhookUrl":"https://example.com/tello/webhook"}'`,
		deleteNote: `An agent that is still the account's default target or is linked to a
channel or an inbound number is refused with agentInUse (data.reason says
which); change that link first.`,
	}
	return group(a, k,
		"List the deployed agents for calls, and manage agents",
		"List the deployed agents of this account; use an id with call create --agent",
		func(ctx context.Context, c *rest.Client) (rest.Object, error) { return c.ListAgents(ctx) })
}

// NewCrew returns the `crew` command.
func NewCrew(a *app.App) *cobra.Command {
	k := kind{
		res: rest.Crews, noun: "crew", an: "a crew", exampleID: "34",
		updateNote: fmt.Sprintf(webhookNote, "crew") + `

Crew calls send their events to the crew's webhookUrl, never to those of its
member agents.`,
		updateExample: `  tello crew update 34 --body '{"webhookUrl":"https://example.com/tello/webhook"}'
  tello crew update 34 --body '{"webhookUrl":null}'`,
		deleteNote: `A crew that is still the account's default target or is linked to an
inbound number is refused with crewInUse (data.reason says which); change
that link first.`,
	}
	cmd := group(a, k,
		"List the crews ready for calls, and manage crews",
		"List the crews with an active production version; use an id with call create --crew",
		func(ctx context.Context, c *rest.Client) (rest.Object, error) { return c.ListCrews(ctx) })
	cmd.AddCommand(newGenerate(a), newGeneration(a))
	return cmd
}

func group(a *app.App, k kind, short, listShort string, list func(context.Context, *rest.Client) (rest.Object, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   k.noun,
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE:  a.GroupRunE,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: listShort,
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := rest.New(a)
			if err != nil {
				return err
			}
			page, err := list(cmd.Context(), c)
			if err != nil {
				return err
			}
			if !a.JSON {
				printTargets(a, page)
			}
			return a.WriteResult(page)
		},
	})
	cmd.AddCommand(manageCommands(a, k)...)
	return cmd
}

func printTargets(a *app.App, page rest.Object) {
	items, _ := page["data"].([]any)
	if len(items) == 0 {
		a.Printf("None.\n")
	}
	for _, item := range items {
		t, _ := item.(map[string]any)
		desc, _ := t["description"].(string)
		a.Printf("%-8v %v", t["id"], t["name"])
		if desc != "" {
			a.Printf("  (%s)", desc)
		}
		a.Printf("\n")
	}
}
