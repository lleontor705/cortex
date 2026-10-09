package embedding

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// dedupVectorStore extends fakeVectorWriter with a canned Search reply so the
// worker's dedup hook can be exercised end-to-end.
type dedupVectorStore struct {
	*fakeVectorWriter

	mu         sync.Mutex
	candidates []domain.VectorCandidate
	searchErr  error
	searches   int
}

func (f *dedupVectorStore) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches++
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	out := make([]domain.VectorCandidate, len(f.candidates))
	copy(out, f.candidates)
	return out, nil
}

func (f *dedupVectorStore) setSearch(cands []domain.VectorCandidate, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candidates = cands
	f.searchErr = err
}

func (f *dedupVectorStore) searchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searches
}

// fakeGraphStore records created edges in memory.
type fakeGraphStore struct {
	mu    sync.Mutex
	edges []*domain.Edge
}

func (f *fakeGraphStore) CreateEdge(_ context.Context, edge *domain.Edge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edges = append(f.edges, edge)
	return nil
}

func (f *fakeGraphStore) GetEdgesForObservation(_ context.Context, obsID int64) ([]*domain.Edge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Edge
	for _, e := range f.edges {
		if e.FromObsID == obsID || e.ToObsID == obsID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeGraphStore) countDuplicatesOf(fromObsID, toObsID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.edges {
		if e.RelationType == RelationDuplicatesOf && e.FromObsID == fromObsID && e.ToObsID == toObsID {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestWorker_Dedup_LinksDuplicate runs a full worker loop: when the fake
// vector search reports a near-duplicate (different id, score >= threshold),
// a duplicates_of edge from the new observation to the existing one is
// created and the intent still completes normally.
func TestWorker_Dedup_LinksDuplicate(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	canonicalID := insertTestObservation(t, obsStore, "canonical", "original content")
	dupID := insertTestObservation(t, obsStore, "dupe", "same content again")
	intentID := enqueueIntent(t, db, dupID, "dedup-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "dedup-model"}
	vec := &dedupVectorStore{fakeVectorWriter: newFakeVectorWriter()}
	vec.setSearch([]domain.VectorCandidate{
		{ID: dupID, Score: 1.0},        // self (excluded)
		{ID: canonicalID, Score: 0.97}, // the near-duplicate
	}, nil)
	graph := &fakeGraphStore{}

	w := NewWorker(outbox, obsStore, embSvc, vec, fastWorkerConfig())
	w.EnableDedup(graph, &DedupConfig{Enabled: true, Threshold: DefaultDedupThreshold})
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlite.OutboxStatusComplete, 3*time.Second)

	if n := graph.countDuplicatesOf(dupID, canonicalID); n != 1 {
		t.Fatalf("duplicates_of edges %d->%d = %d, want 1", dupID, canonicalID, n)
	}
	graph.mu.Lock()
	edge := graph.edges[0]
	graph.mu.Unlock()
	if edge.Confidence != 0.97 || edge.Source != dedupSource || edge.Weight != 1.0 {
		t.Errorf("edge metadata = confidence %.2f source %q weight %.2f", edge.Confidence, edge.Source, edge.Weight)
	}
	if edge.Reasoning == "" {
		t.Error("edge reasoning is empty")
	}
}

// TestWorker_Dedup_NoRelationForNonDuplicate: candidate below threshold →
// no edge, intent completes.
func TestWorker_Dedup_NoRelationForNonDuplicate(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	otherID := insertTestObservation(t, obsStore, "other", "different content")
	obsID := insertTestObservation(t, obsStore, "subject", "subject content")
	intentID := enqueueIntent(t, db, obsID, "dedup-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "dedup-model"}
	vec := &dedupVectorStore{fakeVectorWriter: newFakeVectorWriter()}
	vec.setSearch([]domain.VectorCandidate{
		{ID: obsID, Score: 1.0},
		{ID: otherID, Score: 0.80}, // below 0.95
	}, nil)
	graph := &fakeGraphStore{}

	w := NewWorker(outbox, obsStore, embSvc, vec, fastWorkerConfig())
	w.EnableDedup(graph, &DedupConfig{Enabled: true, Threshold: DefaultDedupThreshold})
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlite.OutboxStatusComplete, 3*time.Second)
	if n := graph.countDuplicatesOf(obsID, otherID); n != 0 {
		t.Fatalf("unexpected duplicates_of edges = %d, want 0", n)
	}
	if !vec.has(obsID) {
		t.Fatal("vector was not upserted: dedup must not affect the embed outcome")
	}
}

// TestWorker_DedupDisabled_NoBehaviorChange: without EnableDedup (flag OFF is
// the default), processing is byte-for-byte the legacy path — no searches, no
// edges.
func TestWorker_DedupDisabled_NoBehaviorChange(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "plain", "plain content")
	intentID := enqueueIntent(t, db, obsID, "plain-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "plain-model"}
	vec := &dedupVectorStore{fakeVectorWriter: newFakeVectorWriter()}
	graph := &fakeGraphStore{}

	w := NewWorker(outbox, obsStore, embSvc, vec, fastWorkerConfig())
	w.EnableDedup(graph, nil) // nil cfg = disabled
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlite.OutboxStatusComplete, 3*time.Second)
	if vec.searchCount() != 0 {
		t.Fatalf("dedup search ran with the feature disabled (%d searches)", vec.searchCount())
	}
	if len(graph.edges) != 0 {
		t.Fatalf("edges created with the feature disabled: %d", len(graph.edges))
	}
}

// TestWorker_Dedup_IdempotentRetry: a retried intent must not duplicate the
// duplicates_of edge.
func TestWorker_Dedup_IdempotentRetry(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	canonicalID := insertTestObservation(t, obsStore, "canonical", "original")
	dupID := insertTestObservation(t, obsStore, "dupe", "same")
	intentID := enqueueIntent(t, db, dupID, "idem-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "idem-model"}
	vec := &dedupVectorStore{fakeVectorWriter: newFakeVectorWriter()}
	vec.setSearch([]domain.VectorCandidate{{ID: canonicalID, Score: 0.98}}, nil)
	graph := &fakeGraphStore{}

	w := NewWorker(outbox, obsStore, embSvc, vec, fastWorkerConfig())
	w.EnableDedup(graph, &DedupConfig{Enabled: true, Threshold: DefaultDedupThreshold})
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlite.OutboxStatusComplete, 3*time.Second)
	// Simulate the retry processing the same intent again.
	w.maybeLinkDuplicate(context.Background(), dupID, make([]float32, 768))

	if n := graph.countDuplicatesOf(dupID, canonicalID); n != 1 {
		t.Fatalf("duplicates_of edges after re-run = %d, want 1", n)
	}
}

// TestWorker_Dedup_SearchFailureDoesNotFailIntent: the hook is best-effort.
func TestWorker_Dedup_SearchFailureDoesNotFailIntent(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "resilient", "content")
	intentID := enqueueIntent(t, db, obsID, "fail-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "fail-model"}
	vec := &dedupVectorStore{fakeVectorWriter: newFakeVectorWriter()}
	vec.setSearch(nil, errors.New("vector index unavailable"))
	graph := &fakeGraphStore{}

	w := NewWorker(outbox, obsStore, embSvc, vec, fastWorkerConfig())
	w.EnableDedup(graph, &DedupConfig{Enabled: true, Threshold: DefaultDedupThreshold})
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlite.OutboxStatusComplete, 3*time.Second)
	if len(graph.edges) != 0 {
		t.Fatalf("edges recorded despite search failure: %d", len(graph.edges))
	}
}

// TestDedupConfigFromEnv covers the flag/threshold environment contract.
func TestDedupConfigFromEnv(t *testing.T) {
	if cfg := DedupConfigFromEnv(); cfg != nil {
		t.Fatalf("feature enabled by default: %+v", cfg)
	}

	t.Setenv(EnvIngestDedup, "true")
	cfg := DedupConfigFromEnv()
	if cfg == nil || !cfg.Enabled || cfg.Threshold != DefaultDedupThreshold {
		t.Fatalf("enabled config = %+v, want enabled with default threshold %.2f", cfg, DefaultDedupThreshold)
	}

	t.Setenv(EnvIngestDedup, "TRUE") // case-insensitive
	if cfg := DedupConfigFromEnv(); cfg == nil {
		t.Fatal("TRUE not accepted")
	}

	t.Setenv(EnvIngestDedupThreshold, "0.9")
	if cfg := DedupConfigFromEnv(); cfg == nil || cfg.Threshold != 0.9 {
		t.Fatalf("custom threshold config = %+v, want 0.9", cfg)
	}

	t.Setenv(EnvIngestDedupThreshold, "not-a-number")
	if cfg := DedupConfigFromEnv(); cfg == nil || cfg.Threshold != DefaultDedupThreshold {
		t.Fatalf("invalid threshold config = %+v, want default %.2f", cfg, DefaultDedupThreshold)
	}

	t.Setenv(EnvIngestDedupThreshold, "1.5")
	if cfg := DedupConfigFromEnv(); cfg == nil || cfg.Threshold != DefaultDedupThreshold {
		t.Fatalf("out-of-range threshold config = %+v, want default %.2f", cfg, DefaultDedupThreshold)
	}

	t.Setenv(EnvIngestDedup, "false")
	if cfg := DedupConfigFromEnv(); cfg != nil {
		t.Fatalf("explicit false still enabled: %+v", cfg)
	}
}

// TestNearestOtherUnit pins the self-exclusion and ordering-defensive scan.
func TestNearestOtherUnit(t *testing.T) {
	cands := []domain.VectorCandidate{
		{ID: 7, Score: 1.0},  // self
		{ID: 3, Score: 0.99}, // best other
		{ID: 5, Score: 0.96},
	}
	got := nearestOther(cands, 7, 0.95)
	if got == nil || got.ID != 3 {
		t.Fatalf("nearestOther = %+v, want id 3", got)
	}
	if nearestOther(cands, 7, 0.995) != nil {
		t.Fatal("threshold not enforced")
	}
	if nearestOther(nil, 7, 0.5) != nil {
		t.Fatal("empty candidates must yield nil")
	}
}
