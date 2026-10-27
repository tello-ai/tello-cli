// Package app holds what every command shares: standard streams, global
// flags, injected dependencies, error classification and the output
// contract of docs/design.md sections 5 and 6.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/tello-ai/tello-go/tello"
)

// Credential is a resolved API key and where it came from.
type Credential struct {
	Key string
	// Source is "env", "keyring" or "file".
	Source string
	// Path is the credentials file when Source is "file".
	Path string
}

// SecretStore is the OS keychain. Get returns ErrSecretNotFound when the
// entry does not exist; any other error means the keychain is unavailable.
type SecretStore interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

var ErrSecretNotFound = errors.New("secret not found")

// App is created once per process by main and passed to every command.
// Tests build it directly with fakes.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Getenv func(string) string
	Getwd  func() (string, error)
	// ConfigDir returns the tello configuration directory
	// (<user config dir>/tello). It need not exist.
	ConfigDir func() (string, error)
	Keychain  SecretStore
	// APIKey resolves the key every gateway command uses. It returns
	// ErrNoCredential when none is configured.
	APIKey func() (Credential, error)
	// StdinIsTerminal reports whether Stdin is an interactive terminal.
	StdinIsTerminal func() bool
	// ReadSecret prompts on Stderr and reads a line from the terminal
	// without echo.
	ReadSecret func(prompt string) (string, error)

	// Global flags, bound by the root command.
	JSON bool
	// URL is the WebSocket gateway (--url), used by calls answered from the
	// terminal or a handler.
	URL string
	// APIURL is the REST API base (--api-url), used by everything else.
	APIURL string

	outMu  sync.Mutex
	outErr error
}

// EffectiveURL is the gateway URL: --url, then TELLO_URL, then the
// production gateway.
func (a *App) EffectiveURL() string {
	if a.URL != "" {
		return a.URL
	}
	if v := a.Getenv(tello.EnvURL); v != "" {
		return v
	}
	return tello.DefaultURL
}

// EnvAPIURL overrides the REST API base URL; DefaultAPIURL is production.
const (
	EnvAPIURL     = "TELLO_API_URL"
	DefaultAPIURL = "https://api.telloai.io/v1"
)

// EffectiveAPIURL is the REST API base URL: --api-url, then TELLO_API_URL,
// then production. A trailing slash is dropped so paths join cleanly.
func (a *App) EffectiveAPIURL() string {
	raw := a.APIURL
	if raw == "" {
		raw = a.Getenv(EnvAPIURL)
	}
	if raw == "" {
		raw = DefaultAPIURL
	}
	return strings.TrimRight(raw, "/")
}

// ValidateAPIURL requires HTTPS except for loopback HTTP test servers.
func ValidateAPIURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") ||
		(u.Scheme == "http" && !isLoopbackHost(u.Hostname())) {
		return NewError(KindUsage, "invalidUrl",
			fmt.Sprintf("API URL must use HTTPS (HTTP is allowed for loopback only), with a host and no credentials, query or fragment (--api-url / %s)", EnvAPIURL))
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewClient resolves the API key and builds an unconnected client for the
// effective URL. Register event handlers before calling Connect.
func (a *App) NewClient() (*tello.Client, Credential, error) {
	if err := ValidateURL(a.EffectiveURL()); err != nil {
		return nil, Credential{}, err
	}
	cred, err := a.Credential()
	if err != nil {
		return nil, Credential{}, err
	}
	client, err := tello.NewClient(cred.Key, tello.WithURL(a.EffectiveURL()))
	if err != nil {
		return nil, Credential{}, NewError(KindAuth, "apiKeyMissing", err.Error())
	}
	return client, cred, nil
}

// Credential resolves the API key every command uses; a missing key is an
// auth error (apiKeyMissing).
func (a *App) Credential() (Credential, error) {
	if a.APIKey == nil {
		return Credential{}, NewError(KindInternal, "internal", "API key resolver not configured")
	}
	cred, err := a.APIKey()
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return Credential{}, NewError(KindAuth, "apiKeyMissing", "no API key configured")
		}
		return Credential{}, err
	}
	return cred, nil
}

// ValidateURL reports a gateway URL that is not ws:// or wss:// with a host
// as a usage error (invalidUrl).
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return NewError(KindUsage, "invalidUrl",
			fmt.Sprintf("gateway URL %q must be ws:// or wss:// with a host (--url / %s)", raw, tello.EnvURL))
	}
	return nil
}

// Connect opens and authenticates the connection. Transport failures are
// connectionFailed: those the SDK returns unclassified (dial, TLS,
// handshake), and a connection that ends during authentication without the
// gateway rejecting the key (the SDK reports it as an AuthenticationError
// without a code; real rejections carry "unauthenticated").
func (a *App) Connect(ctx context.Context, client *tello.Client) error {
	err := client.Connect(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var authErr *tello.AuthenticationError
	if errors.As(err, &authErr) && authErr.Code != "" {
		return err
	}
	if authErr == nil && Classify(err).Kind != KindInternal {
		return err
	}
	return &Error{
		Kind:    KindConnection,
		Code:    "connectionFailed",
		Message: fmt.Sprintf("connect to %s: %v", a.EffectiveURL(), err),
	}
}
