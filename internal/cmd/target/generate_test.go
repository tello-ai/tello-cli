package target_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/cmd/target"
	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

const (
	goal     = "치과 예약 고객에게 내일 예약을 확인한다"
	fastPoll = "--poll-interval=10ms"
)

func generationStatuses(r result) []string {
	var out []string
	for _, l := range r.linesOfType("cli.status") {
		out = append(out, l["generationId"].(string)+":"+l["status"].(string))
	}
	return out
}

func TestCrewGenerateReturnsJob(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	data := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal).ok(t)
	if data["id"] != "41" || data["status"] != "running" {
		t.Fatalf("data = %v, want the running job", data)
	}
	reqs := api.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/crews/generate" || jsonOf(reqs[0].Body) != `{"goal":"`+goal+`"}` || reqs[0].IdempotencyKey == "" {
		t.Fatalf("requests = %+v", reqs)
	}

	r := exec(t, target.NewCrew, api.URL, "", false, "generation", "41")
	if r.exit != 0 || !strings.Contains(r.stdout, "41") || !strings.Contains(r.stdout, "succeeded") || !strings.Contains(r.stdout, "35") {
		t.Fatalf("generation: exit %d stdout:\n%s", r.exit, r.stdout)
	}
	exec(t, target.NewCrew, api.URL, "", true, "generation", "99").failed(t, 5, "crewGenerationNotFound")
}

func TestCrewGenerateWaitSucceeded(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.GenerationStatuses("running", "running", "running", "succeeded")
	r := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal, "--wait", fastPoll, "--idempotency-key", "gen-1")
	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s", r.exit, r.stdout)
	}
	if got := strings.Join(generationStatuses(r), ","); got != "41:running,41:succeeded" {
		t.Fatalf("cli.status = %s, want each change once", got)
	}
	if results := r.linesOfType("cli.result"); len(results) != 1 || r.lines[len(r.lines)-1]["type"] != "cli.result" {
		t.Fatalf("want exactly one final cli.result:\n%s", r.stdout)
	}
	data, _ := r.lines[len(r.lines)-1]["data"].(map[string]any)
	if crew, _ := data["crew"].(map[string]any); data["status"] != "succeeded" || crew["id"] != "35" {
		t.Fatalf("data = %v, want the finished job with its crew", data)
	}
	if reqs := api.Requests(); reqs[0].IdempotencyKey != "gen-1" {
		t.Fatalf("key = %q", reqs[0].IdempotencyKey)
	}

	api.GenerationStatuses("running", "succeeded")
	r = exec(t, target.NewCrew, api.URL, "", false, "generate", "--goal", goal+" 2", "--wait", fastPoll)
	if r.exit != 0 || !strings.Contains(r.stdout, "42: running") || !strings.Contains(r.stdout, "succeeded") {
		t.Fatalf("human: exit %d stdout:\n%s", r.exit, r.stdout)
	}
}

func TestCrewGenerateWaitFailed(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.GenerationStatuses("running", "failed")
	api.GenerationError("crew_deploy_failed")
	r := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal, "--wait", fastPoll)
	data := r.failed(t, 8, "crewDeployFailed")
	if crew, _ := data["crew"].(map[string]any); data["id"] != "41" || crew["id"] != "35" {
		t.Fatalf("data = %v, want the failed job with the crew it left", data)
	}
	if got := strings.Join(generationStatuses(r), ","); got != "41:running,41:failed" {
		t.Fatalf("cli.status = %s", got)
	}

	api.GenerationError("crew_generation_failed")
	exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", "다른 목표", "--wait", fastPoll).failed(t, 8, "crewGenerationFailed")
}

func TestCrewGenerateWaitTimeoutLeavesJobRunning(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.GenerationStatuses("running")
	data := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal, "--wait", "--timeout", "100ms", fastPoll).
		failed(t, 75, "crewGenerationRunning")
	if data["id"] != "41" || data["status"] != "running" {
		t.Fatalf("data = %v, want the running job", data)
	}
}

func TestCrewGenerateWaitInterrupt(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.GenerationStatuses("running")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	data := execCtx(t, ctx, target.NewCrew, api.URL, "", true, "generate", "--goal", goal, "--wait", fastPoll).failed(t, 130, "interrupted")
	if data["id"] != "41" {
		t.Fatalf("data = %v, want the job that keeps running", data)
	}
}

// A failed poll is retried; polls that keep failing give up without
// claiming an outcome, keeping the job to look up later.
func TestCrewGenerateWaitPollFailures(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.GenerationStatuses("running", "succeeded")
	api.FailGenerationGets(fakeapi.Failure{Status: 503, Code: "upstream_unavailable"})
	if r := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal, "--wait", fastPoll); r.exit != 0 || len(api.Requests()) != 3 {
		t.Fatalf("exit %d requests %d, want success after a retried poll\n%s", r.exit, len(api.Requests()), r.stdout)
	}

	api = fakeapi.New(t, "key-1")
	api.GenerationStatuses("running")
	api.FailGenerationGets(
		fakeapi.Failure{Status: 503, Code: "upstream_unavailable"},
		fakeapi.Failure{Status: 503, Code: "upstream_unavailable"},
		fakeapi.Failure{Status: 503, Code: "upstream_unavailable"},
	)
	data := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal, "--wait", fastPoll).failed(t, 7, "crewGenerationStatusUnknown")
	if data["id"] != "41" || data["status"] != "running" {
		t.Fatalf("data = %v, want the last known job", data)
	}
}

func TestCrewGenerateUsageErrors(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no goal":              {"generate"},
		"blank goal":           {"generate", "--goal", "  "},
		"timeout without wait": {"generate", "--goal", goal, "--timeout", "1m"},
		"negative timeout":     {"generate", "--goal", goal, "--wait", "--timeout", "-1s"},
		"zero poll":            {"generate", "--goal", goal, "--wait", "--poll-interval", "0s"},
		"argument":             {"generate", "goal", "--goal", goal},
		"bad key":              {"generate", "--goal", goal, "--idempotency-key", "a,b"},
		"generation no id":     {"generation"},
		"generation bad id":    {"generation", "g57"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := fakeapi.New(t, "key-1")
			exec(t, target.NewCrew, api.URL, "", true, args...).failed(t, 2, "usage")
			if reqs := api.Requests(); len(reqs) != 0 {
				t.Fatalf("requests sent: %+v", reqs)
			}
		})
	}
}

func TestCrewGenerateInProgressElsewhere(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.GenerationStatuses("running")
	exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal).ok(t)
	data := exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", "다른 목표", "--idempotency-key", "gen-2").failed(t, 75, "crewGenerationInProgress")
	if data["id"] != "41" || data["idempotencyKey"] != "gen-2" {
		t.Fatalf("data = %v, want the running job id and the key", data)
	}
	// The same goal while running returns that job (200).
	if data = exec(t, target.NewCrew, api.URL, "", true, "generate", "--goal", goal).ok(t); data["id"] != "41" {
		t.Fatalf("data = %v", data)
	}
}
