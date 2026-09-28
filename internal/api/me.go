package api

import "net/http"

// Me serves GET /api/me. The envelope reproduces the pre-extraction server
// response: the principalResponse fields (internal/platform/server/http.go:1526)
// plus the optional workspace_id, display_name and email keys the server me
// handler added (internal/platform/server/http.go:437-458).
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	principal, err := h.port.CurrentPrincipal(r.Context())
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, principalEnvelope(principal))
}

func principalEnvelope(p Principal) map[string]any {
	identity := p.Identity
	envelope := map[string]any{
		"id":                       identity.Subject,
		"type":                     identity.Type,
		"org_id":                   identity.OrgID,
		"workspaces":               identity.WorkspacesCopy(),
		"projects":                 identity.ProjectsCopy(),
		"roles":                    identity.RolesCopy(),
		"scopes":                   identity.ScopesCopy(),
		"classification_clearance": identity.ClassificationClearanceCopy(),
		"auth_method":              identity.AuthMethod,
	}
	if p.WorkspaceID != "" {
		envelope["workspace_id"] = p.WorkspaceID
	}
	// The server only consulted the user profile store for user principals
	// (http.go:447), so the guard is replicated here to keep the envelope
	// byte-identical regardless of what an adapter chooses to populate.
	if identity.Type == "user" && identity.Subject != "" {
		if p.DisplayName != "" {
			envelope["display_name"] = p.DisplayName
		}
		if p.Email != "" {
			envelope["email"] = p.Email
		}
	}
	return envelope
}
