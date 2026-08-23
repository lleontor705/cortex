//go:build !pgvector_integration

// Package pgvector is the W8.3 conformance + architecture unit test suite for
// the pgvector external VectorIndex adapter.
//
// These tests run WITHOUT a PostgreSQL server (build tag !pgvector_integration).
// They exercise the adapter through a narrow DB interface and a fake, proving:
//   - The adapter implements domain.VectorIndex (compile-time + runtime).
//   - Capabilities declares index type, distance metrics, max dimensions,
//     filter support (PostFilter), hybrid (engine), namespaces, consistency,
//     and batch.
//   - Dimension-mismatch vectors are REJECTED fail-closed before any DB call
//     (REQ-VEC-001 dim-mismatch corruption pin).
//   - Model-namespace mismatch vectors are REJECTED fail-closed.
//   - Upsert generates parameterized INSERT ... ON CONFLICT SQL with correct
//     metadata columns (project/scope/tenant_id/model/model_version/source/type).
//   - Upsert batches large inputs at MaxBatchSize within a transaction.
//   - Upsert sets statement_timeout within the transaction.
//   - Search generates parameterized SELECT with cosine distance and dynamic
//     WHERE clauses, returning VectorCandidate results with provenance.
//   - Search applies the score threshold client-side.
//   - Delete generates parameterized DELETE with ANY($1).
//   - Health translates Ping result; Close delegates to the pool.
//   - No DSN password ever appears in an error message (no plaintext leak).
//   - Safe identifier validation rejects injection attempts.
//
// The integration suite (Docker pgvector) lives in adapter_integration_test.go
// behind the pgvector_integration build tag.
package pgvector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// --- fake DB (implements pgvectorDB) ----------------------------------------

type fakeDB struct {
	// canned responses
	pingErr   error
	queryErr  error
	execErr   error
	beginErr  error
	queryRows pgx.Rows // set by tests via newFakeRows

	// recorded calls
	execCalls  []execCall
	queryCalls []queryCall
	beginCalls int
	closeCalls int
}

type execCall struct {
	sql  string
	args []any
}

type queryCall struct {
	sql  string
	args []any
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execCalls = append(f.execCalls, execCall{sql: sql, args: args})
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.queryCalls = append(f.queryCalls, queryCall{sql: sql, args: args})
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryRows != nil {
		return f.queryRows, nil
	}
	return newFakeRows(nil), nil
}

func (f *fakeDB) BeginTx(_ context.Context) (pgvectorTx, error) {
	f.beginCalls++
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return &fakeTx{db: f}, nil
}

func (f *fakeDB) Ping(_ context.Context) error {
	return f.pingErr
}

func (f *fakeDB) Close() {
	f.closeCalls++
}

// --- fake Tx (implements pgvectorTx) ----------------------------------------

type fakeTx struct {
	db        *fakeDB
	committed bool
	rolled    bool
}

func (t *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.db.Exec(ctx, sql, args...)
}

func (t *fakeTx) Commit(_ context.Context) error {
	t.committed = true
	return nil
}

func (t *fakeTx) Rollback(_ context.Context) error {
	t.rolled = true
	return nil
}

// --- fake Rows (implements pgx.Rows) ----------------------------------------

// baseRows provides default no-op implementations for the pgx.Rows methods the
// adapter does not use. Embedding it reduces fake boilerplate.
type baseRows struct{}

func (baseRows) Close()                                       {}
func (baseRows) Err() error                                   { return nil }
func (baseRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (baseRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (baseRows) Values() ([]any, error)                       { return nil, nil }
func (baseRows) RawValues() [][]byte                          { return nil }
func (baseRows) Conn() *pgx.Conn                              { return nil }

// fakeRows implements pgx.Rows with canned data. Each row is a []any slice
// matching the column order of the search query: [id int64, similarity float64].
type fakeRows struct {
	baseRows
	data [][]any
	idx  int
	err  error
}

func newFakeRows(data [][]any) *fakeRows {
	return &fakeRows{data: data}
}

func (r *fakeRows) Next() bool {
	if r.err != nil {
		return false
	}
	r.idx++
	return r.idx <= len(r.data)
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx < 1 || r.idx > len(r.data) {
		return fmt.Errorf("fakeRows: scan out of range (idx=%d, len=%d)", r.idx, len(r.data))
	}
	row := r.data[r.idx-1]
	for i := range dest {
		if i >= len(row) {
			break
		}
		switch dst := dest[i].(type) {
		case *int64:
			if v, ok := row[i].(int64); ok {
				*dst = v
			}
		case *float64:
			if v, ok := row[i].(float64); ok {
				*dst = v
			}
		case *string:
			if v, ok := row[i].(string); ok {
				*dst = v
			}
		case *any:
			*dst = row[i]
		default:
			return fmt.Errorf("fakeRows: unsupported scan target type %T at index %d", dest[i], i)
		}
	}
	return nil
}

// --- test helpers -----------------------------------------------------------

// newTestAdapter builds an Adapter wired to a fake for unit tests. The schema
// is marked as pre-existing so no DDL is attempted.
func newTestAdapter(t *testing.T, db *fakeDB) *Adapter {
	t.Helper()
	a, err := NewWithDB(db, AdapterConfig{
		DSN:       "postgres://test:test@localhost:5432/test",
		Schema:    "cortex_test",
		Table:     "embeddings",
		Dimension: 4,
		ModelName: "test-model",
	})
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	a.created = true
	return a
}

// validPoints returns a batch of 2 valid 4-dim points with full metadata.
func validPoints() []domain.VectorPoint {
	return []domain.VectorPoint{
		{
			ID:     1,
			Vector: []float32{0.1, 0.2, 0.3, 0.4},
			ModelInfo: domain.ModelInfo{
				Name:      "test-model",
				Dimension: 4,
				Version:   "v1",
			},
			Metadata: map[string]any{
				"project":   "myproj",
				"scope":     "project",
				"tenant_id": "tenant-a",
				"source":    "manual",
				"type":      "decision",
			},
		},
		{
			ID:     2,
			Vector: []float32{0.5, 0.6, 0.7, 0.8},
			ModelInfo: domain.ModelInfo{
				Name:      "test-model",
				Dimension: 4,
				Version:   "v1",
			},
			Metadata: map[string]any{"project": "other"},
		},
	}
}

// --- conformance tests ------------------------------------------------------

// TestAdapter_ImplementsVectorIndex is the compile-time + runtime conformance
// assertion: the adapter MUST satisfy the domain.VectorIndex port.
func TestAdapter_ImplementsVectorIndex(t *testing.T) {
	var _ domain.VectorIndex = (*Adapter)(nil)
	a, err := NewWithDB(&fakeDB{}, AdapterConfig{
		DSN:       "postgres://localhost/db",
		Dimension: 4,
	})
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	var _ domain.VectorIndex = a
}

// TestAdapter_ID_DeclaresPgvector verifies the adapter identifies itself.
func TestAdapter_ID_DeclaresPgvector(t *testing.T) {
	a := newTestAdapter(t, &fakeDB{})
	if a.ID() != adapterID {
		t.Errorf("ID() = %q, want %q", a.ID(), adapterID)
	}
}

// TestAdapter_Capabilities_DeclaresFullSet verifies every Capabilities field
// mandated by REQ-VEC-001 / ADR-05.
func TestAdapter_Capabilities_DeclaresFullSet(t *testing.T) {
	a := newTestAdapter(t, &fakeDB{})
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
	if caps.MaxDimensions < 1 {
		t.Errorf("MaxDimensions = %d, want >= 1", caps.MaxDimensions)
	}
	if caps.Filters != "PostFilter" {
		t.Errorf("Filters = %q, want PostFilter", caps.Filters)
	}
	if caps.Hybrid != "engine" {
		t.Errorf("Hybrid = %q, want engine (dense-only, engine owns fusion)", caps.Hybrid)
	}
	if caps.Namespaces != "supported" {
		t.Errorf("Namespaces = %q, want supported", caps.Namespaces)
	}
	if caps.Consistency == "" {
		t.Error("Consistency is empty")
	}
	if !caps.BatchUpsert {
		t.Error("BatchUpsert = false; pgvector supports batched upsert")
	}
	if caps.MaxBatchSize <= 0 {
		t.Errorf("MaxBatchSize = %d, want > 0", caps.MaxBatchSize)
	}
}

// TestAdapter_Capabilities_MaxBatchSizeReflectsConfig is the regression test
// for stale hardcoded batch size.
func TestAdapter_Capabilities_MaxBatchSizeReflectsConfig(t *testing.T) {
	a, err := NewWithDB(&fakeDB{}, AdapterConfig{
		DSN:          "postgres://localhost/db",
		Dimension:    4,
		MaxBatchSize: 100,
	})
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	caps, _ := a.Capabilities(context.Background())
	if caps.MaxBatchSize != 100 {
		t.Errorf("MaxBatchSize = %d, want 100 (configured value)", caps.MaxBatchSize)
	}
	if a.maxBatchSize != 100 {
		t.Errorf("adapter.maxBatchSize = %d, want 100", a.maxBatchSize)
	}

	// Default (<=0) normalizes to 256.
	aDefault, _ := NewWithDB(&fakeDB{}, AdapterConfig{
		DSN:       "postgres://localhost/db",
		Dimension: 4,
	})
	capsDefault, _ := aDefault.Capabilities(context.Background())
	if capsDefault.MaxBatchSize != 256 {
		t.Errorf("default MaxBatchSize = %d, want 256", capsDefault.MaxBatchSize)
	}
}

// TestAdapter_Upsert_DimensionMismatchRejected is the REQ-VEC-001 defect pin:
// mismatched vectors MUST be rejected BEFORE any DB call.
func TestAdapter_Upsert_DimensionMismatchRejected(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)

	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 3), // 3-dim, but ModelInfo says 4
		ModelInfo: domain.ModelInfo{
			Name:      "test-model",
			Dimension: 4,
			Version:   "v1",
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("expected ErrDimensionMismatch, got %v", err)
	}
	if db.beginCalls != 0 {
		t.Errorf("dim-mismatch MUST NOT begin a tx; got %d begin calls", db.beginCalls)
	}
	var dme *domain.DimensionMismatchError
	if !errors.As(err, &dme) {
		t.Fatalf("error is not a *DimensionMismatchError: %T", err)
	}
	if dme.Expected != 4 || dme.Actual != 3 {
		t.Errorf("mismatch fields: Expected=%d Actual=%d, want 4/3", dme.Expected, dme.Actual)
	}
}

// TestAdapter_Upsert_CollectionDimensionMismatchRejected verifies dimension
// mismatch when ModelInfo.Dimension is zero (adapter falls back to collection dim).
func TestAdapter_Upsert_CollectionDimensionMismatchRejected(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db) // dim = 4

	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 8), // 8-dim, adapter expects 4
		ModelInfo: domain.ModelInfo{
			Name: "test-model",
			// Dimension zero: adapter falls back to collection dim (4)
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("expected ErrDimensionMismatch (collection dim 4 vs vector 8), got %v", err)
	}
	if db.beginCalls != 0 {
		t.Errorf("dim-mismatch MUST NOT begin a tx; got %d begin calls", db.beginCalls)
	}
}

// TestAdapter_Upsert_ModelNamespaceMismatchRejected verifies model-namespace
// mismatch is fail-closed.
func TestAdapter_Upsert_ModelNamespaceMismatchRejected(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db) // model = "test-model", dim = 4

	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 4), // correct dim
		ModelInfo: domain.ModelInfo{
			Name:      "different-model", // wrong model
			Dimension: 4,
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !errors.Is(err, domain.ErrNamespaceMismatch) {
		t.Fatalf("expected ErrNamespaceMismatch, got %v", err)
	}
	if db.beginCalls != 0 {
		t.Errorf("namespace mismatch MUST NOT begin a tx; got %d begin calls", db.beginCalls)
	}
}

// TestAdapter_Upsert_GeneratesParameterizedSQL verifies the adapter generates
// INSERT ... ON CONFLICT with parameterized values for all metadata columns.
func TestAdapter_Upsert_GeneratesParameterizedSQL(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)

	if err := a.Upsert(context.Background(), validPoints()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Find the upsert exec call (skip statement_timeout set_config).
	var upsertExecs []execCall
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "INSERT INTO") {
			upsertExecs = append(upsertExecs, c)
		}
	}
	if len(upsertExecs) != 2 {
		t.Fatalf("expected 2 INSERT calls, got %d", len(upsertExecs))
	}

	// Verify the SQL contains ON CONFLICT (parameterized upsert).
	if !strings.Contains(upsertExecs[0].sql, "ON CONFLICT (id) DO UPDATE") {
		t.Errorf("SQL should contain ON CONFLICT: %s", upsertExecs[0].sql)
	}
	// Verify the SQL uses parameterized values ($1, $2::vector, etc.).
	if !strings.Contains(upsertExecs[0].sql, "$2::vector") {
		t.Errorf("SQL should use $2::vector for embedding: %s", upsertExecs[0].sql)
	}
	// Verify qualified table name.
	if !strings.Contains(upsertExecs[0].sql, "cortex_test.embeddings") {
		t.Errorf("SQL should use qualified table name: %s", upsertExecs[0].sql)
	}

	// Verify first point's args: [id, vector, model, model_version, dimension, project, scope, tenant_id, source, type]
	args := upsertExecs[0].args
	if len(args) != 10 {
		t.Fatalf("expected 10 args, got %d", len(args))
	}
	if args[0] != int64(1) {
		t.Errorf("arg[0] (id) = %v, want 1", args[0])
	}
	// arg[1] is pgvector.Vector (check via fmt)
	if !strings.Contains(fmt.Sprintf("%v", args[1]), "0.1") {
		t.Errorf("arg[1] (vector) does not contain expected values: %v", args[1])
	}
	if args[2] != "test-model" {
		t.Errorf("arg[2] (model) = %v, want test-model", args[2])
	}
	if args[4] != 4 {
		t.Errorf("arg[4] (dimension) = %v, want 4", args[4])
	}
	if args[5] != "myproj" {
		t.Errorf("arg[5] (project) = %v, want myproj", args[5])
	}
	if args[7] != "tenant-a" {
		t.Errorf("arg[7] (tenant_id) = %v, want tenant-a", args[7])
	}
	if args[9] != "decision" {
		t.Errorf("arg[9] (type) = %v, want decision", args[9])
	}
}

// TestAdapter_Upsert_UsesTransaction verifies the adapter opens a transaction
// for batch upsert and commits it.
func TestAdapter_Upsert_UsesTransaction(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)

	if err := a.Upsert(context.Background(), validPoints()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if db.beginCalls != 1 {
		t.Errorf("expected 1 BeginTx call, got %d", db.beginCalls)
	}
	// Verify statement_timeout was set within the tx.
	foundSetTimeout := false
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "set_config('statement_timeout'") {
			foundSetTimeout = true
			if len(c.args) < 1 {
				t.Errorf("statement_timeout call has no args")
			}
			// The arg should contain "ms" suffix.
			timeoutStr, ok := c.args[0].(string)
			if !ok || !strings.HasSuffix(timeoutStr, "ms") {
				t.Errorf("statement_timeout arg = %v, want string ending with 'ms'", c.args[0])
			}
		}
	}
	if !foundSetTimeout {
		t.Error("statement_timeout not set within transaction")
	}
}

// TestAdapter_Upsert_EmptyBatchIsNoop verifies an empty batch is a no-op.
func TestAdapter_Upsert_EmptyBatchIsNoop(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	if err := a.Upsert(context.Background(), nil); err != nil {
		t.Fatalf("Upsert(nil): %v", err)
	}
	if db.beginCalls != 0 {
		t.Errorf("empty batch should not begin a tx; got %d begin calls", db.beginCalls)
	}
}

// TestAdapter_Search_TranslatesFilters verifies the adapter generates a
// parameterized SELECT with WHERE clauses for recognized filter keys.
func TestAdapter_Search_TranslatesFilters(t *testing.T) {
	db := &fakeDB{
		queryRows: newFakeRows([][]any{
			{int64(1), 0.95},
			{int64(2), 0.80},
		}),
	}
	a := newTestAdapter(t, db)

	q := domain.VectorQuery{
		Vector:    []float32{0.1, 0.2, 0.3, 0.4},
		Limit:     10,
		Threshold: 0.5,
		Filters: map[string]any{
			"project":   "myproj",
			"scope":     "project",
			"tenant_id": "tenant-a",
			"type":      "decision",
		},
	}
	results, err := a.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify the SQL structure.
	if len(db.queryCalls) != 1 {
		t.Fatalf("expected 1 query call, got %d", len(db.queryCalls))
	}
	qc := db.queryCalls[0]
	if !strings.Contains(qc.sql, "<=>") {
		t.Errorf("SQL should contain cosine distance operator <=>: %s", qc.sql)
	}
	if !strings.Contains(qc.sql, "ORDER BY") {
		t.Errorf("SQL should contain ORDER BY: %s", qc.sql)
	}
	if !strings.Contains(qc.sql, "LIMIT") {
		t.Errorf("SQL should contain LIMIT: %s", qc.sql)
	}
	// Verify WHERE clauses for all 4 filter keys.
	for _, key := range []string{"project", "scope", "tenant_id", "type"} {
		if !strings.Contains(qc.sql, key) {
			t.Errorf("SQL should contain filter column %q: %s", key, qc.sql)
		}
	}
	// Verify all filter values appear in args.
	argStr := fmt.Sprintf("%v", qc.args)
	for _, val := range []string{"myproj", "project", "tenant-a", "decision"} {
		if !strings.Contains(argStr, val) {
			t.Errorf("filter value %q not found in args: %v", val, qc.args)
		}
	}

	// Provenance is set on every candidate.
	for _, r := range results {
		if r.Provenance != adapterID {
			t.Errorf("Provenance = %q, want %q", r.Provenance, adapterID)
		}
	}
}

// TestAdapter_Search_NoFilters verifies search works without any filters.
func TestAdapter_Search_NoFilters(t *testing.T) {
	db := &fakeDB{
		queryRows: newFakeRows([][]any{
			{int64(1), 0.9},
		}),
	}
	a := newTestAdapter(t, db)
	results, err := a.Search(context.Background(), domain.VectorQuery{
		Vector: []float32{0.1, 0.2, 0.3, 0.4},
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// SQL should NOT contain WHERE (no filters).
	qc := db.queryCalls[0]
	if strings.Contains(qc.sql, "WHERE") {
		t.Errorf("SQL should NOT contain WHERE when no filters: %s", qc.sql)
	}
}

// TestAdapter_Search_ThresholdApplied verifies threshold is applied client-side.
func TestAdapter_Search_ThresholdApplied(t *testing.T) {
	db := &fakeDB{
		queryRows: newFakeRows([][]any{
			{int64(1), 0.9},
			{int64(2), 0.4}, // below threshold
		}),
	}
	a := newTestAdapter(t, db)
	results, err := a.Search(context.Background(), domain.VectorQuery{
		Vector:    []float32{0.1, 0.2, 0.3, 0.4},
		Limit:     10,
		Threshold: 0.5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after threshold, got %d", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("result ID = %d, want 1", results[0].ID)
	}
}

// TestAdapter_Search_EmptyVectorRejected verifies empty search vector is rejected.
func TestAdapter_Search_EmptyVectorRejected(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	_, err := a.Search(context.Background(), domain.VectorQuery{
		Vector: []float32{},
		Limit:  5,
	})
	if err == nil {
		t.Fatal("expected error for empty search vector, got nil")
	}
}

// TestAdapter_Search_DimensionMismatch verifies search rejects wrong-dimension vectors.
func TestAdapter_Search_DimensionMismatch(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db) // dim = 4
	_, err := a.Search(context.Background(), domain.VectorQuery{
		Vector: []float32{0.1, 0.2}, // 2-dim, adapter expects 4
		Limit:  5,
	})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("expected ErrDimensionMismatch, got %v", err)
	}
}

// TestAdapter_Search_ServerErrorPropagates verifies a DB error is returned.
func TestAdapter_Search_ServerErrorPropagates(t *testing.T) {
	serverErr := errors.New("pgvector: connection reset")
	db := &fakeDB{queryErr: serverErr}
	a := newTestAdapter(t, db)
	_, err := a.Search(context.Background(), domain.VectorQuery{
		Vector: []float32{0.1, 0.2, 0.3, 0.4},
		Limit:  5,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestAdapter_Delete_GeneratesParameterizedSQL verifies the adapter generates
// DELETE ... WHERE id = ANY($1).
func TestAdapter_Delete_GeneratesParameterizedSQL(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	if err := a.Delete(context.Background(), []int64{10, 20, 30}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(db.execCalls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(db.execCalls))
	}
	c := db.execCalls[0]
	if !strings.Contains(c.sql, "DELETE FROM") {
		t.Errorf("SQL should contain DELETE FROM: %s", c.sql)
	}
	if !strings.Contains(c.sql, "ANY($1)") {
		t.Errorf("SQL should contain ANY($1): %s", c.sql)
	}
	// The args should contain the []int64 slice.
	if len(c.args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(c.args))
	}
	ids, ok := c.args[0].([]int64)
	if !ok {
		t.Fatalf("arg[0] type = %T, want []int64", c.args[0])
	}
	if len(ids) != 3 || ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Errorf("ids = %v, want [10 20 30]", ids)
	}
}

// TestAdapter_Delete_EmptyIsNoop verifies empty delete is a no-op.
func TestAdapter_Delete_EmptyIsNoop(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	if err := a.Delete(context.Background(), nil); err != nil {
		t.Fatalf("Delete(nil): %v", err)
	}
	if len(db.execCalls) != 0 {
		t.Errorf("empty delete should not exec; got %d calls", len(db.execCalls))
	}
}

// TestAdapter_Health_Healthy verifies Ping success translates to healthy.
func TestAdapter_Health_Healthy(t *testing.T) {
	db := &fakeDB{pingErr: nil}
	a := newTestAdapter(t, db)
	h := a.Health(context.Background())
	if h.Status != domain.StatusHealthy {
		t.Errorf("Status = %q, want %q (msg: %s)", h.Status, domain.StatusHealthy, h.Message)
	}
}

// TestAdapter_Health_UnhealthyOnError verifies Ping failure translates to
// unhealthy WITHOUT leaking the password.
func TestAdapter_Health_UnhealthyOnError(t *testing.T) {
	db := &fakeDB{pingErr: errors.New("connection refused")}
	a := newTestAdapter(t, db)
	h := a.Health(context.Background())
	if h.Status != domain.StatusUnhealthy {
		t.Errorf("Status = %q, want %q", h.Status, domain.StatusUnhealthy)
	}
	if strings.Contains(h.Message, "test") {
		t.Errorf("health message should not leak password: %s", h.Message)
	}
}

// TestAdapter_Close_DelegatesToDB verifies Close closes the underlying pool.
func TestAdapter_Close_DelegatesToDB(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	a.ownDB = true
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if db.closeCalls != 1 {
		t.Errorf("db Close called %d times, want 1", db.closeCalls)
	}
}

// TestAdapter_Close_NoopWhenNotOwned verifies Close is a no-op when ownDB=false.
func TestAdapter_Close_NoopWhenNotOwned(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)
	a.ownDB = false
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if db.closeCalls != 0 {
		t.Errorf("db Close called %d times, want 0 (not owned)", db.closeCalls)
	}
}

// TestAdapter_NoSecretsInErrors verifies the DSN password never appears in any
// error message (no plaintext secret leak — REQ-CP-002).
func TestAdapter_NoSecretsInErrors(t *testing.T) {
	const secret = "super-secret-pw-do-not-leak"
	db := &fakeDB{
		beginErr: errors.New("connection failed: auth as user postgres password=" + secret),
	}
	a := newTestAdapter(t, db)
	a.password = secret

	err := a.Upsert(context.Background(), validPoints())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("password LEAKED into error message: %s", err.Error())
	}
}

// --- validation tests -------------------------------------------------------

// TestValidateAdapterConfig_RejectsUnsafeIdentifiers verifies that schema/table
// names containing SQL injection payloads are rejected.
func TestValidateAdapterConfig_RejectsUnsafeIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		cfg  AdapterConfig
		err  string
	}{
		{
			name: "empty DSN",
			cfg:  AdapterConfig{DSN: "", Dimension: 4},
			err:  "DSN",
		},
		{
			name: "dimension zero",
			cfg:  AdapterConfig{DSN: "x", Dimension: 0},
			err:  "dimension",
		},
		{
			name: "schema with semicolon (injection)",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, Schema: "foo; DROP TABLE"},
			err:  "safe identifier",
		},
		{
			name: "schema with quote (injection)",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, Schema: "foo'--"},
			err:  "safe identifier",
		},
		{
			name: "table with space (injection)",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, Table: "t WHERE 1=1"},
			err:  "safe identifier",
		},
		{
			name: "schema with dot (path traversal)",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, Schema: "public.cortex"},
			err:  "safe identifier",
		},
		{
			name: "invalid index type",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, IndexType: "bogus"},
			err:  "index_type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAdapterConfig(&tt.cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.err)
			}
			if !strings.Contains(err.Error(), tt.err) {
				t.Errorf("error should contain %q, got: %v", tt.err, err)
			}
		})
	}
}

// TestValidateAdapterConfig_AppliesDefaults verifies default values are applied.
func TestValidateAdapterConfig_AppliesDefaults(t *testing.T) {
	cfg := AdapterConfig{DSN: "postgres://localhost/db", Dimension: 4}
	if err := validateAdapterConfig(&cfg); err != nil {
		t.Fatalf("validateAdapterConfig: %v", err)
	}
	if cfg.Schema != "cortex_vector" {
		t.Errorf("default Schema = %q, want cortex_vector", cfg.Schema)
	}
	if cfg.Table != "embeddings" {
		t.Errorf("default Table = %q, want embeddings", cfg.Table)
	}
	if cfg.IndexType != "hnsw" {
		t.Errorf("default IndexType = %q, want hnsw", cfg.IndexType)
	}
	if cfg.MaxBatchSize != 256 {
		t.Errorf("default MaxBatchSize = %d, want 256", cfg.MaxBatchSize)
	}
	if cfg.Timeout != defaultTimeout {
		t.Errorf("default Timeout = %v, want %v", cfg.Timeout, defaultTimeout)
	}
	if cfg.StatementTimeoutMs != defaultStatementTimeoutMs {
		t.Errorf("default StatementTimeoutMs = %d, want %d", cfg.StatementTimeoutMs, defaultStatementTimeoutMs)
	}
}

// --- secret redaction tests -------------------------------------------------

// TestExtractPassword_URLFormat verifies password extraction from URL DSN.
func TestExtractPassword_URLFormat(t *testing.T) {
	pw := extractPassword("postgres://user:secretpass@localhost:5432/db")
	if pw != "secretpass" {
		t.Errorf("extractPassword = %q, want secretpass", pw)
	}
}

// TestExtractPassword_KVPFormat verifies password extraction from key=value DSN.
func TestExtractPassword_KVPFormat(t *testing.T) {
	pw := extractPassword("host=localhost password=kvpsecret dbname=test")
	if pw != "kvpsecret" {
		t.Errorf("extractPassword = %q, want kvpsecret", pw)
	}
}

// TestExtractPassword_NoPassword verifies empty string when no password.
func TestExtractPassword_NoPassword(t *testing.T) {
	pw := extractPassword("postgres://localhost/db")
	if pw != "" {
		t.Errorf("extractPassword = %q, want empty", pw)
	}
}

// TestRedactDSN_ReplacesPassword verifies password is scrubbed from errors.
func TestRedactDSN_ReplacesPassword(t *testing.T) {
	const secret = "pw123"
	err := fmt.Errorf("auth failed for user postgres password=%s", secret)
	out := redactDSN(err, secret)
	if strings.Contains(out.Error(), secret) {
		t.Errorf("password still present after redact: %s", out.Error())
	}
	if !strings.Contains(out.Error(), "***REDACTED***") {
		t.Errorf("expected placeholder: %s", out.Error())
	}
	// Empty secret or nil error → passthrough.
	if redactDSN(nil, secret) != nil {
		t.Error("nil error should pass through")
	}
	plain := errors.New("no secret here")
	if got := redactDSN(plain, ""); got != plain {
		t.Error("empty secret should pass through")
	}
}

// --- schema statements tests -------------------------------------------------

// TestSchemaStatements_ContainsExtensionAndTable verifies the DDL includes
// CREATE EXTENSION, CREATE SCHEMA, CREATE TABLE, and CREATE INDEX. The index
// uses HNSW with typed (m, ef_construction) options.
func TestSchemaStatements_ContainsExtensionAndTable(t *testing.T) {
	stmts := schemaStatements("cortex_vector", "embeddings", 384, indexTuning{
		IndexType:          "hnsw",
		HNSWM:              16,
		HNSWEfConstruction: 64,
	})
	if len(stmts) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(stmts))
	}
	if !strings.Contains(stmts[0], "CREATE EXTENSION IF NOT EXISTS vector") {
		t.Errorf("stmt[0] should create extension: %s", stmts[0])
	}
	if !strings.Contains(stmts[1], "CREATE SCHEMA IF NOT EXISTS cortex_vector") {
		t.Errorf("stmt[1] should create schema: %s", stmts[1])
	}
	if !strings.Contains(stmts[2], "CREATE TABLE IF NOT EXISTS cortex_vector.embeddings") {
		t.Errorf("stmt[2] should create table: %s", stmts[2])
	}
	if !strings.Contains(stmts[2], "vector(384)") {
		t.Errorf("stmt[2] should specify vector(384): %s", stmts[2])
	}
	if !strings.Contains(stmts[3], "CREATE INDEX IF NOT EXISTS") {
		t.Errorf("stmt[3] should create index: %s", stmts[3])
	}
	if !strings.Contains(stmts[3], "USING hnsw") {
		t.Errorf("stmt[3] should use hnsw: %s", stmts[3])
	}
	if !strings.Contains(stmts[3], "vector_cosine_ops") {
		t.Errorf("stmt[3] should use vector_cosine_ops: %s", stmts[3])
	}
}

// TestSchemaStatements_HNSWEmitsTypedOptions verifies the HNSW DDL emits
// validated integer WITH (m = N, ef_construction = N), never raw SQL.
func TestSchemaStatements_HNSWEmitsTypedOptions(t *testing.T) {
	stmts := schemaStatements("s", "t", 128, indexTuning{
		IndexType:          "hnsw",
		HNSWM:              32,
		HNSWEfConstruction: 128,
	})
	idx := stmts[3]
	if !strings.Contains(idx, "WITH (m = 32, ef_construction = 128)") {
		t.Errorf("HNSW DDL should emit typed WITH options, got: %s", idx)
	}
	// No IVFFlat-specific option should appear.
	if strings.Contains(idx, "lists") {
		t.Errorf("HNSW DDL should NOT contain ivfflat lists option: %s", idx)
	}
}

// TestSchemaStatements_IVFFlatEmitsTypedOptions verifies IVFFlat DDL emits
// validated integer WITH (lists = N).
func TestSchemaStatements_IVFFlatEmitsTypedOptions(t *testing.T) {
	stmts := schemaStatements("s", "t", 128, indexTuning{
		IndexType:    "ivfflat",
		IVFFlatLists: 200,
	})
	idx := stmts[3]
	if !strings.Contains(idx, "USING ivfflat") {
		t.Errorf("stmt[3] should use ivfflat: %s", idx)
	}
	if !strings.Contains(idx, "WITH (lists = 200)") {
		t.Errorf("IVFFlat DDL should emit typed WITH (lists = 200), got: %s", idx)
	}
	// No HNSW-specific options should appear.
	if strings.Contains(idx, "m =") || strings.Contains(idx, "ef_construction") {
		t.Errorf("IVFFlat DDL should NOT contain HNSW options: %s", idx)
	}
}

// TestSchemaStatements_NoRawSQLSurface verifies the DDL builder accepts only
// typed integers — there is no string parameter that could carry arbitrary SQL.
// This is a compile-time contract: schemaStatements takes an indexTuning struct
// of integers, so this test documents that invariant.
func TestSchemaStatements_NoRawSQLSurface(t *testing.T) {
	// indexTuning has no string field for options — only typed integers.
	tuning := indexTuning{
		IndexType:          "hnsw",
		HNSWM:              16,
		HNSWEfConstruction: 64,
		IVFFlatLists:       100,
	}
	stmts := schemaStatements("s", "t", 4, tuning)
	idx := stmts[3]
	// The only string content in the index DDL is the validated op class and
	// the integer options. Verify no semicolon (no statement injection vector).
	if strings.Contains(idx, ";") {
		t.Errorf("index DDL should not contain semicolon (injection vector): %s", idx)
	}
}

// --- index tuning validation tests -------------------------------------------

// TestValidateAdapterConfig_RejectsBadIndexTuning verifies out-of-range index
// tuning values are rejected at adapter construction.
func TestValidateAdapterConfig_RejectsBadIndexTuning(t *testing.T) {
	tests := []struct {
		name string
		cfg  AdapterConfig
		err  string
	}{
		{
			name: "hnsw_m below min",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, HNSWM: 1},
			err:  "hnsw_m",
		},
		{
			name: "hnsw_m above max",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, HNSWM: 200},
			err:  "hnsw_m",
		},
		{
			name: "hnsw_ef_construction below min",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, HNSWEfConstruction: -1},
			err:  "hnsw_ef_construction",
		},
		{
			name: "hnsw_ef_construction above max",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, HNSWEfConstruction: 5000},
			err:  "hnsw_ef_construction",
		},
		{
			name: "ivfflat_lists below min",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, IVFFlatLists: -1},
			err:  "ivfflat_lists",
		},
		{
			name: "ivfflat_lists above max",
			cfg:  AdapterConfig{DSN: "x", Dimension: 4, IVFFlatLists: 100000},
			err:  "ivfflat_lists",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAdapterConfig(&tt.cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.err)
			}
			if !strings.Contains(err.Error(), tt.err) {
				t.Errorf("error should contain %q, got: %v", tt.err, err)
			}
		})
	}
}

// TestValidateAdapterConfig_AppliesIndexTuningDefaults verifies zero values
// normalize to pgvector-recommended defaults.
func TestValidateAdapterConfig_AppliesIndexTuningDefaults(t *testing.T) {
	cfg := AdapterConfig{DSN: "postgres://localhost/db", Dimension: 4}
	if err := validateAdapterConfig(&cfg); err != nil {
		t.Fatalf("validateAdapterConfig: %v", err)
	}
	if cfg.HNSWM != 16 {
		t.Errorf("default HNSWM = %d, want 16", cfg.HNSWM)
	}
	if cfg.HNSWEfConstruction != 64 {
		t.Errorf("default HNSWEfConstruction = %d, want 64", cfg.HNSWEfConstruction)
	}
	if cfg.IVFFlatLists != 100 {
		t.Errorf("default IVFFlatLists = %d, want 100", cfg.IVFFlatLists)
	}
}

// --- ON CONFLICT updated_at test ---------------------------------------------

// TestAdapter_Upsert_OnConflictSetsUpdatedAt verifies the upsert SQL sets
// updated_at = NOW() so re-upserts refresh the timestamp.
func TestAdapter_Upsert_OnConflictSetsUpdatedAt(t *testing.T) {
	db := &fakeDB{}
	a := newTestAdapter(t, db)

	if err := a.Upsert(context.Background(), validPoints()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var upsertExecs []execCall
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "INSERT INTO") {
			upsertExecs = append(upsertExecs, c)
		}
	}
	if len(upsertExecs) == 0 {
		t.Fatal("expected at least 1 INSERT exec call")
	}
	sql := upsertExecs[0].sql
	if !strings.Contains(sql, "updated_at = NOW()") {
		t.Errorf("upsert SQL should set updated_at = NOW() on conflict: %s", sql)
	}
	if !strings.Contains(sql, "ON CONFLICT (id) DO UPDATE") {
		t.Errorf("upsert SQL should use ON CONFLICT: %s", sql)
	}
}

// --- config defaults test ---------------------------------------------------

// TestDefaultTimeout_Constant verifies the timeout constant is set.
func TestDefaultTimeout_Constant(t *testing.T) {
	if defaultTimeout != 30*time.Second {
		t.Errorf("defaultTimeout = %v, want 30s", defaultTimeout)
	}
}
