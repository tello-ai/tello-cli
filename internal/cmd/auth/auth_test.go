package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/auth"
	"github.com/tello-ai/tello-cli/internal/credentials"
	"github.com/tello-ai/tello-cli/internal/fakegateway"
)

// fakeKeychain is an in-memory app.SecretStore; err makes it unavailable,
// setErr fails only Set.
type fakeKeychain struct {
	secrets map[string]string
	err     error
	setErr  error
}

func (k *fakeKeychain) Get(service, user string) (string, error) {
	if k.err != nil {
		return "", k.err
	}
	v, ok := k.secrets[service+"/"+user]
	if !ok {
		return "", app.ErrSecretNotFound
	}
	return v, nil
}

func (k *fakeKeychain) Set(service, user, secret string) error {
	if k.err != nil {
		return k.err
	}
	if k.setErr != nil {
		return k.setErr
	}
	k.secrets[service+"/"+user] = secret
	return nil
}

func (k *fakeKeychain) Delete(service, user string) error {
	if k.err != nil {
		return k.err
	}
	if _, ok := k.secrets[service+"/"+user]; !ok {
		return app.ErrSecretNotFound
	}
	delete(k.secrets, service+"/"+user)
	return nil
}

func (k *fakeKeychain) stored() string { return k.secrets[credentials.Service+"/"+credentials.Account] }

type env struct {
	app       *app.App
	out, errb *bytes.Buffer
	keychain  *fakeKeychain
	configDir string
	vars      map[string]string
	ctx       context.Context
}

func newEnv(t *testing.T, url string) *env {
	t.Helper()
	e := &env{
		out: &bytes.Buffer{}, errb: &bytes.Buffer{},
		keychain:  &fakeKeychain{secrets: map[string]string{}},
		configDir: filepath.Join(t.TempDir(), "tello"),
		vars:      map[string]string{},
		ctx:       context.Background(),
	}
	getenv := func(k string) string { return e.vars[k] }
	configDir := func() (string, error) { return e.configDir, nil }
	e.app = &app.App{
		Stdin: strings.NewReader(""), Stdout: e.out, Stderr: e.errb,
		Getenv:          getenv,
		ConfigDir:       configDir,
		Keychain:        e.keychain,
		APIKey:          credentials.New(getenv, configDir, e.keychain).Resolve,
		StdinIsTerminal: func() bool { return false },
		ReadSecret:      func(string) (string, error) { return "", errors.New("no terminal") },
		URL:             url,
		JSON:            true,
	}
	return e
}

type result struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error map[string]any `json:"error"`
}

func (e *env) run(t *testing.T, args ...string) (result, int) {
	t.Helper()
	cmd := auth.New(e.app)
	cmd.SetArgs(args)
	cmd.SetContext(e.ctx)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	exit := 0
	if err := cmd.Execute(); err != nil {
		exit = e.app.ReportError(err)
	}
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout = %q, want one cli.result line", e.out.String())
	}
	var r result
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatal(err)
	}
	return r, exit
}

func (e *env) credentialsFile() string { return filepath.Join(e.configDir, "credentials.json") }

func gateway(t *testing.T) *fakegateway.Server {
	return fakegateway.New(t, func(c *fakegateway.Conn) {
		if c.Authenticate("key-1") {
			c.WaitClosed()
		}
	})
}

func TestLoginRejectedKeyStoresNothing(t *testing.T) {
	e := newEnv(t, gateway(t).URL)
	e.app.Stdin = strings.NewReader("bad-key\n")

	r, exit := e.run(t, "login")
	if exit != 3 || r.Error["code"] != "unauthenticated" {
		t.Fatalf("exit = %d, result = %+v, want unauthenticated (3)", exit, r)
	}
	if e.keychain.stored() != "" {
		t.Fatalf("rejected key was stored in the keychain")
	}
	if _, err := os.Stat(e.credentialsFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected key was stored in a file: %v", err)
	}
}

func TestLoginFromStdinStoresVerifiedKeyInKeychain(t *testing.T) {
	e := newEnv(t, gateway(t).URL)
	e.app.Stdin = strings.NewReader("key-1\nignored second line\n")

	r, exit := e.run(t, "login")
	if exit != 0 || !r.OK || r.Data["stored"] != "keyring" {
		t.Fatalf("exit = %d, result = %+v", exit, r)
	}
	if _, hasPath := r.Data["path"]; hasPath {
		t.Fatalf("data has a path for a keychain save: %v", r.Data)
	}
	if e.keychain.stored() != "key-1" {
		t.Fatalf("keychain = %q, want key-1", e.keychain.stored())
	}
	if strings.Contains(e.out.String()+e.errb.String(), "key-1") {
		t.Fatalf("API key leaked to output: stdout %q stderr %q", e.out.String(), e.errb.String())
	}
}

func TestLoginTTYPromptFallsBackToFileWithWarnings(t *testing.T) {
	e := newEnv(t, "ws://127.0.0.1:1/sdk") // never dialed with --no-verify
	e.keychain.err = errors.New("keychain unavailable")
	e.vars["TELLO_API_KEY"] = "env-key"
	e.app.StdinIsTerminal = func() bool { return true }
	e.app.ReadSecret = func(string) (string, error) { return "typed-key\n", nil }

	r, exit := e.run(t, "login", "--no-verify")
	if exit != 0 || r.Data["stored"] != "file" || r.Data["path"] != e.credentialsFile() {
		t.Fatalf("exit = %d, result = %+v", exit, r)
	}
	data, err := os.ReadFile(e.credentialsFile())
	if err != nil || !strings.Contains(string(data), `"typed-key"`) {
		t.Fatalf("credentials file = %q, %v", data, err)
	}
	stderr := e.errb.String()
	if !strings.Contains(stderr, e.credentialsFile()) || !strings.Contains(stderr, "TELLO_API_KEY") {
		t.Fatalf("stderr = %q, want plain-file and TELLO_API_KEY warnings", stderr)
	}
}

func TestLoginWarnsOnlyWhenOldKeychainEntryMayShadowFile(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*fakeKeychain)
		wantShadow bool
	}{
		{"keychain unusable", func(k *fakeKeychain) { k.err = errors.New("keychain locked") }, true},
		{"keychain write fails, nothing stored", func(k *fakeKeychain) { k.setErr = errors.New("keychain locked") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, "ws://127.0.0.1:1/sdk")
			tt.setup(e.keychain)
			e.app.Stdin = strings.NewReader("key-1\n")

			r, exit := e.run(t, "login", "--no-verify")
			if exit != 0 || r.Data["stored"] != "file" {
				t.Fatalf("exit = %d, result = %+v", exit, r)
			}
			if got := strings.Contains(e.errb.String(), "tello auth logout"); got != tt.wantShadow {
				t.Fatalf("stderr = %q, want keychain-shadow warning: %v", e.errb.String(), tt.wantShadow)
			}
		})
	}
}

func TestLoginAlreadyInterruptedDoesNotPrompt(t *testing.T) {
	e := newEnv(t, "ws://127.0.0.1:1/sdk")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.ctx = ctx
	prompted := false
	e.app.StdinIsTerminal = func() bool { return true }
	e.app.ReadSecret = func(string) (string, error) { prompted = true; return "key-1", nil }

	r, exit := e.run(t, "login", "--no-verify")
	if exit != 130 || r.Error["code"] != "interrupted" {
		t.Fatalf("exit = %d, result = %+v, want interrupted (130)", exit, r)
	}
	if prompted || e.keychain.stored() != "" {
		t.Fatalf("prompted = %v, keychain = %q; want no prompt and nothing stored", prompted, e.keychain.stored())
	}
}

func TestLoginWithoutKeyIsUsageError(t *testing.T) {
	e := newEnv(t, "ws://127.0.0.1:1/sdk")
	e.app.Stdin = strings.NewReader("\n")

	r, exit := e.run(t, "login")
	if exit != 2 || r.Error["kind"] != "usage" {
		t.Fatalf("exit = %d, result = %+v, want usage error", exit, r)
	}
}

func TestStatus(t *testing.T) {
	gw := gateway(t)
	t.Run("offline", func(t *testing.T) {
		e := newEnv(t, gw.URL)
		e.keychain.secrets[credentials.Service+"/"+credentials.Account] = "key-1"
		r, exit := e.run(t, "status", "--offline")
		want := map[string]any{"source": "keyring", "url": gw.URL}
		if exit != 0 || !mapsEqual(r.Data, want) {
			t.Fatalf("exit = %d, data = %v, want %v", exit, r.Data, want)
		}
	})
	t.Run("online file key", func(t *testing.T) {
		e := newEnv(t, gw.URL)
		if err := os.MkdirAll(e.configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.credentialsFile(), []byte(`{"apiKey":"key-1"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		r, exit := e.run(t, "status")
		want := map[string]any{"source": "file", "path": e.credentialsFile(), "url": gw.URL, "authenticated": true}
		if exit != 0 || !mapsEqual(r.Data, want) {
			t.Fatalf("exit = %d, data = %v, want %v", exit, r.Data, want)
		}
	})
	t.Run("rejected key", func(t *testing.T) {
		e := newEnv(t, gw.URL)
		e.vars["TELLO_API_KEY"] = "bad-key"
		r, exit := e.run(t, "status")
		want := map[string]any{"source": "env", "url": gw.URL, "authenticated": false}
		if exit != 3 || r.Error["code"] != "unauthenticated" || !mapsEqual(r.Data, want) {
			t.Fatalf("exit = %d, result = %+v", exit, r)
		}
	})
	t.Run("missing key", func(t *testing.T) {
		e := newEnv(t, gw.URL)
		r, exit := e.run(t, "status", "--offline")
		if exit != 3 || r.Error["code"] != "apiKeyMissing" {
			t.Fatalf("exit = %d, result = %+v", exit, r)
		}
	})
}

func TestLogoutRemovesStoredKeys(t *testing.T) {
	e := newEnv(t, "ws://127.0.0.1:1/sdk")
	e.keychain.secrets[credentials.Service+"/"+credentials.Account] = "key-1"
	e.vars["TELLO_API_KEY"] = "env-key"

	r, exit := e.run(t, "logout")
	removed, _ := r.Data["removed"].([]any)
	if exit != 0 || len(removed) != 1 || removed[0] != "keyring" {
		t.Fatalf("exit = %d, data = %v", exit, r.Data)
	}
	if e.keychain.stored() != "" {
		t.Fatalf("key still in keychain")
	}
	if !strings.Contains(e.errb.String(), "TELLO_API_KEY") {
		t.Fatalf("stderr = %q, want a note that TELLO_API_KEY is still set", e.errb.String())
	}
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	e := newEnv(t, "ws://127.0.0.1:1/sdk")
	r, exit := e.run(t, "signup")
	if exit != 2 || r.Error["kind"] != "usage" {
		t.Fatalf("exit = %d, result = %+v", exit, r)
	}
}

func mapsEqual(got, want map[string]any) bool {
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	return bytes.Equal(g, w)
}
