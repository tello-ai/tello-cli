package call_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

const fastPoll = "--poll-interval=10ms"

func statusesOf(r result) []string {
	var out []string
	for _, l := range r.linesOfType("cli.status") {
		out = append(out, l["status"].(string))
	}
	return out
}

func TestCreateWithAgentReturnsWithoutWaiting(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	r := runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+821012345678",
		"--var", "고객명=홍길동", "--var", "메모=a=b")

	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	}
	data, _ := r.last(t)["data"].(map[string]any)
	if data["id"] != "c_1" || data["status"] != "dialing" {
		t.Fatalf("result data = %v, want the created call", data)
	}
	creates := api.Creates()
	if len(creates) != 1 || creates[0].IdempotencyKey == "" {
		t.Fatalf("creates = %+v, want one with an idempotency key", creates)
	}
	want := `{"agentId":"12","to":"+821012345678","variables":{"고객명":"홍길동","메모":"a=b"}}`
	if got := jsonOf(creates[0].Body); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestCreateWaitStreamsStatusesUntilCompleted(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.Statuses("dialing", "dialing", "inProgress", "completed")
	r := runAPI(t, api.URL, "", true, "create", "--crew", "34", "--to", "+82", "--wait", fastPoll)

	if r.exit != 0 {
		t.Fatalf("exit %d stderr:\n%s", r.exit, r.stderr)
	}
	if got := strings.Join(statusesOf(r), ","); got != "dialing,inProgress,completed" {
		t.Fatalf("cli.status sequence = %s, want each change once", got)
	}
	data, _ := r.last(t)["data"].(map[string]any)
	if data["status"] != "completed" || data["summary"] != "Booked." {
		t.Fatalf("result data = %v, want the final call", data)
	}
	if body := api.Creates()[0].Body; body["crewId"] != "34" || body["agentId"] != nil {
		t.Fatalf("body = %v, want crewId only", body)
	}
}

func TestCreateWaitUnansweredExits6(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.Statuses("dialing", "noAnswer")
	r := runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82", "--wait", fastPoll)
	data := r.failed(t, 6, "noAnswer")
	if data["id"] != "c_1" {
		t.Fatalf("data = %v, want the call", data)
	}
}

func TestCreateWaitTimeoutEndsCall(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.Statuses("dialing", "inProgress")
	r := runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82",
		"--wait", "--timeout", "100ms", fastPoll)

	data := r.failed(t, 6, "timeout")
	if ends := api.Ends(); len(ends) != 1 || ends[0] != "c_1" {
		t.Fatalf("ends = %v, want c_1 ended once", ends)
	}
	if data["status"] != "cancelled" {
		t.Fatalf("data = %v, want the call settled as cancelled", data)
	}
}

func TestCreateWaitInterruptEndsCall(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.Statuses("dialing", "inProgress")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	r := runCtx(t, ctx, func(a *app.App) { a.APIURL = api.URL }, "", true,
		"create", "--agent", "12", "--to", "+82", "--wait", fastPoll)

	r.failed(t, 130, "interrupted")
	if ends := api.Ends(); len(ends) != 1 {
		t.Fatalf("ends = %v, want the live call ended", ends)
	}
}

func TestCreateFailureReportsIdempotencyKey(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 502, Code: "call_outcome_unknown"})
	r := runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82", "--idempotency-key", "job-7")

	data := r.failed(t, 8, "callOutcomeUnknown")
	if data["idempotencyKey"] != "job-7" || api.Creates()[0].IdempotencyKey != "job-7" {
		t.Fatalf("data = %v, sent key %q; want job-7 both", data, api.Creates()[0].IdempotencyKey)
	}
}

func TestCreateMissingVariablesListsNames(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 422, Code: "missing_variables", Data: map[string]any{"missingVariables": []string{"고객명"}}})
	r := runAPI(t, api.URL, "", true, "create", "--crew", "34", "--to", "+82")

	data := r.failed(t, 5, "missingVariables")
	if jsonOf(data["missingVariables"]) != `["고객명"]` || data["idempotencyKey"] == nil {
		t.Fatalf("data = %v, want missingVariables and the key to retry with", data)
	}
}

func TestCreateTargetUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"agent and crew":         {"create", "--to", "+82", "--agent", "1", "--crew", "2"},
		"agent with -i":          {"create", "--to", "+82", "--agent", "1", "-i"},
		"agent with handler":     {"create", "--to", "+82", "--agent", "1", "--", "bot"},
		"agent with prompt":      {"create", "--to", "+82", "--agent", "1", "--prompt", "p"},
		"agent with metadata":    {"create", "--to", "+82", "--agent", "1", "--metadata", "{}"},
		"timeout without wait":   {"create", "--to", "+82", "--agent", "1", "--timeout", "1m"},
		"var without equals":     {"create", "--to", "+82", "--agent", "1", "--var", "name"},
		"var empty name":         {"create", "--to", "+82", "--agent", "1", "--var", "=x"},
		"var repeated":           {"create", "--to", "+82", "--agent", "1", "--var", "a=1", "--var", "a=2"},
		"var without target":     {"create", "--to", "+82", "-i", "--var", "a=1"},
		"wait without target":    {"create", "--to", "+82", "-i", "--wait"},
		"idempotency w/o target": {"create", "--to", "+82", "-i", "--idempotency-key", "k"},
		"non-positive poll":      {"create", "--to", "+82", "--agent", "1", "--wait", "--poll-interval", "0s"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := runAPI(t, "http://127.0.0.1:1/v1", "", true, args...)
			r.failed(t, 2, "usage")
		})
	}
}

func TestGetListEnd(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.Statuses("inProgress")
	runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82")

	r := runAPI(t, api.URL, "", true, "get", "c_1")
	if data, _ := r.last(t)["data"].(map[string]any); r.exit != 0 || data["status"] != "inProgress" {
		t.Fatalf("get: exit %d data %v", r.exit, data)
	}

	r = runAPI(t, api.URL, "", true, "end", "c_1")
	if r.exit != 0 || len(api.Ends()) != 1 {
		t.Fatalf("end: exit %d ends %v", r.exit, api.Ends())
	}
	r = runAPI(t, api.URL, "", true, "end", "c_1")
	r.failed(t, 5, "callNotActive")

	r = runAPI(t, api.URL, "", true, "get", "c_404")
	r.failed(t, 5, "callNotFound")

	api.ListPage(map[string]any{"data": []any{map[string]any{"id": "c_1", "status": "cancelled"}}, "nextCursor": "cur-2"})
	r = runAPI(t, api.URL, "", true, "list", "--status", "cancelled", "--agent", "12",
		"--created-after", "2026-10-20T00:00:00+09:00", "--limit", "5", "--cursor", "cur-1")
	data, _ := r.last(t)["data"].(map[string]any)
	if r.exit != 0 || data["nextCursor"] != "cur-2" {
		t.Fatalf("list: exit %d data %v", r.exit, data)
	}
	q := api.ListQueries()[0]
	if q.Get("status") != "cancelled" || q.Get("agentId") != "12" || q.Get("limit") != "5" ||
		q.Get("cursor") != "cur-1" || q.Get("createdAfter") != "2026-10-20T00:00:00+09:00" {
		t.Fatalf("list query = %v", q)
	}
}

func TestGetHumanOutput(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.Statuses("completed")
	runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82")
	r := runAPI(t, api.URL, "", false, "get", "c_1")
	for _, want := range []string{"c_1", "completed", "60", "Booked.", "user: hi"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, r.stdout)
		}
	}
}

func TestBatchWithAgentRunsTasksInOrder(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	tasks := writeTasks(t, `[{"to":"+8201","variables":{"고객명":"가","인원":2}},{"to":"+8202"}]`)
	r := runAPI(t, api.URL, "", true, "batch", "--tasks", tasks, "--agent", "12", fastPoll)

	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	}
	creates := api.Creates()
	if len(creates) != 2 || creates[0].IdempotencyKey == creates[1].IdempotencyKey {
		t.Fatalf("creates = %+v, want two with distinct keys", creates)
	}
	if got := jsonOf(creates[0].Body); got != `{"agentId":"12","to":"+8201","variables":{"고객명":"가","인원":2}}` {
		t.Fatalf("first body = %s", got)
	}
	taskLines := r.linesOfType("cli.task")
	if len(taskLines) != 2 || taskLines[1]["callId"] != "c_2" || taskLines[1]["status"] != "completed" {
		t.Fatalf("cli.task lines = %v", taskLines)
	}
	if data, _ := r.last(t)["data"].(map[string]any); jsonOf(data) != `{"completed":2,"failed":0,"skipped":0,"total":2}` {
		t.Fatalf("result data = %v", data)
	}
}

// A retried task must reuse its idempotency key so a create that did reach
// the service is replayed instead of dialing twice.
func TestBatchWithAgentRetriesWithSameKey(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 409, Code: "concurrent_limit_exceeded"})
	tasks := writeTasks(t, `[{"to":"+8201"}]`)
	r := runAPI(t, api.URL, "", true, "batch", "--tasks", tasks, "--agent", "12", "--retry-delay", "10ms", fastPoll)

	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s", r.exit, r.stdout)
	}
	creates := api.Creates()
	if len(creates) != 2 || creates[0].IdempotencyKey != creates[1].IdempotencyKey {
		t.Fatalf("creates = %+v, want a retry with the same key", creates)
	}
	if retries := r.linesOfType("cli.retry"); len(retries) != 1 || retries[0]["code"] != "concurrentLimitExceeded" {
		t.Fatalf("cli.retry lines = %v", retries)
	}
}

func TestBatchWithAgentStopsOnTargetRefusal(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailCreates(fakeapi.Failure{Status: 422, Code: "agent_inactive"})
	tasks := writeTasks(t, `[{"to":"+8201"},{"to":"+8202"}]`)
	r := runAPI(t, api.URL, "", true, "batch", "--tasks", tasks, "--agent", "12", fastPoll)

	data := r.failed(t, 5, "agentInactive")
	if len(api.Creates()) != 1 || data["skipped"] != float64(1) {
		t.Fatalf("creates %d data %v, want the batch stopped after the first task", len(api.Creates()), data)
	}
}

func TestBatchTargetUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		stdin string
		args  []string
	}{
		"prompt with agent":      {`[{"to":"+82","prompt":"p"}]`, []string{"batch", "--tasks", "-", "--agent", "1"}},
		"metadata with agent":    {`[{"to":"+82","metadata":{}}]`, []string{"batch", "--tasks", "-", "--agent", "1"}},
		"variables with handler": {`[{"to":"+82","variables":{"a":"1"}}]`, []string{"batch", "--tasks", "-", "--", "bot"}},
		"agent with handler":     {`[{"to":"+82"}]`, []string{"batch", "--tasks", "-", "--agent", "1", "--", "bot"}},
		"agent and crew":         {`[{"to":"+82"}]`, []string{"batch", "--tasks", "-", "--agent", "1", "--crew", "2"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := runAPI(t, "http://127.0.0.1:1/v1", tc.stdin, true, tc.args...)
			r.failed(t, 2, "usage")
		})
	}
}
