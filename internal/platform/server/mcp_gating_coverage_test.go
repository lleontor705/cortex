package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCoverageCleanProjectName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", ""},
		{"my-project", "my-project"},
		{"  spacey  ", "spacey"},
		{"/path/to/project.git", "project"},
		{"C:\\Users\\me\\repo\\myapp", "myapp"},
		{"repo/main", "main"},
		{"just-slashes//double/", "double"},
	}
	for _, tc := range tests {
		got := cleanProjectName(tc.input)
		if got != tc.want {
			t.Errorf("cleanProjectName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCoverageToolProjectFallbacks(t *testing.T) {
	fake := newFakeOperations()
	handler := sessionStartTool(fake)
	fields := []struct{ key, val string }{
		{"folder_name", "from-folder"},
		{"folder", "from-fold"},
		{"directory", "from-dir"},
		{"cwd", "from-cwd"},
		{"path", "from-path"},
	}
	for _, f := range fields {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{f.key: f.val, "summary": "s"}
		result, err := handler(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		text := serverToolText(result)
		if !strings.Contains(text, f.val) {
			t.Errorf("toolProject with %q=%q: result missing value, got %s", f.key, f.val, text)
		}
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"summary": "no-project"}
	handler(context.Background(), req)
}

func TestCoverageSessionStartTool(t *testing.T) {
	fake := newFakeOperations()
	handler := sessionStartTool(fake)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"project": "demo", "summary": "test session"}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if text := serverToolText(result); !strings.Contains(text, "demo") {
		t.Fatalf("session start missing project: %s", text)
	}
}

func TestCoverageUpdateAndDeleteTool(t *testing.T) {
	fake := newFakeOperations()
	fake.observations[1] = &domain.Observation{ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Title: "orig", Content: "content", Project: "demo"}

	updateH := updateTool(fake)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "00000000-0000-0000-0000-000000000001", "title": "updated", "content": "new content", "type": "bugfix", "project": "proj", "scope": "project", "topic_key": "k"}
	result, err := updateH(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(serverToolText(result), "updated") {
		t.Fatalf("update result: %s", serverToolText(result))
	}
	if fake.observations[1].Title != "updated" {
		t.Fatalf("update not applied: %s", fake.observations[1].Title)
	}

	deleteH := deleteTool(fake)
	req2 := mcp.CallToolRequest{}
	req2.Params.Arguments = map[string]any{"id": "00000000-0000-0000-0000-000000000001"}
	result2, err := deleteH(context.Background(), req2)
	if err != nil {
		t.Fatal(err)
	}
	text := serverToolText(result2)
	if !strings.Contains(text, "deleted") || !strings.Contains(text, "true") {
		t.Fatalf("delete result: %s", text)
	}
}

func TestCoverageToolIntBranches(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"n": 42}
	if got := toolInt(req, "n", 0); got != 42 {
		t.Fatalf("int: got %d", got)
	}
	req.Params.Arguments = map[string]any{"n": float64(3.14)}
	if got := toolInt(req, "n", 0); got != 3 {
		t.Fatalf("float64: got %d", got)
	}
	req.Params.Arguments = map[string]any{"n": int64(99)}
	if got := toolInt(req, "n", 0); got != 99 {
		t.Fatalf("int64: got %d", got)
	}
	if got := toolInt(req, "missing", 7); got != 7 {
		t.Fatalf("default: got %d", got)
	}
}

func TestCoverageToolTags(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"tags": "a, b , ,c"}
	got := toolTags(req, "tags")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("tags: %v", got)
	}
	req.Params.Arguments = map[string]any{"tags": ""}
	if got := toolTags(req, "tags"); got != nil {
		t.Fatalf("empty tags: %v", got)
	}
}

func TestCoverageToolBool(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"flag": true}
	if !toolBool(req, "flag", false) {
		t.Fatal("expected true")
	}
	if toolBool(req, "missing", false) {
		t.Fatal("expected false default")
	}
}

func TestCoverageClassifyMCPEncodedBody(t *testing.T) {
	lookup := func(name string) bool {
		return name == "cortex_project_artifact_save" || name == "cortex_project_protocol"
	}
	if c := classifyMCPEncodedBody(nil, lookup); c.Method != "" {
		t.Fatalf("nil body: %+v", c)
	}
	if c := classifyMCPEncodedBody([]byte(`{"method":"tools/call","params":{"name":"cortex_project_artifact_save"}}`), lookup); !c.Large {
		t.Fatalf("cortex_project_artifact_save should be large: %+v", c)
	}
	if c := classifyMCPEncodedBody([]byte(`{"method":"tools/call","params":{"name":"cortex_project_protocol"}}`), lookup); !c.Heavy {
		t.Fatalf("protocol should be heavy: %+v", c)
	}
	if c := classifyMCPEncodedBody([]byte(`{"method":"initialize"}`), lookup); !c.Initialize {
		t.Fatalf("initialize not detected: %+v", c)
	}
}

func TestCoverageSessionRegistryResolveNilRequest(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 1})
	mgr := reg.ResolveSessionIdManager(nil)
	if mgr == nil {
		t.Fatal("nil request must return maintenance manager")
	}
	id := mgr.Generate()
	if id == "" {
		t.Fatal("maintenance manager must issue IDs for the sweeper")
	}
}

func TestCoverageSessionRollbackGenerated(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 2, Total: 4})
	res := reg.reserve("principal-1")
	if res == nil {
		t.Fatal("reserve failed")
	}
	fingerprint := "principal-1"
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	mgr := reg.ResolveSessionIdManager(req)
	id := mgr.Generate()
	if id == "" {
		t.Fatal("Generate must issue an ID")
	}
	res.rollbackGenerated()
	if !reg.canCreate(fingerprint) {
		t.Fatal("rollback must return slot")
	}
}

func TestCoverageSessionEvictExpired(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{
		IdleTTL:      10 * time.Millisecond,
		AbsoluteTTL:  time.Hour,
		PerPrincipal: 1,
		Total:        2,
	})
	res := reg.reserve("p1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	reg.ResolveSessionIdManager(req).Generate()
	time.Sleep(15 * time.Millisecond)
	res2 := reg.reserve("p1")
	if res2 == nil {
		t.Fatal("expired session should free slot")
	}
	res2.release()
}

func TestCoverageRedactingLogger(t *testing.T) {
	logger := redactingLogger{next: coverageNoopLogger{}}
	logger.Infof("request with Bearer secret-key-12345 to %s", "internal.example.com:5432/sql")
	logger.Errorf("session abc-123 failed: %s", "driver: bad connection")
}

type coverageNoopLogger struct{}

func (coverageNoopLogger) Infof(string, ...any)  {}
func (coverageNoopLogger) Errorf(string, ...any) {}

func TestCoverageMCPAdmissionGrowBeyondLimit(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 1024, Global: 2048})
	h := adm.acquire("p1", 900)
	if h == nil {
		t.Fatal("acquire failed")
	}
	if h.grow(200) {
		t.Fatal("grow beyond limit should fail")
	}
	h.release()
}

func TestCoverageMCPSessionReservationFromContextEmpty(t *testing.T) {
	res := mcpSessionReservationFromContext(context.Background())
	if res != nil {
		t.Fatal("expected nil from empty context")
	}
}

func TestCoverageMCPSessionRegistryGlobalCap(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 4, Total: 2})
	res1 := reg.reserve("p1")
	if res1 == nil {
		t.Fatal("first reserve failed")
	}
	res2 := reg.reserve("p2")
	if res2 == nil {
		t.Fatal("second reserve failed")
	}
	res3 := reg.reserve("p3")
	if res3 != nil {
		res3.release()
		t.Fatal("global cap exceeded")
	}
	res1.release()
	res2.release()
}

func TestCoverageMCPSessionFingerprint(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	fp := mcpSessionFingerprint(req)
	if fp == "" {
		t.Fatal("fingerprint must not be empty")
	}
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req2.Header.Set("Authorization", "Bearer different-token")
	fp2 := mcpSessionFingerprint(req2)
	if fp == fp2 {
		t.Fatal("different tokens must yield different fingerprints")
	}
}

func TestCoverageSessionIdleEviction(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	res := reg.reserve("p1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	id := reg.ResolveSessionIdManager(req).Generate()
	if id == "" {
		t.Fatal("Generate failed")
	}
	reg.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	res2 := reg.reserve("p1")
	if res2 == nil {
		t.Fatal("idle eviction should free slot")
	}
	res2.release()
}

func TestCoverageClassifyMCPEncodedBodyEmptyLookup(t *testing.T) {
	if c := classifyMCPEncodedBody([]byte(`{"method":"tools/call","params":{"name":"cortex_save"}}`), nil); c.Large {
		t.Fatalf("nil lookup must not unlock large: %+v", c)
	}
}

func TestCoverageAdmissionHandleBecomeHeavyAlreadyHeavy(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 4096, Global: 8192})
	h := adm.acquire("p1", 100)
	if h == nil {
		t.Fatal("acquire failed")
	}
	if !h.becomeHeavy() {
		t.Fatal("first becomeHeavy must succeed")
	}
	if h.becomeHeavy() {
		t.Fatal("second becomeHeavy must fail")
	}
	h.release()
}

func TestCoverageMCPSessionAbsoluteTTL(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Hour, AbsoluteTTL: 30 * time.Minute, PerPrincipal: 2, Total: 4})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	res := reg.reserve(mcpSessionFingerprint(req))
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	id := reg.ResolveSessionIdManager(req).Generate()
	terminated, err := reg.ResolveSessionIdManager(req).Validate(id)
	if terminated || err != nil {
		t.Fatalf("fresh session should be valid: terminated=%v err=%v", terminated, err)
	}
	reg.now = func() time.Time { return time.Now().Add(31 * time.Minute) }
	terminated, _ = reg.ResolveSessionIdManager(req).Validate(id)
	if !terminated {
		t.Fatal("absolute TTL should terminate session")
	}
}

func TestCoverageMCPSessionReserveConcurrentSafety(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 10})
	res := reg.reserve("p1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	_ = reg.ResolveSessionIdManager(req).Generate()
	if _, err := reg.ResolveSessionIdManager(req).Validate("nonexistent"); err == nil {
	}
	res2 := reg.reserve("p1")
	if res2 != nil {
		res2.release()
		t.Fatal("per-principal cap must prevent second reservation")
	}
	res.release()
}

func TestCoverageMCPSessionManagerTerminate(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 2, Total: 4})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	res := reg.reserve(mcpSessionFingerprint(req))
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	mgr := reg.ResolveSessionIdManager(req)
	id := mgr.Generate()
	if _, err := mgr.Terminate(id); err != nil {
		t.Fatalf("terminate error: %v", err)
	}
	if _, err := mgr.Validate(id); err == nil {
		t.Fatal("terminated session should not validate")
	}
}

func TestCoverageToolFloat(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"f": 3.14}
	if got := toolFloat(req, "f", 0); got != 3.14 {
		t.Fatalf("float: got %f", got)
	}
	if got := toolFloat(req, "missing", 2.5); got != 2.5 {
		t.Fatalf("default: got %f", got)
	}
}

func TestCoverageMCPSessionManagerTerminateNonexistent(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	mgr := mcpSessionManager{registry: reg, principal: "p1"}
	if _, err := mgr.Terminate("nonexistent-id"); err == nil {
		t.Fatal("terminate nonexistent must fail")
	}
}

func TestCoverageMCPSessionValidateNonexistent(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	mgr := mcpSessionManager{registry: reg, principal: "p1"}
	terminated, err := mgr.Validate("nonexistent-id")
	if err == nil {
		t.Fatal("validate nonexistent must fail")
	}
	if terminated {
		t.Fatal("nonexistent session should not be terminated")
	}
}

func TestCoverageMCPSessionReserveTotalCap(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 10, Total: 1})
	res1 := reg.reserve("p1")
	if res1 == nil {
		t.Fatal("first reserve failed")
	}
	res2 := reg.reserve("p2")
	if res2 != nil {
		res2.release()
		t.Fatal("total cap exceeded")
	}
	res1.release()
}

func TestCoverageMCPSessionGenerateWithReservation(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 2, Total: 4})
	res := reg.reserve("p1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	mgr := reg.ResolveSessionIdManager(req)
	id := mgr.Generate()
	if id == "" {
		t.Fatal("Generate with reservation must issue ID")
	}
	res.rollbackGenerated()
	if !reg.canCreate("p1") {
		t.Fatal("rollback must free slot")
	}
}

func TestCoverageMCPSessionManagerGenerateWithoutReservation(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	res := reg.reserve("p1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	_ = reg.ResolveSessionIdManager(req).Generate()
	unreserved := mcpSessionManager{registry: reg, principal: "p1"}
	if id := unreserved.Generate(); id != "" {
		t.Fatalf("over-cap unreserved must refuse, got %q", id)
	}
	res.release()
}

func TestCoverageMCPSessionManagerTerminateNilID(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	mgr := mcpSessionManager{registry: reg, principal: "p1"}
	if _, err := mgr.Terminate(""); err == nil {
		t.Fatal("terminate empty id must fail")
	}
}

func TestCoverageMCPSessionManagerValidateNilID(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	mgr := mcpSessionManager{registry: reg, principal: "p1"}
	if _, err := mgr.Validate(""); err == nil {
		t.Fatal("validate empty id must fail")
	}
}

func TestCoverageAdmissionHandleGrowAfterRelease(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 4096, Global: 8192})
	h := adm.acquire("p1", 100)
	h.release()
	if h.grow(50) {
		t.Fatal("grow after release must fail")
	}
}

func TestCoverageMCPSessionRegistryMaintenanceGenerate(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})
	mgr := mcpSessionManager{registry: reg, maintenance: true}
	id := mgr.Generate()
	if id == "" {
		t.Fatal("maintenance manager must issue IDs while caps allow")
	}
}

func TestCoverageMCPSessionRegistryTerminateFromNonOwner(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 2, Total: 4})
	res := reg.reserve("owner")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req = req.WithContext(withMCPSessionReservation(req.Context(), res))
	id := reg.ResolveSessionIdManager(req).Generate()
	if id == "" {
		t.Fatal("Generate failed")
	}
	res.release()
	stranger := mcpSessionManager{registry: reg, principal: "stranger"}
	if _, err := stranger.Terminate(id); err == nil {
		t.Fatal("non-owner terminate must fail")
	}
}

func TestCoverageAdmissionHandleGrowBeyondGlobal(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 4096, Global: 2048})
	h := adm.acquire("p1", 1500)
	if h == nil {
		t.Fatal("acquire failed")
	}
	h2 := adm.acquire("p2", 500)
	if h2 == nil {
		t.Fatal("acquire p2 failed")
	}
	if h.grow(200) {
		t.Fatal("grow must fail: global cap exceeded")
	}
	h.release()
	h2.release()
}

func TestCoverageToolResultSuccessAndError(t *testing.T) {
	result, err := toolResult(map[string]string{"ok": "true"}, nil)
	if err != nil {
		t.Fatalf("toolResult success: %v", err)
	}
	text := serverToolText(result)
	if !strings.Contains(text, "ok") {
		t.Fatalf("missing result: %s", text)
	}
	result2, err2 := toolResult(nil, errors.New("fail"))
	if err2 != nil {
		t.Fatalf("toolResult error: %v", err2)
	}
	if !result2.IsError {
		t.Fatal("expected isError=true")
	}
}
