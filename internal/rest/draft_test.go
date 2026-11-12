package rest_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

func TestCallDraftRequests(t *testing.T) {
	ctx := context.Background()
	var got []string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, fmt.Sprintf("%s %s %s key=%s ct=%q", r.Method, r.URL.Path, body, r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")))
		switch r.URL.Path {
		case "/v1/call-drafts":
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"cd_1","status":"pendingConfirmation"}`)
		case "/v1/call-drafts/cd_1/revise":
			_, _ = io.WriteString(w, `{"id":"cd_2","status":"pendingConfirmation"}`)
		default:
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"c_9","status":"dialing"}`)
		}
	})
	draft := rest.CallDraft{AgentID: "12", To: "+82", Variables: map[string]any{"a": "1"}, PerCall: rest.PerCall{Instructions: "x", NotifyOnEnd: true}}
	if _, err := c.CreateCallDraft(ctx, draft, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReviseCallDraft(ctx, "cd_1", "y", "k2"); err != nil {
		t.Fatal(err)
	}
	if call, err := c.ConfirmCallDraft(ctx, "cd_2", "k3"); err != nil || call["id"] != "c_9" {
		t.Fatalf("call %v err %v", call, err)
	}
	want := []string{
		`POST /v1/call-drafts {"agentId":"12","to":"+82","variables":{"a":"1"},"instructions":"x","notifyOnEnd":true} key=k1 ct="application/json"`,
		`POST /v1/call-drafts/cd_1/revise {"request":"y"} key=k2 ct="application/json"`,
		`POST /v1/call-drafts/cd_2/confirm  key=k3 ct=""`,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("requests:\n%q\nwant:\n%q", got, want)
	}
}

// confirm answers the placed call; anything else must not pass for one.
func TestConfirmCallDraftValidatesCall(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		valid  bool
	}{
		{201, `{"id":"c_9","status":"dialing"}`, true},
		{201, `{"id":"cd_9","status":"pendingConfirmation"}`, false},
		{201, `{}`, false},
	} {
		c := client(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, tt.body)
		})
		_, err := c.ConfirmCallDraft(context.Background(), "cd_1", "k")
		if tt.valid != (err == nil) || (!tt.valid && app.Classify(err).Code != "invalidResponse") {
			t.Fatalf("%s: err %v", tt.body, err)
		}
	}
}

func TestCallDraftResponsesNeedStatus(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		valid  bool
	}{
		{200, `{"id":"cd_2","status":"pendingConfirmation"}`, true},
		{200, `{"id":null,"status":"needsClarification"}`, true},
		{201, `{"id":"cd_2","status":"pendingConfirmation"}`, false},
		{200, `{"id":"cd_2"}`, false},
	} {
		c := client(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, tt.body)
		})
		_, err := c.ReviseCallDraft(context.Background(), "cd_1", "x", "k")
		if tt.valid != (err == nil) {
			t.Fatalf("%d %s: err %v", tt.status, tt.body, err)
		}
	}
}

// Creating a draft can place a call at once (a ready scenario), so an
// undocumented 503 or a lost response leaves the call outcome unknown.
func TestCreateCallDraftOutcomeUnknown(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "unavailable")
	})
	_, err := c.CreateCallDraft(context.Background(), rest.CallDraft{AgentID: "12", To: "+82"}, "k")
	if e := app.Classify(err); e.Code != "callOutcomeUnknown" || e.ExitCode() != 8 {
		t.Fatalf("code %s exit %d", e.Code, e.ExitCode())
	}
}
