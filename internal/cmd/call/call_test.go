package call_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/call"
	"github.com/tello-ai/tello-cli/internal/fakegateway"
	"github.com/tello-ai/tello-cli/internal/handler/handlertest"
)

func TestMain(m *testing.M) { handlertest.Main(m) }

type result struct {
	exit   int
	stdout string
	stderr string
	lines  []map[string]any
}

// run executes `call <args>` like the root command does, against the
// WebSocket gateway at url.
func run(t *testing.T, url, stdin string, jsonMode bool, args ...string) result {
	t.Helper()
	return runCtx(t, context.Background(), func(a *app.App) { a.URL = url }, stdin, jsonMode, args...)
}

// runAPI executes `call <args>` against the REST API at apiURL.
func runAPI(t *testing.T, apiURL, stdin string, jsonMode bool, args ...string) result {
	t.Helper()
	return runCtx(t, context.Background(), func(a *app.App) { a.APIURL = apiURL }, stdin, jsonMode, args...)
}

func runCtx(t *testing.T, ctx context.Context, setup func(*app.App), stdin string, jsonMode bool, args ...string) result {
	t.Helper()
	var stdout bytes.Buffer
	stderr := &handlertest.SyncBuffer{}
	a := &app.App{
		Stdin:  strings.NewReader(stdin),
		Stdout: &stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1", Source: "env"}, nil },
		JSON:   jsonMode,
	}
	setup(a)
	cmd := call.New(a)
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

func (r result) last(t *testing.T) map[string]any {
	t.Helper()
	if len(r.lines) == 0 {
		t.Fatalf("no stdout lines; exit %d stderr:\n%s", r.exit, r.stderr)
	}
	l := r.lines[len(r.lines)-1]
	if l["type"] != "cli.result" {
		t.Fatalf("last line %v is not cli.result", l)
	}
	return l
}

func (r result) failed(t *testing.T, exit int, code string) map[string]any {
	t.Helper()
	l := r.last(t)
	errObj, _ := l["error"].(map[string]any)
	if r.exit != exit || l["ok"] != false || errObj["code"] != code {
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
	b, _ := json.Marshal(v)
	return string(b)
}

// completeCall scripts one successful call with callID.
func completeCall(c *fakegateway.Conn, callID string) map[string]any {
	data := c.ReadEvent("createCall")
	c.Event("call.created", callID, nil)
	c.Event("call.completed", callID, map[string]any{"status": "completed"})
	c.WaitClosed()
	return data
}

func TestCreateHappyPathJSON(t *testing.T) {
	t.Parallel()
	created := make(chan map[string]any, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		created <- completeCall(c, "call-1")
	})
	r := run(t, gw.URL, "", true, "create", "--to", "+821012345678", "--prompt", "예약 확인",
		"--metadata", `{"orderId":42,"tags":["a","b"]}`, "-i")

	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	}
	got := <-created
	delete(got, "requestId")
	if want := `{"metadata":{"orderId":42,"tags":["a","b"]},"prompt":"예약 확인","to":"+821012345678"}`; jsonOf(got) != want {
		t.Fatalf("createCall data = %s, want %s", jsonOf(got), want)
	}
	if n := len(r.lines); n != 3 || r.lines[0]["type"] != "call.created" || r.lines[1]["type"] != "call.completed" {
		t.Fatalf("stdout lines = %v", r.lines)
	}
	if res := r.last(t); res["ok"] != true || jsonOf(res["data"]) != `{"callId":"call-1","status":"completed"}` {
		t.Fatalf("result = %v", res)
	}
}

func TestCreateMetadataFromFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.json")
	if err := os.WriteFile(path, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	created := make(chan map[string]any, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		created <- completeCall(c, "call-1")
	})
	r := run(t, gw.URL, "", true, "create", "--to", "+82", "--metadata", "@"+path, "-i")
	if r.exit != 0 {
		t.Fatalf("exit %d stderr:\n%s", r.exit, r.stderr)
	}
	if got := jsonOf((<-created)["metadata"]); got != `{"from":"file"}` {
		t.Fatalf("metadata = %s", got)
	}
}

func TestCreateWithHandlerAnswersTurns(t *testing.T) {
	t.Parallel()
	answers := make(chan map[string]any, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.Event("user.turn", "call-1", map[string]any{"turnIndex": 0, "text": "hi"})
		answers <- c.ReadEvent("answer")
		c.Event("call.completed", "call-1", map[string]any{"status": "completed"})
		c.WaitClosed()
	})
	argv := append([]string{"create", "--to", "+82", "--prompt", "p", "--"}, handlertest.Command(t, handlertest.Answerer)...)
	r := run(t, gw.URL, "", true, argv...)
	if r.exit != 0 {
		t.Fatalf("exit %d stderr:\n%s", r.exit, r.stderr)
	}
	if text := (<-answers)["text"]; text != "seen=cli.start,call.created,user.turn to=+82 prompt=p index=- metadata={}" {
		t.Fatalf("answer text = %v", text)
	}
}

func TestCreateHandlerStartFailurePlacesNoCall(t *testing.T) {
	t.Parallel()
	var connections atomic.Int32
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		connections.Add(1)
		c.WaitClosed()
	})
	r := run(t, gw.URL, "", true, "create", "--to", "+82", "--", "/nonexistent/tello-handler")
	r.failed(t, 9, "handlerStartFailed")
	if connections.Load() != 0 {
		t.Fatal("gateway was contacted although the handler failed to start")
	}
}

func TestCreateUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"missing --to":           {"create", "-i"},
		"no answer source":       {"create", "--to", "+82"},
		"both answer sources":    {"create", "--to", "+82", "-i", "--", "bot"},
		"args before --":         {"create", "--to", "+82", "stray", "--", "bot"},
		"args without --":        {"create", "--to", "+82", "bot"},
		"empty handler":          {"create", "--to", "+82", "--"},
		"metadata not object":    {"create", "--to", "+82", "-i", "--metadata", `[1]`},
		"metadata invalid":       {"create", "--to", "+82", "-i", "--metadata", `{`},
		"metadata trailing data": {"create", "--to", "+82", "-i", "--metadata", `{} {}`},
		"metadata missing file":  {"create", "--to", "+82", "-i", "--metadata", "@/nonexistent/meta.json"},
		"negative timeout":       {"create", "--to", "+82", "-i", "--timeout", "-1s"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := run(t, "ws://127.0.0.1:1/sdk", "", true, args...)
			r.failed(t, 2, "usage")
		})
	}
}

func writeTasks(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBatchRetriesTemporaryRefusal(t *testing.T) {
	t.Parallel()
	var conns atomic.Int32
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		if conns.Add(1) == 1 {
			data := c.ReadEvent("createCall")
			c.Error("concurrentLimitExceeded", "busy", data["requestId"].(string))
			c.WaitClosed()
			return
		}
		completeCall(c, "call-1")
	})
	path := writeTasks(t, `[{"to":"+8201","prompt":"p","metadata":{"k":1}}]`)
	argv := append([]string{"batch", "--tasks", path, "--retry-delay", "1ms", "--"}, handlertest.Command(t, handlertest.Answerer)...)
	r := run(t, gw.URL, "", true, argv...)

	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	}
	retries := r.linesOfType("cli.retry")
	if len(retries) != 1 || jsonOf(retries[0]) != `{"attempt":2,"code":"concurrentLimitExceeded","delayMs":1,"index":0,"type":"cli.retry"}` {
		t.Fatalf("cli.retry lines = %v", retries)
	}
	tasks := r.linesOfType("cli.task")
	if len(tasks) != 1 || jsonOf(tasks[0]) != `{"attempts":2,"callId":"call-1","index":0,"ok":true,"status":"completed","to":"+8201","type":"cli.task"}` {
		t.Fatalf("cli.task lines = %v", tasks)
	}
	if got := jsonOf(r.last(t)["data"]); got != `{"completed":1,"failed":0,"skipped":0,"total":1}` {
		t.Fatalf("result data = %s", got)
	}
}

func TestBatchAbortsOnInsufficientCredit(t *testing.T) {
	t.Parallel()
	var conns atomic.Int32
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		if conns.Add(1) == 1 {
			completeCall(c, "call-1")
			return
		}
		data := c.ReadEvent("createCall")
		c.Error("insufficientCredit", "no credit", data["requestId"].(string))
		c.WaitClosed()
	})
	path := writeTasks(t, `[{"to":"+8201"},{"to":"+8202"},{"to":"+8203"},{"to":"+8204"}]`)
	r := run(t, gw.URL, "", true, "batch", "--tasks", path, "-i")

	data := r.failed(t, 4, "insufficientCredit")
	if jsonOf(data) != `{"completed":1,"failed":1,"skipped":2,"total":4}` {
		t.Fatalf("data = %v", data)
	}
	tasks := r.linesOfType("cli.task")
	if len(tasks) != 2 {
		t.Fatalf("cli.task lines = %v", tasks)
	}
	errObj, _ := tasks[1]["error"].(map[string]any)
	if tasks[1]["ok"] != false || tasks[1]["attempts"] != float64(1) || errObj["code"] != "insufficientCredit" || errObj["exitCode"] != float64(4) {
		t.Fatalf("failed task line = %v", tasks[1])
	}
	if conns.Load() != 2 {
		t.Fatalf("gateway saw %d connections, want 2", conns.Load())
	}
}

func TestBatchContinuesAfterCallFailure(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		data := c.ReadEvent("createCall")
		if data["to"] == "+8201" {
			c.Event("call.created", "call-1", nil)
			c.Event("call.noAnswer", "call-1", map[string]any{"status": "noAnswer"})
			c.WaitClosed()
			return
		}
		c.Event("call.created", "call-2", nil)
		c.Event("call.completed", "call-2", map[string]any{"status": "completed"})
		c.WaitClosed()
	})
	path := writeTasks(t, `[{"to":"+8201"},{"to":"+8202"}]`)
	r := run(t, gw.URL, "", true, "batch", "--tasks", path, "-i")

	data := r.failed(t, 6, "batchIncomplete")
	if jsonOf(data) != `{"completed":1,"failed":1,"skipped":0,"total":2}` {
		t.Fatalf("data = %v", data)
	}
	tasks := r.linesOfType("cli.task")
	if len(tasks) != 2 || tasks[0]["callId"] != "call-1" || tasks[0]["status"] != "noAnswer" || tasks[1]["ok"] != true {
		t.Fatalf("cli.task lines = %v", tasks)
	}
}

func TestBatchTasksFromStdinAndHandlerSeesIndex(t *testing.T) {
	t.Parallel()
	answers := make(chan any, 2)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-x", nil)
		c.Event("user.turn", "call-x", map[string]any{"turnIndex": 0, "text": "hi"})
		answers <- c.ReadEvent("answer")["text"]
		c.Event("call.completed", "call-x", map[string]any{"status": "completed"})
		c.WaitClosed()
	})
	argv := append([]string{"batch", "--tasks", "-", "--"}, handlertest.Command(t, handlertest.Answerer)...)
	r := run(t, gw.URL, `[{"to":"+8201"},{"to":"+8202"}]`, true, argv...)
	if r.exit != 0 {
		t.Fatalf("exit %d stderr:\n%s", r.exit, r.stderr)
	}
	for i, to := range []string{"+8201", "+8202"} {
		want := "seen=cli.start,call.created,user.turn to=" + to + " prompt= index=" + string(rune('0'+i)) + " metadata={}"
		if got := <-answers; got != want {
			t.Fatalf("task %d answer = %v, want %q (fresh handler per task)", i, got, want)
		}
	}
}

func TestBatchUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		stdin string
		args  []string
	}{
		"missing --tasks":      {"", []string{"batch", "-i"}},
		"not an array":         {`{"to":"+82"}`, []string{"batch", "--tasks", "-", "--", "bot"}},
		"empty array":          {`[]`, []string{"batch", "--tasks", "-", "--", "bot"}},
		"task without to":      {`[{"prompt":"p"}]`, []string{"batch", "--tasks", "-", "--", "bot"}},
		"metadata not object":  {`[{"to":"+82","metadata":"x"}]`, []string{"batch", "--tasks", "-", "--", "bot"}},
		"stdin tasks with -i":  {`[{"to":"+82"}]`, []string{"batch", "--tasks", "-", "-i"}},
		"no answer source":     {`[{"to":"+82"}]`, []string{"batch", "--tasks", "-"}},
		"negative max-retries": {`[{"to":"+82"}]`, []string{"batch", "--tasks", "-", "--max-retries", "-1", "--", "bot"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := run(t, "ws://127.0.0.1:1/sdk", tc.stdin, true, tc.args...)
			r.failed(t, 2, "usage")
		})
	}
}
