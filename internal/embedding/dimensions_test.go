package embedding

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqliteblob "github.com/lleontor705/cortex/v2/internal/vector/sqlite_blob"
	"github.com/lleontor705/cortex/v2/testutil"
)

// TestUpsertRejectsDrifted4096DimVectorAfter768Baseline pins REQ-EMB-002's
// guard: a corpus whose declared dimension is 768 must reject a 4096-dim
// upsert with domain.ErrDimensionMismatch instead of storing dimension-corrupt
// rows. The declared ModelInfo dimension is the namespace the adapter
// enforces, so it must be fed from the live service rather than a static
// provider map; this upsert check is the fail-closed backstop when the two
// ever disagree (e.g. after an embedding model switch).
func TestUpsertRejectsDrifted4096DimVectorAfter768Baseline(t *testing.T) {
	db := testutil.NewTestDB(t)
	index := sqliteblob.New(db.DB())
	ctx := context.Background()

	baseline := domain.VectorPoint{
		ID:        1,
		Vector:    make([]float32, 768),
		ModelInfo: domain.ModelInfo{Name: "qwen3-embedding-8B", Dimension: 768},
	}
	if err := index.Upsert(ctx, []domain.VectorPoint{baseline}); domain.IsDimensionMismatch(err) {
		t.Fatalf("768-dim baseline upsert rejected as dimension mismatch: %v", err)
	}

	drifted := domain.VectorPoint{
		ID:        2,
		Vector:    make([]float32, 4096),
		ModelInfo: domain.ModelInfo{Name: "qwen3-embedding-8B", Dimension: 768},
	}
	err := index.Upsert(ctx, []domain.VectorPoint{drifted})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("4096-dim upsert after 768-dim baseline = %v, want dimension mismatch", err)
	}
	if !errors.Is(err, domain.ErrDimensionMismatch) {
		t.Fatalf("error %v does not unwrap to domain.ErrDimensionMismatch", err)
	}
}
