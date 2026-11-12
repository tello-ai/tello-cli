package call_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

func TestDraftCreateReviseConfirm(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	r := runAPI(t, api.URL, "", true, "draft", "create", "--agent", "12", "--to", "+821012345678",
		"--instructions", "내일 2시 예약을 확인해 주세요", "--var", "고객명=홍길동", "--max-duration-seconds", "300")
	data, _ := r.last(t)["data"].(map[string]any)
	if r.exit != 0 || len(r.lines) != 1 || data["id"] != "cd_481" || data["status"] != "pendingConfirmation" {
		t.Fatalf("create: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	reqs := api.Requests()
	want := `{"agentId":"12","instructions":"내일 2시 예약을 확인해 주세요","maxDurationSeconds":300,"to":"+821012345678","variables":{"고객명":"홍길동"}}`
	if reqs[0].Path != "/call-drafts" || jsonOf(reqs[0].Body) != want || reqs[0].IdempotencyKey == "" {
		t.Fatalf("create request = %+v", reqs[0])
	}
	if len(api.Creates()) != 0 {
		t.Fatal("a draft must not place a call")
	}

	r = runAPI(t, api.URL, "", true, "draft", "revise", "cd_481", "--request", "4시도 안 되면 6시로 물어봐 주세요")
	if data, _ = r.last(t)["data"].(map[string]any); r.exit != 0 || data["id"] != "cd_482" || data["revision"] != float64(2) {
		t.Fatalf("revise: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	if req := api.Requests()[1]; req.Path != "/call-drafts/cd_481/revise" || jsonOf(req.Body) != `{"request":"4시도 안 되면 6시로 물어봐 주세요"}` || req.IdempotencyKey == "" {
		t.Fatalf("revise request = %+v", req)
	}

	r = runAPI(t, api.URL, "", true, "draft", "confirm", "cd_481", "--idempotency-key", "confirm-0")
	if data := r.failed(t, 5, "draftSuperseded"); data["idempotencyKey"] != "confirm-0" {
		t.Fatalf("data = %v", data)
	}

	r = runAPI(t, api.URL, "", true, "draft", "confirm", "cd_482", "--idempotency-key", "confirm-1")
	if data, _ = r.last(t)["data"].(map[string]any); r.exit != 0 || data["id"] != "c_1" || data["status"] != "dialing" {
		t.Fatalf("confirm: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	req := api.Requests()[3]
	if req.Path != "/call-drafts/cd_482/confirm" || req.IdempotencyKey != "confirm-1" || req.Body != nil {
		t.Fatalf("confirm request = %+v, want no body and the key", req)
	}
	// The same key replays the call instead of dialing again.
	r = runAPI(t, api.URL, "", true, "draft", "confirm", "cd_482", "--idempotency-key", "confirm-1")
	if data, _ = r.last(t)["data"].(map[string]any); r.exit != 0 || data["id"] != "c_1" {
		t.Fatalf("replay: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	if data := runAPI(t, api.URL, "", true, "draft", "confirm", "cd_482").failed(t, 5, "draftAlreadyConfirmed"); data["callId"] != "c_1" {
		t.Fatalf("data = %v, want the call placed before", data)
	}
	if data, _ = runAPI(t, api.URL, "", true, "get", "c_1").last(t)["data"].(map[string]any); data["id"] != "c_1" {
		t.Fatalf("get = %v", data)
	}
}

func TestDraftWithoutConfirmation(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.DraftStatus("needsClarification")
	r := runAPI(t, api.URL, "", true, "draft", "create", "--agent", "12", "--to", "+82", "--instructions", "예약 변경")
	if data, _ := r.last(t)["data"].(map[string]any); r.exit != 0 || data["id"] != nil || data["question"] != "몇 시로 바꿀까요?" {
		t.Fatalf("exit %d stdout:\n%s", r.exit, r.stdout)
	}
	r = runAPI(t, api.URL, "", false, "draft", "create", "--agent", "12", "--to", "+82", "--instructions", "예약 변경")
	if r.exit != 0 || !strings.Contains(r.stdout, "needsClarification") || !strings.Contains(r.stdout, "몇 시로 바꿀까요?") {
		t.Fatalf("human: exit %d stdout:\n%s", r.exit, r.stdout)
	}

	// An agent with a ready scenario is called at once.
	api.DraftStatus("confirmed")
	r = runAPI(t, api.URL, "", false, "draft", "create", "--agent", "12", "--to", "+82")
	if r.exit != 0 || !strings.Contains(r.stdout, "c_1") || !strings.Contains(r.stdout, "tello call get c_1") {
		t.Fatalf("human: exit %d stdout:\n%s", r.exit, r.stdout)
	}

	api.DraftStatus("pendingConfirmation")
	r = runAPI(t, api.URL, "", false, "draft", "create", "--agent", "12", "--to", "+82")
	for _, want := range []string{"cd_481", "pendingConfirmation", "시나리오 1판", "tello call draft confirm cd_481"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, r.stdout)
		}
	}
	r = runAPI(t, api.URL, "", false, "draft", "confirm", "cd_481")
	if r.exit != 0 || !strings.Contains(r.stdout, "c_2") || !strings.Contains(r.stdout, "tello call get c_2") {
		t.Fatalf("confirm human: exit %d stdout:\n%s", r.exit, r.stdout)
	}
}

func TestDraftRefusals(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailResources(
		fakeapi.Failure{Status: 422, Code: "draft_not_applicable", Data: map[string]any{"reason": "scenario_disabled"}},
		fakeapi.Failure{Status: 410, Code: "draft_expired"},
		fakeapi.Failure{Status: 422, Code: "draft_revision_limit"},
		fakeapi.Failure{Status: 502, Code: "call_outcome_unknown"},
		fakeapi.Failure{Status: 402, Code: "insufficient_credit"},
		fakeapi.Failure{Status: 503, Code: "upstream_unavailable"},
		fakeapi.Failure{Status: 409, Code: "draft_not_confirmable", Data: map[string]any{"status": "refused"}},
		fakeapi.Failure{Status: 404, Code: "draft_not_found"},
	)
	if data := runAPI(t, api.URL, "", true, "draft", "create", "--agent", "12", "--to", "+82").failed(t, 5, "draftNotApplicable"); data["reason"] != "scenario_disabled" || data["idempotencyKey"] == nil {
		t.Fatalf("data = %v", data)
	}
	runAPI(t, api.URL, "", true, "draft", "confirm", "cd_1").failed(t, 5, "draftExpired")
	runAPI(t, api.URL, "", true, "draft", "revise", "cd_1", "--request", "x").failed(t, 5, "draftRevisionLimit")
	if data := runAPI(t, api.URL, "", true, "draft", "confirm", "cd_1", "--idempotency-key", "c-9").failed(t, 8, "callOutcomeUnknown"); data["idempotencyKey"] != "c-9" {
		t.Fatalf("data = %v", data)
	}
	runAPI(t, api.URL, "", true, "draft", "confirm", "cd_1").failed(t, 4, "insufficientCredit")
	runAPI(t, api.URL, "", true, "draft", "revise", "cd_1", "--request", "x").failed(t, 75, "upstreamUnavailable")
	runAPI(t, api.URL, "", true, "draft", "confirm", "cd_1").failed(t, 5, "draftNotConfirmable")
	runAPI(t, api.URL, "", true, "draft", "confirm", "cd_1").failed(t, 5, "draftNotFound")
}

// Ctrl-C does not stop a confirm the service already received.
func TestDraftConfirmInterruptKeepsKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	r := runCtx(t, ctx, func(a *app.App) { a.APIURL = srv.URL + "/v1" }, "", true, "draft", "confirm", "cd_1", "--idempotency-key", "c-9")
	data := r.failed(t, 130, "interrupted")
	msg, _ := r.last(t)["error"].(map[string]any)["message"].(string)
	if data["idempotencyKey"] != "c-9" || !strings.Contains(msg, "may have been placed") {
		t.Fatalf("message %q data %v", msg, data)
	}
}

func TestDraftUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"no subcommand":         {"draft"},
		"unknown subcommand":    {"draft", "send"},
		"create without agent":  {"draft", "create", "--to", "+82"},
		"create without to":     {"draft", "create", "--agent", "12"},
		"create bad duration":   {"draft", "create", "--agent", "12", "--to", "+82", "--max-duration-seconds", "10"},
		"create bad var":        {"draft", "create", "--agent", "12", "--to", "+82", "--var", "x"},
		"create bad key":        {"draft", "create", "--agent", "12", "--to", "+82", "--idempotency-key", "a,b"},
		"create argument":       {"draft", "create", "cd_1", "--agent", "12", "--to", "+82"},
		"revise without req":    {"draft", "revise", "cd_1"},
		"revise blank req":      {"draft", "revise", "cd_1", "--request", " "},
		"revise without id":     {"draft", "revise", "--request", "x"},
		"confirm without id":    {"draft", "confirm"},
		"confirm two ids":       {"draft", "confirm", "cd_1", "cd_2"},
		"confirm call id":       {"draft", "confirm", "c_1"},
		"confirm bare number":   {"draft", "confirm", "481"},
		"confirm zero":          {"draft", "confirm", "cd_0"},
		"confirm leading zero":  {"draft", "confirm", "cd_0481"},
		"confirm letters":       {"draft", "confirm", "cd_abc"},
		"confirm out of range":  {"draft", "confirm", "cd_9223372036854775808"},
		"confirm key control":   {"draft", "confirm", "cd_1", "--idempotency-key", "a\nb"},
		"confirm empty key":     {"draft", "confirm", "cd_1", "--idempotency-key", ""},
		"revise bad id":         {"draft", "revise", "cd_x", "--request", "x"},
		"create instructions >": {"draft", "create", "--agent", "12", "--to", "+82", "--max-duration-seconds", "1801"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := fakeapi.New(t, "key-1")
			runAPI(t, api.URL, "", true, args...).failed(t, 2, "usage")
			if reqs := api.Requests(); len(reqs) != 0 {
				t.Fatalf("requests sent: %+v", reqs)
			}
		})
	}
}
