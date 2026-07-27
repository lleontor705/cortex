// Package external is the SERVER-TRACK VectorIndex factory (W8.4, ADR-05,
// REQ-VEC-001/002).
//
// It is the ONLY place in the codebase that selects a concrete external vector
// adapter (qdrant, pgvector) based on config. The local composition path
// (internal/app) wires the sqlite_blob zero-CGO default DIRECTLY and MUST NOT
// import this package — the architecture gate (TestNoLocalToServerImport) bans
// it. This preserves REQ-FOUND-001: CGO_ENABLED=0 local build with zero
// external vector dependencies.
//
// Provider selection is EXPLICIT and FAIL-CLOSED:
//
//   - "" / "sqlite_blob"  → sqlite_blob adapter over the caller's *sql.DB
//   - "qdrant"            → qdrant adapter over the official gRPC client
//   - "pgvector"          → pgvector adapter over the pgx pure-Go driver
//   - "none"              → nil (vector search disabled)
//   - unknown             → error (NO silent fallback to sqlite_blob)
//
// There is NO graceful degradation to sqlite_blob when an EXTERNAL provider is
// CONFIGURED but unhealthy. Doing so would silently serve STALE results from a
// different index, violating the no-dual-source-of-truth invariant
// (REQ-VEC-002). The caller receives the unhealthy adapter (or a degraded
// health surface) and decides policy explicitly.
package external

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/vector/pgvector"
	"github.com/lleontor705/cortex/internal/vector/qdrant"
	"github.com/lleontor705/cortex/internal/vector/sqlite_blob"
)

// FactoryInput carries the runtime handles the factory needs to construct a
// concrete adapter. Not every field is used by every provider — the per-field
// doc explains when each is required.
type FactoryInput struct {
	// DB is the shared SQLite *sql.DB. REQUIRED for the sqlite_blob provider
	// (the adapter wraps the existing concrete store). Ignored by external
	// providers (qdrant, pgvector) — they hold their own connection pools.
	DB *sql.DB

	// ModelInfo is the resolved embedding model identity. REQUIRED for the
	// qdrant and pgvector providers: the adapters stamp this on every upsert
	// for namespace enforcement (model-version namespacing, REQ-VEC-001
	// dim-mismatch pin) and use Dimension for collection/index sizing. For
	// sqlite_blob it is OPTIONAL — the adapter validates dimensions against
	// VectorPoint.ModelInfo at upsert time, so the factory does not need to
	// pre-declare it.
	ModelInfo domain.ModelInfo
}

// NewVectorIndex selects and constructs the concrete domain.VectorIndex for
// the configured provider. It is the SERVER composition entry point.
//
// SELECTION IS EXPLICIT AND FAIL-CLOSED:
//
//   - cfg.Provider == "" or "sqlite_blob": the sqlite_blob adapter over
//     in.DB. The local composition wires this directly; calling the factory
//     here is allowed for symmetry/testing.
//   - cfg.Provider == "qdrant": the qdrant adapter, configured from
//     cfg.Qdrant + in.ModelInfo. The adapter owns its gRPC client.
//   - cfg.Provider == "pgvector": the pgvector adapter, configured from
//     cfg.Pgvector + in.ModelInfo. The adapter owns its pgxpool.
//   - cfg.Provider == "none": returns (nil, nil) — vector search disabled
//     by explicit operator choice. Callers gate on Health / nil-check.
//   - cfg.Provider is anything else: returns an error. There is NO silent
//     fallback to sqlite_blob — an unknown provider is a configuration error
//     the operator must fix explicitly.
//
// REQUIRED INPUT VALIDATION (fail-closed BEFORE constructing any adapter):
//
//   - sqlite_blob: in.DB MUST be non-nil.
//   - qdrant: in.ModelInfo.Dimension MUST be > 0 (the adapter needs it for
//     collection creation and namespace enforcement). The QdrantConfig is
//     already validated by config.Load, but the model dimension is runtime
//     data not known at config-load time, so it is validated here.
//   - pgvector: in.ModelInfo.Dimension MUST be > 0 (same reason).
//
// SECRET SAFETY: the factory NEVER echoes APIKey or DSN passwords in errors.
// Per-adapter redaction (qdrant.redact, pgvector.redactDSN) is the defense-in-
// depth layer; the factory adds none of its own surface.
func NewVectorIndex(ctx context.Context, cfg config.VectorConfig, in FactoryInput) (domain.VectorIndex, error) {
	switch cfg.Provider {
	case "", "sqlite_blob":
		if in.DB == nil {
			return nil, errors.New("external: sqlite_blob provider requires a non-nil *sql.DB (FactoryInput.DB)")
		}
		return sqlite_blob.New(in.DB), nil

	case "none":
		// Explicit operator opt-out. Vector search is disabled; callers
		// must nil-check and report unavailable. NOT an error.
		return nil, nil

	case "qdrant":
		if in.ModelInfo.Dimension <= 0 {
			return nil, fmt.Errorf("external: qdrant provider requires FactoryInput.ModelInfo.Dimension > 0 (got %d); resolve from the configured embedding model before constructing the adapter",
				in.ModelInfo.Dimension)
		}
		adapterCfg := mapQdrantConfig(cfg.Qdrant, in.ModelInfo)
		a, err := qdrant.New(adapterCfg)
		if err != nil {
			return nil, fmt.Errorf("external: construct qdrant adapter: %w", err)
		}
		return a, nil

	case "pgvector":
		if in.ModelInfo.Dimension <= 0 {
			return nil, fmt.Errorf("external: pgvector provider requires FactoryInput.ModelInfo.Dimension > 0 (got %d); resolve from the configured embedding model before constructing the adapter",
				in.ModelInfo.Dimension)
		}
		adapterCfg := mapPgvectorConfig(cfg.Pgvector, in.ModelInfo)
		a, err := pgvector.New(ctx, adapterCfg)
		if err != nil {
			return nil, fmt.Errorf("external: construct pgvector adapter: %w", err)
		}
		return a, nil

	default:
		return nil, fmt.Errorf(
			"external: unknown vector provider %q (valid: \"\", sqlite_blob, qdrant, pgvector, none); no silent fallback is performed — correct the configuration explicitly",
			cfg.Provider,
		)
	}
}

// mapQdrantConfig translates the data-only config.QdrantConfig into the
// qdrant.AdapterConfig the adapter package expects, injecting the resolved
// ModelInfo for namespace enforcement. This is the SINGLE mapping surface —
// the adapter package stays self-contained (no config import) and config.go
// stays local-track (no qdrant client import).
func mapQdrantConfig(c config.QdrantConfig, model domain.ModelInfo) qdrant.AdapterConfig {
	return qdrant.AdapterConfig{
		Host:         c.Host,
		Port:         c.Port,
		Collection:   c.Collection,
		Dimension:    model.Dimension,
		ModelName:    model.Name,
		APIKey:       c.APIKey,
		UseTLS:       c.UseTLS,
		MaxBatchSize: c.MaxBatchSize,
		MaxRetries:   c.MaxRetries,
		Timeout:      c.Timeout,
	}
}

// mapPgvectorConfig translates config.PGVectorConfig into pgvector.AdapterConfig.
// See mapQdrantConfig for the mapping rationale.
func mapPgvectorConfig(c config.PGVectorConfig, model domain.ModelInfo) pgvector.AdapterConfig {
	return pgvector.AdapterConfig{
		DSN:                c.DSN,
		Schema:             c.Schema,
		Table:              c.Table,
		Dimension:          model.Dimension,
		ModelName:          model.Name,
		IndexType:          c.IndexType,
		HNSWM:              c.HNSWM,
		HNSWEfConstruction: c.HNSWEfConstruction,
		IVFFlatLists:       c.IVFFlatLists,
		MaxBatchSize:       c.MaxBatchSize,
		Timeout:            c.Timeout,
		MaxConns:           c.MaxConns,
		StatementTimeoutMs: c.StatementTimeoutMs,
	}
}
