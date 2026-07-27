//go:build pgvector_integration

// Package pgvector: integration test suite against a live PostgreSQL server
// with the pgvector extension.
//
// Build tag pgvector_integration GATES compilation: this file is excluded from
// the default `go test ./...` run. Run with:
//
//	go test -tags pgvector_integration ./internal/vector/pgvector/ -v -count=1
//
// The suite expects a PostgreSQL server with pgvector installed, reachable via
// CORTEX_PGVECTOR_DSN (default postgres://postgres:postgres@localhost:5432/postgres).
// Start one via:
//
//	docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres pgvector/pgvector:pg16
//
// Each test creates and DELETES an ISOLATED schema (unique suffix) so tests
// never collide and leave no state behind.
package pgvector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// schemaCounter ensures each test gets a unique schema name.
var schemaCounter int64

// integrationConfig resolves DSN from env and returns a config with a unique
// schema name for test isolation.
func integrationConfig(t *testing.T) AdapterConfig {
	t.Helper()
	dsn := os.Getenv("CORTEX_PGVECTOR_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres"
	}
	n := atomic.AddInt64(&schemaCounter, 1)
	return AdapterConfig{
		DSN:                dsn,
		Schema:             fmt.Sprintf("cortex_test_%d", n),
		Table:              "embeddings",
		Dimension:          4,
		ModelName:          "integration-model",
		IndexType:          "hnsw",
		MaxBatchSize:       100,
		Timeout:            15 * time.Second,
		MaxConns:           5,
		StatementTimeoutMs: 5000,
	}
}

// newIntegrationAdapter builds a real adapter via New() and registers cleanup
// to drop the isolated schema + close the pool.
func newIntegrationAdapter(t *testing.T, cfg AdapterConfig) *Adapter {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New adapter: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort schema cleanup; ignore errors (already dropped, etc.)
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_, _ = a.db.Exec(dropCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", cfg.Schema))
		_ = a.Close()
	})
	return a
}

// TestIntegration_Pgvector_RoundTrip exercises the full Upsert → Search → Delete
// lifecycle against a live pgvector server in an isolated schema.
func TestIntegration_Pgvector_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t)
	a := newIntegrationAdapter(t, cfg)
	ctx := context.Background()

	points := []domain.VectorPoint{
		{
			ID: 1, Vector: []float32{1.0, 0.0, 0.0, 0.0},
			ModelInfo: domain.ModelInfo{Name: "integration-model", Dimension: 4, Version: "v1"},
			Metadata:  map[string]any{"project": "test", "scope": "project", "tenant_id": "tenant-a"},
		},
		{
			ID: 2, Vector: []float32{0.0, 1.0, 0.0, 0.0},
			ModelInfo: domain.ModelInfo{Name: "integration-model", Dimension: 4, Version: "v1"},
			Metadata:  map[string]any{"project": "test", "scope": "project", "tenant_id": "tenant-b"},
		},
	}
	if err := a.Upsert(ctx, points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search for nearest neighbor of point 1's vector.
	results, err := a.Search(ctx, domain.VectorQuery{
		Vector: []float32{1.0, 0.0, 0.0, 0.0},
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results; expected at least 1")
	}
	if results[0].ID != 1 {
		t.Errorf("top result ID = %d, want 1 (nearest neighbor)", results[0].ID)
	}
	if results[0].Provenance != adapterID {
		t.Errorf("Provenance = %q, want %q", results[0].Provenance, adapterID)
	}
	if results[0].Score < 0.99 {
		t.Errorf("similarity = %.4f, want >= 0.99 for identical vector", results[0].Score)
	}

	// Filtered search: tenant_id=tenant-b should return only point 2.
	resultsB, err := a.Search(ctx, domain.VectorQuery{
		Vector: []float32{0.5, 0.5, 0.0, 0.0},
		Limit:  5,
		Filters: map[string]any{
			"tenant_id": "tenant-b",
		},
	})
	if err != nil {
		t.Fatalf("filtered Search: %v", err)
	}
	if len(resultsB) == 0 {
		t.Fatal("filtered Search returned no results; expected point 2")
	}
	for _, r := range resultsB {
		if r.ID != 2 {
			t.Errorf("filtered result ID = %d; PostFilter should exclude tenant-a (point 1)", r.ID)
		}
	}

	// Delete point 1 and confirm it no longer appears.
	if err := a.Delete(ctx, []int64{1}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	resultsAfterDelete, err := a.Search(ctx, domain.VectorQuery{
		Vector: []float32{1.0, 0.0, 0.0, 0.0},
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, r := range resultsAfterDelete {
		if r.ID == 1 {
			t.Error("point 1 returned after Delete; delete was not effective")
		}
	}
}

// TestIntegration_Pgvector_DimensionMismatchRejectedClientSide verifies that a
// vector whose dimension does not match the adapter's declared dimension is
// REJECTED on the client side BEFORE any DB mutation.
func TestIntegration_Pgvector_DimensionMismatchRejectedClientSide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t)
	a := newIntegrationAdapter(t, cfg)
	ctx := context.Background()

	// Seed: one valid 4-dim point.
	seed := domain.VectorPoint{
		ID:     100,
		Vector: []float32{1.0, 0.0, 0.0, 0.0},
		ModelInfo: domain.ModelInfo{
			Name:      "integration-model",
			Dimension: 4,
		},
	}
	if err := a.Upsert(ctx, []domain.VectorPoint{seed}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// Attempt: a 3-dim vector whose ModelInfo.Dimension LIES as 4.
	bad := domain.VectorPoint{
		ID:     200,
		Vector: []float32{0.5, 0.5, 0.5}, // 3-dim, but declares Dimension: 4
		ModelInfo: domain.ModelInfo{
			Name:      "integration-model",
			Dimension: 4,
		},
	}
	err := a.Upsert(ctx, []domain.VectorPoint{bad})
	if err == nil {
		t.Fatal("expected dimension-mismatch error for 3-dim vector, got nil")
	}
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("expected ErrDimensionMismatch, got: %v", err)
	}
	var dme *domain.DimensionMismatchError
	if !errors.As(err, &dme) {
		t.Fatalf("error is not a *DimensionMismatchError: %T", err)
	}
	if dme.Expected != 4 || dme.Actual != 3 {
		t.Errorf("mismatch fields: Expected=%d Actual=%d, want 4/3", dme.Expected, dme.Actual)
	}

	// Zero-mutation proof: search — ONLY the seed (id 100) should exist.
	results, err := a.Search(ctx, domain.VectorQuery{
		Vector: []float32{1.0, 0.0, 0.0, 0.0},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("search after rejected upsert: %v", err)
	}
	for _, r := range results {
		if r.ID == 200 {
			t.Error("rejected point id 200 found; client-side rejection failed")
		}
	}
	if len(results) == 0 {
		t.Error("expected the seed point to still be present, got empty search")
	}
}

// TestIntegration_Pgvector_Health probes the live server's health.
func TestIntegration_Pgvector_Health(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t)
	a := newIntegrationAdapter(t, cfg)
	ctx := context.Background()
	h := a.Health(ctx)
	if h.Status != domain.StatusHealthy {
		t.Fatalf("Health = %q (%s); want %q", h.Status, h.Message, domain.StatusHealthy)
	}
}

// TestIntegration_Pgvector_Capabilities verifies capabilities against live server.
func TestIntegration_Pgvector_Capabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t)
	a := newIntegrationAdapter(t, cfg)
	caps, err := a.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.IndexType != adapterID {
		t.Errorf("IndexType = %q, want %q", caps.IndexType, adapterID)
	}
	if caps.Filters != "PostFilter" {
		t.Errorf("Filters = %q, want PostFilter", caps.Filters)
	}
}

// TestIntegration_Pgvector_UpsertOnConflict verifies ON CONFLICT DO UPDATE
// (idempotent re-upsert updates the vector without error).
func TestIntegration_Pgvector_UpsertOnConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t)
	a := newIntegrationAdapter(t, cfg)
	ctx := context.Background()

	p1 := domain.VectorPoint{
		ID:        300,
		Vector:    []float32{1.0, 0.0, 0.0, 0.0},
		ModelInfo: domain.ModelInfo{Name: "integration-model", Dimension: 4},
		Metadata:  map[string]any{"project": "first"},
	}
	if err := a.Upsert(ctx, []domain.VectorPoint{p1}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Re-upsert same ID with different vector + metadata.
	p2 := domain.VectorPoint{
		ID:        300,
		Vector:    []float32{0.0, 1.0, 0.0, 0.0},
		ModelInfo: domain.ModelInfo{Name: "integration-model", Dimension: 4},
		Metadata:  map[string]any{"project": "second"},
	}
	if err := a.Upsert(ctx, []domain.VectorPoint{p2}); err != nil {
		t.Fatalf("re-upsert (ON CONFLICT): %v", err)
	}

	// Verify the updated vector is retrieved.
	results, err := a.Search(ctx, domain.VectorQuery{
		Vector: []float32{0.0, 1.0, 0.0, 0.0},
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 || results[0].ID != 300 {
		t.Fatalf("expected point 300 with updated vector, got %v", results)
	}
	// Verify the updated metadata is filterable.
	resultsMeta, err := a.Search(ctx, domain.VectorQuery{
		Vector:  []float32{0.0, 1.0, 0.0, 0.0},
		Limit:   5,
		Filters: map[string]any{"project": "second"},
	})
	if err != nil {
		t.Fatalf("filtered Search: %v", err)
	}
	found := false
	for _, r := range resultsMeta {
		if r.ID == 300 {
			found = true
		}
	}
	if !found {
		t.Error("point 300 not found with project=second filter; ON CONFLICT metadata update failed")
	}

	// Verify the OLD metadata is no longer present.
	resultsOld, err := a.Search(ctx, domain.VectorQuery{
		Vector:  []float32{0.0, 1.0, 0.0, 0.0},
		Limit:   5,
		Filters: map[string]any{"project": "first"},
	})
	if err != nil {
		t.Fatalf("filtered Search (old): %v", err)
	}
	for _, r := range resultsOld {
		if r.ID == 300 {
			t.Error("point 300 found with project=first; ON CONFLICT should have updated to second")
		}
	}
}
