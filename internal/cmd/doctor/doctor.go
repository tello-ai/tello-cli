// Package doctor implements `tello doctor` (docs/design.md section 11).
package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/guide"
	buildinfo "github.com/tello-ai/tello-cli/internal/version"
	"github.com/tello-ai/tello-go/tello"
)

// Check statuses.
const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
	statusSkip = "skip"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`

	err *app.Error // the classified failure when Status is fail
}

func ok(name, detail string) check   { return check{Name: name, Status: statusOK, Detail: detail} }
func warn(name, detail string) check { return check{Name: name, Status: statusWarn, Detail: detail} }
func skip(name, detail string) check { return check{Name: name, Status: statusSkip, Detail: detail} }

func fail(name string, e *app.Error) check {
	return check{Name: name, Status: statusFail, Detail: e.Message, Code: e.Code, err: e}
}

type report struct {
	Checks []check `json:"checks"`
}

// New returns the `doctor` command.
func New(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check version, API key, gateway URL, authentication and skill files",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := runChecks(cmd.Context(), a)
			if !a.JSON {
				for _, c := range checks {
					a.Printf("%-4s  %-10s  %s\n", c.Status, c.Name, c.Detail)
				}
			}
			data := report{Checks: checks}
			for _, c := range checks {
				if c.Status == statusFail {
					c.err.Data = data
					return c.err
				}
			}
			return a.WriteResult(data)
		},
	}
}

func runChecks(ctx context.Context, a *app.App) []check {
	checks := []check{
		ok("version", fmt.Sprintf("tello %s (sdk %s, protocol %s)", buildinfo.Version, tello.Version, tello.ProtocolVersion)),
	}

	cred, keyCheck := checkAPIKey(a)
	checks = append(checks, keyCheck)

	gatewayURL := a.EffectiveURL()
	urlCheck := checkURL(gatewayURL)
	checks = append(checks, urlCheck)

	switch {
	case keyCheck.Status != statusOK:
		checks = append(checks, skip("auth", "no usable API key"))
	case urlCheck.Status != statusOK:
		checks = append(checks, skip("auth", "invalid gateway URL"))
	default:
		checks = append(checks, checkAuth(ctx, a, cred.Key, gatewayURL))
	}

	return append(checks, checkSkills(a))
}

func checkAPIKey(a *app.App) (app.Credential, check) {
	if a.APIKey == nil {
		return app.Credential{}, fail("apiKey", app.NewError(app.KindInternal, "internal", "API key resolver not configured"))
	}
	cred, err := a.APIKey()
	if err != nil {
		// Classify maps app.ErrNoCredential to apiKeyMissing.
		return app.Credential{}, fail("apiKey", app.Classify(err))
	}
	switch cred.Source {
	case "env":
		return cred, ok("apiKey", "from "+tello.EnvAPIKey)
	case "keyring":
		return cred, ok("apiKey", "from the OS keychain")
	case "file":
		return cred, ok("apiKey", "from "+cred.Path)
	}
	return cred, ok("apiKey", "from "+cred.Source)
}

func checkURL(raw string) check {
	if err := app.ValidateURL(raw); err != nil {
		return fail("gatewayUrl", app.Classify(err))
	}
	return ok("gatewayUrl", raw)
}

func checkAuth(ctx context.Context, a *app.App, key, gatewayURL string) check {
	client, err := tello.NewClient(key, tello.WithURL(gatewayURL))
	if err != nil {
		return fail("auth", app.Classify(err))
	}
	start := time.Now()
	err = a.Connect(ctx, client)
	elapsed := time.Since(start).Milliseconds()
	_ = client.Close()
	if err != nil {
		return fail("auth", app.Classify(err))
	}
	return ok("auth", fmt.Sprintf("authenticated in %d ms", elapsed))
}

// checkSkills compares the skill files in the current directory with the
// templates `tello init` would write.
func checkSkills(a *app.App) check {
	dir, err := a.Getwd()
	if err != nil {
		return warn("skills", fmt.Sprintf("cannot determine the current directory: %v", err))
	}
	status := statusOK
	var details []string
	for _, target := range guide.SkillTargets() {
		rel, want, err := guide.SkillFile(target)
		if err != nil {
			return warn("skills", err.Error())
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		var state string
		switch {
		case errors.Is(err, os.ErrNotExist):
			status, state = statusWarn, "not installed (run tello init)"
		case err != nil:
			status, state = statusWarn, fmt.Sprintf("unreadable: %v", err)
		case !bytes.Equal(got, want):
			status, state = statusWarn, "stale (run tello init)"
		default:
			state = "up to date"
		}
		details = append(details, target+": "+state)
	}
	return check{Name: "skills", Status: status, Detail: strings.Join(details, "; ")}
}
