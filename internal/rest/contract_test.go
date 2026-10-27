package rest_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

func TestCreateServiceUnavailableClassification(t *testing.T) {
	for _, tt := range []struct {
		name, body, code string
		retryable        bool
	}{
		{"proxy", "unavailable", "callOutcomeUnknown", false},
		{"unknown code", `{"error":"new_outage"}`, "callOutcomeUnknown", false},
		{"known pre-dial", `{"error":"voice_gateway_unavailable"}`, "voiceGatewayUnavailable", true},
		{"known outcome", `{"error":"call_outcome_unknown"}`, "callOutcomeUnknown", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Idempotency-Key") != "same-key" {
					t.Errorf("missing idempotency key")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.CreateCall(context.Background(), rest.CreateCall{AgentID: "1", To: "+82"}, "same-key")
			if err == nil {
				t.Fatal("expected error")
			}
			e := app.Classify(err)
			if e.Code != tt.code || e.Retryable() != tt.retryable {
				t.Fatalf("code=%s retryable=%v", e.Code, e.Retryable())
			}
		})
	}
}

func TestAgentsRequiresDataArray(t *testing.T) {
	for _, tt := range []struct {
		body  string
		valid bool
	}{
		{`{"data":[]}`, true}, {`{"data":[{"id":"1"}]}`, true},
		{`{}`, false}, {`{"data":null}`, false}, {`{"data":{}}`, false}, {`{"data":"x"}`, false},
	} {
		t.Run(tt.body, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tt.body) })
			_, err := c.ListAgents(context.Background())
			if tt.valid && err != nil {
				t.Fatal(err)
			}
			if !tt.valid && (err == nil || app.Classify(err).Code != "invalidResponse") {
				t.Fatalf("expected invalidResponse, got %v", err)
			}
		})
	}
}

func TestEndValidatesCall(t *testing.T) {
	for _, tt := range []struct {
		body  string
		valid bool
	}{
		{`{"id":"c_1","status":"inProgress"}`, true}, {`{}`, false},
		{`{"id":"c_2","status":"completed"}`, false}, {`{"id":"c_1","status":"unknown"}`, false},
	} {
		t.Run(tt.body, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.EndCall(context.Background(), "c_1")
			if tt.valid && err != nil {
				t.Fatal(err)
			}
			if !tt.valid && (err == nil || app.Classify(err).Code != "invalidResponse") {
				t.Fatalf("expected invalidResponse, got %v", err)
			}
		})
	}
}

func TestAuthenticationAndEndRejectUnexpectedSuccessStatus(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"c_1","status":"inProgress"}`)
	})
	if _, err := c.ListAgents(context.Background()); err == nil || app.Classify(err).Code != "invalidResponse" {
		t.Fatalf("unexpected agent status accepted: %v", err)
	}
	if _, err := c.EndCall(context.Background(), "c_1"); err == nil || app.Classify(err).Code != "invalidResponse" {
		t.Fatalf("unexpected end status accepted: %v", err)
	}
}

func TestAPIURLTransportPolicy(t *testing.T) {
	for _, tt := range []struct {
		url   string
		valid bool
	}{
		{"http://127.0.0.1:123/v1", true}, {"http://127.1.2.3/v1", true}, {"http://[::1]:123/v1", true},
		{"http://localhost:123/v1", true}, {"http://LOCALHOST/v1", true}, {"https://example.com/v1", true},
		{"http://example.com/v1", false}, {"http://localhost.example.com/v1", false}, {"http://192.168.1.1/v1", false},
		{"http://[::2]/v1", false}, {"http://secret.example/v1", false},
	} {
		t.Run(tt.url, func(t *testing.T) {
			_, err := rest.New(newApp(tt.url))
			if tt.valid && err != nil {
				t.Fatal(err)
			}
			if !tt.valid && (err == nil || app.Classify(err).Code != "invalidUrl" || strings.Contains(err.Error(), tt.url)) {
				t.Fatalf("invalid URL error: %v", err)
			}
		})
	}
}

func TestCreateRespectsContextDeadline(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.CreateCall(ctx, rest.CreateCall{AgentID: "1", To: "+82"}, "key")
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && app.Classify(err).Code != "connectionFailed") {
		t.Fatalf("deadline error: %v", err)
	}
}

func TestConnectionErrorDoesNotExposeURL(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := c.GetCall(ctx, "c_1")
	if err == nil || strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("URL leaked: %v", err)
	}
}

func TestCanceledRequestDoesNotExposeURL(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetCall(ctx, "c_1")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("canceled request error: %v", err)
	}
}

func TestCreateConnectionFailureWarnsOfUnknownOutcome(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, err := rest.New(newApp(srv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateCall(context.Background(), rest.CreateCall{AgentID: "1"}, "key")
	if err == nil {
		t.Fatal("expected connection failure")
	}
	e := app.Classify(err)
	if e.Code != "connectionFailed" || e.Retryable() || !strings.Contains(e.Message, "outcome may be unknown") {
		t.Fatalf("unexpected create error: %+v", e)
	}
}
