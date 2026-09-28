package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// graphPort records the arguments ProjectGraph receives so the handler's query
// parsing and clamping can be asserted without any store dependency.
type graphPort struct {
	subgraph    *domain.GraphSubgraph
	err         error
	gotProject  string
	gotDepth    int
	gotMaxNodes int
	calls       int
}

func (p *graphPort) CurrentPrincipal(context.Context) (Principal, error) { return Principal{}, nil }

func (p *graphPort) ServerStats(context.Context) (*domain.ServerStats, error) { return nil, nil }

func (p *graphPort) Projects(context.Context) ([]string, error) { return nil, nil }

func (p *graphPort) AgentProjects(context.Context) (map[string]string, error) { return nil, nil }

func (p *graphPort) ProjectGraph(_ context.Context, projectID string, depth, maxNodes int) (*domain.GraphSubgraph, error) {
	p.calls++
	p.gotProject = projectID
	p.gotDepth = depth
	p.gotMaxNodes = maxNodes
	return p.subgraph, p.err
}

var _ Port = (*graphPort)(nil)

func serveProjectGraph(t *testing.T, port Port, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	New(port).ProjectGraph(rec, req)
	return rec
}

func TestProjectGraphDefaultBounds(t *testing.T) {
	port := &graphPort{subgraph: &domain.GraphSubgraph{Root: "all_projects"}}

	rec := serveProjectGraph(t, port, "/api/graph/project-graph")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}
	if port.calls != 1 {
		t.Fatalf("ProjectGraph calls = %d, want 1", port.calls)
	}
	if port.gotProject != "" {
		t.Fatalf("project = %q, want empty", port.gotProject)
	}
	if port.gotDepth != 2 || port.gotMaxNodes != 100 {
		t.Fatalf("bounds = (%d,%d), want (2,100)", port.gotDepth, port.gotMaxNodes)
	}
}

func TestProjectGraphClampsBounds(t *testing.T) {
	cases := []struct {
		name         string
		target       string
		wantDepth    int
		wantMaxNodes int
	}{
		{"above max clamps", "/api/graph/project-graph?depth=99&max_nodes=999", 10, 200},
		{"upper boundary passes", "/api/graph/project-graph?depth=10&max_nodes=200", 10, 200},
		{"lower boundary passes", "/api/graph/project-graph?depth=1&max_nodes=1", 1, 1},
		{"in range passes", "/api/graph/project-graph?depth=5&max_nodes=150", 5, 150},
		{"zero falls back", "/api/graph/project-graph?depth=0&max_nodes=0", 2, 100},
		{"negative falls back", "/api/graph/project-graph?depth=-4&max_nodes=-1", 2, 100},
		{"non-numeric falls back", "/api/graph/project-graph?depth=abc&max_nodes=xyz", 2, 100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := &graphPort{subgraph: &domain.GraphSubgraph{Root: "all_projects"}}
			serveProjectGraph(t, port, tc.target)
			if port.gotDepth != tc.wantDepth || port.gotMaxNodes != tc.wantMaxNodes {
				t.Fatalf("bounds = (%d,%d), want (%d,%d)", port.gotDepth, port.gotMaxNodes, tc.wantDepth, tc.wantMaxNodes)
			}
		})
	}
}

// TestProjectGraphSerializesSubgraphIdentically pins the emitted bytes to the
// domain.GraphSubgraph encoding the pre-extraction server handler wrote
// (internal/platform/server/http.go:1101). Any wrapper or field rename breaks
// the Sigma client that both muxes feed.
func TestProjectGraphSerializesSubgraphIdentically(t *testing.T) {
	port := &graphPort{subgraph: &domain.GraphSubgraph{
		Root: "proj-a",
		Nodes: []domain.GraphNode{{
			ID:       "observation:abc",
			Kind:     "note",
			Label:    "Root note",
			Project:  "proj-a",
			Hop:      0,
			Metadata: map[string]any{"source": "manual", "scope": "project"},
		}},
		Edges: []domain.GraphLink{{
			ID:     "link:1",
			Source: "observation:abc",
			Target: "entity:xyz",
			Type:   "mentions",
		}},
	}}

	rec := serveProjectGraph(t, port, "/api/graph/project-graph?project=proj-a")

	if port.gotProject != "proj-a" {
		t.Fatalf("project = %q, want %q", port.gotProject, "proj-a")
	}
	const want = `{"root":"proj-a","nodes":[{"id":"observation:abc","kind":"note",` +
		`"label":"Root note","project":"proj-a","hop":0,` +
		`"metadata":{"scope":"project","source":"manual"}}],` +
		`"edges":[{"id":"link:1","source":"observation:abc","target":"entity:xyz",` +
		`"type":"mentions"}],"truncated":false}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("subgraph mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestProjectGraphNoSubgraphEmitsServerEmptyEnvelope covers the tolerance the
// server handler had for failed/absent subgraph lookups: a nil subgraph is not
// an error, it is the root-labelled empty envelope.
func TestProjectGraphNoSubgraphEmitsServerEmptyEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
	}{
		{
			"named project",
			"/api/graph/project-graph?project=ghost",
			`{"root":"ghost","nodes":null,"edges":null,"truncated":false}` + "\n",
		},
		{
			"no project falls back to all_projects label",
			"/api/graph/project-graph",
			`{"root":"all_projects","nodes":null,"edges":null,"truncated":false}` + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveProjectGraph(t, &graphPort{}, tc.target)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestProjectGraphMapsPortErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			"unauthenticated",
			ErrUnauthenticated,
			http.StatusUnauthorized,
			`{"error":{"code":"unauthorized","message":"valid bearer token required"}}` + "\n",
		},
		{
			"forbidden",
			ErrForbidden,
			http.StatusForbidden,
			`{"error":{"code":"forbidden","message":"principal is not authorized for this operation"}}` + "\n",
		},
		{
			"not found",
			domain.ErrNotFound,
			http.StatusNotFound,
			`{"error":{"code":"not_found","message":"resource not found"}}` + "\n",
		},
		{
			"invalid input",
			domain.ErrInvalidInput,
			http.StatusBadRequest,
			`{"error":{"code":"invalid_request","message":"invalid operation input"}}` + "\n",
		},
		{
			"unexpected",
			errors.New("store failure: leaked-secret-detail"),
			http.StatusInternalServerError,
			`{"error":{"code":"operation_failed","message":"operation failed"}}` + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveProjectGraph(t, &graphPort{err: tc.err}, "/api/graph/project-graph")

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Fatalf("error envelope mismatch\n got: %s\nwant: %s", got, tc.wantBody)
			}
			if strings.Contains(rec.Body.String(), "leaked-secret-detail") {
				t.Fatalf("raw error text leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestProjectGraphUnauthenticatedSetsChallenge(t *testing.T) {
	rec := serveProjectGraph(t, &graphPort{err: ErrUnauthenticated}, "/api/graph/project-graph")

	if got, want := rec.Header().Get("WWW-Authenticate"), `Bearer realm="cortex"`; got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}
