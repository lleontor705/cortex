package purego

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func TestPureGoVectorAdapter_CosineSearch(t *testing.T) {
	adapter := New()

	ctx := context.Background()
	points := []domain.VectorPoint{
		{
			ID:     1,
			Vector: []float32{1.0, 0.0, 0.0},
			ModelInfo: domain.ModelInfo{
				Name:      "test-model",
				Dimension: 3,
			},
		},
		{
			ID:     2,
			Vector: []float32{0.0, 1.0, 0.0},
			ModelInfo: domain.ModelInfo{
				Name:      "test-model",
				Dimension: 3,
			},
		},
		{
			ID:     3,
			Vector: []float32{0.8, 0.6, 0.0},
			ModelInfo: domain.ModelInfo{
				Name:      "test-model",
				Dimension: 3,
			},
		},
	}

	if err := adapter.Upsert(ctx, points); err != nil {
		t.Fatalf("failed to upsert vectors: %v", err)
	}

	// Search for vector close to [1, 0, 0]
	query := domain.VectorQuery{
		Vector:    []float32{0.9, 0.1, 0.0},
		Limit:     2,
		Threshold: 0.5,
	}

	results, err := adapter.Search(ctx, query)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].ID != 1 {
		t.Errorf("expected top result to be ID 1, got %d (score: %f)", results[0].ID, results[0].Score)
	}

	if err := adapter.Delete(ctx, []int64{1}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	resultsAfterDelete, err := adapter.Search(ctx, query)
	if err != nil {
		t.Fatalf("search after delete failed: %v", err)
	}
	if len(resultsAfterDelete) > 0 && resultsAfterDelete[0].ID == 1 {
		t.Errorf("expected ID 1 to be deleted")
	}
}
