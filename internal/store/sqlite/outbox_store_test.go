package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"

	_ "modernc.org/sqlite"
)

// setupOutboxDB creates an in-memory SQLite database with the index_outbox and
// index_state tables (mirrors migrations/v2/001_init.sql §11). Returns the DB
// and an OutboxStore backed by it.
func setupOutboxDB(t *testing.T) (*sql.DB, *OutboxStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, content TEXT NOT NULL)`,
		`CREATE TABLE index_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			observation_id INTEGER NOT NULL,
			intent TEXT NOT NULL,
			model_info TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 5,
			next_retry_at TEXT,
			leased_at TEXT,
			completed_at TEXT,
			error TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX idx_outbox_pending ON index_outbox(status, next_retry_at) WHERE status = 'pending'`,
		`CREATE TABLE index_state (namespace TEXT PRIMARY KEY, coverage REAL NOT NULL, parity INTEGER DEFAULT 0, authority_digest TEXT, updated_at TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	store := NewOutboxStore(db)
	return db, store
}

// insertOutboxRow inserts a raw row into index_outbox for test setup, bypassing
// the store methods so tests can assert store behavior against known fixtures.
func insertOutboxRow(t *testing.T, db *sql.DB, obsID int64, status string, attempts, maxAttempts int, nextRetryAt *string) int64 {
	t.Helper()
	var nextRetry sql.NullString
	if nextRetryAt != nil {
		nextRetry = sql.NullString{String: *nextRetryAt, Valid: true}
	}
	res, err := db.Exec(
		`INSERT INTO index_outbox (observation_id, intent, status, attempts, max_attempts, next_retry_at) VALUES (?, 'embed_upsert', ?, ?, ?, ?)`,
		obsID, status, attempts, maxAttempts, nextRetry,
	)
	if err != nil {
		t.Fatalf("insert outbox fixture: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func assertOutboxStatus(t *testing.T, db *sql.DB, id int64, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`SELECT status FROM index_outbox WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("query status for id %d: %v", id, err)
	}
	if got != want {
		t.Fatalf("outbox id %d status = %q, want %q", id, got, want)
	}
}

// withTestTx runs fn inside a manually-managed *sql.Tx stashed under the same
// txKey used by Store.WithinTx / OutboxStore.WithinTx. This mirrors the UoW
// enlistment path without importing the bundle package (which would create an
// import cycle in this white-box test).
func withTestTx(t *testing.T, db *sql.DB, fn func(ctx context.Context) error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	ctx := context.WithValue(context.Background(), txKey{}, tx)
	if err := fn(ctx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("fn within tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// --- EnqueueInTx ---

func TestOutboxStore_EnqueueInTx_InsertsPendingIntent(t *testing.T) {
	db, store := setupOutboxDB(t)
	withTestTx(t, db, func(ctx context.Context) error {
		return store.EnqueueInTx(ctx, 42, "embed_upsert", "nomic-embed-text:768")
	})

	var obsID int64
	var intent, modelInfo, status string
	err := db.QueryRow(`SELECT observation_id, intent, model_info, status FROM index_outbox WHERE observation_id = 42`).Scan(&obsID, &intent, &modelInfo, &status)
	if err != nil {
		t.Fatalf("query intent: %v", err)
	}
	if obsID != 42 || intent != "embed_upsert" || modelInfo != "nomic-embed-text:768" || status != "pending" {
		t.Fatalf("intent row = (%d, %q, %q, %q), want (42, embed_upsert, nomic-embed-text:768, pending)", obsID, intent, modelInfo, status)
	}
}

func TestOutboxStore_EnqueueInTx_RequiresActiveTx(t *testing.T) {
	_, store := setupOutboxDB(t)
	err := store.EnqueueInTx(context.Background(), 1, "embed_upsert", "")
	if err == nil {
		t.Fatal("expected error when EnqueueInTx called outside a shared transaction")
	}
}

func TestOutboxStore_EnqueueInTx_RollbackWithTx(t *testing.T) {
	db, store := setupOutboxDB(t)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	ctx := context.WithValue(context.Background(), txKey{}, tx)
	if err := store.EnqueueInTx(ctx, 99, "embed_upsert", ""); err != nil {
		t.Fatalf("EnqueueInTx: %v", err)
	}
	// Simulate downstream failure → rollback.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	_ = db.QueryRow(`SELECT count(*) FROM index_outbox WHERE observation_id = 99`).Scan(&count)
	if count != 0 {
		t.Fatalf("outbox intent survived rollback: count=%d, want 0", count)
	}
}

// --- WithinTx (TxParticipant) ---

func TestOutboxStore_WithinTx_ThreadsSharedTx(t *testing.T) {
	db, store := setupOutboxDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	// WithinTx stashes the *sql.Tx so EnqueueInTx reads it via txFromContext.
	if err := store.WithinTx(context.Background(), tx, func(ctx context.Context) error {
		return store.EnqueueInTx(ctx, 7, "embed_upsert", "test-model")
	}); err != nil {
		t.Fatalf("WithinTx+EnqueueInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var status string
	_ = db.QueryRow(`SELECT status FROM index_outbox WHERE observation_id = 7`).Scan(&status)
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
}

func TestOutboxStore_WithinTx_RejectsInvalidHandle(t *testing.T) {
	_, store := setupOutboxDB(t)
	err := store.WithinTx(context.Background(), "not-a-tx", func(ctx context.Context) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for invalid handle type")
	}
}

func TestOutboxStore_ImplementsTxParticipant(t *testing.T) {
	var _ domain.TxParticipant = (*OutboxStore)(nil)
}

// --- Lease ---

func TestOutboxStore_Lease_ClaimsPendingIntents(t *testing.T) {
	db, store := setupOutboxDB(t)
	id1 := insertOutboxRow(t, db, 1, "pending", 0, 5, nil)
	id2 := insertOutboxRow(t, db, 2, "pending", 0, 5, nil)

	intents, err := store.Lease(context.Background(), 10)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if len(intents) != 2 {
		t.Fatalf("leased %d intents, want 2", len(intents))
	}

	for _, in := range intents {
		assertOutboxStatus(t, db, in.ID, "leased")
		if in.Attempts != 1 {
			t.Fatalf("intent %d attempts = %d, want 1 (incremented on lease)", in.ID, in.Attempts)
		}
	}
	seen := map[int64]bool{id1: false, id2: false}
	for _, in := range intents {
		seen[in.ID] = true
	}
	for id, ok := range seen {
		if !ok {
			t.Fatalf("intent %d was not leased", id)
		}
	}
}

func TestOutboxStore_Lease_RespectsNextRetryAt(t *testing.T) {
	db, store := setupOutboxDB(t)
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339Nano)
	insertOutboxRow(t, db, 1, "pending", 1, 5, &future)
	id2 := insertOutboxRow(t, db, 2, "pending", 0, 5, nil)

	intents, err := store.Lease(context.Background(), 10)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if len(intents) != 1 {
		t.Fatalf("leased %d intents, want 1 (future-retry excluded)", len(intents))
	}
	if intents[0].ID != id2 {
		t.Fatalf("leased wrong intent: got %d, want %d", intents[0].ID, id2)
	}
}

func TestOutboxStore_Lease_LimitsBatchSize(t *testing.T) {
	db, store := setupOutboxDB(t)
	for i := 1; i <= 5; i++ {
		insertOutboxRow(t, db, int64(i), "pending", 0, 5, nil)
	}
	intents, err := store.Lease(context.Background(), 2)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if len(intents) != 2 {
		t.Fatalf("leased %d, want 2 (batch limit)", len(intents))
	}
}

func TestOutboxStore_Lease_SkipsNonPending(t *testing.T) {
	db, store := setupOutboxDB(t)
	insertOutboxRow(t, db, 1, "leased", 1, 5, nil)
	insertOutboxRow(t, db, 2, "complete", 1, 5, nil)
	id3 := insertOutboxRow(t, db, 3, "pending", 0, 5, nil)

	intents, err := store.Lease(context.Background(), 10)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if len(intents) != 1 || intents[0].ID != id3 {
		t.Fatalf("leased = %v, want only id %d", intents, id3)
	}
}

func TestOutboxStore_Lease_EmptyWhenNothingPending(t *testing.T) {
	_, store := setupOutboxDB(t)
	intents, err := store.Lease(context.Background(), 10)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if len(intents) != 0 {
		t.Fatalf("leased %d, want 0", len(intents))
	}
}

// --- MarkComplete ---

func TestOutboxStore_MarkComplete(t *testing.T) {
	db, store := setupOutboxDB(t)
	id := insertOutboxRow(t, db, 1, "leased", 1, 5, nil)

	if err := store.MarkComplete(context.Background(), id); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	assertOutboxStatus(t, db, id, "complete")

	var completedAt sql.NullString
	_ = db.QueryRow(`SELECT completed_at FROM index_outbox WHERE id = ?`, id).Scan(&completedAt)
	if !completedAt.Valid || completedAt.String == "" {
		t.Fatal("completed_at not set")
	}
}

// --- MarkFailed ---

func TestOutboxStore_MarkFailed_RetriesWhenUnderCap(t *testing.T) {
	db, store := setupOutboxDB(t)
	id := insertOutboxRow(t, db, 1, "leased", 1, 5, nil)

	retryAt := time.Now().Add(2 * time.Second)
	if err := store.MarkFailed(context.Background(), id, fmt.Errorf("transient"), retryAt); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	assertOutboxStatus(t, db, id, "pending")

	var nextRetry sql.NullString
	var errMsg sql.NullString
	_ = db.QueryRow(`SELECT next_retry_at, error FROM index_outbox WHERE id = ?`, id).Scan(&nextRetry, &errMsg)
	if !nextRetry.Valid {
		t.Fatal("next_retry_at not set for retry")
	}
	if !errMsg.Valid || !strings.Contains(errMsg.String, "transient") {
		t.Fatalf("error not stored: %q", errMsg.String)
	}
}

func TestOutboxStore_MarkFailed_DeadLettersAtCap(t *testing.T) {
	db, store := setupOutboxDB(t)
	id := insertOutboxRow(t, db, 1, "leased", 5, 5, nil)

	retryAt := time.Now().Add(2 * time.Second)
	if err := store.MarkFailed(context.Background(), id, fmt.Errorf("persistent"), retryAt); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	assertOutboxStatus(t, db, id, "dead_letter")

	var errMsg sql.NullString
	_ = db.QueryRow(`SELECT error FROM index_outbox WHERE id = ?`, id).Scan(&errMsg)
	if !errMsg.Valid || !strings.Contains(errMsg.String, "persistent") {
		t.Fatalf("error cause not stored: %q", errMsg.String)
	}
}

// --- DeadLetter (explicit terminal) ---

func TestOutboxStore_DeadLetter(t *testing.T) {
	db, store := setupOutboxDB(t)
	id := insertOutboxRow(t, db, 1, "leased", 1, 5, nil)

	if err := store.DeadLetter(context.Background(), id, fmt.Errorf("model not found")); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	assertOutboxStatus(t, db, id, "dead_letter")

	var errMsg sql.NullString
	_ = db.QueryRow(`SELECT error FROM index_outbox WHERE id = ?`, id).Scan(&errMsg)
	if !errMsg.Valid || !strings.Contains(errMsg.String, "model not found") {
		t.Fatalf("error cause not stored: %q", errMsg.String)
	}
}

// --- PendingCount ---

func TestOutboxStore_PendingCount(t *testing.T) {
	db, store := setupOutboxDB(t)
	insertOutboxRow(t, db, 1, "pending", 0, 5, nil)
	insertOutboxRow(t, db, 2, "pending", 0, 5, nil)
	insertOutboxRow(t, db, 3, "leased", 1, 5, nil)
	insertOutboxRow(t, db, 4, "complete", 1, 5, nil)

	count, err := store.PendingCount(context.Background())
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	// PendingCount counts all non-terminal work (pending + leased) — backlog pressure.
	if count != 3 {
		t.Fatalf("PendingCount = %d, want 3", count)
	}
}

// --- RecoverPending (crash recovery) ---

func TestOutboxStore_RecoverPending_ResetsLeasedToPending(t *testing.T) {
	db, store := setupOutboxDB(t)
	idLeased := insertOutboxRow(t, db, 1, "leased", 1, 5, nil)
	idPending := insertOutboxRow(t, db, 2, "pending", 0, 5, nil)
	idComplete := insertOutboxRow(t, db, 3, "complete", 1, 5, nil)

	if err := store.RecoverPending(context.Background()); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}

	assertOutboxStatus(t, db, idLeased, "pending")
	assertOutboxStatus(t, db, idPending, "pending")
	assertOutboxStatus(t, db, idComplete, "complete")
}

// --- index_state namespace tracking ---

func TestOutboxStore_UpdateIndexState(t *testing.T) {
	_, store := setupOutboxDB(t)
	ns := "nomic-embed-text:768"
	if err := store.UpdateIndexState(context.Background(), ns, 0.85, 1); err != nil {
		t.Fatalf("UpdateIndexState: %v", err)
	}

	var coverage float64
	var parity int
	err := store.db.QueryRow(`SELECT coverage, parity FROM index_state WHERE namespace = ?`, ns).Scan(&coverage, &parity)
	if err != nil {
		t.Fatalf("query index_state: %v", err)
	}
	if coverage != 0.85 || parity != 1 {
		t.Fatalf("index_state = (%v, %d), want (0.85, 1)", coverage, parity)
	}

	// Upsert (second call updates).
	if err := store.UpdateIndexState(context.Background(), ns, 0.90, 1); err != nil {
		t.Fatalf("UpdateIndexState upsert: %v", err)
	}
	_ = store.db.QueryRow(`SELECT coverage FROM index_state WHERE namespace = ?`, ns).Scan(&coverage)
	if coverage != 0.90 {
		t.Fatalf("coverage after upsert = %v, want 0.90", coverage)
	}
}
