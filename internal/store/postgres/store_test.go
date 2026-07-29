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
	"github.com/lleontor705/cortex/internal/identity"
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
	graph := (&Store{}).graph()
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
	err = (&Store{pool: pool}).graph().CreateEdge(context.Background(), &domain.Edge{
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

func TestGrantFiltersAndSystemConstruction(t *testing.T) {
	s := &Store{principal: domain.Principal{ProjectIDs: []string{"p1", "p2"}, ClassificationClearance: []string{"confidential"}}}
	projects, wildcard := s.projectGrantFilter()
	if wildcard || len(projects) != 2 {
		t.Fatalf("project grants=%v wildcard=%v", projects, wildcard)
	}
	classes, wildcard := s.classificationGrantFilter()
	if wildcard || len(classes) != 1 || classes[0] != "confidential" {
		t.Fatalf("classification grants=%v wildcard=%v", classes, wildcard)
	}
	s.principal.ProjectIDs = []string{"*"}
	if _, wildcard = s.projectGrantFilter(); !wildcard {
		t.Fatal("wildcard project grant not recognized")
	}
	s.principal.ClassificationClearance = []string{"*"}
	if _, wildcard = s.classificationGrantFilter(); !wildcard {
		t.Fatal("wildcard classification grant not recognized")
	}
	if _, err := NewSystemService(nil); !errors.Is(err, ErrAuthorizedStoreRequired) {
		t.Fatalf("system nil error=%v", err)
	}
	if _, err := NewAuditSink(nil, "", "", 0); err == nil {
		t.Fatal("invalid audit sink accepted")
	}
}

func TestAuthorizedOperationInputGuards(t *testing.T) {
	var s AuthorizedStore
	ctx := context.Background()
	if err := s.SaveObservation(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("save nil=%v", err)
	}
	if err := s.BulkSaveObservations(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("bulk empty=%v", err)
	}
	if _, err := s.GetObservationByID(ctx, 1); err == nil {
		t.Fatal("nil store read allowed")
	}
	if err := s.UpdateObservation(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("update nil=%v", err)
	}
	if err := s.DeleteObservation(ctx, 1); err == nil {
		t.Fatal("nil store delete allowed")
	}
	if _, err := s.ListObservations(ctx, domain.ObservationFilter{}); err == nil {
		t.Fatal("nil store list allowed")
	}
	if _, err := s.SearchObservations(ctx, "q", domain.SearchOptions{}); err == nil {
		t.Fatal("nil store search allowed")
	}
	if err := s.CreateGraphEdge(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("graph nil=%v", err)
	}
	if _, err := s.GetGraphEdge(ctx, 1); err == nil {
		t.Fatal("nil store graph read allowed")
	}
	if _, err := s.GetRelatedObservations(ctx, 1, 1); err == nil {
		t.Fatal("nil store related allowed")
	}
	if err := s.DeleteGraphEdge(ctx, 1); err == nil {
		t.Fatal("nil store graph delete allowed")
	}
	if _, err := s.GetImportanceScore(ctx, 1); err == nil {
		t.Fatal("nil store score read allowed")
	}
	if err := s.UpdateImportanceScore(ctx, 1, 1); err == nil {
		t.Fatal("nil store score write allowed")
	}
}

func TestAuthorizedOperationDenialsDoNotReachRepositories(t *testing.T) {
	ctx := context.Background()
	p := domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString()}
	s := &AuthorizedStore{store: &Store{tenant: &domain.TenantContext{TenantID: p.OrgID}, principal: p}}
	if err := s.SaveObservation(ctx, &domain.Observation{Title: "t", Content: "c", Project: "p"}); err == nil {
		t.Fatal("unauthorized save allowed")
	}
	if err := s.BulkSaveObservations(ctx, []*domain.Observation{{Title: "t", Content: "c", Project: "p"}}); err == nil {
		t.Fatal("unauthorized bulk save allowed")
	}
	if _, err := s.ListObservations(ctx, domain.ObservationFilter{Project: "p"}); err == nil {
		t.Fatal("unauthorized list allowed")
	}
	if _, err := s.SearchObservations(ctx, "query", domain.SearchOptions{Project: "p"}); err == nil {
		t.Fatal("unauthorized search allowed")
	}
	if _, err := s.IssueToken(ctx, identity.TokenIssue{Subject: "subject"}); err == nil {
		t.Fatal("unauthorized token issue allowed")
	}
	if _, err := s.VerifyToken(ctx, "secret", "read"); err == nil {
		t.Fatal("unauthorized token verify allowed")
	}
	if err := s.RevokeToken(ctx, "id"); err == nil {
		t.Fatal("unauthorized token revoke allowed")
	}
	if _, err := s.RotateToken(ctx, "id"); err == nil {
		t.Fatal("unauthorized token rotate allowed")
	}
	if _, err := s.ListTokens(ctx); err == nil {
		t.Fatal("unauthorized token list allowed")
	}
}

func TestSystemAndAuditFailurePaths(t *testing.T) {
	ctx := context.Background()
	id := uuid.NewString()
	p := domain.Principal{Subject: id, OrgID: uuid.NewString(), GrantDigest: "digest", GrantVersion: 1}
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	audit, err := NewAuditSink(pool, p.Subject, p.GrantDigest, p.GrantVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(ctx, authz.AuditEvent{Actor: id, Action: "write", Resource: "memory"}); err == nil {
		t.Fatal("audit unexpectedly succeeded against unavailable database")
	}
	store := &AuthorizedStore{store: &Store{tenant: &domain.TenantContext{TenantID: p.OrgID}, principal: p}}
	system, err := NewSystemService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := system.ListArchivable(ctx, time.Now(), 1, 1); err == nil {
		t.Fatal("system operation without permission allowed")
	}
}

func TestRepositoryValidationPaths(t *testing.T) {
	ctx := context.Background()
	obs := (&Store{}).observations()
	if err := obs.Save(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("observation nil save=%v", err)
	}
	if err := obs.SaveBulk(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("observation empty bulk=%v", err)
	}
	if err := obs.Update(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("observation nil update=%v", err)
	}
	if err := obs.Update(ctx, &domain.Observation{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("observation invalid update=%v", err)
	}
	if _, err := obs.GetByTopicKey(ctx, "", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("topic invalid=%v", err)
	}
	if err := (&Store{}).prompts().Save(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("prompt nil=%v", err)
	}
	if err := (&Store{}).prompts().Save(ctx, &domain.Prompt{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("prompt empty=%v", err)
	}
	if _, err := (&Store{}).search().Search(ctx, "", domain.SearchOptions{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("search empty=%v", err)
	}
	graph := (&Store{}).graph()
	if err := graph.CreateEdge(ctx, nil); !errors.Is(err, ErrInvalidEdge) {
		t.Fatalf("graph nil=%v", err)
	}
	if err := graph.CreateEdge(ctx, &domain.Edge{FromObsID: 1, ToObsID: 1, RelationType: domain.RelationReferences}); !errors.Is(err, ErrInvalidEdge) {
		t.Fatalf("graph self=%v", err)
	}
	if err := graph.UpdateEdge(ctx, nil); !errors.Is(err, ErrInvalidEdge) {
		t.Fatalf("graph update nil=%v", err)
	}
	if err := graph.UpdateEdge(ctx, &domain.Edge{ID: 0, RelationType: domain.RelationReferences}); !errors.Is(err, ErrInvalidEdge) {
		t.Fatalf("graph update id=%v", err)
	}
	if err := graph.UpdateEdge(ctx, &domain.Edge{ID: 1, RelationType: "bad"}); !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("graph update relation=%v", err)
	}
}

func TestAuthorizedOperationsStopAtDatabaseBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	org := uuid.NewString()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := domain.Principal{Subject: uuid.NewString(), OrgID: org, Roles: []string{"owner"}, GrantDigest: "digest", GrantVersion: 1}
	s := &AuthorizedStore{store: &Store{pool: pool, tenant: &domain.TenantContext{TenantID: org}, principal: p, authorized: true, grantDigest: p.GrantDigest, grantVersion: p.GrantVersion, authorizer: authz.NewPolicy()}}
	if _, err := s.GetObservationByID(ctx, 1); err == nil {
		t.Fatal("unreachable database read succeeded")
	}
	if _, err := s.GetObservationByPublicID(ctx, uuid.NewString()); err == nil {
		t.Fatal("unreachable public read succeeded")
	}
	if err := s.UpdateObservation(ctx, &domain.Observation{ID: 1, Title: "t", Content: "c", Project: "p"}); err == nil {
		t.Fatal("unreachable update succeeded")
	}
	if err := s.DeleteObservation(ctx, 1); err == nil {
		t.Fatal("unreachable delete succeeded")
	}
	if _, err := s.GetGraphEdge(ctx, 1); err == nil {
		t.Fatal("unreachable graph read succeeded")
	}
	if _, err := s.GetRelatedObservations(ctx, 1, 1); err == nil {
		t.Fatal("unreachable related read succeeded")
	}
	if err := s.DeleteGraphEdge(ctx, 1); err == nil {
		t.Fatal("unreachable graph delete succeeded")
	}
	if _, err := s.GetImportanceScore(ctx, 1); err == nil {
		t.Fatal("unreachable score read succeeded")
	}
	if err := s.UpdateImportanceScore(ctx, 1, 1); err == nil {
		t.Fatal("unreachable score update succeeded")
	}
}

func TestRepositoryOperationsStopAtDatabaseBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := &Store{pool: pool, tenant: &domain.TenantContext{TenantID: uuid.NewString(), WorkspaceID: uuid.NewString()}, principal: domain.Principal{Subject: uuid.NewString()}, authorized: true}
	obs := s.observations()
	if _, err := obs.GetByID(ctx, 1); err == nil {
		t.Fatal("observation read succeeded")
	}
	if _, err := obs.GetByPublicID(ctx, uuid.NewString()); err == nil {
		t.Fatal("public observation read succeeded")
	}
	if _, err := obs.List(ctx, domain.ObservationFilter{Project: "p"}); err == nil {
		t.Fatal("observation list succeeded")
	}
	if _, err := obs.ListArchivable(ctx, time.Now(), 1, 1); err == nil {
		t.Fatal("archival list succeeded")
	}
	if err := obs.Delete(ctx, 1); err == nil {
		t.Fatal("observation delete succeeded")
	}
	graph := s.graph()
	if _, err := graph.GetEdge(ctx, 1); err == nil {
		t.Fatal("edge read succeeded")
	}
	if _, err := graph.GetRelated(ctx, 1, 1); err == nil {
		t.Fatal("related read succeeded")
	}
	if err := graph.DeleteEdge(ctx, 1); err == nil {
		t.Fatal("edge delete succeeded")
	}
	if _, err := s.search().Search(ctx, "query", domain.SearchOptions{}); err == nil {
		t.Fatal("search succeeded")
	}
	if _, err := s.GetScore(ctx, 1); err == nil {
		t.Fatal("score read succeeded")
	}
	if err := s.UpdateScore(ctx, 1, 1); err == nil {
		t.Fatal("score update succeeded")
	}
	if _, err := s.GetTopByScore(ctx, "p", 1); err == nil {
		t.Fatal("score list succeeded")
	}
}
