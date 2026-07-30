//go:build !cortex_vectors

// Package sqlite_blob: stub-mode conformance tests (W8.4).
//
// The default zero-CGO build produces a VectorStore stub that returns
// ErrVectorSearchDisabled for every operation. This file verifies the adapter
// behaves HONESTLY in that degraded state:
//
//   - Capabilities are still declared (the adapter identifies itself, declares
//     cosine/post-filter/strong/etc.) so the engine can reason about the
//     provider even when it cannot round-trip.
//   - Health reports degraded with a diagnostic message — never healthy.
//   - Upsert/Search/Delete return ErrVectorSearchDisabled — never panic, never
//     claim success.
//   - The dimension-mismatch corruption pin holds even in stub mode: a
//     mismatched vector is rejected with ErrDimensionMismatch BEFORE the
//     disabled error propagates.
//
// These tests are the EXPLICIT degraded/stub conformance: they do NOT run
// RunSuite (the stub cannot round-trip). The full suite runs under the
// cortex_vectors build tag (conformance_enabled_test.go).
package sqlite_blob

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

// TestStubConformance_CapabilitiesDeclared verifies the adapter declares a
// complete Capabilities struct even in stub mode. The engine needs these
// fields to select strategy and report provider state to callers — empty
// strings would be a programming error.
func TestStubConformance_CapabilitiesDeclared(t *testing.T) {
	a := New(nil)
	caps, err := a.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.IndexType != adapterID {
		t.Errorf("IndexType = %q, want %q", caps.IndexType, adapterID)
	}
	foundCosine := false
	for _, m := range caps.DistanceMetrics {
		if m == "cosine" {
			foundCosine = true
		}
	}
	if !foundCosine {
		t.Errorf("DistanceMetrics %v does not include cosine", caps.DistanceMetrics)
	}
	if caps.MaxDimensions <= 0 {
		t.Errorf("MaxDimensions = %d; must be > 0", caps.MaxDimensions)
	}
	if caps.Filters == "" {
		t.Error("Filters is empty; must declare PreFilter|PostFilter|none")
	}
	if caps.Hybrid == "" {
		t.Error("Hybrid is empty; must declare enabled|disabled")
	}
	if caps.Namespaces == "" {
		t.Error("Namespaces is empty; must declare supported|unsupported")
	}
	if caps.Consistency == "" {
		t.Error("Consistency is empty; must declare strong|eventual")
	}
}

// TestStubConformance_HealthDegraded verifies the stub adapter reports
// degraded health — NEVER healthy. A healthy report from a stub that cannot
// round-trip would be a correctness bug (the engine would route vector work
// to a provider that cannot execute it).
func TestStubConformance_HealthDegraded(t *testing.T) {
	a := New(nil)
	h := a.Health(context.Background())
	if h.Status != domain.StatusDegraded {
		t.Errorf("Health.Status = %q, want %q (stub cannot round-trip)", h.Status, domain.StatusDegraded)
	}
	if h.Message == "" {
		t.Error("degraded Health must carry a diagnostic message explaining how to enable vectors")
	}
}

// TestStubConformance_UpsertDisabled verifies Upsert returns
// ErrVectorSearchDisabled in stub mode — never nil (silent success would be
// corruption: the caller believes vectors were stored when they were not).
func TestStubConformance_UpsertDisabled(t *testing.T) {
	a := New(nil)
	vec := make([]float32, 128)
	point := domain.VectorPoint{
		ID:     1,
		Vector: vec,
		ModelInfo: domain.ModelInfo{
			Name:      "test-model",
			Dimension: 128,
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if err == nil {
		t.Fatal("Upsert returned nil error in stub mode; expected ErrVectorSearchDisabled")
	}
	if !errors.Is(err, domain.ErrVectorSearchDisabled) {
		t.Errorf("Upsert error = %v, want ErrVectorSearchDisabled (wrapped)", err)
	}
}

// TestStubConformance_SearchDisabled verifies Search returns
// ErrVectorSearchDisabled in stub mode — never nil results with nil error
// (that ambiguous state would make error handling unreliable).
func TestStubConformance_SearchDisabled(t *testing.T) {
	a := New(nil)
	results, err := a.Search(context.Background(), domain.VectorQuery{
		Vector: make([]float32, 128),
		Limit:  10,
	})
	if err == nil {
		t.Fatal("Search returned nil error in stub mode; expected ErrVectorSearchDisabled")
	}
	if !errors.Is(err, domain.ErrVectorSearchDisabled) {
		t.Errorf("Search error = %v, want ErrVectorSearchDisabled (wrapped)", err)
	}
	if results != nil {
		t.Errorf("Search returned %d results in disabled mode; expected nil", len(results))
	}
}

// TestStubConformance_DeleteDisabled verifies Delete returns
// ErrVectorSearchDisabled in stub mode.
func TestStubConformance_DeleteDisabled(t *testing.T) {
	a := New(nil)
	err := a.Delete(context.Background(), []int64{1, 2, 3})
	if err == nil {
		t.Fatal("Delete returned nil error in stub mode; expected ErrVectorSearchDisabled")
	}
	if !errors.Is(err, domain.ErrVectorSearchDisabled) {
		t.Errorf("Delete error = %v, want ErrVectorSearchDisabled (wrapped)", err)
	}
}

// TestStubConformance_DimensionMismatchRejectedBeforeDisabled verifies the
// REQ-VEC-001 corruption pin holds even in stub mode: a dimension-mismatched
// vector is rejected with ErrDimensionMismatch BEFORE the disabled error
// propagates. This proves the namespace check is an adapter-level guard, not
// dependent on the underlying store being available.
func TestStubConformance_DimensionMismatchRejectedBeforeDisabled(t *testing.T) {
	a := New(nil)
	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 100), // 100-dim vector
		ModelInfo: domain.ModelInfo{
			Name:      "test-model",
			Dimension: 128, // declares 128
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("expected ErrDimensionMismatch (corruption pin), got %v", err)
	}
	// The error must NOT be ErrVectorSearchDisabled — dim check fires first.
	if errors.Is(err, domain.ErrVectorSearchDisabled) {
		t.Fatal("dim-mismatch check was bypassed; got ErrVectorSearchDisabled instead")
	}
}

// TestStubConformance_IDStable verifies the adapter identifies itself even in
// stub mode — the ID is used for logging, health reporting, and capability
// negotiation.
func TestStubConformance_IDStable(t *testing.T) {
	a := New(nil)
	if a.ID() != adapterID {
		t.Errorf("ID() = %q, want %q", a.ID(), adapterID)
	}
}

// TestStubConformance_CloseSafe verifies Close is safe to call in stub mode.
func TestStubConformance_CloseSafe(t *testing.T) {
	a := New(nil)
	if err := a.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
