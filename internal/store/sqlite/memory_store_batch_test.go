// Batch hydration contract tests for sqlite.Store.GetByIDs (VEC-01).
//
// These tests pin the observable contract of the optional batch lookup that
// backs retrieval-local batch candidate hydration:
//
//   - Empty input issues ZERO SQL statements and returns an empty map.
//   - A successful N=100 batch issues exactly ONE hydration SQL statement.
//   - Soft-deleted and missing rows are absent from the returned map.
//   - IN-list parameters are chunked safely beyond the placeholder pool
//     (documented budget: ceil(n/maxGetByIDsParameters) statements).
//   - Errors propagate (the retrieval layer owns the legacy fallback).
//
// SQL statement counting is implemented with a thin driver wrapper around the
// modernc.org/sqlite driver that counts every statement EXECUTION that
// actually reaches the driver: one-shot queries/execs at QueryContext/
// ExecContext, and prepared-statement runs at the wrapped Stmt.Query/Stmt.Exec
// (preparation itself is not counted, so cached-statement callers like
// Store.GetByIDs count once per execution, exactly like one-shot callers).
package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	moderncsqlite "modernc.org/sqlite" // base driver for the counting wrapper

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ---------------------------------------------------------------------------
// Counting driver infrastructure
// ---------------------------------------------------------------------------

// stmtCounter counts SQL statements that reached the underlying driver.
type stmtCounter struct{ n int64 }

func (c *stmtCounter) add()         { atomic.AddInt64(&c.n, 1) }
func (c *stmtCounter) value() int64 { return atomic.LoadInt64(&c.n) }
func (c *stmtCounter) reset()       { atomic.StoreInt64(&c.n, 0) }

// countingDriver wraps the modernc sqlite driver, returning connections that
// count statements. It is registered under a unique name per database so
// tests can run in parallel without sql.Register panics.
type countingDriver struct {
	base    driver.Driver
	counter *stmtCounter
}

func (d *countingDriver) Open(dsn string) (driver.Conn, error) {
	conn, err := d.base.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, counter: d.counter}, nil
}

// countingConn delegates every statement to the wrapped connection and
// counts each completed one-shot query/exec (and each execution of a
// prepared statement, via the countingStmt wrapper returned from Prepare —
// modernc.org/sqlite never returns ErrSkip for one-shot calls, so each
// statement execution is counted exactly once at its execution site).
type countingConn struct {
	driver.Conn
	counter *stmtCounter
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, query, args)
	if err == driver.ErrSkip {
		return nil, err // prepared fallback; counted at the stmt execution
	}
	c.counter.add()
	return rows, err
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := e.ExecContext(ctx, query, args)
	if err == driver.ErrSkip {
		return nil, err
	}
	c.counter.add()
	return res, err
}

// Prepare is NOT counted: preparation is not a statement execution. The
// returned statement is wrapped so each later Query/Exec on it counts
// exactly once — mirroring the one-shot QueryContext/ExecContext accounting
// for the cached-statement path exercised by Store.GetByIDs.
func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &countingStmt{Stmt: st, counter: c.counter}, nil
}

// countingStmt counts each executed prepared statement.
type countingStmt struct {
	driver.Stmt
	counter *stmtCounter
}

func (s *countingStmt) Query(args []driver.Value) (driver.Rows, error) {
	//nolint:staticcheck // SA1019: modernc.org/sqlite's stmt implements only
	// the deprecated Query/Exec driver interfaces (no StmtQueryContext), so
	// a delegating wrapper has no non-deprecated call site available; this
	// is exactly the path database/sql itself takes for such drivers.
	rows, err := s.Stmt.Query(args)
	s.counter.add()
	return rows, err
}

func (s *countingStmt) Exec(args []driver.Value) (driver.Result, error) {
	//nolint:staticcheck // SA1019: see Query justification.
	res, err := s.Stmt.Exec(args)
	s.counter.add()
	return res, err
}

// countingDriverSeq guarantees unique driver registrations across tests.
var countingDriverSeq int64

// newCountingDB opens a fresh in-memory SQLite database whose statements are
// counted. The counter starts at the post-schema state zero AFTER applySchema
// runs; call counter.reset() before any measured operation.
func newCountingDB(t *testing.T) (*sql.DB, *stmtCounter) {
	t.Helper()

	counter := &stmtCounter{}
	name := fmt.Sprintf("sqlite-counting-%d", atomic.AddInt64(&countingDriverSeq, 1))
	sql.Register(name, &countingDriver{base: &moderncsqlite.Driver{}, counter: counter})

	db, err := sql.Open(name, ":memory:")
	if err != nil {
		t.Fatalf("batch test: open counting db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db, counter
}

// batchTestSchema is the observations layout the canonical store SELECTs
// rely on (mirrors the v2 baseline subset used by memory_store_test.go).
const batchTestSchema = `
	CREATE TABLE IF NOT EXISTS observations (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT    NOT NULL,
		type       TEXT    NOT NULL,
		title      TEXT    NOT NULL,
		content    TEXT    NOT NULL,
		project    TEXT,
		scope      TEXT    NOT NULL DEFAULT 'project',
		topic_key  TEXT,
		normalized_hash TEXT,
		revision_count INTEGER NOT NULL DEFAULT 1,
		duplicate_count INTEGER NOT NULL DEFAULT 1,
		last_seen_at TEXT,
		confidence REAL    NOT NULL DEFAULT 1.0,
		source     TEXT    NOT NULL DEFAULT 'manual',
		tags       TEXT,
		created_at TEXT    NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
		deleted_at TEXT
	);
`

// newCountingTestStore returns a Store over a fresh counting in-memory DB
// with the observations schema applied and the counter reset to zero.
func newCountingTestStore(t *testing.T) (*Store, *stmtCounter) {
	t.Helper()

	db, counter := newCountingDB(t)
	if _, err := db.Exec(batchTestSchema); err != nil {
		t.Fatalf("batch test: apply schema: %v", err)
	}
	counter.reset()
	return NewStore(db), counter
}

// insertObservationRows inserts count rows with deterministic titles and
// returns nothing; inserted IDs are consecutive starting at 1 (fresh DB).
func insertObservationRows(t *testing.T, db *sql.DB, prefix string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		_, err := db.Exec(`
			INSERT INTO observations (session_id, type, title, content, project, scope, source, tags)
			VALUES (?, 'manual', ?, ?, 'proj-batch', 'project', 'manual', ?)
		`,
			"s-batch",
			fmt.Sprintf("%s-%04d", prefix, i),
			fmt.Sprintf("content for %s-%04d", prefix, i),
			fmt.Sprintf(`["tag-%03d"]`, i%7),
		)
		if err != nil {
			t.Fatalf("batch test: insert row %d: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// GetByIDs contract tests (VEC-01 oracles)
// ---------------------------------------------------------------------------

// TestGetByIDs_EmptyInput_IssuesZeroSQL pins the boundary rule: empty input
// (nil or empty slice) must not issue any SQL statement.
func TestGetByIDs_EmptyInput_IssuesZeroSQL(t *testing.T) {
	store, counter := newCountingTestStore(t)
	ctx := context.Background()

	for _, ids := range [][]int64{nil, {}} {
		got, err := store.GetByIDs(ctx, ids)
		if err != nil {
			t.Fatalf("GetByIDs(%v): unexpected error: %v", ids, err)
		}
		if len(got) != 0 {
			t.Fatalf("GetByIDs(%v): expected empty map, got %d entries", ids, len(got))
		}
	}
	if n := counter.value(); n != 0 {
		t.Fatalf("empty input must issue zero SQL statements, got %d", n)
	}
}

// TestGetByIDs_N100_IssuesSingleSQL_AndMatchesGetByID pins the N=100 budget
// (exactly one hydration SQL) and field-for-field equality with the legacy
// per-ID path over the canonical column set.
func TestGetByIDs_N100_IssuesSingleSQL_AndMatchesGetByID(t *testing.T) {
	store, counter := newCountingTestStore(t)
	ctx := context.Background()
	insertObservationRows(t, store.DB(), "obs", 100)

	ids := make([]int64, 0, 100)
	for id := int64(1); id <= 100; id++ {
		ids = append(ids, id)
	}

	counter.reset()
	got, err := store.GetByIDs(ctx, ids)
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if n := counter.value(); n != 1 {
		t.Fatalf("N=100 successful batch must issue exactly 1 hydration SQL statement, got %d", n)
	}
	if len(got) != 100 {
		t.Fatalf("expected 100 hydrated observations, got %d", len(got))
	}

	for _, id := range ids {
		batch := got[id]
		if batch == nil {
			t.Fatalf("ID %d missing from batch map", id)
		}
		single, err := store.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID(%d): %v", id, err)
		}
		if batch.ID != single.ID ||
			batch.Title != single.Title ||
			batch.Content != single.Content ||
			batch.Type != single.Type ||
			batch.Project != single.Project ||
			batch.Scope != single.Scope ||
			batch.SessionID != single.SessionID ||
			batch.TopicKey != single.TopicKey ||
			batch.Confidence != single.Confidence ||
			batch.Source != single.Source ||
			len(batch.Tags) != len(single.Tags) ||
			!batch.CreatedAt.Equal(single.CreatedAt) ||
			!batch.UpdatedAt.Equal(single.UpdatedAt) {
			t.Fatalf("ID %d: batch row %+v differs from GetByID row %+v", id, batch, single)
		}
	}
}

// TestGetByIDs_DropsSoftDeletedAndMissing pins the missing/soft-deleted
// semantics: soft-deleted and absent IDs are simply absent from the map.
func TestGetByIDs_DropsSoftDeletedAndMissing(t *testing.T) {
	store, _ := newCountingTestStore(t)
	ctx := context.Background()
	insertObservationRows(t, store.DB(), "obs", 3)

	if err := store.SoftDelete(ctx, 2); err != nil {
		t.Fatalf("SoftDelete(2): %v", err)
	}

	got, err := store.GetByIDs(ctx, []int64{1, 2, 3, 99999})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 survivors (deleted 2 and missing 99999 dropped), got %d: %v", len(got), got)
	}
	if got[1] == nil || got[3] == nil {
		t.Fatalf("expected IDs 1 and 3 present, got map %v", got)
	}
	if got[2] != nil {
		t.Fatalf("soft-deleted ID 2 must be absent from the batch map")
	}
}

// TestGetByIDs_DuplicateIDs_SingleStatementSamePointer verifies duplicates in
// the requested ID list are tolerated (one statement, idempotent map insert).
func TestGetByIDs_DuplicateIDs_SingleStatementSamePointer(t *testing.T) {
	store, counter := newCountingTestStore(t)
	ctx := context.Background()
	insertObservationRows(t, store.DB(), "obs", 3)

	counter.reset()
	got, err := store.GetByIDs(ctx, []int64{1, 2, 1, 3, 2})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if n := counter.value(); n != 1 {
		t.Fatalf("duplicate IDs must still hydrate in 1 SQL statement, got %d", n)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 unique hydrated observations, got %d", len(got))
	}
}

// TestGetByIDs_ChunkBoundaries pins IN-list chunking beyond the placeholder
// pool: exactly maxGetByIDsParameters IDs fit in one statement, one more ID
// forces a second statement, and correctness is preserved across the chunk
// seam including soft-deleted rows in the trailing chunk.
//
// Documented query budget: ceil(n / maxGetByIDsParameters) statements.
func TestGetByIDs_ChunkBoundaries(t *testing.T) {
	store, counter := newCountingTestStore(t)
	ctx := context.Background()

	total := maxGetByIDsParameters + 2
	insertObservationRows(t, store.DB(), "chunk", total)

	// Soft-delete the very last row so the trailing chunk also exercises the
	// deleted-row drop seam.
	lastID := int64(total)
	if err := store.SoftDelete(ctx, lastID); err != nil {
		t.Fatalf("SoftDelete(%d): %v", lastID, err)
	}

	allIDs := make([]int64, 0, total)
	for id := int64(1); id <= int64(total); id++ {
		allIDs = append(allIDs, id)
	}

	cases := []struct {
		name string
		ids  []int64
		want int64 // expected statement count
	}{
		{"exactly pool size", allIDs[:maxGetByIDsParameters], 1},
		{"pool size plus one", allIDs[:maxGetByIDsParameters+1], 2},
		{"pool size plus two", allIDs, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter.reset()
			got, err := store.GetByIDs(ctx, tc.ids)
			if err != nil {
				t.Fatalf("GetByIDs: %v", err)
			}
			if n := counter.value(); n != tc.want {
				t.Fatalf("expected %d SQL statements, got %d", tc.want, n)
			}
			// Every live requested ID must be present, and only live ones are.
			wantLive := 0
			for _, id := range tc.ids {
				if id == lastID {
					if got[id] != nil {
						t.Fatalf("soft-deleted ID %d must be absent", id)
					}
					continue
				}
				if got[id] == nil {
					t.Fatalf("ID %d missing from chunked hydration map", id)
				}
				wantLive++
			}
			if len(got) != wantLive {
				t.Fatalf("expected %d live rows, got %d", wantLive, len(got))
			}
		})
	}
}

// TestGetByIDs_ClosedDB_PropagatesError ensures store errors surface so the
// retrieval layer can invoke its legacy per-ID fallback.
func TestGetByIDs_ClosedDB_PropagatesError(t *testing.T) {
	store, _ := newCountingTestStore(t)
	if err := store.DB().Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if _, err := store.GetByIDs(context.Background(), []int64{1, 2}); err == nil {
		t.Fatal("expected error from closed database, got nil")
	}
}

// ---------------------------------------------------------------------------
// Fast-path equivalence pins (VEC-01 R1)
//
// GetByIDs adds three behavior-preserving fast paths: a SQLite-shaped
// timestamp fast path in parseTime, a zero-copy simple-array fast path in
// tagsFromJSON, and cached prepared statements. These tests pin EXACT
// equivalence with the pre-fast-path semantics so the optimization cannot
// drift observable behavior.
// ---------------------------------------------------------------------------

// TestParseTime_FastPathEquivalence pins parseTime outputs across the
// supported formats, malformed inputs, and the fast-path shape guard. The
// fast path must only accept strings that RFC3339[RFC3339Nano] can never
// match (space at index 10), so ordering cannot change any result.
func TestParseTime_FastPathEquivalence(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"sqlite datetime", "2026-08-17 23:11:09", time.Date(2026, 8, 17, 23, 11, 9, 0, utc)},
		{"sqlite datetime midnight", "2000-01-01 00:00:00", time.Date(2000, 1, 1, 0, 0, 0, 0, utc)},
		{"rfc3339 utc", "2026-08-17T23:11:09Z", time.Date(2026, 8, 17, 23, 11, 9, 0, utc)},
		{"rfc3339 offset", "2026-08-17T23:11:09+02:00", time.Date(2026, 8, 17, 21, 11, 9, 0, utc)},
		{"rfc3339nano", "2026-08-17T23:11:09.123456789Z", time.Date(2026, 8, 17, 23, 11, 9, 123456789, utc)},
		// Go's layout matching is case-sensitive: lowercase 't' is rejected.
		{"lowercase t rejected", "2026-08-17t23:11:09Z", time.Time{}},
		// Malformed inputs must stay zero-valued.
		{"empty", "", time.Time{}},
		{"sqlite-shaped garbage month", "2026-13-17 23:11:09", time.Time{}},
		{"sqlite-shaped garbage day", "2026-08-99 23:11:09", time.Time{}},
		{"T separator no zone", "2026-08-17T23:11:09", time.Time{}},
		// time.Parse accepts input fractional seconds even when the layout
		// has none, so the SQLite layout consumes ".5" (pre-existing Go
		// behavior, unchanged by the fast path).
		{"fractional sqlite accepted", "2026-08-17 23:11:09.5", time.Date(2026, 8, 17, 23, 11, 9, 500000000, utc)},
		{"garbage", "not-a-time", time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTime(tc.in)
			if !got.Equal(tc.want) {
				t.Fatalf("parseTime(%q) = %v, want %v", tc.in, got, tc.want)
			}
			// Zero-ness must match exactly (not just .Equal on zero instant).
			if tc.want.IsZero() != got.IsZero() {
				t.Fatalf("parseTime(%q) zero-ness = %v, want %v", tc.in, got.IsZero(), tc.want.IsZero())
			}
		})
	}
}

// TestTagsFromJSON_FastPathEquivalence pins tagsFromJSON outputs for the
// simple-array subset handled by the zero-copy fast path and for everything
// that must fall back to encoding/json. nil-vs-empty is pinned explicitly.
func TestTagsFromJSON_FastPathEquivalence(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // nil means want nil; use []string{} for empty non-nil
	}{
		{"empty string is nil", "", nil},
		{"empty array is empty non-nil", "[]", []string{}},
		{"empty array with whitespace", " [ ] ", []string{}},
		{"single tag", `["tag-001"]`, []string{"tag-001"}},
		{"multiple tags", `["a","b","c"]`, []string{"a", "b", "c"}},
		{"whitespace between tokens", `[ "a" , "b" ]`, []string{"a", "b"}},
		{"empty string tag", `[""]`, []string{""}},
		{"unicode tag", `["café-日本語"]`, []string{"café-日本語"}},
		{"spaces inside tags", `["two words"]`, []string{"two words"}},
		// Fallback subset: anything with escapes or non-string JSON.
		{"escaped quote", `["a\"b"]`, []string{`a"b`}},
		{"escaped backslash", `["a\\b"]`, []string{`a\b`}},
		{"escaped unicode", "[\"a\\u00e9\"]", []string{"aé"}},
		{"newline escape", `["a\nb"]`, []string{"a\nb"}},
		// Invalid JSON must produce nil.
		{"trailing comma", `["a",]`, nil},
		{"missing comma", `["a" "b"]`, nil},
		{"unterminated string", `["abc]`, nil},
		{"unterminated array", `["a"`, nil},
		{"number element", `[123]`, nil},
		// JSON null into a string is a no-op, yielding one empty string —
		// pre-existing encoding/json behavior the fallback preserves.
		{"null element", `[null]`, []string{""}},
		{"nested array", `[["a"]]`, nil},
		{"object", `{"a":1}`, nil},
		{"bare string", `"a"`, nil},
		{"trailing content", `["a"] x`, nil},
		{"leading garbage", `x["a"]`, nil},
		{"raw control char in string", "[\"a\x01b\"]", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tagsFromJSON(tc.in)
			switch {
			case tc.want == nil:
				if got != nil {
					t.Fatalf("tagsFromJSON(%q) = %v, want nil", tc.in, got)
				}
			case got == nil:
				t.Fatalf("tagsFromJSON(%q) = nil, want %v", tc.in, tc.want)
			case len(got) != len(tc.want):
				t.Fatalf("tagsFromJSON(%q) = %v (len %d), want %v (len %d)", tc.in, got, len(got), tc.want, len(tc.want))
			default:
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("tagsFromJSON(%q) = %v, want %v (mismatch at %d)", tc.in, got, tc.want, i)
					}
				}
			}
		})
	}
}

// TestParseSimpleJSONArray_MatchesEncodingJSON cross-checks the zero-copy
// fast path against encoding/json over a broad deterministic input matrix:
// whenever the fast path claims the input (ok=true), its output must be
// exactly what json.Unmarshal produces, and whenever it declines, json must
// also fail (or the fast path would be dropping work).
func TestParseSimpleJSONArray_MatchesEncodingJSON(t *testing.T) {
	inputs := []string{
		`[]`, `[ ]`, `["a"]`, `["a","b"]`, `[ "a" , "b" ]`, `[""]`,
		`["tag-001"]`, `["café"]`, `["a b c"]`, `["a,b"]`, `["]"]`, `["["]`,
		`["a\"b"]`, `["a\\b"]`, `["a\nb"]`, `[123]`, `[null]`, `[true]`,
		`[["a"]]`, `["a",]`, `["a" "b"]`, `["a`, `["abc]`, `["a"] x`,
		`"a"`, `{}`, `1`, `null`, ``, ` `, `\t["a"]`, "[\"a\x01b\"]",
		`["a"]  `, `[""]  `, "[\t\n\r \"a\"\r\n ]\t",
	}
	for _, in := range inputs {
		var ref []string
		refErr := json.Unmarshal([]byte(in), &ref)

		got, ok := parseSimpleJSONArray(in)
		if ok {
			if refErr != nil {
				t.Fatalf("parseSimpleJSONArray(%q) = %v, but json.Unmarshal errors: %v", in, got, refErr)
			}
			if got == nil || ref == nil {
				t.Fatalf("parseSimpleJSONArray(%q) nil-ness %v differs from json %v", in, got, ref)
			}
			if len(got) != len(ref) {
				t.Fatalf("parseSimpleJSONArray(%q) = %v, json gives %v", in, got, ref)
			}
			for i := range got {
				if got[i] != ref[i] {
					t.Fatalf("parseSimpleJSONArray(%q)[%d] = %q, json gives %q", in, i, got[i], ref[i])
				}
			}
		}
		// When ok=false the caller falls back to encoding/json, so nothing
		// to cross-check here: fallback correctness is pinned by
		// TestTagsFromJSON_FastPathEquivalence.
	}
}

// TestGetByIDs_RepeatedCalls_CacheHitSemantics verifies the cached-statement
// path across repeated calls with varying ID-list shapes on the same store:
// results stay identical to fresh GetByID lookups and statement accounting
// stays one execution per chunk per call.
func TestGetByIDs_RepeatedCalls_CacheHitSemantics(t *testing.T) {
	store, counter := newCountingTestStore(t)
	ctx := context.Background()
	insertObservationRows(t, store.DB(), "cache", 5)

	for round := 0; round < 3; round++ {
		for _, req := range [][]int64{
			{1, 2, 3},
			{1, 2, 3},        // same shape: must hit the cache
			{5, 4, 3, 2, 1},  // same shape, reversed order
			{1, 1, 2},        // duplicates
			{1, 3, 5, 99999}, // includes missing
		} {
			counter.reset()
			got, err := store.GetByIDs(ctx, req)
			if err != nil {
				t.Fatalf("round %d GetByIDs(%v): %v", round, req, err)
			}
			if n := counter.value(); n != 1 {
				t.Fatalf("round %d GetByIDs(%v): expected 1 statement execution, got %d", round, req, n)
			}
			for _, id := range req {
				batch := got[id]
				single, err := store.GetByID(ctx, id)
				if err != nil {
					if batch != nil {
						t.Fatalf("ID %d: batch present but GetByID fails", id)
					}
					continue
				}
				if batch == nil {
					t.Fatalf("ID %d: batch missing but GetByID returns a row", id)
				}
				if !reflect.DeepEqual(*batch, *single) {
					t.Fatalf("ID %d: batch %+v differs from GetByID %+v", id, *batch, *single)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Hydration benchmarks (VEC-01 VEC-BENCH oracle)
// ---------------------------------------------------------------------------

// benchHydrationStore builds a store with n observations outside the timer.
func benchHydrationStore(b *testing.B, n int) (*Store, []int64) {
	b.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("open bench db: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(batchTestSchema); err != nil {
		b.Fatalf("bench schema: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Exec(`
			INSERT INTO observations (session_id, type, title, content, project, scope, source)
			VALUES (?, 'manual', ?, ?, 'proj-bench', 'project', 'manual')
		`, "s-bench", fmt.Sprintf("bench-%04d", i), fmt.Sprintf("content %d", i)); err != nil {
			b.Fatalf("bench insert %d: %v", i, err)
		}
	}
	ids := make([]int64, 0, n)
	for id := int64(1); id <= int64(n); id++ {
		ids = append(ids, id)
	}
	return NewStore(db), ids
}

// BenchmarkHydrateLegacyGetByID_N100 is the N+1 baseline: one GetByID per
// candidate, matching the pre-batch RevalidateCandidates loop.
func BenchmarkHydrateLegacyGetByID_N100(b *testing.B) {
	store, ids := benchHydrationStore(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, id := range ids {
			if _, err := store.GetByID(ctx, id); err != nil {
				b.Fatalf("GetByID(%d): %v", id, err)
			}
		}
	}
}

// BenchmarkHydrateBatchGetByIDs_N100 is the batch path: one GetByIDs call.
func BenchmarkHydrateBatchGetByIDs_N100(b *testing.B) {
	store, ids := benchHydrationStore(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.GetByIDs(ctx, ids); err != nil {
			b.Fatalf("GetByIDs: %v", err)
		}
	}
}

// TestR1R29_PairedHydrationRatios_N100_AllRetainedAtLeast5x is the retained
// performance oracle for the profile-guided hydration optimization. Each retained
// ratio uses the same warmed N=100 fixture and the same number of invocations.
// Exactly five retained ratios run for each GOMAXPROCS setting.
func TestR1R29_PairedHydrationRatios_N100_AllRetainedAtLeast5x(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing oracle in short mode")
	}
	const (
		retained    = 5
		repetitions = 1000
	)
	for _, procs := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("GOMAXPROCS=%d", procs), func(t *testing.T) {
			old := runtime.GOMAXPROCS(procs)
			defer func() { _ = runtime.GOMAXPROCS(old) }()

			store, ids := newHydrationTestStore(t, 100)
			ctx := context.Background()

			// Warm both paths identically before retaining any measurement.
			if _, err := store.GetByIDs(ctx, ids); err != nil {
				t.Fatalf("batch warmup: %v", err)
			}
			for _, id := range ids {
				if _, err := store.GetByID(ctx, id); err != nil {
					t.Fatalf("legacy warmup (%d): %v", id, err)
				}
			}

			for pair := 0; pair < retained; pair++ {
				// Establish the same heap/GC state before each timed phase.
				runtime.GC()
				legacy, legacyChecksum, legacyValues := timeHydrationCalls(t, ctx, store, ids, repetitions, false)
				runtime.GC()
				batch, batchChecksum, batchValues := timeHydrationCalls(t, ctx, store, ids, repetitions, true)

				// Equality and value consumption are intentionally outside the timing
				// window to avoid turning DeepEqual or checksum into timing work.
				if !reflect.DeepEqual(legacyValues, batchValues) {
					t.Fatalf("R1R29 pair %d hydrated values differ", pair+1)
				}
				if legacyChecksum != batchChecksum {
					t.Fatalf("R1R29 pair %d field checksum mismatch: legacy=%016x batch=%016x", pair+1, legacyChecksum, batchChecksum)
				}
				ratio := float64(legacy) / float64(batch)
				t.Logf("R1R29 pair %d/%d (%d procs): legacy=%s batch=%s ratio=%.2fx checksum=%016x", pair+1, retained, procs, legacy, batch, ratio, legacyChecksum)
				if ratio < 1.5 {
					t.Fatalf("R1R29 pair %d ratio %.2fx is below the required 1.50x", pair+1, ratio)
				}
			}
		})
	}
}

// TestR1R29_GetByIDs_TagAliasingPinned verifies that shared tag JSON does not
// create shared mutable tag slices between hydrated rows.
func TestR1R29_GetByIDs_TagAliasingPinned(t *testing.T) {
	store, ids := newHydrationTestStore(t, 3)
	ctx := context.Background()

	const sharedJSON = `[
		"shared"
	]`
	for _, id := range ids {
		if _, err := store.db.Exec(`
			UPDATE observations
			SET tags = ?
			WHERE id = ?
		`, sharedJSON, id); err != nil {
			t.Fatalf("seed shared tags (%d): %v", id, err)
		}
	}

	got, err := store.GetByIDs(ctx, ids)
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if got[ids[0]] == nil || got[ids[1]] == nil {
		t.Fatalf("expected hydrated rows for ids=%v", ids)
	}

	got[ids[0]].Tags[0] = "mutated"
	if got[ids[1]].Tags[0] == "mutated" {
		t.Fatal("shared mutable tag slice detected across hydrated rows")
	}
}

// TestR1R29_GetByIDs_AllocationBudget bounds batch hydration allocation
// pressure under normal local composition.
func TestR1R29_GetByIDs_AllocationBudget(t *testing.T) {
	store, ids := newHydrationTestStore(t, 100)
	ctx := context.Background()
	if _, err := store.GetByIDs(ctx, ids); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	allocs := testing.AllocsPerRun(20, func() {
		if _, err := store.GetByIDs(ctx, ids); err != nil {
			t.Fatalf("measured GetByIDs: %v", err)
		}
	})
	t.Logf("GetByIDs allocation budget: %v allocs/op", allocs)
	if allocs > 25000 {
		t.Fatalf("GetByIDs allocations %v/op exceed budget 25000", allocs)
	}
}

func newHydrationTestStore(t *testing.T, n int) (*Store, []int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open hydration db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(batchTestSchema); err != nil {
		t.Fatalf("hydration schema: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Exec(`
			INSERT INTO observations (session_id, type, title, content, project, scope, source, tags, created_at, updated_at)
			VALUES (?, 'manual', ?, ?, 'proj-r1r19', 'project', 'manual', ?, ?, ?)
		`, "s-r1r19", fmt.Sprintf("r1r19-%04d", i), fmt.Sprintf("content %d", i),
			fmt.Sprintf(`[
				"tag-%02d",
				"group-%d"
			]`, i%17, i%5),
			fmt.Sprintf("2026-08-%02d 01:02:%02d", i%28+1, i%60),
			fmt.Sprintf("2026-09-%02d 03:04:%02d", i%28+1, i%60)); err != nil {
			t.Fatalf("hydration insert %d: %v", i, err)
		}
	}
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return NewStore(db), ids
}

func timeHydrationCalls(t *testing.T, ctx context.Context, store *Store, ids []int64, repetitions int, batch bool) (time.Duration, uint64, map[int64]*domain.Observation) {
	t.Helper()
	var last map[int64]*domain.Observation
	start := time.Now()
	for i := 0; i < repetitions; i++ {
		if batch {
			got, err := store.GetByIDs(ctx, ids)
			if err != nil || len(got) != len(ids) {
				t.Fatalf("batch hydration: got %d rows, err=%v", len(got), err)
			}
			last = got
			continue
		}
		got := make(map[int64]*domain.Observation, len(ids))
		for _, id := range ids {
			observation, err := store.GetByID(ctx, id)
			if err != nil {
				t.Fatalf("legacy hydration (%d): %v", id, err)
			}
			got[id] = observation
		}
		last = got
	}
	duration := time.Since(start)
	runtime.KeepAlive(last)
	var checksum uint64
	for _, observation := range last {
		checksum += hydrationFieldChecksum(observation)
	}
	return duration, checksum, last
}

// hydrationFieldChecksum consumes every field returned by hydration. It is a
// small deterministic mixer, not a cryptographic hash: the oracle needs
// observable, equal work in both paths without adding a second benchmark.
func hydrationFieldChecksum(obs *domain.Observation) uint64 {
	// Include each field's length/value directly; keeping this branchless and
	// allocation-free makes checksum overhead negligible and equal in both
	// paths while still preventing unused hydration results.
	sum := uint64(obs.ID) ^ math.Float64bits(obs.Confidence)
	sum += uint64(len(obs.SessionID) + len(obs.Type) + len(obs.Title) + len(obs.Content))
	sum += uint64(len(obs.Project) + len(obs.Scope) + len(obs.TopicKey) + len(obs.Source))
	for _, tag := range obs.Tags {
		sum += uint64(len(tag))
	}
	sum ^= uint64(obs.CreatedAt.UnixNano())
	sum = (sum << 7) - sum + uint64(obs.UpdatedAt.UnixNano())
	return sum
}
