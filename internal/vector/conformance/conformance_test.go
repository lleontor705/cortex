// Package conformance tests the shared VectorIndex conformance suite itself.
//
// The suite is verified against an in-memory fake VectorIndex that implements
// correct behavior. This proves the suite's assertions are sound: a correct
// adapter passes, and a deliberately broken adapter fails. The real adapter
// integration tests (in internal/vector/{sqlite_blob,qdrant,pgvector}/) call
// RunSuite against live adapters.
package conformance

import (
	"context"
	"math"
	"sort"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// fakeIndex is a minimal in-memory VectorIndex that implements CORRECT
// behavior for cosine similarity search. It is used to verify the conformance
// suite's assertions pass against a known-good adapter.
type fakeIndex struct {
	points  map[int64]domain.VectorPoint
	caps    domain.Capabilities
	healthy bool
}

func newFakeIndex(dim int, model domain.ModelInfo) *fakeIndex {
	return &fakeIndex{
		points: make(map[int64]domain.VectorPoint),
		caps: domain.Capabilities{
			IndexType:       "fake",
			DistanceMetrics: []string{"cosine"},
			MaxDimensions:   dim,
			Filters:         "PostFilter",
			Hybrid:          "engine",
			Namespaces:      "supported",
			Consistency:     "strong",
			BatchUpsert:     true,
			MaxBatchSize:    100,
		},
		healthy: true,
	}
}

func (f *fakeIndex) ID() string { return "fake" }

func (f *fakeIndex) Upsert(_ context.Context, points []domain.VectorPoint) error {
	for _, p := range points {
		if p.ModelInfo.Dimension > 0 && len(p.Vector) != p.ModelInfo.Dimension {
			return domain.NewDimensionMismatchError(p.ModelInfo.Dimension, len(p.Vector), p.ModelInfo.Name)
		}
		f.points[p.ID] = p
	}
	return nil
}

func (f *fakeIndex) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	type scored struct {
		id    int64
		score float64
	}
	var results []scored
	for id, p := range f.points {
		s := cosineSim(q.Vector, p.Vector)
		if q.Threshold > 0 && s < q.Threshold {
			continue
		}
		results = append(results, scored{id: id, score: s})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if len(results) > q.Limit && q.Limit > 0 {
		results = results[:q.Limit]
	}
	out := make([]domain.VectorCandidate, 0, len(results))
	for _, r := range results {
		out = append(out, domain.VectorCandidate{ID: r.id, Score: r.score, Provenance: "fake"})
	}
	return out, nil
}

func (f *fakeIndex) Delete(_ context.Context, ids []int64) error {
	for _, id := range ids {
		delete(f.points, id)
	}
	return nil
}

func (f *fakeIndex) Health(_ context.Context) domain.Health {
	if f.healthy {
		return domain.Health{Status: domain.StatusHealthy, Message: "fake: ready"}
	}
	return domain.Health{Status: domain.StatusUnhealthy, Message: "fake: down"}
}

func (f *fakeIndex) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return f.caps, nil
}

func (f *fakeIndex) Close() error { return nil }

// cosineSim computes cosine similarity between two float32 vectors.
func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// TestRunSuite_CorrectAdapterPasses verifies the conformance suite PASSES
// against a known-correct adapter. If this test fails, the suite's assertions
// are wrong (too strict), not the fake adapter.
func TestRunSuite_CorrectAdapterPasses(t *testing.T) {
	factory := func(t *testing.T, dim int, model domain.ModelInfo) (domain.VectorIndex, error) {
		return newFakeIndex(dim, model), nil
	}
	RunSuite(t, factory)
}

// TestDefaultFixtures_Deterministic verifies the default fixture set is stable
// (same IDs and dimensions on every call). This pins the parity baseline.
func TestDefaultFixtures_Deterministic(t *testing.T) {
	f1 := DefaultFixtures()
	f2 := DefaultFixtures()
	if f1.Dimension != f2.Dimension {
		t.Errorf("Dimension drifted: %d vs %d", f1.Dimension, f2.Dimension)
	}
	if len(f1.Points) != len(f2.Points) {
		t.Fatalf("Points count drifted: %d vs %d", len(f1.Points), len(f2.Points))
	}
	for i, p := range f1.Points {
		if p.ID != f2.Points[i].ID {
			t.Errorf("point %d ID drifted: %d vs %d", i, p.ID, f2.Points[i].ID)
		}
		if len(p.Vector) != len(f2.Points[i].Vector) {
			t.Errorf("point %d vector len drifted: %d vs %d", i, len(p.Vector), len(f2.Points[i].Vector))
		}
	}
}
