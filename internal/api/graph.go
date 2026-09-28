package api

import (
	"net/http"
	"strconv"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Query bounds for GET /api/graph/project-graph, mirroring the pre-extraction
// server traversal (internal/platform/server/http.go:44,892): depth defaults to
// 2 inside 1..10 and max_nodes to 100 inside 1..200, so an untrusted query
// string can never widen the traversal.
const (
	defaultGraphDepth    = 2
	minGraphDepth        = 1
	maxGraphDepth        = 10
	defaultGraphMaxNodes = 100
	minGraphMaxNodes     = 1
	maxGraphMaxNodes     = 200
)

// ProjectGraph serves GET /api/graph/project-graph. It parses the project
// selector plus the depth/max_nodes bounds, delegates the traversal to the Port
// (store-backed in server mode, BFS in local mode) and serializes the returned
// domain.GraphSubgraph unchanged.
//
// A nil subgraph with no error means "no graph for this project" (an unknown
// project, or a traversal whose per-node lookups all failed). The server handler
// tolerated those and still answered with the root-labelled empty envelope, so
// the same 200 shape is emitted here rather than a null body.
func (h *Handlers) ProjectGraph(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	project := query.Get("project")
	depth := boundedQueryInt(query.Get("depth"), defaultGraphDepth, minGraphDepth, maxGraphDepth)
	maxNodes := boundedQueryInt(query.Get("max_nodes"), defaultGraphMaxNodes, minGraphMaxNodes, maxGraphMaxNodes)

	subgraph, err := h.port.ProjectGraph(r.Context(), project, depth, maxNodes)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if subgraph == nil {
		subgraph = &domain.GraphSubgraph{Root: projectGraphRoot(project)}
	}
	writeJSON(w, http.StatusOK, subgraph)
}

func projectGraphRoot(project string) string {
	if project == "" {
		return "all_projects"
	}
	return project
}

// boundedQueryInt reproduces the server queryInt contract
// (internal/platform/server/http.go:1391): a missing, malformed or below-minimum
// value falls back to fallback while an above-maximum value clamps to max.
func boundedQueryInt(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}
