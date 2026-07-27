// Package external: provider health aggregation (W8.4, REQ-VEC-002).
//
// This file implements the EXPLICIT health/fallback semantics for vector
// providers. The design invariant is NO SILENT FALLBACK: when an operator
// configures an external provider (qdrant, pgvector), Cortex MUST NOT silently
// substitute sqlite_blob when that provider is unhealthy. Doing so would:
//
//  1. Serve STALE results from a different index (the external index may have
//     a different vector set, different model version, or be ahead/behind the
//     local store).
//  2. Violate the no-dual-source-of-truth invariant (ADR-05): SQLite is the
//     authoritative observation store, but the EXTERNAL index is the
//     authoritative VECTOR store for the configured deployment. Switching
//     indices silently changes which vectors answer a query.
//  3. Mask a configuration or operational problem the operator needs to see.
//
// Instead, ResolveProviderHealth reports the REAL health (degraded/unhealthy)
// of the configured provider, and SelectForSearch returns the configured
// provider's index (even if unhealthy) so the caller sees the real error. The
// caller's policy (retry, alert, or operator-initiated switchover) decides
// what to do — the engine does not decide for it.
//
// This is the opposite of a "graceful degradation" that hides problems. The
// spec scenario (REQ-VEC-002 edge: "external adapter outage with fallback")
// describes an OPERATOR-APPROVED fallback, not an automatic one. Automatic
// fallback is a separate, explicit code path (ApplyApprovedFallback) that the
// caller invokes only when it has decided to switch.
package external

import (
	"context"
	"errors"
	"fmt"

	"github.com/lleontor705/cortex/internal/domain"
)

// ProviderHealth is the aggregated health surface for a configured vector
// provider. It carries the provider name, the adapter's declared IndexType,
// the current health status, and whether a fallback is in use.
type ProviderHealth struct {
	// Provider is the configured provider name ("sqlite_blob", "qdrant",
	// "pgvector", "none").
	Provider string

	// IndexType is the adapter's declared Capabilities.IndexType. May differ
	// from Provider if a fallback was applied (it should not, under the
	// no-silent-fallback invariant).
	IndexType string

	// Status is the aggregated health: healthy, degraded, or unhealthy.
	Status string

	// Message is a human-readable diagnostic.
	Message string

	// FallbackUsed reports whether a fallback index was substituted. Under
	// the no-silent-fallback invariant this is ALWAYS false for a configured
	// external provider. It is true only when ApplyApprovedFallback has been
	// explicitly invoked by caller policy.
	FallbackUsed bool
}

// ResolveProviderHealth reads the health of the configured provider's adapter
// and returns the aggregated surface. It does NOT perform any fallback — it
// reports the REAL health of the REAL configured index.
//
// For a nil index (provider=none), it reports unhealthy with a clear message.
func ResolveProviderHealth(ctx context.Context, provider string, idx domain.VectorIndex) ProviderHealth {
	if idx == nil {
		return ProviderHealth{
			Provider: provider,
			Status:   domain.StatusUnhealthy,
			Message:  fmt.Sprintf("provider %q has no index (vector search disabled)", provider),
		}
	}
	h := idx.Health(ctx)
	ph := ProviderHealth{
		Provider: provider,
		Status:   h.Status,
		Message:  h.Message,
	}
	// Best-effort: read IndexType from Capabilities for diagnostics. A
	// Capabilities error does not change the health verdict — the adapter
	// already reported its health via Health().
	if caps, err := idx.Capabilities(ctx); err == nil {
		ph.IndexType = caps.IndexType
	}
	if ph.Status == "" {
		ph.Status = domain.StatusUnhealthy
		ph.Message = fmt.Sprintf("provider %q returned empty health status (treated as unhealthy)", provider)
	}
	return ph
}

// SelectForSearch returns the VectorIndex to use for a search operation and
// its health surface. Under the NO SILENT FALLBACK invariant, it returns the
// CONFIGURED provider's index directly — even if that index is unhealthy.
//
// The optional fallback parameter is the sqlite_blob local index. It is
// IGNORED for external providers (qdrant, pgvector) regardless of health,
// because substituting it silently would serve stale results from a different
// index. For the local provider (sqlite_blob), the primary IS the local
// index and fallback is unused.
//
// Callers that want explicit, operator-approved fallback MUST call
// ApplyApprovedFallback separately — this function does not perform it.
func SelectForSearch(ctx context.Context, provider string, primary domain.VectorIndex, fallback domain.VectorIndex) (domain.VectorIndex, ProviderHealth) {
	// No silent fallback for external providers. Report the real health.
	h := ResolveProviderHealth(ctx, provider, primary)
	return primary, h
}

// ApplyApprovedFallback is the EXPLICIT fallback path. It is invoked by caller
// policy (CLI, operator command, monitoring hook) when the operator has
// decided to switch from an unhealthy external provider to the local
// sqlite_blob index. It is NEVER called automatically by the search path.
//
// Returns the fallback index and a ProviderHealth marked FallbackUsed=true.
// If the fallback is nil, returns the primary (unhealthy) index with a
// fail-closed message.
func ApplyApprovedFallback(ctx context.Context, provider string, primary domain.VectorIndex, fallback domain.VectorIndex) (domain.VectorIndex, ProviderHealth) {
	if fallback == nil {
		// No eligible fallback: fail-closed. Return the primary (unhealthy)
		// so the caller sees the real error.
		h := ResolveProviderHealth(ctx, provider, primary)
		h.Message = "approved fallback requested but no fallback index is available (fail-closed)"
		return primary, h
	}
	fbHealth := fallback.Health(ctx)
	fbCaps, _ := fallback.Capabilities(ctx)
	ph := ProviderHealth{
		Provider:     "sqlite_blob", // fallback is always sqlite_blob
		IndexType:    fbCaps.IndexType,
		Status:       fbHealth.Status,
		Message:      fmt.Sprintf("approved fallback from unhealthy %q to sqlite_blob", provider),
		FallbackUsed: true,
	}
	return fallback, ph
}

// ErrProviderUnhealthy is returned by callers that choose fail-closed when
// the configured provider reports unhealthy. It wraps the health surface so
// the caller can inspect status and message without re-querying.
type ErrProviderUnhealthy struct {
	Provider string
	Status   string
	Message  string
}

func (e *ErrProviderUnhealthy) Error() string {
	return fmt.Sprintf("external: provider %q is %s: %s", e.Provider, e.Status, e.Message)
}

// IsProviderUnhealthy reports whether err is an *ErrProviderUnhealthy.
func IsProviderUnhealthy(err error) bool {
	var e *ErrProviderUnhealthy
	return errors.As(err, &e)
}
