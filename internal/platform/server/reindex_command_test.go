package server

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/server/external"
)

type commandReindexSource struct {
	observations []*domain.Observation
}

func (s commandReindexSource) DescribeCorpus(context.Context, external.ReindexScope) (external.ReindexCorpusDescriptor, error) {
	return external.ReindexCorpusDescriptor{Generation: "1", Checksum: "stable", Count: len(s.observations)}, nil
}

func (s commandReindexSource) List(_ context.Context, _ external.ReindexScope, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	if filter.Offset >= len(s.observations) {
		return nil, nil
	}
	end := filter.Offset + filter.Limit
	if end > len(s.observations) {
		end = len(s.observations)
	}
	return s.observations[filter.Offset:end], nil
}
func (s commandReindexSource) Scope(_ context.Context, id int64) (external.ReindexScope, error) {
	return external.ReindexScope{TenantID: "10000000-a000-0000-0000-000000000001", WorkspaceID: "10000000-a000-0000-0000-000000000002", ProjectID: "10000000-a000-0000-0000-000000000003"}, nil
}
func (s commandReindexSource) GetEmbedding(context.Context, external.ReindexScope, int64) ([]float32, string, error) {
	return []float32{1, 0}, "test", nil
}

type commandEmbeddingProvider struct{}

func (commandEmbeddingProvider) Embed(context.Context, []string) ([][]float32, domain.ModelInfo, error) {
	return nil, domain.ModelInfo{Name: "test", Dimension: 2}, nil
}
func (commandEmbeddingProvider) ModelInfo() domain.ModelInfo {
	return domain.ModelInfo{Name: "test", Dimension: 2}
}
func (commandEmbeddingProvider) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}

type commandVector struct{ points int }

func (v *commandVector) ID() string { return "test" }
func (v *commandVector) Upsert(_ context.Context, points []domain.VectorPoint) error {
	v.points += len(points)
	return nil
}
func (*commandVector) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (*commandVector) Delete(context.Context, []int64) error { return nil }
func (*commandVector) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}
func (*commandVector) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}
func (*commandVector) Close() error { return nil }

type commandAudit struct {
	events []reindexAuditEvent
}

func (a *commandAudit) Record(_ context.Context, event reindexAuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

type cancelingReindexSource struct{ cancel context.CancelFunc }

func (cancelingReindexSource) DescribeCorpus(context.Context, external.ReindexScope) (external.ReindexCorpusDescriptor, error) {
	return external.ReindexCorpusDescriptor{Generation: "1", Checksum: "stable", Count: 0}, nil
}

func (s cancelingReindexSource) List(ctx context.Context, _ external.ReindexScope, _ domain.ObservationFilter) ([]*domain.Observation, error) {
	s.cancel()
	return nil, ctx.Err()
}
func (cancelingReindexSource) Scope(context.Context, int64) (external.ReindexScope, error) {
	return external.ReindexScope{}, domain.ErrNotFound
}
func (cancelingReindexSource) GetEmbedding(context.Context, external.ReindexScope, int64) ([]float32, string, error) {
	return nil, "", domain.ErrNotFound
}

type contextCheckingAudit struct {
	events    []reindexAuditEvent
	ctxErrors []error
}

func (a *contextCheckingAudit) Record(ctx context.Context, event reindexAuditEvent) error {
	a.events = append(a.events, event)
	a.ctxErrors = append(a.ctxErrors, ctx.Err())
	return nil
}

func TestRunServerReindexAuthorizesAndAuditsOneTerminalOutcome(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	audit := &commandAudit{}
	vector := &commandVector{}
	authorized := 0
	deps := reindexCommandDeps{
		authorize: func(_ context.Context, gotProject, correlationID string) (reindexAuthority, error) {
			authorized++
			if gotProject != project || correlationID == "" {
				t.Fatalf("authorize(%q,%q)", gotProject, correlationID)
			}
			return reindexAuthority{ActorID: "10000000-a000-0000-0000-000000000004", TenantID: "10000000-a000-0000-0000-000000000001", WorkspaceID: "10000000-a000-0000-0000-000000000002", ProjectID: project, ProjectLabel: "cortex"}, nil
		},
		source: func(reindexAuthority) (external.ReindexSource, error) {
			return commandReindexSource{observations: []*domain.Observation{{ID: 7, Project: "cortex"}}}, nil
		},
		provider: commandEmbeddingProvider{}, target: vector, audit: audit,
	}

	result, err := runServerReindex(context.Background(), project, deps)
	if err != nil {
		t.Fatal(err)
	}
	if authorized != 1 || result.Total != 1 || result.Upserted != 1 || vector.points != 1 {
		t.Fatalf("authorization/result/vector = %d/%+v/%d", authorized, result, vector.points)
	}
	if len(audit.events) != 2 || audit.events[0].Phase != "start" || audit.events[1].Phase != "succeeded" {
		t.Fatalf("audit events = %+v, want start plus one terminal success", audit.events)
	}
	if audit.events[0].ProjectID != project || audit.events[0].ProjectLabel != "cortex" {
		t.Fatalf("audit lost canonical project metadata: %+v", audit.events[0])
	}
}

func TestRunServerReindexFailsClosedAndAuditsExactlyOneTerminalFailure(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	audit := &commandAudit{}
	deps := reindexCommandDeps{
		authorize: func(context.Context, string, string) (reindexAuthority, error) {
			return reindexAuthority{ActorID: "10000000-a000-0000-0000-000000000004", TenantID: "10000000-a000-0000-0000-000000000001", WorkspaceID: "10000000-a000-0000-0000-000000000002", ProjectID: project, ProjectLabel: "cortex"}, nil
		},
		source: func(reindexAuthority) (external.ReindexSource, error) {
			return commandReindexSource{observations: []*domain.Observation{nil}}, nil
		},
		provider: commandEmbeddingProvider{}, target: &commandVector{}, audit: audit,
	}

	_, err := runServerReindex(context.Background(), project, deps)
	if err == nil || !errors.Is(err, errReindexCoverageIncomplete) {
		t.Fatalf("error = %v, want incomplete coverage", err)
	}
	if len(audit.events) != 2 || audit.events[0].Phase != "start" || audit.events[1].Phase != "failed" {
		t.Fatalf("audit events = %+v, want start plus exactly one terminal failure", audit.events)
	}
}

func TestRunServerReindexRejectsAbsentDependenciesAndInvalidProject(t *testing.T) {
	valid := "10000000-a000-0000-0000-000000000003"
	for name, tc := range map[string]struct {
		project string
		deps    reindexCommandDeps
	}{
		"invalid project":  {"not-a-uuid", reindexCommandDeps{}},
		"missing provider": {valid, reindexCommandDeps{target: &commandVector{}}},
		"missing vector":   {valid, reindexCommandDeps{provider: commandEmbeddingProvider{}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runServerReindex(context.Background(), tc.project, tc.deps); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func TestRunServerReindexCancellationStillRecordsDetachedTerminalOutcome(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	ctx, cancel := context.WithCancel(context.Background())
	audit := &contextCheckingAudit{}
	deps := reindexCommandDeps{
		authorize: func(context.Context, string, string) (reindexAuthority, error) {
			return reindexAuthority{ActorID: "10000000-a000-0000-0000-000000000004", TenantID: "10000000-a000-0000-0000-000000000001", WorkspaceID: "10000000-a000-0000-0000-000000000002", ProjectID: project, ProjectLabel: "cortex"}, nil
		},
		source: func(reindexAuthority) (external.ReindexSource, error) {
			return cancelingReindexSource{cancel: cancel}, nil
		},
		provider: commandEmbeddingProvider{}, target: &commandVector{}, audit: audit,
	}
	if _, err := runServerReindex(ctx, project, deps); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if len(audit.events) != 2 || audit.events[1].Phase != "failed" || audit.ctxErrors[1] != nil {
		t.Fatalf("terminal audit = %+v ctx errors=%v", audit.events, audit.ctxErrors)
	}
}
