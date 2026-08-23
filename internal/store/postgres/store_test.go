package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/identity"
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
	if err := validateTenantContext(&domain.TenantContext{TenantID: p.OrgID, WorkspaceID: "not-a-uuid"}); err == nil {
		t.Fatal("invalid workspace accepted")
	}
	if err := validatePrincipal(domain.Principal{}); !errors.Is(err, ErrPrincipalRequired) {
		t.Fatalf("empty principal error=%v", err)
	}
	if err := validatePrincipal(domain.Principal{Subject: "subject", OrgID: "not-a-uuid"}); err == nil {
		t.Fatal("invalid org accepted")
	}
}

func TestNewStoreRejectsTenantAndWorkspaceGrantMismatches(t *testing.T) {
	ctx := context.Background()
	pool := mustClosedPool(t, ctx)
	org, other := uuid.NewString(), uuid.NewString()
	p := domain.Principal{Subject: "subject", OrgID: org, WorkspaceIDs: []string{uuid.NewString()}}
	if _, err := NewStore(pool, &domain.TenantContext{TenantID: other}, p); err == nil {
		t.Fatal("tenant mismatch accepted")
	}
	if _, err := NewStore(pool, &domain.TenantContext{TenantID: org, WorkspaceID: uuid.NewString()}, p); err == nil {
		t.Fatal("workspace grant mismatch accepted")
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

func TestAuthorizedContextRejectsMissingVersionAndTenantMismatch(t *testing.T) {
	base := authz.AuthorizedContext{Principal: domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString(), GrantDigest: "digest", GrantVersion: 1}, GrantDigest: "digest", Tenant: domain.TenantContext{}}
	if err := validateAuthorizedContext(base); !errors.Is(err, ErrTenantContextRequired) {
		t.Fatalf("missing tenant error=%v", err)
	}
	base.Tenant.TenantID = uuid.NewString()
	if err := validateAuthorizedContext(base); !errors.Is(err, ErrTenantContextRequired) {
		t.Fatalf("mismatched tenant error=%v", err)
	}
	base.Tenant.TenantID = base.Principal.OrgID
	base.Principal.GrantVersion = 0
	if err := validateAuthorizedContext(base); !errors.Is(err, ErrGrantVersionRequired) {
		t.Fatalf("missing grant version error=%v", err)
	}
}

func TestAuthorizedStoreConstructorRejectsInvalidContext(t *testing.T) {
	pool := mustClosedPool(t, context.Background())
	valid := authz.AuthorizedContext{Principal: domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString(), GrantVersion: 1}, GrantDigest: "digest"}
	if _, err := NewAuthorizedStore(pool, valid); err == nil {
		t.Fatal("authorized store accepted missing tenant")
	}
	valid.Tenant.TenantID = valid.Principal.OrgID
	if _, err := NewAuthorizedStore(pool, valid); err != nil {
		t.Fatalf("valid authorized context rejected: %v", err)
	}
	valid.Principal.Subject = ""
	if _, err := NewAuthorizedStore(pool, valid); !errors.Is(err, ErrPrincipalRequired) {
		t.Fatalf("invalid principal error=%v", err)
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
	s.principal.ProjectIDs = nil
	s.principal.Scopes = []string{"project:scoped"}
	projects, wildcard = s.projectGrantFilter()
	if wildcard || len(projects) != 1 || projects[0] != "scoped" {
		t.Fatalf("scoped project grants=%v wildcard=%v", projects, wildcard)
	}
	s.principal.Type = "service_account"
	if classes, wildcard := s.classificationGrantFilter(); wildcard || classes != nil {
		t.Fatalf("service classification grants=%v wildcard=%v", classes, wildcard)
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
	if err := s.RecordImportanceAccess(ctx, 1); err == nil {
		t.Fatal("nil store access allowed")
	}
	if err := s.SetImportanceScore(ctx, 1, 1); err == nil {
		t.Fatal("nil store score set allowed")
	}
	if _, err := s.GetObservationByPublicID(ctx, uuid.NewString()); err == nil {
		t.Fatal("nil store public read allowed")
	}
	if _, err := s.IssueToken(ctx, identity.TokenIssue{Subject: "subject"}); err == nil {
		t.Fatal("nil store token issue allowed")
	}
	if _, err := s.VerifyToken(ctx, "secret", "read"); err == nil {
		t.Fatal("nil store token verify allowed")
	}
	if err := s.RevokeToken(ctx, "id"); err == nil {
		t.Fatal("nil store token revoke allowed")
	}
	if _, err := s.RotateToken(ctx, "id"); err == nil {
		t.Fatal("nil store token rotate allowed")
	}
	if _, err := s.ListTokens(ctx); err == nil {
		t.Fatal("nil store token list allowed")
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

func TestAuthorizedOperationsPassPolicyBeforeStorage(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.NewString()
	p := domain.Principal{
		Subject:                 uuid.NewString(),
		OrgID:                   tenant,
		Roles:                   []string{"owner"},
		ProjectIDs:              []string{"*"},
		ClassificationClearance: []string{"*"},
	}
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s := &AuthorizedStore{store: &Store{
		pool:       pool,
		tenant:     &domain.TenantContext{TenantID: tenant},
		principal:  p,
		authorized: true,
		authorizer: authz.NewPolicy(),
	}, caps: newCapabilities(nil)}
	// These calls intentionally use valid owner grants. They must reach the
	// closed database seam rather than being rejected by the policy layer.
	if err := s.SaveObservation(ctx, &domain.Observation{Project: "project", Scope: domain.ScopeProject, Title: "title", Content: "content"}); err == nil {
		t.Fatal("authorized save unexpectedly succeeded")
	}
	if err := s.BulkSaveObservations(ctx, []*domain.Observation{{Project: "project", Scope: domain.ScopeProject, Title: "title", Content: "content"}}); err == nil {
		t.Fatal("authorized bulk save unexpectedly succeeded")
	}
	if _, err := s.ListObservations(ctx, domain.ObservationFilter{Project: "project"}); err == nil {
		t.Fatal("authorized list unexpectedly succeeded")
	}
	if _, err := s.SearchObservations(ctx, "query", domain.SearchOptions{Project: "project"}); err == nil {
		t.Fatal("authorized search unexpectedly succeeded")
	}
	if _, err := s.IssueToken(ctx, identity.TokenIssue{Subject: p.Subject}); err == nil {
		t.Fatal("authorized token issue unexpectedly succeeded")
	}
	if _, err := s.VerifyToken(ctx, "secret", "read"); err == nil {
		t.Fatal("authorized token verify unexpectedly succeeded")
	}
	if err := s.RevokeToken(ctx, "token"); err == nil {
		t.Fatal("authorized token revoke unexpectedly succeeded")
	}
	if _, err := s.RotateToken(ctx, "token"); err == nil {
		t.Fatal("authorized token rotate unexpectedly succeeded")
	}
	if _, err := s.ListTokens(ctx); err == nil {
		t.Fatal("authorized token list unexpectedly succeeded")
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
	for _, invalid := range []*domain.Observation{{Title: "", Content: "content"}, {Title: "title", Content: ""}, {Title: " ", Content: "content"}} {
		if err := obs.Save(ctx, invalid); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid observation save=%v", err)
		}
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
	if err := graph.CreateEdge(ctx, &domain.Edge{FromObsID: 0, ToObsID: 2, RelationType: domain.RelationReferences}); !errors.Is(err, ErrInvalidEdge) {
		t.Fatalf("graph invalid from=%v", err)
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

func TestAuthorizedOperationsCoverGraphLifecycleAndScoreBoundaries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	org := uuid.NewString()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := domain.Principal{Subject: uuid.NewString(), OrgID: org, Roles: []string{"owner"}, ProjectIDs: []string{"project"}, ClassificationClearance: []string{"confidential"}, GrantDigest: "digest", GrantVersion: 1}
	s := &AuthorizedStore{store: &Store{pool: pool, tenant: &domain.TenantContext{TenantID: org}, principal: p, authorized: true, grantDigest: p.GrantDigest, grantVersion: p.GrantVersion, authorizer: authz.NewPolicy()}}
	for name, call := range map[string]func() error{
		"create edge": func() error {
			return s.CreateGraphEdge(ctx, &domain.Edge{FromObsID: 1, ToObsID: 2, RelationType: domain.RelationReferences})
		},
		"delete edge":   func() error { return s.DeleteGraphEdge(ctx, 1) },
		"record access": func() error { return s.RecordImportanceAccess(ctx, 1) },
		"set score":     func() error { return s.SetImportanceScore(ctx, 1, 2) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("unreachable database operation succeeded")
			}
		})
	}
	if _, err := s.GetGraphEdge(ctx, 1); err == nil {
		t.Fatal("graph read unexpectedly succeeded")
	}
	if _, err := s.GetRelatedObservations(ctx, 1, 2); err == nil {
		t.Fatal("related read unexpectedly succeeded")
	}
}

func TestSystemServiceAuthorizedBoundaryAndAuditRecordFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString(), Roles: []string{"owner"}, GrantDigest: "digest", GrantVersion: 1}
	raw := &Store{pool: pool, tenant: &domain.TenantContext{TenantID: p.OrgID}, principal: p, authorized: true, grantDigest: p.GrantDigest, grantVersion: p.GrantVersion, authorizer: authz.NewPolicy()}
	service, err := NewSystemService(&AuthorizedStore{store: raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListArchivable(ctx, time.Now(), 1, 10); err == nil {
		t.Fatal("system list unexpectedly succeeded")
	}
	if err := service.Delete(ctx, 1); err == nil {
		t.Fatal("system delete unexpectedly succeeded")
	}
	audit, err := NewAuditSink(pool, p.Subject, p.GrantDigest, p.GrantVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(ctx, authz.AuditEvent{Actor: p.Subject, Action: "delete", Resource: "memory", Allowed: true}); err == nil {
		t.Fatal("audit unexpectedly succeeded against unavailable database")
	}
}

func TestAuthorizedConstructionAndHealthFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenant, workspace := uuid.NewString(), uuid.NewString()
	p := domain.Principal{Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace}, GrantVersion: 1, GrantDigest: "digest"}
	store, err := NewStore(pool, &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace}, p)
	if err != nil {
		t.Fatal(err)
	}
	if store.Backend() != "postgres" {
		t.Fatalf("backend=%q", store.Backend())
	}
	if health := store.Health(ctx); health.Status != domain.StatusUnhealthy {
		t.Fatalf("health=%+v", health)
	}
	if _, err := store.BeginTx(ctx); err == nil {
		t.Fatal("unreachable database transaction succeeded")
	}
	ac := authz.AuthorizedContext{Principal: p, Tenant: domain.TenantContext{TenantID: tenant, WorkspaceID: workspace}, GrantDigest: p.GrantDigest}
	authorized, err := NewAuthorizedStore(pool, ac)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.store.authorizer == nil || authorized.caps == nil {
		t.Fatal("authorized store did not install policy capabilities")
	}
	if _, err := authorized.store.BeginTx(ctx); err == nil {
		t.Fatal("authorized transaction unexpectedly succeeded")
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

func TestTenantScopedRepositoryOperationsStopBeforeUnboundDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := &Store{pool: pool, tenant: &domain.TenantContext{TenantID: uuid.NewString(), WorkspaceID: uuid.NewString()}, authorized: true, grantDigest: "digest", grantVersion: 1, principal: domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString()}}
	checks := map[string]func() error{
		"observation count":      func() error { _, e := s.observations().CountAll(ctx); return e },
		"root count":             func() error { _, e := s.observations().CountByRoot(ctx, 1); return e },
		"edge observation count": func() error { _, e := s.observations().CountEdgesAsObs(ctx, 1); return e },
		"observation source":     func() error { _, e := s.observations().GetBySource(ctx, "manual", 10); return e },
		"observation type":       func() error { _, e := s.observations().GetByType(ctx, "manual", 10); return e },
		"observation public":     func() error { _, e := s.observations().GetByPublicID(ctx, uuid.NewString()); return e },
		"observation list": func() error {
			_, e := s.observations().List(ctx, domain.ObservationFilter{Project: "project"})
			return e
		},
		"observation archive": func() error {
			_, e := s.observations().ListArchivable(ctx, time.Now(), 1, 10)
			return e
		},
		"observation delete": func() error { return s.observations().Delete(ctx, 1) },
		"prompt list":        func() error { _, e := s.prompts().List(ctx, "project", 0); return e },
		"edge public":        func() error { _, e := s.graph().GetEdgeByPublicID(ctx, uuid.NewString()); return e },
		"edges observation":  func() error { _, e := s.graph().GetEdgesForObservation(ctx, 1); return e },
		"evolution":          func() error { _, e := s.graph().GetEvolutionChain(ctx, 1, 2); return e },
		"edge count":         func() error { _, e := s.graph().CountEdgesByObservation(ctx, 1); return e },
		"all edge count":     func() error { _, e := s.graph().CountAllEdges(ctx); return e },
		"contradictions": func() error {
			_, e := s.graph().GetContradictions(ctx, time.Now().Add(-time.Hour), time.Now())
			return e
		},
		"score top":          func() error { _, e := s.GetTop(ctx, "project", 0); return e },
		"all scores":         func() error { _, e := s.GetAllScores(ctx); return e },
		"incoming count":     func() error { _, e := s.GetIncomingEdgeCount(ctx, 1); return e },
		"scored observation": func() error { _, e := s.GetObservation(ctx, 1); return e },
	}
	for name, call := range checks {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("unreachable database operation succeeded")
			}
		})
	}
}

func TestOutboxOperationsRequireBoundTenantTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	r := &OutboxStore{Store: &Store{pool: pool, tenant: &domain.TenantContext{TenantID: uuid.NewString()}, authorized: true, grantDigest: "digest", grantVersion: 1, principal: domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString()}}}
	if err := r.EnqueueInTx(ctx, 1, "embed", "model"); err == nil {
		t.Fatal("enqueue without active transaction succeeded")
	}
	if err := r.WithinTx(ctx, struct{}{}, func(context.Context) error { return nil }); err == nil {
		t.Fatal("invalid transaction handle accepted")
	}
	checks := []func() error{
		func() error { _, e := r.Lease(ctx, 1); return e },
		func() error { return r.MarkComplete(ctx, 1) },
		func() error { return r.MarkFailed(ctx, 1, errors.New("failure"), time.Now()) },
		func() error { return r.DeadLetter(ctx, 1, errors.New("dead")) },
		func() error { _, e := r.PendingCount(ctx); return e },
		func() error { return r.RecoverPending(ctx) },
	}
	for _, check := range checks {
		if err := check(); err == nil {
			t.Fatal("unreachable outbox operation succeeded")
		}
	}
}

func TestSessionAndEntityRepositoriesRemainTenantBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	s := &Store{pool: mustClosedPool(t, ctx), tenant: &domain.TenantContext{TenantID: uuid.NewString()}, authorized: true, grantDigest: "digest", grantVersion: 1, principal: domain.Principal{Subject: uuid.NewString(), OrgID: uuid.NewString()}}
	sessions := &SessionRepository{Store: s}
	if err := sessions.Create(ctx, &domain.Session{Project: "project", StartedAt: time.Now()}); err == nil {
		t.Fatal("session create unexpectedly succeeded")
	}
	if _, err := sessions.GetByID(ctx, uuid.NewString()); err == nil {
		t.Fatal("session read unexpectedly succeeded")
	}
	if err := sessions.End(ctx, uuid.NewString(), "done"); err == nil {
		t.Fatal("session end unexpectedly succeeded")
	}
	if _, err := sessions.List(ctx, "project"); err == nil {
		t.Fatal("session list unexpectedly succeeded")
	}
	entities := &EntityRepository{Store: s}
	if err := entities.SaveLinks(ctx, []*domain.EntityLink{{ObservationID: 1, EntityType: "file", EntityValue: "README.md"}}); err == nil {
		t.Fatal("entity save unexpectedly succeeded")
	}
	if _, err := entities.GetByObservation(ctx, 1); err == nil {
		t.Fatal("entity observation read unexpectedly succeeded")
	}
	if _, err := entities.FindByEntity(ctx, "file", "README.md"); err == nil {
		t.Fatal("entity lookup unexpectedly succeeded")
	}
	if err := entities.DeleteByObservation(ctx, 1); err == nil {
		t.Fatal("entity delete unexpectedly succeeded")
	}
}

func TestTokenRepositoryValidationAndSecretHelpers(t *testing.T) {
	tenant := uuid.NewString()
	r := &TokenRepository{Store: &Store{tenant: &domain.TenantContext{TenantID: tenant}}}
	if _, err := r.Issue(context.Background(), identity.TokenIssue{}); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("empty token issue=%v", err)
	}
	if _, err := r.Verify(context.Background(), "short", ""); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("short token verify=%v", err)
	}
	if !contains([]string{"read", "write"}, "write") || contains([]string{"read"}, "admin") {
		t.Fatal("token scope helper mismatch")
	}
	if nullTime(time.Time{}) != nil || nullTime(time.Unix(1, 0)) == nil {
		t.Fatal("null time helper mismatch")
	}
	if len(r.digest("secret")) != sha256.Size {
		t.Fatal("token digest has unexpected length")
	}
}

func TestVerifyPrincipalErrorMappingIsStable(t *testing.T) {
	for _, tc := range []struct {
		code    string
		message string
		want    error
	}{
		{"28000", "token is revoked", identity.ErrTokenRevoked},
		{"28000", "token is expired", identity.ErrTokenExpired},
		{"42501", "token is missing required scope", identity.ErrInsufficientScope},
	} {
		err := mapVerifyPrincipalError(&pgconn.PgError{Code: tc.code, Message: tc.message})
		if !errors.Is(err, tc.want) {
			t.Fatalf("code=%s message=%q error=%v, want %v", tc.code, tc.message, err, tc.want)
		}
	}
	// Unrelated SQLSTATE failures must pass through unchanged so transport
	// error taxonomies keep their causes.
	foreign := &pgconn.PgError{Code: "28000", Message: "principal grant is revoked or stale"}
	if err := mapVerifyPrincipalError(foreign); !errors.Is(err, foreign) {
		t.Fatalf("foreign binder error rewritten: %v", err)
	}
	plain := errors.New("boom")
	if err := mapVerifyPrincipalError(plain); !errors.Is(err, plain) {
		t.Fatalf("plain error rewritten: %v", err)
	}
}

func TestMediatedMutationErrorsMapBySQLState(t *testing.T) {
	subjectMissing := &pgconn.PgError{Code: "23503", Message: "token subject does not exist in tenant"}
	err := mapMediatedMutationError(subjectMissing, "token subject", "subject")
	var notFoundErr *domain.NotFoundError
	if !errors.As(err, &notFoundErr) || notFoundErr.Type != "token subject" {
		t.Fatalf("subject error=%v (%T), want token subject NotFoundError", err, err)
	}
	tokenMissing := &pgconn.PgError{Code: "23503", Message: "token does not exist in tenant or is revoked"}
	if err := mapMediatedMutationError(tokenMissing, "token", "id"); !errors.As(err, &notFoundErr) || notFoundErr.Type != "token" {
		t.Fatalf("token error=%v (%T), want token NotFoundError", err, err)
	}
	other := &pgconn.PgError{Code: "23505", Message: "actor is already provisioned in tenant"}
	if err := mapMediatedMutationError(other, "user", "id"); !errors.Is(err, other) {
		t.Fatalf("unrelated SQLSTATE rewritten: %v", err)
	}
	if err := mapMediatedMutationError(errors.New("boom"), "user", "id"); err.Error() != "boom" {
		t.Fatalf("plain error rewritten: %v", err)
	}
}

func TestProvisionGrantPayloadCanonicalization(t *testing.T) {
	payload, err := provisionGrantPayload(userGrants(identity.UserCreate{
		Roles:      []string{"owner", "admin", "owner"},
		Workspaces: []string{" w1 ", ""},
		Projects:   []string{"p1"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"project","value":"p1"},{"type":"role","value":"admin"},{"type":"role","value":"owner"},{"type":"workspace","value":"w1"}]`
	if got := string(payload); got != want {
		t.Fatalf("payload=%s, want %s", got, want)
	}
	if _, err := provisionGrantPayload(nil); err == nil {
		t.Fatal("empty grant payload accepted")
	}
	// Values that need JSON escaping must survive round-trip into the
	// migration-owned provisioning routine.
	encoded, err := provisionGrantPayload([]persistedGrant{{kind: "role", value: `a "quoted" \ value`}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `a \"quoted\" \\ value`) {
		t.Fatalf("payload=%s, want escaped value", encoded)
	}
}

func TestBindActorFailsClosedForOpaqueSubjects(t *testing.T) {
	ctx := context.Background()
	resolved, err := (&Store{principal: domain.Principal{Subject: uuid.NewString()}}).bindActor(ctx, nil)
	if err != nil {
		t.Fatalf("uuid subject rejected: %v", err)
	}
	if _, ok := resolved.Value(actorKey{}).(uuid.UUID); !ok {
		t.Fatal("resolved actor missing from transaction context")
	}
	opaque := &Store{principal: domain.Principal{Subject: "opaque-subject"}}
	if got, err := opaque.bindActor(ctx, nil); !errors.Is(err, ErrPrincipalRequired) {
		t.Fatalf("opaque subject error=%v, want %v", err, ErrPrincipalRequired)
	} else if got != nil {
		t.Fatal("opaque subject must not produce a transaction context")
	}
}

func TestRotatedRecordCarriesSubjectAndPrincipalType(t *testing.T) {
	scanned := identity.TokenIssue{Subject: uuid.NewString(), PrincipalType: "service_account", Name: "rotated", Scopes: []string{"read"}, Workspaces: []string{uuid.NewString()}, ExpiresAt: time.Unix(1000, 0)}
	rec := assembleRotatedRecord(scanned, "token-id", "ctx_prefix12", []byte("digest-bytes"), "org-id")
	if rec.Subject != scanned.Subject {
		t.Fatalf("rotated record subject=%q, want %q", rec.Subject, scanned.Subject)
	}
	if rec.PrincipalType != "service_account" {
		t.Fatalf("rotated record principal type=%q, want service_account", rec.PrincipalType)
	}
	// REQ-BPR-006: keep every diagnostic field-level; the record and its
	// digest are never printable failure payloads.
	wantDigest := base64.RawURLEncoding.EncodeToString([]byte("digest-bytes"))
	if rec.ID != "token-id" {
		t.Fatalf("rotated record id=%q, want %q", rec.ID, "token-id")
	}
	if rec.Prefix != "ctx_prefix12" {
		t.Fatalf("rotated record prefix=%q, want %q", rec.Prefix, "ctx_prefix12")
	}
	if rec.Name != "rotated" {
		t.Fatalf("rotated record name=%q, want %q", rec.Name, "rotated")
	}
	if rec.OrgID != "org-id" {
		t.Fatalf("rotated record org=%q, want %q", rec.OrgID, "org-id")
	}
	if rec.Digest != wantDigest {
		t.Fatal("rotated record digest mismatch")
	}
	if !rec.ExpiresAt.Equal(time.Unix(1000, 0)) {
		t.Fatal("rotated record expiry mismatch")
	}
	if len(rec.Scopes) != 1 {
		t.Fatalf("rotated record scopes len=%d, want 1", len(rec.Scopes))
	}
	if len(rec.Workspaces) != 1 {
		t.Fatalf("rotated record workspaces len=%d, want 1", len(rec.Workspaces))
	}
	// The returned record must not alias the scanned slices: later mutation
	// of the scan target can never leak into the issued record.
	scanned.Scopes[0] = "mutated"
	if rec.Scopes[0] != "read" {
		t.Fatal("rotated record aliases scanned scopes")
	}
}

func TestRepositoryErrorAndOutboxCauseHelpers(t *testing.T) {
	err := notFound("observation", 42)
	if err == nil || err.Error() == "" {
		t.Fatal("not-found helper returned an empty error")
	}
	if causeString(nil) != "" || causeString(errors.New("boom")) != "boom" {
		t.Fatal("outbox cause helper mismatch")
	}
}

func mustClosedPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, "postgres://127.0.0.1:1/cortex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
