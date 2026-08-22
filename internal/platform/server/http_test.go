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

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/identity"
	"github.com/lleontor705/cortex/internal/mcp/memorycontract"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

type fakeOperations struct {
	observations     map[int64]*domain.Observation
	nextID           int64
	subgraph         *domain.GraphSubgraph
	subgraphID       string
	subgraphDepth    int
	subgraphMax      int
	createdEdge      *domain.Edge
	issuedToken      identity.TokenIssue
	issueTokenErr    error
	saveEffectErr    error
	saveEffectStatus domain.WriteStatus
	handoffRequest   domain.HandoffRequest
	handoffResult    domain.ObservationWriteResult
	handoffErr       error
	searchErr        error
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
func (f *fakeOperations) PushSync(_ context.Context, batch *domain.SyncBatch) (*domain.SyncResult, error) {
	return &domain.SyncResult{Accepted: len(batch.Sessions) + len(batch.Observations) + len(batch.Prompts) + len(batch.Edges)}, nil
}
func (f *fakeOperations) PullSync(_ context.Context, cursor int64, _ int) (*domain.SyncPage, error) {
	return &domain.SyncPage{Cursor: cursor}, nil
}
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
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return []*domain.SearchResult{}, nil
}

func (f *fakeOperations) CreateGraphEdge(_ context.Context, edge *domain.Edge) error {
	copy := *edge
	f.createdEdge = &copy
	return nil
}
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
func (f *fakeOperations) GetGraphSubgraph(_ context.Context, id string, depth, maxNodes int) (*domain.GraphSubgraph, error) {
	f.subgraphID, f.subgraphDepth, f.subgraphMax = id, depth, maxNodes
	if f.subgraph != nil {
		return f.subgraph, nil
	}
	return &domain.GraphSubgraph{}, nil
}

func callServerTool(t *testing.T, handler mcpserver.ToolHandlerFunc, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = arguments
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func serverToolText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
func (f *fakeOperations) CreateUser(context.Context, identity.UserCreate) (identity.UserRecord, error) {
	return identity.UserRecord{}, nil
}
func (f *fakeOperations) ListUsers(context.Context) ([]identity.UserRecord, error) { return nil, nil }
func (f *fakeOperations) SetUserActive(context.Context, string, bool) error        { return nil }
func (f *fakeOperations) IssueToken(_ context.Context, input identity.TokenIssue) (identity.IssuedToken, error) {
	f.issuedToken = input
	return identity.IssuedToken{}, f.issueTokenErr
}
func (f *fakeOperations) ListTokens(context.Context) ([]identity.TokenRecord, error) { return nil, nil }
func (f *fakeOperations) RevokeToken(context.Context, string) error                  { return nil }
func (f *fakeOperations) RotateToken(context.Context, string) (identity.IssuedToken, error) {
	return identity.IssuedToken{}, nil
}

func (f *fakeOperations) SaveObservationWithEffect(ctx context.Context, o *domain.Observation) (domain.SaveEffect, error) {
	if f.saveEffectErr != nil {
		return domain.SaveEffect{}, f.saveEffectErr
	}
	if err := f.SaveObservation(ctx, o); err != nil {
		return domain.SaveEffect{}, err
	}
	status := f.saveEffectStatus
	if status == "" {
		status = domain.WriteStatusCreated
	}
	return domain.SaveEffect{Observation: o, Status: status}, nil
}

func (f *fakeOperations) ExecuteHandoff(_ context.Context, request domain.HandoffRequest) (domain.ObservationWriteResult, error) {
	f.handoffRequest = request
	if f.handoffErr != nil {
		return domain.ObservationWriteResult{}, f.handoffErr
	}
	return f.handoffResult, nil
}

func (f *fakeOperations) GetProjectContext(_ context.Context, project string) (*domain.ProjectContext, error) {
	return &domain.ProjectContext{
		Project:      project,
		SystemPrompt: "Test System Prompt for " + project,
		Rules:        []domain.ProjectRule{{Key: "rule_1", Title: "Rule 1", Content: "Rule Content", Scope: "project"}},
		Skills:       []domain.ProjectSkillSummary{{Key: "skill_1", Title: "Skill 1", Description: "Test Skill", Scope: "project", Project: project}},
	}, nil
}

func (f *fakeOperations) ListProjectSkills(_ context.Context, project string) ([]*domain.ProjectSkill, error) {
	return []*domain.ProjectSkill{
		{ID: "00000000-0000-0000-0000-000000000099", Key: "skill_1", Title: "Skill 1", Description: "Test Skill", Content: "Skill Instructions", Scope: "project", Project: project},
	}, nil
}

func (f *fakeOperations) GetProjectSkill(_ context.Context, project, key string) (*domain.ProjectSkill, error) {
	return &domain.ProjectSkill{
		ID: "00000000-0000-0000-0000-000000000099", Key: key, Title: "Skill 1", Description: "Test Skill", Content: "Skill Instructions", Scope: "project", Project: project,
	}, nil
}

func (f *fakeOperations) SaveProjectArtifact(_ context.Context, in domain.SaveProjectArtifactInput) (*domain.ProjectArtifactItem, error) {
	return &domain.ProjectArtifactItem{
		ID: "00000000-0000-0000-0000-000000000099", Kind: in.Kind, Key: in.Key, Title: in.Title, Description: in.Description, Content: in.Content, Scope: in.Scope, Project: in.Project, Revision: 1, Status: "active",
	}, nil
}

func (f *fakeOperations) ListProjectArtifacts(_ context.Context, project string, kind string) ([]*domain.ProjectArtifactItem, error) {
	return []*domain.ProjectArtifactItem{
		{ID: "00000000-0000-0000-0000-000000000099", Kind: "rule", Key: "rule_1", Title: "Rule 1", Content: "Rule Content", Scope: "project", Project: project, Revision: 1, Status: "active"},
	}, nil
}

func (f *fakeOperations) DeleteProjectArtifact(_ context.Context, id string, reason string) error {
	return nil
}

func (f *fakeOperations) GetUserProfile(_ context.Context, id string) (*identity.UserRecord, error) {
	return &identity.UserRecord{
		ID:          id,
		Email:       "test@example.com",
		DisplayName: "Test User",
		Active:      true,
	}, nil
}


func testHandler(health healthCheck) http.Handler {
	h, _ := newVerifiedHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}, Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20}}, newFakeOperations(), health)
	return h
}

// newVerifiedHTTPHandler mirrors Open's production wiring: every request
// bearer is verified through requestAuthenticator before any operations are
// composed, and routes delegate through requestOperations. The stub verifier
// accepts exactly the configured token. There is deliberately no
// static-compare constructor to test with — that path was removed.
func newVerifiedHTTPHandler(cfg config.Config, ops Operations, health healthCheck) (http.Handler, *mcpserver.StreamableHTTPServer) {
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != cfg.HTTP.Token {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "00000000-0000-0000-0000-0000000000f1", OrgID: "00000000-0000-0000-0000-000000000001"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			return ops, nil
		}),
	}
	return newHTTPHandlerWithAuth(cfg, requestOperations{}, health, auth.middleware)
}

// TestConfiguredBearerHasNoStaticBypassAtHTTPWiring pins the IDP-T03B
// invariant at the HTTP layer: presenting the configured bearer byte-for-byte
// can never authenticate without verifier approval. No wiring exists that
// compares the presented secret against the configured token.
func TestConfiguredBearerHasNoStaticBypassAtHTTPWiring(t *testing.T) {
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) {
			return domain.Principal{}, errors.New("unknown credential")
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			t.Fatal("operation factory called for unverified bearer")
			return nil, nil
		}),
	}
	h, _ := newHTTPHandlerWithAuth(config.Config{HTTP: config.HTTPConfig{Token: "configured-static-compare-token"}}, requestOperations{}, func(context.Context) error { return nil }, auth.middleware)
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	req.Header.Set("Authorization", "Bearer configured-static-compare-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("configured bearer without verification status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
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

func TestIssueTokenRejectsInvalidSubjectUUID(t *testing.T) {
	ops := newFakeOperations()
	h, _ := newVerifiedHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}, ops, func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens", strings.NewReader(`{"subject":"not-a-uuid","name":"agent"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if ops.issuedToken.Subject != "" {
		t.Fatal("invalid subject reached token operations")
	}
}

func TestIssueTokenMapsMissingSubjectToNotFound(t *testing.T) {
	ops := newFakeOperations()
	ops.issueTokenErr = domain.ErrNotFound
	h, _ := newVerifiedHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}, ops, func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tokens", strings.NewReader(`{"subject":"00000000-0000-0000-0000-000000000123","name":"agent"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestTokenResponseUsesNullForUnsetLifecycleTimes(t *testing.T) {
	response := tokenResponse(identity.TokenRecord{})
	for _, field := range []string{"expires_at", "revoked_at", "last_used_at"} {
		if response[field] != nil {
			t.Errorf("%s = %v, want nil", field, response[field])
		}
	}
}

func TestHTTPAPIBearerAuthAndObservationREST(t *testing.T) {
	ops := newFakeOperations()
	h, _ := newVerifiedHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}, ops, func(context.Context) error { return nil })

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

func TestHTTPGraphSubgraphRoute(t *testing.T) {
	ops := newFakeOperations()
	ops.subgraph = &domain.GraphSubgraph{Root: "observation:00000000-0000-0000-0000-000000000001"}
	h, _ := newVerifiedHTTPHandler(config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}, ops, func(context.Context) error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/api/graph/00000000-0000-0000-0000-000000000001/subgraph?depth=2&max_nodes=50", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ops.subgraph.Root) {
		t.Fatalf("subgraph response = %d %s", rec.Code, rec.Body.String())
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
	h, transport := newVerifiedHTTPHandler(cfg, newFakeOperations(), func(context.Context) error { return nil })
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
	for _, name := range []string{"cortex_save", "cortex_session_start", "cortex_search", "cortex_get_observation", "cortex_update", "cortex_delete", "cortex_relate", "cortex_graph", "cortex_graph_subgraph", "cortex_score", "cortex_handoff"} {
		if !strings.Contains(rec.Body.String(), `"name":"`+name+`"`) {
			t.Errorf("tools/list missing %s: %s", name, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), `"mem_`) {
		t.Fatalf("tools/list exposed legacy namespace: %s", rec.Body.String())
	}
}

func TestMCPGraphSubgraphToolDelegatesBounds(t *testing.T) {
	ops := newFakeOperations()
	ops.subgraph = &domain.GraphSubgraph{Root: "observation:root", Truncated: true}
	result := callServerTool(t, graphSubgraphTool(ops), map[string]any{"observation_id": "root", "depth": float64(3), "max_nodes": float64(40)})

	if ops.subgraphID != "root" || ops.subgraphDepth != 3 || ops.subgraphMax != 40 {
		t.Fatalf("subgraph arguments = %q, %d, %d", ops.subgraphID, ops.subgraphDepth, ops.subgraphMax)
	}
	if text := serverToolText(result); !strings.Contains(text, `"root":"observation:root"`) || !strings.Contains(text, `"truncated":true`) {
		t.Fatalf("subgraph result = %s", text)
	}
}

func TestMCPRelateToolPreservesMetadataAndRejectsInvalidRanges(t *testing.T) {
	ops := newFakeOperations()
	from := &domain.Observation{Title: "from", Content: "from"}
	to := &domain.Observation{Title: "to", Content: "to"}
	if err := ops.SaveObservation(context.Background(), from); err != nil {
		t.Fatal(err)
	}
	if err := ops.SaveObservation(context.Background(), to); err != nil {
		t.Fatal(err)
	}
	result := callServerTool(t, relateTool(ops), map[string]any{"from_id": from.PublicID, "to_id": to.PublicID, "relation_type": "references", "weight": 2.5, "confidence": 0.7, "source": "ai", "reasoning": "shared contract"})
	if ops.createdEdge == nil || ops.createdEdge.Weight != 2.5 || ops.createdEdge.Confidence != 0.7 || ops.createdEdge.Source != "ai" || ops.createdEdge.Reasoning != "shared contract" {
		t.Fatalf("created edge = %+v", ops.createdEdge)
	}
	if text := serverToolText(result); !strings.Contains(text, `"weight":2.5`) || !strings.Contains(text, `"reasoning":"shared contract"`) {
		t.Fatalf("relate result = %s", text)
	}

	invalid := newFakeOperations()
	invalidResult := callServerTool(t, relateTool(invalid), map[string]any{"from_id": "from", "to_id": "to", "relation_type": "references", "confidence": 1.2})
	if invalid.createdEdge != nil || !strings.Contains(serverToolText(invalidResult), "confidence") {
		t.Fatalf("invalid result = %s, edge = %+v", serverToolText(invalidResult), invalid.createdEdge)
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

func TestRespondOperationErrorMapsAuthorizationDenialToForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	respondOperationError(rec, errors.New(authz.DenyRole))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("authorization denial response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestValidateConfigRequiresToken(t *testing.T) {
	cfg := config.Config{
		Server: config.ServerConfig{
			Storage:          config.ServerStorageConfig{Driver: "postgres", DSN: "postgres://db/cortex"},
			TenantID:         "00000000-0000-0000-0000-000000000001",
			WorkspaceID:      "00000000-0000-0000-0000-000000000002",
			PrincipalSubject: "00000000-0000-0000-0000-000000000003",
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
	cfg.HTTP.Token = "short"
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("short bearer error = %v, want bearer length requirement", err)
	}
	if err := validateConfig(cfg); err != nil && strings.Contains(err.Error(), "short") {
		t.Fatalf("bearer length error echoes the bearer: %v", err)
	}
	cfg.HTTP.Token = "configured-bootstrap-bearer"
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("authenticated loopback config rejected: %v", err)
	}
}

// TestIssuedTokenResponsePopulatesIdentityFields pins the public rotate/issue
// response contract: the rotated credential must identify its subject exactly
// like a freshly issued one (identity fields never empty).
func TestIssuedTokenResponsePopulatesIdentityFields(t *testing.T) {
	issued := identity.IssuedToken{
		Secret: "ctx_rotated_secret_value",
		Record: identity.TokenRecord{
			ID:            "00000000-0000-0000-0000-0000000000aa",
			Name:          "agent",
			Subject:       "00000000-0000-0000-0000-000000000009",
			PrincipalType: "service_account",
		},
	}
	response := issuedTokenResponse(issued)
	if response["subject"] != issued.Record.Subject {
		t.Fatalf("subject = %v, want %q", response["subject"], issued.Record.Subject)
	}
	if response["principal_type"] != issued.Record.PrincipalType {
		t.Fatalf("principal_type = %v, want %q", response["principal_type"], issued.Record.PrincipalType)
	}
	if response["secret"] != issued.Secret {
		t.Fatalf("secret = %v, want the issued one-time secret", response["secret"])
	}
}

// --- R7: durable memory write surface (REM-AUTH-001, REM-MCP-001, RD5/RD6) ---

// serverNormalizedJSON canonicalizes raw JSON so schema comparisons are
// insensitive to re-serialization by the MCP server.
func serverNormalizedJSON(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		t.Fatalf("JSON is not valid: %v", err)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatalf("renormalize JSON: %v", err)
	}
	return encoded
}

func serverRawOutputSchema(t *testing.T, tool *mcpserver.ServerTool) json.RawMessage {
	t.Helper()
	if tool == nil {
		return nil
	}
	raw := tool.Tool.RawOutputSchema
	if len(raw) == 0 && tool.Tool.OutputSchema.Type != "" {
		encoded, err := json.Marshal(tool.Tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal output schema: %v", err)
		}
		raw = encoded
	}
	return serverNormalizedJSON(t, raw)
}

func serverRawInputSchema(t *testing.T, tool *mcpserver.ServerTool) json.RawMessage {
	t.Helper()
	if tool == nil {
		return nil
	}
	raw := tool.Tool.RawInputSchema
	if len(raw) == 0 {
		encoded, err := json.Marshal(tool.Tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema: %v", err)
		}
		raw = encoded
	}
	return serverNormalizedJSON(t, raw)
}

// TestServerToolsPublishMemoryContractParity asserts the server MCP publishes
// cortex_save and cortex_handoff with the exact shared schemas and hint
// annotations from memorycontract (REM-MCP-001: three-route parity).
func TestServerToolsPublishMemoryContractParity(t *testing.T) {
	srv := newServerMCP(newFakeOperations())

	save := srv.GetTool(memorycontract.ToolSave)
	if save == nil {
		t.Fatal("cortex_save is not registered on the server MCP")
	}
	if got, want := serverRawOutputSchema(t, save), serverNormalizedJSON(t, memorycontract.WriteOutputSchemaJSON); string(got) != string(want) {
		t.Errorf("cortex_save outputSchema = %s, want shared memorycontract schema %s", got, want)
	}
	annotations := save.Tool.Annotations
	if annotations.Title != memorycontract.SaveHints.Title {
		t.Errorf("cortex_save title = %q, want %q", annotations.Title, memorycontract.SaveHints.Title)
	}
	if annotations.IdempotentHint != nil && *annotations.IdempotentHint {
		t.Errorf("cortex_save annotations = %+v, save is not idempotent", annotations)
	}

	handoff := srv.GetTool(memorycontract.ToolHandoff)
	if handoff == nil {
		t.Fatal("cortex_handoff is not registered on the server MCP (REM-AUTH-001 end-to-end handoff)")
	}
	if got, want := serverRawOutputSchema(t, handoff), serverNormalizedJSON(t, memorycontract.WriteOutputSchemaJSON); string(got) != string(want) {
		t.Errorf("cortex_handoff outputSchema = %s, want shared memorycontract schema %s", got, want)
	}
	if got, want := serverRawInputSchema(t, handoff), serverNormalizedJSON(t, memorycontract.HandoffInputSchemaJSON); string(got) != string(want) {
		t.Errorf("cortex_handoff inputSchema = %s, want shared memorycontract schema %s", got, want)
	}
	handoffAnnotations := handoff.Tool.Annotations
	if handoffAnnotations.Title != memorycontract.HandoffHints.Title {
		t.Errorf("cortex_handoff title = %q, want %q", handoffAnnotations.Title, memorycontract.HandoffHints.Title)
	}
	if handoffAnnotations.IdempotentHint == nil || !*handoffAnnotations.IdempotentHint {
		t.Errorf("cortex_handoff annotations = %+v, handoff must be idempotent", handoffAnnotations)
	}
	if handoffAnnotations.DestructiveHint != nil && *handoffAnnotations.DestructiveHint {
		t.Errorf("cortex_handoff annotations = %+v, handoff must not be destructive", handoffAnnotations)
	}
}

func structuredSavePayload(t *testing.T, result *mcp.CallToolResult) memorycontract.SaveStructured {
	t.Helper()
	payload, ok := result.StructuredContent.(memorycontract.SaveStructured)
	if !ok {
		t.Fatalf("structuredContent = %#v, want memorycontract.SaveStructured", result.StructuredContent)
	}
	return payload
}

func structuredErrorPayload(t *testing.T, result *mcp.CallToolResult) memorycontract.ErrorStructured {
	t.Helper()
	payload, ok := result.StructuredContent.(memorycontract.ErrorStructured)
	if !ok {
		t.Fatalf("structuredContent = %#v, want memorycontract.ErrorStructured", result.StructuredContent)
	}
	return payload
}

// TestMCPSaveToolReturnsStructuredPublicRef asserts the server cortex_save
// lowers the durable write effect into the shared structured payload with the
// public UUID namespace exclusively (REM-SAVE-001, REM-MCP-001).
func TestMCPSaveToolReturnsStructuredPublicRef(t *testing.T) {
	ops := newFakeOperations()
	result := callServerTool(t, saveTool(ops), map[string]any{
		"title": "JWT auth", "content": "body", "type": "decision",
		"session_id": "00000000-0000-0000-0000-000000000010",
	})
	if result.IsError {
		t.Fatalf("save failed: %s", serverToolText(result))
	}
	payload := structuredSavePayload(t, result)
	if payload.ObservationRef.PublicID == nil || payload.ObservationRef.LocalID != nil {
		t.Fatalf("observation_ref = %+v, want exclusive public_id namespace", payload.ObservationRef)
	}
	if parsed, err := uuid.Parse(*payload.ObservationRef.PublicID); err != nil || parsed.String() != *payload.ObservationRef.PublicID {
		t.Fatalf("public_id = %q, want canonical UUID", *payload.ObservationRef.PublicID)
	}
	if payload.Status != string(domain.WriteStatusCreated) {
		t.Fatalf("status = %q, want created", payload.Status)
	}
	if text := serverToolText(result); !strings.Contains(text, "Memory saved") {
		t.Fatalf("legacy text = %q, want useful save confirmation", text)
	}
}

// TestMCPSaveToolClassifiesFailures asserts authorization denials and invalid
// input keep isError semantics, the stable class, and no reference.
func TestMCPSaveToolClassifiesFailures(t *testing.T) {
	forbidden := newFakeOperations()
	forbidden.saveEffectErr = errors.New(authz.DenyRole)
	result := callServerTool(t, saveTool(forbidden), map[string]any{"title": "t", "content": "c", "session_id": "00000000-0000-0000-0000-000000000010"})
	if !result.IsError {
		t.Fatalf("authorization denial must be an error result: %s", serverToolText(result))
	}
	payload := structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeForbidden {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeForbidden)
	}
	if strings.Contains(serverToolText(result), "role_not_permitted") {
		t.Fatalf("raw denial reason leaked: %s", serverToolText(result))
	}

	invalid := newFakeOperations()
	invalid.saveEffectErr = domain.ErrInvalidInput
	result = callServerTool(t, saveTool(invalid), map[string]any{"title": "", "content": "c", "session_id": "00000000-0000-0000-0000-000000000010"})
	if !result.IsError {
		t.Fatal("invalid input must be an error result")
	}
	payload = structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeValidation {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
	}
}

// TestMCPHandoffToolLowersPublicNamespace asserts the server cortex_handoff
// lowers tool arguments into a domain HandoffRequest, executes through
// Operations, and returns the public-namespace structured result.
func TestMCPHandoffToolLowersPublicNamespace(t *testing.T) {
	target := "00000000-0000-0000-0000-0000000000ab"
	publicRef, refErr := uuid.Parse("00000000-0000-0000-0000-0000000000cd")
	if refErr != nil {
		t.Fatal(refErr)
	}
	ops := newFakeOperations()
	ops.handoffResult = domain.ObservationWriteResult{
		Ref:    domain.ObservationRef{PublicID: &publicRef},
		Status: domain.WriteStatusReplayed,
	}
	result := callServerTool(t, handoffTool(ops), map[string]any{
		"idempotency_key": "key-1",
		"observation": map[string]any{
			"title": "Handoff", "content": "body", "project": "cortex",
			"type": "decision", "confidence": 0.9, "tags": []any{"auth", "mcp"},
		},
		"relation": map[string]any{
			"target":     map[string]any{"public_id": target},
			"type":       "references",
			"weight":     2.5,
			"confidence": 0.5,
			"reasoning":  "shared contract",
		},
	})
	if result.IsError {
		t.Fatalf("handoff failed: %s", serverToolText(result))
	}
	if ops.handoffRequest.IdempotencyKey != "key-1" {
		t.Fatalf("idempotency key = %q", ops.handoffRequest.IdempotencyKey)
	}
	obs := ops.handoffRequest.Observation
	if obs.Title != "Handoff" || obs.Content != "body" || obs.Project != "cortex" || obs.Type != "decision" || obs.Confidence != 0.9 {
		t.Fatalf("lowered observation = %+v", obs)
	}
	if len(obs.Tags) != 2 || obs.Tags[0] != "auth" || obs.Tags[1] != "mcp" {
		t.Fatalf("lowered tags = %+v", obs.Tags)
	}
	rel := ops.handoffRequest.Relation
	if rel == nil || rel.Type != "references" || rel.Weight != 2.5 || rel.Confidence != 0.5 || rel.Reasoning != "shared contract" {
		t.Fatalf("lowered relation = %+v", rel)
	}
	if rel.Target.PublicID == nil || rel.Target.PublicID.String() != target || rel.Target.LocalID != nil {
		t.Fatalf("relation target = %+v, want exclusive public namespace", rel.Target)
	}
	payload := structuredSavePayload(t, result)
	if payload.ObservationRef.PublicID == nil || *payload.ObservationRef.PublicID != publicRef.String() {
		t.Fatalf("observation_ref = %+v, want %q", payload.ObservationRef, publicRef.String())
	}
	if payload.Status != string(domain.WriteStatusReplayed) {
		t.Fatalf("status = %q, want replayed", payload.Status)
	}
	if text := serverToolText(result); !strings.Contains(text, "Handoff recorded") {
		t.Fatalf("legacy text = %q, want useful handoff confirmation", text)
	}
}

// TestMCPHandoffToolValidationAndAuthorization asserts the server handoff
// namespace rules: public targets only, validation before execution, and
// stable fail-closed classifications without references.
func TestMCPHandoffToolValidationAndAuthorization(t *testing.T) {
	localTarget := newFakeOperations()
	result := callServerTool(t, handoffTool(localTarget), map[string]any{
		"idempotency_key": "key-1",
		"observation":     map[string]any{"title": "T", "content": "C"},
		"relation":        map[string]any{"target": map[string]any{"local_id": float64(7)}, "type": "references"},
	})
	if !result.IsError {
		t.Fatal("local_id target must be rejected on the server namespace")
	}
	if localTarget.handoffRequest.IdempotencyKey != "" {
		t.Fatal("rejected request must not reach operations")
	}
	payload := structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeValidation {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
	}

	missingKey := newFakeOperations()
	result = callServerTool(t, handoffTool(missingKey), map[string]any{
		"observation": map[string]any{"title": "T", "content": "C"},
	})
	if !result.IsError {
		t.Fatal("missing idempotency key must be rejected")
	}
	payload = structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeValidation {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
	}

	forbidden := newFakeOperations()
	forbidden.handoffErr = errors.New(authz.DenyRole)
	result = callServerTool(t, handoffTool(forbidden), map[string]any{
		"idempotency_key": "key-1",
		"observation":     map[string]any{"title": "T", "content": "C"},
	})
	if !result.IsError {
		t.Fatal("authorization denial must be an error result")
	}
	payload = structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeForbidden {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeForbidden)
	}

	unavailable := newFakeOperations()
	unavailable.handoffErr = domain.ErrHandoffUnavailable
	result = callServerTool(t, handoffTool(unavailable), map[string]any{
		"idempotency_key": "key-1",
		"observation":     map[string]any{"title": "T", "content": "C"},
	})
	if !result.IsError {
		t.Fatal("auth dependency failure must be an error result")
	}
	payload = structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeUnavailable || !payload.Error.Retryable {
		t.Fatalf("error body = %+v, want retryable unavailable", payload.Error)
	}
}

// TestMCPHandoffToolRelationArgumentContract pins the relation argument shape
// on the server runtime: a present-but-non-object relation is validation
// (never silently omitted) and the target must set exactly one namespace —
// both or neither fail before Operations is reached (review R7 fix 2).
func TestMCPHandoffToolRelationArgumentContract(t *testing.T) {
	cases := []struct {
		name     string
		relation any
		message  string
	}{
		{"relation not an object", "references", "relation must be an object"},
		{"target with both namespaces", map[string]any{
			"target": map[string]any{"local_id": float64(3), "public_id": "00000000-0000-0000-0000-0000000000ab"},
			"type":   "references",
		}, "exactly one of public_id or local_id"},
		{"target with neither namespace", map[string]any{
			"target": map[string]any{},
			"type":   "references",
		}, "exactly one of public_id or local_id"},
		{"public_id not a uuid string", map[string]any{
			"target": map[string]any{"public_id": float64(42)},
			"type":   "references",
		}, "public_id must be a UUID"},
		{"local namespace rejected", map[string]any{
			"target": map[string]any{"local_id": float64(7)},
			"type":   "references",
		}, "server namespace accepts public_id targets only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops := newFakeOperations()
			result := callServerTool(t, handoffTool(ops), map[string]any{
				"idempotency_key": "rel-contract",
				"observation":     map[string]any{"title": "T", "content": "C"},
				"relation":        tc.relation,
			})
			payload := structuredErrorPayload(t, result)
			if payload.Error.Code != memorycontract.CodeValidation {
				t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
			}
			if !strings.Contains(payload.Error.Message, tc.message) {
				t.Fatalf("message = %q, want it to contain %q", payload.Error.Message, tc.message)
			}
			if ops.handoffRequest.IdempotencyKey != "" {
				t.Fatal("rejected relation shape reached operations")
			}
		})
	}
}

// TestRequestOperationsExposeSaveEffectAndHandoff asserts the capability-only
// transport operations delegate the durable write surface to the
// principal-scoped AuthorizedStore and fail closed without one.
func TestRequestOperationsExposeSaveEffectAndHandoff(t *testing.T) {
	request := domain.HandoffRequest{IdempotencyKey: "k"}
	if _, err := (requestOperations{}).ExecuteHandoff(context.Background(), request); err == nil {
		t.Fatal("handoff without authenticated operations must fail closed")
	}
	observation := &domain.Observation{Title: "t", Content: "c"}
	if _, err := (requestOperations{}).SaveObservationWithEffect(context.Background(), observation); err == nil {
		t.Fatal("save without authenticated operations must fail closed")
	}

	publicRef, refErr := uuid.Parse("00000000-0000-0000-0000-0000000000ef")
	if refErr != nil {
		t.Fatal(refErr)
	}
	ops := newFakeOperations()
	ops.handoffResult = domain.ObservationWriteResult{
		Ref:    domain.ObservationRef{PublicID: &publicRef},
		Status: domain.WriteStatusCreated,
	}
	ctx := withOperations(context.Background(), ops)
	result, err := requestOperations{}.ExecuteHandoff(ctx, request)
	if err != nil {
		t.Fatalf("delegated handoff: %v", err)
	}
	if result.Ref.PublicID == nil || result.Ref.PublicID.String() != publicRef.String() || result.Status != domain.WriteStatusCreated {
		t.Fatalf("delegated result = %+v", result)
	}
	if ops.handoffRequest.IdempotencyKey != "k" {
		t.Fatalf("delegated request = %+v", ops.handoffRequest)
	}

	effect, err := requestOperations{}.SaveObservationWithEffect(ctx, observation)
	if err != nil {
		t.Fatalf("delegated save: %v", err)
	}
	if effect.Observation == nil || effect.Observation.PublicID == "" || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("delegated effect = %+v", effect)
	}
}

// --- T08: standardized, redacted public errors ------------------------------

// serverCanaryErrorTexts is the raw-cause corpus that must never surface in
// any public response: driver/SQL fragments, a DSN, a credential, a path, a
// URL with userinfo, an IP, and an upstream body.
var serverCanaryErrorTexts = []string{
	"pq: duplicate key value violates unique constraint (SQLSTATE 23505)",
	"postgres://svc:cortex-pass@10.9.8.7:5432/cortex?sslmode=disable",
	"Bearer sk-server-canary-42",
	`/var/lib/cortex/secrets/token.txt`,
	"http://169.254.169.254/latest/meta-data/",
	"169.254.169.254",
	`{"upstream":"secret body canary"}`,
}

func serverCanaryError() error {
	return errors.New(strings.Join(serverCanaryErrorTexts, " | "))
}

func assertNoServerCanaries(t *testing.T, text string) {
	t.Helper()
	for _, canary := range serverCanaryErrorTexts {
		if strings.Contains(text, canary) {
			t.Fatalf("canary %q leaked into public output: %q", canary, text)
		}
	}
}

// TestMCPToolResultErrorClassificationAndRedactionCanary proves the generic
// server MCP tool error path lowers failures into the shared structured error
// contract with a stable code and a constant, bounded message instead of the
// universal opaque text, and that raw causes never surface.
func TestMCPToolResultErrorClassificationAndRedactionCanary(t *testing.T) {
	ops := newFakeOperations()
	ops.searchErr = serverCanaryError()
	result := callServerTool(t, searchTool(ops), map[string]any{"query": "anything"})
	if !result.IsError {
		t.Fatalf("search failure must be an error result: %s", serverToolText(result))
	}
	payload := structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodePersistence {
		t.Fatalf("code = %q, want %q", payload.Error.Code, memorycontract.CodePersistence)
	}
	text := serverToolText(result)
	if !strings.Contains(text, payload.Error.Code) {
		t.Fatalf("error text must carry the stable code, got %q", text)
	}
	assertNoServerCanaries(t, text)

	missing := newFakeOperations()
	result = callServerTool(t, getTool(missing), map[string]any{"id": "00000000-0000-0000-0000-000000000abc"})
	if !result.IsError {
		t.Fatalf("missing observation must be an error result: %s", serverToolText(result))
	}
	payload = structuredErrorPayload(t, result)
	if payload.Error.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", payload.Error.Code)
	}
	assertNoServerCanaries(t, serverToolText(result))

	forbidden := newFakeOperations()
	forbidden.searchErr = errors.New(authz.DenyRole)
	result = callServerTool(t, searchTool(forbidden), map[string]any{"query": "anything"})
	if !result.IsError {
		t.Fatal("authorization denial must be an error result")
	}
	payload = structuredErrorPayload(t, result)
	if payload.Error.Code != memorycontract.CodeForbidden {
		t.Fatalf("code = %q, want %q", payload.Error.Code, memorycontract.CodeForbidden)
	}
	assertNoServerCanaries(t, serverToolText(result))
}

// TestRESTOperationErrorRedactionCanary proves REST operation errors keep the
// stable coded shape and never echo raw internal causes.
func TestRESTOperationErrorRedactionCanary(t *testing.T) {
	rec := httptest.NewRecorder()
	respondOperationError(rec, serverCanaryError())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Error.Code != "operation_failed" || body.Error.Message != "operation failed" {
		t.Fatalf("coded error = %+v", body.Error)
	}
	assertNoServerCanaries(t, rec.Body.String())

	extractionRec := httptest.NewRecorder()
	respondExtractionError(extractionRec, serverCanaryError(), "extraction_failed")
	if extractionRec.Code != http.StatusBadRequest {
		t.Fatalf("extraction status = %d, want 400", extractionRec.Code)
	}
	if err := json.Unmarshal(extractionRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode extraction body %q: %v", extractionRec.Body.String(), err)
	}
	if body.Error.Code != "extraction_failed" || body.Error.Message != "request could not be processed" {
		t.Fatalf("extraction error = %+v", body.Error)
	}
	assertNoServerCanaries(t, extractionRec.Body.String())
}
