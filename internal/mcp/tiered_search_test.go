package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

type mockRemoteSearcher struct {
	called    bool
	callCount int
	lastQuery string
	lastOpts  domain.SearchOptions
	results   []*domain.SearchResult
	err       error
}

func (m *mockRemoteSearcher) SearchHybrid(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	m.called = true
	m.callCount++
	m.lastQuery = query
	m.lastOpts = opts
	return m.results, m.err
}

func TestHandleSearch_CRAGEscalation_EmptyLocal(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")

	remoteMock := &mockRemoteSearcher{
		results: []*domain.SearchResult{
			{
				Observation: domain.Observation{
					PublicID:  "remote-obs-uuid-1",
					Title:     "Remote Architecture Decision",
					Content:   "Architecture decision stored on remote server",
					Type:      "architecture",
					Project:   "demo",
					Scope:     "project",
					CreatedAt: time.Now(),
				},
				Rank: 0.92,
			},
		},
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "nonexistent local query",
		"project": "demo",
	})

	text := resultText(result)
	if !remoteMock.called {
		t.Fatal("expected remote search escalation when local results are empty")
	}
	if !strings.Contains(text, "Remote Architecture Decision") {
		t.Errorf("expected remote result in output, got %q", text)
	}
	if !strings.Contains(text, "strategy=remote") {
		t.Errorf("expected score_breakdown to indicate remote strategy, got %q", text)
	}
}

func TestHandleSearch_CRAGEscalation_ForcedScopes(t *testing.T) {
	// Tests that "team" and "global" scopes force escalation to RemoteSearch
	for _, forcedScope := range []string{"team", "global"} {
		t.Run("Scope_"+forcedScope, func(t *testing.T) {
			stores := setupTestStores(t)
			createSession(t, stores, "s1", "demo")
			saveObs(t, stores, "Local note", "demo", "s1")

			remoteMock := &mockRemoteSearcher{
				results: []*domain.SearchResult{
					{
						Observation: domain.Observation{
							ID:        100,
							Title:     "Team Shared Protocol",
							Content:   "Shared across all engineers",
							Type:      "decision",
							Project:   "demo",
							Scope:     forcedScope,
							CreatedAt: time.Now(),
						},
						Rank: 0.85,
					},
				},
			}
			stores.RemoteSearch = remoteMock

			handler := handleSearch(stores)
			result := callTool(t, handler, map[string]interface{}{
				"query":   "note",
				"project": "demo",
				"scope":   forcedScope,
			})

			if !remoteMock.called {
				t.Fatalf("expected remote search escalation for scope %q", forcedScope)
			}
			if remoteMock.lastOpts.Scope != forcedScope {
				t.Errorf("expected remoteOpts.Scope = %q, got %q", forcedScope, remoteMock.lastOpts.Scope)
			}
			text := resultText(result)
			if !strings.Contains(text, "Team Shared Protocol") {
				t.Errorf("expected team protocol in output, got %q", text)
			}
		})
	}
}

func TestHandleSearch_ScopeLocal_NeverEscalates(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")

	remoteMock := &mockRemoteSearcher{
		results: []*domain.SearchResult{
			{
				Observation: domain.Observation{
					Title:   "Should Not Be Returned",
					Content: "Remote content",
				},
			},
		},
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query": "nonexistent",
		"scope": "local",
	})

	if remoteMock.called {
		t.Fatal("remote search should NOT be called when scope is 'local'")
	}
	text := resultText(result)
	if !strings.Contains(text, "No memories found") {
		t.Errorf("expected no memories found, got %q", text)
	}
}

func TestHandleSearch_RemoteFailure_OfflineResilience(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Important local discovery", "demo", "s1")

	// Remote searcher returns network failure
	remoteMock := &mockRemoteSearcher{
		err: errors.New("connection refused: server offline"),
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "discovery",
		"project": "demo",
		"scope":   "team", // Forces escalation, but remote is down
	})

	if !remoteMock.called {
		t.Fatal("expected remote search to be attempted")
	}

	// Should not crash and should return local search result (or graceful no memories if scope was filtered)
	// Because scope="team" was requested and local has no scope="team", no memories found is returned gracefully.
	if result.IsError {
		t.Fatalf("tool handler returned error when remote failed: %+v", result)
	}
}

func TestHandleSearch_FusionAndDeduplication(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Shared Architecture Pattern", "demo", "s1")

	// Remote returns same observation title + a new unique one
	remoteMock := &mockRemoteSearcher{
		results: []*domain.SearchResult{
			{
				Observation: domain.Observation{
					PublicID:  "remote-pattern-uuid",
					Title:     "Shared Architecture Pattern",
					Content:   "Updated content from server",
					Type:      "architecture",
					Project:   "demo",
					Scope:     "project",
					CreatedAt: time.Now(),
				},
				Rank: 0.90,
			},
			{
				Observation: domain.Observation{
					PublicID:  "remote-unique-uuid",
					Title:     "Unique Remote Knowledge",
					Content:   "Only on server",
					Type:      "pattern",
					Project:   "demo",
					Scope:     "project",
					CreatedAt: time.Now(),
				},
				Rank: 0.85,
			},
		},
	}
	stores.RemoteSearch = remoteMock

	handler := handleSearch(stores)
	result := callTool(t, handler, map[string]interface{}{
		"query":   "Pattern",
		"project": "demo",
		"scope":   "team", // Forces escalation
	})

	text := resultText(result)
	if !strings.Contains(text, "Shared Architecture Pattern") {
		t.Errorf("expected Shared Architecture Pattern in output, got %q", text)
	}
	if !strings.Contains(text, "Unique Remote Knowledge") {
		t.Errorf("expected Unique Remote Knowledge in output, got %q", text)
	}
	if !strings.Contains(text, "strategy=remote") {
		t.Errorf("expected strategy=remote in breakdown, got %q", text)
	}
}
