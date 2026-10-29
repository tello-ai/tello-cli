package call_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/call"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

func TestBatchStopsWhenLiveCallStatusCannotBeDetermined(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	api.Statuses("inProgress")
	api.FailGets(fakeapi.Failure{Status: 503, Code: "voice_gateway_unavailable"}, fakeapi.Failure{Status: 503, Code: "voice_gateway_unavailable"}, fakeapi.Failure{Status: 503, Code: "voice_gateway_unavailable"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r := runCtx(t, ctx, func(a *app.App) { a.APIURL = api.URL }, "", true, "batch", "--tasks", writeTasks(t, `[{"to":"+8201"},{"to":"+8202"}]`), "--agent", "12", "--max-retries", "0", fastPoll)
	if len(api.Creates()) != 1 {
		t.Fatalf("created %d calls; first may still be active", len(api.Creates()))
	}
	data := r.failed(t, 7, "callStatusUnknown")
	if data["id"] != "c_1" {
		t.Fatalf("missing last known call: %v", data)
	}
	if e := r.last(t)["error"].(map[string]any); e["retryable"] != false {
		t.Fatalf("status lookup failure must not be retryable: %v", e)
	}
}

func TestBatchOutcomeUnknownReportsKey(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 502, Code: "call_outcome_unknown"})
	r := runAPI(t, api.URL, "", true, "batch", "--tasks", writeTasks(t, `[{"to":"+8201"},{"to":"+8202"}]`), "--agent", "12", fastPoll)
	data := r.failed(t, 8, "callOutcomeUnknown")
	if data["idempotencyKey"] != api.Creates()[0].IdempotencyKey {
		t.Fatalf("missing retry key in result: %v", data)
	}
}

func TestWaitTimeoutInterruptsBlockedPoll(t *testing.T) {
	var ended atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/calls":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"c_1","status":"dialing"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/calls/c_1/end":
			ended.Store(true)
			w.WriteHeader(http.StatusAccepted)
			io.WriteString(w, `{"id":"c_1","status":"dialing"}`)
		case ended.Load():
			io.WriteString(w, `{"id":"c_1","status":"cancelled"}`)
		default:
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	start := time.Now()
	r := runAPI(t, server.URL+"/v1", "", true, "create", "--agent", "12", "--to", "+82", "--wait", "--timeout", "100ms", fastPoll)
	r.failed(t, 6, "timeout")
	if !ended.Load() || time.Since(start) > 2*time.Second {
		t.Fatalf("termination delayed or not requested: %v", time.Since(start))
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestBatchStopsIfOutputFails(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	var stderr bytes.Buffer
	a := &app.App{
		Stdin:  bytes.NewBufferString(`[{"to":"+8201"},{"to":"+8202"}]`),
		Stdout: failingWriter{}, Stderr: &stderr, JSON: true,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1"}, nil },
		APIURL: api.URL,
	}
	cmd := call.New(a)
	cmd.SetArgs([]string{"batch", "--tasks", "-", "--agent", "12", fastPoll})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected output failure")
	}
	if got := len(api.Creates()); got != 1 {
		t.Fatalf("created %d calls after output failure", got)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("c_1")) {
		t.Fatalf("call ID missing from stderr: %s", stderr.String())
	}
}

// An accepted transfer whose result cannot be written must still leave the
// call ID and the key on stderr.
func TestTransferOutputFailureKeepsKey(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	api.Statuses("inProgress")
	runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82")
	var stderr bytes.Buffer
	a := &app.App{
		Stdout: failingWriter{}, Stderr: &stderr, JSON: true,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1"}, nil },
		APIURL: api.URL,
	}
	cmd := call.New(a)
	cmd.SetArgs([]string{"transfer", "c_1", "--to", "+821099998888", "--idempotency-key", "t-7"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected output failure")
	}
	if len(api.Transfers()) != 1 || !bytes.Contains(stderr.Bytes(), []byte("c_1")) || !bytes.Contains(stderr.Bytes(), []byte("t-7")) {
		t.Fatalf("stderr %q, want the call ID and key", stderr.String())
	}
}

func TestBatchDoesNotRetryAfterRetryEventOutputFails(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 409, Code: "concurrent_limit_exceeded"})
	var stderr bytes.Buffer
	a := &app.App{
		Stdin: bytes.NewBufferString(`[{"to":"+8201"}]`), Stdout: failingWriter{}, Stderr: &stderr, JSON: true,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1"}, nil },
		APIURL: api.URL,
	}
	cmd := call.New(a)
	cmd.SetArgs([]string{"batch", "--tasks", "-", "--agent", "12", "--retry-delay", "0", fastPoll})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected output failure")
	}
	if got := len(api.Creates()); got != 1 {
		t.Fatalf("created %d requests after retry output failed", got)
	}
}

func TestBatchPreservesUnknownOutcomeWhenOutputFails(t *testing.T) {
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 502, Code: "call_outcome_unknown"})
	var stderr bytes.Buffer
	a := &app.App{
		Stdin: bytes.NewBufferString(`[{"to":"+8201"}]`), Stdout: failingWriter{}, Stderr: &stderr, JSON: true,
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1"}, nil },
		APIURL: api.URL,
	}
	cmd := call.New(a)
	cmd.SetArgs([]string{"batch", "--tasks", "-", "--agent", "12", fastPoll})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err != nil {
		a.ReportError(err)
	} else {
		t.Fatal("expected failure")
	}
	key := api.Creates()[0].IdempotencyKey
	if !bytes.Contains(stderr.Bytes(), []byte("callOutcomeUnknown")) || !bytes.Contains(stderr.Bytes(), []byte(key)) {
		t.Fatalf("stderr must preserve the outcome and key: %s", stderr.String())
	}
}
