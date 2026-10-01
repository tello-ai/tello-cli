package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := &app.App{
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Getenv:    func(string) string { return "" },
		Getwd:     func() (string, error) { return t.TempDir(), nil },
		ConfigDir: func() (string, error) { return t.TempDir(), nil },
		APIKey:    func() (app.Credential, error) { return app.Credential{}, app.ErrNoCredential },
	}
	code := Main(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, _, stderr := run(t, "dial")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "dial") {
		t.Fatalf("stderr should name the unknown command: %q", stderr)
	}
}

func TestUnknownFlagIsUsageErrorInJSON(t *testing.T) {
	code, stdout, _ := run(t, "--json", "version", "--nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &line); err != nil {
		t.Fatalf("stdout must be one JSON line: %q", stdout)
	}
	if line["type"] != "cli.result" || line["ok"] != false {
		t.Fatalf("unexpected result: %v", line)
	}
}

func TestJSONFlagReachesSubcommands(t *testing.T) {
	code, stdout, _ := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &line); err != nil {
		t.Fatalf("stdout must be one JSON line: %q", stdout)
	}
	if line["type"] != "cli.result" || line["ok"] != true {
		t.Fatalf("unexpected result: %v", line)
	}
}

func TestMissingAPIKeyExitsAuth(t *testing.T) {
	code, _, stderr := run(t, "call", "summary", "call-1")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stderr %q)", code, stderr)
	}
}

func TestGroupsPrintHelpWithoutSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"call"}, {"auth"}} {
		code, stdout, stderr := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d, want 0 (stderr %q)", args, code, stderr)
		}
		if !strings.Contains(stdout, "Available Commands") {
			t.Fatalf("%v: stdout should be help: %q", args, stdout)
		}
	}
}

func TestGroupsWithoutSubcommandFailInJSON(t *testing.T) {
	code, stdout, _ := run(t, "--json", "call")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stdout, `"ok":false`) || strings.Contains(stdout, "Available Commands") {
		t.Fatalf("JSON mode must print one failed cli.result, not help: %q", stdout)
	}
}

func TestUnknownSubcommandSuggestsName(t *testing.T) {
	code, _, stderr := run(t, "call", "creat")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "create") {
		t.Fatalf("stderr should suggest create: %q", stderr)
	}
}

// Every command in the tree honors the --json contract; cobra's generated
// completion command would not, so it is not offered.
func TestNoCompletionCommand(t *testing.T) {
	code, stdout, _ := run(t, "--json", "completion", "bash")
	if code != 2 || !strings.Contains(stdout, `"ok":false`) {
		t.Fatalf("exit = %d stdout %q, want usage error", code, stdout)
	}
}
