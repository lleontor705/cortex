//go:build !pgvector_integration

// Tuning unit tests for the config-gated pgvector exact-scan tuning
// (REQ-RET-102). These run WITHOUT a PostgreSQL server (same build tag as
// adapter_test.go) and reuse its fake rows helper. The DB-backed ordering
// acceptance (IP mode ordering == cosine ordering on normalized data) needs a
// live pgvector instance and stays behind the pgvector_integration suite.
package pgvector

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// --- env parsing -------------------------------------------------------------

func TestSearchTuningFromEnv_DisabledByDefault(t *testing.T) {
	tuning := SearchTuningFromEnv(func(string) string { return "" })
	if tuning.Enabled() {
		t.Fatal("unset env must leave tuning fully disabled")
	}
	if tuning.MaxParallelWorkersPerGather != 0 || tuning.InnerProduct {
		t.Fatalf("zero tuning expected, got %+v", tuning)
	}
}

func TestSearchTuningFromEnv_ParallelWorkers(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"4", 4},
		{" 8 ", 8},
		{"0", 0},   // zero disables
		{"-2", 0},  // negative disables
		{"abc", 0}, // non-numeric disables
		{"4x", 0},  // partial numeric disables
		{"", 0},    // empty disables
	}
	for _, tc := range cases {
		tuning := SearchTuningFromEnv(func(k string) string {
			if k == EnvMaxParallelWorkersPerGather {
				return tc.raw
			}
			return ""
		})
		if tuning.MaxParallelWorkersPerGather != tc.want {
			t.Errorf("env %q → MaxParallelWorkersPerGather = %d, want %d", tc.raw, tuning.MaxParallelWorkersPerGather, tc.want)
		}
	}
}

func TestSearchTuningFromEnv_DistanceMode(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"ip", true},
		{"IP", true},
		{" ip ", true},
		{"cosine", false},
		{"COSINE", false},
		{"", false},
		{"garbage", false}, // unrecognized values fall back to the default mode
	}
	for _, tc := range cases {
		tuning := SearchTuningFromEnv(func(k string) string {
			if k == EnvDistanceMode {
				return tc.raw
			}
			return ""
		})
		if tuning.InnerProduct != tc.want {
			t.Errorf("env %q → InnerProduct = %v, want %v", tc.raw, tuning.InnerProduct, tc.want)
		}
	}
}

// --- SQL shape ---------------------------------------------------------------

// preTuningSearchSQL is the exact SQL rendered by Search before the tuning
// change. The zero tuning MUST reproduce it byte-for-byte (REQ-RET-102
// "default behavior unchanged").
const preTuningSearchSQL = `SELECT id, 1 - (embedding <=> $1::vector) AS similarity
FROM cortex_test.embeddings WHERE project = $2
ORDER BY embedding <=> $1::vector
LIMIT $3`

func TestSearchTuning_DefaultSQLByteIdentical(t *testing.T) {
	var zero SearchTuning
	got := zero.searchSQL("cortex_test.embeddings", " WHERE project = $2", 3)
	if got != preTuningSearchSQL {
		t.Fatalf("default search SQL drifted from the pre-tuning exact scan:\n got: %q\nwant: %q", got, preTuningSearchSQL)
	}
}

func TestSearchTuning_IPModeSQL(t *testing.T) {
	ip := SearchTuning{InnerProduct: true}
	sql := ip.searchSQL("s.t", "", 2)
	if !strings.Contains(sql, "1 - (embedding <#> $1::vector) AS similarity") {
		t.Errorf("IP similarity must stay 1 - distance with the <#> operator: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY embedding <#> $1::vector") {
		t.Errorf("IP ordering must use <#>: %s", sql)
	}
	if strings.Contains(sql, "<=>") {
		t.Errorf("IP mode must not use the cosine operator: %s", sql)
	}
}

func TestSearchTuning_SetLocalStatements(t *testing.T) {
	var zero SearchTuning
	if stmts := zero.setLocalStatements(); stmts != nil {
		t.Fatalf("disabled tuning must emit no SET LOCAL, got %v", stmts)
	}
	tuned := SearchTuning{MaxParallelWorkersPerGather: 4}
	stmts := tuned.setLocalStatements()
	if len(stmts) != 1 || !strings.Contains(stmts[0], "SET LOCAL max_parallel_workers_per_gather = 4") {
		t.Fatalf("unexpected SET LOCAL statements: %v", stmts)
	}
}

// --- adapter search paths (fakes) ---------------------------------------------

func tuneTestAdapter(t *testing.T, db pgvectorDB, tuning SearchTuning) *Adapter {
	t.Helper()
	a, err := NewWithDB(db, AdapterConfig{
		DSN:       "postgres://test:test@localhost:5432/test",
		Schema:    "cortex_test",
		Table:     "embeddings",
		Dimension: 4,
		ModelName: "test-model",
		Tuning:    tuning,
	})
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	a.created = true
	return a
}

func tuneSearchQuery() domain.VectorQuery {
	return domain.VectorQuery{
		Vector: []float32{0.1, 0.2, 0.3, 0.4},
		Limit:  5,
	}
}

// TestAdapter_Search_DefaultTuningNoTx pins the default path: no transaction,
// no SET LOCAL, cosine operator — byte-identical pre-tuning behavior.
func TestAdapter_Search_DefaultTuningNoTx(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows([][]any{{int64(1), 0.9}})}
	a := tuneTestAdapter(t, db, SearchTuning{})

	results, err := a.Search(context.Background(), tuneSearchQuery())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if db.beginCalls != 0 {
		t.Errorf("default tuning must not begin a tx; got %d", db.beginCalls)
	}
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "SET LOCAL") {
			t.Errorf("default tuning must not issue SET LOCAL: %s", c.sql)
		}
	}
	if len(db.queryCalls) != 1 || !strings.Contains(db.queryCalls[0].sql, "<=>") {
		t.Fatalf("default search must use the cosine operator: %v", db.queryCalls)
	}
}

// TestAdapter_Search_IPModeNoTx keeps the pool-level query when only the
// distance mode is enabled (no parallel-gather knob).
func TestAdapter_Search_IPModeNoTx(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows([][]any{{int64(1), 1.85}})}
	a := tuneTestAdapter(t, db, SearchTuning{InnerProduct: true})

	results, err := a.Search(context.Background(), tuneSearchQuery())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Score != 1.85 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if db.beginCalls != 0 {
		t.Errorf("IP-only tuning must not begin a tx; got %d", db.beginCalls)
	}
	if len(db.queryCalls) != 1 || !strings.Contains(db.queryCalls[0].sql, "<#>") {
		t.Fatalf("IP search must use the <#> operator: %v", db.queryCalls)
	}
	if strings.Contains(db.queryCalls[0].sql, "1 - (embedding <=>") {
		t.Errorf("IP similarity expression must use <#>: %s", db.queryCalls[0].sql)
	}
}

// TestAdapter_Search_ParallelTuningFallbackTx exercises the tuned path with the
// minimal fake tx (no Query support): SET LOCAL is issued inside the tx, the
// statement falls back to the pool query, and the tx is rolled back (nothing
// to commit — SET LOCAL is transaction-scoped).
func TestAdapter_Search_ParallelTuningFallbackTx(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows([][]any{{int64(1), 0.9}})}
	a := tuneTestAdapter(t, db, SearchTuning{MaxParallelWorkersPerGather: 4})

	if _, err := a.Search(context.Background(), tuneSearchQuery()); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if db.beginCalls != 1 {
		t.Fatalf("tuned search must begin a tx; got %d begin calls", db.beginCalls)
	}
	found := false
	for _, c := range db.execCalls {
		if strings.Contains(c.sql, "SET LOCAL max_parallel_workers_per_gather = 4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("SET LOCAL not issued inside the tuned tx: %v", db.execCalls)
	}
}

// --- full tuned-tx fake (tx supports Query) ------------------------------------

// tuningFakeDB is a fake DB whose transactions implement Query, mirroring the
// production poolTx/pgx.Tx shape. It records tx-scoped execs and queries so the
// true tuned path (SET LOCAL + tx-scoped SELECT + Commit) can be asserted.
type tuningFakeDB struct {
	execErr   error
	queryErr  error
	queryRows pgx.Rows

	execCalls  []execCall
	txQueries  []queryCall
	txExecs    []execCall
	beginCalls int
	closeCalls int
	committed  bool
	rolledBack bool
}

func (f *tuningFakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execCalls = append(f.execCalls, execCall{sql: sql, args: args})
	return pgconn.CommandTag{}, f.execErr
}

func (f *tuningFakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryRows != nil {
		return f.queryRows, nil
	}
	return newFakeRows(nil), nil
}

func (f *tuningFakeDB) BeginTx(_ context.Context) (pgvectorTx, error) {
	f.beginCalls++
	return &tuningFakeTx{db: f}, nil
}

func (f *tuningFakeDB) Ping(_ context.Context) error { return nil }
func (f *tuningFakeDB) Close()                       { f.closeCalls++ }

type tuningFakeTx struct {
	db        *tuningFakeDB
	committed bool
	rolled    bool
}

func (t *tuningFakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.db.txExecs = append(t.db.txExecs, execCall{sql: sql, args: args})
	return pgconn.CommandTag{}, nil
}

func (t *tuningFakeTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.db.txQueries = append(t.db.txQueries, queryCall{sql: sql, args: args})
	if t.db.queryErr != nil {
		return nil, t.db.queryErr
	}
	if t.db.queryRows != nil {
		return t.db.queryRows, nil
	}
	return newFakeRows(nil), nil
}

func (t *tuningFakeTx) Commit(_ context.Context) error {
	t.committed = true
	t.db.committed = true
	return nil
}

func (t *tuningFakeTx) Rollback(_ context.Context) error {
	t.rolled = true
	t.db.rolledBack = true
	return nil
}

// TestAdapter_Search_ParallelTuningFullTxPath asserts the production-shaped
// tuned path: SET LOCAL scoped to the tx, SELECT executed on the tx, Commit
// after rows are consumed.
func TestAdapter_Search_ParallelTuningFullTxPath(t *testing.T) {
	db := &tuningFakeDB{queryRows: newFakeRows([][]any{{int64(7), 0.77}})}
	a := tuneTestAdapter(t, db, SearchTuning{MaxParallelWorkersPerGather: 2})

	results, err := a.Search(context.Background(), tuneSearchQuery())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != 7 || results[0].Score != 0.77 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if db.beginCalls != 1 {
		t.Fatalf("expected 1 BeginTx, got %d", db.beginCalls)
	}
	if len(db.txExecs) != 1 || !strings.Contains(db.txExecs[0].sql, "SET LOCAL max_parallel_workers_per_gather = 2") {
		t.Fatalf("SET LOCAL must run inside the tx: %v", db.txExecs)
	}
	if len(db.txQueries) != 1 || !strings.Contains(db.txQueries[0].sql, "<=>") {
		t.Fatalf("search SELECT must run on the tx: %v", db.txQueries)
	}
	if !db.committed {
		t.Error("tuned tx must be committed after successful search")
	}
	if db.rolledBack {
		t.Error("successful tuned search must not roll back")
	}
}

// TestAdapter_Search_ParallelTuningQueryErrorRollsBack verifies the tuned tx
// is rolled back when the search statement fails.
func TestAdapter_Search_ParallelTuningQueryErrorRollsBack(t *testing.T) {
	db := &tuningFakeDB{queryErr: context.DeadlineExceeded}
	a := tuneTestAdapter(t, db, SearchTuning{MaxParallelWorkersPerGather: 2})

	if _, err := a.Search(context.Background(), tuneSearchQuery()); err == nil {
		t.Fatal("expected error, got nil")
	}
	if !db.rolledBack {
		t.Error("failed tuned search must roll back the tx")
	}
	if db.committed {
		t.Error("failed tuned search must not commit")
	}
}

// TestAdapter_Search_IPModeParallelTuningCombined verifies both knobs compose.
func TestAdapter_Search_IPModeParallelTuningCombined(t *testing.T) {
	db := &tuningFakeDB{queryRows: newFakeRows([][]any{{int64(1), 1.9}})}
	a := tuneTestAdapter(t, db, SearchTuning{MaxParallelWorkersPerGather: 4, InnerProduct: true})

	if _, err := a.Search(context.Background(), tuneSearchQuery()); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(db.txExecs) != 1 || !strings.Contains(db.txExecs[0].sql, "SET LOCAL max_parallel_workers_per_gather = 4") {
		t.Fatalf("SET LOCAL must run inside the tx: %v", db.txExecs)
	}
	if len(db.txQueries) != 1 || !strings.Contains(db.txQueries[0].sql, "1 - (embedding <#> $1::vector) AS similarity") {
		t.Fatalf("combined mode must keep 1 - distance with <#>: %v", db.txQueries)
	}
}
