//go:build unix

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestClosedStdoutEndsCall(t *testing.T) {
	if os.Getenv("TELLO_PIPE_TEST") == "1" {
		os.Args = []string{"tello", "--json", "--api-url", os.Getenv("TELLO_PIPE_API"), "call", "create", "--agent", "1", "--to", "+82", "--wait", "--poll-interval=30s"}
		main()
		return
	}
	ended := make(chan struct{}, 1)
	polled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/calls":
			fmt.Fprint(w, `{"id":"c_1","status":"dialing"}`)
		case "/v1/calls/c_1/end":
			ended <- struct{}{}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"c_1","status":"inProgress"}`)
		case "/v1/calls/c_1":
			polled <- struct{}{}
			fmt.Fprint(w, `{"id":"c_1","status":"cancelled"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestClosedStdoutEndsCall$")
	cmd.Env = append(os.Environ(), "TELLO_PIPE_TEST=1", "TELLO_PIPE_API="+server.URL+"/v1", "TELLO_API_KEY=key")
	cmd.Stdout = writer
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected output failure")
		}
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Fatalf("exit %v, stderr %s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("CLI did not finish")
	}
	select {
	case <-ended:
	default:
		t.Fatal("call was not ended")
	}
	select {
	case <-polled:
	default:
		t.Fatal("call was not polled immediately after end")
	}
}
