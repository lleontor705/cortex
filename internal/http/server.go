// Package http implements the REST API server for Cortex.
//
// It exposes observation, session, search, graph, and scoring endpoints
// using the standard library net/http package.
package http

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
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
	Observations *sqlitestore.Store
	Sessions     *session.Store
	Search       *search.Store
	Prompts      *prompt.Store
	Graph        *graphstore.Store
	Scoring      *scoringstore.Store
}

// Server wraps an http.Server with Cortex handlers.
type Server struct {
	httpServer     *http.Server
	deps           *Deps
	graphService   *graphdomain.Service
	scoringService *scoringdomain.Service
}

// NewServer creates a new HTTP server on the given address.
func NewServer(addr string, deps *Deps) *Server {
	mux := http.NewServeMux()
	s := &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      mux,
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
	mux.HandleFunc("PUT /api/observations/{id}", s.handleUpdateObservation)
	mux.HandleFunc("DELETE /api/observations/{id}", s.handleDeleteObservation)

	// Sessions
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("POST /api/sessions/{id}/end", s.handleEndSession)

	// Search
	mux.HandleFunc("GET /api/search", s.handleSearch)

	// Graph
	mux.HandleFunc("POST /api/graph/edges", s.handleCreateEdge)
	mux.HandleFunc("GET /api/graph/{id}/related", s.handleGetRelated)
	mux.HandleFunc("DELETE /api/graph/edges/{id}", s.handleDeleteEdge)

	// Scoring
	mux.HandleFunc("GET /api/scores/{id}", s.handleGetScore)
	mux.HandleFunc("POST /api/scores/{id}/recalculate", s.handleRecalculateScore)

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

// ─── Health ─────────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── Observations ───────────────────────────────────────────────────────────

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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) handleCreateObservation(w http.ResponseWriter, r *http.Request) {
	var obs domain.Observation
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&obs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := s.deps.Observations.Save(r.Context(), &obs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, obs)
}

func (s *Server) handleGetObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	obs, err := s.deps.Observations.GetByID(r.Context(), id)
	if err != nil {
		if domain.IsNotFoundError(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) handleUpdateObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var obs domain.Observation
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&obs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	obs.ID = id

	if err := s.deps.Observations.Update(r.Context(), &obs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) handleDeleteObservation(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.deps.Observations.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	sessions, err := s.deps.Sessions.List(r.Context(), project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var sess domain.Session
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&sess); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := s.deps.Sessions.Create(r.Context(), &sess); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := s.deps.Sessions.End(r.Context(), id, body.Summary); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ended"})
}

// ─── Search ─────────────────────────────────────────────────────────────────

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// ─── Graph ──────────────────────────────────────────────────────────────────

func (s *Server) handleCreateEdge(w http.ResponseWriter, r *http.Request) {
	var edge domain.Edge
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&edge); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	svc := s.graphService
	if err := svc.CreateEdge(r.Context(), &edge); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, edge)
}

func (s *Server) handleGetRelated(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	depth := queryInt(r, "depth", 1)

	svc := s.graphService
	related, err := svc.GetRelated(r.Context(), id, depth)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, related)
}

func (s *Server) handleDeleteEdge(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	svc := s.graphService
	if err := svc.DeleteEdge(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ─── Scoring ────────────────────────────────────────────────────────────────

func (s *Server) handleGetScore(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	svc := s.scoringService
	score, err := svc.GetScore(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, score)
}

func (s *Server) handleRecalculateScore(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	svc := s.scoringService
	newScore, err := svc.CalculateScore(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]float64{"score": newScore})
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers already sent; log but cannot write error response
		log.Printf("writeJSON encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
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

