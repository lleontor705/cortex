//go:build postgres_integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/server/external"
)

type integrationReindexProvider struct{ mutate func() error }

func (p integrationReindexProvider) Embed(_ context.Context, texts []string) ([][]float32, domain.ModelInfo, error) {
	if err := p.mutate(); err != nil {
		return nil, domain.ModelInfo{}, err
	}
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{1, 0}
	}
	return vectors, domain.ModelInfo{Name: "integration", Dimension: 2}, nil
}
func (integrationReindexProvider) ModelInfo() domain.ModelInfo {
	return domain.ModelInfo{Name: "integration", Dimension: 2}
}
func (integrationReindexProvider) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}

type integrationReindexTarget struct{ upserted int }

func (*integrationReindexTarget) ID() string { return "integration" }
func (t *integrationReindexTarget) Upsert(_ context.Context, points []domain.VectorPoint) error {
	t.upserted += len(points)
	return nil
}
func (*integrationReindexTarget) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (*integrationReindexTarget) Delete(context.Context, []int64) error { return nil }
func (*integrationReindexTarget) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}
func (*integrationReindexTarget) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}
func (*integrationReindexTarget) Close() error { return nil }

type scopedCodeFixture struct {
	tenant    uuid.UUID
	workspace uuid.UUID
	project   uuid.UUID
	store     *AuthorizedStore
}

func TestPostgresReindexSourceUsesDurableProjectIdentityAndRejectsSiblingCorpus(t *testing.T) {
	h := newPostgresHarness(t)
	tenant, workspaceA, workspaceB := uuid.New(), uuid.New(), uuid.New()
	projectA, projectB := uuid.New(), uuid.New()
	primary := newScopedCodeFixture(t, h, tenant, workspaceA, projectA, "same-label")
	sibling := newScopedCodeFixture(t, h, tenant, workspaceB, projectB, "same-label")

	insertObservation := func(f scopedCodeFixture, title string) int64 {
		t.Helper()
		var id int64
		if err := h.admin.QueryRow(t.Context(), `
			WITH se AS (
				INSERT INTO sessions(tenant_id,workspace_id,project_id,project_key,started_at)
				SELECT $1,w.id,p.id,$4,now() FROM workspaces w JOIN projects p ON p.tenant_id=w.tenant_id AND p.workspace_id=w.id
				WHERE w.tenant_id=$1 AND w.public_id=$2 AND p.public_id=$3 RETURNING id,workspace_id,project_id
			)
			INSERT INTO observations(tenant_id,workspace_id,project_id,session_id,project_key,type,title,content)
			SELECT $1,se.workspace_id,se.project_id,se.id,$4,'manual',$5,'body' FROM se RETURNING id`,
			f.tenant, f.workspace, f.project, "same-label", title).Scan(&id); err != nil {
			t.Fatalf("insert observation: %v", err)
		}
		return id
	}
	primaryID := insertObservation(primary, "primary")
	siblingID := insertObservation(sibling, "sibling")

	source, err := NewPostgresReindexSource(t.Context(), primary.store, projectA.String())
	if err != nil {
		t.Fatal(err)
	}
	if source.CanonicalProjectLabel() != "same-label" || source.ReindexScope().ProjectID != projectA.String() {
		t.Fatalf("durable binding = %q/%+v", source.CanonicalProjectLabel(), source.ReindexScope())
	}
	items, err := source.List(t.Context(), source.ReindexScope(), domain.ObservationFilter{Limit: 64, OrderAsc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != primaryID || items[0].Title != "primary" {
		t.Fatalf("scoped observations = %#v", items)
	}
	if _, err := source.Scope(t.Context(), siblingID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sibling Scope error = %v, want ErrNotFound", err)
	}
	if _, _, err := source.GetEmbedding(t.Context(), source.ReindexScope(), primaryID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetEmbedding error = %v, want ErrNotFound", err)
	}
	forged := external.ReindexScope{TenantID: tenant.String(), WorkspaceID: workspaceB.String(), ProjectID: projectB.String()}
	if _, err := source.List(t.Context(), forged, domain.ObservationFilter{Limit: 64, OrderAsc: true}); err == nil {
		t.Fatal("forged sibling scope accepted")
	}
	if _, err := NewPostgresReindexSource(t.Context(), primary.store, projectB.String()); err == nil {
		t.Fatal("ungranted sibling project resolved")
	}
}

func newReindexPrincipalStore(t *testing.T, h *postgresHarness, fixture scopedCodeFixture, subject uuid.UUID, principalType string, roles, clearance, scopes []string) *AuthorizedStore {
	t.Helper()
	_, provenance := mintBindingProvenance(t, h, fixture.tenant, subject, 1, "reindex-visibility")
	grantScopedCodeAccess(t, h, fixture.tenant, subject, fixture.workspace, fixture.project)
	principal := domain.Principal{Subject: subject.String(), Type: principalType, OrgID: fixture.tenant.String(), Roles: roles, Scopes: scopes, WorkspaceIDs: []string{fixture.workspace.String()}, ProjectIDs: []string{fixture.project.String()}, ClassificationClearance: clearance, GrantDigest: provenance, GrantVersion: 1}
	store, err := NewAuthorizedStore(h.pool, authz.AuthorizedContext{Principal: principal, Tenant: domain.TenantContext{TenantID: fixture.tenant.String(), WorkspaceID: fixture.workspace.String()}, GrantDigest: provenance})
	if err != nil {
		t.Fatalf("construct reindex principal: %v", err)
	}
	return store
}

func insertReindexObservation(t *testing.T, h *postgresHarness, fixture scopedCodeFixture, title, classification string, owner uuid.UUID) int64 {
	t.Helper()
	var id int64
	err := h.admin.QueryRow(t.Context(), `WITH se AS (INSERT INTO sessions(tenant_id,workspace_id,project_id,project_key,started_at) SELECT $1,w.id,p.id,$4,now() FROM workspaces w JOIN projects p ON p.tenant_id=w.tenant_id AND p.workspace_id=w.id WHERE w.tenant_id=$1 AND w.public_id=$2 AND p.public_id=$3 RETURNING id,workspace_id,project_id) INSERT INTO observations(tenant_id,workspace_id,project_id,session_id,project_key,type,title,content,classification,owner_subject) SELECT $1,se.workspace_id,se.project_id,se.id,$4,'manual',$5,$5,$6,$7 FROM se RETURNING id`, fixture.tenant, fixture.workspace, fixture.project, fixture.project.String(), title, classification, owner.String()).Scan(&id)
	if err != nil {
		t.Fatalf("insert reindex observation %s: %v", title, err)
	}
	return id
}

func TestPostgresReindexSourceAppliesPrincipalVisibilityBeforeEmbedding(t *testing.T) {
	h := newPostgresHarness(t)
	fixture := newScopedCodeFixture(t, h, uuid.New(), uuid.New(), uuid.New(), "visibility")
	serviceSubject, adminSubject, clearedSubject, foreignSubject := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	insertReindexObservation(t, h, fixture, "normal", "project", foreignSubject)
	insertReindexObservation(t, h, fixture, "restricted", "restricted", foreignSubject)
	insertReindexObservation(t, h, fixture, "confidential", "confidential", foreignSubject)
	insertReindexObservation(t, h, fixture, "foreign-personal", "personal", foreignSubject)
	insertReindexObservation(t, h, fixture, "service-personal", "personal", serviceSubject)
	insertReindexObservation(t, h, fixture, "admin-personal", "personal", adminSubject)
	insertReindexObservation(t, h, fixture, "cleared-personal", "personal", clearedSubject)

	tests := []struct {
		name  string
		store *AuthorizedStore
		want  map[string]bool
	}{
		{name: "service_account_without_clearance", store: newReindexPrincipalStore(t, h, fixture, serviceSubject, "service_account", nil, nil, []string{"admin:manage", "project:" + fixture.project.String()}), want: map[string]bool{"normal": true, "service-personal": true}},
		{name: "admin_without_clearance", store: newReindexPrincipalStore(t, h, fixture, adminSubject, "user", []string{"admin"}, nil, nil), want: map[string]bool{"normal": true, "admin-personal": true}},
		{name: "admin_with_granted_clearance", store: newReindexPrincipalStore(t, h, fixture, clearedSubject, "user", []string{"admin"}, []string{"restricted", "confidential"}, nil), want: map[string]bool{"normal": true, "restricted": true, "confidential": true, "cleared-personal": true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source, err := NewPostgresReindexSource(t.Context(), tc.store, fixture.project.String())
			if err != nil {
				t.Fatal(err)
			}
			items, err := source.List(t.Context(), source.ReindexScope(), domain.ObservationFilter{Limit: 100, OrderAsc: true})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, item := range items {
				got[item.Title] = true
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("visible titles = %v, want %v", got, tc.want)
			}
			descriptor, err := source.DescribeCorpus(t.Context(), source.ReindexScope())
			if err != nil {
				t.Fatal(err)
			}
			if descriptor.Count != len(tc.want) {
				t.Fatalf("descriptor count = %d, visible = %d", descriptor.Count, len(tc.want))
			}
			repeated, err := source.DescribeCorpus(t.Context(), source.ReindexScope())
			if err != nil {
				t.Fatal(err)
			}
			if repeated != descriptor {
				t.Fatalf("stable corpus descriptor changed without mutation: before=%+v after=%+v", descriptor, repeated)
			}
		})
	}
}

func TestPostgresReindexRejectsConcurrentCorpusInsertUpdateAndDelete(t *testing.T) {
	for _, mutation := range []string{"insert", "update", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			h := newPostgresHarness(t)
			fixture := newScopedCodeFixture(t, h, uuid.New(), uuid.New(), uuid.New(), "mutation-"+mutation)
			subject := uuid.New()
			observationID := insertReindexObservation(t, h, fixture, "original", "project", subject)
			store := newReindexPrincipalStore(t, h, fixture, subject, "user", []string{"admin"}, nil, nil)
			source, err := NewPostgresReindexSource(t.Context(), store, fixture.project.String())
			if err != nil {
				t.Fatal(err)
			}
			provider := integrationReindexProvider{mutate: func() error {
				switch mutation {
				case "insert":
					insertReindexObservation(t, h, fixture, "inserted-during-run", "project", subject)
					return nil
				case "update":
					_, err := h.admin.Exec(t.Context(), `UPDATE observations SET title='updated-during-run' WHERE tenant_id=$1 AND id=$2`, fixture.tenant, observationID)
					return err
				case "delete":
					_, err := h.admin.Exec(t.Context(), `UPDATE observations SET deleted_at=now() WHERE tenant_id=$1 AND id=$2`, fixture.tenant, observationID)
					return err
				default:
					return errors.New("unknown mutation")
				}
			}}
			target := &integrationReindexTarget{}
			result, err := external.Reindex(t.Context(), source, provider, target, external.ReindexOptions{TenantID: fixture.tenant.String(), WorkspaceID: fixture.workspace.String(), ProjectID: fixture.project.String(), BatchSize: 1})
			if !errors.Is(err, external.ErrReindexCorpusChanged) {
				t.Fatalf("error = %v, want corpus changed", err)
			}
			if result == nil || target.upserted == 0 {
				t.Fatalf("result/target = %+v/%d; mutation did not occur inside run", result, target.upserted)
			}
		})
	}
}

func grantScopedCodeAccess(t *testing.T, h *postgresHarness, tenant, subject, workspace, project uuid.UUID) {
	t.Helper()
	if _, err := h.admin.Exec(t.Context(), `INSERT INTO principal_grants(tenant_id,actor_public_id,grant_type,grant_value) VALUES($1,$2,'workspace',$3),($1,$2,'project',$4) ON CONFLICT (tenant_id,actor_public_id,grant_type,grant_value) DO NOTHING`, tenant, subject, workspace.String(), project.String()); err != nil {
		t.Fatalf("seed code scope grants: %v", err)
	}
}

func newScopedCodeFixture(t *testing.T, h *postgresHarness, tenant, workspace, project uuid.UUID, projectName string) scopedCodeFixture {
	t.Helper()
	ctx := t.Context()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2) ON CONFLICT (tenant_id) DO NOTHING`, tenant, "code-"+tenant.String()); err != nil {
		t.Fatalf("seed code organization: %v", err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, "code-"+workspace.String()); err != nil {
		t.Fatalf("seed code workspace: %v", err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO projects(tenant_id,workspace_id,public_id,name) VALUES($1,(SELECT id FROM workspaces WHERE tenant_id=$1 AND public_id=$2),$3,$4)`, tenant, workspace, project, projectName); err != nil {
		t.Fatalf("seed code project: %v", err)
	}

	subject := uuid.New()
	_, provenance := mintBindingProvenance(t, h, tenant, subject, 1, "code-isolation")
	grantScopedCodeAccess(t, h, tenant, subject, workspace, project)
	principal := domain.Principal{
		Subject: subject.String(), Type: "user", OrgID: tenant.String(),
		Roles: []string{"owner"}, WorkspaceIDs: []string{workspace.String()},
		ProjectIDs: []string{project.String()}, ClassificationClearance: []string{"*"},
		GrantDigest: provenance, GrantVersion: 1,
	}
	store, err := NewAuthorizedStore(h.pool, authz.AuthorizedContext{
		Principal:   principal,
		Tenant:      domain.TenantContext{TenantID: tenant.String(), WorkspaceID: workspace.String()},
		GrantDigest: provenance,
	})
	if err != nil {
		t.Fatalf("construct scoped code store: %v", err)
	}
	return scopedCodeFixture{tenant: tenant, workspace: workspace, project: project, store: store}
}

func saveScopedGraph(t *testing.T, fixture scopedCodeFixture, marker string) {
	t.Helper()
	project := fixture.project.String()
	graph := &code.CodeGraph{
		Project: project,
		Symbols: []code.Symbol{
			{ID: "shared-entry", Project: project, FilePath: marker + "/ApplicationDbContext.cs", LineNumber: 1, EndLine: 4, Kind: "class", Name: marker + "DbContext", Signature: "class " + marker + "DbContext"},
			{ID: "shared-helper", Project: project, FilePath: marker + "/Repository.cs", LineNumber: 8, EndLine: 9, Kind: "method", Name: marker + "Repository"},
		},
		Relations: []code.Relation{{Project: project, SourceID: "shared-entry", TargetID: "shared-helper", Relation: "uses", Confidence: 1}},
	}
	if err := fixture.store.SaveCodeGraph(t.Context(), graph); err != nil {
		t.Fatalf("save %s graph: %v", marker, err)
	}
}

func TestPostgresScopedCodeIndexIsolatesTenantWorkspaceProjectAndLegacyRows(t *testing.T) {
	h := newPostgresHarness(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	workspaceA, siblingWorkspace, workspaceB := uuid.New(), uuid.New(), uuid.New()
	projectA, siblingProject, sameWorkspaceProject, projectB := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	primary := newScopedCodeFixture(t, h, tenantA, workspaceA, projectA, "primary")
	sibling := newScopedCodeFixture(t, h, tenantA, siblingWorkspace, siblingProject, "sibling-workspace")
	foreign := newScopedCodeFixture(t, h, tenantB, workspaceB, projectB, "foreign-tenant")

	// A second project shares the primary tenant/workspace. Seed it without
	// recreating that workspace to prove project identity is part of scope.
	if _, err := h.admin.Exec(t.Context(), `INSERT INTO projects(tenant_id,workspace_id,public_id,name) VALUES($1,(SELECT id FROM workspaces WHERE tenant_id=$1 AND public_id=$2),$3,'same-workspace-project')`, tenantA, workspaceA, sameWorkspaceProject); err != nil {
		t.Fatalf("seed same-workspace project: %v", err)
	}
	subject := uuid.New()
	_, provenance := mintBindingProvenance(t, h, tenantA, subject, 1, "same-workspace")
	grantScopedCodeAccess(t, h, tenantA, subject, workspaceA, sameWorkspaceProject)
	principal := domain.Principal{Subject: subject.String(), Type: "user", OrgID: tenantA.String(), Roles: []string{"owner"}, WorkspaceIDs: []string{workspaceA.String()}, ProjectIDs: []string{sameWorkspaceProject.String()}, GrantDigest: provenance, GrantVersion: 1}
	sameProjectStore, err := NewAuthorizedStore(h.pool, authz.AuthorizedContext{Principal: principal, Tenant: domain.TenantContext{TenantID: tenantA.String(), WorkspaceID: workspaceA.String()}, GrantDigest: provenance})
	if err != nil {
		t.Fatal(err)
	}
	sameWorkspace := scopedCodeFixture{tenant: tenantA, workspace: workspaceA, project: sameWorkspaceProject, store: sameProjectStore}

	saveScopedGraph(t, primary, "primary")
	saveScopedGraph(t, sibling, "sibling")
	saveScopedGraph(t, sameWorkspace, "same-project")
	saveScopedGraph(t, foreign, "foreign")

	// No sessions or observations were seeded for these projects. Readiness
	// must therefore be discovered through a project-bound code transaction;
	// an unbound EXISTS against the RLS table would incorrectly return none.
	listed, err := primary.store.ListAgentProjects(t.Context())
	if err != nil {
		t.Fatalf("list code-only agent projects: %v", err)
	}
	if len(listed) != 1 || listed[projectA.String()] != "primary" {
		t.Fatalf("code-only agent projects = %#v; want primary UUID", listed)
	}

	for name, fixture := range map[string]scopedCodeFixture{
		"primary": primary, "sibling workspace": sibling, "same workspace project": sameWorkspace, "foreign tenant": foreign,
	} {
		t.Run(name, func(t *testing.T) {
			symbols, err := fixture.store.ListCodeSymbols(t.Context(), code.SymbolFilter{Project: fixture.project.String(), Query: "DbContext"})
			if err != nil {
				t.Fatal(err)
			}
			if len(symbols) != 1 || symbols[0].Project != fixture.project.String() || !strings.Contains(symbols[0].FilePath, strings.Fields(name)[0]) {
				t.Fatalf("scoped symbols = %#v; want exactly the bound corpus", symbols)
			}
			graph, err := fixture.store.GetCodeGraph(t.Context(), fixture.project.String())
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.Symbols) != 2 || len(graph.Relations) != 1 || graph.Relations[0].Project != fixture.project.String() {
				t.Fatalf("scoped graph = %#v; want 2 symbols and 1 relation from bound project", graph)
			}
		})
	}

	// The app role cannot force a sibling workspace/project into a transaction
	// already bound to primary. This is the database RLS oracle, independent
	// of the store's explicit WHERE predicates.
	err = primary.store.store.transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		var workspaceID, projectID int64
		if err := h.admin.QueryRow(ctx, `SELECT w.id,p.id FROM workspaces w JOIN projects p ON p.tenant_id=w.tenant_id AND p.workspace_id=w.id WHERE w.tenant_id=$1 AND w.public_id=$2 AND p.public_id=$3`, tenantA, siblingWorkspace, siblingProject).Scan(&workspaceID, &projectID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO scoped_code_index_state(tenant_id,workspace_id,project_id,project,state) VALUES($1,$2,$3,$4,'ready')`, tenantA, workspaceID, projectID, siblingProject.String())
		return err
	})
	if err == nil {
		t.Fatal("bound primary transaction inserted sibling workspace/project row; want RLS denial")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("cross-scope insert error = %v; want PostgreSQL RLS 42501", err)
	}

	// A legacy project-only row is ambiguous by construction. Even when a
	// privileged fixture creates it, the scoped store must never rank or cite it.
	legacyName := "legacy-secret-" + uuid.NewString()
	var legacyExists bool
	if err := h.admin.QueryRow(t.Context(), `SELECT to_regclass('public.code_symbols') IS NOT NULL`).Scan(&legacyExists); err != nil {
		t.Fatal(err)
	}
	createdLegacy := !legacyExists
	if createdLegacy {
		if _, err := h.admin.Exec(t.Context(), `CREATE TABLE code_symbols(id varchar(64) PRIMARY KEY,project varchar(128) NOT NULL,file_path text NOT NULL,line_number integer NOT NULL,kind varchar(32) NOT NULL,name varchar(255) NOT NULL); CREATE TABLE code_relations(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,project varchar(128) NOT NULL,source_id varchar(64) NOT NULL,target_id varchar(64) NOT NULL,relation varchar(32) NOT NULL)`); err != nil {
			t.Fatalf("create legacy fixture tables: %v", err)
		}
		t.Cleanup(func() {
			_, _ = h.admin.Exec(context.Background(), `DROP TABLE code_relations, code_symbols`)
		})
	}
	legacyID := uuid.NewString()
	if _, err := h.admin.Exec(t.Context(), `INSERT INTO code_symbols(id,project,file_path,line_number,kind,name) VALUES($1,$2,'legacy/ApplicationDbContext.cs',1,'class',$3)`, legacyID, primary.project.String(), legacyName); err != nil {
		t.Fatalf("seed legacy symbol: %v", err)
	}
	if !createdLegacy {
		t.Cleanup(func() { _, _ = h.admin.Exec(context.Background(), `DELETE FROM code_symbols WHERE id=$1`, legacyID) })
	}
	symbols, err := primary.store.ListCodeSymbols(t.Context(), code.SymbolFilter{Project: primary.project.String(), Query: legacyName})
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 0 {
		t.Fatalf("legacy symbol leaked through scoped retrieval: %#v", symbols)
	}
}
