package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// parityPort is the stats/projects/agent-projects test double. Only the three
// methods under test are populated; the remaining Port methods return zero
// values so the double proves the neutral interface stays satisfiable without a
// store or server import.
type parityPort struct {
	stats         *domain.ServerStats
	statsErr      error
	projects      []string
	projectsErr   error
	agentProjects map[string]string
	agentErr      error
}

func (p parityPort) CurrentPrincipal(context.Context) (Principal, error) { return Principal{}, nil }

func (p parityPort) ServerStats(context.Context) (*domain.ServerStats, error) {
	return p.stats, p.statsErr
}

func (p parityPort) Projects(context.Context) ([]string, error) { return p.projects, p.projectsErr }

func (p parityPort) AgentProjects(context.Context) (map[string]string, error) {
	return p.agentProjects, p.agentErr
}

func (p parityPort) ProjectGraph(context.Context, string, int, int) (*domain.GraphSubgraph, error) {
	return nil, nil
}

var _ Port = parityPort{}

func serveStats(t *testing.T, port Port, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	New(port).Stats(rec, req)
	return rec
}

func serveProjects(t *testing.T, port Port, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	New(port).Projects(rec, req)
	return rec
}

func serveAgentProjects(t *testing.T, port Port, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	New(port).AgentProjects(rec, req)
	return rec
}

// TestStatsGoldenEnvelopeMatchesServerSerialization pins /api/stats to the
// pre-extraction server body (internal/platform/server/http.go:621-628), which
// wrote the domain.ServerStats value verbatim. encoding/json emits struct fields
// in declaration order (internal/domain/models.go:139-145), so the assertion is
// byte-exact.
func TestStatsGoldenEnvelopeMatchesServerSerialization(t *testing.T) {
	rec := serveStats(t, parityPort{stats: &domain.ServerStats{
		Observations:   42,
		Sessions:       7,
		ActiveSessions: 2,
		Edges:          13,
		Projects:       3,
	}}, "/api/stats")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}
	const want = `{"observations":42,"sessions":7,"active_sessions":2,"edges":13,"projects":3}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestStatsIgnoresProjectQueryParameter records the server's query contract:
// apiHandler.stats never read r.URL.Query(), so /api/stats?project=... must be
// byte-identical to /api/stats. The web client sends the parameter opportunistically
// (web/src/lib/api.ts:479-482); the extraction must not start honoring it.
func TestStatsIgnoresProjectQueryParameter(t *testing.T) {
	port := parityPort{stats: &domain.ServerStats{Observations: 5, Projects: 1}}

	plain := serveStats(t, port, "/api/stats")
	filtered := serveStats(t, port, "/api/stats?project=cortex&limit=0")

	if plain.Code != filtered.Code {
		t.Fatalf("status diverged: %d vs %d", plain.Code, filtered.Code)
	}
	if plain.Body.String() != filtered.Body.String() {
		t.Fatalf("query changed body\n plain: %s\nfiltered: %s", plain.Body.String(), filtered.Body.String())
	}
}

// TestProjectsGoldenEnvelopeMatchesServerSerialization pins /api/projects to the
// server handler (internal/platform/server/http.go:657-664), which wrote the
// ListProjects []string verbatim: a non-nil slice serializes as a JSON array and
// a nil slice as null.
func TestProjectsGoldenEnvelopeMatchesServerSerialization(t *testing.T) {
	rec := serveProjects(t, parityPort{projects: []string{"alpha", "beta"}}, "/api/projects")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	const want = `["alpha","beta"]` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestProjectsNilListSerializesAsNull(t *testing.T) {
	rec := serveProjects(t, parityPort{}, "/api/projects")

	const want = "null\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestAgentProjectsGoldenEnvelopeMatchesServerSerialization pins
// /api/agent/projects to the server handler
// (internal/platform/server/agent_http.go:34-52): no-store headers, the map
// flattened to {id,label} entries sorted by label then id, wrapped under a
// single "projects" key.
func TestAgentProjectsGoldenEnvelopeMatchesServerSerialization(t *testing.T) {
	rec := serveAgentProjects(t, parityPort{agentProjects: map[string]string{
		"p-b": "Zeta",
		"p-a": "Alpha",
	}}, "/api/agent/projects")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want %q", got, "no-cache")
	}
	const want = `{"projects":[{"id":"p-a","label":"Alpha"},{"id":"p-b","label":"Zeta"}]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestAgentProjectsSortTieBreaksByID covers the server comparator's second key
// (agent_http.go:45-50): equal labels order by ascending project id.
func TestAgentProjectsSortTieBreaksByID(t *testing.T) {
	rec := serveAgentProjects(t, parityPort{agentProjects: map[string]string{
		"p-z": "Same",
		"p-a": "Same",
	}}, "/api/agent/projects")

	const want = `{"projects":[{"id":"p-a","label":"Same"},{"id":"p-z","label":"Same"}]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestAgentProjectsEmptyMapSerializesAsEmptyArray(t *testing.T) {
	rec := serveAgentProjects(t, parityPort{agentProjects: map[string]string{}}, "/api/agent/projects")

	const want = `{"projects":[]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

type envelopeCase struct {
	name       string
	err        error
	wantStatus int
	wantBody   string
}

// TestStatsAndProjectsErrorMapping verifies the shared operation mapping
// (internal/api/port.go:76-89) reproduces the server respondOperationError
// contract (internal/platform/server/http.go:1419-1435) plus the neutral
// ErrUnauthenticated -> 401 translation.
func TestStatsAndProjectsErrorMapping(t *testing.T) {
	cases := []envelopeCase{
		{"unauthenticated", ErrUnauthenticated, http.StatusUnauthorized,
			`{"error":{"code":"unauthorized","message":"valid bearer token required"}}` + "\n"},
		{"forbidden", ErrForbidden, http.StatusForbidden,
			`{"error":{"code":"forbidden","message":"principal is not authorized for this operation"}}` + "\n"},
		{"not_found", domain.ErrNotFound, http.StatusNotFound,
			`{"error":{"code":"not_found","message":"resource not found"}}` + "\n"},
		{"invalid_input", domain.ErrInvalidInput, http.StatusBadRequest,
			`{"error":{"code":"invalid_request","message":"invalid operation input"}}` + "\n"},
		{"operation_failed", errors.New("store unavailable"), http.StatusInternalServerError,
			`{"error":{"code":"operation_failed","message":"operation failed"}}` + "\n"},
	}

	for _, tc := range cases {
		t.Run("stats/"+tc.name, func(t *testing.T) {
			rec := serveStats(t, parityPort{statsErr: tc.err}, "/api/stats")
			assertEnvelope(t, rec, tc)
		})
		t.Run("projects/"+tc.name, func(t *testing.T) {
			rec := serveProjects(t, parityPort{projectsErr: tc.err}, "/api/projects")
			assertEnvelope(t, rec, tc)
		})
	}
}

// TestAgentProjectsErrorMapping reproduces respondAgentOperationError
// (internal/platform/server/agent_http.go:388-394): authorization denial becomes
// the shared 403 envelope and any other failure collapses to 503
// agent_unavailable, never leaking the underlying error. ErrUnauthenticated maps
// to 401 to preserve the end-to-end middleware result.
func TestAgentProjectsErrorMapping(t *testing.T) {
	cases := []envelopeCase{
		{"unauthenticated", ErrUnauthenticated, http.StatusUnauthorized,
			`{"error":{"code":"unauthorized","message":"valid bearer token required"}}` + "\n"},
		{"forbidden", ErrForbidden, http.StatusForbidden,
			`{"error":{"code":"forbidden","message":"principal is not authorized for this operation"}}` + "\n"},
		{"not_found", domain.ErrNotFound, http.StatusServiceUnavailable,
			`{"error":{"code":"agent_unavailable","message":"project agent is unavailable"}}` + "\n"},
		{"invalid_input", domain.ErrInvalidInput, http.StatusServiceUnavailable,
			`{"error":{"code":"agent_unavailable","message":"project agent is unavailable"}}` + "\n"},
		{"operation_failed", errors.New("agent backend down"), http.StatusServiceUnavailable,
			`{"error":{"code":"agent_unavailable","message":"project agent is unavailable"}}` + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveAgentProjects(t, parityPort{agentErr: tc.err}, "/api/agent/projects")
			assertEnvelope(t, rec, tc)
		})
	}
}

func assertEnvelope(t *testing.T, rec *httptest.ResponseRecorder, tc envelopeCase) {
	t.Helper()
	if rec.Code != tc.wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
	}
	if got := rec.Body.String(); got != tc.wantBody {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, tc.wantBody)
	}
}
