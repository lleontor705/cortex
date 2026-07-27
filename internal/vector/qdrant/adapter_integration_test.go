//go:build qdrant_integration

// Package qdrant: integration test suite against a live Qdrant server.
//
// Build tag qdrant_integration GATES compilation: this file is excluded from
// the default `go test ./...` run. Run with:
//
//	go test -tags qdrant_integration ./internal/vector/qdrant/ -v -count=1
//
// The suite expects a Qdrant server reachable at CORTEX_QDRANT_HOST:CORTEX_QDRANT_PORT
// (defaults localhost:6334). Start one via:
//
//	docker run -d -p 6333:6333 -p 6334:6334 qdrant/qdrant:v1.18.3
//
// Each test creates and DELETES an ISOLATED collection (unique suffix) so tests
// never collide and leave no state behind.
package qdrant

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// integrationConfig resolves host/port from env (defaults localhost:6334).
// Both CORTEX_QDRANT_HOST and CORTEX_QDRANT_PORT are honored so CI can point
// the suite at an arbitrary Qdrant instance.
func integrationConfig(t *testing.T, suffix string) AdapterConfig {
	t.Helper()
	host := os.Getenv("CORTEX_QDRANT_HOST")
	if host == "" {
		host = "localhost"
	}
	port := 6334
	if p := os.Getenv("CORTEX_QDRANT_PORT"); p != "" {
		parsed, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("CORTEX_QDRANT_PORT %q is not a valid integer: %v", p, err)
		}
		if parsed < 1 || parsed > 65535 {
			t.Fatalf("CORTEX_QDRANT_PORT %d is out of range (1-65535)", parsed)
		}
		port = parsed
	}
	return AdapterConfig{
		Host:         host,
		Port:         port,
		Collection:   "cortex_integration_" + suffix,
		Dimension:    4,
		ModelName:    "integration-model",
		MaxBatchSize: 100,
		MaxRetries:   3,
		Timeout:      15 * time.Second,
	}
}

// newIntegrationAdapter builds a real adapter and registers cleanup to delete
// the isolated collection + close the client.
func newIntegrationAdapter(t *testing.T, cfg AdapterConfig) *Adapter {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New adapter: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort collection cleanup; ignore errors (already deleted, etc.)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.client.DeleteCollection(ctx, cfg.Collection)
		_ = a.Close()
	})
	return a
}

// TestIntegration_Qdrant_RoundTrip exercises the full Upsert → Search → Delete
// lifecycle against a live Qdrant server in an isolated collection.
func TestIntegration_Qdrant_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t, "roundtrip")
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

	// Search for the nearest neighbor of point 1's vector.
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
			t.Errorf("filtered result ID = %d; PreFilter should exclude tenant-a (point 1)", r.ID)
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

// TestIntegration_Qdrant_DimensionMismatchRejectedClientSide verifies that a
// vector whose dimension does not match the adapter's declared dimension is
// REJECTED on the client side BEFORE any server mutation (no point reaches
// Qdrant). This is the REQ-VEC-001 dim-mismatch corruption pin exercised
// against a live server: the collection is seeded with one valid point, then a
// wrong-dimension upsert is attempted and must fail without adding any points.
//
// The earlier (misleading) version of this test claimed to assert server-side
// rejection but could not actually reach the server — the client-side check
// catches the mismatch first. This rewrite is honest: it asserts the
// client-side rejection AND proves zero server mutation (only the seed point
// survives in the collection after the rejected attempt).
func TestIntegration_Qdrant_DimensionMismatchRejectedClientSide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t, "dimreject")
	a := newIntegrationAdapter(t, cfg)
	ctx := context.Background()

	// Seed: one valid 4-dim point establishes the collection.
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

	// Attempt: a 3-dim vector whose ModelInfo.Dimension LIES as 4. The adapter's
	// validatePoint checks len(Vector)==dim, so this is rejected client-side.
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

	// Zero-mutation proof: search the collection — ONLY the seed (id 100)
	// should exist. The rejected point (id 200) must never have reached the
	// server.
	results, err := a.Search(ctx, domain.VectorQuery{
		Vector: []float32{1.0, 0.0, 0.0, 0.0},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("search after rejected upsert: %v", err)
	}
	for _, r := range results {
		if r.ID == 200 {
			t.Error("rejected point id 200 found in collection; client-side rejection failed to prevent server mutation")
		}
	}
	if len(results) == 0 {
		t.Error("expected the seed point to still be present, got empty search")
	}
}

// TestIntegration_Qdrant_Health probes the live server's Health endpoint.
func TestIntegration_Qdrant_Health(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t, "health")
	a := newIntegrationAdapter(t, cfg)
	ctx := context.Background()
	h := a.Health(ctx)
	if h.Status != domain.StatusHealthy {
		t.Fatalf("Health = %q (%s); want %q", h.Status, h.Message, domain.StatusHealthy)
	}
}

// TestIntegration_Qdrant_Capabilities verifies the adapter reports the expected
// capabilities when connected to a live server.
func TestIntegration_Qdrant_Capabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	cfg := integrationConfig(t, "caps")
	a := newIntegrationAdapter(t, cfg)
	caps, err := a.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.IndexType != adapterID {
		t.Errorf("IndexType = %q, want %q", caps.IndexType, adapterID)
	}
	if caps.Filters != "PreFilter" {
		t.Errorf("Filters = %q, want PreFilter", caps.Filters)
	}
}
