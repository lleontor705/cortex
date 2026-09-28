// Package api exposes the HTTP handlers shared by the server mux
// (internal/platform/server) and the local serve mux (internal/http). It
// depends only on internal/domain and the standard library, so both muxes can
// import it without crossing the server-track architecture boundary
// (REQ-SH-010).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Sentinel errors a Port implementation returns to select a response contract.
// Adapters translate their own error vocabulary (missing credentials,
// authorization denials, store failures) into these so the emitted envelope
// stays identical on both muxes without this package importing their types.
var (
	// ErrUnauthenticated selects the 401 unauthorized envelope.
	ErrUnauthenticated = errors.New("api: unauthenticated")
	// ErrForbidden selects the 403 forbidden envelope.
	ErrForbidden = errors.New("api: forbidden")
)

// Principal is the neutral projection of the authenticated identity plus the
// optional enrichment the /api/me envelope may carry. Identity reuses
// domain.Principal so the base field serialization is byte-identical to the
// pre-extraction server response; WorkspaceID, DisplayName and Email stay empty
// when the backing mux has nothing to enrich.
type Principal struct {
	Identity    domain.Principal
	WorkspaceID string
	DisplayName string
	Email       string
}

// Port is the complete surface these handlers need from their host mux. Server
// mode backs it with the request-scoped Operations; local mode derives it from
// the SQLite bundle.
type Port interface {
	CurrentPrincipal(ctx context.Context) (Principal, error)
	ServerStats(ctx context.Context) (*domain.ServerStats, error)
	Projects(ctx context.Context) ([]string, error)
	AgentProjects(ctx context.Context) (map[string]string, error)
	ProjectGraph(ctx context.Context, projectID string, depth, maxNodes int) (*domain.GraphSubgraph, error)
}

// Handlers serves the parity endpoints over a single Port.
type Handlers struct {
	port Port
}

// New binds the handlers to a Port implementation.
func New(port Port) *Handlers {
	return &Handlers{port: port}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
}

func writeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeUnauthorized(w)
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "principal is not authorized for this operation")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, domain.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid operation input")
	default:
		writeError(w, http.StatusInternalServerError, "operation_failed", "operation failed")
	}
}
