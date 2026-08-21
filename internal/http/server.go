// Package http implements the REST API server for Cortex.
//
// It exposes observation, session, search, graph, and scoring endpoints
// using the standard library net/http package.
package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	graphdomain "github.com/lleontor705/cortex/internal/domain/graph"
	scoringdomain "github.com/lleontor705/cortex/internal/domain/scoring"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

const (
	// maxRequestBodySize limits JSON request bodies to 1 MB.
	maxRequestBodySize = 1 << 20
	// maxLimit caps the maximum number of results per query.
	maxLimit = 100
)

// Deps bundles store dependencies for HTTP handlers.
type Deps struct {
	Observations      *sqlitestore.Store
	Sessions          *session.Store
	Search            *search.Store
	Prompts           *prompt.Store
	Graph             *graphstore.Store
	Scoring           *scoringstore.Store
	TemporalSnapshots *sqlitestore.TemporalSnapshotRepository
}

// Options configures optional HTTP hardening behavior.
type Options struct {
	AuthToken      string
	AllowedOrigins []string
}

// Server wraps an http.Server with Cortex handlers.
type Server struct {
	httpServer     *http.Server
	deps           *Deps
	graphService   *graphdomain.Service
	scoringService *scoringdomain.Service
}

// NewServer creates a new HTTP server on the given address.
func NewServer(addr string, deps *Deps, opts Options) *Server {
	mux := http.NewServeMux()
	s := &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      corsHandler(opts.AllowedOrigins, withAuth(mux, opts.AuthToken)),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		deps:           deps,
		graphService:   graphdomain.NewService(deps.Graph),
		scoringService: scoringdomain.NewService(deps.Scoring),
	}

	// Health
	mux.HandleFunc("GET /health", s.handleHealth)

	// Observations
	mux.HandleFunc("GET /api/observations", s.handleListObservations)
	mux.HandleFunc("POST /api/observations", s.handleCreateObservation)
	mux.HandleFunc("GET /api/observations/{id}", s.handleGetObservation)
	mux.HandleFunc("GET /api/observations/{id}/revisions", s.handleGetObservationRevisions)
	mux.HandleFunc("PUT /api/observations/{id}", s.handleUpdateObservation)
	mux.HandleFunc("DELETE /api/observations/{id}", s.handleDeleteObservation)

	// Sessions
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("POST /api/sessions/{id}/end", s.handleEndSession)

	// Prompts
	mux.HandleFunc("POST /api/prompts", s.handleCreatePrompt)

	// Search
	mux.HandleFunc("GET /api/search", s.handleSearch)

	// Graph
	mux.HandleFunc("POST /api/graph/edges", s.handleCreateEdge)
	mux.HandleFunc("GET /api/graph/{id}/related", s.handleGetRelated)
	mux.HandleFunc("DELETE /api/graph/edges/{id}", s.handleDeleteEdge)

	// Archive
	mux.HandleFunc("POST /api/observations/{id}/archive", s.handleArchiveObservation)

	// Search -- hybrid
	mux.HandleFunc("GET /api/search/hybrid", s.handleSearchHybrid)

	// Scoring
	mux.HandleFunc("GET /api/scores/{id}", s.handleGetScore)
	mux.HandleFunc("POST /api/scores/{id}/recalculate", s.handleRecalculateScore)

	// Export/Import
	mux.HandleFunc("GET /api/export", s.handleExport)
	mux.HandleFunc("POST /api/import", s.handleImport)

	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the server's listen address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// --- Health -----------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Observations -----------------------------------------------------------

func (s *Server) handleListObservations(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	if limit > maxLimit {
		limit = maxLimit
	}
	filter := domain.ObservationFilter{
		Project: r.URL.Query().Get("project"),
		Scope:   r.URL.Query().Get("scope"),
		Type:    r.URL.Query().Get("type"),
		Limit:   limit,
		Offset:  queryInt(r, "offset", 0),
	}

	obs, err := s.deps.Observations.List(r.Context(), filter)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) handleCreateObservation(w http.ResponseWriter, r *http.Request) {
	var obs domain.Observation
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&obs); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}

	if err := s.deps.Observations.Save(r.Context(), &obs); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, obs)
}

func (s *Server) handleGetObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	obs, err := s.deps.Observations.GetByID(r.Context(), id)
	if err != nil {
		if domain.IsNotFoundError(err) {
			writeError(w, http.StatusNotFound, codeNotFound, "resource not found")
			return
		}
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

type observationRevisionPayload struct {
	Timestamp      time.Time `json:"timestamp"`
	Reason         string    `json:"reason"`
	RevisionCount  int       `json:"revision_count"`
	Title          string    `json:"title"`
	ContentPreview string    `json:"content_preview,omitempty"`
}

func (s *Server) handleGetObservationRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	if _, err := s.deps.Observations.GetByID(r.Context(), id); err != nil {
		if domain.IsNotFoundError(err) {
			writeError(w, http.StatusNotFound, codeNotFound, "resource not found")
			return
		}
		writeDomainError(w, err)
		return
	}

	history, err := loadObservationRevisionPayloads(r.Context(), s.deps.TemporalSnapshots, id, queryInt(r, "limit", 20))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleUpdateObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	var obs domain.Observation
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&obs); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}
	obs.ID = id

	if err := s.deps.Observations.Update(r.Context(), &obs); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) handleDeleteObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	if err := s.deps.Observations.Delete(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Sessions ---------------------------------------------------------------

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	sessions, err := s.deps.Sessions.List(r.Context(), project)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var sess domain.Session
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&sess); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}

	if err := s.deps.Sessions.Create(r.Context(), &sess); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Sessions.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}

	if err := s.deps.Sessions.End(r.Context(), id, body.Summary); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ended"})
}

func (s *Server) handleCreatePrompt(w http.ResponseWriter, r *http.Request) {
	var value domain.Prompt
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&value); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}
	if strings.TrimSpace(value.SessionID) == "" || strings.TrimSpace(value.Content) == "" || strings.TrimSpace(value.Project) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidReq, "session_id, content, and project are required")
		return
	}
	if _, err := s.deps.Sessions.GetByID(r.Context(), value.SessionID); err != nil {
		mapDomainError(w, err)
		return
	}
	if err := s.deps.Prompts.Save(r.Context(), &value); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

// --- Search -----------------------------------------------------------------

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, codeInvalidReq, "query parameter 'q' is required")
		return
	}

	opts := domain.SearchOptions{
		Query:   query,
		Project: r.URL.Query().Get("project"),
		Scope:   r.URL.Query().Get("scope"),
		Type:    r.URL.Query().Get("type"),
		Limit:   queryInt(r, "limit", 20),
	}

	results, err := s.deps.Search.Search(r.Context(), query, opts)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// --- Archive ---------------------------------------------------------------

func (s *Server) handleArchiveObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	if err := s.deps.Observations.Delete(r.Context(), id); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

// --- Hybrid Search ---------------------------------------------------------

func (s *Server) handleSearchHybrid(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, codeInvalidReq, "query parameter 'q' is required")
		return
	}

	limit := queryInt(r, "limit", 10)
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	opts := domain.SearchOptions{
		Query:   query,
		Project: r.URL.Query().Get("project"),
		Scope:   r.URL.Query().Get("scope"),
		Limit:   limit,
	}

	results, err := s.deps.Search.Search(r.Context(), query, opts)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// --- Graph ------------------------------------------------------------------

func (s *Server) handleCreateEdge(w http.ResponseWriter, r *http.Request) {
	var edge domain.Edge
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&edge); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}

	svc := s.graphService
	if err := svc.CreateEdge(r.Context(), &edge); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, edge)
}

func (s *Server) handleGetRelated(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}
	depth := queryInt(r, "depth", 1)

	svc := s.graphService
	related, err := svc.GetRelated(r.Context(), id, depth)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, related)
}

func (s *Server) handleDeleteEdge(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	svc := s.graphService
	if err := svc.DeleteEdge(r.Context(), id); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Scoring ----------------------------------------------------------------

func (s *Server) handleGetScore(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	svc := s.scoringService
	score, err := svc.GetScore(r.Context(), id)
	if err != nil {
		if domain.IsNotFoundError(err) {
			writeError(w, http.StatusNotFound, codeNotFound, "resource not found")
			return
		}
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, score)
}

func (s *Server) handleRecalculateScore(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidID, "invalid id")
		return
	}

	svc := s.scoringService
	newScore, err := svc.CalculateScore(r.Context(), id)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]float64{"score": newScore})
}

// --- Export/Import ----------------------------------------------------------

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := s.deps.Observations.ExportAll(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var data sqlitestore.ExportData
	if err := json.NewDecoder(io.LimitReader(r.Body, 10<<20)).Decode(&data); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON")
		return
	}
	result, err := s.deps.Observations.ImportData(r.Context(), &data)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Public error contract (T08) ---------------------------------------------
//
// Every local HTTP error response carries a bounded, stable public code and
// message. Raw store/driver causes (SQL text, DSNs, paths, credentials) are
// never surfaced: known classes map to consistent statuses, and every unknown
// cause collapses to the constant internal error.

const (
	codeNotFound     = "not_found"
	codeValidation   = "validation"
	codeConflict     = "conflict"
	codeUnauthorized = "unauthorized"
	codeUnavailable  = "unavailable"
	codeTimeout      = "timeout"
	codeInternal     = "internal"
	codeInvalidJSON  = "invalid_json"
	codeInvalidID    = "invalid_id"
	codeInvalidReq   = "invalid_request"

	maxPublicErrorMessageRunes = 200
)

// publicError is the stable public classification of an error.
type publicError struct {
	status  int
	code    string
	message string
}

// classifyPublicError lowers any error into the bounded public contract.
// Messages come only from this table (plus safe, domain-constructed
// validation text); the raw error string is never echoed.
func classifyPublicError(err error) publicError {
	switch {
	case err == nil:
		return publicError{http.StatusInternalServerError, codeInternal, "internal error"}
	case domain.IsNotFoundError(err):
		return publicError{http.StatusNotFound, codeNotFound, "resource not found"}
	case isFailedClassification(err):
		// ClassFailed wraps a real persistence failure whose cause carries
		// driver/SQL text: classify as internal with a constant message.
		return publicError{http.StatusInternalServerError, codeInternal, "internal error"}
	case domain.IsValidationError(err):
		return publicError{http.StatusBadRequest, codeValidation, boundedValidationMessage(err)}
	case domain.IsConflictError(err),
		errors.Is(err, domain.ErrAlreadyExists),
		errors.Is(err, domain.ErrSessionEnded),
		errors.Is(err, domain.ErrConflict):
		return publicError{http.StatusConflict, codeConflict, "conflict with current state"}
	case errors.Is(err, domain.ErrUnauthorized):
		return publicError{http.StatusUnauthorized, codeUnauthorized, "authentication required"}
	case isSQLiteBusy(err):
		return publicError{http.StatusServiceUnavailable, codeUnavailable, "database is busy; retry the operation"}
	case errors.Is(err, context.DeadlineExceeded):
		return publicError{http.StatusGatewayTimeout, codeTimeout, "request timed out"}
	default:
		return publicError{http.StatusInternalServerError, codeInternal, "internal error"}
	}
}

// isFailedClassification reports whether err is a domain persistence-failure
// classification (ClassFailed), whose wrapped cause must never surface.
func isFailedClassification(err error) bool {
	var validation *domain.ValidationError
	return errors.As(err, &validation) && validation != nil && validation.Code == domain.ClassFailed
}

// boundedValidationMessage surfaces only domain-constructed validation text:
// the field and message (and rejected rule), never a wrapped cause. Bounded
// to maxPublicErrorMessageRunes runes.
func boundedValidationMessage(err error) string {
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation == nil {
		return "invalid input"
	}
	message := validation.Message
	if validation.Code == "" { // legacy field-validation rendering
		message = fmt.Sprintf("validation error on field %q: %s", validation.Field, validation.Message)
	} else if validation.Rule != "" {
		message = fmt.Sprintf("%s (rule: %s)", validation.Message, validation.Rule)
	}
	return boundPublicText(message)
}

// boundPublicText truncates text to maxPublicErrorMessageRunes runes.
func boundPublicText(text string) string {
	runes := []rune(text)
	if len(runes) <= maxPublicErrorMessageRunes {
		return text
	}
	return string(runes[:maxPublicErrorMessageRunes]) + "…[truncated]"
}

// isSQLiteBusy reports whether err is a SQLite write-contention failure.
// The matched texts are constant driver messages, not attacker-controlled.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case strings.Contains(err.Error(), "database is locked"),
		strings.Contains(err.Error(), "database table is locked"):
		return true
	}
	return false
}

// writeDomainError publishes the stable classification of err.
func writeDomainError(w http.ResponseWriter, err error) {
	classified := classifyPublicError(err)
	writeError(w, classified.status, classified.code, classified.message)
}

// --- Helpers ----------------------------------------------------------------

func mapDomainError(w http.ResponseWriter, err error) {
	writeDomainError(w, err)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers already sent; log but cannot write error response. The
		// encode error text is bounded and never contains request payloads.
		log.Printf("writeJSON encode error: %v", err)
	}
}

// writeError emits the stable public error envelope: a constant, bounded
// message plus its machine-readable code.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": boundPublicText(msg), "code": code})
}

func pathInt64(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func queryInt(r *http.Request, key string, defaultVal int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

type revisionSnapshotDescription struct {
	Reason   string `json:"reason"`
	Previous struct {
		Title         string `json:"title"`
		Content       string `json:"content"`
		RevisionCount int    `json:"revision_count"`
	} `json:"previous"`
}

func loadObservationRevisionPayloads(ctx context.Context, repo *sqlitestore.TemporalSnapshotRepository, observationID int64, limit int) ([]observationRevisionPayload, error) {
	if repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	snapshots, err := repo.GetByRootObservation(ctx, observationID)
	if err != nil {
		return nil, err
	}

	history := make([]observationRevisionPayload, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var parsed revisionSnapshotDescription
		if err := json.Unmarshal([]byte(snapshot.Description), &parsed); err != nil {
			continue
		}
		history = append(history, observationRevisionPayload{
			Timestamp:      snapshot.Timestamp,
			Reason:         parsed.Reason,
			RevisionCount:  parsed.Previous.RevisionCount,
			Title:          parsed.Previous.Title,
			ContentPreview: truncateHTTP(parsed.Previous.Content, 150),
		})
		if len(history) >= limit {
			break
		}
	}

	return history, nil
}

func truncateHTTP(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func withAuth(next http.Handler, token string) http.Handler {
	token = strings.TrimSpace(token)
	if token == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if requestAuthorized(r, token) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or invalid API token")
	})
}

func requestAuthorized(r *http.Request, token string) bool {
	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(authHeader, "Bearer ") {
		candidate := strings.TrimSpace(authHeader[len("Bearer "):])
		if candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			return true
		}
	}
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return subtle.ConstantTimeCompare([]byte(apiKey), []byte(token)) == 1
	}
	return false
}

func corsHandler(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if origin = strings.TrimSpace(origin); origin != "" && origin != "*" {
			allowed[origin] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if _, ok := allowed[origin]; ok && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, X-API-Key, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
