package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func testStoreImportPromptWriteFault(t *testing.T) {
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

	// Seed prior database state.
	if _, err := db.Exec(`INSERT INTO sessions(id, project, directory, summary) VALUES ('s-prior-p', 'cortex', '/app', 'Prior Session P')`); err != nil {
		t.Fatalf("insert prior session failed: %v", err)
	}
	priorObs := &domain.Observation{
		SessionID: "s-prior-p",
		Type:      domain.TypeManual,
		Title:     "Prior Title P",
		Content:   "Prior Content P",
		Project:   "cortex",
		Scope:     "project",
	}
	if err := store.Save(ctx, priorObs); err != nil {
		t.Fatalf("Save prior observation failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_prompts(session_id, content, project) VALUES ('s-prior-p', 'Prior Prompt P', 'cortex')`); err != nil {
		t.Fatalf("insert prior prompt failed: %v", err)
	}

	const (
		canaryTitle   = "canary_tx_title_p_1102"
		canaryBody    = "canary_tx_body_p_2203"
		canaryPrompt  = "canary_tx_prompt_p_3304"
		canaryBackend = "canary_backend_prompt_fault_4405"
	)

	// Induce a real post-preflight database write failure on prompt insert inside the transaction.
	_, err = db.Exec(`
		CREATE TRIGGER fault_import_prompt BEFORE INSERT ON user_prompts
		BEGIN
			SELECT RAISE(ABORT, 'sqlite prompt write fault: ` + canaryBackend + `');
		END;
	`)
	if err != nil {
		t.Fatalf("create fault trigger failed: %v", err)
	}

	data := &ExportData{
		Sessions: []*domain.Session{
			{
				ID:        "s-import-prompt-tx",
				Project:   "cortex",
				Directory: "/app",
				Summary:   "Session Summary",
			},
		},
		Observations: []*domain.Observation{
			{
				SessionID: "s-import-prompt-tx",
				Type:      domain.TypeManual,
				Title:     "Imported Note <private>" + canaryTitle + "</private>",
				Content:   "Imported Body <private>" + canaryBody + "</private> details",
				Project:   "cortex",
				Scope:     "project",
				Tags:      []string{"privacy", "prompt-tx-test"},
			},
		},
		Prompts: []*domain.Prompt{
			{
				SessionID: "s-import-prompt-tx",
				Project:   "cortex",
				Content:   "Prompt Query <private>" + canaryPrompt + "</private>",
			},
		},
	}

	cleanData, pErr := PreflightExportData(data)
	if pErr != nil {
		t.Fatalf("expected preflight to succeed, got: %v", pErr)
	}
	if cleanData == nil || len(cleanData.Prompts) != 1 {
		t.Fatalf("unexpected preflight clean data: %+v", cleanData)
	}

	origObsTitle := data.Observations[0].Title
	origObsContent := data.Observations[0].Content
	origPromptContent := data.Prompts[0].Content

	res, err := store.ImportData(ctx, data)
	if res != nil {
		t.Fatalf("expected nil result on write failure, got %+v", res)
	}
	if err == nil {
		t.Fatalf("expected post-preflight transaction write error, got nil")
	}

	// Payload-free error mapping check.
	if !errors.Is(err, ErrImportPrompt) {
		t.Fatalf("expected errors.Is(err, ErrImportPrompt), got: %v", err)
	}
	if !errors.Is(err, ErrImportFailed) {
		t.Fatalf("expected errors.Is(err, ErrImportFailed), got: %v", err)
	}
	if err.Error() != "import prompt failed" {
		t.Fatalf("expected error message %q, got %q", "import prompt failed", err.Error())
	}

	// Reject canaries.
	if strings.Contains(err.Error(), canaryTitle) || strings.Contains(err.Error(), canaryBody) ||
		strings.Contains(err.Error(), canaryPrompt) || strings.Contains(err.Error(), canaryBackend) ||
		strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("error leaked payload or canary: %q", err.Error())
	}

	// Rollback verification: earlier session AND observation writes inside tx must be rolled back.
	if count := countRows(t, db, "SELECT count(*) FROM sessions"); count != 1 {
		t.Errorf("sessions count after rollback = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM sessions WHERE id = 's-import-prompt-tx'"); count != 0 {
		t.Errorf("session 's-import-prompt-tx' was not rolled back: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations"); count != 1 {
		t.Errorf("observations count after rollback = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations WHERE session_id = 's-import-prompt-tx'"); count != 0 {
		t.Errorf("observations for 's-import-prompt-tx' were not rolled back: count = %d", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations_fts"); count != 1 {
		t.Errorf("observations_fts count after rollback = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM user_prompts"); count != 1 {
		t.Errorf("user_prompts count after rollback = %d, want 1", count)
	}

	// Caller-input invariance check.
	if data.Observations[0].Title != origObsTitle || data.Observations[0].Content != origObsContent ||
		data.Prompts[0].Content != origPromptContent {
		t.Errorf("caller struct mutated: obs=(%q, %q), prompt=%q", data.Observations[0].Title, data.Observations[0].Content, data.Prompts[0].Content)
	}
}
