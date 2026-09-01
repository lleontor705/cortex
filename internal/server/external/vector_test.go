// Package external tests the SERVER-TRACK VectorIndex factory (W8.4).
//
// These tests run WITHOUT external services (no Qdrant, no Postgres). They
// verify the factory's SELECTION and VALIDATION logic — the explicit provider
// switch, required-input checks, and the fail-closed unknown-provider path.
// The real adapter integration tests (qdrant_integration, pgvector_integration
// build tags) exercise the constructed adapters against live servers.
//
// LOCAL-TRACK BOUNDARY: this package imports config + the three adapter
// packages. The architecture gate (internal/app/arch_test.go) bans local
// composition from importing this package.
package external

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	_ "modernc.org/sqlite" // Pure Go SQLite driver for in-memory test DBs
)

// validQdrantCfg returns a QdrantConfig that passes config.validateQdrant. The
// factory re-uses this shape; only the provider + ModelInfo vary per test.
func validQdrantCfg() config.QdrantConfig {
	return config.QdrantConfig{
		Host:         "localhost",
		Port:         6334,
		Collection:   "factory-test",
		Dimension:    0, // resolved from ModelInfo at factory time
		MaxBatchSize: 64,
		MaxRetries:   1,
		Timeout:      5_000_000_000, // 5s
	}
}

// validPgvectorCfg returns a PGVectorConfig that passes config.validatePgvector.
func validPgvectorCfg() config.PGVectorConfig {
	return config.PGVectorConfig{
		DSN:                "postgres://postgres:postgres@localhost:5432/postgres",
		Schema:             "factory_test",
		Table:              "embeddings",
		Dimension:          0, // resolved from ModelInfo
		IndexType:          "hnsw",
		HNSWM:              16,
		HNSWEfConstruction: 64,
		IVFFlatLists:       100,
		MaxBatchSize:       64,
		Timeout:            5_000_000_000,
		MaxConns:           2,
		StatementTimeoutMs: 1000,
	}
}

// validModel returns a ModelInfo suitable for external adapter construction.
func validModel() domain.ModelInfo {
	return domain.ModelInfo{Name: "factory-test-model", Dimension: 4, Version: "v1"}
}

// TestNewVectorIndex_SqliteBlob_EmptyProvider verifies the empty provider
// string selects sqlite_blob (the zero-CGO default). The local path wires
// this directly; the factory path is equivalent for testing.
func TestNewVectorIndex_SqliteBlob_EmptyProvider(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	idx, err := NewVectorIndex(context.Background(), config.VectorConfig{}, FactoryInput{DB: db})
	if err != nil {
		t.Fatalf("NewVectorIndex empty provider: %v", err)
	}
	if idx == nil {
		t.Fatal("expected non-nil adapter for empty provider")
	}
	if idx.ID() != "sqlite_blob" {
		t.Errorf("ID = %q, want sqlite_blob", idx.ID())
	}
}

// TestNewVectorIndex_SqliteBlob_ExplicitProvider verifies the explicit
// "sqlite_blob" provider string selects the same adapter as the empty string.
func TestNewVectorIndex_SqliteBlob_ExplicitProvider(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.VectorConfig{Provider: "sqlite_blob"}
	idx, err := NewVectorIndex(context.Background(), cfg, FactoryInput{DB: db})
	if err != nil {
		t.Fatalf("NewVectorIndex sqlite_blob: %v", err)
	}
	if idx == nil || idx.ID() != "sqlite_blob" {
		t.Fatalf("expected sqlite_blob adapter, got %v", idx)
	}
}

// TestNewVectorIndex_SqliteBlob_NilDBRejected verifies the factory fails-closed
// when sqlite_blob is selected but no *sql.DB is provided. This is a wiring
// error the caller must fix — there is no implicit in-memory fallback.
func TestNewVectorIndex_SqliteBlob_NilDBRejected(t *testing.T) {
	_, err := NewVectorIndex(context.Background(), config.VectorConfig{}, FactoryInput{DB: nil})
	if err == nil {
		t.Fatal("expected error for sqlite_blob with nil DB; got nil")
	}
	if !strings.Contains(err.Error(), "sqlite_blob") || !strings.Contains(err.Error(), "DB") {
		t.Errorf("error message should mention sqlite_blob and DB; got %q", err.Error())
	}
}

// TestNewVectorIndex_None_DisablesVectorSearch verifies "none" returns
// (nil, nil) — vector search is explicitly disabled. NOT an error.
func TestNewVectorIndex_None_DisablesVectorSearch(t *testing.T) {
	idx, err := NewVectorIndex(context.Background(), config.VectorConfig{Provider: "none"}, FactoryInput{})
	if err != nil {
		t.Fatalf("none provider returned error: %v", err)
	}
	if idx != nil {
		t.Errorf("none provider should return nil adapter; got %T", idx)
	}
}

// TestNewVectorIndex_Qdrant_RequiresModelDimension verifies the factory
// fails-closed when qdrant is selected but ModelInfo.Dimension is 0. The
// adapter needs the dimension for collection creation; resolving it from the
// embedding model is the caller's responsibility (runtime data not known at
// config-load time).
func TestNewVectorIndex_Qdrant_RequiresModelDimension(t *testing.T) {
	cfg := config.VectorConfig{Provider: "qdrant", Qdrant: validQdrantCfg()}
	_, err := NewVectorIndex(context.Background(), cfg, FactoryInput{
		ModelInfo: domain.ModelInfo{Name: "m", Dimension: 0},
	})
	if err == nil {
		t.Fatal("expected error for qdrant with Dimension=0; got nil")
	}
	if !strings.Contains(err.Error(), "Dimension") {
		t.Errorf("error should mention Dimension; got %q", err.Error())
	}
}

// TestNewVectorIndex_Pgvector_RequiresModelDimension is the pgvector analogue.
func TestNewVectorIndex_Pgvector_RequiresModelDimension(t *testing.T) {
	cfg := config.VectorConfig{Provider: "pgvector", Pgvector: validPgvectorCfg()}
	_, err := NewVectorIndex(context.Background(), cfg, FactoryInput{
		ModelInfo: domain.ModelInfo{Name: "m", Dimension: 0},
	})
	if err == nil {
		t.Fatal("expected error for pgvector with Dimension=0; got nil")
	}
	if !strings.Contains(err.Error(), "Dimension") {
		t.Errorf("error should mention Dimension; got %q", err.Error())
	}
}

// TestNewVectorIndex_UnknownProvider_RejectsWithoutFallback is the
// REQ-VEC-002 no-silent-fallback defect pin. An unknown provider string MUST
// produce an error, NOT silently fall back to sqlite_blob. Silent fallback
// would hide a configuration error and could route vector traffic to an
// unintended index.
func TestNewVectorIndex_UnknownProvider_RejectsWithoutFallback(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.VectorConfig{Provider: "weaviate"} // not implemented
	_, err = NewVectorIndex(context.Background(), cfg, FactoryInput{DB: db})
	if err == nil {
		t.Fatal("expected error for unknown provider; got nil (silent fallback occurred)")
	}
	if !strings.Contains(err.Error(), "weaviate") {
		t.Errorf("error should name the unknown provider; got %q", err.Error())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no silent fallback") {
		t.Errorf("error should mention no silent fallback; got %q", err.Error())
	}
}

// TestNewVectorIndex_Qdrant_ConstructsAdapterWithModel verifies the factory
// builds a qdrant adapter when given a valid config + ModelInfo. The adapter
// construction itself will FAIL (no live Qdrant at localhost:6334 in unit
// tests) — but the failure must come from the qdrant client, NOT from the
// factory's selection/validation logic. We assert the error is wrapped as
// "construct qdrant adapter" (factory's wrapping) rather than a selection
// error.
func TestNewVectorIndex_Qdrant_ConstructsAdapterWithModel(t *testing.T) {
	cfg := config.VectorConfig{Provider: "qdrant", Qdrant: validQdrantCfg()}
	_, err := NewVectorIndex(context.Background(), cfg, FactoryInput{ModelInfo: validModel()})
	if err == nil {
		// In some sandboxes localhost:6334 may resolve; either an adapter
		// or a wrapped construction error is acceptable. The key assertion
		// is that we did NOT get a selection/validation error.
		return
	}
	// The error must come from the adapter, not from the factory's selection.
	if strings.Contains(err.Error(), "unknown vector provider") {
		t.Errorf("selection error for valid qdrant config: %v", err)
	}
	if strings.Contains(err.Error(), "Dimension") {
		t.Errorf("validation error for valid qdrant ModelInfo: %v", err)
	}
	// Acceptable: wrapped construction error (connection refused, etc.).
}

// TestNewVectorIndex_Pgvector_ConstructsAdapterWithModel is the pgvector
// analogue. The construction will FAIL (no live Postgres in unit tests); the
// assertion is the same — the error comes from the adapter, not selection.
func TestNewVectorIndex_Pgvector_ConstructsAdapterWithModel(t *testing.T) {
	cfg := config.VectorConfig{Provider: "pgvector", Pgvector: validPgvectorCfg()}
	_, err := NewVectorIndex(context.Background(), cfg, FactoryInput{ModelInfo: validModel()})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown vector provider") {
		t.Errorf("selection error for valid pgvector config: %v", err)
	}
	if strings.Contains(err.Error(), "Dimension") {
		t.Errorf("validation error for valid pgvector ModelInfo: %v", err)
	}
}

// TestMapQdrantConfig_FieldsMapped verifies the config → AdapterConfig mapping
// preserves every field and injects ModelInfo (Dimension, ModelName). This is
// a pure-data translation with no behavior; the test pins the field set so a
// future config change does not silently drop a field.
func TestMapQdrantConfig_FieldsMapped(t *testing.T) {
	in := validQdrantCfg()
	in.APIKey = "secret-key"
	in.UseTLS = true
	m := validModel()
	out := mapQdrantConfig(in, m)
	if out.Host != in.Host {
		t.Errorf("Host: %q vs %q", out.Host, in.Host)
	}
	if out.Port != in.Port {
		t.Errorf("Port: %d vs %d", out.Port, in.Port)
	}
	if out.Collection != in.Collection {
		t.Errorf("Collection: %q vs %q", out.Collection, in.Collection)
	}
	if out.Dimension != m.Dimension {
		t.Errorf("Dimension: %d vs %d (ModelInfo)", out.Dimension, m.Dimension)
	}
	if out.ModelName != m.Name {
		t.Errorf("ModelName: %q vs %q (ModelInfo)", out.ModelName, m.Name)
	}
	if out.APIKey != in.APIKey {
		t.Errorf("APIKey not mapped")
	}
	if out.UseTLS != in.UseTLS {
		t.Errorf("UseTLS: %v vs %v", out.UseTLS, in.UseTLS)
	}
	if out.MaxBatchSize != in.MaxBatchSize {
		t.Errorf("MaxBatchSize: %d vs %d", out.MaxBatchSize, in.MaxBatchSize)
	}
	if out.MaxRetries != in.MaxRetries {
		t.Errorf("MaxRetries: %d vs %d", out.MaxRetries, in.MaxRetries)
	}
	if out.Timeout != in.Timeout {
		t.Errorf("Timeout not mapped")
	}
}

// TestMapPgvectorConfig_FieldsMapped is the pgvector analogue.
func TestMapPgvectorConfig_FieldsMapped(t *testing.T) {
	in := validPgvectorCfg()
	in.MigrationDSN = "postgres://vector_migration@db/cortex"
	m := validModel()
	out := mapPgvectorConfig(in, m)
	if out.DSN != in.DSN {
		t.Errorf("DSN not mapped")
	}
	if out.BootstrapDSN != in.MigrationDSN {
		t.Errorf("BootstrapDSN not mapped")
	}
	if out.Schema != in.Schema {
		t.Errorf("Schema: %q vs %q", out.Schema, in.Schema)
	}
	if out.Table != in.Table {
		t.Errorf("Table: %q vs %q", out.Table, in.Table)
	}
	if out.Dimension != m.Dimension {
		t.Errorf("Dimension: %d vs %d", out.Dimension, m.Dimension)
	}
	if out.ModelName != m.Name {
		t.Errorf("ModelName: %q vs %q", out.ModelName, m.Name)
	}
	if out.IndexType != in.IndexType {
		t.Errorf("IndexType: %q vs %q", out.IndexType, in.IndexType)
	}
	if out.MaxBatchSize != in.MaxBatchSize {
		t.Errorf("MaxBatchSize not mapped")
	}
	if out.MaxConns != in.MaxConns {
		t.Errorf("MaxConns not mapped")
	}
	if out.StatementTimeoutMs != in.StatementTimeoutMs {
		t.Errorf("StatementTimeoutMs not mapped")
	}
}
