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
)

type failingAdminAudit struct{ err error }

func (a failingAdminAudit) Record(context.Context, authz.AuditEvent) error { return a.err }

func TestAuthorizeAdminManageUsesAuditedAdminBoundary(t *testing.T) {
	tenant, workspace := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name    string
		role    string
		audit   authz.AuditSink
		wantErr bool
	}{
		{name: "admin", role: "admin"},
		{name: "member denied", role: "member", wantErr: true},
		{name: "viewer denied", role: "viewer", wantErr: true},
		{name: "audit failure denies admin", role: "admin", audit: failingAdminAudit{err: errors.New("audit unavailable")}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			principal := domain.Principal{Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace}, Roles: []string{tc.role}}
			policy := authz.NewPolicy()
			policy.Audit = tc.audit
			store := &AuthorizedStore{store: &Store{
				tenant: &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace}, principal: principal, authorizer: policy,
			}}
			err := store.AuthorizeAdminManage(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("AuthorizeAdminManage() error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestCodeOperationsAuthorizeBeforeDatabaseAccess(t *testing.T) {
	tenant, workspace, project := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name       string
		roles      []string
		projectIDs []string
	}{
		{name: "role lacks code read", roles: []string{"operator"}, projectIDs: []string{project}},
		{name: "project not granted", roles: []string{"viewer"}, projectIDs: []string{uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &AuthorizedStore{
				store: &Store{
					tenant:     &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
					principal:  domain.Principal{Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace}, Roles: tc.roles, ProjectIDs: tc.projectIDs},
					authorizer: authz.NewPolicy(),
				},
			}
			if _, err := store.GetCodeGraph(context.Background(), project); err == nil {
				t.Fatal("GetCodeGraph expected authorization error before nil pool access")
			}
		})
	}
}

func TestListAgentProjectsRequiresSearchCapabilityBeforeDatabaseAccess(t *testing.T) {
	tenant, workspace := uuid.NewString(), uuid.NewString()
	store := &AuthorizedStore{store: &Store{
		tenant:     &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
		principal:  domain.Principal{Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace}, Roles: []string{"operator"}},
		authorizer: authz.NewPolicy(),
	}}
	if _, err := store.ListAgentProjects(context.Background()); err == nil {
		t.Fatal("ListAgentProjects expected capability denial before nil pool access")
	}
}

type agentProjectsTestTx struct {
	pgx.Tx
	projectID string
	label     string
	bound     bool
}

func (t *agentProjectsTestTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "cortex_bind_project_scope") {
		t.bound = true
	}
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (t *agentProjectsTestTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &agentProjectsRows{projectID: t.projectID, label: t.label}, nil
}

func (t *agentProjectsTestTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	return agentProjectsRow{tx: t, sql: sql}
}

type agentProjectsRows struct {
	pgx.Rows
	projectID string
	label     string
	done      bool
}

func (r *agentProjectsRows) Next() bool { return !r.done }
func (r *agentProjectsRows) Close()     {}
func (r *agentProjectsRows) Err() error { return nil }
func (r *agentProjectsRows) Scan(dest ...any) error {
	r.done = true
	*dest[0].(*string) = r.projectID
	*dest[1].(*string) = r.label
	*dest[2].(*bool) = false // no memory corpus
	if len(dest) == 4 {
		*dest[3].(*bool) = false // unbound RLS cannot advertise code readiness
	}
	return nil
}

type agentProjectsRow struct {
	tx  *agentProjectsTestTx
	sql string
}

func (r agentProjectsRow) Scan(dest ...any) error {
	if strings.Contains(r.sql, "cortex_current_project") {
		*dest[0].(*uuid.UUID) = uuid.MustParse("10000000-a000-0000-0000-000000000001")
		*dest[1].(*int64) = 42
		*dest[2].(*int64) = 73
		return nil
	}
	if strings.Contains(r.sql, "scoped_code_index_state") {
		if !r.tx.bound {
			return errors.New("code readiness queried before project bind")
		}
		*dest[0].(*string) = "ready"
		return nil
	}
	return pgx.ErrNoRows
}

func TestListAgentProjectsDetectsCodeOnlyProjectAfterTrustedBind(t *testing.T) {
	tenant, workspace, project := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx := &agentProjectsTestTx{projectID: project, label: "same-label"}
	store := &AuthorizedStore{store: &Store{
		tenant: &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
		principal: domain.Principal{Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace},
			Roles: []string{"viewer"}, ProjectIDs: []string{project}},
		authorizer: authz.NewPolicy(),
	}}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(42))
	projects, err := store.ListAgentProjects(ctx)
	if err != nil {
		t.Fatalf("ListAgentProjects() = %v", err)
	}
	if !tx.bound || projects[project] != "same-label" {
		t.Fatalf("bound=%v projects=%#v; want code-only project", tx.bound, projects)
	}
}

type agentSearchTestTx struct {
	pgx.Tx
	projectInternalID int64
	label             string
	queries           []stubQuery
}

func (t *agentSearchTestTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.queries = append(t.queries, stubQuery{sql: sql, args: append([]any(nil), args...)})
	return agentProjectLabelRow{projectInternalID: t.projectInternalID, label: t.label}
}

func (t *agentSearchTestTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.queries = append(t.queries, stubQuery{sql: sql, args: append([]any(nil), args...)})
	return emptyAgentSearchRows{}, nil
}

type agentProjectLabelRow struct {
	projectInternalID int64
	label             string
}

func (r agentProjectLabelRow) Scan(dest ...any) error {
	if r.label == "" {
		return pgx.ErrNoRows
	}
	*dest[0].(*int64) = r.projectInternalID
	*dest[1].(*string) = r.label
	return nil
}

type emptyAgentSearchRows struct{ pgx.Rows }

func (emptyAgentSearchRows) Next() bool { return false }
func (emptyAgentSearchRows) Err() error { return nil }
func (emptyAgentSearchRows) Close()     {}

type agentHydrationTestTx struct {
	pgx.Tx
	query stubQuery
}

func (t *agentHydrationTestTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.query = stubQuery{sql: sql, args: append([]any(nil), args...)}
	return agentHydrationNotFoundRow{}
}

type agentHydrationNotFoundRow struct{}

func (agentHydrationNotFoundRow) Scan(...any) error { return pgx.ErrNoRows }

func TestGetAgentObservationByIDAppliesCanonicalVisibilityPredicate(t *testing.T) {
	tenant, workspace, projectID, subject := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &AuthorizedStore{store: &Store{
		tenant: &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
		principal: domain.Principal{Subject: subject, Type: "user", OrgID: tenant,
			Roles: []string{"viewer"}, WorkspaceIDs: []string{workspace}, ProjectIDs: []string{projectID},
			ClassificationClearance: []string{"restricted"}},
		authorizer: authz.NewPolicy(),
	}}
	tx := &agentHydrationTestTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(42))

	if _, err := store.GetAgentObservationByID(ctx, projectID, "shared-label", 73); !errors.Is(err, authz.ErrResourceNotFound) {
		t.Fatalf("GetAgentObservationByID() error = %v, want resource not found", err)
	}
	if !strings.Contains(tx.query.sql, "o.classification = ANY($5)") ||
		!strings.Contains(tx.query.sql, "o.classification <> 'personal' OR o.owner_subject=$6") {
		t.Fatalf("hydration query omitted canonical visibility predicate: %s", tx.query.sql)
	}
	if len(tx.query.args) != 6 {
		t.Fatalf("hydration args = %#v, want six scope and visibility arguments", tx.query.args)
	}
	if got := tx.query.args[4]; fmt.Sprint(got) != "[restricted]" {
		t.Fatalf("classification argument = %v, want [restricted]", got)
	}
	if got := tx.query.args[5]; got != subject {
		t.Fatalf("owner argument = %v, want authenticated subject", got)
	}
}

func TestSearchAgentObservationsAuthorizesUUIDAndFiltersCanonicalLabel(t *testing.T) {
	tenant, workspace, projectID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &AuthorizedStore{store: &Store{
		tenant: &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
		principal: domain.Principal{Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace},
			Roles: []string{"viewer"}, ProjectIDs: []string{projectID}},
		authorizer: authz.NewPolicy(),
	}}

	t.Run("colliding label still filters resolved internal project id", func(t *testing.T) {
		// Another granted project may legally carry the same display label;
		// only this public UUID's resolved bigint may select observations.
		tx := &agentSearchTestTx{projectInternalID: 73, label: "shared-label"}
		ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(42))
		results, err := store.SearchAgentObservations(ctx, projectID, "shared-label", "server architecture", domain.SearchOptions{Project: "forged-client-label", Limit: 5})
		if err != nil || len(results) != 0 {
			t.Fatalf("SearchAgentObservations() = %#v, %v", results, err)
		}
		if len(tx.queries) != 2 || !strings.Contains(tx.queries[0].sql, "public_id=$2::uuid") || !strings.Contains(tx.queries[1].sql, "o.project_id=$3") || strings.Contains(tx.queries[1].sql, "o.project_key=$3") {
			t.Fatalf("queries do not preserve id/label boundary: %#v", tx.queries)
		}
		if got := tx.queries[1].args[2]; got != int64(73) {
			t.Fatalf("corpus filter = %v, want resolved internal project id", got)
		}
	})

	t.Run("label mismatch fails before corpus read", func(t *testing.T) {
		tx := &agentSearchTestTx{projectInternalID: 73, label: "cortex"}
		ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(42))
		if _, err := store.SearchAgentObservations(ctx, projectID, "other", "query", domain.SearchOptions{}); err == nil || err.Error() != authz.DenyProject {
			t.Fatalf("mismatch error = %v, want %s", err, authz.DenyProject)
		}
		if len(tx.queries) != 1 {
			t.Fatalf("mismatch executed corpus query: %#v", tx.queries)
		}
	})

	t.Run("sibling project denied before database", func(t *testing.T) {
		tx := &agentSearchTestTx{projectInternalID: 99, label: "sibling"}
		ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(42))
		if _, err := store.SearchAgentObservations(ctx, uuid.NewString(), "sibling", "query", domain.SearchOptions{}); err == nil || err.Error() != authz.DenyProject {
			t.Fatalf("sibling error = %v, want %s", err, authz.DenyProject)
		}
		if len(tx.queries) != 0 {
			t.Fatalf("denied sibling reached database: %#v", tx.queries)
		}
	})
}

func TestSaveCodeGraphRequiresCodeWriteBeforeDatabaseAccess(t *testing.T) {
	tenant, workspace, project := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &AuthorizedStore{
		store: &Store{
			tenant: &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
			principal: domain.Principal{
				Subject: uuid.NewString(), OrgID: tenant, WorkspaceIDs: []string{workspace},
				Roles: []string{"viewer"}, ProjectIDs: []string{project},
			},
			authorizer: authz.NewPolicy(),
		},
	}
	if err := store.SaveCodeGraph(context.Background(), &code.CodeGraph{Project: project}); err == nil {
		t.Fatal("SaveCodeGraph expected code write denial before nil pool access")
	}
}
