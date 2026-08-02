package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
)

type fakeOperations struct {
	observations map[int64]*domain.Observation
	nextID       int64
}

func newFakeOperations() *fakeOperations {
	return &fakeOperations{observations: make(map[int64]*domain.Observation), nextID: 1}
}

func (f *fakeOperations) SaveObservation(_ context.Context, o *domain.Observation) error {
	if o == nil || o.Title == "" || o.Content == "" {
		return domain.ErrInvalidInput
	}
	o.ID = f.nextID
	o.PublicID = fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID)
	f.nextID++
	copy := *o
	f.observations[o.ID] = &copy
	return nil
}

func (f *fakeOperations) GetObservationByPublicID(_ context.Context, publicID string) (*domain.Observation, error) {
	for _, observation := range f.observations {
		if observation.PublicID == publicID {
			copy := *observation
			return &copy, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeOperations) GetObservationByID(_ context.Context, id int64) (*domain.Observation, error) {
	o, ok := f.observations[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copy := *o
	return &copy, nil
}

func (f *fakeOperations) UpdateObservation(_ context.Context, o *domain.Observation) error {
	if _, ok := f.observations[o.ID]; !ok {
		return domain.ErrNotFound
	}
	copy := *o
	f.observations[o.ID] = &copy
	return nil
}

func (f *fakeOperations) DeleteObservation(_ context.Context, id int64) error {
	if _, ok := f.observations[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.observations, id)
	return nil
}

func (f *fakeOperations) CreateSession(_ context.Context, _ *domain.Session) error { return nil }
func (f *fakeOperations) ListSessions(context.Context, string) ([]*domain.Session, error) {
	return []*domain.Session{}, nil
}
func (f *fakeOperations) GetServerStats(context.Context) (*domain.ServerStats, error) {
	return &domain.ServerStats{}, nil
}
func (f *fakeOperations) ListAuditEvents(context.Context, int) ([]*domain.AuditEntry, error) {
	return []*domain.AuditEntry{}, nil
}
func (f *fakeOperations) ListProjects(context.Context) ([]string, error) { return []string{}, nil }

func (f *fakeOperations) ListObservations(context.Context, domain.ObservationFilter) ([]*domain.Observation, error) {
	result := make([]*domain.Observation, 0, len(f.observations))
	for _, o := range f.observations {
		copy := *o
		result = append(result, &copy)
	}
	return result, nil
}

func (f *fakeOperations) SearchObservations(context.Context, string, domain.SearchOptions) ([]*domain.SearchResult, error) {
	return []*domain.SearchResult{}, nil
}

func (f *fakeOperations) CreateGraphEdge(context.Context, *domain.Edge) error { return nil }
func (f *fakeOperations) GetGraphEdgeByPublicID(context.Context, string) (*domain.Edge, error) {
	return &domain.Edge{ID: 1, PublicID: "00000000-0000-0000-0000-000000000020"}, nil
}
func (f *fakeOperations) GetRelatedObservations(context.Context, int64, int) ([]*domain.Observation, error) {
	return []*domain.Observation{}, nil
}
func (f *fakeOperations) DeleteGraphEdge(context.Context, int64) error { return nil }
func (f *fakeOperations) GetImportanceScore(context.Context, int64) (*domain.ImportanceScore, error) {
	return &domain.ImportanceScore{Score: 1}, nil
}

func testHandler(health healthCheck) http.Handler {
	h, _ := newHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}, Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20}}, newFakeOperations(), health)
	return h
}

func TestHTTPHealthIsPublicAndChecksDatabase(t *testing.T) {
	h := testHandler(func(context.Context) error { return nil })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health response = %d %s", rec.Code, rec.Body.String())
	}

	h = testHandler(func(context.Context) error { return errors.New("down") })
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHTTPAPIBearerAuthAndObservationREST(t *testing.T) {
	ops := newFakeOperations()
	h, _ := newHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}, ops, func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/observations", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	body := bytes.NewBufferString(`{"session_id":"00000000-0000-0000-0000-000000000010","title":"decision","content":"use postgres","type":"decision"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/observations", body)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("save response = %d %s", rec.Code, rec.Body.String())
	}
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || saved.ID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("saved observation = %+v, error = %v", saved, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/observations/00000000-0000-0000-0000-000000000001", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "use postgres") {
		t.Fatalf("get response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPObservationRequiresSession(t *testing.T) {
	h := testHandler(func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodPost, "/api/observations", bytes.NewBufferString(`{"title":"decision","content":"use postgres"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "session_id is required") {
		t.Fatalf("save without session response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPCORSAllowsConfiguredOrigin(t *testing.T) {
	cfg := config.Config{HTTP: config.HTTPConfig{Token: "test-token", AllowedOrigins: []string{"http://localhost:5173"}}, Search: config.SearchConfig{DefaultLimit: 20, MaxLimit: 100}}
	h, transport := newHTTPHandler(cfg, newFakeOperations(), func(context.Context) error { return nil })
	t.Cleanup(func() { _ = transport.Shutdown(context.Background()) })

	req := httptest.NewRequest(http.MethodOptions, "/api/observations", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "http://localhost:5173" {
		t.Fatalf("allow origin = %q", origin)
	}
}

func TestMCPInitializeAndListServerTools(t *testing.T) {
	h := testHandler(func(context.Context) error { return nil })
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"serverInfo"`) {
		t.Fatalf("initialize response = %d %s", rec.Code, rec.Body.String())
	}

	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(list))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if sessionID := rec.Header().Get("Mcp-Session-Id"); sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list response = %d %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"cortex_save", "cortex_session_start", "cortex_search", "cortex_get_observation", "cortex_update", "cortex_delete", "cortex_relate", "cortex_graph", "cortex_score"} {
		if !strings.Contains(rec.Body.String(), `"name":"`+name+`"`) {
			t.Errorf("tools/list missing %s: %s", name, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), `"mem_`) {
		t.Fatalf("tools/list exposed legacy namespace: %s", rec.Body.String())
	}
}

func TestEdgeResponseOmitsInternalIdentifiers(t *testing.T) {
	edge := &domain.Edge{
		ID: 1, PublicID: "00000000-0000-0000-0000-000000000001",
		FromObsID: 2, ToObsID: 3,
		FromPublicID: "00000000-0000-0000-0000-000000000002",
		ToPublicID:   "00000000-0000-0000-0000-000000000003",
		EvolutionID:  new(int64), TenantID: "tenant", WorkspaceID: "workspace",
	}
	b, err := json.Marshal(edgeResponse(edge))
	if err != nil {
		t.Fatal(err)
	}
	response := string(b)
	for _, internal := range []string{"from_obs_id", "to_obs_id", "evolution_id", "tenant_id", "workspace_id"} {
		if strings.Contains(response, internal) {
			t.Errorf("edge response exposed %s: %s", internal, response)
		}
	}
}

func TestValidateConfigRequiresToken(t *testing.T) {
	cfg := config.Config{
		Server: config.ServerConfig{
			Storage:          config.ServerStorageConfig{Driver: "postgres", DSN: "postgres://db/cortex"},
			TenantID:         "00000000-0000-0000-0000-000000000001",
			WorkspaceID:      "00000000-0000-0000-0000-000000000002",
			PrincipalSubject: "service",
			GrantDigest:      "digest",
			GrantVersion:     1,
			Roles:            []string{"owner"},
		},
		HTTP: config.HTTPConfig{Enabled: true, Host: "0.0.0.0", Port: 7438},
	}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("validateConfig error = %v, want token requirement", err)
	}
	cfg.HTTP.Host = "127.0.0.1"
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("loopback config error = %v, want token requirement", err)
	}
	cfg.HTTP.Token = "secret"
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("authenticated loopback config rejected: %v", err)
	}
}
