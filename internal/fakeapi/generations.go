package fakeapi

import (
	"net/http"
	"strconv"
	"strings"
)

// generation is one crew generation job. It advances through its scripted
// statuses, one step per GET; the last one repeats.
type generation struct {
	id       string
	goal     string
	statuses []string
}

// GenerationStatuses scripts the statuses each new crew generation job
// reports: the POST response, then one per GET; the last one repeats. The
// default is "running", then "succeeded".
func (s *Server) GenerationStatuses(statuses ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen.statuses = statuses
}

// GenerationError sets the error.code a failed job reports (default
// crew_generation_failed).
func (s *Server) GenerationError(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen.errorCode = code
}

// FailGenerationGets makes the next GET /crew-generations/{id} fail, in
// order.
func (s *Server) FailGenerationGets(f ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen.getFailures = append(s.gen.getFailures, f...)
}

type generations struct {
	statuses    []string
	errorCode   string
	getFailures []Failure
	jobs        map[string]*generation
}

func newGenerations() generations {
	return generations{statuses: []string{"running", "succeeded"}, errorCode: "crew_generation_failed", jobs: map[string]*generation{}}
}

// serveGeneration answers POST /crews/generate and GET /crew-generations/{id}.
// It reports whether path was one of them.
func (s *Server) serveGeneration(w http.ResponseWriter, r *http.Request, path string) bool {
	id, isGet := strings.CutPrefix(path, "/crew-generations/")
	switch {
	case r.Method == http.MethodPost && path == "/crews/generate":
	case r.Method == http.MethodGet && isGet:
	default:
		return false
	}
	body, ok := s.record(w, r, path)
	if !ok {
		return true
	}
	if isGet {
		if len(s.gen.getFailures) > 0 {
			writeFailure(w, s.gen.getFailures[0])
			s.gen.getFailures = s.gen.getFailures[1:]
			return true
		}
		job := s.gen.jobs[id]
		if job == nil {
			writeFailure(w, Failure{Status: 404, Code: "crew_generation_not_found"})
			return true
		}
		if len(job.statuses) > 1 {
			job.statuses = job.statuses[1:]
		}
		writeJSON(w, 200, s.job(job))
		return true
	}
	goal, _ := body["goal"].(string)
	if strings.TrimSpace(goal) == "" {
		writeFailure(w, Failure{Status: 400, Code: "invalid_crew"})
		return true
	}
	for _, job := range s.gen.jobs {
		if job.statuses[0] != "running" {
			continue
		}
		if job.goal != goal {
			writeFailure(w, Failure{Status: 409, Code: "crew_generation_in_progress", Data: map[string]any{"id": job.id}})
		} else {
			writeJSON(w, 200, s.job(job)) // the same goal returns the running job
		}
		return true
	}
	s.idempotent(w, r, path, body, func() (int, any) {
		s.res.nextID++
		job := &generation{id: strconv.Itoa(s.res.nextID), goal: goal, statuses: append([]string(nil), s.gen.statuses...)}
		s.gen.jobs[job.id] = job
		return 202, s.job(job)
	})
	return true
}

func (s *Server) job(g *generation) map[string]any {
	status := g.statuses[0]
	obj := map[string]any{
		"id": g.id, "status": status, "goal": g.goal, "crew": nil, "error": nil,
		"createdAt": "2026-11-12T01:00:00.000Z", "updatedAt": "2026-11-12T01:00:00.000Z",
	}
	switch status {
	case "succeeded":
		obj["crew"] = map[string]any{"id": "35", "name": "치과 예약 확인", "requiredVariables": []string{"고객명"}}
	case "failed":
		obj["error"] = map[string]any{"code": s.gen.errorCode, "message": "fake " + s.gen.errorCode}
		if s.gen.errorCode != "crew_generation_failed" {
			obj["crew"] = map[string]any{"id": "35", "name": "치과 예약 확인"}
		}
	}
	return obj
}
