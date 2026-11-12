package fakeapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Request is one recorded request to the tool, agent and crew management,
// crew generation or call draft endpoints, failed ones included.
type Request struct {
	Method         string
	Path           string // without the /v1 prefix
	Query          url.Values
	Body           map[string]any
	IdempotencyKey string
}

// stored is a replayable response of an idempotent request.
type stored struct {
	request string // method, path and body the key was first used with
	status  int
	body    any
}

type resources struct {
	failures []Failure
	requests []Request
	items    map[string]map[string]map[string]any // kind → id → object
	byKey    map[string]stored
	nextID   int
}

func newResources() resources {
	return resources{
		items:  map[string]map[string]map[string]any{"tools": {}, "agents": {}, "crews": {}},
		byKey:  map[string]stored{},
		nextID: 40,
	}
}

// FailResources makes the next requests to the management, crew generation
// and call draft endpoints fail, in order.
func (s *Server) FailResources(f ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res.failures = append(s.res.failures, f...)
}

// Requests returns the recorded management, crew generation and call draft
// requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.res.requests...)
}

// Resource returns the stored tool, agent or crew (kind "tools", "agents" or
// "crews"), or nil.
func (s *Server) Resource(kind, id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.res.items[kind][id]
}

// serveResource answers the management, crew generation and call draft
// endpoints. It reports whether path was one of them.
func (s *Server) serveResource(w http.ResponseWriter, r *http.Request, path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	kind := parts[0]
	switch {
	case (kind == "agents" || kind == "crews") && len(parts) == 1 && r.Method == http.MethodGet:
		return false // the target catalogs
	case kind != "tools" && kind != "agents" && kind != "crews":
		return false
	}
	body, ok := s.record(w, r, path)
	if !ok {
		return true
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		s.listTools(w, r.URL.Query())
	case len(parts) == 1 && r.Method == http.MethodPost:
		s.idempotent(w, r, path, body, func() (int, any) { return s.createResource(kind, body) })
	case len(parts) == 2 && r.Method == http.MethodGet:
		s.withResource(w, kind, parts[1], func(obj map[string]any) { writeJSON(w, 200, obj) })
	case len(parts) == 2 && r.Method == http.MethodPatch:
		s.withResource(w, kind, parts[1], func(obj map[string]any) {
			revision, _ := obj["revision"].(int)
			for k, v := range body {
				obj[k] = v
			}
			obj["revision"] = revision + 1
			writeJSON(w, 200, obj)
		})
	case len(parts) == 2 && r.Method == http.MethodDelete:
		s.withResource(w, kind, parts[1], func(map[string]any) {
			delete(s.res.items[kind], parts[1])
			w.WriteHeader(http.StatusNoContent)
		})
	default:
		writeFailure(w, Failure{Status: 404, Code: "route_not_found"})
	}
	return true
}

// record logs the request and answers a scripted failure, if any. It
// reports whether the request should be served.
func (s *Server) record(w http.ResponseWriter, r *http.Request, path string) (map[string]any, bool) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	s.res.requests = append(s.res.requests, Request{
		Method: r.Method, Path: path, Query: r.URL.Query(), Body: body,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if len(s.res.failures) > 0 {
		f := s.res.failures[0]
		s.res.failures = s.res.failures[1:]
		writeFailure(w, f)
		return nil, false
	}
	return body, true
}

// idempotent replays the stored response of a key used before with the same
// request, refuses it for another request, or runs the request and stores a
// success.
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, path string, body map[string]any, run func() (int, any)) {
	key := r.Header.Get("Idempotency-Key")
	request := r.Method + " " + path + " " + jsonText(body)
	if prev, ok := s.res.byKey[key]; ok && key != "" {
		if prev.request != request {
			writeFailure(w, Failure{Status: 409, Code: "idempotency_key_reused"})
			return
		}
		writeJSON(w, prev.status, prev.body)
		return
	}
	status, resp := run()
	if key != "" && status >= 200 && status <= 299 {
		s.res.byKey[key] = stored{request: request, status: status, body: resp}
	}
	writeJSON(w, status, resp)
}

func (s *Server) createResource(kind string, body map[string]any) (int, any) {
	s.res.nextID++
	obj := map[string]any{}
	for k, v := range body {
		obj[k] = v
	}
	obj["id"] = strconv.Itoa(s.res.nextID)
	obj["revision"] = 1
	s.res.items[kind][obj["id"].(string)] = obj
	return 201, obj
}

func (s *Server) withResource(w http.ResponseWriter, kind, id string, found func(map[string]any)) {
	obj := s.res.items[kind][id]
	if obj == nil {
		writeFailure(w, Failure{Status: 404, Code: strings.TrimSuffix(kind, "s") + "_not_found"})
		return
	}
	found(obj)
}

// listTools pages tools by ascending id; the cursor is the last id returned.
func (s *Server) listTools(w http.ResponseWriter, q url.Values) {
	limit := 50
	if raw, ok := q["limit"]; ok {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 {
			writeFailure(w, Failure{Status: 400, Code: "invalid_query"})
			return
		}
		limit = n
	}
	after := 0
	if c := q.Get("cursor"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil {
			writeFailure(w, Failure{Status: 400, Code: "invalid_cursor"})
			return
		}
		after = n
	}
	var ids []int
	for id := range s.res.items["tools"] {
		if n, _ := strconv.Atoi(id); n > after {
			ids = append(ids, n)
		}
	}
	sort.Ints(ids)
	page := map[string]any{"data": []any{}, "nextCursor": nil}
	data := []any{}
	for i, n := range ids {
		if i == limit {
			page["nextCursor"] = strconv.Itoa(ids[i-1])
			break
		}
		data = append(data, s.res.items["tools"][strconv.Itoa(n)])
	}
	page["data"] = data
	writeJSON(w, 200, page)
}

func jsonText(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}
