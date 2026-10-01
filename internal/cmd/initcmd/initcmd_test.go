package initcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/initcmd"
	"github.com/tello-ai/tello-cli/internal/guide"
)

type fileStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type result struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error map[string]any  `json:"error"`
}

func run(t *testing.T, wd string, args ...string) ([]fileStatus, result, int) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app.App{
		Stdout: &out, Stderr: &errb,
		Getenv: func(string) string { return "" },
		Getwd:  func() (string, error) { return wd, nil },
		JSON:   true,
	}
	cmd := initcmd.New(a)
	cmd.SetArgs(args)
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
	var data struct {
		Files []fileStatus `json:"files"`
	}
	if r.OK {
		if err := json.Unmarshal(r.Data, &data); err != nil {
			t.Fatal(err)
		}
	}
	return data.Files, r, exit
}

func TestInitCreatesThenReportsUnchangedAndUpdated(t *testing.T) {
	wd := t.TempDir()
	claudePath, claudeContent, _ := guide.SkillFile("claude")
	codexPath, _, _ := guide.SkillFile("codex")

	files, _, exit := run(t, wd)
	want := []fileStatus{{claudePath, "created"}, {codexPath, "created"}}
	if exit != 0 || !reflect.DeepEqual(files, want) {
		t.Fatalf("first run: exit %d, files %v, want %v", exit, files, want)
	}
	written, err := os.ReadFile(filepath.Join(wd, filepath.FromSlash(claudePath)))
	if err != nil || !bytes.Equal(written, claudeContent) {
		t.Fatalf("claude skill file not written with the template: %v", err)
	}

	files, _, _ = run(t, wd)
	want = []fileStatus{{claudePath, "unchanged"}, {codexPath, "unchanged"}}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("rerun: files %v, want %v", files, want)
	}

	full := filepath.Join(wd, filepath.FromSlash(claudePath))
	if err := os.WriteFile(full, []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, _ = run(t, wd)
	want = []fileStatus{{claudePath, "updated"}, {codexPath, "unchanged"}}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("after edit: files %v, want %v", files, want)
	}
	if written, _ := os.ReadFile(full); !bytes.Equal(written, claudeContent) {
		t.Fatalf("edited skill file was not overwritten")
	}
}

func TestInitTargetAndRelativeDir(t *testing.T) {
	wd := t.TempDir()
	if err := os.Mkdir(filepath.Join(wd, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath, _, _ := guide.SkillFile("codex")

	files, _, exit := run(t, wd, "--target", "codex", "--dir", "proj")
	if exit != 0 || !reflect.DeepEqual(files, []fileStatus{{codexPath, "created"}}) {
		t.Fatalf("exit %d, files %v", exit, files)
	}
	if _, err := os.Stat(filepath.Join(wd, "proj", filepath.FromSlash(codexPath))); err != nil {
		t.Fatalf("skill file not in --dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wd, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("untargeted claude files were written: %v", err)
	}
}

func TestInitUsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown target":  {"--target", "claude,vim"},
		"missing dir":     {"--dir", "does-not-exist"},
		"extra arguments": {"now"},
	} {
		t.Run(name, func(t *testing.T) {
			wd := t.TempDir()
			_, r, exit := run(t, wd, args...)
			if exit != 2 || r.OK {
				t.Fatalf("exit = %d, result = %+v, want usage error", exit, r)
			}
			if entries, _ := os.ReadDir(wd); len(entries) != 0 {
				t.Fatalf("files written despite usage error: %v", entries)
			}
		})
	}
}
