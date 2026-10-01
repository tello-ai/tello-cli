package doctor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/doctor"
	"github.com/tello-ai/tello-cli/internal/fakegateway"
	"github.com/tello-ai/tello-cli/internal/guide"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

type result struct {
	OK   bool `json:"ok"`
	Data struct {
		Checks []check `json:"checks"`
	} `json:"data"`
	Error map[string]any `json:"error"`
}

func run(t *testing.T, a *app.App) (result, int) {
	t.Helper()
	var out bytes.Buffer
	a.Stdout, a.Stderr, a.JSON = &out, &bytes.Buffer{}, true
	if a.Getenv == nil {
		a.Getenv = func(string) string { return "" }
	}
	cmd := doctor.New(a)
	cmd.SetArgs(nil)
	cmd.SetContext(context.Background())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	exit := 0
	if err := cmd.Execute(); err != nil {
		exit = a.ReportError(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout = %q, want one cli.result line", out.String())
	}
	var r result
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatal(err)
	}
	return r, exit
}

// statuses renders checks as "name=status" (plus ":code" when set).
func statuses(checks []check) string {
	parts := make([]string, len(checks))
	for i, c := range checks {
		parts[i] = c.Name + "=" + c.Status
		if c.Code != "" {
			parts[i] += ":" + c.Code
		}
	}
	return strings.Join(parts, " ")
}

func envKey(key string) func() (app.Credential, error) {
	return func() (app.Credential, error) { return app.Credential{Key: key, Source: "env"}, nil }
}

func noKey() (app.Credential, error) { return app.Credential{}, app.ErrNoCredential }

func wd(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func installSkill(t *testing.T, dir, target string, content []byte) {
	t.Helper()
	rel, current, err := guide.SkillFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if content == nil {
		content = current
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func authGateway(t *testing.T) *fakegateway.Server {
	return fakegateway.New(t, func(c *fakegateway.Conn) {
		if c.Authenticate("key-1") {
			c.WaitClosed()
		}
	})
}

func TestDoctorAllOK(t *testing.T) {
	dir := t.TempDir()
	for _, target := range guide.SkillTargets() {
		installSkill(t, dir, target, nil)
	}
	r, exit := run(t, &app.App{APIKey: envKey("key-1"), URL: authGateway(t).URL, Getwd: wd(dir)})

	want := "version=ok apiKey=ok gatewayUrl=ok auth=ok skills=ok"
	if exit != 0 || !r.OK || statuses(r.Data.Checks) != want {
		t.Fatalf("exit = %d, checks = %q, want %q", exit, statuses(r.Data.Checks), want)
	}
}

func TestDoctorSkillWarningsDoNotFail(t *testing.T) {
	dir := t.TempDir()
	installSkill(t, dir, "claude", []byte("old template\n")) // stale; codex missing
	r, exit := run(t, &app.App{APIKey: envKey("key-1"), URL: authGateway(t).URL, Getwd: wd(dir)})

	checks := r.Data.Checks
	if exit != 0 || !r.OK || checks[4].Name != "skills" || checks[4].Status != "warn" {
		t.Fatalf("exit = %d, checks = %+v", exit, checks)
	}
	if !strings.Contains(checks[4].Detail, "stale") || !strings.Contains(checks[4].Detail, "not installed") {
		t.Fatalf("skills detail = %q, want stale claude and missing codex", checks[4].Detail)
	}
}

func TestDoctorFailures(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(refusing.Close)

	tests := []struct {
		name     string
		app      *app.App
		exit     int
		code     string
		statuses string
	}{
		{
			name:     "missing key",
			app:      &app.App{APIKey: noKey, URL: "ws://127.0.0.1:1/sdk"},
			exit:     3,
			code:     "apiKeyMissing",
			statuses: "version=ok apiKey=fail:apiKeyMissing gatewayUrl=ok auth=skip skills=warn",
		},
		{
			name:     "bad url scheme",
			app:      &app.App{APIKey: envKey("key-1"), URL: "https://api.telloai.io/sdk"},
			exit:     2,
			code:     "invalidUrl",
			statuses: "version=ok apiKey=ok gatewayUrl=fail:invalidUrl auth=skip skills=warn",
		},
		{
			name:     "rejected key",
			app:      &app.App{APIKey: envKey("bad-key"), URL: authGateway(t).URL},
			exit:     3,
			code:     "unauthenticated",
			statuses: "version=ok apiKey=ok gatewayUrl=ok auth=fail:unauthenticated skills=warn",
		},
		{
			name:     "refused upgrade",
			app:      &app.App{APIKey: envKey("key-1"), URL: "ws" + strings.TrimPrefix(refusing.URL, "http") + "/sdk"},
			exit:     7,
			code:     "connectionFailed",
			statuses: "version=ok apiKey=ok gatewayUrl=ok auth=fail:connectionFailed skills=warn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.app.Getwd = wd(t.TempDir())
			r, exit := run(t, tt.app)
			if exit != tt.exit || r.OK || r.Error["code"] != tt.code {
				t.Fatalf("exit = %d, error = %v, want exit %d code %s", exit, r.Error, tt.exit, tt.code)
			}
			if got := statuses(r.Data.Checks); got != tt.statuses {
				t.Fatalf("checks = %q, want %q", got, tt.statuses)
			}
		})
	}
}
