package pgvector

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// TestUpsertChunkSQL_SingleRowMatchesColumnOrder verifies the n=1 chunk SQL
// keeps the exact per-point placeholder shape ($1..$12, $2::vector) and the
// shared ON CONFLICT body.
func TestUpsertChunkSQL_SingleRowMatchesColumnOrder(t *testing.T) {
	a := newTestAdapter(t, &fakeDB{})
	sql := a.upsertChunkSQL(1)
	if !strings.Contains(sql, "INSERT INTO cortex_test.embeddings (id, embedding, model, model_version, dimension, project, project_id, scope, tenant_id, workspace_id, source, type) VALUES ($1, $2::vector, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)") {
		t.Fatalf("single-row placeholders wrong: %s", sql)
	}
	if !strings.Contains(sql, "ON CONFLICT (id) DO UPDATE") || !strings.Contains(sql, "updated_at = NOW()") {
		t.Fatalf("conflict clause missing: %s", sql)
	}
}

// TestUpsertChunkSQL_MultiRowPlaceholders verifies row n continues at
// parameter (n-1)*12+1 so flattened pointArgs stay aligned.
func TestUpsertChunkSQL_MultiRowPlaceholders(t *testing.T) {
	a := newTestAdapter(t, &fakeDB{})
	sql := a.upsertChunkSQL(3)
	for _, marker := range []string{
		"($13, $14::vector, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)",
		"($25, $26::vector, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36)",
	} {
		if !strings.Contains(sql, marker) {
			t.Fatalf("missing row markers %q in: %s", marker, sql)
		}
	}
	if strings.Count(sql, "INSERT INTO") != 1 {
		t.Fatalf("expected exactly one INSERT statement, got: %s", sql)
	}
	if strings.Count(sql, "ON CONFLICT (id) DO UPDATE") != 1 {
		t.Fatalf("expected exactly one conflict clause, got: %s", sql)
	}
}

// TestDedupeUpsertChunk_LastWriteWins verifies duplicate ids collapse to the
// latest occurrence (per-point upsert semantics) without touching distinct ids.
func TestDedupeUpsertChunk_LastWriteWins(t *testing.T) {
	mk := func(id int64, v float32) domain.VectorPoint {
		return domain.VectorPoint{ID: id, Vector: []float32{v}, ModelInfo: domain.ModelInfo{Name: "m", Dimension: 1}}
	}
	chunk := []domain.VectorPoint{mk(1, 0.1), mk(2, 0.2), mk(1, 0.3), mk(3, 0.4), mk(2, 0.5)}
	got := dedupeUpsertChunk(chunk)
	if len(got) != 3 {
		t.Fatalf("deduped len = %d, want 3", len(got))
	}
	if got[0].ID != 1 || got[0].Vector[0] != 0.3 {
		t.Fatalf("id 1 must keep the latest write, got %#v", got[0])
	}
	if got[1].ID != 2 || got[1].Vector[0] != 0.5 {
		t.Fatalf("id 2 must keep the latest write, got %#v", got[1])
	}
	if got[2].ID != 3 || got[2].Vector[0] != 0.4 {
		t.Fatalf("id 3 untouched, got %#v", got[2])
	}
	if dedupeUpsertChunk(nil) != nil {
		t.Fatal("nil chunk must stay nil")
	}
}

// TestUpsert_ChunksMultiRowStatements verifies a batch larger than
// maxBatchSize is written as one multi-row statement per chunk with flattened
// args, preserving the statement_timeout set_config inside the tx.
func TestUpsert_ChunksMultiRowStatements(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	a.maxBatchSize = 2 // force 2-row chunks over a 5-point batch
	points := make([]domain.VectorPoint, 0, 5)
	for i := 0; i < 5; i++ {
		points = append(points, domain.VectorPoint{
			ID:        int64(i + 1),
			Vector:    []float32{0.1, 0.2, 0.3, 0.4},
			ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4},
		})
	}
	if err := a.Upsert(context.Background(), points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	var inserts []execCall
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "INSERT INTO") {
			inserts = append(inserts, c)
		}
	}
	if len(inserts) != 3 {
		t.Fatalf("expected 3 chunk statements (2+2+1), got %d", len(inserts))
	}
	if len(inserts[0].args) != 24 || len(inserts[1].args) != 24 || len(inserts[2].args) != 12 {
		t.Fatalf("chunk arg counts = %d/%d/%d, want 24/24/12",
			len(inserts[0].args), len(inserts[1].args), len(inserts[2].args))
	}
	if !strings.Contains(inserts[0].sql, "($13, $14::vector") {
		t.Fatalf("first chunk must be a 2-row statement: %s", inserts[0].sql)
	}
	if strings.Contains(inserts[2].sql, "($13,") {
		t.Fatalf("last chunk must be a 1-row statement: %s", inserts[2].sql)
	}
}

// TestUpsert_DuplicateIdsInOneStatement verifies duplicate ids inside one
// chunk are deduped (latest write wins) so the multi-row ON CONFLICT DO
// UPDATE never affects the same row twice.
func TestUpsert_DuplicateIdsInOneStatement(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	mk := func(v float32) domain.VectorPoint {
		return domain.VectorPoint{
			ID:        7,
			Vector:    []float32{v, 0.2, 0.3, 0.4},
			ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4},
		}
	}
	if err := a.Upsert(context.Background(), []domain.VectorPoint{mk(0.1), mk(0.9)}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	var inserts []execCall
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "INSERT INTO") {
			inserts = append(inserts, c)
		}
	}
	if len(inserts) != 1 {
		t.Fatalf("expected 1 deduped statement, got %d", len(inserts))
	}
	if len(inserts[0].args) != 12 {
		t.Fatalf("deduped statement args = %d, want 12", len(inserts[0].args))
	}
	if got := fmt.Sprintf("%v", inserts[0].args[1]); !strings.Contains(got, "0.9") {
		t.Fatalf("surviving vector must be the latest write (0.9), got %s", got)
	}
}
