package fakeapi

import (
	"fmt"
	"net/http"
	"strings"
)

type draft struct {
	obj        map[string]any
	superseded bool
	callID     string // set once confirmed
}

type drafts struct {
	status string
	items  map[string]*draft
	nextID int
}

func newDrafts() drafts {
	return drafts{status: "pendingConfirmation", items: map[string]*draft{}, nextID: 480}
}

// DraftStatus sets the status new drafts report: pendingConfirmation (the
// default, a stored draft), needsClarification (no draft) or confirmed (a
// call is placed at once).
func (s *Server) DraftStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts.status = status
}

// serveDraft answers POST /call-drafts, /call-drafts/{id}/revise and
// /call-drafts/{id}/confirm. It reports whether path was one of them.
func (s *Server) serveDraft(w http.ResponseWriter, r *http.Request, path string) bool {
	rest, ok := strings.CutPrefix(path, "/call-drafts")
	if !ok || r.Method != http.MethodPost {
		return false
	}
	body, ok := s.record(w, r, path)
	if !ok {
		return true
	}
	if rest == "" {
		switch {
		case body["crewId"] != nil:
			writeFailure(w, Failure{Status: 422, Code: "unsupported_for_crew", Data: map[string]any{"fields": []string{"crewId"}}})
		case body["agentId"] == nil:
			writeFailure(w, Failure{Status: 400, Code: "target_required"})
		default:
			s.idempotent(w, r, path, body, func() (int, any) { return 201, s.newDraft(body, 1) })
		}
		return true
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	d := s.drafts.items[id]
	switch {
	case action == "confirm" && r.Header.Get("Idempotency-Key") == "":
		writeFailure(w, Failure{Status: 400, Code: "idempotency_key_required"})
	case s.replay(w, r, path, body):
	case d == nil:
		writeFailure(w, Failure{Status: 404, Code: "draft_not_found"})
	case action == "revise":
		if req, _ := body["request"].(string); strings.TrimSpace(req) == "" {
			writeFailure(w, Failure{Status: 400, Code: "invalid_request"})
			return true
		}
		if d.superseded {
			writeFailure(w, Failure{Status: 409, Code: "draft_superseded"})
			return true
		}
		s.idempotent(w, r, path, body, func() (int, any) {
			d.superseded = true
			return 200, s.newDraft(nil, d.obj["revision"].(int)+1)
		})
	case action == "confirm":
		switch {
		case d.superseded:
			writeFailure(w, Failure{Status: 409, Code: "draft_superseded"})
		case d.callID != "":
			writeFailure(w, Failure{Status: 409, Code: "draft_already_confirmed", Data: map[string]any{"callId": d.callID}})
		default:
			s.idempotent(w, r, path, body, func() (int, any) {
				c := s.newCall()
				d.callID = c.id
				return 201, s.object(c)
			})
		}
	default:
		writeFailure(w, Failure{Status: 404, Code: "route_not_found"})
	}
	return true
}

func (s *Server) newDraft(body map[string]any, revision int) map[string]any {
	obj := map[string]any{
		"id": nil, "status": s.drafts.status, "agentId": "12", "to": "*******5678",
		"preview": map[string]any{"summary": nil}, "question": nil, "excludedContent": []any{},
		"revision": nil, "revisionsLeft": nil, "expiresAt": nil, "callId": nil,
	}
	switch s.drafts.status {
	case "pendingConfirmation":
		s.drafts.nextID++
		id := fmt.Sprintf("cd_%d", s.drafts.nextID)
		obj["id"], obj["revision"], obj["revisionsLeft"] = id, revision, 5-revision
		obj["preview"] = map[string]any{"summary": fmt.Sprintf("시나리오 %d판", revision)}
		obj["expiresAt"] = "2026-11-12T01:10:00.000Z"
		s.drafts.items[id] = &draft{obj: obj}
	case "needsClarification":
		obj["question"] = "몇 시로 바꿀까요?"
	case "confirmed":
		obj["callId"] = s.newCall().id
	}
	return obj
}
