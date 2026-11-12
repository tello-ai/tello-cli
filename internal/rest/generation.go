package rest

import (
	"context"
	"net/http"
	"net/url"

	"github.com/tello-ai/tello-cli/internal/app"
)

// GenerateCrew starts building, deploying and publishing a crew from a goal.
// The account builds one crew at a time: the same goal while it runs (or
// within 10 minutes of its success) returns that job instead of a new one.
// The job is {"id","status":"running"|"succeeded"|"failed","goal",...}.
func (c *Client) GenerateCrew(ctx context.Context, goal, idempotencyKey string) (Object, error) {
	job, err := c.do(ctx, opGenerate, http.MethodPost, "/crews/generate", nil, map[string]string{"goal": goal}, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return validateJob(job, "")
}

// GetCrewGeneration returns a crew generation job; a succeeded job has the
// crew, a failed one its error and possibly the crew it left behind.
func (c *Client) GetCrewGeneration(ctx context.Context, id string) (Object, error) {
	job, err := c.do(ctx, opRead, http.MethodGet, "/crew-generations/"+url.PathEscape(id), nil, nil, "")
	if err != nil {
		return nil, err
	}
	return validateJob(job, id)
}

// validateJob keeps a malformed response from passing for a job to follow.
func validateJob(job Object, expectedID string) (Object, error) {
	id := ID(job)
	if id == "" || Status(job) == "" || (expectedID != "" && id != expectedID) {
		return nil, app.NewError(app.KindServer, "invalidResponse", "invalid crew generation response")
	}
	return job, nil
}

// GenerationOutcome maps a finished job to the result: only succeeded is a
// success. A failed job is a server error with its error.code camelCased
// (crewGenerationFailed when absent) and the job as data.
func GenerationOutcome(job Object) error {
	var e *app.Error
	switch Status(job) {
	case "succeeded":
		return nil
	case "failed":
		code, message := "crewGenerationFailed", "the crew generation failed"
		if jobErr, ok := job["error"].(map[string]any); ok {
			if c, _ := jobErr["code"].(string); c != "" {
				code = camel(c)
			}
			if m, _ := jobErr["message"].(string); m != "" {
				message += ": " + m
			}
		}
		e = app.NewError(app.KindServer, code, message)
	default:
		e = app.NewError(app.KindServer, "invalidResponse", "unknown crew generation status "+Status(job))
	}
	e.Data = job
	return e
}
