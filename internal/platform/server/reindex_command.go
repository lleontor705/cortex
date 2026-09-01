package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/server/external"
)

var errReindexCoverageIncomplete = errors.New("server: vector reindex coverage is incomplete")

type reindexAuthority struct {
	ActorID, TenantID, WorkspaceID, ProjectID, ProjectLabel string
	source                                                  external.ReindexSource
}

// reindexAuditEvent is intentionally metadata-only. It has no field capable
// of carrying observation text, embeddings, provider destinations or tokens.
type reindexAuditEvent struct {
	Phase, CorrelationID, ActorID, TenantID, WorkspaceID string
	ProjectID, ProjectLabel, ResultClass                 string
	Total, Upserted, ReEmbedded, Skipped                 int
	Duration                                             time.Duration
}

type reindexAuditSink interface {
	Record(context.Context, reindexAuditEvent) error
}

// reindexProvider is the command-owned narrow view of the embedding port.
// Keeping the composition boundary structural avoids adopting the deferred
// domain.EmbeddingProvider seam in internal/platform.
type reindexProvider interface {
	Embed(context.Context, []string) ([][]float32, domain.ModelInfo, error)
	ModelInfo() domain.ModelInfo
	Health(context.Context) domain.Health
}

type reindexCommandDeps struct {
	authorize func(context.Context, string, string) (reindexAuthority, error)
	source    func(reindexAuthority) (external.ReindexSource, error)
	provider  reindexProvider
	target    domain.VectorIndex
	audit     reindexAuditSink
}

// ReindexResult is the server-platform outcome exposed to composition callers.
// It deliberately mirrors only the stable counters required by operators so
// cmd/cortex does not depend on the server implementation package.
type ReindexResult struct {
	TenantID    string
	WorkspaceID string
	Total       int
	Upserted    int
	ReEmbedded  int
	Skipped     int
	Batches     int
}

func newReindexResult(result *external.ReindexResult) *ReindexResult {
	if result == nil {
		return nil
	}
	return &ReindexResult{
		TenantID: result.TenantID, WorkspaceID: result.WorkspaceID,
		Total: result.Total, Upserted: result.Upserted,
		ReEmbedded: result.ReEmbedded, Skipped: result.Skipped,
		Batches: result.Batches,
	}
}

type reindexEmbeddingProvider struct{ service embedding.Service }

func (p reindexEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float32, domain.ModelInfo, error) {
	if p.service == nil {
		return nil, domain.ModelInfo{}, errors.New("server: reindex embedding provider is not configured")
	}
	vectors := make([][]float32, len(texts))
	for i, value := range texts {
		vector, err := p.service.Embed(ctx, value)
		if err != nil {
			return nil, domain.ModelInfo{}, err
		}
		if len(vector) == 0 {
			return nil, domain.ModelInfo{}, errors.New("server: reindex embedding provider returned an empty vector")
		}
		vectors[i] = vector
	}
	dimension := p.service.Dimensions()
	if dimension == 0 && len(vectors) > 0 {
		dimension = len(vectors[0])
	}
	return vectors, domain.ModelInfo{Name: p.service.Model(), Dimension: dimension}, nil
}
func (p reindexEmbeddingProvider) ModelInfo() domain.ModelInfo {
	if p.service == nil {
		return domain.ModelInfo{}
	}
	return domain.ModelInfo{Name: p.service.Model(), Dimension: p.service.Dimensions()}
}

// Health deliberately reports configuration state, not remote readiness.
// embedding.Service has no side-effect-free probe; the first authorized Embed
// call is the only truthful readiness check.
func (p reindexEmbeddingProvider) Health(context.Context) domain.Health {
	if p.service == nil {
		return domain.Health{Status: domain.StatusUnhealthy, Message: "not configured"}
	}
	return domain.Health{Status: domain.StatusDegraded, Message: "configured; readiness is verified on use"}
}

type reindexPostgresAudit struct{ sink authz.AuditSink }

func (a reindexPostgresAudit) Record(ctx context.Context, event reindexAuditEvent) error {
	if a.sink == nil {
		return errors.New("server: reindex audit sink is unavailable")
	}
	reason := fmt.Sprintf("result=%s total=%d upserted=%d reembedded=%d skipped=%d duration_ms=%d",
		event.ResultClass, event.Total, event.Upserted, event.ReEmbedded, event.Skipped, event.Duration.Milliseconds())
	return a.sink.Record(ctx, authz.AuditEvent{
		CorrelationID: event.CorrelationID, Actor: event.ActorID,
		Action: "reindex_" + event.Phase, Resource: string(authz.ResourceAdmin),
		ResourceID: event.ProjectID, Reason: reason, Allowed: event.Phase != "failed",
	})
}

func runServerReindex(ctx context.Context, projectID string, deps reindexCommandDeps) (result *ReindexResult, retErr error) {
	parsed, err := uuid.Parse(strings.TrimSpace(projectID))
	if err != nil || parsed == uuid.Nil {
		return nil, errors.New("server: reindex --project-id must be a public UUID")
	}
	projectID = parsed.String()
	if deps.authorize == nil || deps.source == nil || deps.audit == nil {
		return nil, errors.New("server: reindex trusted composition is incomplete")
	}

	correlationID := uuid.NewString()
	authority, err := deps.authorize(ctx, projectID, correlationID)
	if err != nil {
		return nil, fmt.Errorf("server: reindex authorization: %w", err)
	}
	if err := validateReindexAuthority(authority, projectID); err != nil {
		return nil, err
	}
	ctx = external.WithRequestVectorScope(ctx, authority.TenantID, authority.WorkspaceID)
	started := time.Now()
	base := reindexAuditEvent{
		Phase: "start", CorrelationID: correlationID, ActorID: authority.ActorID,
		TenantID: authority.TenantID, WorkspaceID: authority.WorkspaceID,
		ProjectID: authority.ProjectID, ProjectLabel: authority.ProjectLabel,
		ResultClass: "started",
	}
	if err := deps.audit.Record(ctx, base); err != nil {
		return nil, fmt.Errorf("server: reindex start audit: %w", err)
	}
	defer func() {
		terminal := base
		terminal.Phase = "succeeded"
		terminal.ResultClass = "complete"
		terminal.Duration = time.Since(started)
		if result != nil {
			terminal.Total, terminal.Upserted = result.Total, result.Upserted
			terminal.ReEmbedded, terminal.Skipped = result.ReEmbedded, result.Skipped
		}
		if retErr != nil {
			terminal.Phase = "failed"
			terminal.ResultClass = "failed"
		}
		// A canceled operator context must stop provider/vector work, but it
		// must not erase the terminal audit. Bound the detached write so a
		// failing database cannot delay process shutdown indefinitely.
		auditCtx, cancelAudit := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelAudit()
		if auditErr := deps.audit.Record(auditCtx, terminal); auditErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("server: reindex terminal audit: %w", auditErr))
		}
	}()

	// Provider access intentionally happens only after project-scoped admin
	// authorization and the durable start audit. embedding.Service exposes
	// no truthful side-effect-free health operation; readiness is therefore
	// verified by the first real Embed call when re-embedding is required.
	if deps.provider == nil {
		return nil, errors.New("server: reindex embedding provider is not configured")
	}
	if deps.target == nil {
		return nil, errors.New("server: reindex vector provider is not configured")
	}
	if health := deps.target.Health(ctx); health.Status != domain.StatusHealthy {
		return nil, errors.New("server: reindex vector provider is unavailable")
	}
	source, err := deps.source(authority)
	if err != nil {
		return nil, fmt.Errorf("server: reindex source: %w", err)
	}
	if source == nil {
		return nil, errors.New("server: reindex source is unavailable")
	}
	externalResult, reindexErr := external.Reindex(ctx, source, deps.provider, deps.target, external.ReindexOptions{
		TenantID: authority.TenantID, WorkspaceID: authority.WorkspaceID,
		ProjectID: authority.ProjectID, BatchSize: 64,
	})
	result, retErr = newReindexResult(externalResult), reindexErr
	if retErr != nil {
		return result, retErr
	}
	if result.Total == 0 || result.Skipped != 0 || result.Upserted != result.Total {
		retErr = fmt.Errorf("%w: corpus=%d upserted=%d skipped=%d", errReindexCoverageIncomplete, result.Total, result.Upserted, result.Skipped)
	}
	return result, retErr
}

func validateReindexAuthority(authority reindexAuthority, requestedProject string) error {
	for name, value := range map[string]string{
		"actor": authority.ActorID, "tenant": authority.TenantID,
		"workspace": authority.WorkspaceID, "project": authority.ProjectID,
		"project label": authority.ProjectLabel,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("server: reindex authority has no %s", name)
		}
	}
	project, err := uuid.Parse(authority.ProjectID)
	if err != nil || project == uuid.Nil || project.String() != requestedProject {
		return errors.New("server: reindex authority project mismatch")
	}
	for _, value := range []string{authority.ActorID, authority.TenantID, authority.WorkspaceID} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return errors.New("server: reindex authority contains an invalid identity")
		}
	}
	return nil
}

// ReindexProject executes the bounded administrative job synchronously. It
// intentionally has no HTTP handler: operators invoke it through the server
// mode CLI and cancellation is inherited from the process context.
func (r *Runtime) ReindexProject(ctx context.Context, projectID string) (*ReindexResult, error) {
	if r == nil {
		return nil, errors.New("server: reindex runtime is unavailable")
	}
	return runServerReindex(ctx, projectID, r.reindex)
}
