//go:build cortex_vectors

// Package sqlite_blob: REQ-VEC-FILTER-001 end-to-end cardinality switch.
//
// These tests run only under the cortex_vectors tag because the plan needs a
// live SQLite corpus. The schema is the shared conformance schema plus the
// observations.source column so the pushed-down source filter has a target.
package sqlite_blob

import (
	"context"
	"math/rand"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/testutil"
)

const filterDim = 64

type filterFixture struct {
	adapter *Adapter
	query   []float32
	rare    map[int64]bool
	common  map[int64]bool
}

// seedFilterCorpus builds 5 rare rows (type gotchas, source import) among 60
// common rows (type decision, source manual). Every vector sits close to the
// shared base query so all similarities clear the default zero threshold and
// a missing candidate cannot hide behind threshold filtering.
func seedFilterCorpus(t *testing.T) filterFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	db.MustExec(conformanceSchemaSQL)
	db.MustExec(`ALTER TABLE observations ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'`)
	db.MustExec(`INSERT INTO sessions (id, project, directory) VALUES ('s-filter', 'p1', '/filter')`)

	a := New(db.DB())
	ctx := context.Background()
	model := domain.ModelInfo{Name: "filter-model", Dimension: filterDim}
	rng := rand.New(rand.NewSource(20261002))
	base := make([]float32, filterDim)
	for i := range base {
		base[i] = float32(rng.NormFloat64())
	}

	fixture := filterFixture{
		adapter: a,
		query:   base,
		rare:    make(map[int64]bool),
		common:  make(map[int64]bool),
	}
	insert := func(typ, source string, rare bool) {
		t.Helper()
		vec := make([]float32, filterDim)
		for i := range vec {
			vec[i] = base[i] + float32(rng.NormFloat64())*0.05
		}
		res, err := db.DB().Exec(`INSERT INTO observations (session_id, type, title, content, project, scope, source)
			VALUES ('s-filter', ?, ?, ?, 'p1', 'project', ?)`, typ, typ+"-row", "content", source)
		if err != nil {
			t.Fatalf("seed observation: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last id: %v", err)
		}
		if err := a.Upsert(ctx, []domain.VectorPoint{{ID: id, Vector: vec, ModelInfo: model}}); err != nil {
			t.Fatalf("upsert %d: %v", id, err)
		}
		if rare {
			fixture.rare[id] = true
		} else {
			fixture.common[id] = true
		}
	}
	for i := 0; i < 5; i++ {
		insert("gotchas", "import", true)
	}
	for i := 0; i < 60; i++ {
		insert("decision", "manual", false)
	}
	return fixture
}

// TestAdapter_FilteredSearch_SelectiveFilterReturnsFullTopK pins the
// REQ-VEC-FILTER-001 happy path: a filter matching 5 of 65 rows (well under
// the exact selectivity bound) must fill the entire requested top-k from the
// matching set, never from post-filtered leftovers.
func TestAdapter_FilteredSearch_SelectiveFilterReturnsFullTopK(t *testing.T) {
	fixture := seedFilterCorpus(t)
	ctx := context.Background()

	q := domain.VectorQuery{
		Vector:  fixture.query,
		Limit:   5,
		Filters: map[string]any{"type": "gotchas"},
	}
	got, err := fixture.adapter.Search(ctx, q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got == nil {
		t.Fatal("Search returned nil for a populated filter match")
	}
	if len(got) != 5 {
		t.Fatalf("got %d candidates, want full top-k 5", len(got))
	}
	for _, c := range got {
		if !fixture.rare[c.ID] {
			t.Errorf("candidate %d does not match the type filter", c.ID)
		}
	}

	// Fewer matches than the limit fills what exists, still non-nil.
	partial, err := fixture.adapter.Search(ctx, domain.VectorQuery{
		Vector:  fixture.query,
		Limit:   20,
		Filters: map[string]any{"type": "gotchas"},
	})
	if err != nil {
		t.Fatalf("Search partial: %v", err)
	}
	if len(partial) != 5 {
		t.Errorf("got %d candidates for limit 20, want all 5 matches", len(partial))
	}

	// Combined type+source pushdown keeps the common set intact too.
	combined, err := fixture.adapter.Search(ctx, domain.VectorQuery{
		Vector:  fixture.query,
		Limit:   10,
		Filters: map[string]any{"type": "decision", "source": "manual"},
	})
	if err != nil {
		t.Fatalf("Search combined: %v", err)
	}
	if len(combined) != 10 {
		t.Fatalf("got %d candidates, want full top-k 10", len(combined))
	}
	for _, c := range combined {
		if !fixture.common[c.ID] {
			t.Errorf("candidate %d does not match the type+source filter", c.ID)
		}
	}
}

// TestAdapter_FilteredSearch_ZeroCandidatesReturnsEmpty pins the error-state
// scenario: a filter matching nothing returns a non-nil empty result with a
// nil error, deterministically — never an ambiguous starvation artifact.
func TestAdapter_FilteredSearch_ZeroCandidatesReturnsEmpty(t *testing.T) {
	fixture := seedFilterCorpus(t)
	ctx := context.Background()

	filters := []map[string]any{
		{"type": "no-such-type"},
		{"source": "no-such-source"},
		{"project": "no-such-project"},
		{"type": "decision", "source": "ghost"},
	}
	for i, f := range filters {
		for run := 0; run < 2; run++ {
			got, err := fixture.adapter.Search(ctx, domain.VectorQuery{
				Vector:  fixture.query,
				Limit:   10,
				Filters: f,
			})
			if err != nil {
				t.Fatalf("filter %d run %d: Search: %v", i, run, err)
			}
			if got == nil {
				t.Fatalf("filter %d run %d: nil result slice, want non-nil empty", i, run)
			}
			if len(got) != 0 {
				t.Fatalf("filter %d run %d: got %d candidates, want 0", i, run, len(got))
			}
		}
	}
}
