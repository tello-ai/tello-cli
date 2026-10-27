package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tello-ai/tello-cli/internal/app"
)

type deadlineTransport struct {
	t   *testing.T
	min time.Duration
	max time.Duration
}

func (d deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining < d.min || remaining > d.max {
		d.t.Errorf("request deadline remaining %v (set=%v), want %v..%v", remaining, ok, d.min, d.max)
	}
	return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"id":"c_1","status":"dialing"}`)), Header: make(http.Header), Request: req}, nil
}

func TestCreateTimeoutAndCallerDeadline(t *testing.T) {
	c, err := NewWithKey(&app.App{APIURL: "http://localhost/v1"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	c.createHTTP.Transport = deadlineTransport{t: t, min: 90 * time.Second, max: 3 * time.Minute}
	if _, err := c.CreateCall(context.Background(), CreateCall{AgentID: "1"}, "key"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c.createHTTP.Transport = deadlineTransport{t: t, min: 5 * time.Second, max: 10 * time.Second}
	if _, err := c.CreateCall(ctx, CreateCall{AgentID: "1"}, "key"); err != nil {
		t.Fatal(err)
	}
}
