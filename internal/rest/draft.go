package rest

import (
	"context"
	"net/http"
	"net/url"

	"github.com/tello-ai/tello-cli/internal/app"
)

// CallDraft is the POST /call-drafts body: the agent request of POST /calls
// without crewId.
type CallDraft struct {
	AgentID   string         `json:"agentId"`
	To        string         `json:"to"`
	Variables map[string]any `json:"variables,omitempty"`
	PerCall
}

// CreateCallDraft asks for a call scenario to review before dialing. It
// usually stores a draft (pendingConfirmation) and dials nothing, but an
// agent with a ready scenario is called at once (status confirmed, callId),
// so it is sent like a call create.
func (c *Client) CreateCallDraft(ctx context.Context, body CallDraft, idempotencyKey string) (Object, error) {
	draft, err := c.do(ctx, opCreate, http.MethodPost, "/call-drafts", nil, body, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return validateDraft(draft)
}

// ReviseCallDraft asks for a new draft with request applied; the draft
// revised can no longer be confirmed.
func (c *Client) ReviseCallDraft(ctx context.Context, draftID, request, idempotencyKey string) (Object, error) {
	draft, err := c.do(ctx, opDraftRevise, http.MethodPost, "/call-drafts/"+url.PathEscape(draftID)+"/revise", nil, map[string]string{"request": request}, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return validateDraft(draft)
}

// ConfirmCallDraft places the call of a draft and returns it. The API
// requires idempotencyKey; resending with it replays the placed call.
func (c *Client) ConfirmCallDraft(ctx context.Context, draftID, idempotencyKey string) (Object, error) {
	call, err := c.do(ctx, opCreate, http.MethodPost, "/call-drafts/"+url.PathEscape(draftID)+"/confirm", nil, nil, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return validateCall(call, "")
}

func validateDraft(draft Object) (Object, error) {
	if Status(draft) == "" {
		return nil, app.NewError(app.KindServer, "invalidResponse", "invalid call draft response; a call may have been placed, check the call list before retrying")
	}
	return draft, nil
}
