package mcp

// W9.12 coverage push for internal/mcp: additive tests over tool dispatch edges,
// profile/argument helpers, and the local RRF fusion path left uncovered by the
// pre-existing suite. No production behavior is asserted beyond the handler API.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCoverage_LocalErrorHelpers(t *testing.T) {
	if got := boundLocalText("short"); got != "short" {
		t.Fatalf("short text rewritten: %q", got)
	}
	got := boundLocalText(strings.Repeat("x", maxLocalErrorRunes+10))
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Fatalf("long text not truncated: %q", got)
	}
	if trimmed := strings.TrimSuffix(got, "…[truncated]"); len([]rune(trimmed)) != maxLocalErrorRunes {
		t.Fatalf("truncated length = %d, want %d", len([]rune(trimmed)), maxLocalErrorRunes)
	}
	if got := localValidationMessage(errors.New("plain")); got != "invalid input" {
		t.Fatalf("non-validation message = %q", got)
	}
	legacy := &domain.ValidationError{Field: "title", Message: "required"}
	if got := localValidationMessage(legacy); !strings.Contains(got, `"title"`) {
		t.Fatalf("legacy field message = %q", got)
	}
	if got := localValidationMessage(domain.NewRejected("acl", "denied")); !strings.Contains(got, "rule: acl") {
		t.Fatalf("rule message = %q", got)
	}
	if got := localValidationMessage(domain.NewDedupSkipped("duplicate")); got != "duplicate" {
		t.Fatalf("coded message = %q", got)
	}
}

func TestCoverage_ClassifyLocalError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"nil", nil, localCodeInternal},
		{"not_found", &domain.NotFoundError{Type: "observation", ID: 1}, localCodeNotFound},
		{"failed", domain.NewFailed(errors.New("boom"), "persist failed"), localCodeInternal},
		{"validation", &domain.ValidationError{Field: "f", Message: "bad"}, localCodeValidation},
		{"conflict", &domain.ConflictError{Entity: "e", Reason: "r"}, localCodeConflict},
		{"already_exists", domain.ErrAlreadyExists, localCodeConflict},
		{"session_ended", domain.ErrSessionEnded, localCodeConflict},
		{"unauthorized", domain.ErrUnauthorized, localCodeUnauthorized},
		{"busy", errors.New("database is locked"), localCodeUnavailable},
		{"timeout", context.DeadlineExceeded, localCodeTimeout},
		{"unknown", errors.New("weird"), localCodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := classifyLocalError(tc.err); code != tc.code {
				t.Fatalf("code = %q, want %q", code, tc.code)
			}
		})
	}
}

func covResult(id int64, publicID, project, topic, title string) *domain.SearchResult {
	r := &domain.SearchResult{Rank: 1}
	r.ID, r.PublicID, r.Project, r.TopicKey, r.Title = id, publicID, project, topic, title
	return r
}

func TestCoverage_FuseSearchResults(t *testing.T) {
	t.Run("local_empty_returns_remote", func(t *testing.T) {
		remote := []*domain.SearchResult{covResult(1, "", "p", "", "a"), covResult(2, "", "p", "", "b"), covResult(3, "", "p", "", "c")}
		got := fuseSearchResults(nil, remote, 2)
		if len(got) != 2 || got[0].ScoreBreakdown.Strategy != "remote" {
			t.Fatalf("remote-only fusion = %d results, strategy %q", len(got), got[0].ScoreBreakdown.Strategy)
		}
		if len(fuseSearchResults(nil, remote, 0)) != 3 {
			t.Fatal("limit<=0 must not truncate the remote-only path")
		}
	})
	t.Run("remote_empty_returns_local", func(t *testing.T) {
		local := []*domain.SearchResult{covResult(1, "", "p", "", "a"), covResult(2, "", "p", "", "b"), covResult(3, "", "p", "", "c")}
		if len(fuseSearchResults(local, nil, 2)) != 2 {
			t.Fatal("local-only path ignored the limit")
		}
		if len(fuseSearchResults(local, nil, 0)) != 3 {
			t.Fatal("limit<=0 must not truncate the local-only path")
		}
	})
	t.Run("fuses_by_public_id", func(t *testing.T) {
		local := []*domain.SearchResult{covResult(1, "pub-1", "p", "", "Local Title")}
		remote := []*domain.SearchResult{covResult(0, "pub-1", "p", "", "Remote Title"), covResult(9, "pub-2", "p", "", "Unique Remote")}
		got := fuseSearchResults(local, remote, 10)
		if len(got) != 2 {
			t.Fatalf("expected a merge plus an append, got %d", len(got))
		}
		if got[0].PublicID != "pub-1" || got[0].ScoreBreakdown.Strategy != "local+remote" {
			t.Fatalf("merged head not fused: %+v", got[0].ScoreBreakdown)
		}
		if got[0].ScoreBreakdown.FusionScore <= 0 {
			t.Fatal("fused score must be positive")
		}
	})
	t.Run("matches_by_id_topic_and_title", func(t *testing.T) {
		if got := fuseSearchResults([]*domain.SearchResult{covResult(7, "", "p", "", "A")}, []*domain.SearchResult{covResult(7, "", "p", "", "B")}, 10); len(got) != 1 {
			t.Fatalf("identical ID must merge, got %d", len(got))
		}
		if got := fuseSearchResults([]*domain.SearchResult{covResult(1, "", "p", "topic/x", "A")}, []*domain.SearchResult{covResult(0, "", "p", "topic/x", "Different")}, 10); len(got) != 1 {
			t.Fatalf("identical topic key must merge, got %d", len(got))
		}
		if got := fuseSearchResults([]*domain.SearchResult{covResult(1, "", "p", "", "Same Title")}, []*domain.SearchResult{covResult(0, "", "p", "", "same title")}, 10); len(got) != 1 {
			t.Fatalf("case-insensitive title must merge, got %d", len(got))
		}
	})
	t.Run("existing_remote_strategy_is_appended", func(t *testing.T) {
		local := []*domain.SearchResult{covResult(1, "", "p", "", "A")}
		local[0].ScoreBreakdown.Strategy = "hybrid"
		remote := []*domain.SearchResult{covResult(2, "", "p", "", "A")}
		remote[0].ScoreBreakdown.Strategy = "already remote"
		if got := fuseSearchResults(local, remote, 10); got[0].ScoreBreakdown.Strategy != "hybrid+remote" {
			t.Fatalf("strategy = %q, want hybrid+remote", got[0].ScoreBreakdown.Strategy)
		}
	})
	t.Run("limit_defaults_and_truncates", func(t *testing.T) {
		local := make([]*domain.SearchResult, 12)
		for i := range local {
			local[i] = covResult(int64(i+1), "", "p", "", "t")
		}
		remote := []*domain.SearchResult{covResult(100, "z", "q", "", "z")}
		if got := fuseSearchResults(local, remote, -1); len(got) != 10 {
			t.Fatalf("default limit expected 10, got %d", len(got))
		}
		if got := fuseSearchResults(local, remote, 2); len(got) != 2 {
			t.Fatalf("limit 2 expected 2, got %d", len(got))
		}
	})
}

func TestCoverage_MarkRemoteResult(t *testing.T) {
	markRemoteResult(nil)
	empty := &domain.SearchResult{}
	markRemoteResult(empty)
	if empty.ScoreBreakdown.Strategy != "remote" {
		t.Fatalf("empty strategy = %q", empty.ScoreBreakdown.Strategy)
	}
	already := &domain.SearchResult{}
	already.ScoreBreakdown.Strategy = "remote"
	markRemoteResult(already)
	if already.ScoreBreakdown.Strategy != "remote" {
		t.Fatalf("remote strategy rewritten: %q", already.ScoreBreakdown.Strategy)
	}
	other := &domain.SearchResult{}
	other.ScoreBreakdown.Strategy = "keyword"
	markRemoteResult(other)
	if other.ScoreBreakdown.Strategy != "keyword+remote" {
		t.Fatalf("strategy = %q, want keyword+remote", other.ScoreBreakdown.Strategy)
	}
}

func TestCoverage_ResolveToolsEdges(t *testing.T) {
	if len(ResolveTools("")) != len(ProfileAgent) {
		t.Fatal("empty input must resolve ProfileAgent")
	}
	if len(ResolveTools("all")) != len(ProfileAgent) {
		t.Fatal(`"all" must resolve ProfileAgent`)
	}
	// A blank token is skipped and a mid-list "all" short-circuits the merge.
	if len(ResolveTools("minimal, ,all,custom_tool")) != len(ProfileAgent) {
		t.Fatal(`mid-list "all" must resolve ProfileAgent`)
	}
	named := ResolveTools("cortex_save,custom_tool")
	if !named["cortex_save"] || !named["custom_tool"] || len(named) != 2 {
		t.Fatalf("explicit tool tokens = %v", named)
	}
}

func TestCoverage_ArgumentHelpers(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"f": float64(7), "i": 3, "i64": int64(5), "s": " 9 ", "bad": "x", "nilv": nil,
		"b": true, "ib": 1, "i64b": int64(0), "fb": float64(0),
		"sb_yes": "YES", "sb_no": "no", "sb_maybe": "maybe",
	}
	if intArg(req, "missing", 11) != 11 || intArg(req, "nilv", 11) != 11 || intArg(req, "bad", 4) != 4 {
		t.Fatal("missing, nil, or unparseable int arguments must fall back to the default")
	}
	if intArg(req, "f", 0) != 7 || intArg(req, "i", 0) != 3 || intArg(req, "i64", 0) != 5 || intArg(req, "s", 0) != 9 {
		t.Fatal("numeric int argument variants decoded incorrectly")
	}
	if !boolArg(req, "b", false) || !boolArg(req, "ib", false) {
		t.Fatal("true-ish bool arguments decoded incorrectly")
	}
	if boolArg(req, "i64b", true) || boolArg(req, "fb", true) {
		t.Fatal("zero numeric bool arguments must decode false")
	}
	if !boolArg(req, "sb_yes", false) || boolArg(req, "sb_no", true) {
		t.Fatal("string bool arguments decoded incorrectly")
	}
	if !boolArg(req, "sb_maybe", true) {
		t.Fatal("unknown string bool must fall back to the default")
	}
}

func TestCoverage_HandleSearchTemporal(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "covt-s", "covt")
	saveObs(t, stores, "Temporal anchor note", "covt", "covt-s")
	handler := handleSearchTemporal(stores)
	if r := callTool(t, handler, map[string]any{}); !r.IsError {
		t.Fatal("missing query must be rejected")
	}
	if r := callTool(t, handler, map[string]any{"query": "anchor"}); !r.IsError {
		t.Fatal("missing as_of must be rejected")
	}
	if r := callTool(t, handler, map[string]any{"query": "anchor", "as_of": "not-a-time"}); !r.IsError {
		t.Fatal("malformed as_of must be rejected")
	}
	if r := callTool(t, handler, map[string]any{"query": "anchor", "as_of": "2030-01-01T00:00:00Z", "project": "covt"}); r.IsError {
		t.Fatalf("valid temporal search errored: %q", resultText(r))
	}
	if r := callTool(t, handler, map[string]any{"query": "zzz-no-such-token", "as_of": "2030-01-01T00:00:00Z", "project": "covt"}); r.IsError {
		t.Fatalf("empty temporal result errored: %q", resultText(r))
	}
}

func TestCoverage_HandleConsolidate(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "covc-s", "covc")
	handler := handleConsolidate(stores)
	if r := callTool(t, handler, map[string]any{}); !r.IsError {
		t.Fatal("missing project must be rejected")
	}
	topic := "architecture/cov"
	first := &domain.Observation{SessionID: "covc-s", Type: domain.TypeManual, Title: "Auth v1",
		Content: "first distinct content", Project: "covc", Scope: domain.ScopeProject, TopicKey: topic}
	if err := stores.Observations.Save(context.Background(), first); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if r := callTool(t, handler, map[string]any{"project": "covc", "topic_key": topic}); !strings.Contains(resultText(r), "nothing to consolidate") {
		t.Fatalf("single topic observation = %q", resultText(r))
	}
	// A different scope avoids the topic_key upsert so a second row exists.
	second := &domain.Observation{SessionID: "covc-s", Type: domain.TypeManual, Title: "Auth v2",
		Content: "second distinct content", Project: "covc", Scope: domain.ScopePersonal, TopicKey: topic}
	if err := stores.Observations.Save(context.Background(), second); err != nil {
		t.Fatalf("save second: %v", err)
	}
	if r := callTool(t, handler, map[string]any{"project": "covc", "topic_key": topic}); !strings.Contains(resultText(r), "Consolidation candidates for topic_key") {
		t.Fatalf("topic candidate list = %q", resultText(r))
	}
	if r := callTool(t, handler, map[string]any{"project": "covc"}); !strings.Contains(resultText(r), "Consolidation candidates in project") {
		t.Fatalf("group scan = %q", resultText(r))
	}
}

func TestCoverage_HandleSearchModes(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "covsr-s", "covsr")
	a := saveObs(t, stores, "Coverage alpha note", "covsr", "covsr-s")
	b := saveObs(t, stores, "Coverage beta note", "covsr", "covsr-s")
	if err := stores.Graph.CreateEdge(context.Background(), &domain.Edge{
		FromObsID: a.ID, ToObsID: b.ID, RelationType: domain.RelationRelatesTo, Weight: 1, Confidence: 1,
	}); err != nil {
		t.Fatalf("create edge: %v", err)
	}
	handler := handleSearch(stores)
	if r := callTool(t, handler, map[string]any{"query": "Coverage", "project": "covsr", "limit": 0, "mode": "direct", "scope": "personal"}); r.IsError {
		t.Fatalf("direct mode errored: %q", resultText(r))
	}
	if r := callTool(t, handler, map[string]any{"query": "Coverage", "project": "covsr", "limit": 100, "mode": "semantic"}); r.IsError {
		t.Fatalf("semantic mode errored: %q", resultText(r))
	}
	r := callTool(t, handler, map[string]any{"query": "Coverage", "project": "covsr", "mode": "multi_hop", "limit": 5})
	if r.IsError {
		t.Fatalf("multi_hop errored: %q", resultText(r))
	}
	if !strings.Contains(resultText(r), "Coverage") {
		t.Fatalf("multi_hop lost seeded results: %q", resultText(r))
	}
}

func TestCoverage_CodeToolBranches(t *testing.T) {
	stores := setupTestStores(t)
	ctx := context.Background()
	syms := []code.Symbol{
		{ID: "func:covp.CoverageSvc", Name: "CoverageSvc", Kind: "func", FilePath: "covp/svc.go", Project: "covp"},
		{ID: "func:covp.TestCoverageSvc", Name: "TestCoverageSvc", Kind: "func", FilePath: "covp/svc_test.go", Project: "covp"},
	}
	rels := []code.Relation{{SourceID: "func:covp.TestCoverageSvc", TargetID: "func:covp.CoverageSvc", Relation: "calls", Project: "covp"}}
	if err := stores.Code.SaveSymbols(ctx, syms); err != nil {
		t.Fatalf("save symbols: %v", err)
	}
	if err := stores.Code.SaveRelations(ctx, rels); err != nil {
		t.Fatalf("save relations: %v", err)
	}
	regex := callTool(t, handleGetCodeSymbols(stores), map[string]any{"project": "covp", "query": "Coverage.*", "is_regex": true})
	if !strings.Contains(resultText(regex), "CoverageSvc") {
		t.Fatalf("regex symbol search = %q", resultText(regex))
	}
	repoMap := callTool(t, handleGetCodeMap(stores), map[string]any{"project": "covp", "budget": 512})
	if repoMap.IsError || !strings.Contains(resultText(repoMap), "svc.go") {
		t.Fatalf("repo map = %q", resultText(repoMap))
	}
	blast := callTool(t, handleGetBlastRadius(stores), map[string]any{"project": "covp", "target": "CoverageSvc", "depth": 2, "include_tests": true})
	blastText := resultText(blast)
	if !strings.Contains(blastText, "impacted_test_files") || !strings.Contains(blastText, "CoverageSvc") {
		t.Fatalf("blast radius with tests = %q", blastText)
	}
	bare := &Stores{}
	if r := callTool(t, handleGetCodeMap(bare), map[string]any{}); !r.IsError {
		t.Fatal("repo map without a code store must fail closed")
	}
	if r := callTool(t, handleGetCodeGraph(bare), map[string]any{}); !r.IsError {
		t.Fatal("code graph without a code store must fail closed")
	}
	if r := callTool(t, handleGetImpactedTests(bare), map[string]any{"target": "x"}); !r.IsError {
		t.Fatal("test impact without a code store must fail closed")
	}
	if r := callTool(t, handleFindSymbols(bare), map[string]any{"query": "x"}); !r.IsError {
		t.Fatal("symbol search without a code store must fail closed")
	}
}

func TestCoverage_AgentContextCompact(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "covac-s", "covac")
	obs := &domain.Observation{SessionID: "covac-s", Type: domain.TypeDecision, Title: "Coverage decision",
		Content: "chosen for coverage", Project: "covac", Scope: domain.ScopeProject}
	if err := stores.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("save: %v", err)
	}
	handler := handleGetAgentContext(stores)
	if r := callTool(t, handler, map[string]any{"project": "covac"}); r.IsError {
		t.Fatalf("default format: %q", resultText(r))
	}
	if r := callTool(t, handler, map[string]any{"project": "covac", "format": "compact"}); r.IsError || strings.TrimSpace(resultText(r)) == "" {
		t.Fatalf("compact format: %q", resultText(r))
	}
	if r := callTool(t, handler, map[string]any{"project": "covac", "max_tokens": 400}); r.IsError {
		t.Fatalf("max_tokens route: %q", resultText(r))
	}
}

func TestCoverage_ExecuteValidation(t *testing.T) {
	handler := handleExecute(setupTestStores(t))
	if r := callTool(t, handler, map[string]any{"language": "go", "code": "   "}); !r.IsError {
		t.Fatal("blank sandbox code must be rejected")
	}
	if r := callTool(t, handler, map[string]any{"language": "not-a-language", "code": "print(1)"}); !r.IsError {
		t.Fatal("unknown sandbox language must be rejected")
	}
}

type covRemoteClient struct {
	startErr error
	initErr  error
	listErr  error
	initRes  *mcp.InitializeResult
	tools    []mcp.Tool
}

func (c *covRemoteClient) Start(context.Context) error { return c.startErr }
func (c *covRemoteClient) Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	if c.initRes != nil {
		return c.initRes, nil
	}
	return &mcp.InitializeResult{}, nil
}
func (c *covRemoteClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return &mcp.ListToolsResult{Tools: c.tools}, nil
}
func (c *covRemoteClient) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("ok"), nil
}
func (c *covRemoteClient) Close() error { return nil }

func TestCoverage_RemoteProxyErrorPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := openRemoteProxy(ctx, &covRemoteClient{startErr: errors.New("start boom")}); err == nil {
		t.Fatal("transport start failure must surface")
	}
	if _, err := openRemoteProxy(ctx, &covRemoteClient{initErr: errors.New("init boom")}); err == nil {
		t.Fatal("initialize failure must surface")
	}
	if _, err := openRemoteProxy(ctx, &covRemoteClient{listErr: errors.New("list boom")}); err == nil {
		t.Fatal("list-tools failure must surface")
	}
	// Empty server info falls back to the proxy defaults, and an empty raw
	// schema is marshalled from the structured input schema.
	proxy, err := openRemoteProxy(ctx, &covRemoteClient{tools: []mcp.Tool{{Name: "cortex_echo", Description: "echo"}}})
	if err != nil {
		t.Fatalf("openRemoteProxy: %v", err)
	}
	if proxy.Server == nil || proxy.client == nil {
		t.Fatal("proxy not wired")
	}
	var empty RemoteProxy
	if err := empty.Close(); err != nil {
		t.Fatalf("nil-client close: %v", err)
	}
	var nilProxy *RemoteProxy
	if err := nilProxy.Close(); err != nil {
		t.Fatalf("nil-receiver close: %v", err)
	}
}
