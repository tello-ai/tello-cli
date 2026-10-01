// Package version holds build metadata injected by GoReleaser ldflags:
//
//	-X github.com/tello-ai/tello-cli/internal/version.Version={{.Version}}
//	-X github.com/tello-ai/tello-cli/internal/version.Commit={{.ShortCommit}}
//	-X github.com/tello-ai/tello-cli/internal/version.Date={{.Date}}
package version

var (
	// Version is the release without the leading "v"; "dev" for local builds.
	Version = "dev"
	Commit  = ""
	Date    = ""
)
