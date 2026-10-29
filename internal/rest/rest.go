// Package rest is the client for the Tello public REST API (/v1): calls
// answered by a portal agent or crew, call lookup and control, and the
// target catalog. Responses are kept as decoded JSON objects so fields the
// CLI does not know yet still reach --json output.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/version"
)

// Object is a JSON object decoded with exact numbers (json.Number).
type Object = map[string]any

// Creation can include the server's 90-second processing window and
// additional network latency. Other requests remain short.
const (
	requestTimeout = 30 * time.Second
	createTimeout  = 2 * time.Minute
)

// maxBody caps how much of a response is read.
const maxBody = 8 << 20

// Client calls the REST API with one API key.
type Client struct {
	base       string
	key        string
	http       *http.Client
	createHTTP *http.Client
}

// New builds a client for the effective API URL and the configured key.
func New(a *app.App) (*Client, error) {
	cred, err := a.Credential()
	if err != nil {
		return nil, err
	}
	return NewWithKey(a, cred.Key)
}

// NewWithKey builds a client for the effective API URL with key, e.g. to
// verify a key before it is stored.
func NewWithKey(a *app.App, key string) (*Client, error) {
	base := a.EffectiveAPIURL()
	if err := app.ValidateAPIURL(base); err != nil {
		return nil, err
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		base: base, key: key,
		http:       &http.Client{Timeout: requestTimeout, CheckRedirect: noRedirect},
		createHTTP: &http.Client{Timeout: createTimeout, CheckRedirect: noRedirect},
	}, nil
}

// CreateCall is the POST /calls body. Exactly one of AgentID and CrewID is
// set; the server rejects anything else with targetRequired.
type CreateCall struct {
	AgentID   string         `json:"agentId,omitempty"`
	CrewID    string         `json:"crewId,omitempty"`
	To        string         `json:"to"`
	Variables map[string]any `json:"variables,omitempty"`
}

// ListCalls is the GET /calls query; zero values are omitted.
type ListCalls struct {
	Limit         int
	Cursor        string
	Status        string
	AgentID       string
	CrewID        string
	CreatedAfter  string
	CreatedBefore string
}

func (q ListCalls) values() url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	set("cursor", q.Cursor)
	set("status", q.Status)
	set("agentId", q.AgentID)
	set("crewId", q.CrewID)
	set("createdAfter", q.CreatedAfter)
	set("createdBefore", q.CreatedBefore)
	return v
}

// CreateCall places a call. idempotencyKey makes retries of the same request
// return the first response instead of dialing again.
func (c *Client) CreateCall(ctx context.Context, body CreateCall, idempotencyKey string) (Object, error) {
	call, err := c.do(ctx, opCreate, http.MethodPost, "/calls", nil, body, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return validateCall(call, "")
}

// GetCall returns one call of the account.
func (c *Client) GetCall(ctx context.Context, callID string) (Object, error) {
	call, err := c.do(ctx, opRead, http.MethodGet, "/calls/"+url.PathEscape(callID), nil, nil, "")
	if err != nil {
		return nil, err
	}
	return validateCall(call, callID)
}

// EndCall asks the service to end a live call. The returned call is the
// state when the request was accepted, not the final one.
func (c *Client) EndCall(ctx context.Context, callID string) (Object, error) {
	call, err := c.do(ctx, opEnd, http.MethodPost, "/calls/"+url.PathEscape(callID)+"/end", nil, nil, "")
	if err != nil {
		return nil, err
	}
	return validateCall(call, callID)
}

// transferCall is the POST /calls/{id}/transfer body. The API supports only
// an immediate (cold) transfer to a phone number.
type transferCall struct {
	Mode        string              `json:"mode"`
	Destination transferDestination `json:"destination"`
}

type transferDestination struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

// TransferCall asks the service to hand a live call over to the phone number
// to. idempotencyKey is required by the API; resending an accepted request
// with the same key replays the stored 202 instead of transferring again.
// Refused requests are not stored, so the same key runs them again. The
// returned call is the state when the request was accepted; the call stays
// inProgress while the transfer runs.
func (c *Client) TransferCall(ctx context.Context, callID, to, idempotencyKey string) (Object, error) {
	body := transferCall{Mode: "cold", Destination: transferDestination{Type: "phone", Target: to}}
	call, err := c.do(ctx, opTransfer, http.MethodPost, "/calls/"+url.PathEscape(callID)+"/transfer", nil, body, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return validateCall(call, callID)
}

// ListCalls returns one page: {"data": [...], "nextCursor": string|null}.
func (c *Client) ListCalls(ctx context.Context, q ListCalls) (Object, error) {
	return c.do(ctx, opRead, http.MethodGet, "/calls", q.values(), nil, "")
}

// ListAgents returns {"data": [{"id","name","description"}]}.
func (c *Client) ListAgents(ctx context.Context) (Object, error) {
	result, err := c.do(ctx, opAgents, http.MethodGet, "/agents", nil, nil, "")
	if err != nil {
		return nil, err
	}
	if _, ok := result["data"].([]any); !ok {
		return nil, app.NewError(app.KindServer, "invalidResponse", "invalid agents response")
	}
	return result, nil
}

// ListCrews returns {"data": [{"id","name","description"}]}.
func (c *Client) ListCrews(ctx context.Context) (Object, error) {
	return c.do(ctx, opRead, http.MethodGet, "/crews", nil, nil, "")
}

// operation is what a request does. It picks the HTTP timeout, the success
// status the API documents and how a lost or failed response is reported.
type operation int

const (
	opRead     operation = iota // get, list, crews: no side effects
	opAgents                    // agent list, also the key check: exactly 200
	opCreate                    // places a call
	opEnd                       // asks to end a call: exactly 202
	opTransfer                  // asks to transfer a call: exactly 202
)

// wantStatus is the only success status accepted, or 0 for any 2xx.
func (op operation) wantStatus() int {
	switch op {
	case opAgents:
		return http.StatusOK
	case opEnd, opTransfer:
		return http.StatusAccepted
	}
	return 0
}

func (c *Client) do(ctx context.Context, op operation, method, path string, query url.Values, body any, idempotencyKey string) (Object, error) {
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, app.NewError(app.KindUsage, "invalidUrl", "could not build API request")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tello-cli/"+version.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	httpClient := c.http
	if op == opCreate {
		httpClient = c.createHTTP
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		switch op {
		case opCreate:
			return nil, app.NewError(app.KindConnection, "connectionFailed", "create request failed; call outcome may be unknown, check the call list before retrying")
		case opTransfer:
			return nil, app.NewError(app.KindConnection, "connectionFailed", "transfer request failed; the transfer may have been requested, check the call before retrying with the same idempotency key")
		}
		return nil, app.NewError(app.KindConnection, "connectionFailed", fmt.Sprintf("%s request failed", method))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		switch op {
		case opCreate:
			return nil, app.NewError(app.KindConnection, "connectionClosed", "create response could not be read; call outcome may be unknown, check the call list before retrying")
		case opTransfer:
			return nil, app.NewError(app.KindConnection, "connectionClosed", "transfer response could not be read; the transfer may have been requested, check the call before retrying with the same idempotency key")
		}
		return nil, app.NewError(app.KindConnection, "connectionClosed", "could not read response")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := apiError(resp.StatusCode, raw)
		if _, known := kinds[apiErr.Code]; !known {
			switch {
			case op == opCreate && resp.StatusCode == http.StatusServiceUnavailable:
				return nil, app.NewError(app.KindServer, "callOutcomeUnknown", "call outcome is unknown; check the call list before retrying")
			case op == opTransfer && resp.StatusCode >= 500:
				// Not a documented transfer error (e.g. a proxy page): the
				// service may have taken the request before it failed.
				return nil, app.NewError(app.KindServer, "transferOutcomeUnknown", fmt.Sprintf("transfer outcome is unknown (%s); the transfer may have been requested, check the call before retrying with the same idempotency key", apiErr.Message))
			}
		}
		return nil, apiErr
	}
	if want := op.wantStatus(); want != 0 && resp.StatusCode != want {
		return nil, app.NewError(app.KindServer, "invalidResponse", "unexpected API response status")
	}
	var obj Object
	if err := decode(raw, &obj); err != nil || obj == nil {
		return nil, app.NewError(app.KindServer, "invalidResponse", fmt.Sprintf("%s %s: response is not a JSON object", method, path))
	}
	return obj, nil
}

func decode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

// Status is a call object's public status ("" when absent).
func Status(call Object) string {
	s, _ := call["status"].(string)
	return s
}

// ID is a call object's id ("" when absent).
func ID(call Object) string {
	s, _ := call["id"].(string)
	return s
}

// validateCall prevents malformed or mismatched success responses from being
// mistaken for a completed call or a successful new dial.
func validateCall(call Object, expectedID string) (Object, error) {
	id := ID(call)
	validID := strings.HasPrefix(id, "c_") && len(id) > 2
	for _, c := range strings.TrimPrefix(id, "c_") {
		if c < '0' || c > '9' {
			validID = false
		}
	}
	validStatus := false
	switch Status(call) {
	case "dialing", "inProgress", "transferring", "completed", "noAnswer", "failed", "cancelled":
		validStatus = true
	}
	if !validID || !validStatus || (expectedID != "" && id != expectedID) {
		return nil, app.NewError(app.KindServer, "invalidResponse", "invalid call response; check the call list or call ID before retrying")
	}
	return call, nil
}

// Terminal reports whether a public status is final.
func Terminal(status string) bool {
	switch status {
	case "completed", "noAnswer", "failed", "cancelled":
		return true
	}
	return false
}

// kinds classifies the documented REST error codes (camelCased). Codes not
// listed fall back to the HTTP status in statusKind.
var kinds = map[string]app.Kind{
	"unauthenticated": app.KindAuth,

	// Account policy: fixing it takes a human, retrying does not help.
	"insufficientCredit":      app.KindRefused,
	"outboundLimitExceeded":   app.KindRefused,
	"calleeNotVerified":       app.KindRefused,
	"destinationBlocked":      app.KindRefused,
	"callerNumberUnavailable": app.KindRefused,

	// Refusals that clear by themselves; retry with the same key. A transfer
	// refused with voiceGatewayUnavailable can still go through late (a call
	// is transferred at most once), so check the call before resending it.
	"concurrentLimitExceeded": app.KindTemporary,
	"duplicateCall":           app.KindTemporary,
	"idempotencyInProgress":   app.KindTemporary,
	"creditCheckUnavailable":  app.KindTemporary,
	"callRecordUnavailable":   app.KindTemporary,
	"voiceGatewayUnavailable": app.KindTemporary,

	// Transfer of a call that is not inProgress yet (still dialing, or being
	// handed to a portal counselor): the same request can succeed later.
	"callNotInProgress": app.KindTemporary,

	// The call may have been placed: never retry with a new key.
	"callOutcomeUnknown": app.KindServer,
	"internalError":      app.KindServer,
}

func statusKind(status int) app.Kind {
	switch {
	case status == http.StatusUnauthorized:
		return app.KindAuth
	case status == http.StatusTooManyRequests, status == http.StatusServiceUnavailable:
		return app.KindTemporary
	case status >= 500:
		return app.KindServer
	}
	return app.KindInvalid
}

// apiError turns a non-2xx response into an *app.Error. The envelope's
// snake_case `error` becomes the camelCase code the rest of the CLI uses;
// its `data` (e.g. missingVariables) is reported with the error.
func apiError(status int, raw []byte) *app.Error {
	var env struct {
		Message string `json:"message"`
		Error   string `json:"error"`
		Data    any    `json:"data"`
	}
	if err := decode(raw, &env); err != nil || env.Error == "" {
		code := "http" + strconv.Itoa(status)
		return app.NewError(statusKind(status), code, fmt.Sprintf("HTTP %d %s", status, http.StatusText(status)))
	}
	code := camel(env.Error)
	kind, ok := kinds[code]
	if !ok {
		kind = statusKind(status)
	}
	e := app.NewError(kind, code, env.Message)
	if e.Message == "" {
		e.Message = code
	}
	if env.Data != nil {
		e.Data = env.Data
	}
	return e
}

// camel converts snake_case to camelCase.
func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if p := []rune(parts[i]); len(p) > 0 {
			p[0] = unicode.ToUpper(p[0])
			parts[i] = string(p)
		}
	}
	return strings.Join(parts, "")
}
