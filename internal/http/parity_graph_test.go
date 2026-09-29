package http

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// localGraphFixture is an in-memory stand-in for the graph store so the BFS can
// be exercised deterministically without SQLite. Adjacency is bidirectional:
// the real store returns an edge when the observation is either endpoint.
type localGraphFixture struct {
	observations map[int64]*domain.Observation
	edges        map[int64][]*domain.Edge
	failing      map[int64]bool
}

func newLocalGraphFixture(observations ...*domain.Observation) *localGraphFixture {
	fixture := &localGraphFixture{
		observations: make(map[int64]*domain.Observation, len(observations)),
		edges:        make(map[int64][]*domain.Edge),
		failing:      make(map[int64]bool),
	}
	for _, observation := range observations {
		fixture.observations[observation.ID] = observation
	}
	return fixture
}

func (f *localGraphFixture) addEdge(edge *domain.Edge) {
	f.edges[edge.FromObsID] = append(f.edges[edge.FromObsID], edge)
	f.edges[edge.ToObsID] = append(f.edges[edge.ToObsID], edge)
}

func (f *localGraphFixture) neighbors(_ context.Context, id int64) ([]*domain.Edge, error) {
	if f.failing[id] {
		return nil, errors.New("edge lookup failed")
	}
	return f.edges[id], nil
}

func (f *localGraphFixture) lookup(_ context.Context, id int64) (*domain.Observation, error) {
	observation, ok := f.observations[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return observation, nil
}

func localGraphObservation(id int64, title string) *domain.Observation {
	return &domain.Observation{ID: id, Title: title, Type: "manual", Project: "demo", Scope: "project", Source: "manual"}
}

func localGraphEdge(id, from, to int64, relation string) *domain.Edge {
	return &domain.Edge{ID: id, FromObsID: from, ToObsID: to, RelationType: relation, Weight: 1, Confidence: 1}
}

func localGraphHop(subgraph *domain.GraphSubgraph, nodeID string) (int, bool) {
	for _, node := range subgraph.Nodes {
		if node.ID == nodeID {
			return node.Hop, true
		}
	}
	return 0, false
}

func localGraphNodeIDs(subgraph *domain.GraphSubgraph) map[string]bool {
	ids := make(map[string]bool, len(subgraph.Nodes))
	for _, node := range subgraph.Nodes {
		ids[node.ID] = true
	}
	return ids
}

func TestLocalProjectGraphClampBounds(t *testing.T) {
	cases := []struct {
		name                    string
		depth, maxNodes         int
		wantDepth, wantMaxNodes int
	}{
		{"zero falls back", 0, 0, 2, 100},
		{"negative falls back", -4, -9, 2, 100},
		{"minimum accepted", 1, 1, 1, 1},
		{"defaults accepted", 2, 100, 2, 100},
		{"maximum accepted", 10, 200, 10, 200},
		{"above maximum clamps", 11, 201, 10, 200},
		{"far above maximum clamps", 500, 9000, 10, 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			depth, maxNodes := clampProjectGraphBounds(tc.depth, tc.maxNodes)
			if depth != tc.wantDepth || maxNodes != tc.wantMaxNodes {
				t.Fatalf("clampProjectGraphBounds(%d, %d) = (%d, %d), want (%d, %d)",
					tc.depth, tc.maxNodes, depth, maxNodes, tc.wantDepth, tc.wantMaxNodes)
			}
		})
	}
}

func TestLocalProjectGraphNilStoreIsSafe(t *testing.T) {
	ctx := context.Background()

	var zero localProjectGraph
	empty, err := zero.ProjectGraph(ctx, "demo", 2, 100)
	if err != nil {
		t.Fatalf("zero-value ProjectGraph error = %v, want nil", err)
	}
	if empty == nil || empty.Root != "demo" {
		t.Fatalf("zero-value subgraph = %+v, want root demo", empty)
	}
	if len(empty.Nodes) != 0 || len(empty.Edges) != 0 {
		t.Fatalf("zero-value subgraph nodes/edges = %d/%d, want 0/0", len(empty.Nodes), len(empty.Edges))
	}

	allProjects, err := zero.ProjectGraph(ctx, "", 0, 0)
	if err != nil {
		t.Fatalf("zero-value ProjectGraph error = %v, want nil", err)
	}
	if allProjects.Root != "all_projects" {
		t.Fatalf("empty project root = %q, want all_projects", allProjects.Root)
	}
	if allProjects.Nodes != nil || allProjects.Edges != nil {
		t.Fatalf("empty project nodes/edges = %v/%v, want nil/nil", allProjects.Nodes, allProjects.Edges)
	}

	// Observations wired but graph store nil must not dereference the store.
	observationsOnly := newLocalProjectGraph(&Deps{})
	if _, err := observationsOnly.ProjectGraph(ctx, "demo", 2, 100); err != nil {
		t.Fatalf("nil graph store error = %v, want nil", err)
	}
}

func TestLocalProjectGraphBoundedDepth(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalGraphFixture(
		localGraphObservation(1, "one"),
		localGraphObservation(2, "two"),
		localGraphObservation(3, "three"),
		localGraphObservation(4, "four"),
	)
	fixture.addEdge(localGraphEdge(11, 1, 2, "references"))
	fixture.addEdge(localGraphEdge(12, 2, 3, "references"))
	fixture.addEdge(localGraphEdge(13, 3, 4, "references"))

	seeds := []*domain.Observation{fixture.observations[1], fixture.observations[2]}

	oneHop := buildProjectGraphSubgraph(ctx, "demo", seeds, 1, 10, fixture.neighbors, fixture.lookup)
	if got := localGraphNodeIDs(oneHop); len(got) != 3 || !got["observation:1"] || !got["observation:2"] || !got["observation:3"] {
		t.Fatalf("depth 1 nodes = %v, want observations 1,2,3", got)
	}
	if hop, ok := localGraphHop(oneHop, "observation:3"); !ok || hop != 1 {
		t.Fatalf("depth 1 hop(observation:3) = %d (present=%v), want 1", hop, ok)
	}
	if hop, ok := localGraphHop(oneHop, "observation:1"); !ok || hop != 0 {
		t.Fatalf("depth 1 hop(observation:1) = %d (present=%v), want 0", hop, ok)
	}

	twoHops := buildProjectGraphSubgraph(ctx, "demo", seeds, 2, 10, fixture.neighbors, fixture.lookup)
	if got := localGraphNodeIDs(twoHops); len(got) != 4 || !got["observation:4"] {
		t.Fatalf("depth 2 nodes = %v, want observations 1,2,3,4", got)
	}
	if hop, ok := localGraphHop(twoHops, "observation:4"); !ok || hop != 2 {
		t.Fatalf("depth 2 hop(observation:4) = %d (present=%v), want 2", hop, ok)
	}
}

func TestLocalProjectGraphTruncatesAtMaxNodes(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalGraphFixture(
		localGraphObservation(1, "one"),
		localGraphObservation(2, "two"),
		localGraphObservation(3, "three"),
		localGraphObservation(4, "four"),
		localGraphObservation(5, "five"),
	)
	fixture.addEdge(localGraphEdge(11, 1, 2, "references"))
	fixture.addEdge(localGraphEdge(12, 2, 3, "references"))
	fixture.addEdge(localGraphEdge(13, 3, 4, "references"))
	fixture.addEdge(localGraphEdge(14, 4, 5, "references"))

	subgraph := buildProjectGraphSubgraph(ctx, "demo", []*domain.Observation{fixture.observations[1]}, 10, 3, fixture.neighbors, fixture.lookup)
	if len(subgraph.Nodes) != 3 {
		t.Fatalf("truncated node count = %d, want 3", len(subgraph.Nodes))
	}
	if !subgraph.Truncated {
		t.Fatalf("truncated flag = false, want true")
	}
	if len(subgraph.Edges) != 2 {
		t.Fatalf("truncated edge count = %d, want 2 (dangling edge to truncated node must be dropped)", len(subgraph.Edges))
	}
	if got := localGraphNodeIDs(subgraph); !got["observation:1"] || !got["observation:2"] || !got["observation:3"] {
		t.Fatalf("truncated nodes = %v, want observations 1,2,3", got)
	}
}

func TestLocalProjectGraphTerminatesOnCyclesAndEmpty(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalGraphFixture(
		localGraphObservation(1, "one"),
		localGraphObservation(2, "two"),
		localGraphObservation(3, "three"),
		localGraphObservation(5, "isolated"),
	)
	fixture.addEdge(localGraphEdge(11, 1, 2, "references"))
	fixture.addEdge(localGraphEdge(12, 2, 3, "references"))
	fixture.addEdge(localGraphEdge(13, 3, 1, "references"))

	first := buildProjectGraphSubgraph(ctx, "demo", []*domain.Observation{fixture.observations[1]}, 5, 50, fixture.neighbors, fixture.lookup)
	if len(first.Nodes) != 3 || len(first.Edges) != 3 {
		t.Fatalf("cycle nodes/edges = %d/%d, want 3/3", len(first.Nodes), len(first.Edges))
	}
	if first.Truncated {
		t.Fatalf("cycle truncated = true, want false")
	}
	second := buildProjectGraphSubgraph(ctx, "demo", []*domain.Observation{fixture.observations[1]}, 5, 50, fixture.neighbors, fixture.lookup)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("traversal is not deterministic:\nfirst  = %+v\nsecond = %+v", first, second)
	}

	disconnected := buildProjectGraphSubgraph(ctx, "demo",
		[]*domain.Observation{fixture.observations[1], fixture.observations[5]}, 2, 50, fixture.neighbors, fixture.lookup)
	if got := localGraphNodeIDs(disconnected); len(got) != 4 || !got["observation:5"] {
		t.Fatalf("disconnected nodes = %v, want observations 1,2,3,5", got)
	}
	if len(disconnected.Edges) != 3 {
		t.Fatalf("disconnected edges = %d, want 3", len(disconnected.Edges))
	}

	empty := buildProjectGraphSubgraph(ctx, "demo", nil, 2, 100, fixture.neighbors, fixture.lookup)
	if empty.Root != "demo" || len(empty.Nodes) != 0 || len(empty.Edges) != 0 {
		t.Fatalf("empty subgraph = %+v, want empty root demo", empty)
	}
}

func TestLocalProjectGraphPayloadShape(t *testing.T) {
	ctx := context.Background()
	first := &domain.Observation{ID: 7, PublicID: "pub-7", Title: "Design", Type: "decision", Project: "p", Scope: "project", Source: "ai"}
	second := &domain.Observation{ID: 8, PublicID: "pub-8", Title: "Follow-up", Type: "pattern", Project: "p", Scope: "personal", Source: "manual"}
	fixture := newLocalGraphFixture(first, second)
	edge := localGraphEdge(42, 7, 8, "references")
	edge.Weight = 1.5
	edge.Confidence = 0.9
	edge.AssertionKind = "deterministic"
	edge.AssertionStatus = "accepted"
	fixture.addEdge(edge)

	subgraph := buildProjectGraphSubgraph(ctx, "p", []*domain.Observation{first, second}, 1, 100, fixture.neighbors, fixture.lookup)

	if len(subgraph.Nodes) != 2 {
		t.Fatalf("node count = %d, want 2", len(subgraph.Nodes))
	}
	root := subgraph.Nodes[0]
	if root.ID != "observation:pub-7" || root.Kind != "decision" || root.Label != "Design" || root.Project != "p" || root.Hop != 0 {
		t.Fatalf("root node shape = %+v", root)
	}
	if root.Metadata["source"] != "ai" || root.Metadata["scope"] != "project" {
		t.Fatalf("root node metadata = %v", root.Metadata)
	}

	if len(subgraph.Edges) != 1 {
		t.Fatalf("edge count = %d, want 1", len(subgraph.Edges))
	}
	link := subgraph.Edges[0]
	want := domain.GraphLink{
		ID: "edge:42", Source: "observation:pub-7", Target: "observation:pub-8",
		Type: "references", Weight: 1.5, Confidence: 0.9,
		AssertionKind: "deterministic", AssertionStatus: "accepted",
	}
	if !reflect.DeepEqual(link, want) {
		t.Fatalf("link = %+v, want %+v", link, want)
	}

	local := buildProjectGraphSubgraph(ctx, "p", []*domain.Observation{{ID: 9, Title: "Local"}}, 1, 100,
		func(context.Context, int64) ([]*domain.Edge, error) { return nil, nil },
		func(context.Context, int64) (*domain.Observation, error) { return nil, domain.ErrNotFound })
	if len(local.Nodes) != 1 || local.Nodes[0].ID != "observation:9" {
		t.Fatalf("local id fallback = %+v, want observation:9", local.Nodes)
	}
}

func TestLocalProjectGraphToleratesLookupFailures(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalGraphFixture(localGraphObservation(1, "one"), localGraphObservation(2, "two"))
	fixture.addEdge(localGraphEdge(11, 1, 2, "references"))
	fixture.failing[1] = true

	subgraph := buildProjectGraphSubgraph(ctx, "demo", []*domain.Observation{fixture.observations[1]}, 3, 100, fixture.neighbors, func(context.Context, int64) (*domain.Observation, error) {
		return nil, errors.New("lookup failed")
	})
	if len(subgraph.Nodes) != 1 || len(subgraph.Edges) != 0 {
		t.Fatalf("failing fixture nodes/edges = %d/%d, want 1/0", len(subgraph.Nodes), len(subgraph.Edges))
	}
}

func TestLocalProjectGraphOverStore(t *testing.T) {
	srv := setupTestServer(t)
	ctx := context.Background()

	if err := srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	newObservation := func(title string) *domain.Observation {
		return &domain.Observation{SessionID: "s1", Title: title, Content: title, Type: "manual", Project: "demo", Scope: "project", Source: "manual"}
	}
	first, second, third := newObservation("A"), newObservation("B"), newObservation("C")
	for _, observation := range []*domain.Observation{first, second, third} {
		if err := srv.deps.Observations.Save(ctx, observation); err != nil {
			t.Fatalf("save observation %q: %v", observation.Title, err)
		}
	}
	for _, edge := range []*domain.Edge{
		{FromObsID: first.ID, ToObsID: second.ID, RelationType: "references", Weight: 1, Confidence: 1},
		{FromObsID: second.ID, ToObsID: third.ID, RelationType: "references", Weight: 1, Confidence: 1},
	} {
		if err := srv.deps.Graph.CreateEdge(ctx, edge); err != nil {
			t.Fatalf("create edge: %v", err)
		}
	}

	source := newLocalProjectGraph(srv.deps)
	subgraph, err := source.ProjectGraph(ctx, "demo", 2, 100)
	if err != nil {
		t.Fatalf("ProjectGraph error = %v", err)
	}
	if subgraph.Root != "demo" || len(subgraph.Nodes) != 3 || len(subgraph.Edges) != 2 {
		t.Fatalf("subgraph root/nodes/edges = %q/%d/%d, want demo/3/2", subgraph.Root, len(subgraph.Nodes), len(subgraph.Edges))
	}

	defaulted, err := source.ProjectGraph(ctx, "demo", 0, 0)
	if err != nil {
		t.Fatalf("defaulted ProjectGraph error = %v", err)
	}
	if len(defaulted.Nodes) != len(subgraph.Nodes) || len(defaulted.Edges) != len(subgraph.Edges) {
		t.Fatalf("defaulted subgraph = %d/%d, want %d/%d", len(defaulted.Nodes), len(defaulted.Edges), len(subgraph.Nodes), len(subgraph.Edges))
	}

	unknown, err := source.ProjectGraph(ctx, "missing", 2, 100)
	if err != nil {
		t.Fatalf("unknown project error = %v", err)
	}
	if unknown.Root != "missing" || len(unknown.Nodes) != 0 || len(unknown.Edges) != 0 {
		t.Fatalf("unknown project subgraph = %+v, want empty root missing", unknown)
	}
}

// TestLocalProjectGraphDoesNotLeakOutOfProjectNodes is the MEDIUM-5
// regression (REQ-SQ-SEC-005): the seed query is project-scoped but expansion
// reads nodes by identifier through an unscoped GetByID, so an edge crossing
// the project boundary used to serialize the foreign project's title and
// labels into the served subgraph.
func TestLocalProjectGraphDoesNotLeakOutOfProjectNodes(t *testing.T) {
	srv := setupParityServer(t)
	ctx := context.Background()

	if err := srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "alpha", Directory: "."}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	save := func(title, project string) *domain.Observation {
		observation := &domain.Observation{
			SessionID: "s1", Title: title, Content: title, Type: "manual",
			Project: project, Scope: "project", Source: "manual",
		}
		if err := srv.deps.Observations.Save(ctx, observation); err != nil {
			t.Fatalf("save observation %q: %v", title, err)
		}
		return observation
	}
	alphaRoot := save("alpha root", "alpha")
	betaSecret := save("beta secret", "beta")
	alphaLeaf := save("alpha leaf", "alpha")
	for _, edge := range []*domain.Edge{
		{FromObsID: alphaRoot.ID, ToObsID: betaSecret.ID, RelationType: "references", Weight: 1, Confidence: 1},
		{FromObsID: betaSecret.ID, ToObsID: alphaLeaf.ID, RelationType: "references", Weight: 1, Confidence: 1},
	} {
		if err := srv.deps.Graph.CreateEdge(ctx, edge); err != nil {
			t.Fatalf("create edge: %v", err)
		}
	}

	rec := doRaw(t, srv.httpServer.Handler, http.MethodGet, "/api/graph/project-graph?project=alpha", nil, parityAuth())
	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); strings.Contains(body, "beta secret") {
		t.Fatalf("project graph leaked an out-of-project title: %s", body)
	}

	var subgraph domain.GraphSubgraph
	decodeJSON(t, rec, &subgraph)
	if len(subgraph.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want exactly the two seeded alpha observations", subgraph.Nodes)
	}
	wantNode := map[string]bool{localObservationNodeID(alphaRoot): true, localObservationNodeID(alphaLeaf): true}
	for _, node := range subgraph.Nodes {
		if !wantNode[node.ID] || node.Project != "alpha" || node.Label == "beta secret" {
			t.Fatalf("out-of-project node leaked: %+v", node)
		}
	}
	if len(subgraph.Edges) != 0 {
		t.Fatalf("crossing edges = %+v, want both boundary edges dropped", subgraph.Edges)
	}
}

// TestLocalProjectGraphPrunesCrossProjectBoundary pins the scoped counterpart:
// an in-project node linked only to an out-of-project node survives while the
// boundary edge is dropped, keeping the payload referentially closed.
func TestLocalProjectGraphPrunesCrossProjectBoundary(t *testing.T) {
	ctx := context.Background()
	alpha := &domain.Observation{ID: 1, Title: "alpha", Project: "alpha", Scope: "project"}
	beta := &domain.Observation{ID: 2, Title: "beta", Project: "beta", Scope: "project"}
	fixture := newLocalGraphFixture(alpha, beta)
	fixture.addEdge(localGraphEdge(11, 1, 2, "references"))

	subgraph := buildProjectGraphSubgraph(ctx, "alpha", []*domain.Observation{alpha}, 2, 10, fixture.neighbors, fixture.lookup)
	if got := localGraphNodeIDs(subgraph); len(got) != 1 || !got["observation:1"] {
		t.Fatalf("nodes = %v, want only the in-project seed", got)
	}
	if len(subgraph.Edges) != 0 {
		t.Fatalf("edges = %+v, want the boundary edge dropped", subgraph.Edges)
	}
}

// TestLocalProjectGraphAllProjectsKeepsCrossProjectExpansion pins that the
// aggregate root disables the boundary, so unscoped traversal is unchanged.
func TestLocalProjectGraphAllProjectsKeepsCrossProjectExpansion(t *testing.T) {
	ctx := context.Background()
	alpha := &domain.Observation{ID: 1, Title: "alpha", Project: "alpha", Scope: "project"}
	beta := &domain.Observation{ID: 2, Title: "beta", Project: "beta", Scope: "project"}
	fixture := newLocalGraphFixture(alpha, beta)
	fixture.addEdge(localGraphEdge(11, 1, 2, "references"))

	subgraph := buildProjectGraphSubgraph(ctx, "all_projects", []*domain.Observation{alpha}, 2, 10, fixture.neighbors, fixture.lookup)
	if got := localGraphNodeIDs(subgraph); len(got) != 2 || !got["observation:2"] {
		t.Fatalf("all_projects nodes = %v, want both projects expanded", got)
	}
	if len(subgraph.Edges) != 1 {
		t.Fatalf("all_projects edges = %d, want 1", len(subgraph.Edges))
	}
}
