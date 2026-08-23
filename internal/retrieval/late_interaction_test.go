package retrieval

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestTokenizeLateInteraction(t *testing.T) {
	tokens := TokenizeLateInteraction("func HandleSearch_Hybrid(ctx context.Context)")
	expected := []string{"func", "handlesearch_hybrid", "ctx", "context", "context"}

	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %v", len(expected), tokens)
	}
	for i, exp := range expected {
		if tokens[i] != exp {
			t.Errorf("token %d: expected %s, got %s", i, exp, tokens[i])
		}
	}
}

func TestComputeMaxSimScore(t *testing.T) {
	queryTokens := []string{"auth", "session", "token"}
	exactDoc := []string{"auth", "service", "handles", "session", "token"}
	irrelevantDoc := []string{"database", "migrations", "sqlite", "blob"}

	scoreExact := ComputeMaxSimScore(queryTokens, exactDoc)
	scoreIrrelevant := ComputeMaxSimScore(queryTokens, irrelevantDoc)

	if scoreExact != 1.0 {
		t.Errorf("expected 1.0 for exact token coverage, got %f", scoreExact)
	}
	if scoreIrrelevant >= 0.3 {
		t.Errorf("expected low similarity for irrelevant doc, got %f", scoreIrrelevant)
	}
}

func TestReRankWithLateInteraction(t *testing.T) {
	query := "postgresql tenant isolation"

	results := []*domain.SearchResult{
		{
			Rank: 0.5,
			Observation: domain.Observation{
				ID:      1,
				Title:   "SQLite Single Binary",
				Content: "Zero CGO local database.",
			},
		},
		{
			Rank: 0.4,
			Observation: domain.Observation{
				ID:      2,
				Title:   "PostgreSQL Multi-Tenant Isolation",
				Content: "Row Level Security with tenant isolation and crypto tokens.",
			},
		},
	}

	reRanked := ReRankWithLateInteraction(query, results)
	if len(reRanked) != 2 {
		t.Fatalf("expected 2 results, got %d", len(reRanked))
	}

	// Observation #2 should be promoted to rank #1 due to high Late-Interaction MaxSim score
	if reRanked[0].ID != 2 {
		t.Errorf("expected observation #2 to be re-ranked #1, got #%d", reRanked[0].ID)
	}
}

func BenchmarkComputeMaxSimScore(b *testing.B) {
	queryTokens := TokenizeLateInteraction("How does authentication and session management work in multi-tenant mode?")
	docTokens := TokenizeLateInteraction("AuthService manages user credentials, OAuth tokens, and PostgreSQL Row-Level Security sessions across tenants.")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputeMaxSimScore(queryTokens, docTokens)
	}
}

func BenchmarkReRankWithLateInteraction(b *testing.B) {
	query := "postgresql multi-tenant session isolation"
	candidates := make([]*domain.SearchResult, 20)
	for i := 0; i < 20; i++ {
		candidates[i] = &domain.SearchResult{
			Rank: float64(20-i) * 0.05,
			Observation: domain.Observation{
				ID:      int64(i + 1),
				Title:   "Candidate Document Title",
				Content: "Candidate document content with details about database, auth, sessions and tenant isolation.",
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ReRankWithLateInteraction(query, candidates)
	}
}
