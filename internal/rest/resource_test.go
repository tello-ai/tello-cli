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

// Each write answers one documented success status; another 2xx must not
// pass for a done write.
func TestResourceWritesCheckSuccessStatus(t *testing.T) {
	ctx := context.Background()
	ops := map[string]struct {
		want int
		send func(*rest.Client) error
	}{
		"create": {201, func(c *rest.Client) error {
			_, err := c.CreateResource(ctx, rest.Tools, rest.Object{"name": "x"}, "k")
			return err
		}},
		"update": {200, func(c *rest.Client) error {
			_, err := c.UpdateResource(ctx, rest.Agents, "12", rest.Object{"name": "x"})
			return err
		}},
		"delete": {204, func(c *rest.Client) error { return c.DeleteResource(ctx, rest.Crews, "34") }},
	}
	for name, op := range ops {
		for _, status := range []int{200, 201, 202, 204} {
			t.Run(fmt.Sprintf("%s %d", name, status), func(t *testing.T) {
				c := client(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					if status != 204 {
						_, _ = io.WriteString(w, `{"id":"12"}`)
					}
				})
				err := op.send(c)
				if status == op.want && err != nil {
					t.Fatal(err)
				}
				if status != op.want && (err == nil || app.Classify(err).Code != "invalidResponse") {
					t.Fatalf("expected invalidResponse, got %v", err)
				}
			})
		}
	}
}

func TestResourceRequests(t *testing.T) {
	ctx := context.Background()
	var got []string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, fmt.Sprintf("%s %s %s key=%q ct=%q", r.Method, r.URL.RequestURI(), body, r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")))
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(201)
		case http.MethodDelete:
			w.WriteHeader(204)
			return
		}
		_, _ = io.WriteString(w, `{"id":"41","data":[],"nextCursor":null}`)
	})
	if _, err := c.ListTools(ctx, rest.ListTools{Limit: 2, Cursor: "eyJpZCI6NDF9"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTools(ctx, rest.ListTools{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetResource(ctx, rest.Agents, "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateResource(ctx, rest.Crews, rest.Object{"name": "흐름"}, "crew-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateResource(ctx, rest.Tools, "41", rest.Object{}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteResource(ctx, rest.Tools, "41"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`GET /v1/tools?cursor=eyJpZCI6NDF9&limit=2  key="" ct=""`,
		`GET /v1/tools  key="" ct=""`,
		`GET /v1/agents/12  key="" ct=""`,
		`POST /v1/crews {"name":"흐름"} key="crew-1" ct="application/json"`,
		`PATCH /v1/tools/41 {} key="" ct="application/json"`,
		`DELETE /v1/tools/41  key="" ct=""`,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("requests:\n%q\nwant:\n%q", got, want)
	}
}

// A write that failed without a documented code may have been applied.
func TestResourceWriteUndocumentedServerError(t *testing.T) {
	for _, tt := range []struct {
		status    int
		body      string
		code      string
		exit      int
		retryable bool
	}{
		{502, "bad gateway", "writeOutcomeUnknown", 8, false},
		{503, "unavailable", "writeOutcomeUnknown", 8, false},
		{504, `{"error":"new_thing"}`, "writeOutcomeUnknown", 8, false},
		{503, `{"error":"upstream_unavailable"}`, "upstreamUnavailable", 75, true},
		{500, `{"error":"internal_error"}`, "internalError", 8, false},
		{502, `{"error":"write_outcome_unknown"}`, "writeOutcomeUnknown", 8, false},
	} {
		t.Run(fmt.Sprint(tt.status, tt.body), func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			for _, write := range []func() error{
				func() error {
					_, err := c.CreateResource(context.Background(), rest.Tools, rest.Object{}, "k")
					return err
				},
				func() error {
					_, err := c.UpdateResource(context.Background(), rest.Tools, "41", rest.Object{})
					return err
				},
				func() error { return c.DeleteResource(context.Background(), rest.Tools, "41") },
			} {
				e := app.Classify(write())
				if e.Code != tt.code || e.ExitCode() != tt.exit || e.Retryable() != tt.retryable {
					t.Fatalf("code %s exit %d retryable %v", e.Code, e.ExitCode(), e.Retryable())
				}
			}
			// Reads stay classified by status when the code is undocumented.
			if _, err := c.GetResource(context.Background(), rest.Tools, "41"); tt.body[0] != '{' && app.Classify(err).Code == "writeOutcomeUnknown" {
				t.Fatalf("read reported as a write outcome")
			}
		})
	}
}

func TestNewErrorCodesClassification(t *testing.T) {
	for _, tt := range []struct {
		status int
		code   string
		exit   int
	}{
		{403, "insufficient_scope", 3},
		{403, "ip_not_allowed", 3},
		{415, "invalid_request", 5},
		{400, "invalid_query", 5},
		{400, "invalid_cursor", 5},
		{400, "invalid_tool", 5},
		{400, "invalid_agent", 5},
		{400, "invalid_crew", 5},
		{404, "tool_not_found", 5},
		{409, "default_tool_protected", 5},
		{409, "tool_in_use", 5},
		{409, "tool_conflict", 75},
		{422, "tool_type_managed", 5},
		{422, "tool_not_publishable", 5},
		{409, "agent_engine_immutable", 5},
		{409, "agent_in_use", 5},
		{422, "agent_not_publishable", 5},
		{409, "crew_in_use", 5},
		{422, "crew_not_publishable", 5},
		{502, "write_outcome_unknown", 8},
		{503, "upstream_unavailable", 75},
		{413, "payload_too_large", 5},
	} {
		t.Run(tt.code, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, r *http.Request) { envelope(w, tt.status, tt.code, nil) })
			_, err := c.GetResource(context.Background(), rest.Tools, "41")
			if e := app.Classify(err); e.ExitCode() != tt.exit || e.Code != camelCase(tt.code) {
				t.Fatalf("code %s exit %d, want %s exit %d", e.Code, e.ExitCode(), camelCase(tt.code), tt.exit)
			}
		})
	}
}

func camelCase(s string) string {
	out := []rune{}
	up := false
	for _, r := range s {
		switch {
		case r == '_':
			up = true
		case up:
			out = append(out, r-'a'+'A')
			up = false
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
