package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/ast"
	"github.com/lleontor705/cortex/v2/internal/domain/extraction"
	"github.com/lleontor705/cortex/v2/internal/domain/graph"
	"github.com/lleontor705/cortex/v2/internal/identity"
	"github.com/lleontor705/cortex/v2/internal/mcp/memorycontract"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/util"
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
	SaveObservationWithEffect(context.Context, *domain.Observation) (domain.SaveEffect, error)
	ExecuteHandoff(context.Context, domain.HandoffRequest) (domain.ObservationWriteResult, error)
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
	GetUserProfile(context.Context, string) (*identity.UserRecord, error)
	SetUserActive(context.Context, string, bool) error
	IssueToken(context.Context, identity.TokenIssue) (identity.IssuedToken, error)
	ListTokens(context.Context) ([]identity.TokenRecord, error)
	RevokeToken(context.Context, string) error
	RotateToken(context.Context, string) (identity.IssuedToken, error)
	PushSync(context.Context, *domain.SyncBatch) (*domain.SyncResult, error)
	PullSync(context.Context, int64, int) (*domain.SyncPage, error)
	GetProjectContext(context.Context, string) (*domain.ProjectContext, error)
	ListProjectSkills(context.Context, string) ([]*domain.ProjectSkill, error)
	GetProjectSkill(context.Context, string, string) (*domain.ProjectSkill, error)
	SaveProjectArtifact(context.Context, domain.SaveProjectArtifactInput) (*domain.ProjectArtifactItem, error)
	ListProjectArtifacts(context.Context, string, string) ([]*domain.ProjectArtifactItem, error)
	DeleteProjectArtifact(context.Context, string, string) error
	GetProjectDuplicates(context.Context) ([]domain.ProjectDuplicateGroup, error)
	MergeProject(context.Context, string, string) (*domain.ProjectMergeResult, error)
}

type healthCheck func(context.Context) error

// Every authenticated route is wired through newHTTPHandlerWithAuth with a
// verifier-backed middleware (requestAuthenticator in production). There is
// deliberately no static-compare constructor: the configured bearer is a
// secret to verify through TokenPrincipalVerifier, never a comparison value.
//
// The optional extractor argument (SEC-02) injects a server-composed
// extraction service whose outbound destination policy and provider
// credentials come exclusively from administrator configuration. When
// omitted, the default service is heuristic-only: its strict policy approves
// no outbound destination.
func newHTTPHandlerWithAuth(cfg config.Config, ops Operations, health healthCheck, protect func(http.Handler) http.Handler, extractors ...*extraction.Service) (http.Handler, *mcpserver.StreamableHTTPServer) {
	mcpCore := newServerMCP(ops)
	sessions := newMCPSessionRegistry(mcpSessionLimits{
		IdleTTL:      mcpSessionIdleTTLDefault,
		AbsoluteTTL:  mcpSessionAbsoluteTTLDefault,
		PerPrincipal: mcpMaxSessionsPerPrincipal,
		Total:        mcpMaxSessionsTotal,
	})
	transport := mcpserver.NewStreamableHTTPServer(mcpCore,
		mcpserver.WithSessionIdManagerResolver(sessions),
		mcpserver.WithSessionIdleTTL(mcpSessionIdleTTLDefault),
		mcpserver.WithLogger(redactingLogger{next: util.DefaultLogger()}),
	)
	guard := newMCPGuardWithTools(newMCPAdmission(mcpAdmissionLimits{
		PerPrincipal: mcpPrincipalInflightBytes,
		Global:       mcpGlobalInflightBytes,
	}), sessions, func(name string) bool {
		return mcpCore.GetTool(name) != nil
	})
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

	extractor := extraction.NewServiceWithPolicy(extraction.Config{}, extraction.DefaultOutboundPolicy())
	if len(extractors) > 0 && extractors[0] != nil {
		extractor = extractors[0]
	}
	api := &apiHandler{
		ops:          ops,
		defaultLimit: boundedDefault(cfg.Search.DefaultLimit),
		maxLimit:     boundedMax(cfg.Search.MaxLimit),
		extractor:    extractor,
		cfg:          cfg,
	}
	mux.Handle("/api/", protect(api.routes()))
	mux.Handle("/mcp", protect(guard.wrap(transport)))
	return corsHandler(cfg.HTTP.AllowedOrigins, mux), transport
}

func corsHandler(allowed []string, next http.Handler) http.Handler {
	allowAll := len(allowed) == 0
	allow := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		if origin = strings.TrimSpace(origin); origin != "" {
			if origin == "*" {
				allowAll = true
			}
			allow[origin] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else if _, ok := allow[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type apiHandler struct {
	ops          Operations
	defaultLimit int
	maxLimit     int
	extractor    *extraction.Service
	cfg          config.Config
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
	mux.HandleFunc("GET /api/admin/ai/status", a.aiStatus)
	mux.HandleFunc("POST /api/admin/ai/test-llm", a.testLLM)
	mux.HandleFunc("POST /api/admin/ai/test-embedding", a.testEmbedding)
	mux.HandleFunc("GET /api/observations/{id}", a.getObservation)
	mux.HandleFunc("PUT /api/observations/{id}", a.updateObservation)
	mux.HandleFunc("DELETE /api/observations/{id}", a.deleteObservation)
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("POST /api/graph/edges", a.createEdge)
	mux.HandleFunc("DELETE /api/graph/edges/{id}", a.deleteEdge)
	mux.HandleFunc("GET /api/graph/analytics", a.graphAnalytics)
	mux.HandleFunc("GET /api/graph/blast-radius", a.graphBlastRadius)
	mux.HandleFunc("GET /api/graph/project-graph", a.projectGraph)
	mux.HandleFunc("POST /api/graph/ingest-code", a.ingestCode)
	mux.HandleFunc("GET /api/graph/{id}/related", a.related)
	mux.HandleFunc("GET /api/graph/{id}/subgraph", a.subgraph)
	mux.HandleFunc("POST /api/graph/resolve", a.resolveConflict)
	mux.HandleFunc("GET /api/scores/{id}", a.score)
	mux.HandleFunc("POST /api/extract", a.extract)
	mux.HandleFunc("POST /api/synthesize", a.synthesize)
	mux.HandleFunc("POST /api/sync/push", a.pushSync)
	mux.HandleFunc("GET /api/sync/changes", a.pullSync)
	mux.HandleFunc("GET /api/projects/context", a.getProjectContext)
	mux.HandleFunc("GET /api/projects/artifacts", a.listProjectArtifacts)
	mux.HandleFunc("POST /api/projects/artifacts", a.saveProjectArtifact)
	mux.HandleFunc("DELETE /api/projects/artifacts/{id}", a.deleteProjectArtifact)
	mux.HandleFunc("GET /api/projects/duplicates", a.getProjectDuplicates)
	mux.HandleFunc("POST /api/projects/merge", a.mergeProject)
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
	resp := principalResponse(principal)
	if principal.Type == "user" && principal.Subject != "" {
		if u, err := a.ops.GetUserProfile(r.Context(), principal.Subject); err == nil && u != nil {
			if u.DisplayName != "" {
				resp["display_name"] = u.DisplayName
			}
			if u.Email != "" {
				resp["email"] = u.Email
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
	if len(input.Projects) == 0 {
		input.Projects = []string{"*"}
	}
	if input.Clearance == nil {
		input.Clearance = []string{}
	}
	if len(input.Scopes) == 0 {
		if hasString(input.Roles, string(authz.RoleOwner)) || hasString(input.Roles, string(authz.RoleAdmin)) {
			input.Scopes = []string{"admin", "agent", "observations:read", "observations:write", "graph:read", "graph:write"}
		} else {
			input.Scopes = []string{"agent", "observations:read", "observations:write", "graph:read", "graph:write"}
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
	owner := q.Get("owner")
	if owner == "me" {
		principal, _ := principalFromContext(r.Context())
		f.OwnerSubject = principal.Subject
	} else if owner != "" {
		f.OwnerSubject = owner
	}
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

func (a *apiHandler) graphAnalytics(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit := queryInt(r.URL.Query().Get("limit"), 100, 1, 500)

	observations, err := a.ops.ListObservations(r.Context(), domain.ObservationFilter{
		Project: project,
		Limit:   limit,
	})
	if err != nil {
		respondOperationError(w, err)
		return
	}

	var nodes []graph.GraphAnalyticsNode
	var edges []graph.GraphAnalyticsEdge
	obsIDs := make(map[string]bool)

	for _, obs := range observations {
		nodeID := "observation:" + obs.PublicID
		obsIDs[nodeID] = true
		nodes = append(nodes, graph.GraphAnalyticsNode{
			ID:         nodeID,
			Label:      obs.Title,
			Kind:       "observation",
			Subtype:    obs.Type,
			SourceFile: obs.Source,
			Metadata: map[string]any{
				"project": obs.Project,
				"scope":   obs.Scope,
			},
		})
	}

	// Build edges from related subgraphs
	for _, obs := range observations {
		sub, err := a.ops.GetGraphSubgraph(r.Context(), obs.PublicID, 1, 50)
		if err == nil && sub != nil {
			for _, e := range sub.Edges {
				edges = append(edges, graph.GraphAnalyticsEdge{
					ID:         e.ID,
					Source:     e.Source,
					Target:     e.Target,
					Type:       e.Type,
					Weight:     e.Weight,
					Confidence: e.Confidence,
				})
			}
			for _, n := range sub.Nodes {
				if !obsIDs[n.ID] {
					obsIDs[n.ID] = true
					sourceFile, _ := n.Metadata["source"].(string)
					nodes = append(nodes, graph.GraphAnalyticsNode{
						ID:         n.ID,
						Label:      n.Label,
						Kind:       n.Kind,
						Subtype:    n.Subtype,
						SourceFile: sourceFile,
					})
				}
			}
		}
	}

	report := graph.AnalyzeGraph(nodes, edges)
	writeJSON(w, http.StatusOK, report)
}

func (a *apiHandler) graphBlastRadius(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_node_id", "node_id query parameter is required")
		return
	}
	depth := queryInt(r.URL.Query().Get("depth"), 3, 1, 10)

	// Fetch observations to build the local dependency graph
	observations, err := a.ops.ListObservations(r.Context(), domain.ObservationFilter{
		Limit: 200,
	})
	if err != nil {
		respondOperationError(w, err)
		return
	}

	var nodes []graph.GraphAnalyticsNode
	var edges []graph.GraphAnalyticsEdge
	nodeSet := make(map[string]bool)

	for _, obs := range observations {
		nid := "observation:" + obs.PublicID
		nodeSet[nid] = true
		nodes = append(nodes, graph.GraphAnalyticsNode{
			ID:         nid,
			Label:      obs.Title,
			Kind:       "observation",
			Subtype:    obs.Type,
			SourceFile: obs.Source,
		})

		sub, err := a.ops.GetGraphSubgraph(r.Context(), obs.PublicID, 1, 30)
		if err == nil && sub != nil {
			for _, e := range sub.Edges {
				edges = append(edges, graph.GraphAnalyticsEdge{
					ID:     e.ID,
					Source: e.Source,
					Target: e.Target,
					Type:   e.Type,
				})
			}
			for _, n := range sub.Nodes {
				if !nodeSet[n.ID] {
					nodeSet[n.ID] = true
					sourceFile, _ := n.Metadata["source"].(string)
					nodes = append(nodes, graph.GraphAnalyticsNode{
						ID:         n.ID,
						Label:      n.Label,
						Kind:       n.Kind,
						Subtype:    n.Subtype,
						SourceFile: sourceFile,
					})
				}
			}
		}
	}

	blast := graph.CalculateBlastRadius(nodeID, nodes, edges, depth)
	writeJSON(w, http.StatusOK, blast)
}

func (a *apiHandler) projectGraph(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit := queryInt(r.URL.Query().Get("limit"), 150, 1, 500)

	observations, err := a.ops.ListObservations(r.Context(), domain.ObservationFilter{
		Project: project,
		Limit:   limit,
	})
	if err != nil {
		respondOperationError(w, err)
		return
	}

	var nodes []domain.GraphNode
	var edges []domain.GraphLink
	nodeSet := make(map[string]bool)

	for _, obs := range observations {
		nid := "observation:" + obs.PublicID
		nodeSet[nid] = true
		nodes = append(nodes, domain.GraphNode{
			ID:      nid,
			Kind:    obs.Type,
			Label:   obs.Title,
			Project: obs.Project,
			Hop:     0,
			Metadata: map[string]any{
				"source": obs.Source,
				"scope":  obs.Scope,
			},
		})

		sub, err := a.ops.GetGraphSubgraph(r.Context(), obs.PublicID, 1, 30)
		if err == nil && sub != nil {
			edges = append(edges, sub.Edges...)
			for _, n := range sub.Nodes {
				if !nodeSet[n.ID] {
					nodeSet[n.ID] = true
					nodes = append(nodes, n)
				}
			}
		}
	}

	rootLabel := project
	if rootLabel == "" {
		rootLabel = "all_projects"
	}

	writeJSON(w, http.StatusOK, domain.GraphSubgraph{
		Root:  rootLabel,
		Nodes: nodes,
		Edges: edges,
	})
}

func processIngestCode(ctx context.Context, ops Operations, path string, project string, maxFiles int, persist bool) (map[string]any, error) {
	if path == "" {
		path = "."
	}
	if project == "" {
		project = "default"
	}

	extractor := ast.NewExtractor(path)
	res, err := extractor.ExtractPath(path, maxFiles)
	if err != nil {
		return nil, err
	}

	persistedCount := 0
	updatedCount := 0

	if persist {
		existingObs, _ := ops.ListObservations(ctx, domain.ObservationFilter{
			Project: project,
			Limit:   500,
		})
		existingMap := make(map[string]*domain.Observation)
		for _, o := range existingObs {
			key := fmt.Sprintf("%s|%s", o.Title, o.Source)
			existingMap[key] = o
		}

		entityObsMap := make(map[string]*domain.Observation)
		for _, ent := range res.Entities {
			title := fmt.Sprintf("[%s] %s", ent.Kind, ent.Name)
			key := fmt.Sprintf("%s|%s", title, ent.File)
			content := fmt.Sprintf("Source file: %s (line %d). Kind: %s. Package: %s", ent.File, ent.Line, ent.Kind, ent.Package)

			if existing, ok := existingMap[key]; ok {
				existing.Content = content
				existing.Source = ent.File
				if err := ops.UpdateObservation(ctx, existing); err == nil {
					entityObsMap[ent.ID] = existing
					updatedCount++
				}
			} else {
				obs := &domain.Observation{
					Project: project,
					Type:    "code_entity",
					Title:   title,
					Content: content,
					Source:  ent.File,
					Scope:   "project",
				}
				if err := ops.SaveObservation(ctx, obs); err == nil {
					entityObsMap[ent.ID] = obs
					existingMap[key] = obs
					persistedCount++
				}
			}
		}

		for _, rel := range res.Relationships {
			srcObs := entityObsMap[rel.Source]
			tgtObs := entityObsMap[rel.Target]
			if srcObs != nil && tgtObs != nil {
				edge := domain.Edge{
					FromObsID:    srcObs.ID,
					ToObsID:      tgtObs.ID,
					FromPublicID: srcObs.PublicID,
					ToPublicID:   tgtObs.PublicID,
					RelationType: rel.Relation,
					Confidence:   rel.Confidence,
					Reasoning:    rel.Reasoning,
				}
				_ = ops.CreateGraphEdge(ctx, &edge)
			}
		}
	}

	return map[string]any{
		"files_scanned":   res.FilesScanned,
		"entities_count":  len(res.Entities),
		"relations_count": len(res.Relationships),
		"persisted_count": persistedCount,
		"updated_count":   updatedCount,
		"project":         project,
		"entities":        res.Entities,
		"relationships":   res.Relationships,
	}, nil
}

func (a *apiHandler) ingestCode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path      string `json:"path"`
		Directory string `json:"directory"`
		Project   string `json:"project"`
		MaxFiles  int    `json:"max_files"`
		Persist   *bool  `json:"persist"`
	}
	if !decodeBody(w, r, &input) {
		return
	}

	targetPath := input.Path
	if targetPath == "" {
		targetPath = input.Directory
	}

	shouldPersist := true
	if input.Persist != nil {
		shouldPersist = *input.Persist
	}

	result, err := processIngestCode(r.Context(), a.ops, targetPath, input.Project, input.MaxFiles, shouldPersist)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ast_extraction_failed", err.Error())
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
	if requestLLMConfigRejection(w, req.LLMConfig) {
		return
	}
	req.LLMConfig = nil
	result, err := a.extractor.Extract(r.Context(), req)
	if err != nil {
		respondExtractionError(w, err, "extraction_failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *apiHandler) synthesize(w http.ResponseWriter, r *http.Request) {
	var req extraction.SynthesisRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if requestLLMConfigRejection(w, req.LLMConfig) {
		return
	}
	req.LLMConfig = nil
	result, err := a.extractor.Synthesize(r.Context(), req)
	if err != nil {
		respondExtractionError(w, err, "synthesis_failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// requestLLMConfigRejection enforces SEC-02 at the server boundary: a request
// body can never select the outbound destination or the credential. The
// llm_config field stays decodable for compatibility, but supplying a
// base_url or api_key is rejected with a stable invalid_configuration error
// before any outbound attempt. Benign remainder fields are ignored.
func requestLLMConfigRejection(w http.ResponseWriter, cfg *extraction.Config) bool {
	if cfg == nil || (cfg.BaseURL == "" && cfg.APIKey == "") {
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_configuration", "llm_config credentials cannot be supplied by request; provider destinations are server-managed")
	return true
}

// respondExtractionError maps extraction failures to bounded, stable public
// codes. Messages are static: upstream URLs, addresses, bodies, and
// credentials never appear in responses.
func respondExtractionError(w http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case errors.Is(err, extraction.ErrUnsafeDestination):
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "provider destination is not permitted by server policy")
	case errors.Is(err, extraction.ErrResponseTooLarge):
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "provider response exceeded configured limits")
	case errors.Is(err, extraction.ErrProviderRejected),
		errors.Is(err, extraction.ErrProviderUnavailable),
		errors.Is(err, extraction.ErrInvalidProviderResponse):
		writeError(w, http.StatusBadGateway, "provider_unavailable", "provider request failed")
	default:
		writeError(w, http.StatusBadRequest, fallbackCode, "request could not be processed")
	}
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
	id = strings.TrimPrefix(id, "observation:")
	id = strings.TrimPrefix(id, "session:")
	id = strings.TrimPrefix(id, "entity:")
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
		"owner_subject": observation.OwnerSubject,
		"topic_key":     observation.TopicKey, "confidence": observation.Confidence,
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
	add(mcp.NewTool(memorycontract.ToolSave,
		mcp.WithTitleAnnotation(memorycontract.SaveHints.Title),
		mcp.WithReadOnlyHintAnnotation(memorycontract.SaveHints.ReadOnly),
		mcp.WithDestructiveHintAnnotation(memorycontract.SaveHints.Destructive),
		mcp.WithIdempotentHintAnnotation(memorycontract.SaveHints.Idempotent),
		mcp.WithOpenWorldHintAnnotation(memorycontract.SaveHints.OpenWorld),
		mcp.WithDescription("Save an observation."),
		mcp.WithString("title", mcp.Required()),
		mcp.WithString("content", mcp.Required()),
		mcp.WithString("type"),
		mcp.WithString("session_id", mcp.Required()),
		mcp.WithString("project"),
		mcp.WithString("scope"),
		mcp.WithString("topic_key"),
		mcp.WithNumber("confidence"),
		mcp.WithString("tags"),
		mcp.WithString("source"),
		mcp.WithRawOutputSchema(memorycontract.WriteOutputSchemaJSON),
	), saveTool(ops))
	add(func() mcp.Tool {
		tool := mcp.NewTool(memorycontract.ToolHandoff,
			mcp.WithTitleAnnotation(memorycontract.HandoffHints.Title),
			mcp.WithReadOnlyHintAnnotation(memorycontract.HandoffHints.ReadOnly),
			mcp.WithDestructiveHintAnnotation(memorycontract.HandoffHints.Destructive),
			mcp.WithIdempotentHintAnnotation(memorycontract.HandoffHints.Idempotent),
			mcp.WithOpenWorldHintAnnotation(memorycontract.HandoffHints.OpenWorld),
			mcp.WithDescription(`Record a durable, idempotent handoff: persist an observation (and an optional relation) exactly once per idempotency key.

Replaying the identical payload under the same key returns the SAME observation with status "replayed"; the same key with a DIFFERENT payload is a conflict and mutates nothing. Any failure rolls back every effect — observation, relation, and receipt — atomically.

observation.session_id is optional on the server: the handoff resolves an existing workspace session or creates one inside the atomic unit.

The server namespace returns observation_ref.public_id only.`),
			mcp.WithRawInputSchema(memorycontract.HandoffInputSchemaJSON),
			mcp.WithRawOutputSchema(memorycontract.WriteOutputSchemaJSON))
		// mcp.NewTool seeds a structural InputSchema; a raw schema must replace
		// it or Tool.MarshalJSON rejects the tool (mcp-go schema conflict).
		tool.InputSchema = mcp.ToolInputSchema{}
		return tool
	}(), handoffTool(ops))
	add(mcp.NewTool("cortex_session_start", mcp.WithDescription("Start a memory session."), mcp.WithString("project"), mcp.WithString("summary")), sessionStartTool(ops))
	add(mcp.NewTool("cortex_search", mcp.WithDescription("Search observations."), mcp.WithString("query", mcp.Required()), mcp.WithString("type"), mcp.WithString("project"), mcp.WithString("scope"), mcp.WithNumber("limit")), searchTool(ops))
	add(mcp.NewTool("cortex_get_observation", mcp.WithDescription("Get an observation by public UUID."), mcp.WithString("id", mcp.Required())), getTool(ops))
	add(mcp.NewTool("cortex_update", mcp.WithDescription("Update fields on an observation."), mcp.WithString("id", mcp.Required()), mcp.WithString("title"), mcp.WithString("content"), mcp.WithString("type"), mcp.WithString("project"), mcp.WithString("scope"), mcp.WithString("topic_key")), updateTool(ops))
	add(mcp.NewTool("cortex_delete", mcp.WithDescription("Delete an observation."), mcp.WithString("id", mcp.Required())), deleteTool(ops))
	add(mcp.NewTool("cortex_relate", mcp.WithDescription("Create a graph relationship with provenance metadata."), mcp.WithString("from_id", mcp.Required()), mcp.WithString("to_id", mcp.Required()), mcp.WithString("relation_type", mcp.Required()), mcp.WithNumber("weight"), mcp.WithNumber("confidence"), mcp.WithString("source"), mcp.WithString("reasoning")), relateTool(ops))
	add(mcp.NewTool("cortex_graph", mcp.WithDescription("Get related observations."), mcp.WithString("observation_id", mcp.Required()), mcp.WithNumber("depth")), graphTool(ops))
	add(mcp.NewTool("cortex_graph_subgraph", mcp.WithDescription("Get a bounded heterogeneous graph containing observations, entities, actors, sessions, and projects."), mcp.WithString("observation_id", mcp.Required()), mcp.WithNumber("depth"), mcp.WithNumber("max_nodes")), graphSubgraphTool(ops))
	add(mcp.NewTool("cortex_get_blast_radius", mcp.WithDescription("Calculate the blast radius (impacted downstream symbols, callers, and files) when modifying a code entity or observation."), mcp.WithString("node_id", mcp.Required()), mcp.WithNumber("depth")), getBlastRadiusTool(ops))
	add(mcp.NewTool("cortex_analyze_architecture", mcp.WithDescription("Analyze knowledge and code graph architecture, detecting communities, god nodes, and surprising connections."), mcp.WithString("project")), analyzeArchitectureTool(ops))
	add(mcp.NewTool("cortex_detect_cycles", mcp.WithDescription("Detect circular dependencies and import cycles across code entities in the knowledge graph."), mcp.WithString("project")), detectCyclesTool(ops))
	add(mcp.NewTool("cortex_ingest_code", mcp.WithDescription("Scan, ingest or update codebase AST symbols and dependencies into a project knowledge graph. Supports whole repositories or single modified files during refactoring."), mcp.WithString("path"), mcp.WithString("project"), mcp.WithNumber("max_files")), ingestCodeTool(ops))
	add(mcp.NewTool("cortex_score", mcp.WithDescription("Get an observation importance score."), mcp.WithString("observation_id", mcp.Required())), scoreTool(ops))
	add(mcp.NewTool("cortex_get_project_context", mcp.WithDescription("Get corporate & project governance rules, system prompt, and available skills."), mcp.WithString("project")), getProjectContextTool(ops))
	add(mcp.NewTool("cortex_list_skills", mcp.WithDescription("List available corporate and project skills."), mcp.WithString("project")), listProjectSkillsTool(ops))
	add(mcp.NewTool("cortex_get_skill", mcp.WithDescription("Get full skill instructions, rules, and parameters by key."), mcp.WithString("key", mcp.Required()), mcp.WithString("project")), getProjectSkillTool(ops))
	add(mcp.NewTool("cortex_resolve_query", mcp.WithDescription("Intelligently resolve a query in Server mode (PostgreSQL RLS, corporate rules, project context, skills, and observations)."), mcp.WithString("query", mcp.Required()), mcp.WithString("project"), mcp.WithNumber("limit")), resolveQueryTool(ops))
	add(mcp.NewTool("cortex_get_status", mcp.WithDescription("Get the active operational mode (Server PostgreSQL), version, and capabilities.")), getStatusTool(ops))
}

func cleanProjectName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "/") || strings.Contains(raw, "\\") || strings.HasSuffix(raw, ".git") {
		raw = strings.TrimSuffix(raw, ".git")
		parts := strings.FieldsFunc(raw, func(r rune) bool {
			return r == '/' || r == '\\' || r == ':'
		})
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return filepath.Base(raw)
}

func toolProject(req mcp.CallToolRequest) string {
	if p := toolString(req, "project"); p != "" {
		return cleanProjectName(p)
	}
	if p := toolString(req, "folder_name"); p != "" {
		return cleanProjectName(p)
	}
	if p := toolString(req, "folder"); p != "" {
		return cleanProjectName(p)
	}
	if p := toolString(req, "directory"); p != "" {
		return cleanProjectName(p)
	}
	if p := toolString(req, "cwd"); p != "" {
		return cleanProjectName(p)
	}
	if p := toolString(req, "path"); p != "" {
		return cleanProjectName(p)
	}
	return ""
}

func sessionStartTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		s := &domain.Session{Project: toolProject(req), Summary: toolString(req, "summary"), StartedAt: time.Now().UTC()}
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

// toolResult lowers a tool outcome. Failures are published through the shared
// structured error contract with a stable code and a constant, bounded
// message — never the raw operation error (T08: replaces the universal
// opaque "operation failed" text while preserving its prefix).
func toolResult(value any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		payload := serverMemoryError(err)
		result := mcp.NewToolResultStructured(payload, fmt.Sprintf("operation failed: %s [code: %s]", payload.Error.Message, payload.Error.Code))
		result.IsError = true
		return result, nil
	}
	b, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, marshalErr
	}
	return mcp.NewToolResultText(string(b)), nil
}

// saveStructuredFromEffect lowers a durable save effect into the shared
// structured payload using the server's public UUID namespace exclusively. A
// nil structured value (plain text result) is returned when a valid exclusive
// reference cannot be built — structured content is additive and must never
// fabricate a reference the store did not durably assign (REM-SAVE-001).
func saveStructuredFromEffect(effect domain.SaveEffect) any {
	if effect.Observation == nil || effect.Observation.PublicID == "" {
		return nil
	}
	publicID, err := uuid.Parse(effect.Observation.PublicID)
	if err != nil {
		return nil
	}
	structured, err := memorycontract.FromWriteResult(domain.ObservationWriteResult{
		Ref:    domain.ObservationRef{PublicID: &publicID},
		Status: effect.Status,
	})
	if err != nil {
		return nil
	}
	return structured
}

// serverMemoryError lowers any operation error into the shared structured
// error contract. Authorization denials map to the forbidden class, invalid
// input to validation, and missing resources to the additive not_found code
// (read tools only; the frozen save/handoff code set is unchanged); everything
// else keeps memorycontract's stable classification. Raw denial reasons and
// driver text never surface.
func serverMemoryError(err error) memorycontract.ErrorStructured {
	if isAuthorizationDenial(err) {
		return memorycontract.ErrorStructured{Error: memorycontract.ErrorBody{
			Code:    memorycontract.CodeForbidden,
			Message: domain.ErrHandoffForbidden.Message,
		}}
	}
	if errors.Is(err, domain.ErrInvalidInput) {
		return memorycontract.ErrorStructured{Error: memorycontract.ErrorBody{
			Code:    memorycontract.CodeValidation,
			Message: "invalid operation input",
		}}
	}
	if errors.Is(err, domain.ErrNotFound) {
		return memorycontract.ErrorStructured{Error: memorycontract.ErrorBody{
			Code:    serverCodeNotFound,
			Message: "resource not found",
		}}
	}
	return memorycontract.FromError(err)
}

// serverCodeNotFound is the additive public code for missing resources on
// server read tools. It is intentionally outside the frozen save/handoff
// closed code set, which remains byte-identical.
const serverCodeNotFound = "not_found"

// structuredTextResult returns the legacy text content plus the additive
// structuredContent payload. A nil structured value yields a plain text
// result, byte-compatible with the legacy response.
func structuredTextResult(structured any, format string, args ...any) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultStructured(structured, fmt.Sprintf(format, args...)), nil
}

// structuredErrorResult returns an isError result whose text uses the same
// constant, redacted message as the structuredContent error classification —
// never a reference, key, or raw error text (REM-MCP-001).
func structuredErrorResult(payload memorycontract.ErrorStructured, format string, args ...any) (*mcp.CallToolResult, error) {
	result := mcp.NewToolResultStructured(payload, fmt.Sprintf(format, args...))
	result.IsError = true
	return result, nil
}

func saveTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		o := &domain.Observation{Title: toolString(req, "title"), Content: toolString(req, "content"), Type: toolString(req, "type"), SessionID: toolString(req, "session_id"), Project: toolProject(req), Scope: toolString(req, "scope"), TopicKey: toolString(req, "topic_key"), Confidence: toolFloat(req, "confidence", 0), Source: toolString(req, "source"), Tags: toolTags(req, "tags")}
		effect, err := ops.SaveObservationWithEffect(ctx, o)
		if err != nil {
			payload := serverMemoryError(err)
			return structuredErrorResult(payload, "Failed to save: %s", payload.Error.Message)
		}
		typ := o.Type
		if typ == "" {
			typ = "manual"
		}
		return structuredTextResult(saveStructuredFromEffect(effect), "Memory saved: %q (%s)", o.Title, typ)
	}
}

// toolTags splits a comma-separated tag list, trimming blanks.
func toolTags(req mcp.CallToolRequest, key string) []string {
	raw := toolString(req, key)
	if raw == "" {
		return nil
	}
	var tags []string
	for _, tag := range strings.Split(raw, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// handoffTool executes the compound, preauthorized, idempotent handoff through
// the authenticated Operations boundary. The server namespace addresses
// relation targets and observation references with public UUIDs exclusively
// (REM-AUTH-001, REM-HANDOFF-001/002, REM-MCP-001).
func handoffTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		request, invalid := handoffRequestFromArguments(req)
		if invalid != nil {
			return structuredErrorResult(*invalid, "Handoff failed: %s", invalid.Error.Message)
		}
		result, err := ops.ExecuteHandoff(ctx, request)
		if err != nil {
			payload := serverMemoryError(err)
			return structuredErrorResult(payload, "Handoff failed: %s", payload.Error.Message)
		}
		structured, lowerErr := memorycontract.FromWriteResult(result)
		if lowerErr != nil {
			// The operation returned an unusable result: fail closed with a
			// persistence classification instead of fabricating a reference.
			payload := memorycontract.FromError(domain.ErrHandoffPersistence)
			return structuredErrorResult(payload, "Handoff failed: %s", payload.Error.Message)
		}
		publicID := ""
		if result.Ref.PublicID != nil {
			publicID = result.Ref.PublicID.String()
		}
		return structuredTextResult(structured, "Handoff recorded: %q %s (%s)", request.Observation.Title, publicID, result.Status)
	}
}

// handoffRequestFromArguments lowers MCP tool arguments into a domain
// HandoffRequest. Every rejection is a bounded validation classification that
// echoes no payload, key, or reference material.
func handoffRequestFromArguments(req mcp.CallToolRequest) (domain.HandoffRequest, *memorycontract.ErrorStructured) {
	args := toolArgs(req)

	key := toolString(req, "idempotency_key")
	if strings.TrimSpace(key) == "" {
		payload := memorycontract.Validationf("idempotency_key is required")
		return domain.HandoffRequest{}, &payload
	}
	obsRaw, ok := args["observation"].(map[string]any)
	if !ok {
		payload := memorycontract.Validationf("observation object is required")
		return domain.HandoffRequest{}, &payload
	}
	observation := domain.SaveObservationInput{
		Title:     serverStringField(obsRaw, "title"),
		Content:   serverStringField(obsRaw, "content"),
		Type:      serverStringField(obsRaw, "type"),
		Project:   serverStringField(obsRaw, "project"),
		Scope:     serverStringField(obsRaw, "scope"),
		SessionID: serverStringField(obsRaw, "session_id"),
		TopicKey:  serverStringField(obsRaw, "topic_key"),
		Source:    serverStringField(obsRaw, "source"),
	}
	if strings.TrimSpace(observation.Title) == "" || strings.TrimSpace(observation.Content) == "" {
		payload := memorycontract.Validationf("observation.title and observation.content are required")
		return domain.HandoffRequest{}, &payload
	}
	if confidence, ok := obsRaw["confidence"].(float64); ok {
		observation.Confidence = confidence
	}
	if tagsRaw, ok := obsRaw["tags"].([]any); ok {
		for _, tag := range tagsRaw {
			if s, ok := tag.(string); ok && strings.TrimSpace(s) != "" {
				observation.Tags = append(observation.Tags, strings.TrimSpace(s))
			}
		}
	}
	request := domain.HandoffRequest{IdempotencyKey: key, Observation: observation}
	if raw, present := args["relation"]; present && raw != nil {
		relRaw, ok := raw.(map[string]any)
		if !ok {
			// A present relation must be an object: silently omitting it
			// would persist a handoff the caller did not request.
			payload := memorycontract.Validationf("relation must be an object")
			return domain.HandoffRequest{}, &payload
		}
		relation, invalid := handoffRelationFromArguments(relRaw)
		if invalid != nil {
			return domain.HandoffRequest{}, invalid
		}
		request.Relation = relation
	}
	if tuple, ok := args["capability_tuple"]; ok && tuple != nil {
		encoded, err := json.Marshal(tuple)
		if err != nil {
			payload := memorycontract.Validationf("capability_tuple must be JSON data")
			return domain.HandoffRequest{}, &payload
		}
		request.CapabilityTuple = encoded
	}
	return request, nil
}

// handoffRelationFromArguments lowers the optional relation argument. The
// server namespace accepts public_id targets only (REM-MCP-001).
func handoffRelationFromArguments(raw map[string]any) (*domain.HandoffRelationInput, *memorycontract.ErrorStructured) {
	targetRaw, ok := raw["target"].(map[string]any)
	if !ok {
		payload := memorycontract.Validationf("relation.target object is required")
		return nil, &payload
	}
	ref, invalid := handoffRefFromArguments(targetRaw)
	if invalid != nil {
		return nil, invalid
	}
	relationType := serverStringField(raw, "type")
	if relationType == "" {
		payload := memorycontract.Validationf("relation.type is required")
		return nil, &payload
	}
	relation := &domain.HandoffRelationInput{Target: ref, Type: relationType}
	if weight, ok := raw["weight"].(float64); ok {
		relation.Weight = weight
	}
	if confidence, ok := raw["confidence"].(float64); ok {
		relation.Confidence = confidence
	}
	relation.Reasoning = serverStringField(raw, "reasoning")
	return relation, nil
}

// handoffRefFromArguments builds the exclusive public-namespace relation
// target reference. The namespace XOR is enforced exactly — both or neither
// fail — BEFORE the server namespace preference is applied (review R7 fix 2).
func handoffRefFromArguments(raw map[string]any) (domain.ObservationRef, *memorycontract.ErrorStructured) {
	publicRaw, hasPublic := raw["public_id"]
	_, hasLocal := raw["local_id"]
	if hasPublic == hasLocal {
		payload := memorycontract.Validationf("relation.target must set exactly one of public_id or local_id")
		return domain.ObservationRef{}, &payload
	}
	if hasLocal {
		payload := memorycontract.Validationf("the server namespace accepts public_id targets only")
		return domain.ObservationRef{}, &payload
	}
	rawID, ok := publicRaw.(string)
	if !ok {
		payload := memorycontract.Validationf("relation.target.public_id must be a UUID")
		return domain.ObservationRef{}, &payload
	}
	id, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil {
		payload := memorycontract.Validationf("relation.target.public_id must be a UUID")
		return domain.ObservationRef{}, &payload
	}
	return domain.ObservationRef{PublicID: &id}, nil
}

// serverStringField reads a string value from a raw argument object.
func serverStringField(raw map[string]any, key string) string {
	v, _ := raw[key].(string)
	return v
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

func getBlastRadiusTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		nodeID := toolString(req, "node_id")
		if nodeID == "" {
			return mcp.NewToolResultError("node_id is required"), nil
		}
		depth := toolInt(req, "depth", 3)

		observations, err := ops.ListObservations(ctx, domain.ObservationFilter{Limit: 200})
		if err != nil {
			return toolResult(nil, err)
		}

		var nodes []graph.GraphAnalyticsNode
		var edges []graph.GraphAnalyticsEdge
		nodeSet := make(map[string]bool)

		for _, obs := range observations {
			nid := "observation:" + obs.PublicID
			nodeSet[nid] = true
			nodes = append(nodes, graph.GraphAnalyticsNode{
				ID:         nid,
				Label:      obs.Title,
				Kind:       "observation",
				Subtype:    obs.Type,
				SourceFile: obs.Source,
			})

			sub, err := ops.GetGraphSubgraph(ctx, obs.PublicID, 1, 30)
			if err == nil && sub != nil {
				for _, e := range sub.Edges {
					edges = append(edges, graph.GraphAnalyticsEdge{
						ID:     e.ID,
						Source: e.Source,
						Target: e.Target,
						Type:   e.Type,
					})
				}
			}
		}

		blast := graph.CalculateBlastRadius(nodeID, nodes, edges, depth)
		return toolResult(blast, nil)
	}
}

func analyzeArchitectureTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := toolString(req, "project")
		observations, err := ops.ListObservations(ctx, domain.ObservationFilter{Project: project, Limit: 200})
		if err != nil {
			return toolResult(nil, err)
		}

		var nodes []graph.GraphAnalyticsNode
		var edges []graph.GraphAnalyticsEdge
		nodeSet := make(map[string]bool)

		for _, obs := range observations {
			nid := "observation:" + obs.PublicID
			nodeSet[nid] = true
			nodes = append(nodes, graph.GraphAnalyticsNode{
				ID:         nid,
				Label:      obs.Title,
				Kind:       "observation",
				Subtype:    obs.Type,
				SourceFile: obs.Source,
			})

			sub, err := ops.GetGraphSubgraph(ctx, obs.PublicID, 1, 30)
			if err == nil && sub != nil {
				for _, e := range sub.Edges {
					edges = append(edges, graph.GraphAnalyticsEdge{
						ID:     e.ID,
						Source: e.Source,
						Target: e.Target,
						Type:   e.Type,
					})
				}
			}
		}

		report := graph.AnalyzeGraph(nodes, edges)
		return toolResult(report, nil)
	}
}

func detectCyclesTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := toolString(req, "project")
		observations, err := ops.ListObservations(ctx, domain.ObservationFilter{Project: project, Limit: 200})
		if err != nil {
			return toolResult(nil, err)
		}

		var nodes []graph.GraphAnalyticsNode
		var edges []graph.GraphAnalyticsEdge

		for _, obs := range observations {
			nodes = append(nodes, graph.GraphAnalyticsNode{
				ID:    "observation:" + obs.PublicID,
				Label: obs.Title,
			})

			sub, err := ops.GetGraphSubgraph(ctx, obs.PublicID, 1, 30)
			if err == nil && sub != nil {
				for _, e := range sub.Edges {
					edges = append(edges, graph.GraphAnalyticsEdge{
						ID:     e.ID,
						Source: e.Source,
						Target: e.Target,
						Type:   e.Type,
					})
				}
			}
		}

		cycles := graph.FindCycles(nodes, edges)
		return toolResult(map[string]any{"cycles_count": len(cycles), "cycles": cycles}, nil)
	}
}

func ingestCodeTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path := toolString(req, "path")
		if path == "" {
			path = toolString(req, "directory")
		}
		project := toolString(req, "project")
		maxFiles := toolInt(req, "max_files", 250)

		result, err := processIngestCode(ctx, ops, path, project, maxFiles, true)
		return toolResult(result, err)
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

func getProjectContextTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := toolString(req, "project")
		if project == "" {
			project = "default"
		}
		res, err := ops.GetProjectContext(ctx, project)
		return toolResult(res, err)
	}
}

func listProjectSkillsTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := toolString(req, "project")
		res, err := ops.ListProjectSkills(ctx, project)
		return toolResult(res, err)
	}
}

func getProjectSkillTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		key := toolString(req, "key")
		project := toolString(req, "project")
		res, err := ops.GetProjectSkill(ctx, project, key)
		return toolResult(res, err)
	}
}

func resolveQueryTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := toolString(req, "query")
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		project := toolString(req, "project")
		limit := toolInt(req, "limit", 10)

		// Get project context (system prompt, governance rules, and skills)
		projCtx, _ := ops.GetProjectContext(ctx, project)

		// Search authorized observations
		opts := domain.SearchOptions{
			Query:   query,
			Project: project,
			Limit:   limit,
		}
		searchRes, _ := ops.SearchObservations(ctx, query, opts)

		response := map[string]any{
			"mode":            "server",
			"database":        "postgresql",
			"query":           query,
			"project":         project,
			"project_context": projCtx,
			"total_matches":   len(searchRes),
			"observations":    searchRes,
		}

		b, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return toolResult(nil, err)
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

func getStatusTool(ops Operations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		status := map[string]any{
			"mode":         "server",
			"database":     "postgresql",
			"version":      "2.0.0",
			"capabilities": []string{"authorized_rls", "project_context", "corporate_skills", "vector_embeddings", "knowledge_graph", "scoring", "handoff"},
		}
		b, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return toolResult(nil, err)
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

func (a *apiHandler) getProjectContext(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project == "" {
		project = "default"
	}
	res, err := a.ops.GetProjectContext(r.Context(), project)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *apiHandler) listProjectArtifacts(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	kind := r.URL.Query().Get("kind")
	items, err := a.ops.ListProjectArtifacts(r.Context(), project, kind)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *apiHandler) saveProjectArtifact(w http.ResponseWriter, r *http.Request) {
	var input domain.SaveProjectArtifactInput
	if !decodeBody(w, r, &input) {
		return
	}
	item, err := a.ops.SaveProjectArtifact(r.Context(), input)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *apiHandler) deleteProjectArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "deleted from admin interface"
	}
	if err := a.ops.DeleteProjectArtifact(r.Context(), id, reason); err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *apiHandler) getProjectDuplicates(w http.ResponseWriter, r *http.Request) {
	dups, err := a.ops.GetProjectDuplicates(r.Context())
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dups)
}

func (a *apiHandler) mergeProject(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SourceProject string `json:"source_project"`
		TargetProject string `json:"target_project"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	res, err := a.ops.MergeProject(r.Context(), input.SourceProject, input.TargetProject)
	if err != nil {
		respondOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *apiHandler) aiStatus(w http.ResponseWriter, r *http.Request) {
	llmProvider := a.cfg.AI.Provider
	if llmProvider == "" {
		llmProvider = "none"
	}
	llmModel := a.cfg.AI.Model
	if llmModel == "" {
		llmModel = "default"
	}
	llmBaseURL := a.cfg.AI.BaseURL

	embProvider := a.cfg.Search.EmbeddingProvider
	if embProvider == "" {
		embProvider = "none"
	}
	embModel := a.cfg.Search.EmbeddingModel
	if embModel == "" {
		embModel = "bge-m3"
	}
	embBaseURL := a.cfg.Search.EmbeddingBaseURL
	embDim := 1024
	if strings.Contains(embModel, "qwen3-embedding:4b") {
		embDim = 2560
	} else if strings.Contains(embModel, "qwen3-embedding:8b") {
		embDim = 4096
	} else if strings.Contains(embModel, "nomic-embed-text") || strings.Contains(embModel, "text-embedding-004") {
		embDim = 768
	} else if strings.Contains(embModel, "text-embedding-3-small") || strings.Contains(embModel, "text-embedding-ada") {
		embDim = 1536
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"llm": map[string]any{
			"provider":   llmProvider,
			"model":      llmModel,
			"base_url":   llmBaseURL,
			"configured": llmProvider != "" && llmProvider != "none",
		},
		"embedding": map[string]any{
			"provider":   embProvider,
			"model":      embModel,
			"base_url":   embBaseURL,
			"dimensions": embDim,
			"configured": embProvider != "" && embProvider != "none",
		},
	})
}

func (a *apiHandler) testLLM(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	provider := a.cfg.AI.Provider
	model := a.cfg.AI.Model
	baseURL := a.cfg.AI.BaseURL
	apiKey := os.Getenv("CORTEX_AI_API_KEY")

	if provider == "" || provider == "none" {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "not_configured",
			"provider":   "none",
			"model":      "none",
			"latency_ms": 0,
			"message":    "El motor LLM del servidor no está configurado (define CORTEX_AI_PROVIDER y CORTEX_AI_MODEL en el servidor)",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var promptResponse string
	var err error

	if provider == "ollama" {
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/api/generate"
		reqBody, _ := json.Marshal(map[string]any{
			"model":  model,
			"prompt": "Respond with 'Cortex LLM Online and Ready' in 10 words or less.",
			"stream": false,
		})
		httpReq, httpErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if httpErr != nil {
			err = httpErr
		} else {
			httpReq.Header.Set("Content-Type", "application/json")
			resp, doErr := http.DefaultClient.Do(httpReq)
			if doErr != nil {
				err = doErr
			} else {
				defer func() { _ = resp.Body.Close() }()
				var oResp struct {
					Response string `json:"response"`
					Error    string `json:"error"`
				}
				if json.NewDecoder(resp.Body).Decode(&oResp) == nil {
					if oResp.Error != "" {
						err = fmt.Errorf("%s", oResp.Error)
					} else {
						promptResponse = strings.TrimSpace(oResp.Response)
					}
				}
			}
		}
	} else {
		// Generic OpenAI-compatible endpoint
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
		reqBody, _ := json.Marshal(map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": "Respond with 'Cortex LLM Online and Ready' in 10 words or less."},
			},
			"max_tokens": 30,
		})
		httpReq, httpErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if httpErr != nil {
			err = httpErr
		} else {
			httpReq.Header.Set("Content-Type", "application/json")
			if apiKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
			resp, doErr := http.DefaultClient.Do(httpReq)
			if doErr != nil {
				err = doErr
			} else {
				defer func() { _ = resp.Body.Close() }()
				var cResp struct {
					Choices []struct {
						Message struct {
							Content string `json:"content"`
						} `json:"message"`
					} `json:"choices"`
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.NewDecoder(resp.Body).Decode(&cResp) == nil {
					if cResp.Error.Message != "" {
						err = fmt.Errorf("%s", cResp.Error.Message)
					} else if len(cResp.Choices) > 0 {
						promptResponse = strings.TrimSpace(cResp.Choices[0].Message.Content)
					}
				}
			}
		}
	}

	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "error",
			"provider":   provider,
			"model":      model,
			"latency_ms": latency,
			"error":      err.Error(),
		})
		return
	}

	if promptResponse == "" {
		promptResponse = "Cortex LLM Online and Ready"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"provider":   provider,
		"model":      model,
		"latency_ms": latency,
		"response":   promptResponse,
	})
}

func (a *apiHandler) testEmbedding(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	provider := a.cfg.Search.EmbeddingProvider
	model := a.cfg.Search.EmbeddingModel
	baseURL := a.cfg.Search.EmbeddingBaseURL
	apiKey := os.Getenv("CORTEX_SEARCH_EMBEDDING_API_KEY")

	if provider == "" || provider == "none" {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "not_configured",
			"provider":   "none",
			"model":      "none",
			"latency_ms": 0,
			"message":    "El motor de embeddings no está configurado (define CORTEX_SEARCH_EMBEDDING_PROVIDER y CORTEX_SEARCH_EMBEDDING_MODEL en el servidor)",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var vector []float64
	var err error

	if provider == "ollama" {
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/api/embeddings"
		reqBody, _ := json.Marshal(map[string]any{
			"model":  model,
			"prompt": "cortex architectural memory embedding test",
		})
		httpReq, httpErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if httpErr != nil {
			err = httpErr
		} else {
			httpReq.Header.Set("Content-Type", "application/json")
			resp, doErr := http.DefaultClient.Do(httpReq)
			if doErr != nil {
				err = doErr
			} else {
				defer func() { _ = resp.Body.Close() }()
				var oResp struct {
					Embedding []float64 `json:"embedding"`
					Error     string    `json:"error"`
				}
				if json.NewDecoder(resp.Body).Decode(&oResp) == nil {
					if oResp.Error != "" {
						err = fmt.Errorf("%s", oResp.Error)
					} else {
						vector = oResp.Embedding
					}
				}
			}
		}
	} else {
		// Generic OpenAI-compatible embeddings
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/embeddings"
		reqBody, _ := json.Marshal(map[string]any{
			"model": model,
			"input": "cortex architectural memory embedding test",
		})
		httpReq, httpErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if httpErr != nil {
			err = httpErr
		} else {
			httpReq.Header.Set("Content-Type", "application/json")
			if apiKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
			resp, doErr := http.DefaultClient.Do(httpReq)
			if doErr != nil {
				err = doErr
			} else {
				defer func() { _ = resp.Body.Close() }()
				var cResp struct {
					Data []struct {
						Embedding []float64 `json:"embedding"`
					} `json:"data"`
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.NewDecoder(resp.Body).Decode(&cResp) == nil {
					if cResp.Error.Message != "" {
						err = fmt.Errorf("%s", cResp.Error.Message)
					} else if len(cResp.Data) > 0 {
						vector = cResp.Data[0].Embedding
					}
				}
			}
		}
	}

	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "error",
			"provider":   provider,
			"model":      model,
			"latency_ms": latency,
			"error":      err.Error(),
		})
		return
	}

	var sample []float64
	if len(vector) > 5 {
		sample = vector[:5]
	} else {
		sample = vector
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"provider":      provider,
		"model":         model,
		"dimensions":    len(vector),
		"latency_ms":    latency,
		"sample_vector": sample,
	})
}
