package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/fakegateway"
	"github.com/tello-ai/tello-go/tello"
)

func TestClassifyMapsErrorsToKindAndExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		kind     Kind
		code     string
		exitCode int
	}{
		{"gateway auth", &tello.AuthenticationError{TelloError: tello.TelloError{Code: "unauthenticated", Message: "Authentication required"}}, KindAuth, "unauthenticated", 3},
		{"auth timeout without code", &tello.AuthenticationError{TelloError: tello.TelloError{Message: "timed out waiting for authentication"}}, KindAuth, "unauthenticated", 3},
		{"account refusal", tello.ErrorFor("insufficientCredit", "no credit", ""), KindRefused, "insufficientCredit", 4},
		{"caller not verified", tello.ErrorFor("callerNotVerified", "not verified", ""), KindRefused, "callerNotVerified", 4},
		{"concurrent limit is temporary", tello.ErrorFor("concurrentLimitExceeded", "busy", ""), KindTemporary, "concurrentLimitExceeded", 75},
		{"provider draining is temporary", tello.ErrorFor("callProviderDraining", "draining", ""), KindTemporary, "callProviderDraining", 75},
		{"provider unavailable is temporary", tello.ErrorFor("callProviderUnavailable", "unavailable", ""), KindTemporary, "callProviderUnavailable", 75},
		{"call already active is temporary", tello.ErrorFor("callAlreadyActive", "active", ""), KindTemporary, "callAlreadyActive", 75},
		{"provider unauthorized is permanent", tello.ErrorFor("callProviderUnauthorized", "creds", ""), KindServer, "callProviderUnauthorized", 8},
		{"setup failed is permanent", tello.ErrorFor("callSetupFailed", "declined", ""), KindServer, "callSetupFailed", 8},
		{"internal error", tello.ErrorFor("internalError", "boom", ""), KindServer, "internalError", 8},
		{"validation", tello.ErrorFor("toRequired", "to is required", ""), KindInvalid, "toRequired", 5},
		{"call rejected", tello.ErrorFor("callRejected", "rejected", "why?"), KindInvalid, "callRejected", 5},
		{"no active call", tello.ErrorFor("noActiveCall", "none", ""), KindInvalid, "noActiveCall", 5},
		{"connection closed", &tello.ConnectionClosedError{TelloError: tello.TelloError{Message: "closed"}}, KindConnection, "connectionClosed", 7},
		{"session replaced", &tello.SessionReplacedError{TelloError: tello.TelloError{Message: "replaced"}}, KindConnection, "sessionReplaced", 7},
		{"interrupted", context.Canceled, KindInterrupted, "interrupted", 130},
		{"wrapped app error keeps identity", fmt.Errorf("outer: %w", NewError(KindHandler, "handlerExited", "handler exited")), KindHandler, "handlerExited", 9},
		{"usage", Usagef("missing --to"), KindUsage, "usage", 2},
		{"unknown", errors.New("boom"), KindInternal, "internal", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			if got.Kind != tt.kind || got.Code != tt.code || got.ExitCode() != tt.exitCode {
				t.Fatalf("Classify() = kind %q code %q exit %d, want kind %q code %q exit %d",
					got.Kind, got.Code, got.ExitCode(), tt.kind, tt.code, tt.exitCode)
			}
		})
	}
}

func TestClassifyKeepsQuestionOfRejectedCall(t *testing.T) {
	got := Classify(tello.ErrorFor("callRejected", "Call rejected", "Which branch?"))
	if got.Question != "Which branch?" {
		t.Fatalf("Question = %q, want %q", got.Question, "Which branch?")
	}
}

func newTestApp(json bool) (*App, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &App{
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(string) string { return "" },
		JSON:   json,
	}, &stdout, &stderr
}

func decodeLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("stdout line is not JSON: %q: %v", line, err)
		}
		lines = append(lines, v)
	}
	return lines
}

func TestReportErrorJSONWritesSingleResultLine(t *testing.T) {
	a, stdout, stderr := newTestApp(true)
	err := NewError(KindTemporary, "concurrentLimitExceeded", "all lines busy")
	err.Data = map[string]any{"callId": "call-1"}

	exit := a.ReportError(err)

	if exit != 75 {
		t.Fatalf("exit = %d, want 75", exit)
	}
	lines := decodeLines(t, stdout.String())
	if len(lines) != 1 {
		t.Fatalf("want exactly one stdout line, got %d: %q", len(lines), stdout.String())
	}
	got := lines[0]
	if got["type"] != "cli.result" || got["ok"] != false {
		t.Fatalf("unexpected envelope: %v", got)
	}
	errObj := got["error"].(map[string]any)
	if errObj["kind"] != "temporary" || errObj["code"] != "concurrentLimitExceeded" ||
		errObj["retryable"] != true || errObj["exitCode"] != float64(75) || errObj["message"] != "all lines busy" {
		t.Fatalf("unexpected error object: %v", errObj)
	}
	if _, ok := errObj["question"]; ok {
		t.Fatalf("question must be omitted when empty: %v", errObj)
	}
	if data := got["data"].(map[string]any); data["callId"] != "call-1" {
		t.Fatalf("data not carried: %v", got["data"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("JSON mode must keep stderr empty for errors, got %q", stderr.String())
	}
}

func TestReportErrorHumanWritesMessageAndHintToStderr(t *testing.T) {
	a, stdout, stderr := newTestApp(false)

	exit := a.ReportError(NewError(KindAuth, "apiKeyMissing", "no API key configured"))

	if exit != 3 {
		t.Fatalf("exit = %d, want 3", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("human errors must not go to stdout, got %q", stdout.String())
	}
	out := stderr.String()
	if !strings.Contains(out, "no API key configured") || !strings.Contains(out, "apiKeyMissing") || !strings.Contains(out, "tello auth login") {
		t.Fatalf("stderr missing message, code or hint: %q", out)
	}
}

func TestWriteResultAndEventsKeepUnicodeUnescaped(t *testing.T) {
	a, stdout, _ := newTestApp(true)

	if err := a.WriteEvent(tello.Event{Raw: map[string]any{"type": "user.turn", "text": "예약 <변경>"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteResult(map[string]any{"status": "completed"}); err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	if !strings.Contains(out, `"text":"예약 <변경>"`) {
		t.Fatalf("event line must keep UTF-8 and <> unescaped: %q", out)
	}
	lines := decodeLines(t, out)
	if len(lines) != 2 || lines[1]["type"] != "cli.result" || lines[1]["ok"] != true {
		t.Fatalf("unexpected lines: %v", lines)
	}
}

func TestWriteResultIsSilentInHumanMode(t *testing.T) {
	a, stdout, _ := newTestApp(false)
	if err := a.WriteResult(map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("human mode must not print the JSON result, got %q", stdout.String())
	}
}

func TestEffectiveURLPrecedence(t *testing.T) {
	env := map[string]string{}
	a := &App{Getenv: func(k string) string { return env[k] }}

	if got := a.EffectiveURL(); got != tello.DefaultURL {
		t.Fatalf("default = %q, want %q", got, tello.DefaultURL)
	}
	env[tello.EnvURL] = "ws://env.example/sdk"
	if got := a.EffectiveURL(); got != "ws://env.example/sdk" {
		t.Fatalf("env = %q", got)
	}
	a.URL = "ws://flag.example/sdk"
	if got := a.EffectiveURL(); got != "ws://flag.example/sdk" {
		t.Fatalf("flag = %q", got)
	}
}

func TestNewClientWithoutKeyIsAuthError(t *testing.T) {
	a := &App{
		Getenv: func(string) string { return "" },
		APIKey: func() (Credential, error) { return Credential{}, ErrNoCredential },
	}
	_, _, err := a.NewClient()
	got := Classify(err)
	if got.Kind != KindAuth || got.Code != "apiKeyMissing" {
		t.Fatalf("Classify(NewClient err) = %q/%q, want auth/apiKeyMissing", got.Kind, got.Code)
	}
}

func TestConnectFailureIsConnectionError(t *testing.T) {
	// A plain HTTP server refuses the WebSocket upgrade immediately.
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	a := &App{
		Getenv: func(string) string { return "" },
		URL:    "ws" + strings.TrimPrefix(server.URL, "http") + "/sdk",
		APIKey: func() (Credential, error) { return Credential{Key: "k", Source: "env"}, nil },
	}
	client, _, err := a.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	err = a.Connect(context.Background(), client)
	got := Classify(err)
	if got.Kind != KindConnection || got.Code != "connectionFailed" {
		t.Fatalf("Classify(Connect err) = %q/%q, want connection/connectionFailed", got.Kind, got.Code)
	}
}

func TestNewClientRejectsNonWebSocketURL(t *testing.T) {
	for _, raw := range []string{"https://api.telloai.io/sdk", "api.telloai.io/sdk", "wss://"} {
		a := &App{
			Getenv: func(string) string { return "" },
			URL:    raw,
			APIKey: func() (Credential, error) { return Credential{Key: "k", Source: "env"}, nil },
		}
		_, _, err := a.NewClient()
		got := Classify(err)
		if got.Kind != KindUsage || got.Code != "invalidUrl" || got.ExitCode() != 2 {
			t.Fatalf("%q: Classify(NewClient err) = %q/%q, want usage/invalidUrl", raw, got.Kind, got.Code)
		}
	}
}

func TestConnectionDroppedDuringAuthIsConnectionError(t *testing.T) {
	// A gateway restarting mid-handshake closes with 1001 instead of
	// answering auth; that is not a rejected key.
	gw := fakegateway.New(t, func(c *fakegateway.Conn) {
		c.ReadEvent("auth")
		c.CloseWith(1001, "going away")
	})
	a := &App{
		Getenv: func(string) string { return "" },
		URL:    gw.URL,
		APIKey: func() (Credential, error) { return Credential{Key: "key-1", Source: "env"}, nil },
	}
	client, _, err := a.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	got := Classify(a.Connect(context.Background(), client))
	if got.Kind != KindConnection || got.Code != "connectionFailed" {
		t.Fatalf("Classify(Connect err) = %q/%q, want connection/connectionFailed", got.Kind, got.Code)
	}
}

func TestRejectedKeyStaysAuthError(t *testing.T) {
	gw := fakegateway.New(t, func(c *fakegateway.Conn) { c.Authenticate("other-key") })
	a := &App{
		Getenv: func(string) string { return "" },
		URL:    gw.URL,
		APIKey: func() (Credential, error) { return Credential{Key: "key-1", Source: "env"}, nil },
	}
	client, _, err := a.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	got := Classify(a.Connect(context.Background(), client))
	if got.Kind != KindAuth || got.Code != "unauthenticated" {
		t.Fatalf("Classify(Connect err) = %q/%q, want auth/unauthenticated", got.Kind, got.Code)
	}
}
