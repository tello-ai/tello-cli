package target_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
)

type result struct {
	exit   int
	stdout string
	stderr string
	lines  []map[string]any
}

// exec runs `<group> <args>` like the root command does, against the REST
// API at apiURL with key-1.
func exec(t *testing.T, newCmd func(*app.App) *cobra.Command, apiURL, stdin string, jsonMode bool, args ...string) result {
	t.Helper()
	return execCtx(t, context.Background(), newCmd, apiURL, stdin, jsonMode, args...)
}

func execCtx(t *testing.T, ctx context.Context, newCmd func(*app.App) *cobra.Command, apiURL, stdin string, jsonMode bool, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := &app.App{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1", Source: "env"}, nil },
		APIURL: apiURL, JSON: jsonMode,
	}
	cmd := newCmd(a)
	if args == nil {
		args = []string{} // nil makes cobra read os.Args
	}
	cmd.SetArgs(args)
	cmd.SetContext(ctx)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	done := make(chan int, 1)
	go func() {
		exit := 0
		if err := cmd.Execute(); err != nil {
			exit = a.ReportError(err)
		}
		done <- exit
	}()
	var r result
	select {
	case r.exit = <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("command did not return")
	}
	r.stdout, r.stderr = stdout.String(), stderr.String()
	for _, line := range strings.Split(strings.TrimRight(r.stdout, "\n"), "\n") {
		if line == "" || !jsonMode {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		r.lines = append(r.lines, v)
	}
	return r
}

// ok returns the data of the single successful cli.result line.
func (r result) ok(t *testing.T) map[string]any {
	t.Helper()
	if r.exit != 0 || len(r.lines) != 1 || r.lines[0]["type"] != "cli.result" || r.lines[0]["ok"] != true {
		t.Fatalf("exit %d, want one ok cli.result; stdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	}
	data, _ := r.lines[0]["data"].(map[string]any)
	return data
}

// failed checks the last line is a failed cli.result with code and exit,
// and returns its data.
func (r result) failed(t *testing.T, exit int, code string) map[string]any {
	t.Helper()
	if len(r.lines) == 0 {
		t.Fatalf("no stdout lines; exit %d stderr:\n%s", r.exit, r.stderr)
	}
	l := r.lines[len(r.lines)-1]
	errObj, _ := l["error"].(map[string]any)
	if r.exit != exit || l["type"] != "cli.result" || l["ok"] != false || errObj["code"] != code {
		t.Fatalf("exit %d result %v, want exit %d code %s\nstderr:\n%s", r.exit, l, exit, code, r.stderr)
	}
	data, _ := l["data"].(map[string]any)
	return data
}

func (r result) linesOfType(typ string) []map[string]any {
	var out []map[string]any
	for _, l := range r.lines {
		if l["type"] == typ {
			out = append(out, l)
		}
	}
	return out
}

func jsonOf(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
