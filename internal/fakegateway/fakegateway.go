// Package fakegateway is a scripted stand-in for the Tello /sdk WebSocket
// gateway (sdk-ws.v1) used by tests. Each accepted connection runs the
// script given to New on its own goroutine.
package fakegateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Server is a running fake gateway. URL is its ws:// endpoint.
type Server struct {
	URL string

	t      testing.TB
	server *httptest.Server
	wg     sync.WaitGroup
}

// New starts a gateway whose connections run script. The server shuts down
// at test cleanup, after every script returns.
func New(t testing.TB, script func(c *Conn)) *Server {
	t.Helper()
	s := &Server{t: t}
	upgrader := websocket.Upgrader{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("fakegateway: upgrade: %v", err)
			return
		}
		s.wg.Add(1)
		defer s.wg.Done()
		c := &Conn{t: t, ws: ws, Query: r.URL.Query().Encode()}
		defer ws.Close()
		script(c)
	}))
	s.URL = "ws" + strings.TrimPrefix(s.server.URL, "http") + "/sdk"
	t.Cleanup(func() {
		s.server.CloseClientConnections()
		s.server.Close()
		s.wg.Wait()
	})
	return s
}

// Conn is one client connection seen from the gateway.
type Conn struct {
	// Query is the upgrade URL's encoded query (client identity pairs).
	Query string

	t  testing.TB
	ws *websocket.Conn
}

// ReadTimeout bounds every Read so a broken test fails instead of hanging.
const ReadTimeout = 5 * time.Second

// Read returns the next command frame ({"event", "data"}), or nil when the
// client closed the connection or ReadTimeout elapsed.
func (c *Conn) Read() map[string]any {
	_ = c.ws.SetReadDeadline(time.Now().Add(ReadTimeout))
	var frame map[string]any
	if err := c.ws.ReadJSON(&frame); err != nil {
		return nil
	}
	return frame
}

// ReadEvent reads the next command frame and fails the test unless its
// event is want. It returns the frame's data object.
func (c *Conn) ReadEvent(want string) map[string]any {
	c.t.Helper()
	frame := c.Read()
	if frame == nil {
		c.t.Errorf("fakegateway: expected %q command, connection ended", want)
		return nil
	}
	if frame["event"] != want {
		c.t.Errorf("fakegateway: expected %q command, got %v", want, frame)
		return nil
	}
	data, _ := frame["data"].(map[string]any)
	return data
}

// Authenticate reads the auth frame and answers auth.ok when its token is
// key, otherwise rejects it like the gateway (error frame + close 4401).
// It returns whether the client is authenticated.
func (c *Conn) Authenticate(key string) bool {
	c.t.Helper()
	data := c.ReadEvent("auth")
	if data == nil {
		return false
	}
	if data["token"] != key {
		c.Send(map[string]any{"type": "error", "version": "1.0", "code": "unauthenticated", "message": "Authentication required"})
		c.CloseWith(4401, "unauthenticated")
		return false
	}
	c.Send(map[string]any{"type": "auth.ok", "version": "1.0", "accountId": "acc-1"})
	return true
}

// Send writes a raw server frame.
func (c *Conn) Send(frame map[string]any) {
	if err := c.ws.WriteJSON(frame); err != nil {
		c.t.Logf("fakegateway: send %v: %v", frame["type"], err)
	}
}

// Event writes a call-stream event with the common fields filled in.
func (c *Conn) Event(eventType, callID string, fields map[string]any) {
	frame := map[string]any{
		"type":      eventType,
		"version":   "1.0",
		"sessionId": "session-1",
		"callId":    callID,
		"timestamp": "2026-10-01T00:00:00.000Z",
	}
	for k, v := range fields {
		frame[k] = v
	}
	c.Send(frame)
}

// Error writes an error frame answering requestID.
func (c *Conn) Error(code, message, requestID string) {
	frame := map[string]any{"type": "error", "version": "1.0", "code": code, "message": message}
	if requestID != "" {
		frame["requestId"] = requestID
	}
	c.Send(frame)
}

// CloseWith sends a close frame with code and reason.
func (c *Conn) CloseWith(code int, reason string) {
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
}

// Drop closes the TCP connection without a close frame.
func (c *Conn) Drop() {
	_ = c.ws.UnderlyingConn().Close()
}

// WaitClosed blocks until the client closes the connection (or
// ReadTimeout), discarding frames.
func (c *Conn) WaitClosed() {
	for c.Read() != nil {
	}
}
