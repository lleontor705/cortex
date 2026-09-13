package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/testutil"
)

func setupPrivacyTestStore(t *testing.T) (*Store, *sql.DB, func()) {
	t.Helper()
	reg := migration.NewRegistry()
	reg.Register(migration.Migration{
		Version: 1, Name: "init_privacy",
		UpSQL: `
			CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL, started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT, summary TEXT);
			CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT, session_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL, tool_name TEXT, project TEXT, scope TEXT NOT NULL DEFAULT 'project', topic_key TEXT, normalized_hash TEXT, revision_count INTEGER NOT NULL DEFAULT 1, duplicate_count INTEGER NOT NULL DEFAULT 1, last_seen_at TEXT, confidence REAL NOT NULL DEFAULT 1.0, source TEXT NOT NULL DEFAULT 'manual', tags TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT);
			CREATE TABLE temporal_snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT, snapshot_key TEXT NOT NULL, timestamp DATETIME NOT NULL, description TEXT, observation_count INTEGER NOT NULL DEFAULT 0, edge_count INTEGER NOT NULL DEFAULT 0, root_observation_id INTEGER, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
			CREATE VIRTUAL TABLE observations_fts USING fts5(title, content, tool_name, type, project, scope, topic_key, content='observations', content_rowid='id');
			CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
				INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key) VALUES (new.id, new.title, COALESCE(new.type, '') || ' ' || COALESCE(new.topic_key, '') || ' ' || COALESCE(new.project, '') || ' ' || new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
			END;
			CREATE TRIGGER obs_fts_update AFTER UPDATE ON observations BEGIN
				INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, scope, topic_key) VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.scope, old.topic_key);
				INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, scope, topic_key) VALUES (new.id, new.title, COALESCE(new.type, '') || ' ' || COALESCE(new.topic_key, '') || ' ' || COALESCE(new.project, '') || ' ' || new.content, new.tool_name, new.type, new.project, new.scope, new.topic_key);
			END;
		`,
	})
	testDB := testutil.NewTestDBWithMigrations(t, reg)
	return NewStore(testDB.DB()), testDB.DB(), func() { testDB.Cleanup() }
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q failed: %v", query, err)
	}
	return count
}

func assertObsState(t *testing.T, db *sql.DB, id int64, wantRev, wantDup int, wantContent string) {
	t.Helper()
	var rev, dup int
	var content string
	if err := db.QueryRow("SELECT revision_count, duplicate_count, content FROM observations WHERE id = ?", id).
		Scan(&rev, &dup, &content); err != nil {
		t.Fatalf("scan obs %d failed: %v", id, err)
	}
	if rev != wantRev || dup != wantDup || content != wantContent {
		t.Errorf("obs %d state = (rev=%d, dup=%d, content=%q), want (rev=%d, dup=%d, content=%q)",
			id, rev, dup, content, wantRev, wantDup, wantContent)
	}
}

func TestObservationSave_PrivacyRedactionAndHash(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const canaryTitle = "canary_title_secret_9812"
	const canaryContent = "canary_content_secret_4321"

	obs := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeManual,
		Title:   "Cluster <private>" + canaryTitle + "</private> info",
		Content: "Connect using <private>" + canaryContent + "</private> safely",
		Project: "cortex", Scope: "project",
	}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if obs.Title != "Cluster [REDACTED] info" || obs.Content != "Connect using [REDACTED] safely" {
		t.Errorf("in-memory struct leaked: title=%q content=%q", obs.Title, obs.Content)
	}

	assertObsState(t, db, obs.ID, 1, 1, "Connect using [REDACTED] safely")

	var dbHash string
	if err := db.QueryRow("SELECT normalized_hash FROM observations WHERE id = ?", obs.ID).Scan(&dbHash); err != nil {
		t.Fatalf("load hash failed: %v", err)
	}
	if wantHash := hashNormalized("Connect using [REDACTED] safely"); dbHash != wantHash {
		t.Errorf("dbHash = %q, want %q (hash of protected text)", dbHash, wantHash)
	}

	if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"[REDACTED]\"'"); count != 1 {
		t.Errorf("FTS match REDACTED = %d, want 1", count)
	}
	if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"' || ? || '\"'", canaryContent); count != 0 {
		t.Errorf("FTS match canary = %d, want 0", count)
	}
}

func TestObservationSave_PrivacyRejections_NoMutation(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const canary = "canary_leak_proof_xyz"
	cases := []struct {
		name     string
		obs      *domain.Observation
		wantCode privacy.ErrorCode
	}{
		{"unclosed marker", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public <private>" + canary}, privacy.ErrCodeInvalidMarker},
		{"stray closing marker", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public </private>"}, privacy.ErrCodeInvalidMarker},
		{"nested marker", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public <private>a <private>b</private></private>"}, privacy.ErrCodeInvalidMarker},
		{"empty public residual", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "<private>" + canary + "</private>"}, privacy.ErrCodeRequiredEmpty},
		{"whitespace residual", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: " \t <private>" + canary + "</private> \n"}, privacy.ErrCodeRequiredEmpty},
		{"invalid utf8", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public \xff invalid"}, privacy.ErrCodeInvalidUTF8},
		{"marker in topickey", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public", TopicKey: "top/<private>" + canary + "</private>"}, privacy.ErrCodeInvalidMarker},
		{"marker in tags", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public", Tags: []string{"safe", "<private>" + canary + "</private>"}}, privacy.ErrCodeInvalidMarker},
		{"marker in project", &domain.Observation{SessionID: "sess-1", Title: "Title", Content: "Public", Project: "<private>" + canary + "</private>"}, privacy.ErrCodeInvalidMarker},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origTitle, origContent := tc.obs.Title, tc.obs.Content
			err := store.Save(ctx, tc.obs)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var privErr *privacy.Error
			if !errors.As(err, &privErr) || privErr.Code != tc.wantCode {
				t.Fatalf("expected *privacy.Error code %q, got: %v", tc.wantCode, err)
			}
			if strings.Contains(err.Error(), canary) {
				t.Errorf("error leaked canary: %s", err.Error())
			}
			if tc.obs.Title != origTitle || tc.obs.Content != origContent || !tc.obs.CreatedAt.IsZero() || !tc.obs.UpdatedAt.IsZero() {
				t.Errorf("caller observation struct was mutated on rejection")
			}
			if countRows(t, db, "SELECT count(*) FROM observations") != 0 || countRows(t, db, "SELECT count(*) FROM observations_fts") != 0 {
				t.Errorf("prior state dirty: rows persisted on rejected save")
			}
		})
	}
}

func TestObservationSave_PrivacyDeduplicationPriorState(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	obs1 := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeManual, Title: "Dedup note",
		Content: "Deploy to <private>secretA</private> cluster", Project: "cortex", Scope: "project",
	}
	if err := store.Save(ctx, obs1); err != nil {
		t.Fatalf("Save obs1 failed: %v", err)
	}
	assertObsState(t, db, obs1.ID, 1, 1, "Deploy to [REDACTED] cluster")

	obsReject := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeManual, Title: "Dedup note",
		Content: "Deploy to <private>unclosed", Project: "cortex", Scope: "project",
	}
	if err := store.Save(ctx, obsReject); err == nil {
		t.Fatalf("expected privacy rejection, got nil")
	}
	assertObsState(t, db, obs1.ID, 1, 1, "Deploy to [REDACTED] cluster")

	obs2 := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeManual, Title: "Dedup note",
		Content: "Deploy to <private>secretB</private> cluster", Project: "cortex", Scope: "project",
	}
	if err := store.Save(ctx, obs2); !domain.IsClass(err, domain.ClassDedupSkipped) {
		t.Fatalf("expected ClassDedupSkipped, got: %v", err)
	}
	assertObsState(t, db, obs1.ID, 1, 2, "Deploy to [REDACTED] cluster")
}

func TestObservationTopicUpsert_PrivacyPriorState(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	obs1 := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeConfig, Title: "DB config",
		Content: "Initial DB config public", TopicKey: "arch/db", Project: "cortex", Scope: "project",
	}
	if err := store.Save(ctx, obs1); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}
	assertObsState(t, db, obs1.ID, 1, 1, "Initial DB config public")
	if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 0 {
		t.Fatalf("expected 0 snapshots initially")
	}

	obsReject := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeConfig, Title: "DB config",
		Content: "New config <private>unclosed", TopicKey: "arch/db", Project: "cortex", Scope: "project",
	}
	if err := store.Save(ctx, obsReject); err == nil {
		t.Fatalf("expected rejection for unclosed marker, got nil")
	}
	assertObsState(t, db, obs1.ID, 1, 1, "Initial DB config public")
	if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 0 {
		t.Errorf("snapshot created on rejected upsert")
	}

	const canary = "secret_super_pw_11"
	obsUpsert := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeConfig, Title: "DB config",
		Content: "DB host <private>" + canary + "</private> updated", TopicKey: "arch/db", Project: "cortex", Scope: "project",
	}
	effect, err := store.SaveWithEffect(ctx, obsUpsert)
	if err != nil || effect.Status != domain.WriteStatusUpdated {
		t.Fatalf("SaveWithEffect() = (%v, %v), want WriteStatusUpdated", effect, err)
	}

	assertObsState(t, db, obs1.ID, 2, 1, "DB host [REDACTED] updated")
	if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 1 {
		t.Errorf("temporal_snapshots count != 1")
	}
	var desc string
	_ = db.QueryRow("SELECT description FROM temporal_snapshots LIMIT 1").Scan(&desc)
	if strings.Contains(desc, canary) || !strings.Contains(desc, "Initial DB config public") {
		t.Errorf("snapshot description invalid: %s", desc)
	}
}

func TestObservationUpdate_PrivacyPriorState(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	obs := &domain.Observation{
		SessionID: "sess-1", Type: domain.TypeDecision, Title: "Server note",
		Content: "Initial decision note", Project: "cortex",
	}
	if err := store.Save(ctx, obs); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}
	assertObsState(t, db, obs.ID, 1, 1, "Initial decision note")

	obsBad := &domain.Observation{
		ID: obs.ID, SessionID: "sess-1", Type: domain.TypeDecision, Title: "Server note",
		Content: "Updated <private>unclosed", Project: "cortex",
	}
	if err := store.Update(ctx, obsBad); err == nil {
		t.Fatalf("expected Update rejection, got nil")
	}
	assertObsState(t, db, obs.ID, 1, 1, "Initial decision note")
	if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 0 {
		t.Errorf("snapshot created on rejected update")
	}

	const canary = "secret_update_key_77"
	obsGood := &domain.Observation{
		ID: obs.ID, SessionID: "sess-1", Type: domain.TypeDecision, Title: "Server note",
		Content: "Updated <private>" + canary + "</private> complete", Project: "cortex",
	}
	if err := store.Update(ctx, obsGood); err != nil {
		t.Fatalf("Update() failed: %v", err)
	}
	assertObsState(t, db, obs.ID, 2, 1, "Updated [REDACTED] complete")
	if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 1 {
		t.Errorf("temporal_snapshots count != 1")
	}
	var desc string
	_ = db.QueryRow("SELECT description FROM temporal_snapshots LIMIT 1").Scan(&desc)
	if strings.Contains(desc, canary) || !strings.Contains(desc, "Initial decision note") {
		t.Errorf("snapshot description invalid: %s", desc)
	}
}

func TestObservationSaveInTx_PrivacyFailSafe(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = store.WithinTx(ctx, tx, func(txCtx context.Context) error {
		obs := &domain.Observation{
			SessionID: "sess-1", Title: "Transactional note", Content: "Private <private>broken",
		}
		return store.SaveInTx(txCtx, obs)
	})
	if err == nil {
		t.Fatalf("expected error from SaveInTx, got nil")
	}
	_ = tx.Rollback()

	if count := countRows(t, db, "SELECT count(*) FROM observations"); count != 0 {
		t.Errorf("observations count after rollback = %d, want 0", count)
	}
}

func TestObservation_RawLargeProtectedSmall_SaveAndUpdate(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	canaryTitle := strings.Repeat("x", 250)
	canaryContent := strings.Repeat("y", 70*1024)
	const wantTitle = "Public Title [REDACTED]"
	const wantContent = "Public Content [REDACTED] ok"

	t.Run("Save raw-large protected-small", func(t *testing.T) {
		obs := &domain.Observation{
			SessionID: "sess-1", Type: domain.TypeManual, Project: "cortex", Scope: "project",
			Title:   "Public Title <private>" + canaryTitle + "</private>",
			Content: "Public Content <private>" + canaryContent + "</private> ok",
		}
		if err := store.Save(ctx, obs); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
		if obs.Title != wantTitle || obs.Content != wantContent {
			t.Errorf("caller struct not updated: title=%q content=%q", obs.Title, obs.Content)
		}
		assertObsState(t, db, obs.ID, 1, 1, wantContent)
		if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"[REDACTED]\"'"); count != 1 {
			t.Errorf("FTS match REDACTED = %d, want 1", count)
		}
		if count := countRows(t, db, "SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '\"' || ? || '\"'", canaryTitle[:20]); count != 0 {
			t.Errorf("FTS match canary = %d, want 0", count)
		}
	})

	t.Run("Update raw-large protected-small", func(t *testing.T) {
		initialObs := &domain.Observation{
			SessionID: "sess-1", Type: domain.TypeDecision, Title: "Init", Content: "Init", Project: "cortex",
		}
		if err := store.Save(ctx, initialObs); err != nil {
			t.Fatalf("initial Save failed: %v", err)
		}
		updateObs := &domain.Observation{
			ID: initialObs.ID, SessionID: "sess-1", Type: domain.TypeDecision, Project: "cortex",
			Title:   "Public Title <private>" + canaryTitle + "</private>",
			Content: "Public Content <private>" + canaryContent + "</private> ok",
		}
		if err := store.Update(ctx, updateObs); err != nil {
			t.Fatalf("Update() failed: %v", err)
		}
		if updateObs.Title != wantTitle || updateObs.Content != wantContent {
			t.Errorf("caller struct not updated: title=%q content=%q", updateObs.Title, updateObs.Content)
		}
		assertObsState(t, db, initialObs.ID, 2, 1, wantContent)
		if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 1 {
			t.Errorf("temporal_snapshots count != 1")
		}
	})
}

func TestObservation_ProtectedOverLimit_RejectsWithoutPersistenceEffects(t *testing.T) {
	store, db, cleanup := setupPrivacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name    string
		title   string
		content string
	}{
		{"title over limit", strings.Repeat("t", 201) + "<private>secret</private>", "Valid content"},
		{"content over limit", "Valid title", strings.Repeat("c", 64*1024+1) + "<private>secret</private>"},
	}
	for _, tc := range cases {
		t.Run("Save "+tc.name, func(t *testing.T) {
			obs := &domain.Observation{SessionID: "sess-1", Type: domain.TypeManual, Title: tc.title, Content: tc.content, Project: "cortex"}
			origTitle, origContent := obs.Title, obs.Content
			if err := store.Save(ctx, obs); err == nil || !domain.IsValidationError(err) {
				t.Fatalf("expected ValidationError, got %v", err)
			}
			if obs.Title != origTitle || obs.Content != origContent || !obs.CreatedAt.IsZero() || !obs.UpdatedAt.IsZero() {
				t.Errorf("caller struct mutated on rejected save")
			}
			if countRows(t, db, "SELECT count(*) FROM observations") != 0 || countRows(t, db, "SELECT count(*) FROM observations_fts") != 0 {
				t.Errorf("rows persisted on rejected save")
			}
		})
	}

	initialObs := &domain.Observation{SessionID: "sess-1", Type: domain.TypeDecision, Title: "Init", Content: "Init", Project: "cortex"}
	if err := store.Save(ctx, initialObs); err != nil {
		t.Fatalf("setup initial observation failed: %v", err)
	}

	for _, tc := range cases {
		t.Run("Update "+tc.name, func(t *testing.T) {
			obs := &domain.Observation{ID: initialObs.ID, SessionID: "sess-1", Type: domain.TypeDecision, Title: tc.title, Content: tc.content, Project: "cortex"}
			origTitle, origContent := obs.Title, obs.Content
			if err := store.Update(ctx, obs); err == nil || !domain.IsValidationError(err) {
				t.Fatalf("expected ValidationError, got %v", err)
			}
			if obs.Title != origTitle || obs.Content != origContent {
				t.Errorf("caller struct mutated on rejected update")
			}
			assertObsState(t, db, initialObs.ID, 1, 1, "Init")
			if countRows(t, db, "SELECT count(*) FROM temporal_snapshots") != 0 {
				t.Errorf("temporal_snapshots created on rejected update")
			}
		})
	}
}
