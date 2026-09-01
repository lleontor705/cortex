package server

import (
	"context"
	"errors"

	"github.com/lleontor705/cortex/v2/internal/config"
)

// ReindexJob is the bounded server-mode composition for an administrative
// vector rebuild. It intentionally exposes neither HTTP/MCP transports nor
// background lifecycle services.
type ReindexJob struct {
	runtime *Runtime
}

// OpenReindexJob reuses the authenticated PostgreSQL store, authorization,
// audit, embedding, and vector composition without constructing server
// transports, agent providers, sync workers, or archival lifecycle services.
func OpenReindexJob(ctx context.Context, cfg config.Config) (*ReindexJob, error) {
	runtime, err := openRuntime(ctx, cfg, false)
	if err != nil {
		return nil, err
	}
	return &ReindexJob{runtime: runtime}, nil
}

func (j *ReindexJob) ReindexProject(ctx context.Context, projectID string) (*ReindexResult, error) {
	if j == nil || j.runtime == nil {
		return nil, errors.New("server: reindex job is unavailable")
	}
	return j.runtime.ReindexProject(ctx, projectID)
}

// Close releases the vector client, embedding transport, and PostgreSQL pool.
// It is idempotent through Runtime.Close.
func (j *ReindexJob) Close() error {
	if j == nil || j.runtime == nil {
		return nil
	}
	return j.runtime.Close()
}
