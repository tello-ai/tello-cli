// Package auth implements `tello auth login|status|logout`
// (docs/design.md section 7). The API key is never printed.
package auth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/credentials"
	"github.com/tello-ai/tello-go/tello"
)

// New returns the `auth` command with its subcommands.
func New(a *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage the Tello API key",
		Args:  cobra.ArbitraryArgs,
		RunE:  a.GroupRunE,
	}
	cmd.AddCommand(newLogin(a), newStatus(a), newLogout(a))
	return cmd
}

func store(a *app.App) *credentials.Store {
	return credentials.New(a.Getenv, a.ConfigDir, a.Keychain)
}

type loginData struct {
	Stored string `json:"stored"`
	Path   string `json:"path,omitempty"`
}

func newLogin(a *app.App) *cobra.Command {
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API key (prompted on a terminal, else the first line of stdin)",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := readKey(cmd.Context(), a)
			if err != nil {
				return err
			}
			if !noVerify {
				// Verify before saving so a rejected key never replaces a good one.
				client, err := tello.NewClient(key, tello.WithURL(a.EffectiveURL()))
				if err != nil {
					return app.NewError(app.KindAuth, "apiKeyMissing", "no API key given")
				}
				err = a.Connect(cmd.Context(), client)
				_ = client.Close()
				if err != nil {
					return err
				}
			}
			cred, err := store(a).Save(key)
			if err != nil {
				return fmt.Errorf("save API key: %w", err)
			}
			if cred.Source == "file" {
				a.Warnf("OS keychain unavailable; API key stored in plain file %s (readable only by you)", cred.Path)
			}
			if cred.KeychainMayShadow {
				a.Warnf("could not remove an older API key from the OS keychain; if one exists it takes precedence over this file once the keychain is available. Run `tello auth logout` in a session where the keychain works, then log in again")
			}
			if a.Getenv(tello.EnvAPIKey) != "" {
				a.Warnf("%s is set and takes precedence over the stored key", tello.EnvAPIKey)
			}
			if a.JSON {
				return a.WriteResult(loginData{Stored: cred.Source, Path: cred.Path})
			}
			verified := "API key verified and saved"
			if noVerify {
				verified = "API key saved without verification"
			}
			if cred.Source == "file" {
				a.Printf("%s to %s.\n", verified, cred.Path)
			} else {
				a.Printf("%s to the OS keychain.\n", verified)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "save without checking the key against the gateway")
	return cmd
}

// readKey reads the key from a hidden terminal prompt, or the first line of
// stdin when stdin is not a terminal. An interrupt (cancelled ctx) before or
// while reading wins: no prompt is shown and nothing is returned.
func readKey(ctx context.Context, a *app.App) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var key string
	if a.StdinIsTerminal != nil && a.StdinIsTerminal() {
		if a.ReadSecret == nil {
			return "", app.NewError(app.KindInternal, "internal", "terminal prompt not configured")
		}
		secret, err := a.ReadSecret("Tello API key: ")
		if err != nil {
			return "", fmt.Errorf("read API key: %w", err)
		}
		key = secret
	} else if a.Stdin != nil {
		line, err := bufio.NewReader(a.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read API key from stdin: %w", err)
		}
		key = line
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", app.Usagef("no API key given: type it at the prompt or pipe it on stdin (keys are never accepted as flags)")
	}
	return key, nil
}

type statusData struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url"`
	// Authenticated is omitted with --offline.
	Authenticated *bool `json:"authenticated,omitempty"`
}

func newStatus(a *app.App) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show where the API key comes from and check it against the gateway",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cred, err := a.NewClient()
			if err != nil {
				return err
			}
			data := statusData{Source: cred.Source, Path: cred.Path, URL: a.EffectiveURL()}
			if !a.JSON {
				a.Printf("API key: %s\n", describeSource(cred))
				a.Printf("Gateway: %s\n", data.URL)
			}
			if offline {
				return a.WriteResult(data)
			}
			err = a.Connect(cmd.Context(), client)
			_ = client.Close()
			authenticated := err == nil
			data.Authenticated = &authenticated
			if err != nil {
				e := app.Classify(err)
				e.Data = data
				return e
			}
			if !a.JSON {
				a.Printf("Authenticated: yes\n")
			}
			return a.WriteResult(data)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "only resolve the key; do not contact the gateway")
	return cmd
}

func describeSource(cred app.Credential) string {
	switch cred.Source {
	case "env":
		return "from " + tello.EnvAPIKey
	case "keyring":
		return "from the OS keychain"
	case "file":
		return "from " + cred.Path
	}
	return cred.Source
}

type logoutData struct {
	Removed []string `json:"removed"`
}

func newLogout(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored API key",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, err := store(a).Delete()
			if err != nil {
				return fmt.Errorf("remove API key: %w", err)
			}
			if a.Getenv(tello.EnvAPIKey) != "" {
				a.Warnf("%s is still set; commands keep using it", tello.EnvAPIKey)
			}
			if a.JSON {
				return a.WriteResult(logoutData{Removed: removed})
			}
			if len(removed) == 0 {
				a.Printf("No stored API key found.\n")
			} else {
				a.Printf("Removed the API key from: %s.\n", strings.Join(removed, ", "))
			}
			return nil
		},
	}
}
