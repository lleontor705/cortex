package search

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// gatePromoteReranker moves one candidate to the front while preserving the
// rest, so a test can prove a candidate that plain ranking would have
// truncated away is the one returned.
type gatePromoteReranker struct{ firstID int64 }

func (g gatePromoteReranker) Rerank(_ string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	out := make([]*domain.SearchResult, 0, len(docs))
	for _, d := range docs {
		if d.ID == g.firstID {
			out = append(out, d)
		}
	}
	for _, d := range docs {
		if d.ID != g.firstID {
			out = append(out, d)
		}
	}
	return out, nil
}

type gateFailingReranker struct{}

func (gateFailingReranker) Rerank(string, []*domain.SearchResult) ([]*domain.SearchResult, error) {
	return nil, errors.New("rerank endpoint unavailable")
}

type gateDroppingReranker struct{}

func (gateDroppingReranker) Rerank(_ string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	return docs[:1], nil
}

func withStoreReranker(t *testing.T, store *Store, reranker retrieval.Reranker) {
	t.Helper()
	store.rerank.mu.Lock()
	store.rerank.reranker = reranker
	store.rerank.loaded = true
	store.rerank.mu.Unlock()
}

func gateIDs(results []*domain.SearchResult) []int64 {
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids
}

// TestRerankGate_DefaultDisabledIsIdentity pins the disabled gate: default
// config builds no reranker and applyRerank returns the fusion order untouched
// (REQ-RNK-002 byte-identity on the store path).
func TestRerankGate_DefaultDisabledIsIdentity(t *testing.T) {
	if cfg, err := config.Load(""); err == nil && cfg.Search.RerankProvider != "" &&
		cfg.Search.RerankProvider != "none" {
		t.Skipf("search.rerank_provider=%q in this environment; byte-identity pin requires the default", cfg.Search.RerankProvider)
	}

	store := NewStore(setupTestDB(t))
	if reranker := store.rerank.current(); reranker != nil {
		t.Fatalf("default config constructed %T; the disabled default must build zero rerank machinery", reranker)
	}

	candidates := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 7, Title: "Second"}, Rank: 0.4},
		{Observation: domain.Observation{ID: 3, Title: "First"}, Rank: 0.3},
	}
	got := store.rerank.applyRerank("alpha beta", candidates)

	if !reflect.DeepEqual(gateIDs(got), []int64{7, 3}) {
		t.Fatalf("disabled gate reordered candidates: got %v, want [7 3]", gateIDs(got))
	}
	if got[0].Rank != 0.4 || got[1].Rank != 0.3 {
		t.Fatalf("disabled gate mutated ranks: got [%v %v], want [0.4 0.3]", got[0].Rank, got[1].Rank)
	}
}

// TestRerankGate_RerankOrdersBeforeLimit runs the full assembly path: the
// reranker promotes a candidate that plain ranking places last, and that
// candidate is what a limit of 1 returns — proving rerank runs before the
// final truncation, never after.
func TestRerankGate_RerankOrdersBeforeLimit(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Alpha Beta Draft", "alpha beta scratch notes kept for later review", "manual", "test-project", "project")
	insertTestObservation(t, db, 2, "Alpha Beta Decision", "alpha beta decision recorded for the team and enforced everywhere", "manual", "test-project", "project")

	ctx := context.Background()
	opts := domain.SearchOptions{Project: "test-project", Limit: 5}
	control, err := NewStore(db).Search(ctx, "alpha beta", opts)
	if err != nil {
		t.Fatalf("control search: %v", err)
	}
	if len(control) < 2 {
		t.Fatalf("fixture needs >= 2 fused candidates, got %d", len(control))
	}
	naturalTop := control[0].ID
	promoted := control[len(control)-1].ID
	if promoted == naturalTop {
		t.Fatalf("fixture is degenerate: control order %v", gateIDs(control))
	}

	store := NewStore(db)
	withStoreReranker(t, store, gatePromoteReranker{firstID: promoted})

	got, err := store.Search(ctx, "alpha beta", domain.SearchOptions{Project: "test-project", Limit: 1})
	if err != nil {
		t.Fatalf("reranked search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ID != promoted {
		t.Fatalf("got ID %d (natural top %d), want rerank winner %d: rerank must order before the limit", got[0].ID, naturalTop, promoted)
	}
}

// TestRerankGate_RerankFailureKeepsFusionOrder pins the degrade policy on the
// store path: a failing reranker yields exactly the un-reranked result order.
func TestRerankGate_RerankFailureKeepsFusionOrder(t *testing.T) {
	db := setupTestDB(t)
	insertTestObservation(t, db, 1, "Alpha Beta Draft", "alpha beta scratch notes kept for later review", "manual", "test-project", "project")
	insertTestObservation(t, db, 2, "Alpha Beta Decision", "alpha beta decision recorded for the team and enforced everywhere", "manual", "test-project", "project")

	ctx := context.Background()
	opts := domain.SearchOptions{Project: "test-project", Limit: 5}
	control, err := NewStore(db).Search(ctx, "alpha beta", opts)
	if err != nil {
		t.Fatalf("control search: %v", err)
	}

	store := NewStore(db)
	withStoreReranker(t, store, gateFailingReranker{})
	got, err := store.Search(ctx, "alpha beta", opts)
	if err != nil {
		t.Fatalf("search with failing reranker: %v", err)
	}
	if !reflect.DeepEqual(gateIDs(got), gateIDs(control)) {
		t.Fatalf("failing reranker changed output: got %v, want %v", gateIDs(got), gateIDs(control))
	}
}

// TestRerankGate_SetBreakingOutputDegrades pins the set-integrity guard: a
// provider that drops candidates must not silently shrink the result set.
func TestRerankGate_SetBreakingOutputDegrades(t *testing.T) {
	store := &Store{}
	withStoreReranker(t, store, gateDroppingReranker{})

	candidates := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "First"}, Rank: 0.5},
		{Observation: domain.Observation{ID: 2, Title: "Second"}, Rank: 0.4},
	}
	got := store.rerank.applyRerank("alpha beta", candidates)
	if !reflect.DeepEqual(gateIDs(got), []int64{1, 2}) {
		t.Fatalf("set-breaking rerank was applied: got %v, want un-reranked [1 2]", gateIDs(got))
	}
}
