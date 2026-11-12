package target_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/cmd/target"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

const toolBody = `{"type":"apiRequest","name":"주문 조회","functionName":"lookup_order","timeoutSeconds":15,"config":{"url":"https://shop.example.com/orders","method":"POST"}}`

func TestToolCreateGetUpdateDelete(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")

	data := exec(t, target.NewTool, api.URL, "", true, "create", "--body", toolBody, "--idempotency-key", "tool-1").ok(t)
	if data["id"] != "41" || data["name"] != "주문 조회" || data["revision"] != float64(1) {
		t.Fatalf("create data = %v, want the created tool", data)
	}
	data = exec(t, target.NewTool, api.URL, "", true, "get", "41").ok(t)
	if data["functionName"] != "lookup_order" {
		t.Fatalf("get data = %v", data)
	}
	data = exec(t, target.NewTool, api.URL, "", true, "update", "41", "--body", `{"description":"배송 조회"}`).ok(t)
	if data["description"] != "배송 조회" || data["revision"] != float64(2) {
		t.Fatalf("update data = %v", data)
	}
	data = exec(t, target.NewTool, api.URL, "", true, "delete", "41").ok(t)
	if jsonOf(data) != `{"deleted":true,"id":"41"}` {
		t.Fatalf("delete data = %v", data)
	}
	exec(t, target.NewTool, api.URL, "", true, "get", "41").failed(t, 5, "toolNotFound")

	reqs := api.Requests()
	want := []string{"POST /tools", "GET /tools/41", "PATCH /tools/41", "DELETE /tools/41", "GET /tools/41"}
	if len(reqs) != len(want) {
		t.Fatalf("requests = %+v", reqs)
	}
	for i, r := range reqs {
		if r.Method+" "+r.Path != want[i] {
			t.Fatalf("request %d = %s %s, want %s", i, r.Method, r.Path, want[i])
		}
	}
	if reqs[0].IdempotencyKey != "tool-1" || jsonOf(reqs[0].Body) != jsonOf(mustJSON(t, toolBody)) {
		t.Fatalf("create request = %+v", reqs[0])
	}
	// Only creates take an idempotency key.
	if reqs[2].IdempotencyKey != "" || reqs[3].IdempotencyKey != "" || jsonOf(reqs[2].Body) != `{"description":"배송 조회"}` {
		t.Fatalf("update/delete requests = %+v %+v", reqs[2], reqs[3])
	}
}

func TestToolHumanOutput(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	r := exec(t, target.NewTool, api.URL, "", false, "create", "--body", toolBody)
	if r.exit != 0 || !strings.Contains(r.stdout, `"functionName": "lookup_order"`) {
		t.Fatalf("create: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	r = exec(t, target.NewTool, api.URL, "", false, "list")
	if r.exit != 0 || !strings.Contains(r.stdout, "41") || !strings.Contains(r.stdout, "apiRequest") || !strings.Contains(r.stdout, "주문 조회") {
		t.Fatalf("list: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	r = exec(t, target.NewTool, api.URL, "", false, "delete", "41")
	if r.exit != 0 || !strings.Contains(r.stdout, "41") {
		t.Fatalf("delete: exit %d stdout:\n%s", r.exit, r.stdout)
	}
}

func TestToolListPages(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	for range 3 {
		exec(t, target.NewTool, api.URL, "", true, "create", "--body", toolBody).ok(t)
	}
	data := exec(t, target.NewTool, api.URL, "", true, "list", "--limit", "2").ok(t)
	if items, _ := data["data"].([]any); len(items) != 2 || data["nextCursor"] != "42" {
		t.Fatalf("page 1 = %v", data)
	}
	data = exec(t, target.NewTool, api.URL, "", true, "list", "--limit", "2", "--cursor", "42").ok(t)
	if items, _ := data["data"].([]any); len(items) != 1 || data["nextCursor"] != nil {
		t.Fatalf("page 2 = %v", data)
	}
	reqs := api.Requests()
	if q := reqs[len(reqs)-1].Query; q.Get("limit") != "2" || q.Get("cursor") != "42" {
		t.Fatalf("query = %v", q)
	}
	if q := reqs[len(reqs)-2].Query; q.Has("cursor") {
		t.Fatalf("first page query = %v, want no cursor", q)
	}
	exec(t, target.NewTool, api.URL, "", true, "list", "--limit", "101").failed(t, 5, "invalidQuery")
}

func TestToolBodyFromFileAndStdin(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	path := filepath.Join(t.TempDir(), "tool.json")
	if err := os.WriteFile(path, []byte(toolBody+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec(t, target.NewTool, api.URL, "", true, "create", "--body", "@"+path).ok(t)
	exec(t, target.NewTool, api.URL, `{"timeoutSeconds":1.50}`, true, "update", "41", "--body", "-").ok(t)
	reqs := api.Requests()
	if jsonOf(reqs[0].Body) != jsonOf(mustJSON(t, toolBody)) {
		t.Fatalf("file body = %v", reqs[0].Body)
	}
	if reqs[1].Method != http.MethodPatch || jsonOf(reqs[1].Body) != `{"timeoutSeconds":1.5}` {
		t.Fatalf("stdin body = %+v", reqs[1])
	}
}

// Bad bodies, ids and keys are refused before any request.
func TestToolUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		stdin string
		args  []string
	}{
		"create without body":   {"", []string{"create"}},
		"body array":            {"", []string{"create", "--body", `[1]`}},
		"body null":             {"", []string{"create", "--body", `null`}},
		"body string":           {"", []string{"create", "--body", `"x"`}},
		"body not JSON":         {"", []string{"create", "--body", `{name:1}`}},
		"body trailing data":    {"", []string{"create", "--body", `{} {}`}},
		"body missing file":     {"", []string{"create", "--body", "@/nonexistent/tool.json"}},
		"body stdin array":      {`[]`, []string{"create", "--body", "-"}},
		"body stdin empty":      {``, []string{"create", "--body", "-"}},
		"create with arg":       {"", []string{"create", "41", "--body", `{}`}},
		"key with comma":        {"", []string{"create", "--body", `{}`, "--idempotency-key", "a,b"}},
		"update without id":     {"", []string{"update", "--body", `{}`}},
		"update without body":   {"", []string{"update", "41"}},
		"update bad body":       {"", []string{"update", "41", "--body", `1`}},
		"get without id":        {"", []string{"get"}},
		"get two ids":           {"", []string{"get", "41", "42"}},
		"get letters":           {"", []string{"get", "abc"}},
		"get leading zero":      {"", []string{"get", "041"}},
		"get zero":              {"", []string{"get", "0"}},
		"delete path":           {"", []string{"delete", "../calls"}},
		"list negative limit":   {"", []string{"list", "--limit", "-1"}},
		"list with argument":    {"", []string{"list", "x"}},
		"unknown subcommand":    {"", []string{"remove", "41"}},
		"no subcommand in JSON": {"", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := fakeapi.New(t, "key-1")
			exec(t, target.NewTool, api.URL, tc.stdin, true, tc.args...).failed(t, 2, "usage")
			if reqs := api.Requests(); len(reqs) != 0 {
				t.Fatalf("requests sent for invalid input: %+v", reqs)
			}
		})
	}
}

func TestToolCreateFailuresReportKey(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailResources(
		fakeapi.Failure{Status: 502, Code: "write_outcome_unknown"},
		fakeapi.Failure{Status: 409, Code: "idempotency_in_progress"},
		fakeapi.Failure{Status: 422, Code: "tool_not_publishable", Data: map[string]any{"reason": "tool_draft_incomplete"}},
		fakeapi.Failure{Status: 503, Code: "upstream_unavailable"},
	)
	data := exec(t, target.NewTool, api.URL, "", true, "create", "--body", toolBody).failed(t, 8, "writeOutcomeUnknown")
	if key, _ := data["idempotencyKey"].(string); key == "" || api.Requests()[0].IdempotencyKey != key {
		t.Fatalf("data = %v, sent key %q; want the generated key reported", data, api.Requests()[0].IdempotencyKey)
	}
	data = exec(t, target.NewTool, api.URL, "", true, "create", "--body", toolBody, "--idempotency-key", "k-2").failed(t, 75, "idempotencyInProgress")
	if data["idempotencyKey"] != "k-2" {
		t.Fatalf("data = %v", data)
	}
	data = exec(t, target.NewTool, api.URL, "", true, "create", "--body", toolBody).failed(t, 5, "toolNotPublishable")
	if data["reason"] != "tool_draft_incomplete" || data["idempotencyKey"] == nil {
		t.Fatalf("data = %v, want the reason and the key", data)
	}
	exec(t, target.NewTool, api.URL, "", true, "update", "41", "--body", `{}`).failed(t, 75, "upstreamUnavailable")
}

func TestToolWriteRefusals(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailResources(
		fakeapi.Failure{Status: 409, Code: "default_tool_protected"},
		fakeapi.Failure{Status: 409, Code: "tool_in_use"},
		fakeapi.Failure{Status: 409, Code: "tool_conflict"},
		fakeapi.Failure{Status: 403, Code: "insufficient_scope", Data: map[string]any{"requiredScope": "tools:write"}},
		fakeapi.Failure{Status: 502, Code: "write_outcome_unknown"},
	)
	exec(t, target.NewTool, api.URL, "", true, "delete", "41").failed(t, 5, "defaultToolProtected")
	exec(t, target.NewTool, api.URL, "", true, "delete", "41").failed(t, 5, "toolInUse")
	exec(t, target.NewTool, api.URL, "", true, "update", "41", "--body", `{}`).failed(t, 75, "toolConflict")
	data := exec(t, target.NewTool, api.URL, "", true, "delete", "41").failed(t, 3, "insufficientScope")
	if data["requiredScope"] != "tools:write" {
		t.Fatalf("data = %v", data)
	}
	exec(t, target.NewTool, api.URL, "", true, "delete", "41").failed(t, 8, "writeOutcomeUnknown")
}

// A write the service may have received stays possibly applied: Ctrl-C
// says so and keeps the key.
func TestToolCreateInterruptKeepsKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	r := execCtx(t, ctx, target.NewTool, srv.URL+"/v1", "", true, "create", "--body", toolBody, "--idempotency-key", "t-9")
	data := r.failed(t, 130, "interrupted")
	msg, _ := r.lines[len(r.lines)-1]["error"].(map[string]any)["message"].(string)
	if data["idempotencyKey"] != "t-9" || !strings.Contains(msg, "may have") {
		t.Fatalf("message %q data %v", msg, data)
	}
}

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := jsonUnmarshal(s, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
