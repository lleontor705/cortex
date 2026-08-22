//go:build postgres_integration

// Workspace security exploit oracles (SDD tools-security-performance-hardening,
// work unit pg-exploit-proof, refs SEC-01/SEC-03, design:pg-exploit-proof).
//
// These are RED security regression tests. They pin the SECURE behavior that
// remediation tasks pg-list-search (T03) and pg-sync (T04) must establish:
//
//   - SEC-01: PostgreSQL observation List and Search are tenant-scoped but not
//     workspace-scoped (internal/store/postgres/repositories.go List,
//     internal/store/postgres/extras.go Search). With two sibling workspaces of
//     one tenant sharing project/classification grants, a principal bound to
//     one workspace must never observe sibling-workspace rows.
//   - SEC-03: PostgreSQL PushSync resolves sessions/observations/prompts/edges
//     by tenant-wide client IDs and conflicts on tenant-wide
//     (tenant_id, client_id) unique indexes (migrations/v2/102_sync.sql). A
//     sibling workspace pushing identical or known client IDs must never
//     mutate, soft-delete, or cross-reference rows it does not own.
//
// PRE-FIX EXPECTED FAILURE: every oracle that asserts a secure isolation
// invariant below is expected to FAIL against the current unfixed production
// SQL. The failing assertion messages carry the observed row-level evidence
// (public ids, workspace ids, content canaries) that empirically classifies
// the current behavior as VULNERABLE. After T03/T04 land, these tests must be
// GREEN without modification; they then remain committed as permanent
// regression oracles.
//
// The two-workspace fixture uses verified principal/workspace composition
// only: every store is an AuthorizedStore constructed from
// verify-minted binding provenance (mintBindingProvenance), and each
// workspace context comes from the principal's verified workspace grant, never
// from client input. Rows in each workspace are created through the
// workspace-bound save path, so any leak proven here is a read/sync scoping
// defect, not a fixture artifact.
//
// All three CORTEX_TEST_POSTGRES_* DSNs are exercised: the application DSN and
// the privileged migration DSN through newPostgresHarness, and the dedicated
// authorization-admin DSN in TestWorkspaceSecurityExploitAuthzBoundaryRemainsClosed
// to prove the exploitation surface is the app-role SQL scoping gap rather
// than a broken authorization boundary.
package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

const (
	// wsSecProject is the project key shared by both sibling workspaces. The
	// fixture principals hold explicit project grants for it, modeling the
	// audited overlap precondition (same tenant, same project key, same
	// default classification).
	wsSecProject = "wssec-shared-project"
	// Canaries exist only inside one workspace's content, so a hit from the
	// other workspace's principal is unambiguous row-level evidence.
	wsSecCanaryA = "wssec-canary-alpha-4f21"
	wsSecCanaryB = "wssec-canary-beta-8c73"
)

// wsSecFixture provisions one tenant with two sibling workspaces and one
// workspace-bound verified principal per workspace.
type wsSecFixture struct {
	h                      *postgresHarness
	tenant                 uuid.UUID
	workspaceA, workspaceB uuid.UUID
	subjectA, subjectB     uuid.UUID
	storeA, storeB         *AuthorizedStore
}

// newWsSecBoundStore builds an AuthorizedStore whose tenant/workspace context
// is derived exclusively from verify-minted binding provenance and the
// principal's verified workspace grant. The principal carries the member role
// plus the shared project grant so authorized list/search/sync operations are
// permitted for its OWN workspace only.
func newWsSecBoundStore(t *testing.T, h *postgresHarness, tenant, workspace, subject uuid.UUID) *AuthorizedStore {
	t.Helper()
	_, provenance := mintBindingProvenance(t, h, tenant, subject, 1, "wssec-digest")
	p := domain.Principal{
		Subject:      subject.String(),
		Type:         "user",
		OrgID:        tenant.String(),
		Roles:        []string{"member"},
		ProjectIDs:   []string{wsSecProject},
		WorkspaceIDs: []string{workspace.String()},
		GrantDigest:  provenance,
		GrantVersion: 1,
	}
	ac := authz.AuthorizedContext{
		Principal:   p,
		Tenant:      domain.TenantContext{TenantID: tenant.String(), WorkspaceID: workspace.String()},
		GrantDigest: p.GrantDigest,
	}
	store, err := NewAuthorizedStore(h.pool, ac)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newWsSecFixture(t *testing.T) *wsSecFixture {
	t.Helper()
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "wssec-org-"+tenant.String()); err != nil {
		t.Fatal(err)
	}
	f := &wsSecFixture{
		h:          h,
		tenant:     tenant,
		workspaceA: uuid.New(),
		workspaceB: uuid.New(),
		subjectA:   uuid.New(),
		subjectB:   uuid.New(),
	}
	for _, workspace := range []uuid.UUID{f.workspaceA, f.workspaceB} {
		if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, "wssec-ws-"+workspace.String()); err != nil {
			t.Fatal(err)
		}
	}
	f.storeA = newWsSecBoundStore(t, h, tenant, f.workspaceA, f.subjectA)
	f.storeB = newWsSecBoundStore(t, h, tenant, f.workspaceB, f.subjectB)
	return f
}

// wsSecSeedObservation creates one session plus one observation inside the
// given bound workspace through the authorized, workspace-scoped save path.
func wsSecSeedObservation(t *testing.T, store *AuthorizedStore, title, content string) *domain.Observation {
	t.Helper()
	ctx := context.Background()
	session := &domain.Session{Project: wsSecProject, StartedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("seed session for %q: %v", title, err)
	}
	obs := &domain.Observation{
		SessionID: session.ID,
		Project:   wsSecProject,
		Scope:     domain.ScopeProject,
		Source:    domain.SourceManual,
		Type:      domain.TypeManual,
		Title:     title,
		Content:   content,
	}
	if err := store.SaveObservation(ctx, obs); err != nil {
		t.Fatalf("seed observation %q: %v", title, err)
	}
	return obs
}

// wsSecObservationRow is sanitized, bounded row-level evidence read through
// the privileged migration handle (never exposed through the application
// role).
type wsSecObservationRow struct {
	PublicID, Workspace, Content, UpdatedBy string
	Deleted                                 bool
}

func wsSecObservationEvidence(t *testing.T, h *postgresHarness, tenant uuid.UUID, clientID string) []wsSecObservationRow {
	t.Helper()
	rows, err := h.admin.Query(context.Background(), `
		SELECT o.public_id::text, w.public_id::text, left(o.content,60), COALESCE(o.updated_by::text,''), o.deleted_at IS NOT NULL
		FROM observations o
		JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id
		JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id
		WHERE o.tenant_id=$1 AND o.client_id=$2
		ORDER BY o.id`, tenant, clientID)
	if err != nil {
		t.Fatalf("observation evidence query: %v", err)
	}
	defer rows.Close()
	var out []wsSecObservationRow
	for rows.Next() {
		var r wsSecObservationRow
		if err := rows.Scan(&r.PublicID, &r.Workspace, &r.Content, &r.UpdatedBy, &r.Deleted); err != nil {
			t.Fatalf("observation evidence scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("observation evidence iterate: %v", err)
	}
	return out
}

func (r wsSecObservationRow) String() string {
	return fmt.Sprintf("{public_id=%s workspace=%s content=%q updated_by=%s deleted=%v}", r.PublicID, r.Workspace, r.Content, r.UpdatedBy, r.Deleted)
}

// wsSecSyncBase is a fixed timestamp base so conflict-window ordering is
// deterministic and independent of wall-clock jitter.
var wsSecSyncBase = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// TestWorkspaceSecurityExploitSiblingListDisclosure pins SEC-01 for
// ObservationRepository.List.
//
// PRE-FIX EXPECTED FAILURE: List filters by tenant, project, and
// classification but carries no workspace predicate, so a member of workspace
// B holding the shared project grant currently observes workspace A rows.
func TestWorkspaceSecurityExploitSiblingListDisclosure(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	own := wsSecSeedObservation(t, f.storeA, "wssec-list-owned-A", "workspace A owns "+wsSecCanaryA)
	sibling := wsSecSeedObservation(t, f.storeB, "wssec-list-owned-B", "workspace B owns "+wsSecCanaryB)

	listed, err := f.storeB.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 100})
	if err != nil {
		t.Fatalf("workspace B list: %v", err)
	}
	var leaked []string
	for _, o := range listed {
		t.Logf("workspace B list row: public_id=%s title=%q", o.PublicID, o.Title)
		if o.Title == own.Title {
			leaked = append(leaked, fmt.Sprintf("public_id=%s title=%q", o.PublicID, o.Title))
		}
	}
	if len(listed) == 0 {
		t.Fatalf("workspace B list returned no rows at all; fixture is broken (own row %q missing)", sibling.Title)
	}
	if len(leaked) > 0 {
		t.Errorf("SEC-01 VULNERABLE (pre-fix expected failure): workspace B list disclosed %d sibling row(s) from workspace A: %s; sibling observation seeded as %q must never be visible to a workspace-B principal", len(leaked), strings.Join(leaked, ", "), own.Title)
	}

	reverse, err := f.storeA.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 100})
	if err != nil {
		t.Fatalf("workspace A list: %v", err)
	}
	var leakedReverse []string
	for _, o := range reverse {
		if o.Title == sibling.Title {
			leakedReverse = append(leakedReverse, fmt.Sprintf("public_id=%s title=%q", o.PublicID, o.Title))
		}
	}
	if len(leakedReverse) > 0 {
		t.Errorf("SEC-01 VULNERABLE (pre-fix expected failure): workspace A list disclosed sibling workspace B row(s): %s", strings.Join(leakedReverse, ", "))
	}
}

// TestWorkspaceSecurityExploitSiblingSearchDisclosure pins SEC-01 for
// SearchRepository.Search.
//
// PRE-FIX EXPECTED FAILURE: full-text search is tenant/project/classification
// scoped but not workspace scoped, so a term unique to workspace A content
// currently returns workspace A rows to a workspace B principal.
func TestWorkspaceSecurityExploitSiblingSearchDisclosure(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	wsSecSeedObservation(t, f.storeA, "wssec-search-owned-A", "alpha secret "+wsSecCanaryA)
	wsSecSeedObservation(t, f.storeB, "wssec-search-owned-B", "beta secret "+wsSecCanaryB)

	results, err := f.storeB.SearchObservations(ctx, wsSecCanaryA, domain.SearchOptions{Project: wsSecProject, Limit: 50})
	if err != nil {
		t.Fatalf("workspace B search: %v", err)
	}
	var leaked []string
	for _, r := range results {
		t.Logf("workspace B search hit: public_id=%s title=%q rank=%.4f", r.PublicID, r.Title, r.Rank)
		leaked = append(leaked, fmt.Sprintf("public_id=%s title=%q rank=%.4f", r.PublicID, r.Title, r.Rank))
	}
	if len(leaked) > 0 {
		t.Errorf("SEC-01 VULNERABLE (pre-fix expected failure): workspace B search for the workspace-A-only canary %q returned %d sibling row(s): %s", wsSecCanaryA, len(leaked), strings.Join(leaked, ", "))
	}

	// Control: the principal's own canary must remain searchable so the
	// post-fix oracle cannot pass vacuously.
	own, err := f.storeB.SearchObservations(ctx, wsSecCanaryB, domain.SearchOptions{Project: wsSecProject, Limit: 50})
	if err != nil || len(own) == 0 {
		t.Fatalf("workspace B own-canary control search: results=%d err=%v (must always find own rows)", len(own), err)
	}
}

// TestWorkspaceSecurityExploitSyncSessionSiblingClientIDs classifies session
// client-ID behavior across sibling workspaces.
//
// Session push is the SAFE surface today: sessions conflict on
// (tenant_id, workspace_id, client_id) and the insert resolves the workspace
// from the verified principal context, so identical sibling client IDs must
// coexist as independent rows and update independently. These assertions PASS
// pre-fix and MUST STAY GREEN after remediation (T02 requires sibling client
// IDs to coexist).
func TestWorkspaceSecurityExploitSyncSessionSiblingClientIDs(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	const clientID = "wssec-sess-dup"
	t1 := wsSecSyncBase
	t2 := wsSecSyncBase.Add(10 * time.Second)
	t3 := wsSecSyncBase.Add(20 * time.Second)

	if _, err := f.storeA.PushSync(ctx, &domain.SyncBatch{Sessions: []domain.SyncSession{{
		SyncID: clientID, Project: wsSecProject, StartedAt: t1, UpdatedAt: t1, Summary: "workspace A session",
	}}}); err != nil {
		t.Fatalf("workspace A session push: %v", err)
	}
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Sessions: []domain.SyncSession{{
		SyncID: clientID, Project: wsSecProject, StartedAt: t2, UpdatedAt: t2, Summary: "workspace B session",
	}}}); err != nil {
		t.Fatalf("workspace B sibling session push: %v", err)
	}
	rows, err := f.h.admin.Query(ctx, `
		SELECT s.public_id::text, w.public_id::text, COALESCE(s.summary,'')
		FROM sessions s JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id
		WHERE s.tenant_id=$1 AND s.client_id=$2 ORDER BY s.id`, f.tenant, clientID)
	if err != nil {
		t.Fatal(err)
	}
	type sessionRow struct{ publicID, workspace, summary string }
	var sessions []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.publicID, &r.workspace, &r.summary); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		sessions = append(sessions, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		t.Logf("session client_id=%s evidence: public_id=%s workspace=%s summary=%q", clientID, s.publicID, s.workspace, s.summary)
	}
	if len(sessions) != 2 {
		t.Errorf("SEC-03 session coexistence: client_id=%s produced %d session rows, want exactly 2 (one per workspace); rows=%v", clientID, len(sessions), sessions)
	} else if sessions[0].workspace == sessions[1].workspace {
		t.Errorf("SEC-03 session coexistence: both session rows live in workspace %s", sessions[0].workspace)
	}

	// Independent updates: workspace B re-pushes its session; workspace A's
	// summary must not change. Summaries are keyed by workspace public id:
	// array_agg with ORDER BY over the fixture's random UUIDs is positional
	// and would flip the mapping in ~half the runs.
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Sessions: []domain.SyncSession{{
		SyncID: clientID, Project: wsSecProject, StartedAt: t2, UpdatedAt: t3, Summary: "workspace B session updated",
	}}}); err != nil {
		t.Fatalf("workspace B session update push: %v", err)
	}
	srows, err := f.h.admin.Query(ctx, `
		SELECT w.public_id::text, COALESCE(s.summary,'')
		FROM sessions s JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id
		WHERE s.tenant_id=$1 AND s.client_id=$2`, f.tenant, clientID)
	if err != nil {
		t.Fatal(err)
	}
	summaryByWorkspace := make(map[string]string)
	for srows.Next() {
		var ws, summary string
		if err := srows.Scan(&ws, &summary); err != nil {
			srows.Close()
			t.Fatal(err)
		}
		summaryByWorkspace[ws] = summary
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		t.Fatal(err)
	}
	summaryA, okA := summaryByWorkspace[f.workspaceA.String()]
	summaryB, okB := summaryByWorkspace[f.workspaceB.String()]
	if !okA || !okB {
		t.Fatalf("SEC-03 session independence: expected one session row per sibling workspace, got mapping %v", summaryByWorkspace)
	}
	updatedB := "workspace B session updated"
	if summaryB != updatedB {
		t.Errorf("SEC-03 session independence: workspace B summary=%q, want %q", summaryB, updatedB)
	}
	if summaryA != "workspace A session" {
		t.Errorf("SEC-03 session independence: workspace A summary=%q mutated by sibling push, want the original", summaryA)
	}

	// Empirical classification probe (no assertion, behavior intentionally not
	// pinned): an observation push whose session client ID exists in BOTH
	// sibling workspaces currently resolves the session through a tenant-wide
	// scalar subquery. Pre-fix this is expected to fail closed with a
	// more-than-one-row subquery error; post-fix it must succeed inside the
	// pusher's workspace. Logged either way as evidence.
	probe, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: "wssec-sess-ambiguity-probe", SessionSyncID: clientID,
		Title: "ambiguity probe", Content: "ambiguity probe", Type: domain.TypeManual,
		Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t3, UpdatedAt: t3,
	}}})
	if err != nil {
		t.Logf("classification probe: observation push referencing session client_id=%s held by both workspaces failed (expected pre-fix, must succeed post-fix): %v", clientID, err)
	} else {
		t.Logf("classification probe: observation push referencing session client_id=%s held by both workspaces succeeded (accepted=%d)", clientID, probe.Accepted)
	}
}

// TestWorkspaceSecurityExploitSyncObservationCrossMutation pins SEC-03 for
// observation client IDs.
//
// PRE-FIX EXPECTED FAILURE: observation sync conflicts on tenant-wide
// (tenant_id, client_id), resolves the session tenant-wide, and applies the
// deleted flag tenant-wide, so a sibling workspace currently overwrites and
// soft-deletes workspace A's observation while attaching it to workspace A's
// session.
func TestWorkspaceSecurityExploitSyncObservationCrossMutation(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	const (
		sessionClient = "wssec-obs-session"
		obsClient     = "wssec-obs-dup"
	)
	t1 := wsSecSyncBase
	t2 := wsSecSyncBase.Add(10 * time.Second)
	t3 := wsSecSyncBase.Add(20 * time.Second)
	t4 := wsSecSyncBase.Add(30 * time.Second)

	// Workspace A seeds its session and observation through sync push.
	if _, err := f.storeA.PushSync(ctx, &domain.SyncBatch{
		Sessions: []domain.SyncSession{{SyncID: sessionClient, Project: wsSecProject, StartedAt: t1, UpdatedAt: t1, Summary: "workspace A sync session"}},
		Observations: []domain.SyncObservation{{
			SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace A observation",
			Content: "workspace A content " + wsSecCanaryA, Type: domain.TypeManual,
			Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
			CreatedAt: t1, UpdatedAt: t1,
		}},
	}); err != nil {
		t.Fatalf("workspace A observation push: %v", err)
	}

	// Workspace B pushes the SAME observation client ID, referencing the
	// known session client ID owned by workspace A.
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace B hijack",
		Content: "workspace B content " + wsSecCanaryB, Type: domain.TypeManual,
		Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t2, UpdatedAt: t2,
	}}}); err != nil {
		t.Fatalf("workspace B sibling observation push: %v", err)
	}

	rows := wsSecObservationEvidence(t, f.h, f.tenant, obsClient)
	for _, r := range rows {
		t.Logf("observation client_id=%s evidence: %s", obsClient, r)
	}
	if len(rows) != 2 {
		t.Errorf("SEC-03 VULNERABLE (pre-fix expected failure): sibling observation client_id=%s produced %d tenant-wide row(s), want 2 coexisting rows (one per workspace); evidence: %s", obsClient, len(rows), wsSecFormatObservations(rows))
	} else {
		for _, r := range rows {
			if r.Workspace != f.workspaceA.String() && r.Workspace != f.workspaceB.String() {
				t.Errorf("SEC-03: observation row %s resolved unknown workspace %s", r.PublicID, r.Workspace)
			}
		}
	}
	for _, r := range rows {
		if r.Workspace == f.workspaceA.String() && (strings.Contains(r.Content, wsSecCanaryB) || r.UpdatedBy == f.subjectB.String()) {
			t.Errorf("SEC-03 VULNERABLE (pre-fix expected failure): workspace A observation row %s was mutated by the workspace B push (content/actor evidence: %s)", r.PublicID, r)
		}
	}

	// Workspace A restores its row, then workspace B pushes the deleted flag
	// for the same client ID and must not be able to soft-delete workspace A's
	// observation.
	if _, err := f.storeA.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace A observation",
		Content: "workspace A content restored " + wsSecCanaryA, Type: domain.TypeManual,
		Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t3, UpdatedAt: t3,
	}}}); err != nil {
		t.Fatalf("workspace A restore push: %v", err)
	}
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace B tombstone",
		Content: "workspace B tombstone", Type: domain.TypeManual,
		Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t4, UpdatedAt: t4, Deleted: true,
	}}}); err != nil {
		t.Fatalf("workspace B tombstone push: %v", err)
	}
	after := wsSecObservationEvidence(t, f.h, f.tenant, obsClient)
	for _, r := range after {
		t.Logf("observation client_id=%s tombstone evidence: %s", obsClient, r)
		if r.Workspace == f.workspaceA.String() && r.Deleted {
			t.Errorf("SEC-03 VULNERABLE (pre-fix expected failure): workspace B tombstone push soft-deleted workspace A observation %s (evidence: %s)", r.PublicID, r)
		}
	}
}

func wsSecFormatObservations(rows []wsSecObservationRow) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, r.String())
	}
	return strings.Join(parts, "; ")
}

// TestWorkspaceSecurityExploitSyncPromptCrossMutation pins SEC-03 for prompt
// client IDs.
//
// PRE-FIX EXPECTED FAILURE: prompts conflict on tenant-wide
// (tenant_id, client_id) and resolve the session tenant-wide, so a sibling
// workspace currently overwrites workspace A's prompt content.
func TestWorkspaceSecurityExploitSyncPromptCrossMutation(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	const (
		sessionClient = "wssec-prompt-session"
		promptClient  = "wssec-prompt-dup"
	)
	t1 := wsSecSyncBase
	t2 := wsSecSyncBase.Add(10 * time.Second)

	if _, err := f.storeA.PushSync(ctx, &domain.SyncBatch{
		Sessions: []domain.SyncSession{{SyncID: sessionClient, Project: wsSecProject, StartedAt: t1, UpdatedAt: t1, Summary: "workspace A prompt session"}},
		Prompts: []domain.SyncPrompt{{
			SyncID: promptClient, SessionSyncID: sessionClient,
			Content: "workspace A prompt " + wsSecCanaryA, Project: wsSecProject,
			CreatedAt: t1, UpdatedAt: t1,
		}},
	}); err != nil {
		t.Fatalf("workspace A prompt push: %v", err)
	}
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Prompts: []domain.SyncPrompt{{
		SyncID: promptClient, SessionSyncID: sessionClient,
		Content: "workspace B prompt " + wsSecCanaryB, Project: wsSecProject,
		CreatedAt: t2, UpdatedAt: t2,
	}}}); err != nil {
		t.Fatalf("workspace B sibling prompt push: %v", err)
	}

	rows, err := f.h.admin.Query(ctx, `
		SELECT p.public_id::text, w.public_id::text, left(p.content,60), COALESCE(p.updated_by::text,'')
		FROM prompts p
		JOIN sessions s ON s.tenant_id=p.tenant_id AND s.id=p.session_id
		JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id
		WHERE p.tenant_id=$1 AND p.client_id=$2
		ORDER BY p.id`, f.tenant, promptClient)
	if err != nil {
		t.Fatal(err)
	}
	type promptRow struct{ publicID, workspace, content, updatedBy string }
	var prompts []promptRow
	for rows.Next() {
		var r promptRow
		if err := rows.Scan(&r.publicID, &r.workspace, &r.content, &r.updatedBy); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		prompts = append(prompts, r)
		t.Logf("prompt client_id=%s evidence: public_id=%s workspace=%s content=%q updated_by=%s", promptClient, r.publicID, r.workspace, r.content, r.updatedBy)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Errorf("SEC-03 VULNERABLE (pre-fix expected failure): sibling prompt client_id=%s produced %d tenant-wide row(s), want 2 coexisting rows (one per workspace)", promptClient, len(prompts))
	}
	for _, p := range prompts {
		if p.workspace == f.workspaceA.String() && (strings.Contains(p.content, wsSecCanaryB) || p.updatedBy == f.subjectB.String()) {
			t.Errorf("SEC-03 VULNERABLE (pre-fix expected failure): workspace A prompt %s was mutated by the workspace B push (content=%q updated_by=%s)", p.publicID, p.content, p.updatedBy)
		}
	}
}

// TestWorkspaceSecurityExploitSyncEdgeCrossReference pins SEC-03 for edge
// client IDs and edge endpoint references.
//
// PRE-FIX EXPECTED FAILURE: edge endpoints resolve observations by tenant-wide
// client ID, so a sibling workspace push referencing workspace A observation
// client IDs currently creates an edge whose endpoints live in workspace A.
func TestWorkspaceSecurityExploitSyncEdgeCrossReference(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	const (
		sessionClient = "wssec-edge-session"
		fromClient    = "wssec-edge-from"
		toClient      = "wssec-edge-to"
		edgeClient    = "wssec-edge-dup"
	)
	t1 := wsSecSyncBase
	t2 := wsSecSyncBase.Add(10 * time.Second)

	if _, err := f.storeA.PushSync(ctx, &domain.SyncBatch{
		Sessions: []domain.SyncSession{{SyncID: sessionClient, Project: wsSecProject, StartedAt: t1, UpdatedAt: t1, Summary: "workspace A edge session"}},
		Observations: []domain.SyncObservation{
			{SyncID: fromClient, SessionSyncID: sessionClient, Title: "edge source", Content: "edge source " + wsSecCanaryA, Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t1, UpdatedAt: t1},
			{SyncID: toClient, SessionSyncID: sessionClient, Title: "edge target", Content: "edge target " + wsSecCanaryA, Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t1, UpdatedAt: t1},
		},
	}); err != nil {
		t.Fatalf("workspace A edge fixture push: %v", err)
	}
	// Workspace B references workspace A's observation client IDs as edge
	// endpoints. There is no workspace B observation with these client IDs.
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Edges: []domain.SyncEdge{{
		SyncID: edgeClient, FromSyncID: fromClient, ToSyncID: toClient,
		Relation: domain.RelationReferences, CreatedAt: t2, UpdatedAt: t2,
	}}}); err != nil {
		t.Fatalf("workspace B sibling edge push: %v", err)
	}

	rows, err := f.h.admin.Query(ctx, `
		SELECT e.public_id::text, wa.public_id::text, wb.public_id::text
		FROM edges e
		JOIN observations fa ON fa.tenant_id=e.tenant_id AND fa.id=e.from_observation_id
		JOIN sessions fs ON fs.tenant_id=fa.tenant_id AND fs.id=fa.session_id
		JOIN workspaces wa ON wa.tenant_id=fs.tenant_id AND wa.id=fs.workspace_id
		JOIN observations tb ON tb.tenant_id=e.tenant_id AND tb.id=e.to_observation_id
		JOIN sessions ts ON ts.tenant_id=tb.tenant_id AND ts.id=tb.session_id
		JOIN workspaces wb ON wb.tenant_id=ts.tenant_id AND wb.id=ts.workspace_id
		WHERE e.tenant_id=$1 AND e.client_id=$2`, f.tenant, edgeClient)
	if err != nil {
		t.Fatal(err)
	}
	type edgeRow struct{ publicID, fromWorkspace, toWorkspace string }
	var edges []edgeRow
	for rows.Next() {
		var r edgeRow
		if err := rows.Scan(&r.publicID, &r.fromWorkspace, &r.toWorkspace); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		edges = append(edges, r)
		t.Logf("edge client_id=%s evidence: public_id=%s from_workspace=%s to_workspace=%s", edgeClient, r.publicID, r.fromWorkspace, r.toWorkspace)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.fromWorkspace == f.workspaceA.String() || e.toWorkspace == f.workspaceA.String() {
			t.Errorf("SEC-03 VULNERABLE (pre-fix expected failure): workspace B edge %s references workspace A observations (from_workspace=%s to_workspace=%s); edge endpoints must resolve only inside the pusher's workspace", e.publicID, e.fromWorkspace, e.toWorkspace)
		}
	}
	if len(edges) == 0 {
		t.Fatal("SEC-03 edge evidence missing: sibling edge push created no durable row; classification is inconclusive")
	}
}

// TestWorkspaceSecurityExploitAuthzBoundaryRemainsClosed consumes the third
// DSN (CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN) and proves the exploit surface is
// not a broken privilege boundary: the NOSUPERUSER authorization-admin login
// cannot execute the app-only principal binder and observes zero tenant rows
// under RLS. This oracle PASSES pre-fix and MUST STAY GREEN after remediation.
func TestWorkspaceSecurityExploitAuthzBoundaryRemainsClosed(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	wsSecSeedObservation(t, f.storeA, "wssec-boundary-row", "boundary "+wsSecCanaryA)

	adminDSN := os.Getenv("CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN")
	if adminDSN == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN is required for the authorization boundary oracle")
	}
	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse authz admin DSN: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var superuser bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&superuser); err != nil {
		t.Fatal(err)
	}
	if superuser {
		t.Fatalf("authorization admin login %q must be NOSUPERUSER", cfg.ConnConfig.User)
	}
	if _, err := pool.Exec(ctx, `SELECT public.cortex_bind_principal($1,$2,$3)`, f.subjectA, "any-digest", 1); err == nil {
		t.Fatal("authorization admin login executed the app-only principal binder")
	}
	var visible int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM observations`).Scan(&visible); err != nil {
		t.Fatalf("authorization admin RLS probe: %v", err)
	}
	if visible != 0 {
		t.Fatalf("authorization admin login observed %d tenant rows without a principal binding", visible)
	}
}
