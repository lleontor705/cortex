package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

func TestValidateTenantContext(t *testing.T) {
	id := uuid.NewString()
	if err := validateTenantContext(&domain.TenantContext{TenantID: id, WorkspaceID: uuid.NewString()}); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}
	for _, tc := range []*domain.TenantContext{nil, {}, {TenantID: "not-a-uuid"}} {
		if err := validateTenantContext(tc); err == nil {
			t.Fatalf("invalid context %v accepted", tc)
		}
	}
}

func TestTenantContextRoundTrip(t *testing.T) {
	ctx := withTenant(context.Background(), &domain.TenantContext{TenantID: uuid.NewString()})
	got, ok := tenantFromContext(ctx)
	if !ok || got.TenantID == "" {
		t.Fatal("tenant context was not retained")
	}
}

func TestPrincipalMustOwnTenantAndWorkspace(t *testing.T) {
	p := domain.Principal{Subject: "opaque-subject", OrgID: uuid.NewString(), WorkspaceIDs: []string{uuid.NewString()}}
	tenant := &domain.TenantContext{TenantID: uuid.NewString(), WorkspaceID: p.WorkspaceIDs[0]}
	if _, err := NewStore(nil, tenant, p); err == nil {
		t.Fatal("nil pool must be rejected before tenant checks")
	}
	// Validation is deliberately independent of subject representation.
	if err := validatePrincipal(p); err != nil {
		t.Fatalf("opaque subject rejected: %v", err)
	}
	if err := validateTenantContext(&domain.TenantContext{TenantID: p.OrgID, WorkspaceID: p.WorkspaceIDs[0]}); err != nil {
		t.Fatal(err)
	}
}

func TestActorMappingDoesNotCastOpaqueSubject(t *testing.T) {
	ctx := context.WithValue(context.Background(), actorKey{}, uuid.New())
	if actorFromContext(ctx) == nil {
		t.Fatal("resolved actor was lost from transaction context")
	}
	if actorFromContext(context.Background()) != nil {
		t.Fatal("unresolved actor must not become a SQL value")
	}
}

func TestAuthorizedContextRequiresGrantDigest(t *testing.T) {
	ac := authz.AuthorizedContext{Principal: domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString()}, Tenant: domain.TenantContext{TenantID: "00000000-0000-0000-0000-000000000001"}}
	if err := validateAuthorizedContext(ac); !errors.Is(err, ErrGrantDigestRequired) {
		t.Fatalf("error=%v, want %v", err, ErrGrantDigestRequired)
	}
}

func TestEdgeValidationErrorsAreStable(t *testing.T) {
	graph := (&Store{}).Graph()
	if err := graph.CreateEdge(context.Background(), &domain.Edge{FromObsID: 1, ToObsID: 2, RelationType: "unknown"}); !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("relation error=%v", err)
	}
	base := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	for name, times := range map[string][2]time.Time{
		"equal":    {base, base},
		"reversed": {base.Add(time.Minute), base},
	} {
		t.Run(name, func(t *testing.T) {
			from, to := times[0], times[1]
			err := graph.CreateEdge(context.Background(), &domain.Edge{FromObsID: 1, ToObsID: 2, RelationType: domain.RelationReferences, ValidFrom: &from, ValidUntil: &to})
			if !errors.Is(err, ErrInvalidTimeRange) {
				t.Fatalf("range error=%v", err)
			}
		})
	}
	if err := graph.UpdateEdge(context.Background(), &domain.Edge{ID: 1, RelationType: "unknown"}); !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("update relation error=%v", err)
	}
}

func TestCreateEdgeValidRangeReachesRepositorySeam(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	pool.Close()

	from := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	err = (&Store{pool: pool}).Graph().CreateEdge(context.Background(), &domain.Edge{
		FromObsID:    1,
		ToObsID:      2,
		RelationType: domain.RelationReferences,
		ValidFrom:    &from,
		ValidUntil:   &to,
	})
	if err == nil {
		t.Fatal("valid range unexpectedly reached a successful repository operation")
	}
	if errors.Is(err, ErrInvalidTimeRange) {
		t.Fatalf("valid range was rejected before repository access: %v", err)
	}
	if got, want := err.Error(), "postgres store: begin: closed pool"; got != want {
		t.Fatalf("repository error=%q, want %q", got, want)
	}
}
