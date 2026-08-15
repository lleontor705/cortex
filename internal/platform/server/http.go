package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/extraction"
	"github.com/lleontor705/cortex/internal/identity"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	maxRequestBody = 1 << 20
	defaultLimit   = 20
	absoluteLimit  = 100
	maxGraphDepth  = 10
)

// Operations is the operation-only server boundary implemented by
// postgres.AuthorizedStore. It deliberately contains no repository accessors.
type Operations interface {
	SaveObservation(context.Context, *domain.Observation) error
	GetObservationByID(context.Context, int64) (*domain.Observation, error)
	GetObservationByPublicID(context.Context, string) (*domain.Observation, error)
	UpdateObservation(context.Context, *domain.Observation) error
	DeleteObservation(context.Context, int64) error
	CreateSession(context.Context, *domain.Session) error
	ListSessions(context.Context, string) ([]*domain.Session, error)
	GetServerStats(context.Context) (*domain.ServerStats, error)
	ListAuditEvents(context.Context, int) ([]*domain.AuditEntry, error)
	ListProjects(context.Context) ([]string, error)
	ListObservations(context.Context, domain.ObservationFilter) ([]*domain.Observation, error)
	SearchObservations(context.Context, string, domain.SearchOptions) ([]*domain.SearchResult, error)
	CreateGraphEdge(context.Context, *domain.Edge) error
	GetGraphEdgeByPublicID(context.Context, string) (*domain.Edge, error)
	GetRelatedObservations(context.Context, int64, int) ([]*domain.Observation, error)
	GetGraphSubgraph(context.Context, string, int, int) (*domain.GraphSubgraph, error)
	DeleteGraphEdge(context.Context, int64) error
	GetImportanceScore(context.Context, int64) (*domain.ImportanceScore, error)
	CreateUser(context.Context, identity.UserCreate) (identity.UserRecord, error)
	ListUsers(context.Context) ([]identity.UserRecord, error)
	SetUserActive(context.Context, string, bool) error
	IssueToken(context.Context, identity.TokenIssue) (identity.IssuedToken, error)
	ListTokens(context.Context) ([]identity.TokenRecord, error)
	RevokeToken(context.Context, string) error
	RotateToken(context.Context, string) (identity.IssuedToken, error)
	PushSync(context.Context, *domain.SyncBatch) (*domain.SyncResult, error)
	PullSync(context.Context, int64, int) (*domain.SyncPage, error)
}

type healthCheck func(context.Context) error

func newHTTPHandler(cfg config.Config, ops Operations, health healthCheck) (http.Handler, *mcpserver.StreamableHTTPServer) {
	return newHTTPHandlerWithAuth(cfg, ops, health, func(next http.Handler) http.Handler { return bearerAuth(cfg.HTTP.Token, next) })
}

func newHTTPHandlerWithAuth(cfg config.Config, ops Operations, health healthCheck, protect func(http.Handler) http.Handler) (http.Handler, *mcpserver.StreamableHTTPServer) {
	mcpCore := newServerMCP(ops)
	transport := mcpserver.NewStreamableHTTPServer(mcpCore)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := health(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "unhealthy", "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	api := &apiHandler{
		ops:          ops,
		defaultLimit: boundedDefault(cfg.Search.DefaultLimit),
		maxLimit:     boundedMax(cfg.Search.MaxLimit),
		extractor:    extraction.NewService(extraction.Config{}),
	}
	mux.Handle("/api/", protect(api.routes()))
	mux.Handle("/mcp", protect(transport))
	return corsHandler(cfg.HTTP.AllowedOrigins, mux), transport
}

func corsHandler(allowed []string, next http.Handler) http.Handler {
	allow := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		if origin = strings.TrimSpace(origin); origin != "" {
			allow[origin] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, ok := allow[origin]; !ok || origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type apiHandler struct {
	ops          Operations
	defaultLimit int
	maxLimit     int
	extractor    *extraction.Service
}

func (a *apiHandler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/observations", a.listObservations)
	mux.HandleFunc("POST /api/observations", a.saveObservation)
	mux.HandleFunc("POST /api/sessions", a.createSession)
	mux.HandleFunc("GET /api/sessions", a.listSessions)
	mux.HandleFunc("GET /api/stats", a.stats)
	mux.HandleFunc("GET /api/audit", a.audit)
	mux.HandleFunc("GET /api/projects", a.projects)
	mux.HandleFunc("GET /api/me", a.me)
	mux.HandleFunc("GET /api/admin/users", a.listUsers)
	mux.HandleFunc("POST /api/admin/users", a.createUser)
	mux.HandleFunc("POST /api/admin/users/{id}/enable", a.enableUser)
	mux.HandleFunc("POST /api/admin/users/{id}/disable", a.disableUser)
	mux.HandleFunc("GET /api/admin/tokens", a.listTokens)
	mux.HandleFunc("POST /api/admin/tokens", a.issueToken)
	mux.HandleFunc("POST /api/admin/tokens/{id}/rotate", a.rotateToken)
	mux.HandleFunc("DELETE /api/admin/tokens/{id}", a.revokeToken)
	mux.HandleFunc("GET /api/observations/{id}", a.getObservation)
	mux.HandleFunc("PUT /api/observations/{id}", a.updateObservation)
	mux.HandleFunc("DELETE /api/observations/{id}", a.deleteObservation)
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("POST /api/graph/edges", a.createEdge)
	mux.HandleFunc("DELETE /api/graph/edges/{id}", a.deleteEdge)
	mux.HandleFunc("GET /api/graph/{id}/related", a.related)
	mux.HandleFunc("GET /api/graph/{id}/subgraph", a.subgraph)
	mux.HandleFunc("POST /api/graph/resolve", a.resolveConflict)
	mux.HandleFunc("GET /api/scores/{id}", a.score)
	mux.HandleFunc("POST /api/extract", a.extract)
	mux.HandleFunc("POST /api/synthesize", a.synthesize)
	mux.HandleFunc("POST /api/sync/push", a.pushSync)
	mux.HandleFunc("GET /api/sync/changes", a.pullSync)
	return mux
}

func (a *apiHandler) pushSync(w http.ResponseWriter, r *http.Request) {
	var batch domain.SyncBatch
	if !decodeBody(w, r, &batch) {
		return
	}
	result, err := a.ops.PushSync(r.Context(), &batch)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) pullSync(w http.ResponseWriter, r *http.Request) {
	cursor, err := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if err != nil && r.URL.Query().Get("cursor") != "" {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a non-negative integer")
		return
	}
	limit := queryInt(r.URL.Query().Get("limit"), a.defaultLimit, 1, a.maxLimit)
	page, err := a.ops.PullSync(r.Context(), cursor, limit)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *apiHandler) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}
	writeJSON(w, http.StatusOK, principalResponse(principal))
}

func (a *apiHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string   `json:"email"`
		DisplayName string   `json:"display_name"`
		Roles       []string `json:"roles"`
		Workspaces  []string `json:"workspaces"`
		Projects    []string `json:"projects"`
		Scopes      []string `json:"scopes"`
		Clearance   []string `json:"classification_clearance"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	principal, _ := principalFromContext(r.Context())
	if len(input.Workspaces) == 0 {
		input.Workspaces = principal.WorkspacesCopy()
	}
	if hasString(input.Roles, string(authz.RoleOwner)) || hasString(input.Roles, string(authz.RoleAdmin)) {
		if len(input.Projects) == 0 {
			input.Projects = []string{"*"}
		}
		if len(input.Clearance) == 0 {
			input.Clearance = []string{"*"}
		}
	}
	user, err := a.ops.CreateUser(r.Context(), identity.UserCreate{Email: input.Email, DisplayName: input.DisplayName, Roles: input.Roles, Workspaces: input.Workspaces, Projects: input.Projects, Scopes: input.Scopes, ClassificationClearance: input.Clearance})
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, userResponse(user))
}

func (a *apiHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.ops.ListUsers(r.Context())
	if err != nil {
		respondOperationError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, user := range users {
		out = append(out, userResponse(user))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *apiHandler) enableUser(w http.ResponseWriter, r *http.Request) {
	a.setUserActive(w, r, true)
}

func (a *apiHandler) disableUser(w http.ResponseWriter, r *http.Request) {
	a.setUserActive(w, r, false)
}

func (a *apiHandler) setUserActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	if err := a.ops.SetUserActive(r.Context(), id, active); err != nil {
		respondOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiHandler) issueToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Subject    string     `json:"subject"`
		Name       string     `json:"name"`
		Scopes     []string   `json:"scopes"`
		Workspaces []string   `json:"workspaces"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(input.Subject)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid operation input")
		return
	}
	principal, _ := principalFromContext(r.Context())
	if len(input.Workspaces) == 0 {
		input.Workspaces = principal.WorkspacesCopy()
	}
	var expires time.Time
	if input.ExpiresAt != nil {
		expires = *input.ExpiresAt
	}
	issued, err := a.ops.IssueToken(r.Context(), identity.TokenIssue{Subject: input.Subject, PrincipalType: "user", OrgID: principal.OrgID, Name: input.Name, Workspaces: input.Workspaces, Scopes: input.Scopes, ExpiresAt: expires})
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, issuedTokenResponse(issued))
}

func (a *apiHandler) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := a.ops.ListTokens(r.Context())
	if err != nil {
		respondOperationError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, tokenResponse(token))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *apiHandler) rotateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	issued, err := a.ops.RotateToken(r.Context(), id)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, issuedTokenResponse(issued))
}

func (a *apiHandler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	if err := a.ops.RevokeToken(r.Context(), id); err != nil {
		respondOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiHandler) createSession(w http.ResponseWriter, r *http.Request) {
	var session domain.Session
	if !decodeBody(w, r, &session) {
		return
	}
	if err := a.ops.CreateSession(r.Context(), &session); err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (a *apiHandler) listSessions(w http.ResponseWriter, r *http.Request) {
	result, err := a.ops.ListSessions(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) stats(w http.ResponseWriter, r *http.Request) {
	result, err := a.ops.GetServerStats(r.Context())
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) audit(w http.ResponseWriter, r *http.Request) {
	result, err := a.ops.ListAuditEvents(r.Context(), queryInt(r.URL.Query().Get("limit"), 50, 1, 100))
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) projects(w http.ResponseWriter, r *http.Request) {
	result, err := a.ops.ListProjects(r.Context())
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) listObservations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.ObservationFilter{Project: q.Get("project"), Scope: q.Get("scope"), Type: q.Get("type"), Source: q.Get("source"), SessionID: q.Get("session_id")}
	f.Limit = queryInt(q.Get("limit"), a.defaultLimit, 1, a.maxLimit)
	f.Offset = queryInt(q.Get("offset"), 0, 0, 1_000_000)
	result, err := a.ops.ListObservations(r.Context(), f)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observationListResponse(result))
}

func (a *apiHandler) saveObservation(w http.ResponseWriter, r *http.Request) {
	var observation domain.Observation
	if !decodeBody(w, r, &observation) {
		return
	}
	if strings.TrimSpace(observation.SessionID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "session_id is required")
		return
	}
	if err := a.ops.SaveObservation(r.Context(), &observation); err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, observationResponse(&observation))
}

func (a *apiHandler) getObservation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	result, err := a.ops.GetObservationByPublicID(r.Context(), id)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observationResponse(result))
}

func (a *apiHandler) updateObservation(w http.ResponseWriter, r *http.Request) {
	publicID, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	var observation domain.Observation
	if !decodeBody(w, r, &observation) {
		return
	}
	current, err := a.ops.GetObservationByPublicID(r.Context(), publicID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	observation.ID = current.ID
	observation.PublicID = current.PublicID
	if err := a.ops.UpdateObservation(r.Context(), &observation); err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observationResponse(&observation))
}

func (a *apiHandler) deleteObservation(w http.ResponseWriter, r *http.Request) {
	publicID, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	observation, err := a.ops.GetObservationByPublicID(r.Context(), publicID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	if err := a.ops.DeleteObservation(r.Context(), observation.ID); err != nil {
		respondOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiHandler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "q is required")
		return
	}
	opts := domain.SearchOptions{Query: query, Type: q.Get("type"), Project: q.Get("project"), Scope: q.Get("scope"), Limit: queryInt(q.Get("limit"), a.defaultLimit, 1, a.maxLimit)}
	result, err := a.ops.SearchObservations(r.Context(), query, opts)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, searchResponse(result))
}

func (a *apiHandler) createEdge(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FromID       string `json:"from_id"`
		ToID         string `json:"to_id"`
		RelationType string `json:"relation_type"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	from, err := a.ops.GetObservationByPublicID(r.Context(), input.FromID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	to, err := a.ops.GetObservationByPublicID(r.Context(), input.ToID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	edge := domain.Edge{FromObsID: from.ID, ToObsID: to.ID, FromPublicID: from.PublicID, ToPublicID: to.PublicID, RelationType: input.RelationType}
	if err := a.ops.CreateGraphEdge(r.Context(), &edge); err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, edgeResponse(&edge))
}

func (a *apiHandler) related(w http.ResponseWriter, r *http.Request) {
	publicID, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	depth := queryInt(r.URL.Query().Get("depth"), 1, 1, maxGraphDepth)
	observation, err := a.ops.GetObservationByPublicID(r.Context(), publicID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	result, err := a.ops.GetRelatedObservations(r.Context(), observation.ID, depth)
	if len(result) > a.maxLimit {
		result = result[:a.maxLimit]
	}
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observationListResponse(result))
}

func (a *apiHandler) subgraph(w http.ResponseWriter, r *http.Request) {
	publicID, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	result, err := a.ops.GetGraphSubgraph(r.Context(), publicID, queryInt(r.URL.Query().Get("depth"), 2, 1, maxGraphDepth), queryInt(r.URL.Query().Get("max_nodes"), 100, 1, 200))
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) deleteEdge(w http.ResponseWriter, r *http.Request) {
	publicID, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	edge, err := a.ops.GetGraphEdgeByPublicID(r.Context(), publicID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	if err := a.ops.DeleteGraphEdge(r.Context(), edge.ID); err != nil {
		respondOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiHandler) score(w http.ResponseWriter, r *http.Request) {
	publicID, ok := pathPublicID(w, r)
	if !ok {
		return
	}
	observation, err := a.ops.GetObservationByPublicID(r.Context(), publicID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	result, err := a.ops.GetImportanceScore(r.Context(), observation.ID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"observation_id": observation.PublicID, "score": result.Score, "access_count": result.AccessCount, "last_accessed": result.LastAccessed, "updated_at": result.UpdatedAt})
}

func (a *apiHandler) extract(w http.ResponseWriter, r *http.Request) {
	var req extraction.ExtractionRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := a.extractor.Extract(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "extraction_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) synthesize(w http.ResponseWriter, r *http.Request) {
	var req extraction.SynthesisRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := a.extractor.Synthesize(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "synthesis_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) resolveConflict(w http.ResponseWriter, r *http.Request) {
	var input struct {
		NewObsID      string `json:"new_observation_id"`
		ObsoleteObsID string `json:"obsolete_observation_id"`
		Reason        string `json:"reason"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	newObs, err := a.ops.GetObservationByPublicID(r.Context(), input.NewObsID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	obsoleteObs, err := a.ops.GetObservationByPublicID(r.Context(), input.ObsoleteObsID)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	edge := &domain.Edge{
		FromObsID:    newObs.ID,
		ToObsID:      obsoleteObs.ID,
		FromPublicID: newObs.PublicID,
		ToPublicID:   obsoleteObs.PublicID,
		RelationType: domain.RelationSupersedes,
		Weight:       1.0,
		Confidence:   1.0,
		Reasoning:    input.Reason,
		ChangeReason: input.Reason,
	}
	if err := a.ops.CreateGraphEdge(r.Context(), edge); err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, edgeResponse(edge))
}

func bearerAuth(token string, next http.Handler) http.Handler {
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("Authorization")
		if token == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}

func pathPublicID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a UUID")
		return "", false
	}
	return id, true
}

func queryInt(raw string, fallback, min, max int) int {
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

func boundedDefault(limit int) int {
	if limit <= 0 || limit > absoluteLimit {
		return defaultLimit
	}
	return limit
}

func boundedMax(limit int) int {
	if limit <= 0 || limit > absoluteLimit {
		return absoluteLimit
	}
	return limit
}

func respondOperationError(w http.ResponseWriter, err error) {
	switch {
	case isAuthorizationDenial(err):
		writeError(w, http.StatusForbidden, "forbidden", "principal is not authorized for this operation")
	case errors.Is(err, domain.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid operation input")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	default:
		writeError(w, http.StatusInternalServerError, "operation_failed", "operation failed")
	}
}

func isAuthorizationDenial(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, authz.ErrForbidden) {
		return true
	}
	switch err.Error() {
	case authz.DenyRole, authz.DenyScope, authz.DenyTenantMismatch, authz.DenyWorkspace, authz.DenyProject, authz.DenyOwnership, authz.DenyClassification:
		return true
	default:
		return false
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func searchResponse(results []*domain.SearchResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		item := map[string]any{
			"id": result.PublicID, "title": result.Title, "content": result.Content,
			"type": result.Type, "project": result.Project, "scope": result.Scope,
			"topic_key":  result.TopicKey,
			"confidence": result.Confidence, "source": result.Source,
			"created_at": result.CreatedAt, "updated_at": result.UpdatedAt,
			"rank": result.Rank, "score_breakdown": result.ScoreBreakdown,
		}
		if _, err := uuid.Parse(result.SessionID); err == nil {
			item["session_id"] = result.SessionID
		}
		out = append(out, item)
	}
	return out
}

func observationResponse(observation *domain.Observation) map[string]any {
	if observation == nil {
		return nil
	}
	out := map[string]any{
		"id": observation.PublicID, "title": observation.Title,
		"content": observation.Content, "type": observation.Type,
		"project": observation.Project, "scope": observation.Scope,
		"topic_key": observation.TopicKey, "confidence": observation.Confidence,
		"source": observation.Source, "created_at": observation.CreatedAt,
		"updated_at": observation.UpdatedAt,
	}
	if _, err := uuid.Parse(observation.SessionID); err == nil {
		out["session_id"] = observation.SessionID
	}
	return out
}

func observationListResponse(observations []*domain.Observation) []map[string]any {
	out := make([]map[string]any, 0, len(observations))
	for _, observation := range observations {
		if observation != nil {
			out = append(out, observationResponse(observation))
		}
	}
	return out
}

func principalResponse(principal domain.Principal) map[string]any {
	return map[string]any{"id": principal.Subject, "type": principal.Type, "org_id": principal.OrgID, "workspaces": principal.WorkspacesCopy(), "projects": principal.ProjectsCopy(), "roles": principal.RolesCopy(), "scopes": principal.ScopesCopy(), "classification_clearance": principal.ClassificationClearanceCopy(), "auth_method": principal.AuthMethod}
}

func userResponse(user identity.UserRecord) map[string]any {
	return map[string]any{"id": user.ID, "email": user.Email, "display_name": user.DisplayName, "active": user.Active, "roles": user.Roles, "workspaces": user.Workspaces, "projects": user.Projects, "scopes": user.Scopes, "classification_clearance": user.ClassificationClearance, "grant_version": user.GrantVersion, "created_at": user.CreatedAt}
}

func tokenResponse(token identity.TokenRecord) map[string]any {
	return map[string]any{"id": token.ID, "name": token.Name, "prefix": token.Prefix, "subject": token.Subject, "principal_type": token.PrincipalType, "scopes": token.Scopes, "workspaces": token.Workspaces, "expires_at": nullableAPITime(token.ExpiresAt), "revoked_at": nullableAPITime(token.RevokedAt), "last_used_at": nullableAPITime(token.LastUsedAt)}
}

func nullableAPITime(value time.Time) any {
	if value.IsZero() || value.Unix() <= 0 {
		return nil
	}
	return value
}

func issuedTokenResponse(issued identity.IssuedToken) map[string]any {
	response := tokenResponse(issued.Record)
	response["secret"] = issued.Secret
	return response
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func edgeResponse(edge *domain.Edge) map[string]any {
	if edge == nil {
		return nil
	}
	return map[string]any{
		"id": edge.PublicID, "from_id": edge.FromPublicID, "to_id": edge.ToPublicID,
		"relation_type": edge.RelationType, "weight": edge.Weight, "confidence": edge.Confidence,
		"source": edge.Source, "reasoning": edge.Reasoning, "valid_from": edge.ValidFrom,
		"invalid_at": edge.InvalidAt, "valid_until": edge.ValidUntil, "tx_from": edge.TxFrom,
		"tx_until": edge.TxUntil, "created_at": edge.CreatedAt,
		"evolution_type": edge.EvolutionType, "fact_state": edge.FactState, "change_reason": edge.ChangeReason,
	}
}

func listenAddress(cfg config.HTTPConfig) string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

// Address is the configured HTTP listen address.
func (r *Runtime) Address() string {
	if r == nil || r.httpServer == nil {
		return ""
	}
	return r.httpServer.Addr
}

// Serve listens until ctx is canceled or the HTTP server fails.
func (r *Runtime) Serve(ctx context.Context) error {
	if r == nil || r.httpServer == nil {
		return errors.New("server: HTTP runtime is not initialized")
	}
	listener, err := net.Listen("tcp", r.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", r.httpServer.Addr, err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.httpServer.Serve(listener) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.shutdownTransport(shutdownCtx)
		if err := r.httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func newServerMCP(ops Operations) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer("cortex-server", "2.0.0", mcpserver.WithToolCapabilities(true))
	registerServerTools(srv, ops)
	return srv
}

func registerServerTools(srv *mcpserver.MCPServer, ops Operations) {
	add := func(tool mcp.Tool, handler mcpserver.ToolHandlerFunc) { srv.AddTool(tool, handler) }
	add(mcp.NewTool("cortex_save", mcp.WithDescription("Save an observation."), mcp.WithString("title", mcp.Required()), mcp.WithString("content", mcp.Required()), mcp.WithString("type"), mcp.WithString("session_id", mcp.Required()), mcp.WithString("project"), mcp.WithString("scope"), mcp.WithString("topic_key"), mcp.WithString("source")), saveTool(ops))
	add(mcp.NewTool("cortex_session_start", mcp.WithDescription("Start a memory session."), mcp.WithString("project"), mcp.WithString("summary")), sessionStartTool(ops))
	add(mcp.NewTool("cortex_search", mcp.WithDescription("Search observations."), mcp.WithString("query", mcp.Required()), mcp.WithString("type"), mcp.WithString("project"), mcp.WithString("scope"), mcp.WithNumber("limit")), searchTool(ops))
	add(mcp.NewTool("cortex_get_observation", mcp.WithDescription("Get an observation by public UUID."), mcp.WithString("id", mcp.Required())), getTool(ops))
	add(mcp.NewTool("cortex_update", mcp.WithDescription("Update fields on an observation."), mcp.WithString("id", mcp.Required()), mcp.WithString("title"), mcp.WithString("content"), mcp.WithString("type"), mcp.WithString("project"), mcp.WithString("scope"), mcp.WithString("topic_key")), updateTool(ops))
	add(mcp.NewTool("cortex_delete", mcp.WithDescription("Delete an observation."), mcp.WithString("id", mcp.Required())), deleteTool(ops))
	add(mcp.NewTool("cortex_relate", mcp.WithDescription("Create a graph relationship with provenance metadata."), mcp.WithString("from_id", mcp.Required()), mcp.WithString("to_id", mcp.Required()), mcp.WithString("relation_type", mcp.Required()), mcp.WithNumber("weight"), mcp.WithNumber("confidence"), mcp.WithString("source"), mcp.WithString("reasoning")), relateTool(ops))
	add(mcp.NewTool("cortex_graph", mcp.WithDescription("Get related observations."), mcp.WithString("observation_id", mcp.Required()), mcp.WithNumber("depth")), graphTool(ops))
	add(mcp.NewTool("cortex_graph_subgraph", mcp.WithDescription("Get a bounded heterogeneous graph containing observations, entities, actors, sessions, and projects."), mcp.WithString("observation_id", mcp.Required()), mcp.WithNumber("depth"), mcp.WithNumber("max_nodes")), graphSubgraphTool(ops))
	add(mcp.NewTool("cortex_score", mcp.WithDescription("Get an observation importance score."), mcp.WithString("observation_id", mcp.Required())), scoreTool(ops))
}

func sessionStartTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		s := &domain.Session{Project: toolString(req, "project"), Summary: toolString(req, "summary"), StartedAt: time.Now().UTC()}
		err := ops.CreateSession(ctx, s)
		return toolResult(s, err)
	}
}

func toolArgs(req mcp.CallToolRequest) map[string]any { return req.GetArguments() }
func toolString(req mcp.CallToolRequest, key string) string {
	v, _ := toolArgs(req)[key].(string)
	return v
}
func toolInt(req mcp.CallToolRequest, key string, fallback int) int {
	v, ok := toolArgs(req)[key].(float64)
	if !ok {
		return fallback
	}
	return int(v)
}
func toolFloat(req mcp.CallToolRequest, key string, fallback float64) float64 {
	v, ok := toolArgs(req)[key].(float64)
	if !ok {
		return fallback
	}
	return v
}
func toolResult(value any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError("operation failed"), nil
	}
	b, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, marshalErr
	}
	return mcp.NewToolResultText(string(b)), nil
}

func saveTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		o := &domain.Observation{Title: toolString(req, "title"), Content: toolString(req, "content"), Type: toolString(req, "type"), SessionID: toolString(req, "session_id"), Project: toolString(req, "project"), Scope: toolString(req, "scope"), TopicKey: toolString(req, "topic_key"), Source: toolString(req, "source")}
		err := ops.SaveObservation(ctx, o)
		return toolResult(observationResponse(o), err)
	}
}
func searchTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q := toolString(req, "query")
		opts := domain.SearchOptions{Query: q, Type: toolString(req, "type"), Project: toolString(req, "project"), Scope: toolString(req, "scope"), Limit: queryInt(strconv.Itoa(toolInt(req, "limit", defaultLimit)), defaultLimit, 1, absoluteLimit)}
		v, err := ops.SearchObservations(ctx, q, opts)
		if err != nil {
			return toolResult(nil, err)
		}
		return toolResult(searchResponse(v), nil)
	}
}
func getTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		v, err := ops.GetObservationByPublicID(ctx, toolString(req, "id"))
		return toolResult(observationResponse(v), err)
	}
}
func updateTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		o, err := ops.GetObservationByPublicID(ctx, toolString(req, "id"))
		if err != nil {
			return toolResult(nil, err)
		}
		for k, set := range map[string]func(string){"title": func(v string) { o.Title = v }, "content": func(v string) { o.Content = v }, "type": func(v string) { o.Type = v }, "project": func(v string) { o.Project = v }, "scope": func(v string) { o.Scope = v }, "topic_key": func(v string) { o.TopicKey = v }} {
			if v, ok := toolArgs(req)[k].(string); ok {
				set(v)
			}
		}
		err = ops.UpdateObservation(ctx, o)
		return toolResult(observationResponse(o), err)
	}
}
func deleteTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		o, err := ops.GetObservationByPublicID(ctx, toolString(req, "id"))
		if err == nil {
			err = ops.DeleteObservation(ctx, o.ID)
		}
		return toolResult(map[string]bool{"deleted": err == nil}, err)
	}
}
func relateTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromID, toID, relation := toolString(req, "from_id"), toolString(req, "to_id"), toolString(req, "relation_type")
		weight, confidence := toolFloat(req, "weight", 1), toolFloat(req, "confidence", 1)
		if fromID == "" || toID == "" || relation == "" {
			return mcp.NewToolResultError("from_id, to_id, and relation_type are required"), nil
		}
		if weight < 0 || weight > 10 {
			return mcp.NewToolResultError("weight must be between 0.0 and 10.0"), nil
		}
		if confidence < 0 || confidence > 1 {
			return mcp.NewToolResultError("confidence must be between 0.0 and 1.0"), nil
		}
		from, err := ops.GetObservationByPublicID(ctx, fromID)
		if err != nil {
			return toolResult(nil, err)
		}
		to, err := ops.GetObservationByPublicID(ctx, toID)
		if err != nil {
			return toolResult(nil, err)
		}
		e := &domain.Edge{FromObsID: from.ID, ToObsID: to.ID, FromPublicID: from.PublicID, ToPublicID: to.PublicID, RelationType: relation, Weight: weight, Confidence: confidence, Source: toolString(req, "source"), Reasoning: toolString(req, "reasoning")}
		err = ops.CreateGraphEdge(ctx, e)
		return toolResult(edgeResponse(e), err)
	}
}
func graphSubgraphTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		observationID := toolString(req, "observation_id")
		if observationID == "" {
			return mcp.NewToolResultError("observation_id is required"), nil
		}
		depth := queryInt(strconv.Itoa(toolInt(req, "depth", 2)), 2, 1, maxGraphDepth)
		maxNodes := queryInt(strconv.Itoa(toolInt(req, "max_nodes", 100)), 100, 1, 200)
		value, err := ops.GetGraphSubgraph(ctx, observationID, depth, maxNodes)
		return toolResult(value, err)
	}
}
func graphTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		o, err := ops.GetObservationByPublicID(ctx, toolString(req, "observation_id"))
		if err != nil {
			return toolResult(nil, err)
		}
		v, err := ops.GetRelatedObservations(ctx, o.ID, queryInt(strconv.Itoa(toolInt(req, "depth", 1)), 1, 1, maxGraphDepth))
		if len(v) > absoluteLimit {
			v = v[:absoluteLimit]
		}
		return toolResult(observationListResponse(v), err)
	}
}
func scoreTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		o, err := ops.GetObservationByPublicID(ctx, toolString(req, "observation_id"))
		if err != nil {
			return toolResult(nil, err)
		}
		v, err := ops.GetImportanceScore(ctx, o.ID)
		if err != nil {
			return toolResult(nil, err)
		}
		return toolResult(map[string]any{"observation_id": o.PublicID, "score": v.Score, "access_count": v.AccessCount, "last_accessed": v.LastAccessed, "updated_at": v.UpdatedAt}, nil)
	}
}
