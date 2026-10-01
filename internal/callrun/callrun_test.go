package callrun_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/callrun"
	"github.com/tello-ai/tello-cli/internal/fakegateway"
	"github.com/tello-ai/tello-cli/internal/handler/handlertest"
)

func TestMain(m *testing.M) { handlertest.Main(m) }

type harness struct {
	a      *app.App
	stdout *bytes.Buffer
	stderr *handlertest.SyncBuffer
}

func newHarness(t *testing.T, gw *fakegateway.Server, stdin string, json bool) *harness {
	t.Helper()
	h := &harness{stdout: &bytes.Buffer{}, stderr: &handlertest.SyncBuffer{}}
	h.a = &app.App{
		Stdin:  strings.NewReader(stdin),
		Stdout: h.stdout,
		Stderr: h.stderr,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1", Source: "env"}, nil },
		URL:    gw.URL,
		JSON:   json,
	}
	return h
}

// run executes one call and reports like the root command: the returned
// exit code and, in JSON mode, the cli.result line for failures.
func (h *harness) run(t *testing.T, ctx context.Context, opts callrun.Options) (callrun.Result, int) {
	t.Helper()
	done := make(chan struct{})
	var (
		res  callrun.Result
		err  error
		exit int
	)
	go func() {
		defer close(done)
		res, err = callrun.Run(ctx, h.a, opts)
		if err != nil {
			exit = h.a.ReportError(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("callrun.Run did not return")
	}
	return res, exit
}

func (h *harness) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		out = append(out, v)
	}
	return out
}

func types(lines []map[string]any) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l["type"].(string))
	}
	return out
}

func failure(t *testing.T, lines []map[string]any) (errObj, data map[string]any) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("no stdout lines")
	}
	last := lines[len(lines)-1]
	if last["type"] != "cli.result" || last["ok"] != false {
		t.Fatalf("last line = %v, want failed cli.result", last)
	}
	errObj, _ = last["error"].(map[string]any)
	data, _ = last["data"].(map[string]any)
	return errObj, data
}

func assertFailure(t *testing.T, h *harness, exit, wantExit int, wantCode string) map[string]any {
	t.Helper()
	if exit != wantExit {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", exit, wantExit, h.stdout, h.stderr)
	}
	errObj, data := failure(t, h.lines(t))
	if errObj["code"] != wantCode {
		t.Fatalf("error = %v, want code %s", errObj, wantCode)
	}
	return data
}

func equal(a, b []string) bool {
	return strings.Join(a, " ") == strings.Join(b, " ")
}

func TestHandlerGetsStartAndEventsAndItsAnswerReachesGateway(t *testing.T) {
	t.Parallel()
	answers := make(chan map[string]any, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.Event("user.turn", "call-1", map[string]any{"turnIndex": 0, "text": "여보세요"})
		answers <- c.ReadEvent("answer")
		c.Event("call.completed", "call-1", map[string]any{"status": "completed"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	index := 3
	res, exit := h.run(t, context.Background(), callrun.Options{
		To: "+821012345678", Prompt: "예약 확인", Metadata: map[string]any{"k": "v"},
		Handler: handlertest.Command(t, handlertest.Answerer), Index: &index,
	})
	if exit != 0 || res != (callrun.Result{CallID: "call-1", Status: "completed"}) {
		t.Fatalf("Run = %+v exit %d, stderr:\n%s", res, exit, h.stderr)
	}
	answer := <-answers
	wantText := `seen=cli.start,call.created,user.turn to=+821012345678 prompt=예약 확인 index=3 metadata={"k":"v"}`
	if answer["text"] != wantText || answer["messageId"] != "m-0" || answer["requestId"] != "r-0" {
		t.Fatalf("answer frame = %v, want text %q messageId m-0 requestId r-0", answer, wantText)
	}
	if got := types(h.lines(t)); !equal(got, []string{"call.created", "user.turn", "call.completed"}) {
		t.Fatalf("stdout event types = %v", got)
	}
}

func TestNoAnswerFailsWithCallIDAndStatus(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.Event("call.noAnswer", "call-1", map[string]any{"status": "noAnswer", "failureReason": "busy"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	data := assertFailure(t, h, exit, 6, "noAnswer")
	if data["callId"] != "call-1" || data["status"] != "noAnswer" {
		t.Fatalf("data = %v, want callId call-1 status noAnswer", data)
	}
	if got := types(h.lines(t)); !equal(got, []string{"call.created", "call.noAnswer", "cli.result"}) {
		t.Fatalf("stdout types = %v", got)
	}
}

func TestRefusedCreateCallFailsWithGatewayCode(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		data := c.ReadEvent("createCall")
		c.Error("insufficientCredit", "The account has no call credit remaining", data["requestId"].(string))
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	data := assertFailure(t, h, exit, 4, "insufficientCredit")
	if data != nil {
		t.Fatalf("refused call has no callId, data = %v", data)
	}
	if got := types(h.lines(t)); !equal(got, []string{"error", "cli.result"}) {
		t.Fatalf("stdout types = %v (the error frame is an event line)", got)
	}
}

func TestInterruptCancelsCallAndExits130(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan bool, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		cancel() // first Ctrl-C
		cancelled <- c.ReadEvent("cancel") != nil
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "queued"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, ctx, callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	if !<-cancelled {
		t.Fatal("gateway did not receive cancel")
	}
	data := assertFailure(t, h, exit, 130, "interrupted")
	if data["callId"] != "call-1" || data["status"] != "cancelled" {
		t.Fatalf("data = %v", data)
	}
	if got := types(h.lines(t)); !equal(got, []string{"call.created", "call.statusChanged", "cli.result"}) {
		t.Fatalf("stdout types = %v (terminal event must be written before the result)", got)
	}
}

func TestTimeoutCancelsCall(t *testing.T) {
	t.Parallel()
	cancelled := make(chan bool, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		cancelled <- c.ReadEvent("cancel") != nil
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "queued"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin), Timeout: 200 * time.Millisecond})
	if !<-cancelled {
		t.Fatal("gateway did not receive cancel")
	}
	assertFailure(t, h, exit, 6, "timeout")
}

func TestGatewayCancelIsCancelledOutcome(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "queued"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	assertFailure(t, h, exit, 6, "cancelled")
}

func TestHandlerExitMidCallCancelsAndExits9(t *testing.T) {
	t.Parallel()
	cancelled := make(chan bool, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		cancelled <- c.ReadEvent("cancel") != nil
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "queued"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Handler: handlertest.Command(t, handlertest.ExitOnCreated)})
	if !<-cancelled {
		t.Fatal("gateway did not receive cancel")
	}
	data := assertFailure(t, h, exit, 9, "handlerExited")
	if data["callId"] != "call-1" {
		t.Fatalf("data = %v", data)
	}
}

func TestHandlerStartFailurePlacesNoCall(t *testing.T) {
	t.Parallel()
	var connections atomic.Int32
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		connections.Add(1)
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Handler: []string{"/nonexistent/tello-handler"}})
	assertFailure(t, h, exit, 9, "handlerStartFailed")
	if n := connections.Load(); n != 0 {
		t.Fatalf("gateway saw %d connections, want none before the handler runs", n)
	}
}

func TestInteractiveLinesBecomeCommands(t *testing.T) {
	t.Parallel()
	frames := make(chan map[string]any, 3)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		for range 3 {
			frames <- c.Read()
		}
		c.Event("call.completed", "call-1", map[string]any{"status": "completed"})
		c.WaitClosed()
	})
	stdin := "\n/help\n/dtmf 12#\n네, 맞아요\n/cancel\n"
	h := newHarness(t, gw, stdin, true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, h.stderr)
	}
	want := []string{
		`{"data":{"digits":"12#"},"event":"sendDtmf"}`,
		`{"data":{"text":"네, 맞아요"},"event":"answer"}`,
		`{"data":{},"event":"cancel"}`,
	}
	for i, w := range want {
		got, _ := json.Marshal(<-frames)
		if string(got) != w {
			t.Fatalf("frame %d = %s, want %s", i, got, w)
		}
	}
	if !strings.Contains(h.stderr.String(), "/dtmf") {
		t.Fatalf("/help should print the commands to stderr, got:\n%s", h.stderr)
	}
}

func TestConnectionDropMidCallIsConnectionError(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.Drop()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	data := assertFailure(t, h, exit, 7, "connectionClosed")
	if data["callId"] != "call-1" || data["status"] != "queued" {
		t.Fatalf("data = %v", data)
	}
	for _, l := range h.lines(t) {
		if l["type"] == "disconnected" {
			t.Fatal("SDK-synthetic disconnected must not be written")
		}
	}
}

func TestHumanModeRendersConversation(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "inProgress", "previousStatus": "queued"})
		c.Event("user.turn", "call-1", map[string]any{"turnIndex": 0, "text": "여보세요"})
		c.Event("agent.turn", "call-1", map[string]any{"turnIndex": 1, "text": "안녕하세요"})
		c.Event("call.completed", "call-1", map[string]any{"status": "completed"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", false)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, h.stderr)
	}
	out := h.stdout.String()
	for _, want := range []string{"call-1", "inProgress", "user> 여보세요\n", "agent> 안녕하세요\n", "completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{") {
		t.Fatalf("human output must not contain JSON:\n%s", out)
	}
}

// The gateway ignores a cancel that arrives before the call exists
// (sdk-ws.v1 section 4.4). A cancel sent in that window is sent again once
// call.created shows the call exists.
func TestCancelBeforeCallCreatedIsRepeated(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := make(chan bool, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		cancel()              // Ctrl-C before the call exists
		c.ReadEvent("cancel") // ignored: no active call yet
		c.Event("call.created", "call-1", nil)
		second <- c.ReadEvent("cancel") != nil
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "queued"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	start := time.Now()
	_, exit := h.run(t, ctx, callrun.Options{To: "+82", Input: callrun.NewInput(h.a.Stdin)})
	if !<-second {
		t.Fatal("cancel was not repeated after call.created")
	}
	assertFailure(t, h, exit, 130, "interrupted")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("run took %v; it should end once the repeated cancel is answered", elapsed)
	}
}

// A live handler that stops reading stdin must not stall the call: the
// timeout still fires and the call is cancelled.
func TestHandlerThatStopsReadingStillTimesOut(t *testing.T) {
	t.Parallel()
	cancelled := make(chan bool, 1)
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		// Far more than a pipe buffer holds.
		c.Event("user.turn", "call-1", map[string]any{"turnIndex": 0, "text": strings.Repeat("말", 200*1024)})
		cancelled <- c.ReadEvent("cancel") != nil
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "inProgress"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{
		To: "+82", Handler: handlertest.Command(t, handlertest.Hang), Timeout: 300 * time.Millisecond,
	})
	if !<-cancelled {
		t.Fatal("gateway did not receive cancel")
	}
	assertFailure(t, h, exit, 6, "timeout")
}

// A handler that hangs up itself (cancel) and then exits ends the call as
// cancelled, not as a handler failure.
func TestHandlerCancelThenExitIsCancelled(t *testing.T) {
	t.Parallel()
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		if !c.Authenticate("key-1") {
			return
		}
		c.ReadEvent("createCall")
		c.Event("call.created", "call-1", nil)
		c.ReadEvent("cancel")
		// Answer late, so the handler's exit is seen before the terminal event.
		time.Sleep(300 * time.Millisecond)
		c.Event("call.statusChanged", "call-1", map[string]any{"status": "cancelled", "previousStatus": "queued"})
		c.WaitClosed()
	})
	h := newHarness(t, gw, "", true)
	_, exit := h.run(t, context.Background(), callrun.Options{To: "+82", Handler: handlertest.Command(t, handlertest.CancelOnCreated)})
	data := assertFailure(t, h, exit, 6, "cancelled")
	if data["callId"] != "call-1" || data["status"] != "cancelled" {
		t.Fatalf("data = %v", data)
	}
}
