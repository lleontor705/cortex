// Package external: health aggregation tests (W8.4, REQ-VEC-002).
//
// These tests verify the health/fallback semantics:
//   - A healthy external provider reports healthy.
//   - An unhealthy CONFIGURED external provider reports degraded/unhealthy —
//     it does NOT silently fall back to sqlite_blob (no-dual-source-of-truth).
//   - The caller receives the health surface and decides policy explicitly.
package external

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// healthProbeIndex is a VectorIndex with controllable Health.
type healthProbeIndex struct {
	id     string
	health domain.Health
	caps   domain.Capabilities
}

func (h *healthProbeIndex) ID() string                                             { return h.id }
func (h *healthProbeIndex) Upsert(_ context.Context, _ []domain.VectorPoint) error { return nil }
func (h *healthProbeIndex) Search(_ context.Context, _ domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (h *healthProbeIndex) Delete(_ context.Context, _ []int64) error { return nil }
func (h *healthProbeIndex) Close() error                              { return nil }
func (h *healthProbeIndex) Health(_ context.Context) domain.Health    { return h.health }
func (h *healthProbeIndex) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return h.caps, nil
}

// TestResolveProviderHealth_Healthy verifies a healthy configured external
// provider reports StatusHealthy.
func TestResolveProviderHealth_Healthy(t *testing.T) {
	idx := &healthProbeIndex{
		id:     "qdrant",
		health: domain.Health{Status: domain.StatusHealthy, Message: "ready"},
	}
	h := ResolveProviderHealth(context.Background(), "qdrant", idx)
	if h.Status != domain.StatusHealthy {
		t.Errorf("Status = %q, want healthy", h.Status)
	}
	if h.Provider != "qdrant" {
		t.Errorf("Provider = %q, want qdrant", h.Provider)
	}
}

// TestResolveProviderHealth_UnhealthyConfigured_NoSilentFallback is the
// REQ-VEC-002 no-silent-fallback defect pin. When a CONFIGURED external
// provider is unhealthy, ResolveProviderHealth reports degraded/unhealthy —
// it does NOT report healthy by silently switching to sqlite_blob. The caller
// sees the real health and decides policy explicitly (retry, alert, or
// operator-initiated switchover).
func TestResolveProviderHealth_UnhealthyConfigured_NoSilentFallback(t *testing.T) {
	idx := &healthProbeIndex{
		id:     "qdrant",
		health: domain.Health{Status: domain.StatusUnhealthy, Message: "connection refused"},
	}
	h := ResolveProviderHealth(context.Background(), "qdrant", idx)
	if h.Status == domain.StatusHealthy {
		t.Error("CONFIGURED unhealthy provider reported healthy — silent fallback occurred (REQ-VEC-002 violation)")
	}
	if h.Provider != "qdrant" {
		t.Errorf("Provider = %q, want qdrant (the configured provider, not a fallback)", h.Provider)
	}
	if h.FallbackUsed {
		t.Error("FallbackUsed = true; configured external provider must NOT trigger fallback")
	}
}

// TestResolveProviderHealth_DegradedPropagates verifies a degraded adapter
// (e.g. sqlite_blob stub mode) reports degraded, not healthy.
func TestResolveProviderHealth_DegradedPropagates(t *testing.T) {
	idx := &healthProbeIndex{
		id:     "sqlite_blob",
		health: domain.Health{Status: domain.StatusDegraded, Message: "vectors disabled"},
	}
	h := ResolveProviderHealth(context.Background(), "sqlite_blob", idx)
	if h.Status != domain.StatusDegraded {
		t.Errorf("Status = %q, want degraded", h.Status)
	}
}

// TestResolveProviderHealth_NilIndex verifies a nil index (provider=none)
// reports unhealthy with a clear message.
func TestResolveProviderHealth_NilIndex(t *testing.T) {
	h := ResolveProviderHealth(context.Background(), "none", nil)
	if h.Status != domain.StatusUnhealthy {
		t.Errorf("nil index Status = %q, want unhealthy", h.Status)
	}
	if h.Provider != "none" {
		t.Errorf("Provider = %q, want none", h.Provider)
	}
}

// TestResolveProviderHealth_ReportsProviderAndIndexType verifies the health
// surface carries BOTH the configured provider name and the adapter's declared
// IndexType, so operators can diagnose provider/adapter mismatches.
func TestResolveProviderHealth_ReportsProviderAndIndexType(t *testing.T) {
	idx := &healthProbeIndex{
		id:     "pgvector",
		health: domain.Health{Status: domain.StatusHealthy},
		caps:   domain.Capabilities{IndexType: "pgvector"},
	}
	h := ResolveProviderHealth(context.Background(), "pgvector", idx)
	if h.Provider != "pgvector" {
		t.Errorf("Provider = %q, want pgvector", h.Provider)
	}
	if h.IndexType != "pgvector" {
		t.Errorf("IndexType = %q, want pgvector", h.IndexType)
	}
}

// TestSelectForSearch_NoFallbackWhenExternalConfigured verifies that when an
// external provider is configured, SelectForSearch does NOT substitute
// sqlite_blob even when the external index is unhealthy. The caller receives
// the unhealthy index and must handle the error explicitly (fail-closed).
// Silent substitution would serve stale results from a different index.
func TestSelectForSearch_NoFallbackWhenExternalConfigured(t *testing.T) {
	external := &healthProbeIndex{
		id:     "qdrant",
		health: domain.Health{Status: domain.StatusUnhealthy, Message: "down"},
	}
	fallback := &healthProbeIndex{
		id:     "sqlite_blob",
		health: domain.Health{Status: domain.StatusHealthy, Message: "ready"},
	}
	selected, health := SelectForSearch(context.Background(), "qdrant", external, fallback)
	// The EXTERNAL index must be returned, NOT the fallback.
	if selected != external {
		t.Error("SelectForSearch substituted the fallback for an unhealthy configured external provider — silent fallback is prohibited")
	}
	if health.Status == domain.StatusHealthy {
		t.Error("health reported healthy despite configured provider being unhealthy")
	}
	if health.FallbackUsed {
		t.Error("FallbackUsed = true; configured external provider must not use fallback")
	}
}

// TestSelectForSearch_LocalUsesLocal verifies that when provider is
// sqlite_blob (local mode), SelectForSearch returns the local index directly
// with no fallback semantics.
func TestSelectForSearch_LocalUsesLocal(t *testing.T) {
	local := &healthProbeIndex{
		id:     "sqlite_blob",
		health: domain.Health{Status: domain.StatusHealthy},
	}
	selected, health := SelectForSearch(context.Background(), "sqlite_blob", local, nil)
	if selected != local {
		t.Error("local provider should return the local index directly")
	}
	if health.Status != domain.StatusHealthy {
		t.Errorf("local health = %q, want healthy", health.Status)
	}
}
