package api

import "net/http"

// Stats serves GET /api/stats. The pre-extraction server handler
// (internal/platform/server/http.go:621-628) wrote the Operations result
// verbatim and ignored the query string, so the neutral handler serializes
// domain.ServerStats untouched and reads no parameters.
func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.port.ServerStats(r.Context())
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
