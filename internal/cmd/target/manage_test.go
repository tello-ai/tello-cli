package target_test

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cmd/target"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

func TestAgentAndCrewManagement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		group    string
		newCmd   func(*app.App) *cobra.Command
		body     string
		notFound string
	}{
		{"agents", target.NewAgent, `{"name":"예약 안내","systemPrompt":"상담원","toolIds":["41"]}`, "agentNotFound"},
		{"crews", target.NewCrew, `{"name":"접수 흐름","graph":{"nodes":[{"id":"n1"}],"edges":[]},"entryNodeId":"n1"}`, "crewNotFound"},
	} {
		t.Run(tc.group, func(t *testing.T) {
			t.Parallel()
			api := fakeapi.New(t, "key-1")
			data := exec(t, tc.newCmd, api.URL, "", true, "create", "--body", tc.body, "--idempotency-key", tc.group+"-1").ok(t)
			if data["id"] != "41" || data["revision"] != float64(1) {
				t.Fatalf("create data = %v", data)
			}
			if data = exec(t, tc.newCmd, api.URL, "", true, "get", "41").ok(t); data["name"] == nil {
				t.Fatalf("get data = %v", data)
			}
			data = exec(t, tc.newCmd, api.URL, "", true, "update", "41", "--body", `{"name":"v2"}`).ok(t)
			if data["name"] != "v2" || data["revision"] != float64(2) {
				t.Fatalf("update data = %v", data)
			}
			if data = exec(t, tc.newCmd, api.URL, "", true, "delete", "41").ok(t); jsonOf(data) != `{"deleted":true,"id":"41"}` {
				t.Fatalf("delete data = %v", data)
			}
			exec(t, tc.newCmd, api.URL, "", true, "get", "41").failed(t, 5, tc.notFound)

			reqs := api.Requests()
			if len(reqs) != 5 || reqs[0].Path != "/"+tc.group || reqs[0].IdempotencyKey != tc.group+"-1" ||
				reqs[2].Method != "PATCH" || reqs[2].Path != "/"+tc.group+"/41" || reqs[3].Method != "DELETE" {
				t.Fatalf("requests = %+v", reqs)
			}
			// list still answers from the target catalog.
			if data = exec(t, tc.newCmd, api.URL, "", true, "list").ok(t); jsonOf(data) != `{"data":[]}` {
				t.Fatalf("list data = %v", data)
			}
		})
	}
}

func TestAgentAndCrewRefusals(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailResources(
		fakeapi.Failure{Status: 409, Code: "agent_in_use", Data: map[string]any{"reason": "channel_linked", "channels": []string{"web"}}},
		fakeapi.Failure{Status: 409, Code: "agent_engine_immutable"},
		fakeapi.Failure{Status: 422, Code: "agent_not_publishable", Data: map[string]any{"reason": "byok_required"}},
		fakeapi.Failure{Status: 422, Code: "crew_not_publishable", Data: map[string]any{"reason": "graph_invalid"}},
		fakeapi.Failure{Status: 409, Code: "crew_in_use", Data: map[string]any{"reason": "default_target"}},
		fakeapi.Failure{Status: 502, Code: "write_outcome_unknown"},
	)
	if data := exec(t, target.NewAgent, api.URL, "", true, "delete", "12").failed(t, 5, "agentInUse"); data["reason"] != "channel_linked" {
		t.Fatalf("data = %v", data)
	}
	exec(t, target.NewAgent, api.URL, "", true, "update", "12", "--body", `{"engineMode":"s2s"}`).failed(t, 5, "agentEngineImmutable")
	if data := exec(t, target.NewAgent, api.URL, "", true, "create", "--body", `{"name":"a"}`).failed(t, 5, "agentNotPublishable"); data["reason"] != "byok_required" || data["idempotencyKey"] == nil {
		t.Fatalf("data = %v", data)
	}
	exec(t, target.NewCrew, api.URL, "", true, "update", "34", "--body", `{"graph":{}}`).failed(t, 5, "crewNotPublishable")
	exec(t, target.NewCrew, api.URL, "", true, "delete", "34").failed(t, 5, "crewInUse")
	if data := exec(t, target.NewCrew, api.URL, "", true, "create", "--body", `{"name":"c"}`, "--idempotency-key", "crew-9").failed(t, 8, "writeOutcomeUnknown"); data["idempotencyKey"] != "crew-9" {
		t.Fatalf("data = %v", data)
	}
}

func TestAgentAndCrewUsageErrors(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"agent get bad id":       {"get", "a12"},
		"agent update no body":   {"update", "12"},
		"agent create bad body":  {"create", "--body", `[]`},
		"agent delete no id":     {"delete"},
		"agent create bad key":   {"create", "--body", `{}`, "--idempotency-key", ""},
		"agent no subcommand":    nil,
		"agent unknown command":  {"push"},
		"agent list with arg":    {"list", "12"},
		"agent update extra arg": {"update", "12", "13", "--body", `{}`},
	} {
		for group, newCmd := range map[string]func(*app.App) *cobra.Command{"agent": target.NewAgent, "crew": target.NewCrew} {
			t.Run(group+" "+name, func(t *testing.T) {
				t.Parallel()
				api := fakeapi.New(t, "key-1")
				exec(t, newCmd, api.URL, "", true, args...).failed(t, 2, "usage")
				if reqs := api.Requests(); len(reqs) != 0 {
					t.Fatalf("requests sent: %+v", reqs)
				}
			})
		}
	}
}
