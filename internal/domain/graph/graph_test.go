package graph

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// batchMockRepo implements domain.GraphRepository backed by an explicit
// undirected adjacency map plus the optional level-neighbor batch capability.
// It can shuffle returned neighbor rows to prove service-level determinism
// (GRAPH-01 adversarial: shuffled batch rows must not change path choice).
type batchMockRepo struct {
	domain.GraphRepository // nil-embedded: unrelated methods must not be called
	adj                    map[int64][]int64
	batchCalls             int
	perNodeCalls           int
	shuffle                bool
	rng                    *rand.Rand
}

func newBatchMockRepo(adj map[int64][]int64, shuffle bool) *batchMockRepo {
	return &batchMockRepo{adj: adj, shuffle: shuffle, rng: rand.New(rand.NewSource(42))}
}

func (m *batchMockRepo) shuffled(ids []int64) []int64 {
	out := make([]int64, len(ids))
	copy(out, ids)
	if m.shuffle {
		m.rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out
}

func (m *batchMockRepo) obs(id int64) *domain.Observation {
	return &domain.Observation{ID: id, Title: fmt.Sprintf("obs-%d", id), Type: domain.TypeManual}
}

func (m *batchMockRepo) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	m.perNodeCalls++
	ids, ok := m.adj[obsID]
	if !ok {
		return []*domain.Observation{}, nil
	}
	ids = m.shuffled(ids)
	out := make([]*domain.Observation, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.obs(id))
	}
	return out, nil
}

func (m *batchMockRepo) GetLevelNeighborObservations(ctx context.Context, frontier []int64) (map[int64][]*domain.Observation, error) {
	m.batchCalls++
	out := make(map[int64][]*domain.Observation, len(frontier))
	for _, id := range frontier {
		ids := m.shuffled(m.adj[id])
		list := make([]*domain.Observation, 0, len(ids))
		for _, n := range ids {
			list = append(list, m.obs(n))
		}
		out[id] = list
	}
	return out, nil
}

// plainMockRepo exposes only domain.GraphRepository (no batch capability) to
// exercise the bounded deterministic per-node fallback.
type plainMockRepo struct {
	domain.GraphRepository
	adj          map[int64][]int64
	perNodeCalls int
}

func (m *plainMockRepo) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	m.perNodeCalls++
	ids, ok := m.adj[obsID]
	if !ok {
		return []*domain.Observation{}, nil
	}
	out := make([]*domain.Observation, 0, len(ids))
	for _, id := range ids {
		out = append(out, &domain.Observation{ID: id, Title: fmt.Sprintf("obs-%d", id)})
	}
	return out, nil
}

// joinedStarAdj builds the two-300-leaf stars joined at hubs fixture from
// cortex:#516: hub A=1 with leaves 2..301, hub B=302 with leaves 303..602,
// joined through the smallest leaf (2). The 3-hop path A->2->B->303 is the
// lexicographically smallest shortest path.
func joinedStarAdj() map[int64][]int64 {
	adj := make(map[int64][]int64, 902)
	leaves := make([]int64, 0, 300)
	for id := int64(2); id <= 301; id++ {
		leaves = append(leaves, id)
	}
	ms := make([]int64, 0, 300)
	for id := int64(303); id <= 602; id++ {
		ms = append(ms, id)
	}
	adj[1] = leaves
	for _, l := range leaves {
		adj[l] = []int64{1}
	}
	adj[2] = append([]int64{302}, adj[2]...)
	adj[302] = ms
	for _, m := range ms {
		adj[m] = []int64{302}
	}
	return adj
}

func pathIDs(path []int64) []int64 { return path }

// --- GRAPH-01: level-batched deterministic FindPath -------------------------

func TestFindPathJoinedStarLexicographicSmallestThreeQueries(t *testing.T) {
	repo := newBatchMockRepo(joinedStarAdj(), true /* shuffled adjacency */)
	svc := NewService(repo)

	path, err := svc.FindPathBounded(context.Background(), 1, 303, 5, 0)
	if err != nil {
		t.Fatalf("FindPathBounded: %v", err)
	}
	if want := []int64{1, 2, 302, 303}; !reflect.DeepEqual(path, want) {
		t.Fatalf("path = %v, want lexicographically smallest %v", pathIDs(path), want)
	}
	if repo.batchCalls != 3 {
		t.Fatalf("level-batched lookups = %d, want exactly 3 for a 3-hop path", repo.batchCalls)
	}
	if repo.perNodeCalls != 0 {
		t.Fatalf("per-node fallback used (%d calls) despite batch capability", repo.perNodeCalls)
	}
}

func TestFindPathLexicographicChoiceBeatsSmallestParent(t *testing.T) {
	// Two shortest 3-hop paths 1->2->9->10 and 1->3->4->10. The smallest
	// parent of 10 is 4, but the lexicographically smallest path starts with 2.
	adj := map[int64][]int64{
		1:  {2, 3},
		2:  {1, 9},
		3:  {1, 4},
		4:  {3, 10},
		9:  {2, 10},
		10: {4, 9},
	}
	repo := newBatchMockRepo(adj, true)
	svc := NewService(repo)

	path, err := svc.FindPathBounded(context.Background(), 1, 10, 5, 0)
	if err != nil {
		t.Fatalf("FindPathBounded: %v", err)
	}
	if want := []int64{1, 2, 9, 10}; !reflect.DeepEqual(path, want) {
		t.Fatalf("path = %v, want %v (lexicographically smallest, not smallest-parent)", path, want)
	}
}

func TestFindPathBudgetBoundaries(t *testing.T) {
	chain := func() map[int64][]int64 {
		return map[int64][]int64{1: {2}, 2: {1, 3}, 3: {2, 4}, 4: {3}}
	}

	t.Run("exact budget that proves the path succeeds", func(t *testing.T) {
		svc := NewService(newBatchMockRepo(chain(), false))
		path, err := svc.FindPathBounded(context.Background(), 1, 4, 10, 4)
		if err != nil {
			t.Fatalf("exact budget must succeed: %v", err)
		}
		if want := []int64{1, 2, 3, 4}; !reflect.DeepEqual(path, want) {
			t.Fatalf("path = %v, want %v", path, want)
		}
	})

	t.Run("one less than needed returns stable truncation error", func(t *testing.T) {
		svc := NewService(newBatchMockRepo(chain(), false))
		path, err := svc.FindPathBounded(context.Background(), 1, 4, 10, 3)
		if !errors.Is(err, ErrTraversalTruncated) {
			t.Fatalf("err = %v, want ErrTraversalTruncated", err)
		}
		if path != nil {
			t.Fatalf("path = %v, want nil on truncation", path)
		}
	})

	t.Run("missing endpoint with generous budget is a true no-path", func(t *testing.T) {
		svc := NewService(newBatchMockRepo(chain(), false))
		path, err := svc.FindPathBounded(context.Background(), 1, 99, 10, 100)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if path != nil {
			t.Fatalf("path = %v, want nil", path)
		}
	})

	t.Run("exact budget proving no-path is not truncation", func(t *testing.T) {
		adj := map[int64][]int64{1: {2, 3}, 2: {1}, 3: {1}}
		svc := NewService(newBatchMockRepo(adj, false))
		path, err := svc.FindPathBounded(context.Background(), 1, 99, 10, 3)
		if err != nil {
			t.Fatalf("complete traversal within budget must prove no-path: %v", err)
		}
		if path != nil {
			t.Fatalf("path = %v, want nil", path)
		}
	})

	t.Run("budget exhaustion before no-path proof is truncation", func(t *testing.T) {
		// Path 1->3->4 exists but only through the node omitted by the budget.
		adj := map[int64][]int64{1: {2, 3}, 2: {1}, 3: {1, 4}, 4: {3}}
		svc := NewService(newBatchMockRepo(adj, false))
		if _, err := svc.FindPathBounded(context.Background(), 1, 4, 10, 2); !errors.Is(err, ErrTraversalTruncated) {
			t.Fatalf("err = %v, want ErrTraversalTruncated", err)
		}
	})

	t.Run("cycle and duplicate edges do not double count", func(t *testing.T) {
		adj := map[int64][]int64{1: {2, 2}, 2: {1, 1, 3}, 3: {2}}
		svc := NewService(newBatchMockRepo(adj, true))
		path, err := svc.FindPathBounded(context.Background(), 1, 3, 10, 3)
		if err != nil {
			t.Fatalf("cycle with exact budget must not truncate: %v", err)
		}
		if want := []int64{1, 2, 3}; !reflect.DeepEqual(path, want) {
			t.Fatalf("path = %v, want %v", path, want)
		}
	})

	t.Run("depth exhaustion is a bounded no-path not truncation", func(t *testing.T) {
		svc := NewService(newBatchMockRepo(chain(), false))
		path, err := svc.FindPathBounded(context.Background(), 1, 4, 2, 100)
		if err != nil {
			t.Fatalf("err = %v, want nil (depth-bounded no-path)", err)
		}
		if path != nil {
			t.Fatalf("path = %v, want nil", path)
		}
	})

	t.Run("identity path uses no lookups and no budget", func(t *testing.T) {
		repo := newBatchMockRepo(chain(), false)
		svc := NewService(repo)
		path, err := svc.FindPathBounded(context.Background(), 7, 7, 0, 1)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if want := []int64{7}; !reflect.DeepEqual(path, want) {
			t.Fatalf("path = %v, want [7]", path)
		}
		if repo.batchCalls != 0 {
			t.Fatalf("identity path issued %d lookups, want 0", repo.batchCalls)
		}
	})
}

func TestFindPathFallbackParityAndBoundedPerNodeCalls(t *testing.T) {
	adj := joinedStarAdj()
	batched := NewService(newBatchMockRepo(adj, true))
	gotBatch, err := batched.FindPathBounded(context.Background(), 1, 303, 5, 0)
	if err != nil {
		t.Fatalf("batched: %v", err)
	}
	repo := &plainMockRepo{adj: adj}
	fallback := NewService(repo)
	gotFallback, err := fallback.FindPathBounded(context.Background(), 1, 303, 5, 0)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if !reflect.DeepEqual(gotBatch, gotFallback) {
		t.Fatalf("fallback path %v != batched path %v", gotFallback, gotBatch)
	}
	// Bounded per-node fallback: default budget 1000 admits 303 nodes for this
	// fixture, so per-node lookups must stay within the admitted set.
	if repo.perNodeCalls > 303 {
		t.Fatalf("fallback issued %d per-node lookups, want <= 303 (admitted nodes)", repo.perNodeCalls)
	}
}

// --- GRAPH-02: max_visited/max_results truncation semantics ------------------

func resultIDs(res *domain.GraphTraversalResult) []int64 {
	ids := make([]int64, 0, len(res.Observations))
	for _, o := range res.Observations {
		ids = append(ids, o.ID)
	}
	return ids
}

func TestGetRelatedBoundedOrdersByHopThenID(t *testing.T) {
	adj := map[int64][]int64{
		1: {5, 3},
		5: {1, 7},
		3: {1, 6},
	}
	svc := NewService(newBatchMockRepo(adj, true))
	res, err := svc.GetRelatedBounded(context.Background(), 1, domain.GraphTraversalOptions{Depth: 2})
	if err != nil {
		t.Fatalf("GetRelatedBounded: %v", err)
	}
	if want := []int64{3, 5, 6, 7}; !reflect.DeepEqual(resultIDs(res), want) {
		t.Fatalf("rows = %v, want hop-then-ID order %v", resultIDs(res), want)
	}
	if res.Truncated {
		t.Fatalf("complete traversal flagged truncated: %+v", res.TruncationReasons)
	}
}

func TestGetRelatedBoundedTruncationMatrix(t *testing.T) {
	star := func() map[int64][]int64 {
		return map[int64][]int64{1: {2, 3, 4, 5, 6}, 2: {1}, 3: {1}, 4: {1}, 5: {1}, 6: {1}}
	}
	ctx := context.Background()

	cases := []struct {
		name        string
		maxVisited  int
		maxResults  int
		wantRows    []int64
		wantTrunc   bool
		wantReasons []string
	}{
		{
			name:       "budget one visits root and emits zero",
			maxVisited: 1, maxResults: 100,
			wantRows:    []int64{},
			wantTrunc:   true,
			wantReasons: []string{domain.TruncationReasonMaxVisited},
		},
		{
			name:       "max visited exactly equal is not truncated",
			maxVisited: 6, maxResults: 100, // root + five neighbors == limit
			wantRows:  []int64{2, 3, 4, 5, 6},
			wantTrunc: false,
		},
		{
			name:       "max visited one less drops the sentinel row",
			maxVisited: 5, maxResults: 100,
			wantRows:    []int64{2, 3, 4, 5},
			wantTrunc:   true,
			wantReasons: []string{domain.TruncationReasonMaxVisited},
		},
		{
			name:       "max results exactly equal is not truncated",
			maxVisited: 10, maxResults: 5,
			wantRows:  []int64{2, 3, 4, 5, 6},
			wantTrunc: false,
		},
		{
			name:       "max results one less reports max_results",
			maxVisited: 10, maxResults: 4,
			wantRows:    []int64{2, 3, 4, 5},
			wantTrunc:   true,
			wantReasons: []string{domain.TruncationReasonMaxResults},
		},
		{
			name:       "both limits report both reasons",
			maxVisited: 5, maxResults: 2,
			wantRows:    []int64{2, 3},
			wantTrunc:   true,
			wantReasons: []string{domain.TruncationReasonMaxVisited, domain.TruncationReasonMaxResults},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(newBatchMockRepo(star(), true))
			res, err := svc.GetRelatedBounded(ctx, 1, domain.GraphTraversalOptions{Depth: 1, MaxVisited: tc.maxVisited, MaxResults: tc.maxResults})
			if err != nil {
				t.Fatalf("GetRelatedBounded: %v", err)
			}
			if got := resultIDs(res); !reflect.DeepEqual(got, tc.wantRows) {
				t.Fatalf("rows = %v, want %v", got, tc.wantRows)
			}
			if res.Truncated != tc.wantTrunc {
				t.Fatalf("truncated = %v, want %v", res.Truncated, tc.wantTrunc)
			}
			if tc.wantTrunc && !reflect.DeepEqual(res.TruncationReasons, tc.wantReasons) {
				t.Fatalf("reasons = %v, want %v", res.TruncationReasons, tc.wantReasons)
			}
			if !tc.wantTrunc && len(res.TruncationReasons) != 0 {
				t.Fatalf("unexpected reasons on complete result: %v", res.TruncationReasons)
			}
		})
	}
}

func TestGetRelatedBoundedNineResultsUnderVisitedTen(t *testing.T) {
	// GRAPH-02 boundary: nine eligible neighbors, max_visited ten, max_results
	// nine: all nine rows are emitted and nothing is truncated.
	adj := map[int64][]int64{1: {2, 3, 4, 5, 6, 7, 8, 9, 10}}
	for id := int64(2); id <= 10; id++ {
		adj[id] = []int64{1}
	}
	svc := NewService(newBatchMockRepo(adj, true))
	res, err := svc.GetRelatedBounded(context.Background(), 1, domain.GraphTraversalOptions{Depth: 2, MaxVisited: 10, MaxResults: 9})
	if err != nil {
		t.Fatalf("GetRelatedBounded: %v", err)
	}
	if want := []int64{2, 3, 4, 5, 6, 7, 8, 9, 10}; !reflect.DeepEqual(resultIDs(res), want) {
		t.Fatalf("rows = %v, want %v", resultIDs(res), want)
	}
	if res.Truncated {
		t.Fatalf("exact limits must not report truncation: %v", res.TruncationReasons)
	}
}

func TestGetRelatedBoundedFallbackParity(t *testing.T) {
	adj := map[int64][]int64{
		1: {5, 3},
		5: {1, 7},
		3: {1, 6},
	}
	ctx := context.Background()
	batched, err := NewService(newBatchMockRepo(adj, true)).GetRelatedBounded(ctx, 1, domain.GraphTraversalOptions{Depth: 2, MaxVisited: 3, MaxResults: 2})
	if err != nil {
		t.Fatalf("batched: %v", err)
	}
	plain, err := NewService(&plainMockRepo{adj: adj}).GetRelatedBounded(ctx, 1, domain.GraphTraversalOptions{Depth: 2, MaxVisited: 3, MaxResults: 2})
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if !reflect.DeepEqual(resultIDs(batched), resultIDs(plain)) {
		t.Fatalf("fallback rows %v != batched rows %v", resultIDs(plain), resultIDs(batched))
	}
	if batched.Truncated != plain.Truncated || !reflect.DeepEqual(batched.TruncationReasons, plain.TruncationReasons) {
		t.Fatalf("fallback truncation %+v != batched %+v", plain, batched)
	}
}

func TestGetRelatedBoundedDeterministicUnderShuffledAdjacency(t *testing.T) {
	adj := joinedStarAdj()
	var first []int64
	for i := 0; i < 20; i++ {
		svc := NewService(newBatchMockRepo(adj, true))
		res, err := svc.GetRelatedBounded(context.Background(), 1, domain.GraphTraversalOptions{Depth: 3, MaxVisited: 50, MaxResults: 20})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		got := resultIDs(res)
		if i == 0 {
			first = got
			continue
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d rows %v != first run %v", i, got, first)
		}
	}
}

func TestGetRelatedBoundedDepthValidation(t *testing.T) {
	svc := NewService(newBatchMockRepo(map[int64][]int64{1: {2}}, false))
	if _, err := svc.GetRelatedBounded(context.Background(), 1, domain.GraphTraversalOptions{Depth: 0}); !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("err = %v, want ErrInvalidDepth", err)
	}
	if _, err := svc.GetRelatedBounded(context.Background(), 1, domain.GraphTraversalOptions{Depth: 11}); !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("err = %v, want ErrInvalidDepth", err)
	}
}

func TestBudgetNormalization(t *testing.T) {
	if got := normalizeMaxVisited(0); got != DefaultMaxVisited {
		t.Fatalf("normalizeMaxVisited(0) = %d, want %d", got, DefaultMaxVisited)
	}
	if got := normalizeMaxVisited(-5); got != DefaultMaxVisited {
		t.Fatalf("normalizeMaxVisited(-5) = %d, want %d", got, DefaultMaxVisited)
	}
	if got := normalizeMaxVisited(999999); got != MaxVisitedCap {
		t.Fatalf("normalizeMaxVisited(999999) = %d, want %d", got, MaxVisitedCap)
	}
	if got := normalizeMaxVisited(42); got != 42 {
		t.Fatalf("normalizeMaxVisited(42) = %d, want 42", got)
	}
	if got := normalizeMaxResults(0); got != DefaultMaxResults {
		t.Fatalf("normalizeMaxResults(0) = %d, want %d", got, DefaultMaxResults)
	}
	if got := normalizeMaxResults(999999); got != MaxResultsCap {
		t.Fatalf("normalizeMaxResults(999999) = %d, want %d", got, MaxResultsCap)
	}
}
