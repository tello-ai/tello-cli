// Package fakeapi is a scripted stand-in for the Tello public REST API (/v1)
// used by tests. Calls advance through a scripted status sequence, one step
// per GET; ending a live call makes it cancelled. Transfers are recorded and
// leave the call as it is, like the real API while the transfer runs.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Failure is an error envelope the server answers with.
type Failure struct {
	Status int
	Code   string
	Data   any
}

// Create is one recorded POST /v1/calls.
type Create struct {
	Body           map[string]any
	IdempotencyKey string
}

// Transfer is one recorded POST /v1/calls/{id}/transfer.
type Transfer struct {
	CallID         string
	Body           map[string]any
	IdempotencyKey string
}

type call struct {
	id       string
	statuses []string // remaining scripted statuses; the last one repeats
	ended    bool
}

// Server is a running fake API. URL is its /v1 base.
type Server struct {
	URL string

	key string

	mu             sync.Mutex
	agents, crews  []map[string]any
	statuses       []string
	createFailures []Failure
	getFailures    []Failure
	transferFails  []Failure
	creates        []Create
	ends           []string
	transfers      []Transfer
	listQueries    []url.Values
	listPage       map[string]any
	calls          map[string]*call
	byKey          map[string]string
	nextID         int
}

// New starts a fake API that accepts only key. New calls report "dialing",
// then "completed" unless Statuses says otherwise.
func New(t testing.TB, key string) *Server {
	t.Helper()
	s := &Server{
		key:      key,
		statuses: []string{"dialing", "completed"},
		calls:    map[string]*call{},
		byKey:    map[string]string{},
		listPage: map[string]any{"data": []any{}, "nextCursor": nil},
	}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/v1"
	return s
}

// Statuses scripts the statuses each new call reports: the create response,
// then one per GET; the last one repeats.
func (s *Server) Statuses(statuses ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = statuses
}

// Targets sets the agent and crew catalogs.
func (s *Server) Targets(agents, crews []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents, s.crews = agents, crews
}

// FailCreates makes the next creates fail, in order.
func (s *Server) FailCreates(f ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createFailures = append(s.createFailures, f...)
}

// FailGets makes the next GET /calls/{id} fail, in order.
func (s *Server) FailGets(f ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getFailures = append(s.getFailures, f...)
}

// FailTransfers makes the next transfers fail, in order.
func (s *Server) FailTransfers(f ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transferFails = append(s.transferFails, f...)
}

// ListPage sets the GET /calls response.
func (s *Server) ListPage(page map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listPage = page
}

// Creates returns the recorded create requests, failed ones included.
func (s *Server) Creates() []Create {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Create(nil), s.creates...)
}

// Ends returns the call ids POST /calls/{id}/end was called for.
func (s *Server) Ends() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ends...)
}

// Transfers returns the recorded transfer requests, refused ones included.
func (s *Server) Transfers() []Transfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Transfer(nil), s.transfers...)
}

// ListQueries returns the recorded GET /calls queries.
func (s *Server) ListQueries() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.listQueries...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+s.key {
		writeFailure(w, Failure{Status: 401, Code: "unauthenticated"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	switch {
	case r.Method == http.MethodGet && path == "/agents":
		writeJSON(w, 200, map[string]any{"data": orEmpty(s.agents)})
	case r.Method == http.MethodGet && path == "/crews":
		writeJSON(w, 200, map[string]any{"data": orEmpty(s.crews)})
	case r.Method == http.MethodPost && path == "/calls":
		s.create(w, r)
	case r.Method == http.MethodGet && path == "/calls":
		s.listQueries = append(s.listQueries, r.URL.Query())
		writeJSON(w, 200, s.listPage)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/calls/"):
		if len(s.getFailures) > 0 {
			f := s.getFailures[0]
			s.getFailures = s.getFailures[1:]
			writeFailure(w, f)
			return
		}
		c := s.calls[strings.TrimPrefix(path, "/calls/")]
		if c == nil {
			writeFailure(w, Failure{Status: 404, Code: "call_not_found"})
			return
		}
		if len(c.statuses) > 1 && !c.ended {
			c.statuses = c.statuses[1:]
		}
		writeJSON(w, 200, s.object(c))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/calls/") && strings.HasSuffix(path, "/end"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/calls/"), "/end")
		s.ends = append(s.ends, id)
		c := s.calls[id]
		switch {
		case c == nil:
			writeFailure(w, Failure{Status: 404, Code: "call_not_found"})
		case terminal(c.statuses[0]):
			writeFailure(w, Failure{Status: 409, Code: "call_not_active"})
		default:
			obj := s.object(c)
			c.ended, c.statuses = true, []string{"cancelled"}
			writeJSON(w, 202, obj)
		}
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/calls/") && strings.HasSuffix(path, "/transfer"):
		s.transfer(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/calls/"), "/transfer"))
	default:
		writeFailure(w, Failure{Status: 404, Code: "route_not_found"})
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	key := r.Header.Get("Idempotency-Key")
	s.creates = append(s.creates, Create{Body: body, IdempotencyKey: key})
	if len(s.createFailures) > 0 {
		f := s.createFailures[0]
		s.createFailures = s.createFailures[1:]
		writeFailure(w, f)
		return
	}
	if id, ok := s.byKey[key]; ok && key != "" {
		writeJSON(w, 201, s.object(s.calls[id]))
		return
	}
	s.nextID++
	c := &call{id: fmt.Sprintf("c_%d", s.nextID), statuses: append([]string(nil), s.statuses...)}
	s.calls[c.id] = c
	if key != "" {
		s.byKey[key] = c.id
	}
	writeJSON(w, 201, s.object(c))
}

func (s *Server) transfer(w http.ResponseWriter, r *http.Request, id string) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	key := r.Header.Get("Idempotency-Key")
	s.transfers = append(s.transfers, Transfer{CallID: id, Body: body, IdempotencyKey: key})
	c := s.calls[id]
	switch {
	case len(s.transferFails) > 0:
		writeFailure(w, s.transferFails[0])
		s.transferFails = s.transferFails[1:]
	case key == "":
		writeFailure(w, Failure{Status: 400, Code: "idempotency_key_required"})
	case c == nil:
		writeFailure(w, Failure{Status: 404, Code: "call_not_found"})
	case terminal(c.statuses[0]):
		writeFailure(w, Failure{Status: 409, Code: "call_not_active"})
	case c.statuses[0] != "inProgress":
		writeFailure(w, Failure{Status: 409, Code: "call_not_in_progress"})
	default:
		writeJSON(w, 202, s.object(c))
	}
}

func (s *Server) object(c *call) map[string]any {
	status := c.statuses[0]
	obj := map[string]any{
		"id": c.id, "status": status, "direction": "outbound", "source": "api",
		"agentId": "12", "crewId": nil, "createdAt": "2026-10-20T01:00:00.000Z",
		"startedAt": nil, "endedAt": nil, "durationSeconds": nil, "endReason": nil,
		"transcript": nil, "summary": nil, "creditCharged": nil,
	}
	if terminal(status) {
		obj["endedAt"] = "2026-10-20T01:01:08.000Z"
		if status == "completed" {
			obj["durationSeconds"], obj["summary"], obj["transcript"] = 60, "Booked.", "user: hi"
		}
	}
	return obj
}

func terminal(status string) bool {
	switch status {
	case "completed", "noAnswer", "failed", "cancelled":
		return true
	}
	return false
}

func orEmpty(v []map[string]any) []map[string]any {
	if v == nil {
		return []map[string]any{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeFailure(w http.ResponseWriter, f Failure) {
	writeJSON(w, f.Status, map[string]any{"statusCode": f.Status, "message": "fake " + f.Code, "error": f.Code, "data": f.Data})
}
