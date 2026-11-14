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

func TestTokenSpecificityWeight(t *testing.T) {
	stopWeight := TokenSpecificityWeight("the")
	technicalWeight := TokenSpecificityWeight("token_validator")
	normalWeight := TokenSpecificityWeight("server")

	if stopWeight >= normalWeight {
		t.Errorf("expected stop word weight (%f) < normal weight (%f)", stopWeight, normalWeight)
	}
	if technicalWeight <= normalWeight {
		t.Errorf("expected technical identifier weight (%f) > normal weight (%f)", technicalWeight, normalWeight)
	}
}

func TestComputeWeightedMaxSimScore_PrioritizesTechnicalTokens(t *testing.T) {
	// Query: "how does AuthService work"
	query := []string{"how", "does", "auth_service", "work"}

	// Doc A matches stop words ("how", "does", "work"), but misses technical token ("auth_service")
	docA := []string{"how", "does", "database", "work"}

	// Doc B matches the crucial technical token ("auth_service"), but misses stop words
	docB := []string{"auth_service", "handles", "credentials"}

	scoreA := ComputeMaxSimScore(query, docA)
	scoreB := ComputeMaxSimScore(query, docB)

	// Due to specificity weighting, Doc B matching "auth_service" must rank higher than Doc A matching only stop words!
	if scoreB <= scoreA {
		t.Errorf("expected technical match score (%f) > stop words match score (%f)", scoreB, scoreA)
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

// TestTokenSimilarity_GoldenValues pins exact TokenSimilarity outputs. Expected values were
// captured from the reference implementation and are compared with exact float equality.
func TestTokenSimilarity_GoldenValues(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"auth", "auth", 1},
		{"", "", 1},
		{"a", "a", 1},
		{"", "abc", 0},
		{"abc", "", 0},
		{"a", "b", 0},
		{"a", "abc", 0.3333333333333333},
		{"abc", "a", 0.3333333333333333},
		{"session", "sessions", 0.875},
		{"sessions", "session", 0.875},
		{"token", "tokens", 0.8333333333333334},
		{"aaaa", "aaab", 1},
		{"aaab", "aaaa", 1.2},
		{"xyz", "abc", 0},
		{"authentication", "authorization", 0.5833333333333334},
		{"función", "funciones", 0.5333333333333333},
		{"año", "años", 0.8},
		{"postgres", "postgis", 0.6153846153846154},
		{"ab", "ba", 0},
		{"abab", "baba", 1.2},
		{"ñandú", "andú", 0.7142857142857143},
		{"server", "servers", 0.8571428571428571},
		{"ab", "xab", 0.6666666666666666},
		{"abcabc", "cabcab", 1.25},
	}
	for _, tt := range tests {
		got := TokenSimilarity(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("TokenSimilarity(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestComputeWeightedMaxSimScore_GoldenValues pins exact weighted MaxSim outputs on realistic
// sentences. Expected values were captured from the reference implementation.
func TestComputeWeightedMaxSimScore_GoldenValues(t *testing.T) {
	tests := []struct {
		query, doc string
		want       float64
	}{
		{"How does the authentication session token work?", "AuthService manages user credentials, OAuth tokens, and PostgreSQL Row-Level Security sessions across tenants.", 0.47394296951819076},
		{"postgresql tenant isolation", "PostgreSQL Multi-Tenant Isolation. Row Level Security with tenant isolation and crypto tokens.", 1},
		{"how to configure the sqlite database path", "SQLite single binary. Zero CGO local database stored at the configured path.", 0.8810666666666667},
		{"función de búsqueda híbrida", "La función de búsqueda combina FTS y vectores para los resultados.", 0.7307443365695793},
		{"database migrations rollback", "Schema migrations run at startup; rollback is not supported for the blob store.", 0.7499999999999999},
		{"why does the retry loop ignore context cancellation?", "The retry loop checks ctx.Done() before each attempt and returns early.", 0.501510185720712},
	}
	for _, tt := range tests {
		got := ComputeWeightedMaxSimScore(TokenizeLateInteraction(tt.query), TokenizeLateInteraction(tt.doc))
		if got != tt.want {
			t.Errorf("ComputeWeightedMaxSimScore(%q, %q) = %v, want %v", tt.query, tt.doc, got, tt.want)
		}
	}
}
