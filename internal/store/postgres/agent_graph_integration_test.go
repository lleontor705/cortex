//go:build postgres_integration

package postgres

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

func TestAgentGraphSnapshotIsolationAndBudgets(t *testing.T) {
	h := newPostgresHarness(t)
	tenant, workspace, project := uuid.New(), uuid.New(), uuid.New()
	fixture := newScopedCodeFixture(t, h, tenant, workspace, project, "agent-graph")
	actor := uuid.New()
	store := newReindexPrincipalStore(t, h, fixture, actor, "user", []string{"viewer"}, nil, nil)

	insert := func(f scopedCodeFixture, title, classification string, owner uuid.UUID) (int64, string) {
		t.Helper()
		id := insertReindexObservation(t, h, f, title, classification, owner)
		var publicID string
		if err := h.admin.QueryRow(t.Context(), `SELECT public_id::text FROM observations WHERE tenant_id=$1 AND id=$2`, f.tenant, id).Scan(&publicID); err != nil {
			t.Fatal(err)
		}
		return id, publicID
	}
	link := func(tenantID uuid.UUID, from, to int64, relation string) {
		t.Helper()
		if _, err := h.admin.Exec(t.Context(), `INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type) VALUES($1,$2,$3,$4)`, tenantID, from, to, relation); err != nil {
			t.Fatal(err)
		}
	}

	seedID, seedPublicID := insert(fixture, "seed", "internal", actor)
	restrictedID, restrictedPublicID := insert(fixture, "restricted", "restricted", actor)
	confidentialID, confidentialPublicID := insert(fixture, "confidential", "confidential", actor)
	personalID, personalPublicID := insert(fixture, "foreign personal", "personal", uuid.New())

	otherTenantFixture := newScopedCodeFixture(t, h, uuid.New(), uuid.New(), uuid.New(), "other-tenant")
	_, otherTenantPublicID := insert(otherTenantFixture, "other tenant", "internal", uuid.New())
	otherWorkspaceFixture := newScopedCodeFixture(t, h, tenant, uuid.New(), uuid.New(), "other-workspace")
	_, otherWorkspacePublicID := insert(otherWorkspaceFixture, "other workspace", "internal", actor)
	otherProject := uuid.New()
	if _, err := h.admin.Exec(t.Context(), `INSERT INTO projects(tenant_id,workspace_id,public_id,name) SELECT $1,id,$3,$4 FROM workspaces WHERE tenant_id=$1 AND public_id=$2`, tenant, workspace, otherProject, "other-project"); err != nil {
		t.Fatalf("seed sibling project: %v", err)
	}
	otherProjectFixture := scopedCodeFixture{tenant: tenant, workspace: workspace, project: otherProject, store: fixture.store}
	otherProjectFixture.store = newReindexPrincipalStore(t, h, otherProjectFixture, uuid.New(), "user", []string{"owner"}, []string{"*"}, nil)
	otherProjectID, otherProjectPublicID := insert(otherProjectFixture, "other project", "internal", actor)

	link(tenant, seedID, restrictedID, "references")
	link(tenant, seedID, confidentialID, "references")
	link(tenant, seedID, personalID, "references")
	link(tenant, seedID, otherProjectID, "references")

	allSeeds := []string{
		seedPublicID,
		restrictedPublicID,
		confidentialPublicID,
		personalPublicID,
		otherTenantPublicID,
		otherWorkspacePublicID,
		otherProjectPublicID,
	}
	isolation, err := store.GetAgentGraphSnapshot(t.Context(), project.String(), "agent-graph", allSeeds, 3, 32, 64)
	if err != nil {
		t.Fatal(err)
	}
	if isolation.Truncated || len(isolation.Nodes) != 1 || len(isolation.Edges) != 0 || isolation.Nodes[0].ID != "observation:"+seedPublicID {
		t.Fatalf("invisible endpoint or edge entered snapshot: %#v", isolation)
	}

	for i := 0; i < 5; i++ {
		neighborID, _ := insert(fixture, "visible-"+string(rune('a'+i)), "internal", actor)
		link(tenant, seedID, neighborID, "fanout")
	}
	first, err := store.GetAgentGraphSnapshot(t.Context(), project.String(), "agent-graph", []string{seedPublicID}, 2, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GetAgentGraphSnapshot(t.Context(), project.String(), "agent-graph", []string{seedPublicID}, 2, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Truncated || len(first.Nodes) != 3 || len(first.Edges) != 2 {
		t.Fatalf("high fanout escaped explicit budgets: %#v", first)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("snapshot order is nondeterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !sort.SliceIsSorted(first.Nodes, func(i, j int) bool { return first.Nodes[i].ID < first.Nodes[j].ID }) ||
		!sort.SliceIsSorted(first.Edges, func(i, j int) bool { return first.Edges[i].ID < first.Edges[j].ID }) {
		t.Fatalf("snapshot is not canonically ordered: %#v", first)
	}

	saveScopedGraph(t, fixture, "primary")
	saveScopedGraph(t, otherWorkspaceFixture, "sibling")
	saveScopedGraph(t, otherTenantFixture, "foreign")
	if err := otherProjectFixture.store.SaveCodeGraph(t.Context(), &code.CodeGraph{
		Project: otherProject.String(),
		Symbols: []code.Symbol{
			{ID: "000-sibling-entry", Project: otherProject.String(), FilePath: "other-project/ApplicationDbContext.cs", LineNumber: 1, EndLine: 4, Kind: "class", Name: "OtherDbContext"},
			{ID: "000-sibling-helper", Project: otherProject.String(), FilePath: "other-project/Repository.cs", LineNumber: 8, EndLine: 9, Kind: "method", Name: "OtherRepository"},
		},
		Relations: []code.Relation{{Project: otherProject.String(), SourceID: "000-sibling-entry", TargetID: "000-sibling-helper", Relation: "uses", Confidence: 1}},
	}); err != nil {
		t.Fatalf("save sibling-project graph: %v", err)
	}
	oneSymbol, err := store.GetAgentCodeGraphSnapshot(t.Context(), project.String(), "DbContext", 2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(oneSymbol.Symbols) != 1 || len(oneSymbol.Relations) != 0 ||
		oneSymbol.Symbols[0].Project != project.String() ||
		!strings.HasPrefix(oneSymbol.Symbols[0].FilePath, "primary/") {
		t.Fatalf("AST node budget or scope leaked: %#v", oneSymbol)
	}

	firstCode, err := store.GetAgentCodeGraphSnapshot(t.Context(), project.String(), "DbContext", 2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondCode, err := store.GetAgentCodeGraphSnapshot(t.Context(), project.String(), "DbContext", 2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstCode.Symbols) != 2 || len(firstCode.Relations) != 1 {
		t.Fatalf("AST snapshot ignored explicit budgets: %#v", firstCode)
	}
	for _, symbol := range firstCode.Symbols {
		if symbol.Project != project.String() || !strings.HasPrefix(symbol.FilePath, "primary/") {
			t.Fatalf("cross-scope AST symbol entered snapshot: %#v", symbol)
		}
	}
	for _, relation := range firstCode.Relations {
		if relation.Project != project.String() {
			t.Fatalf("cross-scope AST relation entered snapshot: %#v", relation)
		}
	}
	if !reflect.DeepEqual(firstCode, secondCode) {
		t.Fatalf("AST snapshot order is nondeterministic:\nfirst=%#v\nsecond=%#v", firstCode, secondCode)
	}
}
