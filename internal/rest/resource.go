package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Resource is a collection the API manages: tools, agents or crews. Creates,
// updates and deletes save, deploy and publish at once; the next call uses
// the result.
type Resource string

const (
	Tools  Resource = "tools"
	Agents Resource = "agents"
	Crews  Resource = "crews"
)

// ListTools is the GET /tools query; zero values are omitted.
type ListTools struct {
	Limit  int
	Cursor string
}

// ListTools returns one page by ascending id: {"data": [...], "nextCursor": string|null}.
func (c *Client) ListTools(ctx context.Context, q ListTools) (Object, error) {
	v := url.Values{}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	return c.do(ctx, opRead, http.MethodGet, "/tools", v, nil, "")
}

// GetResource returns one tool, agent or crew with its full settings.
func (c *Client) GetResource(ctx context.Context, r Resource, id string) (Object, error) {
	return c.do(ctx, opRead, http.MethodGet, r.item(id), nil, nil, "")
}

// CreateResource creates a tool, agent or crew from body. idempotencyKey
// makes resending the same body replay the first 201 (or 502
// writeOutcomeUnknown) instead of creating another one.
func (c *Client) CreateResource(ctx context.Context, r Resource, body Object, idempotencyKey string) (Object, error) {
	return c.do(ctx, opWriteCreate, http.MethodPost, "/"+string(r), nil, body, idempotencyKey)
}

// UpdateResource changes only the fields in body.
func (c *Client) UpdateResource(ctx context.Context, r Resource, id string, body Object) (Object, error) {
	return c.do(ctx, opWriteUpdate, http.MethodPatch, r.item(id), nil, body, "")
}

// DeleteResource deletes a tool, agent or crew; the API answers 204 without
// a body.
func (c *Client) DeleteResource(ctx context.Context, r Resource, id string) error {
	_, err := c.do(ctx, opWriteDelete, http.MethodDelete, r.item(id), nil, nil, "")
	return err
}

func (r Resource) item(id string) string { return "/" + string(r) + "/" + url.PathEscape(id) }
