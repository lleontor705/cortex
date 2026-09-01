package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/server/external"
)

type orderedReindexProvider struct {
	events *[]string
	health domain.Health
}

func (p orderedReindexProvider) Embed(context.Context, []string) ([][]float32, domain.ModelInfo, error) {
	*p.events = append(*p.events, "embed")
	return nil, domain.ModelInfo{Name: "test", Dimension: 2}, nil
}
func (orderedReindexProvider) ModelInfo() domain.ModelInfo {
	return domain.ModelInfo{Name: "test", Dimension: 2}
}
func (p orderedReindexProvider) Health(context.Context) domain.Health {
	*p.events = append(*p.events, "provider-health")
	return p.health
}

type orderedReindexVector struct {
	events *[]string
	health domain.Health
}

func (*orderedReindexVector) ID() string                                         { return "ordered" }
func (*orderedReindexVector) Upsert(context.Context, []domain.VectorPoint) error { return nil }
func (*orderedReindexVector) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (*orderedReindexVector) Delete(context.Context, []int64) error { return nil }
func (v *orderedReindexVector) Health(context.Context) domain.Health {
	if v.events != nil {
		*v.events = append(*v.events, "vector-health")
	}
	return v.health
}
func (*orderedReindexVector) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}
func (*orderedReindexVector) Close() error { return nil }

type reindexAuditFunc func(context.Context, reindexAuditEvent) error

func (f reindexAuditFunc) Record(ctx context.Context, event reindexAuditEvent) error {
	return f(ctx, event)
}

func validReindexAuthority(project string) reindexAuthority {
	return reindexAuthority{
		ActorID:      "10000000-a000-0000-0000-000000000004",
		TenantID:     "10000000-a000-0000-0000-000000000001",
		WorkspaceID:  "10000000-a000-0000-0000-000000000002",
		ProjectID:    project,
		ProjectLabel: "cortex",
	}
}

func TestRunServerReindexAuthorizesAndAuditsBeforeAnyProviderProbe(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	events := []string{}
	deps := reindexCommandDeps{
		authorize: func(context.Context, string, string) (reindexAuthority, error) {
			events = append(events, "authorize")
			return validReindexAuthority(project), nil
		},
		source: func(reindexAuthority) (external.ReindexSource, error) {
			events = append(events, "source")
			return commandReindexSource{}, nil
		},
		provider: orderedReindexProvider{events: &events, health: domain.Health{Status: domain.StatusHealthy}},
		target:   &orderedReindexVector{events: &events, health: domain.Health{Status: domain.StatusHealthy}},
		audit: reindexAuditFunc(func(_ context.Context, event reindexAuditEvent) error {
			events = append(events, "audit-"+event.Phase)
			return nil
		}),
	}

	_, _ = runServerReindex(context.Background(), project, deps)
	if len(events) < 2 || events[0] != "authorize" || events[1] != "audit-start" {
		t.Fatalf("events = %v, want authorization and start audit before provider access", events)
	}
}

func TestRunServerReindexUnavailableVectorAuditsExactlyOneTerminalFailure(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	audit := &commandAudit{}
	sourceCalls := 0
	deps := reindexCommandDeps{
		authorize: func(context.Context, string, string) (reindexAuthority, error) {
			return validReindexAuthority(project), nil
		},
		source: func(reindexAuthority) (external.ReindexSource, error) {
			sourceCalls++
			return commandReindexSource{}, nil
		},
		provider: commandEmbeddingProvider{},
		target:   &orderedReindexVector{health: domain.Health{Status: domain.StatusUnhealthy}},
		audit:    audit,
	}

	_, err := runServerReindex(context.Background(), project, deps)
	if err == nil || !strings.Contains(err.Error(), "vector provider is unavailable") {
		t.Fatalf("error = %v, want unavailable vector", err)
	}
	if sourceCalls != 0 {
		t.Fatalf("source calls = %d, want none before provider readiness", sourceCalls)
	}
	if len(audit.events) != 2 || audit.events[0].Phase != "start" || audit.events[1].Phase != "failed" {
		t.Fatalf("audit events = %+v, want start plus exactly one terminal failure", audit.events)
	}
}

func TestRunServerReindexAuthorizationFailureNeverTouchesProviders(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	events := []string{}
	deps := reindexCommandDeps{
		authorize: func(context.Context, string, string) (reindexAuthority, error) {
			events = append(events, "authorize-denied")
			return reindexAuthority{}, errors.New("denied")
		},
		source:   func(reindexAuthority) (external.ReindexSource, error) { return nil, nil },
		provider: orderedReindexProvider{events: &events, health: domain.Health{Status: domain.StatusHealthy}},
		target:   &orderedReindexVector{events: &events, health: domain.Health{Status: domain.StatusHealthy}},
		audit:    &commandAudit{},
	}

	if _, err := runServerReindex(context.Background(), project, deps); err == nil {
		t.Fatal("expected authorization failure")
	}
	if len(events) != 1 || events[0] != "authorize-denied" {
		t.Fatalf("events = %v, providers were touched before authorization", events)
	}
}
