package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func TestStore_ImportExport_Privacy(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS user_prompts (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, content TEXT NOT NULL, project TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')) )`)

	data := &ExportData{
		Sessions:     []*domain.Session{{ID: "s-imp", Project: "cortex", Directory: "/app", Summary: "Sess <private>c1</private>"}},
		Observations: []*domain.Observation{{SessionID: "s-imp", Type: domain.TypeManual, Title: "Obs <private>c2</private>", Content: "Data <private>c3</private> note", Project: "cortex", Scope: "project"}},
		Prompts:      []*domain.Prompt{{SessionID: "s-imp", Project: "cortex", Content: "Query <private>c4</private>"}},
	}
	origObs := data.Observations[0].Content
	res, err := store.ImportData(ctx, data)
	if err != nil || res.ObservationsImported != 1 || data.Observations[0].Content != origObs {
		t.Fatalf("ImportData failed or mutated: (%+v, %v)", res, err)
	}

	var title, content, dbHash, sum, prompt string
	_ = db.QueryRow("SELECT title, content, normalized_hash FROM observations WHERE session_id='s-imp'").Scan(&title, &content, &dbHash)
	_ = db.QueryRow("SELECT summary FROM sessions WHERE id='s-imp'").Scan(&sum)
	_ = db.QueryRow("SELECT content FROM user_prompts WHERE session_id='s-imp'").Scan(&prompt)
	if title != "Obs [REDACTED]" || content != "Data [REDACTED] note" || dbHash != hashNormalized("Data [REDACTED] note") || sum != "Sess [REDACTED]" || prompt != "Query [REDACTED]" {
		t.Fatalf("persisted mismatch: title=%q, content=%q, hash=%q, sum=%q, prompt=%q", title, content, dbHash, sum, prompt)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"c3\"'"); count != 0 {
		t.Fatalf("canary in FTS: %d", count)
	}

	exp, err := store.ExportAll(ctx)
	if err != nil || exp.Observations[0].Content != "Data [REDACTED] note" || exp.Sessions[0].Summary != "Sess [REDACTED]" {
		t.Fatalf("ExportAll mismatch: %+v", exp)
	}
}

func TestStore_Import_PrivacyAtomicRejectionZeroEffects(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	data := &ExportData{
		Sessions: []*domain.Session{{ID: "s", Project: "cortex", Directory: "/app"}},
		Observations: []*domain.Observation{
			{SessionID: "s", Type: domain.TypeManual, Title: "V", Content: "Good <private>c</private>", Project: "cortex", Scope: "project"},
			{SessionID: "s", Type: domain.TypeManual, Title: "B", Content: "Bad <private>unclosed", Project: "cortex", Scope: "project"},
		},
	}
	orig0, orig1 := data.Observations[0].Content, data.Observations[1].Content
	if _, err := store.ImportData(ctx, data); err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker, got %v", err)
	}
	if data.Observations[0].Content != orig0 || data.Observations[1].Content != orig1 {
		t.Fatal("source mutated on rejection")
	}
	if countRows(t, db, "SELECT count(*) FROM observations") != 0 || countRows(t, db, "SELECT count(*) FROM sessions") != 0 {
		t.Fatal("rows persisted on rejected import")
	}
}

func TestStore_Import_PrivacyPostPreflightTxFailureRollback(t *testing.T) {
	t.Run("observation_write_fault", testStoreImportObservationWriteFault)
	t.Run("prompt_write_fault", testStoreImportPromptWriteFault)
}