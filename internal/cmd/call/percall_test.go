package call_test

import (
	"testing"

	"github.com/tello-ai/tello-cli/internal/fakeapi"
)

func TestCreateSendsPerCallSettings(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	r := runAPI(t, api.URL, "", true, "create", "--agent", "12", "--to", "+821012345678",
		"--instructions", "내일 오후 2시 예약을 확인해 주세요", "--max-duration-seconds", "300",
		"--ivr-goal", "상담원 연결", "--ivr-opening-message", "예약 확인차 연락드렸습니다", "--notify-on-end")
	if r.exit != 0 {
		t.Fatalf("exit %d stdout:\n%s", r.exit, r.stdout)
	}
	want := `{"agentId":"12","instructions":"내일 오후 2시 예약을 확인해 주세요","ivr":{"goal":"상담원 연결","openingMessage":"예약 확인차 연락드렸습니다"},"maxDurationSeconds":300,"notifyOnEnd":true,"to":"+821012345678"}`
	if got := jsonOf(api.Creates()[0].Body); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}

	r = runAPI(t, api.URL, "", true, "create", "--crew", "34", "--to", "+82", "--max-duration-seconds", "1800", "--ivr-goal", "")
	if r.exit != 0 {
		t.Fatalf("crew exit %d stdout:\n%s", r.exit, r.stdout)
	}
	if got := jsonOf(api.Creates()[1].Body); got != `{"crewId":"34","maxDurationSeconds":1800,"to":"+82"}` {
		t.Fatalf("crew body = %s", got)
	}
}

func TestCreateInstructionRefusals(t *testing.T) {
	t.Parallel()
	api := fakeapi.New(t, "key-1")
	api.FailCreates(
		fakeapi.Failure{Status: 422, Code: "call_refused", Data: map[string]any{"detectedIntent": "debt_collection"}},
		fakeapi.Failure{Status: 422, Code: "instructions_unclear", Data: map[string]any{"question": "몇 시로 바꿀까요?"}},
		fakeapi.Failure{Status: 422, Code: "notify_unavailable", Data: map[string]any{"reason": "notify_disabled"}},
	)
	args := []string{"create", "--agent", "12", "--to", "+82", "--instructions", "x"}
	if data := runAPI(t, api.URL, "", true, args...).failed(t, 5, "callRefused"); data["detectedIntent"] != "debt_collection" || data["idempotencyKey"] == nil {
		t.Fatalf("data = %v", data)
	}
	if data := runAPI(t, api.URL, "", true, args...).failed(t, 5, "instructionsUnclear"); data["question"] != "몇 시로 바꿀까요?" {
		t.Fatalf("data = %v", data)
	}
	if data := runAPI(t, api.URL, "", true, append(args, "--notify-on-end")...).failed(t, 5, "notifyUnavailable"); data["reason"] != "notify_disabled" {
		t.Fatalf("data = %v", data)
	}
}

func TestCreatePerCallUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"crew instructions":      {"create", "--to", "+82", "--crew", "34", "--instructions", "x"},
		"crew ivr goal":          {"create", "--to", "+82", "--crew", "34", "--ivr-goal", "x"},
		"crew opening message":   {"create", "--to", "+82", "--crew", "34", "--ivr-opening-message", "x"},
		"crew notify":            {"create", "--to", "+82", "--crew", "34", "--notify-on-end"},
		"duration too short":     {"create", "--to", "+82", "--agent", "12", "--max-duration-seconds", "29"},
		"duration too long":      {"create", "--to", "+82", "--agent", "12", "--max-duration-seconds", "1801"},
		"interactive duration":   {"create", "--to", "+82", "-i", "--max-duration-seconds", "300"},
		"interactive notify":     {"create", "--to", "+82", "-i", "--notify-on-end"},
		"handler instructions":   {"create", "--to", "+82", "--instructions", "x", "--", "bot"},
		"handler ivr goal":       {"create", "--to", "+82", "--ivr-goal", "x", "--", "bot"},
		"handler opening":        {"create", "--to", "+82", "--ivr-opening-message", "x", "--", "bot"},
		"handler duration":       {"create", "--to", "+82", "--max-duration-seconds", "300", "--", "bot"},
		"interactive ivr goal":   {"create", "--to", "+82", "-i", "--ivr-goal", "x"},
		"interactive instructio": {"create", "--to", "+82", "-i", "--instructions", "x"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := fakeapi.New(t, "key-1")
			runAPI(t, api.URL, "", true, args...).failed(t, 2, "usage")
			if len(api.Creates()) != 0 {
				t.Fatalf("request sent for invalid input")
			}
		})
	}
}
