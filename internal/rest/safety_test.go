package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

func TestRejectsAPIURLWithCredentialsOrQuery(t *testing.T) {
	for _, u := range []string{"https://name:secret@example.com/v1", "https://example.com/v1?token=secret", "https://example.com/v1#secret", "https://example.com/v1#"} {
		_, err := rest.New(newApp(u))
		if err == nil || app.Classify(err).Code != "invalidUrl" || strings.Contains(err.Error(), "secret") {
			t.Errorf("URL %q: error %v", u, err)
		}
	}
}

func TestCreateDoesNotFollowRedirect(t *testing.T) {
	var received atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(http.StatusCreated) }))
	defer dest.Close()
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	})
	_, err := c.CreateCall(context.Background(), rest.CreateCall{AgentID: "12", To: "+82"}, "k")
	if err == nil || received.Load() != 0 {
		t.Fatalf("redirect followed: requests %d error %v", received.Load(), err)
	}
}

func TestRejectsMalformedOrMismatchedCalls(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"id":"c_2","status":"completed"}`))
	})
	_, err := c.CreateCall(context.Background(), rest.CreateCall{AgentID: "12", To: "+82"}, "k")
	if err == nil || app.Classify(err).Code != "invalidResponse" {
		t.Fatalf("malformed create was accepted: %v", err)
	}
	_, err = c.GetCall(context.Background(), "c_1")
	if err == nil || app.Classify(err).Code != "invalidResponse" {
		t.Fatalf("mismatched call was accepted: %v", err)
	}
}
