package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// stubPort is a minimal in-memory Port: Me only exercises CurrentPrincipal, the
// remaining methods exist so the stub proves the interface shape is satisfiable
// without any store or server dependency.
type stubPort struct {
	current    Principal
	currentErr error
}

func (s stubPort) CurrentPrincipal(context.Context) (Principal, error) {
	return s.current, s.currentErr
}

func (s stubPort) ServerStats(context.Context) (*domain.ServerStats, error) { return nil, nil }

func (s stubPort) Projects(context.Context) ([]string, error) { return nil, nil }

func (s stubPort) AgentProjects(context.Context) (map[string]string, error) { return nil, nil }

func (s stubPort) ProjectGraph(context.Context, string, int, int) (*domain.GraphSubgraph, error) {
	return nil, nil
}

var _ Port = stubPort{}

func serveMe(t *testing.T, port Port) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	New(port).Me(rec, req)
	return rec
}

// TestMeGoldenEnvelopeMatchesServerSerialization pins the response bytes to the
// pre-extraction server envelope produced by principalResponse
// (internal/platform/server/http.go:1526-1527) plus the optional workspace_id,
// display_name and email enrichment applied by the server me handler
// (internal/platform/server/http.go:437-458). Key order is the deterministic
// encoding/json map ordering, so the assertion is byte-exact.
func TestMeGoldenEnvelopeMatchesServerSerialization(t *testing.T) {
	port := stubPort{current: Principal{
		Identity: domain.Principal{
			Subject:                 "user-1",
			Type:                    "user",
			OrgID:                   "org-1",
			WorkspaceIDs:            []string{"ws-1", "ws-2"},
			ProjectIDs:              []string{"proj-a"},
			Roles:                   []string{"owner"},
			Scopes:                  []string{"admin", "agent"},
			ClassificationClearance: []string{"internal"},
			AuthMethod:              "static",
		},
		WorkspaceID: "ws-1",
		DisplayName: "Ada Lovelace",
		Email:       "ada@example.com",
	}}

	rec := serveMe(t, port)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}
	const want = `{"auth_method":"static","classification_clearance":["internal"],` +
		`"display_name":"Ada Lovelace","email":"ada@example.com","id":"user-1",` +
		`"org_id":"org-1","projects":["proj-a"],"roles":["owner"],` +
		`"scopes":["admin","agent"],"type":"user","workspace_id":"ws-1",` +
		`"workspaces":["ws-1","ws-2"]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMeGoldenEnvelopeWithoutPrincipalEnrichment pins the service-account shape:
// no workspace_id/display_name/email keys, and empty grants serialize as null
// because the domain copy helpers return a nil slice for empty input — the
// server emitted exactly these bytes before extraction.
func TestMeGoldenEnvelopeWithoutPrincipalEnrichment(t *testing.T) {
	port := stubPort{current: Principal{Identity: domain.Principal{
		Subject:      "svc-1",
		Type:         "service_account",
		OrgID:        "org-1",
		WorkspaceIDs: []string{"ws-1"},
		ProjectIDs:   []string{"*"},
		Scopes:       []string{"agent"},
		AuthMethod:   "api_key",
	}}}

	rec := serveMe(t, port)

	const want = `{"auth_method":"api_key","classification_clearance":null,"id":"svc-1",` +
		`"org_id":"org-1","projects":["*"],"roles":null,"scopes":["agent"],` +
		`"type":"service_account","workspaces":["ws-1"]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMeGoldenEnvelopeNonUserIgnoresProfileEnrichment pins the server guard
// (http.go:447): only user principals could receive display_name/email, so a
// service_account carrying profile data must not surface it.
func TestMeGoldenEnvelopeNonUserIgnoresProfileEnrichment(t *testing.T) {
	port := stubPort{current: Principal{
		Identity: domain.Principal{
			Subject:      "svc-1",
			Type:         "service_account",
			OrgID:        "org-1",
			WorkspaceIDs: []string{"ws-1"},
			ProjectIDs:   []string{"*"},
			Scopes:       []string{"agent"},
			AuthMethod:   "api_key",
		},
		DisplayName: "should not surface",
		Email:       "svc@example.com",
	}}

	rec := serveMe(t, port)

	const want = `{"auth_method":"api_key","classification_clearance":null,"id":"svc-1",` +
		`"org_id":"org-1","projects":["*"],"roles":null,"scopes":["agent"],` +
		`"type":"service_account","workspaces":["ws-1"]}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestMeUnauthenticatedUsesServerErrorEnvelope(t *testing.T) {
	rec := serveMe(t, stubPort{currentErr: ErrUnauthenticated})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got, want := rec.Header().Get("WWW-Authenticate"), `Bearer realm="cortex"`; got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
	const want = `{"error":{"code":"unauthorized","message":"valid bearer token required"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("error envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestMeOperationFailureEnvelope(t *testing.T) {
	rec := serveMe(t, stubPort{currentErr: errors.New("store unavailable")})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	const want = `{"error":{"code":"operation_failed","message":"operation failed"}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("error envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}
