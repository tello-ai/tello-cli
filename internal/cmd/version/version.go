// Package version implements `tello version`.
package version

import (
	"runtime"

	"github.com/spf13/cobra"
	"github.com/tello-ai/tello-cli/internal/app"
	buildinfo "github.com/tello-ai/tello-cli/internal/version"
	"github.com/tello-ai/tello-go/tello"
)

type info struct {
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	Date            string `json:"date"`
	SDKVersion      string `json:"sdkVersion"`
	ProtocolVersion string `json:"protocolVersion"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
}

// New returns the `version` command.
func New(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show CLI, SDK and protocol versions",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			v := info{
				Version:         buildinfo.Version,
				Commit:          buildinfo.Commit,
				Date:            buildinfo.Date,
				SDKVersion:      tello.Version,
				ProtocolVersion: tello.ProtocolVersion,
				OS:              runtime.GOOS,
				Arch:            runtime.GOARCH,
			}
			if a.JSON {
				return a.WriteResult(v)
			}
			a.Printf("tello %s", v.Version)
			if v.Commit != "" {
				a.Printf(" (%s", v.Commit)
				if v.Date != "" {
					a.Printf(", %s", v.Date)
				}
				a.Printf(")")
			}
			a.Printf(" sdk %s protocol %s %s/%s\n", v.SDKVersion, v.ProtocolVersion, v.OS, v.Arch)
			return nil
		},
	}
}
