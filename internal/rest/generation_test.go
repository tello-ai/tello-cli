package rest_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
)

// A new job is 202; a running or just finished job for the same goal comes
// back with the portal's 200. Anything else is not a started job.
func TestGenerateCrewChecksResponse(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		valid  bool
	}{
		{202, `{"id":"57","status":"running","goal":"g"}`, true},
		{200, `{"id":"57","status":"succeeded","goal":"g"}`, true},
		{201, `{"id":"57","status":"running"}`, false},
		{204, ``, false},
		{202, `{"status":"running"}`, false},
		{202, `{"id":"57"}`, false},
		{202, `[]`, false},
	} {
		t.Run(fmt.Sprint(tt.status, tt.body), func(t *testing.T) {
			var got string
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				got = fmt.Sprintf("%s %s %s %s", r.Method, r.URL.Path, body, r.Header.Get("Idempotency-Key"))
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			job, err := c.GenerateCrew(context.Background(), "치과 예약 확인", "gen-1")
			if got != `POST /v1/crews/generate {"goal":"치과 예약 확인"} gen-1` {
				t.Fatalf("request = %s", got)
			}
			if tt.valid && (err != nil || job["id"] != "57") {
				t.Fatalf("job %v err %v", job, err)
			}
			if !tt.valid && (err == nil || app.Classify(err).Code != "invalidResponse") {
				t.Fatalf("expected invalidResponse, got %v", err)
			}
		})
	}
}

func TestGetCrewGenerationChecksJob(t *testing.T) {
	for _, tt := range []struct {
		body  string
		valid bool
	}{
		{`{"id":"57","status":"running"}`, true},
		{`{"id":"58","status":"running"}`, false},
		{`{"id":"57"}`, false},
	} {
		t.Run(tt.body, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/crew-generations/57" {
					t.Errorf("path = %s", r.URL.Path)
				}
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.GetCrewGeneration(context.Background(), "57")
			if tt.valid != (err == nil) {
				t.Fatalf("err = %v, valid %v", err, tt.valid)
			}
		})
	}
}

func TestGenerationErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		status int
		code   string
		exit   int
	}{
		{409, "crew_generation_in_progress", 75},
		{404, "crew_generation_not_found", 5},
		{400, "invalid_crew", 5},
	} {
		c := client(t, func(w http.ResponseWriter, r *http.Request) {
			envelope(w, tt.status, tt.code, map[string]any{"id": "57"})
		})
		_, err := c.GenerateCrew(context.Background(), "g", "k")
		if e := app.Classify(err); e.Code != camelCase(tt.code) || e.ExitCode() != tt.exit {
			t.Fatalf("%s: code %s exit %d", tt.code, e.Code, e.ExitCode())
		}
	}
}
