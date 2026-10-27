package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

func newApp(apiURL string) *app.App {
	return &app.App{
		Getenv: func(string) string { return "" },
		APIKey: func() (app.Credential, error) { return app.Credential{Key: "key-1", Source: "env"}, nil },
		APIURL: apiURL,
	}
}

func client(t *testing.T, h http.HandlerFunc) *rest.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := rest.New(newApp(srv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func envelope(w http.ResponseWriter, status int, code string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"statusCode": status, "message": "msg " + code, "error": code, "data": data})
}

// The REST error envelope decides the exit code; account refusals must not be
// retried and pre-dial service hiccups must be.
func TestErrorEnvelopeClassification(t *testing.T) {
	tests := []struct {
		status    int
		code      string
		wantCode  string
		wantExit  int
		retryable bool
	}{
		{401, "unauthenticated", "unauthenticated", 3, false},
		{400, "target_required", "targetRequired", 5, false},
		{404, "agent_not_found", "agentNotFound", 5, false},
		{402, "insufficient_credit", "insufficientCredit", 4, false},
		{402, "outbound_limit_exceeded", "outboundLimitExceeded", 4, false},
		{422, "callee_not_verified", "calleeNotVerified", 4, false},
		{422, "caller_number_unavailable", "callerNumberUnavailable", 4, false},
		{409, "concurrent_limit_exceeded", "concurrentLimitExceeded", 75, true},
		{409, "idempotency_in_progress", "idempotencyInProgress", 75, true},
		{409, "idempotency_key_reused", "idempotencyKeyReused", 5, false},
		{409, "call_not_active", "callNotActive", 5, false},
		{503, "voice_gateway_unavailable", "voiceGatewayUnavailable", 75, true},
		{502, "call_outcome_unknown", "callOutcomeUnknown", 8, false},
		{500, "internal_error", "internalError", 8, false},
		// Codes this CLI does not know yet fall back to the HTTP status.
		{503, "brand_new_outage", "brandNewOutage", 75, true},
		{418, "brand_new_rule", "brandNewRule", 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) { envelope(w, tt.status, tt.code, nil) })
			_, err := c.GetCall(context.Background(), "c_1")
			e := app.Classify(err)
			if e.Code != tt.wantCode || e.ExitCode() != tt.wantExit || e.Retryable() != tt.retryable {
				t.Fatalf("got code %s exit %d retryable %v, want %s %d %v",
					e.Code, e.ExitCode(), e.Retryable(), tt.wantCode, tt.wantExit, tt.retryable)
			}
		})
	}
}

func TestMissingVariablesKeepsNames(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		envelope(w, 422, "missing_variables", map[string]any{"missingVariables": []string{"고객명"}})
	})
	_, err := c.CreateCall(context.Background(), rest.CreateCall{CrewID: "34", To: "+821012345678"}, "k-1")
	e := app.Classify(err)
	data, _ := e.Data.(map[string]any)
	names, _ := data["missingVariables"].([]any)
	if e.Code != "missingVariables" || len(names) != 1 || names[0] != "고객명" {
		t.Fatalf("code %s data %v, want missingVariables [고객명]", e.Code, e.Data)
	}
}

// A proxy page instead of the envelope still classifies by HTTP status.
func TestNonEnvelopeErrorUsesStatus(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "<html>bad gateway</html>", http.StatusBadGateway)
	})
	e := app.Classify(func() error { _, err := c.ListAgents(context.Background()); return err }())
	if e.Code != "http502" || e.ExitCode() != 8 {
		t.Fatalf("code %s exit %d, want http502 exit 8", e.Code, e.ExitCode())
	}
}

func TestCreateCallRequest(t *testing.T) {
	var (
		gotAuth, gotIdem, gotType string
		gotBody                   map[string]any
	)
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/calls" {
			t.Errorf("request %s %s, want POST /v1/calls", r.Method, r.URL.Path)
		}
		gotAuth, gotIdem, gotType = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"c_9007199254740993","status":"dialing","durationSeconds":null}`)
	})
	call, err := c.CreateCall(context.Background(), rest.CreateCall{
		AgentID: "12", To: "+821012345678", Variables: map[string]any{"고객명": "홍길동", "인원": json.Number("2")},
	}, "booking-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer key-1" || gotIdem != "booking-1" || gotType != "application/json" {
		t.Fatalf("headers auth=%q idem=%q type=%q", gotAuth, gotIdem, gotType)
	}
	if b, _ := json.Marshal(gotBody); string(b) != `{"agentId":"12","to":"+821012345678","variables":{"고객명":"홍길동","인원":2}}` {
		t.Fatalf("body = %s", b)
	}
	if call["id"] != "c_9007199254740993" || rest.Status(call) != "dialing" || call["durationSeconds"] != nil {
		t.Fatalf("call = %v", call)
	}
}

// `+` in a timezone offset must reach the server as %2B, not a space.
func TestListCallsQueryEncoding(t *testing.T) {
	var raw string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"data":[],"nextCursor":null}`)
	})
	_, err := c.ListCalls(context.Background(), rest.ListCalls{
		Limit: 5, Status: "completed", AgentID: "12", CreatedAfter: "2026-10-20T00:00:00+09:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "agentId=12&createdAfter=2026-10-20T00%3A00%3A00%2B09%3A00&limit=5&status=completed"
	if raw != want {
		t.Fatalf("query = %s, want %s", raw, want)
	}
}

func TestUnreachableServerIsConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, err := rest.New(newApp(srv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListAgents(context.Background())
	if e := app.Classify(err); e.Code != "connectionFailed" || e.ExitCode() != 7 {
		t.Fatalf("code %s exit %d, want connectionFailed exit 7", e.Code, e.ExitCode())
	}
}

func TestNewRejectsNonHTTPURL(t *testing.T) {
	_, err := rest.New(newApp("wss://api.telloai.io/sdk"))
	if e := app.Classify(err); e.Code != "invalidUrl" || e.ExitCode() != 2 {
		t.Fatalf("code %s exit %d, want invalidUrl exit 2", e.Code, e.ExitCode())
	}
}
