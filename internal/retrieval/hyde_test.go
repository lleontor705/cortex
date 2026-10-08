package retrieval

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// countingSeams provides injectable HyDE seams with call counters.
type countingSeams struct {
	generateCalls atomic.Int64
	embedCalls    atomic.Int64
	doc           string
	genErr        error
	embedErr      error
	embedded      []float32
}

func (s *countingSeams) generate(ctx context.Context, query string) (string, error) {
	s.generateCalls.Add(1)
	if s.genErr != nil {
		return "", s.genErr
	}
	return s.doc, nil
}

func (s *countingSeams) embed(ctx context.Context, document string) ([]float32, error) {
	s.embedCalls.Add(1)
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	return s.embedded, nil
}

func newTestHyDE(seams *countingSeams) *HyDEGenerator {
	return NewHyDEGenerator(true, "test-embed-model", 128, time.Minute,
		seams.generate, seams.embed)
}

// TestHyDEFromEnvDefaultsOff pins the fail-closed feature flag: unset, empty,
// or unrecognized values never enable HyDE.
func TestHyDEFromEnvDefaultsOff(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"  ", false},
		{"0", false},
		{"false", false},
		{"off", false},
		{"garbage", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"On", true},
		{"yes", true},
	}
	for _, tc := range cases {
		if got := HyDEFromEnv(func(string) string { return tc.value }); got != tc.want {
			t.Errorf("HyDEFromEnv(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
	if HyDEFromEnv(nil) {
		t.Error("HyDEFromEnv(nil) must be OFF")
	}
	// Unset variable (getenv returns empty for everything).
	if HyDEFromEnv(func(string) string { return "" }) {
		t.Error("unset env must keep HyDE OFF")
	}
}

// TestHyDEDisabledMakesNoGenerationCall: with the flag off, no query path may
// reach the chat-LLM seam (REQ-RET-108 scenario 1).
func TestHyDEDisabledMakesNoGenerationCall(t *testing.T) {
	seams := &countingSeams{doc: "hypothetical doc", embedded: []float32{0.1, 0.2}}
	gen := NewHyDEGenerator(false, "m", 0, 0, seams.generate, seams.embed)
	if gen.Enabled() {
		t.Fatal("generator must be disabled when the flag is off")
	}
	for _, tier := range []QueryTier{TierSemanticHybrid, TierMultiHopGraph, TierDirectFactual} {
		if _, ok := gen.VectorForQuery(context.Background(), tier, "any query"); ok {
			t.Fatalf("disabled generator returned a vector for tier %s", tier)
		}
	}
	if got := gen.Calls(); got != 0 {
		t.Fatalf("disabled generator made %d generation calls, want 0", got)
	}
}

// TestHyDENeverOnCheapTiers: even enabled, TierDirectFactual and
// TierArchitecturalGlobal must never trigger generation.
func TestHyDENeverOnCheapTiers(t *testing.T) {
	seams := &countingSeams{doc: "doc", embedded: []float32{1}}
	gen := newTestHyDE(seams)
	if !gen.Enabled() {
		t.Fatal("generator must be enabled for this test")
	}
	for _, tier := range []QueryTier{TierDirectFactual, TierArchitecturalGlobal, QueryTier("")} {
		if _, ok := gen.VectorForQuery(context.Background(), tier, "why does it break"); ok {
			t.Fatalf("tier %s must not engage HyDE", tier)
		}
	}
	if got := gen.Calls(); got != 0 {
		t.Fatalf("cheap tiers made %d generation calls, want 0", got)
	}
}

// TestHyDEGeneratesEmbedsAndCaches: eligible tier generates once, embeds the
// hypothetical document, and a repeated query within TTL reuses the cached
// document without a new LLM call.
func TestHyDEGeneratesEmbedsAndCaches(t *testing.T) {
	seams := &countingSeams{doc: "the retrieval pipeline embeds queries", embedded: []float32{0.5}}
	gen := newTestHyDE(seams)

	ctx := context.Background()
	vec, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, "how are queries embedded")
	if !ok || len(vec) != 1 || vec[0] != 0.5 {
		t.Fatalf("first call must embed the hypothetical document, got %v ok=%v", vec, ok)
	}
	if got := seams.generateCalls.Load(); got != 1 {
		t.Fatalf("generation calls = %d, want 1", got)
	}
	if got := seams.embedCalls.Load(); got != 1 {
		t.Fatalf("embed calls = %d, want 1", got)
	}

	// Repeat within TTL: document cache hit, NO new LLM call.
	if _, ok := gen.VectorForQuery(ctx, TierMultiHopGraph, "  HOW are queries embedded  "); !ok {
		t.Fatal("cached repeat must still return a vector")
	}
	if got := seams.generateCalls.Load(); got != 1 {
		t.Fatalf("cached repeat made %d generation calls, want still 1", got)
	}
	if gen.Hits() != 1 || gen.Misses() != 1 {
		t.Fatalf("cache counters hits=%d misses=%d, want 1/1", gen.Hits(), gen.Misses())
	}
}

// TestHyDECacheKeyNormalizesQuery: whitespace/case variants share one entry.
func TestHyDECacheKeyNormalizesQuery(t *testing.T) {
	seams := &countingSeams{doc: "doc", embedded: []float32{1}}
	gen := newTestHyDE(seams)
	ctx := context.Background()

	for _, q := range []string{"find  symbols", "  FIND symbols ", "Find Symbols"} {
		if _, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, q); !ok {
			t.Fatalf("query %q must resolve", q)
		}
	}
	if got := seams.generateCalls.Load(); got != 1 {
		t.Fatalf("normalized queries made %d generation calls, want 1", got)
	}
}

// TestHyDEProviderErrorNotCachedDegrades: generation or embedding failures
// return false, are never cached, and the next call retries the provider.
func TestHyDEProviderErrorNotCachedDegrades(t *testing.T) {
	t.Run("generation error", func(t *testing.T) {
		seams := &countingSeams{genErr: errors.New("llm down")}
		gen := newTestHyDE(seams)
		ctx := context.Background()
		for i := 0; i < 2; i++ {
			if _, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, "q"); ok {
				t.Fatal("generation failure must degrade to false")
			}
		}
		if got := seams.generateCalls.Load(); got != 2 {
			t.Fatalf("failed generation must not be cached; calls = %d, want 2", got)
		}
		if gen.Hits() != 0 {
			t.Fatalf("failures must not be cached, hits = %d", gen.Hits())
		}
	})
	t.Run("embedding error", func(t *testing.T) {
		seams := &countingSeams{doc: "doc", embedErr: errors.New("embed down")}
		gen := newTestHyDE(seams)
		ctx := context.Background()
		if _, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, "q"); ok {
			t.Fatal("embedding failure must degrade to false")
		}
		if _, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, "q"); ok {
			t.Fatal("embedding failure must degrade to false on retry too")
		}
		if got := seams.generateCalls.Load(); got != 2 {
			t.Fatalf("embedding failure must not cache the document; generations = %d, want 2", got)
		}
	})
	t.Run("empty document", func(t *testing.T) {
		seams := &countingSeams{doc: "   "}
		gen := newTestHyDE(seams)
		if _, ok := gen.VectorForQuery(context.Background(), TierSemanticHybrid, "q"); ok {
			t.Fatal("empty hypothetical document must degrade to false")
		}
		if got := seams.generateCalls.Load(); got != 1 {
			t.Fatalf("empty doc must not be cached; second call would skip generation, got %d calls", got-1)
		}
	})
}

// TestHyDECacheTTLExpiry: beyond the TTL the document is regenerated.
func TestHyDECacheTTLExpiry(t *testing.T) {
	seams := &countingSeams{doc: "doc", embedded: []float32{1}}
	// ScopedCache TTL is wall-clock; use a short TTL and a real sleep margin.
	gen := NewHyDEGenerator(true, "m", 8, 40*time.Millisecond, seams.generate, seams.embed)
	ctx := context.Background()

	if _, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, "q"); !ok {
		t.Fatal("first call must engage")
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok := gen.VectorForQuery(ctx, TierSemanticHybrid, "q"); !ok {
		t.Fatal("post-TTL call must regenerate")
	}
	if got := seams.generateCalls.Load(); got != 2 {
		t.Fatalf("TTL expiry must trigger regeneration; calls = %d, want 2", got)
	}
}

// TestHyDEAllowedForTier pins the adaptive.go tier gate (REQ-RET-108 wiring).
func TestHyDEAllowedForTier(t *testing.T) {
	cases := []struct {
		tier QueryTier
		want bool
	}{
		{TierDirectFactual, false},
		{TierSemanticHybrid, true},
		{TierMultiHopGraph, true},
		{TierArchitecturalGlobal, false},
		{QueryTier("unknown"), false},
		{QueryTier(""), false},
	}
	for _, tc := range cases {
		if got := HyDEAllowedForTier(tc.tier); got != tc.want {
			t.Errorf("HyDEAllowedForTier(%q) = %v, want %v", tc.tier, got, tc.want)
		}
	}
}

// TestHyDENilSeamsDegrade: a generator constructed with nil seams must never
// call anything, even when enabled.
func TestHyDENilSeamsDegrade(t *testing.T) {
	gen := NewHyDEGenerator(true, "m", 0, 0, nil, nil)
	if gen.Enabled() {
		t.Fatal("nil seams must disable the generator")
	}
	if _, ok := gen.VectorForQuery(context.Background(), TierSemanticHybrid, "q"); ok {
		t.Fatal("nil-seam generator must degrade to false")
	}
}

// TestHyDENilReceiverDegrade guards the nil-safe accessor contract.
func TestHyDENilReceiverDegrade(t *testing.T) {
	var gen *HyDEGenerator
	if gen.Enabled() || gen.Calls() != 0 || gen.Hits() != 0 || gen.Misses() != 0 {
		t.Fatal("nil generator must degrade")
	}
	if _, ok := gen.VectorForQuery(context.Background(), TierSemanticHybrid, "q"); ok {
		t.Fatal("nil generator must degrade to false")
	}
}

// TestHyDEPromptShape pins the canonical prompt contract for composition.
func TestHyDEPromptShape(t *testing.T) {
	p := hydePrompt("why does auth fail")
	if len(p) == 0 || !contains(p, "why does auth fail") {
		t.Fatalf("prompt must embed the query verbatim, got %q", p)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
