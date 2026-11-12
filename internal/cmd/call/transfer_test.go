package call_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

const warmPlan = `{"mode":"warm-transfer-say-summary","summaryPlan":{"timeoutSeconds":5},"fallbackPlan":{"message":"지금은 연결이 어려워요."}}`

func liveCall(t *testing.T) *fakeapi.Server {
	t.Helper()
	api := fakeapi.New(t, "key-1")
	api.Statuses("inProgress")
	runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+82")
	return api
}

// The phone transfer of earlier versions sends the same bytes, so keys
// stored for it still match.
func TestTransferLegacyBodyUnchanged(t *testing.T) {
	t.Parallel()
	api := liveCall(t)
	for _, args := range [][]string{
		{"transfer", "c_1", "--to", "+821099998888"},
		{"transfer", "c_1", "--to", "+821099998888", "--mode", "cold"},
	} {
		if r := runAPI(t, api.URL, "", true, args...); r.exit != 0 {
			t.Fatalf("%v: exit %d stdout:\n%s", args, r.exit, r.stdout)
		}
	}
	for _, tr := range api.Transfers() {
		if tr.Raw != `{"mode":"cold","destination":{"type":"phone","target":"+821099998888"}}` {
			t.Fatalf("raw body = %s", tr.Raw)
		}
	}
}

func TestTransferModesAndDestinations(t *testing.T) {
	t.Parallel()
	api := liveCall(t)
	plan := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(plan, []byte(warmPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{
			[]string{"--mode", "warm", "--to", "+821099998888", "--message", "담당 상담사에게 연결해 드릴게요.", "--plan", warmPlan},
			`{"mode":"warm","destination":{"type":"phone","target":"+821099998888"},"message":"담당 상담사에게 연결해 드릴게요.","transferPlan":` + warmPlan + `}`,
		},
		{
			[]string{"--mode", "warm", "--to", "+821099998888", "--plan", "@" + plan},
			`{"mode":"warm","destination":{"type":"phone","target":"+821099998888"},"transferPlan":` + warmPlan + `}`,
		},
		{
			[]string{"--agent-app"},
			`{"mode":"cold","destination":{"type":"agentApp"}}`,
		},
		{
			[]string{"--agent-app", "--message", "상담사를 연결할게요."},
			`{"mode":"cold","destination":{"type":"agentApp"},"message":"상담사를 연결할게요."}`,
		},
		{
			[]string{"--to", "+821099998888", "--plan", `{"mode":"blind-transfer-add-summary-to-sip-header"}`},
			`{"mode":"cold","destination":{"type":"phone","target":"+821099998888"},"transferPlan":{"mode":"blind-transfer-add-summary-to-sip-header"}}`,
		},
	}
	for i, tc := range cases {
		r := runAPI(t, api.URL, "", true, append([]string{"transfer", "c_1"}, tc.args...)...)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d stdout:\n%s", tc.args, r.exit, r.stdout)
		}
		tr := api.Transfers()[i]
		if jsonOf(tr.Body) != jsonOf(mustObject(t, tc.want)) || tr.IdempotencyKey == "" {
			t.Fatalf("%v: body %s, want %s", tc.args, tr.Raw, tc.want)
		}
	}
}

func TestTransferAgentAppHumanOutput(t *testing.T) {
	t.Parallel()
	api := liveCall(t)
	r := runAPI(t, api.URL, "", false, "transfer", "c_1", "--agent-app")
	if r.exit != 0 || !strings.Contains(r.stdout, "agent app") || !strings.Contains(r.stdout, "tello call get c_1") {
		t.Fatalf("exit %d stdout:\n%s", r.exit, r.stdout)
	}
}

func TestTransferModeRefusals(t *testing.T) {
	t.Parallel()
	api := liveCall(t)
	api.FailTransfers(
		fakeapi.Failure{Status: 422, Code: "transfer_mode_unsupported"},
		fakeapi.Failure{Status: 422, Code: "transfer_destination_unsupported"},
		fakeapi.Failure{Status: 400, Code: "invalid_transfer_request"},
	)
	args := []string{"transfer", "c_1", "--mode", "warm", "--to", "+821099998888", "--plan", warmPlan, "--idempotency-key", "t-1"}
	if data := runAPI(t, api.URL, "", true, args...).failed(t, 5, "transferModeUnsupported"); data["idempotencyKey"] != "t-1" {
		t.Fatalf("data = %v", data)
	}
	runAPI(t, api.URL, "", true, "transfer", "c_1", "--agent-app").failed(t, 5, "transferDestinationUnsupported")
	runAPI(t, api.URL, "", true, args...).failed(t, 5, "invalidTransferRequest")
}

func TestTransferModeUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"no destination":       {"transfer", "c_1"},
		"both destinations":    {"transfer", "c_1", "--to", "+82", "--agent-app"},
		"unknown mode":         {"transfer", "c_1", "--to", "+82", "--mode", "blind"},
		"warm without plan":    {"transfer", "c_1", "--to", "+82", "--mode", "warm"},
		"warm agent app":       {"transfer", "c_1", "--agent-app", "--mode", "warm", "--plan", warmPlan},
		"agent app with plan":  {"transfer", "c_1", "--agent-app", "--plan", `{"mode":"blind-transfer"}`},
		"plan not object":      {"transfer", "c_1", "--to", "+82", "--plan", `["warm"]`},
		"plan not JSON":        {"transfer", "c_1", "--to", "+82", "--mode", "warm", "--plan", `{mode:warm}`},
		"plan missing file":    {"transfer", "c_1", "--to", "+82", "--mode", "warm", "--plan", "@/nonexistent/plan.json"},
		"agent app empty --to": {"transfer", "c_1", "--agent-app", "--to", ""},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := fakeapi.New(t, "key-1")
			runAPI(t, api.URL, "", true, args...).failed(t, 2, "usage")
			if len(api.Transfers()) != 0 {
				t.Fatalf("request sent for invalid input")
			}
		})
	}
}

func mustObject(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
