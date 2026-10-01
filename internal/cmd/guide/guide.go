// Package guide implements `tello guide [topic] [--brief]`.
package guide

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/guide"
)

// New returns the `guide` command: without a topic it lists the topics; each
// topic is a subcommand.
func New(a *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide [topic]",
		Short: "Print operating guides for coding agents",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			topics := guide.Topics()
			if len(args) > 0 {
				return app.Usagef("unknown guide topic %q (available: %s)", args[0], strings.Join(topics, ", "))
			}
			if a.JSON {
				return a.WriteResult(map[string]any{"topics": topics})
			}
			a.Printf("Guide topics:\n")
			for _, topic := range topics {
				a.Printf("  %s\n", topic)
			}
			a.Printf("Run `tello guide <topic> [--brief]`.\n")
			return nil
		},
	}
	for _, topic := range guide.Topics() {
		cmd.AddCommand(newTopic(a, topic))
	}
	return cmd
}

func newTopic(a *app.App, topic string) *cobra.Command {
	var brief bool
	cmd := &cobra.Command{
		Use:   topic,
		Short: "Print the " + topic + " guide",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := guide.Text(topic, brief)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.WriteResult(map[string]any{"topic": topic, "brief": brief, "text": text})
			}
			a.Printf("%s", text)
			return nil
		},
	}
	cmd.Flags().BoolVar(&brief, "brief", false, "print the compact version")
	return cmd
}
