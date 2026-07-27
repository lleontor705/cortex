package bundle_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/embedding"
	"github.com/lleontor705/cortex/internal/store/bundle"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"github.com/lleontor705/cortex/internal/vector/sqlite_blob"

	_ "modernc.org/sqlite"
)

// stubEmbeddingService is a minimal embedding.Service for save-path tests.
type stubEmbeddingService struct {
	model string
	dims  int
}

func (s *stubEmbeddingService) Embed(_ context.Context, _ string) ([]float32, error) {
	vec := make([]float32, s.dims)
	return vec, nil
}
func (s *stubEmbeddingService) Dimensions() int { return s.dims }
func (s *stubEmbeddingService) Model() string   { return s.model }

// setupEmbedIntentDB creates a DB with the full observations + index_outbox
// schema needed by Store.Save/SaveInTx and OutboxStore.
func setupEmbedIntentDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT, type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL,
			project TEXT NOT NULL DEFAULT 'default', scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT, normalized_hash TEXT,
			confidence REAL DEFAULT 1.0, source TEXT DEFAULT 'manual',
			tags TEXT, revision_count INTEGER DEFAULT 1, duplicate_count INTEGER DEFAULT 1,
			last_seen_at TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT
		)`,
		`CREATE TABLE index_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			observation_id INTEGER NOT NULL,
			intent TEXT NOT NULL, model_info TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 5,
			next_retry_at TEXT, leased_at TEXT, completed_at TEXT, error TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX idx_outbox_pending ON index_outbox(status, next_retry_at) WHERE status = 'pending'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db
}

// buildEmbedStores creates a Stores with observation store, outbox, UoW, and a
// stub embedding service wired. VectorStore is NOT needed for the save path
// (the save path only enqueues; the worker does the actual embed+upsert).
func buildEmbedStores(db *sql.DB) *bundle.Stores {
	return &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Outbox:       sqlitestore.NewOutboxStore(db),
		UnitOfWork:   bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig()),
		Embeddings:   &stubEmbeddingService{model: "test-model", dims: 768},
	}
}

func newTestObs() *domain.Observation {
	return &domain.Observation{
		Title:   "save path test",
		Content: "content for embed intent",
		Type:    domain.TypeManual,
		Project: "test",
		Scope:   "project",
	}
}

func outboxIntentCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM index_outbox`).Scan(&count); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return count
}

func outboxIntentForObs(t *testing.T, db *sql.DB, obsID int64) (status, modelInfo string) {
	t.Helper()
	err := db.QueryRow(`SELECT status, model_info FROM index_outbox WHERE observation_id = ?`, obsID).Scan(&status, &modelInfo)
	if err != nil {
		t.Fatalf("query outbox for obs %d: %v", obsID, err)
	}
	return
}

// --- SaveWithEmbedIntent: outbox path ---

func TestSaveWithEmbedIntent_EnqueuesOutboxAtomically(t *testing.T) {
	db := setupEmbedIntentDB(t)
	stores := buildEmbedStores(db)
	ctx := context.Background()

	obs := newTestObs()
	if err := bundle.SaveWithEmbedIntent(ctx, stores, obs); err != nil {
		t.Fatalf("SaveWithEmbedIntent: %v", err)
	}

	if obs.ID == 0 {
		t.Fatal("observation ID not populated after save")
	}

	// Observation exists.
	var title string
	_ = db.QueryRow(`SELECT title FROM observations WHERE id = ?`, obs.ID).Scan(&title)
	if title != "save path test" {
		t.Fatalf("observation title = %q, want 'save path test'", title)
	}

	// Outbox intent exists and is pending.
	if count := outboxIntentCount(t, db); count != 1 {
		t.Fatalf("outbox intent count = %d, want 1", count)
	}
	status, modelInfo := outboxIntentForObs(t, db, obs.ID)
	if status != "pending" {
		t.Fatalf("outbox status = %q, want pending", status)
	}
	if modelInfo != "test-model" {
		t.Fatalf("model_info = %q, want test-model", modelInfo)
	}
}

// --- SaveWithEmbedIntent: zero-embedding path unchanged ---

func TestSaveWithEmbedIntent_ZeroEmbedding_StandaloneSaveNoOutbox(t *testing.T) {
	db := setupEmbedIntentDB(t)
	// Stores WITHOUT outbox, UoW, or embeddings — zero-embedding path.
	stores := &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
	}
	ctx := context.Background()

	obs := newTestObs()
	if err := bundle.SaveWithEmbedIntent(ctx, stores, obs); err != nil {
		t.Fatalf("SaveWithEmbedIntent: %v", err)
	}
	if obs.ID == 0 {
		t.Fatal("observation ID not populated")
	}
	// No outbox activity.
	if count := outboxIntentCount(t, db); count != 0 {
		t.Fatalf("zero-embedding path created %d outbox intents, want 0", count)
	}
}

// --- SaveWithEmbedIntent: outbox nil but embeddings present → standalone ---

func TestSaveWithEmbedIntent_OutboxNil_StandaloneSave(t *testing.T) {
	db := setupEmbedIntentDB(t)
	stores := &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Embeddings:   &stubEmbeddingService{model: "m", dims: 768},
		// Outbox and UnitOfWork are nil.
	}
	ctx := context.Background()

	obs := newTestObs()
	if err := bundle.SaveWithEmbedIntent(ctx, stores, obs); err != nil {
		t.Fatalf("SaveWithEmbedIntent: %v", err)
	}
	if count := outboxIntentCount(t, db); count != 0 {
		t.Fatalf("expected 0 outbox intents when outbox is nil, got %d", count)
	}
}

// --- SaveWithEmbedIntent: saturation fail-closed (authoritative = worker) ---

// TestSaveWithEmbedIntent_SaturationFailClosed_ViaWorker proves the save path
// consults the worker's authoritative saturation state (Worker.IsSaturated,
// backed by WorkerConfig.MaxBacklog) — NOT a duplicated bundle-side threshold.
// With the worker reporting saturation, the save fails-closed: the transaction
// rolls back and no observation or intent is persisted.
func TestSaveWithEmbedIntent_SaturationFailClosed_ViaWorker(t *testing.T) {
	db := setupEmbedIntentDB(t)
	stores := buildEmbedStores(db)
	// Authoritative saturation source: the worker's config (MaxBacklog=2),
	// consulted via Worker.IsSaturated by SaveWithEmbedIntent. Note: stores
	// carries NO bundle-side backlog field — the worker is the single source.
	stores.Worker = embedding.NewWorker(
		stores.Outbox,
		stores.Observations,
		stores.Embeddings,
		sqlite_blob.New(db),
		embedding.WorkerConfig{MaxBacklog: 2},
	)

	// Fill the outbox with 3 pending intents (exceeds MaxBacklog=2).
	for i := int64(1); i <= 3; i++ {
		if _, err := db.Exec(
			`INSERT INTO index_outbox (observation_id, intent, status) VALUES (?, 'embed_upsert', 'pending')`,
			i,
		); err != nil {
			t.Fatalf("fill outbox: %v", err)
		}
	}

	ctx := context.Background()
	obs := newTestObs()
	err := bundle.SaveWithEmbedIntent(ctx, stores, obs)
	if err == nil {
		t.Fatal("expected saturation error, got nil")
	}

	// Observation must NOT be saved (tx rolled back).
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM observations`).Scan(&count)
	if count != 0 {
		t.Fatalf("observation saved despite saturation: count=%d", count)
	}
}

// TestSaveWithEmbedIntent_NoWorker_NoSaturationCheck_Proceeds proves
// zero-worker behavior is preserved: when the authoritative worker is absent
// (Worker == nil) but the outbox + UnitOfWork are wired, the save proceeds
// WITHOUT a saturation check. In production the worker and outbox are always
// paired, so this path is test-only wiring — but it must remain a clean
// standalone outbox save, never a fail-closed on a missing worker.
func TestSaveWithEmbedIntent_NoWorker_NoSaturationCheck_Proceeds(t *testing.T) {
	db := setupEmbedIntentDB(t)
	stores := buildEmbedStores(db) // Worker intentionally nil

	ctx := context.Background()
	obs := newTestObs()
	if err := bundle.SaveWithEmbedIntent(ctx, stores, obs); err != nil {
		t.Fatalf("SaveWithEmbedIntent with no worker: %v", err)
	}
	if obs.ID == 0 {
		t.Fatal("observation ID not populated")
	}
	if count := outboxIntentCount(t, db); count != 1 {
		t.Fatalf("expected 1 outbox intent (no worker, no saturation gate), got %d", count)
	}
}

// --- SaveWithEmbedIntent: rollback on outbox failure (atomicity) ---

func TestSaveWithEmbedIntent_Atomicity_OutboxFailureRollsBackObs(t *testing.T) {
	db := setupEmbedIntentDB(t)
	stores := buildEmbedStores(db)
	ctx := context.Background()

	// First save succeeds (creates obs with ID 1 + outbox intent).
	obs1 := newTestObs()
	if err := bundle.SaveWithEmbedIntent(ctx, stores, obs1); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Drop the index_outbox table to force EnqueueInTx to fail mid-transaction.
	if _, err := db.Exec(`DROP TABLE index_outbox`); err != nil {
		t.Fatalf("drop outbox table: %v", err)
	}

	// Second save should fail AND the observation must NOT persist (atomic rollback).
	obs2 := &domain.Observation{
		Title: "should rollback", Content: "content", Type: domain.TypeManual,
		Project: "test", Scope: "project",
	}
	err := bundle.SaveWithEmbedIntent(ctx, stores, obs2)
	if err == nil {
		t.Fatal("expected error when outbox table is missing, got nil")
	}

	// Only obs1 should exist.
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM observations WHERE title = 'should rollback'`).Scan(&count)
	if count != 0 {
		t.Fatalf("rolled-back observation persisted: count=%d", count)
	}
}

// --- Compile-time: stubEmbeddingService satisfies embedding.Service ---

func TestStubEmbeddingService_ImplementsService(t *testing.T) {
	var _ embedding.Service = (*stubEmbeddingService)(nil)
	var _ = fmt.Sprintf
}

// --- W4.2: Bundle.Stores exposes the worker handle (REQ-EMB-001 lifecycle) ---

// TestStores_WorkerField_HoldsHandle proves the Stores bundle exposes the
// embedding worker handle (W4.2). A Stores constructed with a Worker field set
// retains that reference, making the worker accessible through the composition
// root for status/health checks and future waves — not buried in App internals.
func TestStores_WorkerField_HoldsHandle(t *testing.T) {
	db := setupEmbedIntentDB(t)
	worker := embedding.NewWorker(
		sqlitestore.NewOutboxStore(db),
		sqlitestore.NewStore(db),
		&stubEmbeddingService{model: "test-model", dims: 768},
		sqlite_blob.New(db),
		embedding.WorkerConfig{},
	)
	stores := &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Outbox:       sqlitestore.NewOutboxStore(db),
		Worker:       worker,
	}
	if stores.Worker == nil {
		t.Fatal("Stores.Worker is nil; expected the constructed worker handle")
	}
	// The handle must be the exact pointer we assigned (identity, not a copy).
	if stores.Worker != worker {
		t.Fatal("Stores.Worker is not the same pointer assigned")
	}
}

// TestStores_WorkerField_NilByDefault proves Stores.Worker is nil when not set
// (zero-embedding / unwired mode). The local save path must remain unchanged.
func TestStores_WorkerField_NilByDefault(t *testing.T) {
	stores := &bundle.Stores{}
	if stores.Worker != nil {
		t.Fatal("Stores.Worker should be nil by default")
	}
}
