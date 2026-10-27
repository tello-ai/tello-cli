package target_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/target"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

func run(t *testing.T, newCmd func(*app.App) *cobra.Command, apiURL, key string, jsonMode bool) (int, string) {
	t.Helper()
	var out bytes.Buffer
	a := &app.App{
		Stdout: &out, Stderr: &bytes.Buffer{},
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: key, Source: "env"}, nil },
		APIURL: apiURL, JSON: jsonMode,
	}
	cmd := newCmd(a)
	cmd.SetArgs([]string{"list"})
	cmd.SetContext(context.Background())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	exit := 0
	if err := cmd.Execute(); err != nil {
		exit = a.ReportError(err)
	}
	return exit, out.String()
}

func TestListsTargets(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	api.Targets(
		[]map[string]any{{"id": "12", "name": "예약 안내", "description": "예약 확인"}},
		[]map[string]any{{"id": "34", "name": "접수 흐름", "description": nil}},
	)

	exit, out := run(t, target.NewAgent, api.URL, "key-1", true)
	var line struct {
		OK   bool `json:"ok"`
		Data struct {
			Data []map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &line); err != nil || exit != 0 || !line.OK || line.Data.Data[0]["id"] != "12" {
		t.Fatalf("agent list: exit %d stdout %q", exit, out)
	}

	exit, out = run(t, target.NewCrew, api.URL, "key-1", false)
	if exit != 0 || !strings.Contains(out, "34") || !strings.Contains(out, "접수 흐름") {
		t.Fatalf("crew list: exit %d stdout %q", exit, out)
	}
}

func TestRejectedKeyExitsAuth(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	exit, out := run(t, target.NewAgent, api.URL, "bad-key", true)
	if exit != 3 || !strings.Contains(out, `"code":"unauthenticated"`) {
		t.Fatalf("exit %d stdout %q, want exit 3 unauthenticated", exit, out)
	}
}
