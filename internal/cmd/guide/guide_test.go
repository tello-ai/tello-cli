package guide_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	guidecmd "github.com/tello-ai/tello-cli/internal/cmd/guide"
	"github.com/tello-ai/tello-cli/internal/guide"
)

type result struct {
	Type  string         `json:"type"`
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error map[string]any `json:"error"`
}

func run(t *testing.T, jsonMode bool, args ...string) (result, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app.App{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, JSON: jsonMode}
	cmd := guidecmd.New(a)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	exit := 0
	if err := cmd.Execute(); err != nil {
		exit = a.ReportError(err)
	}
	var r result
	if jsonMode {
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("stdout = %q, want exactly one cli.result line", out.String())
		}
		if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
			t.Fatal(err)
		}
	}
	return r, out.String(), exit
}

func TestGuideTopicJSON(t *testing.T) {
	want, err := guide.Text("coding-agent", true)
	if err != nil {
		t.Fatal(err)
	}
	r, _, exit := run(t, true, "coding-agent", "--brief")
	if exit != 0 || !r.OK {
		t.Fatalf("exit = %d, result = %+v", exit, r)
	}
	if r.Data["topic"] != "coding-agent" || r.Data["brief"] != true || r.Data["text"] != want {
		t.Fatalf("data = %v", r.Data)
	}
}

func TestGuideHumanPrintsText(t *testing.T) {
	want, err := guide.Text("coding-agent", false)
	if err != nil {
		t.Fatal(err)
	}
	_, out, exit := run(t, false, "coding-agent")
	if exit != 0 || out != want {
		t.Fatalf("exit = %d, stdout differs from the guide text", exit)
	}
}

func TestGuideListsTopics(t *testing.T) {
	r, _, exit := run(t, true)
	topics, _ := r.Data["topics"].([]any)
	if exit != 0 || len(topics) != 1 || topics[0] != "coding-agent" {
		t.Fatalf("exit = %d, data = %v", exit, r.Data)
	}
}

func TestGuideUnknownTopicIsUsageError(t *testing.T) {
	r, _, exit := run(t, true, "nope")
	if exit != 2 || r.OK || r.Error["kind"] != "usage" {
		t.Fatalf("exit = %d, result = %+v", exit, r)
	}
}
