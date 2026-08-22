//go:build postgres_integration

// Workspace isolation oracles for PostgreSQL observation list/search and
// supporting subqueries (SDD tools-security-performance-hardening, work unit
// pg-list-search, ref SEC-01).
//
// These oracles pin the T03 acceptance contract:
//
//   - sibling-workspace rows never appear under overlapping
//     tenant/project/classification grants;
//   - own-row controls prove list and search are non-vacuous;
//   - a missing/unbound workspace fails closed instead of degrading to
//     tenant-wide visibility;
//   - every list/search/topic/count/supporting read stays workspace scoped
//     while success output ordering and shape remain compatible.
//
// The sibling fixture (newWsSecFixture) and canaries are shared with the T01
// exploit oracles in workspace_security_integration_test.go: both workspaces
// deliberately hold overlapping grants for wsSecProject, so any visible
// sibling row is unambiguous cross-workspace disclosure. Rows are seeded
// through the authorized, workspace-scoped save path, so isolation proofs
// here are read-scoping facts, not fixture artifacts.
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

// wsIsolationSeedAt seeds one observation inside the store's bound workspace
// with an explicit creation timestamp so list ordering assertions are
// deterministic.
func wsIsolationSeedAt(t *testing.T, store *AuthorizedStore, title, content string, createdAt time.Time) *domain.Observation {
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
		CreatedAt: createdAt,
	}
	if err := store.SaveObservation(ctx, obs); err != nil {
		t.Fatalf("seed observation %q: %v", title, err)
	}
	return obs
}

// wsIsolationTitles collects the listed titles for lightweight sibling-leak
// assertions.
func wsIsolationTitles(rows []*domain.Observation) []string {
	titles := make([]string, 0, len(rows))
	for _, o := range rows {
		titles = append(titles, o.Title)
	}
	return titles
}

func wsIsolationHas(titles []string, title string) bool {
	for _, x := range titles {
		if x == title {
			return true
		}
	}
	return false
}

// newWsIsolationUnboundStore builds an AuthorizedStore that carries the
// member role and the shared project grant but no workspace binding: the
// tenant context resolves no workspace, so workspace-scoped reads must fail
// closed at the store boundary rather than degrade to tenant-wide.
func newWsIsolationUnboundStore(t *testing.T, h *postgresHarness, tenant, subject uuid.UUID) *AuthorizedStore {
	t.Helper()
	_, provenance := mintBindingProvenance(t, h, tenant, subject, 1, "wsiso-digest")
	p := domain.Principal{
		Subject:      subject.String(),
		Type:         "user",
		OrgID:        tenant.String(),
		Roles:        []string{"member"},
		ProjectIDs:   []string{wsSecProject},
		GrantDigest:  provenance,
		GrantVersion: 1,
	}
	ac := authz.AuthorizedContext{
		Principal:   p,
		Tenant:      domain.TenantContext{TenantID: tenant.String()},
		GrantDigest: p.GrantDigest,
	}
	store, err := NewAuthorizedStore(h.pool, ac)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestWorkspaceIsolationListOwnRowsOnly proves SEC-01 for the repository
// List path with a non-vacuous own-row control and unchanged success shape:
// under identical tenant/project/classification grants, each workspace sees
// exactly its own rows.
func TestWorkspaceIsolationListOwnRowsOnly(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	ownA1 := wsIsolationSeedAt(t, f.storeA, "iso-list-a1", "list alpha "+wsSecCanaryA, base)
	ownA2 := wsIsolationSeedAt(t, f.storeA, "iso-list-a2", "list alpha two "+wsSecCanaryA, base.Add(time.Minute))
	sibling := wsIsolationSeedAt(t, f.storeB, "iso-list-b1", "list beta "+wsSecCanaryB, base.Add(2*time.Minute))

	listed, err := f.storeA.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 100})
	if err != nil {
		t.Fatalf("workspace A list: %v", err)
	}
	titles := wsIsolationTitles(listed)
	// Non-vacuous control: workspace A must still see both of its own rows.
	if !wsIsolationHas(titles, ownA1.Title) || !wsIsolationHas(titles, ownA2.Title) {
		t.Fatalf("workspace A list is vacuous for its own rows: got %v", titles)
	}
	// Sibling rows must never appear even though workspace B shares the
	// tenant, the project grant, and the default classification.
	if wsIsolationHas(titles, sibling.Title) {
		t.Fatalf("workspace A list disclosed sibling row %q (sibling public_id=%s): %v", sibling.Title, sibling.PublicID, titles)
	}
	// Success shape stays compatible: the same field contract as before.
	for _, o := range listed {
		if o.PublicID == "" || o.ID == 0 || o.SessionID == "" {
			t.Fatalf("workspace A list row identifiers drifted: %+v", o)
		}
		if o.Project != wsSecProject || o.Scope != domain.ScopeProject || o.Source != domain.SourceManual || o.Type != domain.TypeManual {
			t.Fatalf("workspace A list row shape drifted: %+v", o)
		}
	}
}

// TestWorkspaceIsolationListOrderShapeUnchanged proves the list success
// contract (created_at DESC default ordering, ASC via OrderAsc, and
// limit/offset paging) survives the workspace predicate.
func TestWorkspaceIsolationListOrderShapeUnchanged(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC)
	first := wsIsolationSeedAt(t, f.storeA, "iso-order-1", "order one "+wsSecCanaryA, base)
	second := wsIsolationSeedAt(t, f.storeA, "iso-order-2", "order two "+wsSecCanaryA, base.Add(time.Minute))
	third := wsIsolationSeedAt(t, f.storeA, "iso-order-3", "order three "+wsSecCanaryA, base.Add(2*time.Minute))
	wsIsolationSeedAt(t, f.storeB, "iso-order-sibling", "order sibling "+wsSecCanaryB, base.Add(3*time.Minute))

	desc, err := f.storeA.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 10})
	if err != nil {
		t.Fatalf("list desc: %v", err)
	}
	wantDesc := []string{third.Title, second.Title, first.Title}
	gotDesc := wsIsolationTitles(desc)
	if len(gotDesc) != len(wantDesc) {
		t.Fatalf("list desc returned %d rows %v, want %d", len(gotDesc), gotDesc, len(wantDesc))
	}
	for i := range wantDesc {
		if gotDesc[i] != wantDesc[i] {
			t.Fatalf("list desc order drifted at %d: got %v, want %v", i, gotDesc, wantDesc)
		}
	}

	asc, err := f.storeA.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 10, OrderAsc: true})
	if err != nil {
		t.Fatalf("list asc: %v", err)
	}
	gotAsc := wsIsolationTitles(asc)
	if len(gotAsc) != 3 || gotAsc[0] != first.Title || gotAsc[2] != third.Title {
		t.Fatalf("list asc order drifted: %v", gotAsc)
	}

	page1, err := f.storeA.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	page2, err := f.storeA.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if got := wsIsolationTitles(page1); len(got) != 2 || got[0] != third.Title || got[1] != second.Title {
		t.Fatalf("limit paging page 1 drifted: %v", got)
	}
	if got := wsIsolationTitles(page2); len(got) != 1 || got[0] != first.Title {
		t.Fatalf("limit/offset paging page 2 drifted: %v", got)
	}
}

// TestWorkspaceIsolationSearchOwnRowsOnly proves SEC-01 for the search path
// with a non-vacuous own-canary control: the sibling workspace's unique
// canary yields zero hits while the caller's own canary still ranks.
func TestWorkspaceIsolationSearchOwnRowsOnly(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	wsIsolationSeedAt(t, f.storeA, "iso-search-a", "alpha searchable "+wsSecCanaryA, time.Now().UTC())
	wsIsolationSeedAt(t, f.storeB, "iso-search-b", "beta searchable "+wsSecCanaryB, time.Now().UTC())

	own, err := f.storeA.SearchObservations(ctx, wsSecCanaryA, domain.SearchOptions{Project: wsSecProject, Limit: 50})
	if err != nil {
		t.Fatalf("workspace A own-canary search: %v", err)
	}
	if len(own) == 0 {
		t.Fatal("workspace A own-canary control search returned no rows; search oracle is vacuous")
	}
	for _, r := range own {
		if r.Rank <= 0 || r.PublicID == "" || r.Title != "iso-search-a" {
			t.Fatalf("search result shape drifted: %+v", r)
		}
	}

	leaked, err := f.storeA.SearchObservations(ctx, wsSecCanaryB, domain.SearchOptions{Project: wsSecProject, Limit: 50})
	if err != nil {
		t.Fatalf("workspace A sibling-canary search: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("workspace A search for the workspace-B-only canary %q returned %d sibling row(s)", wsSecCanaryB, len(leaked))
	}
}

// TestWorkspaceIsolationListSearchFailClosedWithoutWorkspace proves the
// missing-workspace fail-closed contract: an authorized store whose verified
// tenant context carries no workspace binding must never degrade list,
// search, topic, count, or supporting reads into tenant-wide visibility.
func TestWorkspaceIsolationListSearchFailClosedWithoutWorkspace(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	wsIsolationSeedAt(t, f.storeA, "iso-failclosed-seed", "fail closed seed "+wsSecCanaryA, time.Now().UTC())

	// The unbound principal still holds the member role and the shared
	// project grant, so the authorization layer permits the read and the
	// fail-closed contract is exercised at the store boundary.
	unbound := newWsIsolationUnboundStore(t, f.h, f.tenant, f.subjectA)

	if _, err := unbound.ListObservations(ctx, domain.ObservationFilter{Project: wsSecProject}); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound list: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.SearchObservations(ctx, wsSecCanaryA, domain.SearchOptions{Project: wsSecProject}); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound search: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.observations().GetByTopicKey(ctx, wsSecProject, "iso-topic"); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound topic lookup: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.observations().CountAll(ctx); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound count: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.observations().CountByRoot(ctx, 1); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound root count: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.observations().CountEdgesAsObs(ctx, 1); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound edge-observation count: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.prompts().List(ctx, wsSecProject, 10); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound prompt list: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.graph().GetRelated(ctx, 1, 1); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound related list: err=%v, want errWorkspaceScopeRequired", err)
	}
	if _, err := unbound.store.graph().CountAllEdges(ctx); !errors.Is(err, errWorkspaceScopeRequired) {
		t.Fatalf("unbound edge count: err=%v, want errWorkspaceScopeRequired", err)
	}
}

// TestWorkspaceIsolationListCountsStayScoped proves every supporting count,
// graph, and prompt read stays bound to the caller's workspace even when the
// sibling workspace holds identical project/classification grants.
func TestWorkspaceIsolationListCountsStayScoped(t *testing.T) {
	f := newWsSecFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)

	// Workspace A: two observations joined by one references edge and one
	// contradiction edge, plus one prompt.
	a1 := wsIsolationSeedAt(t, f.storeA, "iso-count-a1", "count alpha "+wsSecCanaryA, base)
	a2 := wsIsolationSeedAt(t, f.storeA, "iso-count-a2", "count alpha two "+wsSecCanaryA, base.Add(time.Minute))
	if err := f.storeA.store.graph().CreateEdge(ctx, &domain.Edge{FromObsID: a1.ID, ToObsID: a2.ID, RelationType: domain.RelationReferences}); err != nil {
		t.Fatalf("workspace A references edge: %v", err)
	}
	if err := f.storeA.store.graph().CreateEdge(ctx, &domain.Edge{FromObsID: a1.ID, ToObsID: a2.ID, RelationType: domain.RelationContradicts}); err != nil {
		t.Fatalf("workspace A contradiction edge: %v", err)
	}
	sessionA := &domain.Session{Project: wsSecProject, StartedAt: base}
	if err := f.storeA.CreateSession(ctx, sessionA); err != nil {
		t.Fatalf("workspace A prompt session: %v", err)
	}
	if err := f.storeA.store.prompts().Save(ctx, &domain.Prompt{SessionID: sessionA.ID, Project: wsSecProject, Content: "workspace A prompt"}); err != nil {
		t.Fatalf("workspace A prompt: %v", err)
	}

	// Workspace B: identical shape, sibling rows that must stay invisible.
	b1 := wsIsolationSeedAt(t, f.storeB, "iso-count-b1", "count beta "+wsSecCanaryB, base.Add(2*time.Minute))
	b2 := wsIsolationSeedAt(t, f.storeB, "iso-count-b2", "count beta two "+wsSecCanaryB, base.Add(3*time.Minute))
	if err := f.storeB.store.graph().CreateEdge(ctx, &domain.Edge{FromObsID: b1.ID, ToObsID: b2.ID, RelationType: domain.RelationReferences}); err != nil {
		t.Fatalf("workspace B references edge: %v", err)
	}
	if err := f.storeB.store.graph().CreateEdge(ctx, &domain.Edge{FromObsID: b1.ID, ToObsID: b2.ID, RelationType: domain.RelationContradicts}); err != nil {
		t.Fatalf("workspace B contradiction edge: %v", err)
	}
	sessionB := &domain.Session{Project: wsSecProject, StartedAt: base}
	if err := f.storeB.CreateSession(ctx, sessionB); err != nil {
		t.Fatalf("workspace B prompt session: %v", err)
	}
	if err := f.storeB.store.prompts().Save(ctx, &domain.Prompt{SessionID: sessionB.ID, Project: wsSecProject, Content: "workspace B prompt"}); err != nil {
		t.Fatalf("workspace B prompt: %v", err)
	}

	// Observation count: workspace A owns exactly its two rows, never the
	// tenant-wide four.
	if n, err := f.storeA.store.observations().CountAll(ctx); err != nil || n != 2 {
		t.Fatalf("workspace A CountAll=%d err=%v, want 2 (sibling rows must be invisible)", n, err)
	}
	// Root-connected count: only a2 is reachable from a1 inside workspace A.
	if n, err := f.storeA.store.observations().CountByRoot(ctx, a1.ID); err != nil || n != 1 {
		t.Fatalf("workspace A CountByRoot=%d err=%v, want 1", n, err)
	}
	if n, err := f.storeA.store.observations().CountEdgesAsObs(ctx, a1.ID); err != nil || n != 1 {
		t.Fatalf("workspace A CountEdgesAsObs=%d err=%v, want 1", n, err)
	}
	// Edge counts: workspace A owns exactly its two edges.
	if n, err := f.storeA.store.graph().CountAllEdges(ctx); err != nil || n != 2 {
		t.Fatalf("workspace A CountAllEdges=%d err=%v, want 2 (never the tenant-wide four)", n, err)
	}
	if n, err := f.storeA.store.graph().CountEdgesByObservation(ctx, a1.ID); err != nil || n != 2 {
		t.Fatalf("workspace A CountEdgesByObservation=%d err=%v, want 2", n, err)
	}
	// Related observations at depth 1: exactly a2, never a sibling row.
	related, err := f.storeA.store.graph().GetRelated(ctx, a1.ID, 1)
	if err != nil {
		t.Fatalf("workspace A GetRelated: %v", err)
	}
	if len(related) != 1 || related[0].ID != a2.ID {
		t.Fatalf("workspace A GetRelated=%v, want exactly observation %d", wsIsolationTitles(related), a2.ID)
	}
	// At depth 2 the historical traversal bounces back to the root; the
	// scoping invariant is that every returned row still belongs to
	// workspace A's id set, never the sibling workspace.
	bounce, err := f.storeA.store.graph().GetRelated(ctx, a1.ID, 2)
	if err != nil {
		t.Fatalf("workspace A GetRelated depth 2: %v", err)
	}
	allowed := map[int64]bool{a1.ID: true, a2.ID: true}
	for _, o := range bounce {
		if !allowed[o.ID] {
			t.Fatalf("workspace A GetRelated depth 2 disclosed row %d (%q) outside workspace A's rows", o.ID, o.Title)
		}
	}
	// Edge list for an observation: only workspace A edges with workspace A
	// endpoints.
	edges, err := f.storeA.store.graph().GetEdgesForObservation(ctx, a1.ID)
	if err != nil {
		t.Fatalf("workspace A GetEdgesForObservation: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("workspace A GetEdgesForObservation=%d edges, want 2", len(edges))
	}
	for _, e := range edges {
		if e.FromPublicID != a1.PublicID || e.ToPublicID != a2.PublicID {
			t.Fatalf("workspace A edge endpoints drifted outside the workspace: from=%s to=%s", e.FromPublicID, e.ToPublicID)
		}
	}
	// Contradiction feed (edges carry DB wall-clock created_at): only
	// workspace A's contradiction edge.
	contradictions, err := f.storeA.store.graph().GetContradictions(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("workspace A GetContradictions: %v", err)
	}
	if len(contradictions) != 1 || contradictions[0].FromPublicID != a1.PublicID || contradictions[0].ToPublicID != a2.PublicID {
		t.Fatalf("workspace A GetContradictions=%d edges, want exactly the workspace A contradiction", len(contradictions))
	}
	// Evolution chain read stays scoped and error-free.
	if _, err := f.storeA.store.graph().GetEvolutionChain(ctx, a1.ID, a2.ID); err != nil {
		t.Fatalf("workspace A GetEvolutionChain: %v", err)
	}
	// Prompt list: only workspace A's prompt is visible under the shared
	// project key.
	prompts, err := f.storeA.store.prompts().List(ctx, wsSecProject, 10)
	if err != nil {
		t.Fatalf("workspace A prompt list: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Content != "workspace A prompt" {
		t.Fatalf("workspace A prompt list=%+v, want exactly the workspace A prompt", prompts)
	}
}
