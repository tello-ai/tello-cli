// Package target implements `tello agent`, `tello crew` and `tello tool`:
// the portal agents and crews a call can be placed with (--agent / --crew)
// and the tools they use, listed and managed through the REST API
// (docs/design.md sections 4 and 12).
package target

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// NewAgent returns the `agent` command.
func NewAgent(a *app.App) *cobra.Command {
	return group(a, kind{res: rest.Agents, noun: "agent"}, "deployed agents", "agent ids are for call create --agent",
		func(ctx context.Context, c *rest.Client) (rest.Object, error) { return c.ListAgents(ctx) })
}

// NewCrew returns the `crew` command.
func NewCrew(a *app.App) *cobra.Command {
	cmd := group(a, kind{res: rest.Crews, noun: "crew"}, "crews with an active production version", "crew ids are for call create --crew",
		func(ctx context.Context, c *rest.Client) (rest.Object, error) { return c.ListCrews(ctx) })
	cmd.AddCommand(newGenerate(a), newGeneration(a))
	return cmd
}

func group(a *app.App, k kind, what, note string, list func(context.Context, *rest.Client) (rest.Object, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   k.noun,
		Short: "List the " + what + " calls can use, and manage " + string(k.res),
		Args:  cobra.ArbitraryArgs,
		RunE:  a.GroupRunE,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the " + what + " of this account (" + note + ")",
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
