//go:build postgres_integration

// Workspace-aware sync oracles (SDD tools-security-performance-hardening,
// work unit pg-sync, ref SEC-03).
//
// These oracles pin the workspace-scoped PushSync/PullSync contract that
// migration 107 (workspace-safe sync schema) plus the sync.go remediation
// must establish:
//
//   - Sibling workspaces of one tenant pushing IDENTICAL client IDs must
//     coexist (one durable row chain per workspace), replay updates, and
//     tombstone independently. Cross-workspace client IDs behave as
//     absent inside the caller workspace: they can never attach to,
//     overwrite, soft-delete, or reveal sibling rows.
//   - Every push/pull identity, conflict, DML, tombstone, endpoint, and
//     hydration lookup carries tenant plus the transaction-resolved bound
//     workspace; an unresolvable bound workspace fails closed before any
//     effect.
//   - A mixed batch that fails validation rolls back atomically with zero
//     partial effects.
//   - The public success shapes stay compatible: Accepted counts one per
//     batch entry and SyncPage keeps its cursor/has_more/payload shape.
//
// The fixture helpers (newWsSecFixture, canaries, wsSecSyncBase) are shared
// with the retained T01 exploit oracles in workspace_security_integration_test.go.
package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// wsSyncRow is sanitized, bounded row-level evidence read through the
// privileged migration handle (never through the application role).
type wsSyncRow struct {
	Workspace string
	Detail    string
	Deleted   bool
}

// wsSyncEvidence resolves durable rows for one client id per table. The
// table name is a fixed literal switch (never interpolated), and evidence
// joins resolve the owning workspace through the durable chains
// (session -> workspace, from-observation -> session -> workspace).
func wsSyncEvidence(t *testing.T, h *postgresHarness, table, clientID string, tenant uuid.UUID) []wsSyncRow {
	t.Helper()
	var query string
	switch table {
	case "sessions":
		query = `SELECT w.public_id::text, COALESCE(s.summary,''), false
			FROM sessions s JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id
			WHERE s.tenant_id=$1 AND s.client_id=$2 ORDER BY s.id`
	case "observations":
		query = `SELECT w.public_id::text, left(o.title,60), o.deleted_at IS NOT NULL
			FROM observations o JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id
			JOIN workspaces w ON w.tenant_id=se.tenant_id AND w.id=se.workspace_id
			WHERE o.tenant_id=$1 AND o.client_id=$2 ORDER BY o.id`
	case "prompts":
		query = `SELECT w.public_id::text, left(p.content,60), false
			FROM prompts p JOIN sessions se ON se.tenant_id=p.tenant_id AND se.id=p.session_id
			JOIN workspaces w ON w.tenant_id=se.tenant_id AND w.id=se.workspace_id
			WHERE p.tenant_id=$1 AND p.client_id=$2 ORDER BY p.id`
	case "edges":
		query = `SELECT w.public_id::text, COALESCE(e.source,''), e.deleted_at IS NOT NULL
			FROM edges e JOIN observations o ON o.tenant_id=e.tenant_id AND o.id=e.from_observation_id
			JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id
			JOIN workspaces w ON w.tenant_id=se.tenant_id AND w.id=se.workspace_id
			WHERE e.tenant_id=$1 AND e.client_id=$2 ORDER BY e.id`
	default:
		t.Fatalf("unknown evidence table %q", table)
	}
	rows, err := h.admin.Query(context.Background(), query, tenant, clientID)
	if err != nil {
		t.Fatalf("%s evidence query: %v", table, err)
	}
	defer rows.Close()
	var out []wsSyncRow
	for rows.Next() {
		var r wsSyncRow
		if err := rows.Scan(&r.Workspace, &r.Detail, &r.Deleted); err != nil {
			t.Fatalf("%s evidence scan: %v", table, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s evidence iterate: %v", table, err)
	}
	return out
}

func (r wsSyncRow) String() string {
	return "{workspace=" + r.Workspace + " detail=" + r.Detail + " deleted=" + boolText(r.Deleted) + "}"
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// wsSyncRowsForWorkspace filters evidence rows to one workspace public id.
func wsSyncRowsForWorkspace(rows []wsSyncRow, workspace uuid.UUID) []wsSyncRow {
	var out []wsSyncRow
	for _, r := range rows {
		if r.Workspace == workspace.String() {
			out = append(out, r)
		}
	}
	return out
}

// wsSyncAssertCoexisting asserts the sibling coexistence invariant for one
// client id: exactly one row per sibling workspace.
func wsSyncAssertCoexisting(t *testing.T, table, clientID string, rows []wsSyncRow, workspaceA, workspaceB uuid.UUID) {
	t.Helper()
	if len(rows) != 2 {
		t.Errorf("SEC-03 sibling coexistence: %s client_id=%s produced %d tenant row(s), want exactly 2 (one per workspace); rows=%v", table, clientID, len(rows), rows)
		return
	}
	inA := wsSyncRowsForWorkspace(rows, workspaceA)
	inB := wsSyncRowsForWorkspace(rows, workspaceB)
	if len(inA) != 1 || len(inB) != 1 {
		t.Errorf("SEC-03 sibling coexistence: %s client_id=%s split workspaces A=%d B=%d, want 1/1; rows=%v", table, clientID, len(inA), len(inB), rows)
	}
}

// TestWorkspaceSyncSiblingCoexistenceAndIndependentReplay pins SEC-03 for
// the full push/pull surface: identical client IDs pushed to two sibling
// workspaces coexist, replay updates independently, tombstone
// independently, and the pull feed hydrates only the caller's workspace.
func TestWorkspaceSyncSiblingCoexistenceAndIndependentReplay(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	const (
		sessionClient = "t04-sibling-session"
		obsClient     = "t04-sibling-obs"
		promptClient  = "t04-sibling-prompt"
		edgeClient    = "t04-sibling-edge"
		fromClient    = "t04-sibling-from"
		toClient      = "t04-sibling-to"
	)
	t1 := wsSecSyncBase
	t2 := wsSecSyncBase.Add(10 * time.Second)
	t3 := wsSecSyncBase.Add(20 * time.Second)
	t4 := wsSecSyncBase.Add(30 * time.Second)

	// batch builds a full, identically-keyed batch whose payloads carry the
	// pushing workspace's canary (including the edge source field, because
	// edges carry no title/content).
	batch := func(canary, summary string, at time.Time) *domain.SyncBatch {
		return &domain.SyncBatch{
			Sessions: []domain.SyncSession{{
				SyncID: sessionClient, Project: wsSecProject, StartedAt: t1, UpdatedAt: at, Summary: summary,
			}},
			Observations: []domain.SyncObservation{
				{SyncID: fromClient, SessionSyncID: sessionClient, Title: "from " + canary, Content: "from " + canary, Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t1, UpdatedAt: at},
				{SyncID: toClient, SessionSyncID: sessionClient, Title: "to " + canary, Content: "to " + canary, Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t1, UpdatedAt: at},
				{SyncID: obsClient, SessionSyncID: sessionClient, Title: "obs " + canary, Content: "obs " + canary, Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t1, UpdatedAt: at},
			},
			Prompts: []domain.SyncPrompt{{
				SyncID: promptClient, SessionSyncID: sessionClient, Content: "prompt " + canary, Project: wsSecProject, CreatedAt: t1, UpdatedAt: at,
			}},
			Edges: []domain.SyncEdge{{
				SyncID: edgeClient, FromSyncID: fromClient, ToSyncID: toClient, Relation: domain.RelationReferences,
				Weight: 0.5, Confidence: 0.9, Source: "edge-src-" + canary, CreatedAt: t1, UpdatedAt: at,
			}},
		}
	}

	resA, err := f.storeA.PushSync(ctx, batch(wsSecCanaryA, "sibling session A", t1))
	if err != nil {
		t.Fatalf("workspace A push: %v", err)
	}
	if resA.Accepted != 6 {
		t.Fatalf("workspace A accepted=%d, want 6 (accepted counts one per batch entry)", resA.Accepted)
	}
	resB, err := f.storeB.PushSync(ctx, batch(wsSecCanaryB, "sibling session B", t2))
	if err != nil {
		t.Fatalf("workspace B sibling push with identical client ids: %v", err)
	}
	if resB.Accepted != 6 {
		t.Fatalf("workspace B accepted=%d, want 6 (accepted counts one per batch entry)", resB.Accepted)
	}

	// Every identically-keyed entity coexists as one row per workspace.
	for _, tc := range []struct{ table, client string }{
		{"sessions", sessionClient},
		{"observations", fromClient},
		{"observations", toClient},
		{"observations", obsClient},
		{"prompts", promptClient},
		{"edges", edgeClient},
	} {
		rows := wsSyncEvidence(t, f.h, tc.table, tc.client, f.tenant)
		for _, r := range rows {
			t.Logf("%s client_id=%s evidence: %s", tc.table, tc.client, r)
		}
		wsSyncAssertCoexisting(t, tc.table, tc.client, rows, f.workspaceA, f.workspaceB)
	}

	// Independent replay update: workspace B re-pushes one observation with
	// newer content; workspace A's row must not change.
	resUpd, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: obsClient, SessionSyncID: sessionClient, Title: "obs-updated " + wsSecCanaryB,
		Content: "obs-updated " + wsSecCanaryB, Type: domain.TypeManual,
		Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t2, UpdatedAt: t3,
	}}})
	if err != nil {
		t.Fatalf("workspace B replay update push: %v", err)
	}
	if resUpd.Accepted != 1 {
		t.Fatalf("workspace B replay update accepted=%d, want 1", resUpd.Accepted)
	}
	for _, r := range wsSyncEvidence(t, f.h, "observations", obsClient, f.tenant) {
		if r.Workspace == f.workspaceA.String() && !strings.Contains(r.Detail, wsSecCanaryA) {
			t.Errorf("SEC-03 independent replay: workspace A observation mutated by sibling update (evidence: %s)", r)
		}
		if r.Workspace == f.workspaceB.String() && !strings.Contains(r.Detail, "obs-updated") {
			t.Errorf("SEC-03 independent replay: workspace B observation not updated (evidence: %s)", r)
		}
	}

	// Independent tombstones: workspace B soft-deletes its observation and
	// edge; workspace A's rows must survive.
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{
		Observations: []domain.SyncObservation{{
			SyncID: obsClient, SessionSyncID: sessionClient, Title: "obs tombstone", Content: "obs tombstone",
			Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
			CreatedAt: t3, UpdatedAt: t4, Deleted: true,
		}},
		Edges: []domain.SyncEdge{{
			SyncID: edgeClient, FromSyncID: fromClient, ToSyncID: toClient, Relation: domain.RelationReferences,
			Weight: 0.5, Confidence: 0.9, Source: "edge-src-" + wsSecCanaryB, CreatedAt: t3, UpdatedAt: t4, Deleted: true,
		}},
	}); err != nil {
		t.Fatalf("workspace B tombstone push: %v", err)
	}
	for _, r := range wsSyncEvidence(t, f.h, "observations", obsClient, f.tenant) {
		if r.Workspace == f.workspaceA.String() && r.Deleted {
			t.Errorf("SEC-03 independent tombstone: sibling tombstone soft-deleted workspace A observation (evidence: %s)", r)
		}
		if r.Workspace == f.workspaceB.String() && !r.Deleted {
			t.Errorf("SEC-03 independent tombstone: workspace B own tombstone not applied (evidence: %s)", r)
		}
	}
	for _, r := range wsSyncEvidence(t, f.h, "edges", edgeClient, f.tenant) {
		if r.Workspace == f.workspaceA.String() && r.Deleted {
			t.Errorf("SEC-03 independent tombstone: sibling tombstone soft-deleted workspace A edge (evidence: %s)", r)
		}
		if r.Workspace == f.workspaceB.String() && !r.Deleted {
			t.Errorf("SEC-03 independent tombstone: workspace B own edge tombstone not applied (evidence: %s)", r)
		}
	}

	// Pull isolation with unchanged page shape: each workspace's feed
	// hydrates only its own rows (canaries prove row-level ownership), the
	// cursor advances, and has_more pages the feed.
	assertPullIsolated := func(store *AuthorizedStore, own, other string) *domain.SyncPage {
		page, err := store.PullSync(ctx, 0, 100)
		if err != nil {
			t.Fatalf("pull cursor 0: %v", err)
		}
		if page.HasMore {
			t.Fatalf("pull cursor 0 with limit 100 must not report has_more")
		}
		leak := func(kind, detail string) {
			t.Errorf("SEC-03 pull leak: %s entry %q carries sibling canary %q", kind, detail, other)
		}
		present := 0
		for _, s := range page.Sessions {
			if strings.Contains(s.Summary, other) {
				leak("session", s.Summary)
			}
			if strings.Contains(s.Summary, own) {
				present++
			}
		}
		for _, o := range page.Observations {
			if strings.Contains(o.Title, other) || strings.Contains(o.Content, other) {
				leak("observation", o.Title)
			}
			if strings.Contains(o.Content, own) {
				present++
			}
		}
		for _, p := range page.Prompts {
			if strings.Contains(p.Content, other) {
				leak("prompt", p.Content)
			}
			if strings.Contains(p.Content, own) {
				present++
			}
		}
		for _, e := range page.Edges {
			if strings.Contains(e.Source, other) {
				leak("edge", e.Source)
			}
			if strings.Contains(e.Source, own) {
				present++
			}
		}
		if present == 0 {
			t.Fatalf("pull is vacuously isolated: no own-canary entry hydrated for %q (fixture broken)", own)
		}
		if page.Cursor <= 0 {
			t.Fatalf("pull cursor must advance past 0 after hydration, got %d", page.Cursor)
		}
		return page
	}
	pageB := assertPullIsolated(f.storeB, wsSecCanaryB, wsSecCanaryA)
	assertPullIsolated(f.storeA, wsSecCanaryA, wsSecCanaryB)

	// Cursor/has_more shape: a bounded pull pages the feed and resumes from
	// the returned cursor without re-emitting the first page.
	first, err := f.storeB.PullSync(ctx, 0, 2)
	if err != nil {
		t.Fatalf("bounded pull: %v", err)
	}
	if !first.HasMore || first.Cursor <= 0 {
		t.Fatalf("bounded pull shape: has_more=%v cursor=%d, want has_more=true and cursor>0", first.HasMore, first.Cursor)
	}
	rest, err := f.storeB.PullSync(ctx, first.Cursor, 100)
	if err != nil {
		t.Fatalf("resume pull: %v", err)
	}
	if rest.HasMore {
		t.Fatalf("resume pull shape: has_more must be false when the remaining feed fits one page")
	}
	if rest.Cursor < first.Cursor {
		t.Fatalf("resume pull shape: cursor went backwards (%d < %d)", rest.Cursor, first.Cursor)
	}
	if pageB.Cursor != rest.Cursor {
		t.Fatalf("pull determinism: full-page cursor %d != paged resume terminal cursor %d", pageB.Cursor, rest.Cursor)
	}
}

// TestWorkspaceSyncCrossWorkspaceReferencesAbsentAndAtomic pins SEC-03
// reference semantics: a client id that exists only in a sibling workspace
// behaves as absent inside the caller workspace. The push still lands
// atomically inside the caller (its own placeholder chain), sibling rows
// are never attached/mutated/deleted, and a mixed batch that fails
// validation leaves zero partial effects.
func TestWorkspaceSyncCrossWorkspaceReferencesAbsentAndAtomic(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	const (
		sessionClient = "t04-xws-session"
		obsClient     = "t04-xws-obs"
		mixedClient   = "t04-xws-mixed-session"
	)
	t1 := wsSecSyncBase
	t2 := wsSecSyncBase.Add(10 * time.Second)
	t3 := wsSecSyncBase.Add(20 * time.Second)
	t4 := wsSecSyncBase.Add(30 * time.Second)

	if _, err := f.storeA.PushSync(ctx, &domain.SyncBatch{
		Sessions:     []domain.SyncSession{{SyncID: sessionClient, Project: wsSecProject, StartedAt: t1, UpdatedAt: t1, Summary: "workspace A xws session"}},
		Observations: []domain.SyncObservation{{SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace A xws obs", Content: "workspace A xws obs " + wsSecCanaryA, Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t1, UpdatedAt: t1}},
	}); err != nil {
		t.Fatalf("workspace A seed push: %v", err)
	}

	// Workspace B pushes an observation under the SAME client id whose
	// session reference resolves only in workspace A: the reference is
	// absent inside B, so the batch must still land inside B and never
	// touch A's chain.
	res, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace B xws obs",
		Content: "workspace B xws obs " + wsSecCanaryB, Type: domain.TypeManual,
		Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t2, UpdatedAt: t2,
	}}})
	if err != nil {
		t.Fatalf("workspace B sibling observation push: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("workspace B sibling observation accepted=%d, want 1", res.Accepted)
	}
	rows := wsSyncEvidence(t, f.h, "observations", obsClient, f.tenant)
	wsSyncAssertCoexisting(t, "observations", obsClient, rows, f.workspaceA, f.workspaceB)
	for _, r := range rows {
		if r.Workspace == f.workspaceA.String() && (strings.Contains(r.Detail, "workspace B") || r.Deleted) {
			t.Errorf("SEC-03 absent reference: workspace A observation mutated by sibling push (evidence: %s)", r)
		}
	}

	// Sibling tombstone stays inside the pusher's workspace.
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{Observations: []domain.SyncObservation{{
		SyncID: obsClient, SessionSyncID: sessionClient, Title: "workspace B tombstone", Content: "workspace B tombstone",
		Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual,
		CreatedAt: t3, UpdatedAt: t3, Deleted: true,
	}}}); err != nil {
		t.Fatalf("workspace B tombstone push: %v", err)
	}
	for _, r := range wsSyncEvidence(t, f.h, "observations", obsClient, f.tenant) {
		if r.Workspace == f.workspaceA.String() && r.Deleted {
			t.Errorf("SEC-03 absent reference: sibling tombstone deleted workspace A observation (evidence: %s)", r)
		}
		if r.Workspace == f.workspaceB.String() && !r.Deleted {
			t.Errorf("SEC-03 absent reference: workspace B own tombstone not applied (evidence: %s)", r)
		}
	}

	// Mixed-batch atomicity: a valid session entry followed by an invalid
	// observation entry (empty title) fails the whole batch, and the valid
	// entry leaves zero partial effects.
	if _, err := f.storeB.PushSync(ctx, &domain.SyncBatch{
		Sessions:     []domain.SyncSession{{SyncID: mixedClient, Project: wsSecProject, StartedAt: t4, UpdatedAt: t4, Summary: "must roll back"}},
		Observations: []domain.SyncObservation{{SyncID: "t04-xws-invalid", SessionSyncID: mixedClient, Title: "", Content: "invalid entry", Type: domain.TypeManual, Project: wsSecProject, Scope: domain.ScopeProject, Source: domain.SourceManual, CreatedAt: t4, UpdatedAt: t4}},
	}); err == nil {
		t.Fatal("mixed batch with an invalid observation must fail")
	}
	if rows := wsSyncEvidence(t, f.h, "sessions", mixedClient, f.tenant); len(rows) != 0 {
		t.Errorf("SEC-03 atomic mixed batch: failed push left %d partial session row(s) (evidence: %v)", len(rows), rows)
	}
}

// TestWorkspaceSyncUnresolvedWorkspaceFailsClosed pins two invariants: a
// workspace binding whose UUID does not resolve inside the tenant fails
// closed before any sync effect, AND the failed transaction rolls back
// with the original non-nil context instead of panicking inside pgconn
// (the bind helpers return a nil context together with their error, so the
// deferred Rollback must not reuse the reassigned ctx).
func TestWorkspaceSyncUnresolvedWorkspaceFailsClosed(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	ghost := newWsSecBoundStore(t, f.h, f.tenant, uuid.New(), uuid.New())

	const ghostClient = "t04-ghost-session"
	if _, err := ghost.PushSync(ctx, &domain.SyncBatch{Sessions: []domain.SyncSession{{
		SyncID: ghostClient, Project: wsSecProject, StartedAt: wsSecSyncBase, UpdatedAt: wsSecSyncBase,
	}}}); err == nil {
		t.Fatal("PushSync with an unresolvable bound workspace must fail closed")
	}
	if _, err := ghost.PullSync(ctx, 0, 10); err == nil {
		t.Fatal("PullSync with an unresolvable bound workspace must fail closed")
	}
	if rows := wsSyncEvidence(t, f.h, "sessions", ghostClient, f.tenant); len(rows) != 0 {
		t.Errorf("fail-closed push left %d durable row(s) (evidence: %v)", len(rows), rows)
	}
}

// TestWorkspaceSyncMissingWorkspaceFailsClosed pins the fail-closed
// precondition: a store whose tenant context carries no workspace binding
// rejects push and pull before any effect. (A workspace binding whose UUID
// does not resolve inside the tenant is additionally rejected by the
// shared transaction workspace resolution before any sync statement.)
func TestWorkspaceSyncMissingWorkspaceFailsClosed(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	subject := uuid.New()
	_, provenance := mintBindingProvenance(t, f.h, f.tenant, subject, 1, "wssec-digest")
	p := domain.Principal{
		Subject:      subject.String(),
		Type:         "user",
		OrgID:        f.tenant.String(),
		Roles:        []string{"member"},
		ProjectIDs:   []string{wsSecProject},
		GrantDigest:  provenance,
		GrantVersion: 1,
	}
	ac := authz.AuthorizedContext{Principal: p, Tenant: domain.TenantContext{TenantID: f.tenant.String()}, GrantDigest: p.GrantDigest}
	bare, err := NewAuthorizedStore(f.h.pool, ac)
	if err != nil {
		t.Fatal(err)
	}

	const bareClient = "t04-bare-session"
	if _, err := bare.PushSync(ctx, &domain.SyncBatch{Sessions: []domain.SyncSession{{
		SyncID: bareClient, Project: wsSecProject, StartedAt: wsSecSyncBase, UpdatedAt: wsSecSyncBase,
	}}}); err == nil {
		t.Fatal("PushSync without a bound workspace must fail closed")
	}
	if _, err := bare.PullSync(ctx, 0, 10); err == nil {
		t.Fatal("PullSync without a bound workspace must fail closed")
	}
	if rows := wsSyncEvidence(t, f.h, "sessions", bareClient, f.tenant); len(rows) != 0 {
		t.Errorf("fail-closed push left %d durable row(s) (evidence: %v)", len(rows), rows)
	}
}
