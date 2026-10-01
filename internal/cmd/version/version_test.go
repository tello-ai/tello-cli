package version_test

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	versioncmd "github.com/tello-ai/tello-cli/internal/cmd/version"
	"github.com/tello-ai/tello-cli/internal/version"
	"github.com/tello-ai/tello-go/tello"
)

func TestVersionJSON(t *testing.T) {
	oldVersion, oldCommit, oldDate := version.Version, version.Commit, version.Date
	version.Version, version.Commit, version.Date = "1.2.3", "abc1234", "2026-10-01T00:00:00Z"
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldVersion, oldCommit, oldDate })

	var out, errb bytes.Buffer
	a := &app.App{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, JSON: true}
	cmd := versioncmd.New(a)
	cmd.SetArgs(nil)
	cmd.SetContext(context.Background())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout = %q, want one line", out.String())
	}
	var result struct {
		Type string            `json:"type"`
		OK   bool              `json:"ok"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &result); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"version":         "1.2.3",
		"commit":          "abc1234",
		"date":            "2026-10-01T00:00:00Z",
		"sdkVersion":      tello.Version,
		"protocolVersion": tello.ProtocolVersion,
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
	}
	if result.Type != "cli.result" || !result.OK || len(result.Data) != len(want) {
		t.Fatalf("result = %+v", result)
	}
	for k, v := range want {
		if result.Data[k] != v {
			t.Fatalf("data[%q] = %q, want %q", k, result.Data[k], v)
		}
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	var out, errb bytes.Buffer
	a := &app.App{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, JSON: true}
	cmd := versioncmd.New(a)
	cmd.SetArgs([]string{"extra"})
	cmd.SetContext(context.Background())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.Execute()
	if err == nil || a.ReportError(err) != 2 {
		t.Fatalf("Execute(extra) = %v, want usage error (exit 2)", err)
	}
}
