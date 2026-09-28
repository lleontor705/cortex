package api

import (
	"errors"
	"net/http"
	"sort"
)

// Projects serves GET /api/projects. The pre-extraction server handler
// (internal/platform/server/http.go:657-664) wrote the ListProjects []string
// verbatim, so a nil list must still serialize as null.
func (h *Handlers) Projects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.port.Projects(r.Context())
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

// agentProject mirrors the server id/label pair
// (internal/platform/server/agent.go:59-62) so the JSON shape is identical.
type agentProject struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AgentProjects serves GET /api/agent/projects. It reproduces the server
// handler (internal/platform/server/agent_http.go:34-52): no-store headers,
// the project map flattened to {id,label} entries sorted by label then id, all
// wrapped under a single "projects" key.
func (h *Handlers) AgentProjects(w http.ResponseWriter, r *http.Request) {
	setAgentNoStore(w)
	projects, err := h.port.AgentProjects(r.Context())
	if err != nil {
		writeAgentOperationError(w, err)
		return
	}
	result := make([]agentProject, 0, len(projects))
	for id, label := range projects {
		result = append(result, agentProject{ID: id, Label: label})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Label != result[j].Label {
			return result[i].Label < result[j].Label
		}
		return result[i].ID < result[j].ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"projects": result})
}

func setAgentNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

// writeAgentOperationError reproduces respondAgentOperationError
// (internal/platform/server/agent_http.go:388-394): authorization denial yields
// the shared 403 envelope, every other failure collapses to 503
// agent_unavailable without leaking the underlying error. ErrUnauthenticated is
// translated to the 401 envelope the server middleware would have produced
// before the request reached the handler.
func writeAgentOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeUnauthorized(w)
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "principal is not authorized for this operation")
	default:
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "project agent is unavailable")
	}
}
