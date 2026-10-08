package server

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/postgres"
)

// fakeEmbeddingSource is a scriptable UnembeddedSource recording MarkEmbedded
// calls so the worker's best-effort flag coupling (issue #119) can be proven:
// stamps happen only after a SUCCESSFUL vector upsert, and a stamp failure is
// tolerated (logged) instead of poisoning the batch.
type fakeEmbeddingSource struct {
	batch     []postgres.UnembeddedObservation
	listErr   error
	markErr   error
	markedIDs [][]int64
}

func (f *fakeEmbeddingSource) ListUnembedded(context.Context, int) ([]postgres.UnembeddedObservation, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.batch, nil
}

func (f *fakeEmbeddingSource) MarkEmbedded(_ context.Context, ids []int64) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.markedIDs = append(f.markedIDs, ids)
	return nil
}

type workerTestEmbedder struct{}

func (workerTestEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}
func (workerTestEmbedder) Dimensions() int { return 3 }
func (workerTestEmbedder) Model() string   { return "test-embed" }

type workerTestVectorIndex struct {
	upserts   [][]domain.VectorPoint
	upsertErr error
}

func (v *workerTestVectorIndex) ID() string { return "test" }
func (v *workerTestVectorIndex) Upsert(_ context.Context, points []domain.VectorPoint) error {
	if v.upsertErr != nil {
		return v.upsertErr
	}
	v.upserts = append(v.upserts, points)
	return nil
}
func (v *workerTestVectorIndex) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (v *workerTestVectorIndex) Delete(context.Context, []int64) error { return nil }
func (v *workerTestVectorIndex) Health(context.Context) domain.Health {
	return domain.Health{Status: "healthy"}
}
func (v *workerTestVectorIndex) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{IndexType: "test", MaxDimensions: 3}, nil
}
func (v *workerTestVectorIndex) Close() error { return nil }

func workerSourceBatch() []postgres.UnembeddedObservation {
	return []postgres.UnembeddedObservation{
		{ID: 7, Title: "alpha", Content: "first", ProjectKey: "demo", ProjectPublicID: "proj-1"},
		{ID: 9, Title: "beta", Content: "second", ProjectKey: "demo", ProjectPublicID: "proj-1"},
	}
}

func TestEmbeddingWorkerMarksEmbeddedAfterSuccessfulUpsert(t *testing.T) {
	source := &fakeEmbeddingSource{batch: workerSourceBatch()}
	vectors := &workerTestVectorIndex{}
	worker := &backgroundEmbeddingWorker{
		source:     source,
		embeddings: workerTestEmbedder{},
		vectors:    vectors,
		interval:   0,
	}

	worker.drainBatch(context.Background())

	if len(vectors.upserts) != 1 {
		t.Fatalf("upsert calls = %d, want 1", len(vectors.upserts))
	}
	if len(source.markedIDs) != 1 {
		t.Fatalf("mark embedded calls = %d, want exactly one after a successful upsert", len(source.markedIDs))
	}
	got := append([]int64(nil), source.markedIDs[0]...)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("stamped ids = %v, want [7 9]", got)
	}
}

func TestEmbeddingWorkerSkipsStampWhenUpsertFails(t *testing.T) {
	source := &fakeEmbeddingSource{batch: workerSourceBatch()}
	vectors := &workerTestVectorIndex{upsertErr: errors.New("vector replica down")}
	worker := &backgroundEmbeddingWorker{
		source:     source,
		embeddings: workerTestEmbedder{},
		vectors:    vectors,
		interval:   0,
	}

	worker.drainBatch(context.Background())

	if len(source.markedIDs) != 0 {
		t.Fatalf("stamped ids after failed upsert = %v, want none: state must never run ahead of the replica", source.markedIDs)
	}
}

func TestEmbeddingWorkerToleratesMarkFailure(t *testing.T) {
	// Best-effort coupling (issue #119): a failed stamp is logged and the
	// batch completes; the rows stay unembedded and the next pass retries.
	source := &fakeEmbeddingSource{batch: workerSourceBatch(), markErr: errors.New("authorized stamp denied")}
	vectors := &workerTestVectorIndex{}
	worker := &backgroundEmbeddingWorker{
		source:     source,
		embeddings: workerTestEmbedder{},
		vectors:    vectors,
		interval:   0,
	}

	worker.drainBatch(context.Background())

	if len(vectors.upserts) != 1 {
		t.Fatalf("upsert calls = %d, want 1 (the vector write must not be rolled back by a stamp failure)", len(vectors.upserts))
	}
}

func TestEmbeddingWorkerSkipsStampWhenNothingUpserted(t *testing.T) {
	source := &fakeEmbeddingSource{batch: []postgres.UnembeddedObservation{
		// Blank text rows are filtered before embedding; nothing is upserted.
		{ID: 3, Title: "", Content: "   "},
	}}
	vectors := &workerTestVectorIndex{}
	worker := &backgroundEmbeddingWorker{
		source:     source,
		embeddings: workerTestEmbedder{},
		vectors:    vectors,
		interval:   0,
	}

	worker.drainBatch(context.Background())

	if len(vectors.upserts) != 0 {
		t.Fatalf("upsert calls = %d, want 0", len(vectors.upserts))
	}
	if len(source.markedIDs) != 0 {
		t.Fatalf("stamped ids = %v, want none when no vectors were upserted", source.markedIDs)
	}
}

// Compile-time proof that both the test fake and the production capability
// satisfy the extended worker source contract.
var (
	_ UnembeddedSource = (*fakeEmbeddingSource)(nil)
	_ UnembeddedSource = (*postgres.SystemService)(nil)
)
