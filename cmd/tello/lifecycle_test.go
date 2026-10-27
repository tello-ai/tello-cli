package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cli"
)

func TestCreateInterruptedWhilePostInFlight(t *testing.T) {
	for _, response := range []string{"created", "lost", "timeout"} {
		t.Run(response, func(t *testing.T) {
			started := make(chan string, 1)
			release := make(chan struct{})
			var posts atomic.Int32
			ended := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/calls":
					started <- r.Header.Get("Idempotency-Key")
					posts.Add(1)
					<-release
					if response == "lost" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = conn.Close()
						}
						return
					}
					fmt.Fprint(w, `{"id":"c_1","status":"dialing"}`)
				case "/v1/calls/c_1/end":
					ended <- r.Method
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprint(w, `{"id":"c_1","status":"inProgress"}`)
				case "/v1/calls/c_1":
					fmt.Fprint(w, `{"id":"c_1","status":"cancelled"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stdout, stderr bytes.Buffer
			a := &app.App{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Getenv: os.Getenv,
				APIKey: func() (app.Credential, error) { return app.Credential{Key: "key"}, nil }}
			done := make(chan int, 1)
			args := []string{"--json", "--api-url", server.URL + "/v1", "call", "create", "--agent", "1", "--to", "+82", "--wait", "--poll-interval=10ms", "--idempotency-key=original"}
			if response == "timeout" {
				args = append(args, "--timeout=30ms")
			}
			go func() { done <- cli.Main(ctx, a, args) }()
			if key := <-started; key != "original" {
				t.Fatalf("key = %q", key)
			}
			if response == "timeout" {
				time.Sleep(150 * time.Millisecond)
			} else {
				cancel()
			}
			close(release)
			select {
			case code := <-done:
				if response == "timeout" && code != 6 {
					t.Fatalf("exit %d: %s", code, stdout.String())
				}
				if response != "timeout" && code != 130 {
					t.Fatalf("exit %d: %s", code, stdout.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("CLI did not finish")
			}
			var last map[string]any
			if posts.Load() != 1 {
				t.Fatalf("POST count = %d, want 1", posts.Load())
			}
			for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
				if err := json.Unmarshal([]byte(line), &last); err != nil {
					t.Fatalf("invalid output %q: %v", line, err)
				}
			}
			data, _ := last["data"].(map[string]any)
			if data["idempotencyKey"] != "original" {
				t.Fatalf("missing key in %v", last)
			}
			if response != "lost" {
				select {
				case method := <-ended:
					if method != http.MethodPost {
						t.Fatalf("end method %s", method)
					}
				default:
					t.Fatal("call was not ended")
				}
				call, _ := data["call"].(map[string]any)
				if call["id"] != "c_1" {
					t.Fatalf("missing call in %v", data)
				}
			} else {
				if !strings.Contains(stdout.String(), "may") {
					t.Fatalf("missing uncertainty warning: %s", stdout.String())
				}
				select {
				case <-ended:
					t.Fatal("ended unknown call")
				default:
				}
			}
		})
	}
}
