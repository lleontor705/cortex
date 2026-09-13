package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func testStoreImportObservationWriteFault(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_prompts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		content TEXT NOT NULL,
		project TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		t.Fatalf("create user_prompts table failed: %v", err)
	}

	// Seed prior database state to verify prior data remains intact after rollback.
	if _, err := db.Exec(`INSERT INTO sessions(id, project, directory, summary) VALUES ('s-prior', 'cortex', '/app', 'Prior Session')`); err != nil {
		t.Fatalf("insert prior session failed: %v", err)
	}
	priorObs := &domain.Observation{
		SessionID: "s-prior",
		Type:      domain.TypeManual,
		Title:     "Prior Title",
		Content:   "Prior Content",
		Project:   "cortex",
		Scope:     "project",
	}
	if err := store.Save(ctx, priorObs); err != nil {
		t.Fatalf("Save prior observation failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_prompts(session_id, content, project) VALUES ('s-prior', 'Prior Prompt', 'cortex')`); err != nil {
		t.Fatalf("insert prior prompt failed: %v", err)
	}

	if countRows(t, db, "SELECT count(*) FROM sessions") != 1 ||
		countRows(t, db, "SELECT count(*) FROM observations") != 1 ||
		countRows(t, db, "SELECT count(*) FROM user_prompts") != 1 ||
		countRows(t, db, "SELECT count(*) FROM observations_fts") != 1 {
		t.Fatalf("unexpected prior state row counts")
	}

	const (
		canaryTitle   = "canary_tx_title_9182"
		canaryBody    = "canary_tx_body_3847"
		canaryBackend = "canary_backend_obs_fault_5619"
		canarySession = "canary_session_summary_2345"
		canaryPrompt  = "canary_prompt_content_6789"
	)

	// Induce a real post-preflight database write failure on observation insert inside the transaction.
	_, err = db.Exec(`
		CREATE TRIGGER fault_import_obs BEFORE INSERT ON observations
		BEGIN
			SELECT RAISE(ABORT, 'sqlite observation write fault: ` + canaryBackend + `');
		END;
	`)
	if err != nil {
		t.Fatalf("create fault trigger failed: %v", err)
	}

	data := &ExportData{
		Sessions: []*domain.Session{
			{
				ID:        "s-import-tx",
				Project:   "cortex",
				Directory: "/app",
				Summary:   "Session Summary <private>" + canarySession + "</private>",
			},
		},
		Observations: []*domain.Observation{
			{
				SessionID: "s-import-tx",
				Type:      domain.TypeManual,
				Title:     "Imported Note <private>" + canaryTitle + "</private>",
				Content:   "Imported Body <private>" + canaryBody + "</private> details",
				Project:   "cortex",
				Scope:     "project",
				Tags:      []string{"privacy", "tx-test"},
			},
		},
		Prompts: []*domain.Prompt{
			{
				SessionID: "s-import-tx",
				Project:   "cortex",
				Content:   "Imported Prompt <private>" + canaryPrompt + "</private>",
			},
		},
	}

	// Prove preflight succeeds cleanly on this data before the transaction runs.
	cleanData, pErr := PreflightExportData(data)
	if pErr != nil {
		t.Fatalf("expected preflight to succeed, got: %v", pErr)
	}
	if cleanData == nil || len(cleanData.Observations) != 1 {
		t.Fatalf("unexpected preflight clean data: %+v", cleanData)
	}

	// Caller snapshots for invariance verification.
	origSessID := data.Sessions[0].ID
	origSessSummary := data.Sessions[0].Summary
	origSessDir := data.Sessions[0].Directory
	origSessProject := data.Sessions[0].Project

	origObsTitle := data.Observations[0].Title
	origObsContent := data.Observations[0].Content
	origObsSessionID := data.Observations[0].SessionID
	origObsProject := data.Observations[0].Project
	origObsType := data.Observations[0].Type
	origObsScope := data.Observations[0].Scope
	origObsTags := append([]string(nil), data.Observations[0].Tags...)

	origPromptSessionID := data.Prompts[0].SessionID
	origPromptProject := data.Prompts[0].Project
	origPromptContent := data.Prompts[0].Content

	// ImportData runs preflight (passes), begins tx, inserts session, fails on observation insert trigger.
	res, err := store.ImportData(ctx, data)
	if res != nil {
		t.Fatalf("expected nil result on write failure, got %+v", res)
	}
	if err == nil {
		t.Fatalf("expected post-preflight transaction write error, got nil")
	}

	// Payload-free error mapping check.
	if !errors.Is(err, ErrImportObservation) {
		t.Fatalf("expected errors.Is(err, ErrImportObservation), got: %v", err)
	}
	if !errors.Is(err, ErrImportFailed) {
		t.Fatalf("expected errors.Is(err, ErrImportFailed), got: %v", err)
	}
	if err.Error() != "import observation failed" {
		t.Fatalf("expected error message %q, got %q", "import observation failed", err.Error())
	}

	// Reject title, body, backend, and envelope canaries in error.
	if strings.Contains(err.Error(), canaryTitle) {
		t.Errorf("error leaked canaryTitle: %q", err.Error())
	}
	if strings.Contains(err.Error(), canaryBody) {
		t.Errorf("error leaked canaryBody: %q", err.Error())
	}
	if strings.Contains(err.Error(), canaryBackend) {
		t.Errorf("error leaked canaryBackend: %q", err.Error())
	}
	if strings.Contains(err.Error(), canarySession) {
		t.Errorf("error leaked canarySession: %q", err.Error())
	}
	if strings.Contains(err.Error(), canaryPrompt) {
		t.Errorf("error leaked canaryPrompt: %q", err.Error())
	}
	if strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("error leaked redacted marker: %q", err.Error())
	}

	// Rollback verification: session inserted earlier in the transaction must be rolled back.
	if count := countRows(t, db, "SELECT count(*) FROM sessions"); count != 1 {
		t.Errorf("sessions count after rollback = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM sessions WHERE id = 's-import-tx'"); count != 0 {
		t.Errorf("session 's-import-tx' was not rolled back: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations"); count != 1 {
		t.Errorf("observations count after rollback = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations WHERE session_id = 's-import-tx'"); count != 0 {
		t.Errorf("observations for 's-import-tx' were persisted: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM user_prompts"); count != 1 {
		t.Errorf("user_prompts count after rollback = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM user_prompts WHERE session_id = 's-import-tx'"); count != 0 {
		t.Errorf("prompts for 's-import-tx' were persisted: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations_fts"); count != 1 {
		t.Errorf("observations_fts count after rollback = %d, want 1", count)
	}

	// Database must not contain any canaries.
	if count := countRows(t, db, "SELECT count(*) FROM observations WHERE title LIKE ? OR content LIKE ?", "%"+canaryTitle+"%", "%"+canaryBody+"%"); count != 0 {
		t.Errorf("observations table persisted canaries: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"' || ? || '\"'", canaryTitle); count != 0 {
		t.Errorf("observations_fts contains canaryTitle: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"' || ? || '\"'", canaryBody); count != 0 {
		t.Errorf("observations_fts contains canaryBody: count = %d", count)
	}

	// Caller-input invariance check.
	if data.Sessions[0].ID != origSessID || data.Sessions[0].Summary != origSessSummary ||
		data.Sessions[0].Directory != origSessDir || data.Sessions[0].Project != origSessProject {
		t.Errorf("caller session struct was mutated: got %+v", data.Sessions[0])
	}
	if data.Observations[0].Title != origObsTitle || data.Observations[0].Content != origObsContent ||
		data.Observations[0].SessionID != origObsSessionID || data.Observations[0].Project != origObsProject ||
		data.Observations[0].Type != origObsType || data.Observations[0].Scope != origObsScope {
		t.Errorf("caller observation struct was mutated: got %+v", data.Observations[0])
	}
	if !data.Observations[0].CreatedAt.IsZero() || !data.Observations[0].UpdatedAt.IsZero() {
		t.Errorf("caller observation timestamps were mutated: created=%v updated=%v", data.Observations[0].CreatedAt, data.Observations[0].UpdatedAt)
	}
	if len(data.Observations[0].Tags) != len(origObsTags) {
		t.Errorf("caller observation tags mutated: got %v, want %v", data.Observations[0].Tags, origObsTags)
	} else {
		for i := range origObsTags {
			if data.Observations[0].Tags[i] != origObsTags[i] {
				t.Errorf("caller observation tag [%d] mutated: got %q, want %q", i, data.Observations[0].Tags[i], origObsTags[i])
			}
		}
	}
	if data.Prompts[0].SessionID != origPromptSessionID || data.Prompts[0].Project != origPromptProject ||
		data.Prompts[0].Content != origPromptContent {
		t.Errorf("caller prompt struct was mutated: got %+v", data.Prompts[0])
	}
}
