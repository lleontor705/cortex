package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func TestProjectContextAndSkillsMCPTools(t *testing.T) {
	ops := newFakeOperations()
	srv := mcpserver.NewMCPServer("cortex-test", "1.0.0")
	registerServerTools(srv, ops)

	ctx := context.Background()

	// 1. Test cortex_get_project_context
	contextHandler := getProjectContextTool(ops)
	res := callServerTool(t, contextHandler, map[string]any{"project": "cortex-core"})
	txt := serverToolText(res)
	if txt == "" {
		t.Fatalf("expected non-empty project context, got empty")
	}
	var ctxPayload domain.ProjectContext
	if err := json.Unmarshal([]byte(txt), &ctxPayload); err != nil {
		t.Fatalf("failed to decode project context JSON: %v", err)
	}
	if ctxPayload.Project != "cortex-core" {
		t.Fatalf("expected project 'cortex-core', got %q", ctxPayload.Project)
	}

	// 2. Test cortex_list_skills
	listSkillsHandler := listProjectSkillsTool(ops)
	resSkills := callServerTool(t, listSkillsHandler, map[string]any{"project": "cortex-core"})
	skillsTxt := serverToolText(resSkills)
	var skillsPayload []*domain.ProjectSkill
	if err := json.Unmarshal([]byte(skillsTxt), &skillsPayload); err != nil {
		t.Fatalf("failed to decode skills JSON: %v", err)
	}
	if len(skillsPayload) != 1 || skillsPayload[0].Key != "skill_1" {
		t.Fatalf("expected 1 skill with key 'skill_1', got %+v", skillsPayload)
	}

	// 3. Test cortex_get_skill
	getSkillHandler := getProjectSkillTool(ops)
	resSkill := callServerTool(t, getSkillHandler, map[string]any{"project": "cortex-core", "key": "skill_1"})
	skillTxt := serverToolText(resSkill)
	var skillPayload domain.ProjectSkill
	if err := json.Unmarshal([]byte(skillTxt), &skillPayload); err != nil {
		t.Fatalf("failed to decode skill JSON: %v", err)
	}
	if skillPayload.Key != "skill_1" || skillPayload.Content != "Skill Instructions" {
		t.Fatalf("unexpected skill content: %+v", skillPayload)
	}

	// 4. Test cortex_resolve_query
	resolveHandler := resolveQueryTool(ops)
	resResolve := callServerTool(t, resolveHandler, map[string]any{"query": "architecture", "project": "cortex-core"})
	resolveTxt := serverToolText(resResolve)
	if resolveTxt == "" {
		t.Fatalf("expected non-empty resolve text")
	}
	var resolvePayload map[string]any
	if err := json.Unmarshal([]byte(resolveTxt), &resolvePayload); err != nil {
		t.Fatalf("failed to decode resolve JSON: %v", err)
	}
	if resolvePayload["mode"] != "server" || resolvePayload["database"] != "postgresql" {
		t.Fatalf("unexpected resolve payload mode/db: %+v", resolvePayload)
	}

	// 5. Test cortex_get_status
	statusHandler := getStatusTool(ops)
	resStatus := callServerTool(t, statusHandler, map[string]any{})
	statusTxt := serverToolText(resStatus)
	var statusPayload map[string]any
	if err := json.Unmarshal([]byte(statusTxt), &statusPayload); err != nil {
		t.Fatalf("failed to decode status JSON: %v", err)
	}
	if statusPayload["mode"] != "server" || statusPayload["database"] != "postgresql" {
		t.Fatalf("unexpected status payload: %+v", statusPayload)
	}

	_ = ctx
}

func TestProjectArtifactsRESTEndpoints(t *testing.T) {
	ops := newFakeOperations()
	handler, _ := newVerifiedHTTPHandler(config.Config{
		HTTP:   config.HTTPConfig{Token: "test-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
	}, ops, func(_ context.Context) error { return nil })

	// 1. GET /api/projects/context
	req := httptest.NewRequest(http.MethodGet, "/api/projects/context?project=cortex-core", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects/context returned %d, want 200", rec.Code)
	}

	// 2. GET /api/projects/artifacts
	req = httptest.NewRequest(http.MethodGet, "/api/projects/artifacts?project=cortex-core", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects/artifacts returned %d, want 200", rec.Code)
	}

	// 3. POST /api/projects/artifacts
	saveBody := domain.SaveProjectArtifactInput{
		Kind:        "rule",
		Key:         "arch_rule_1",
		Title:       "Architecture Rule",
		Description: "Zero CGO and Clean Architecture",
		Content:     "All local code must remain zero-CGO.",
		Scope:       "project",
		Project:     "cortex-core",
	}
	b, _ := json.Marshal(saveBody)
	req = httptest.NewRequest(http.MethodPost, "/api/projects/artifacts", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/projects/artifacts returned %d, want 200", rec.Code)
	}

	// 4. DELETE /api/projects/artifacts/{id}
	req = httptest.NewRequest(http.MethodDelete, "/api/projects/artifacts/00000000-0000-0000-0000-000000000099", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/projects/artifacts/{id} returned %d, want 200", rec.Code)
	}
}
