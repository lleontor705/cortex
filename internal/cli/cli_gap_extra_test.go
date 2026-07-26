package cli

// cli_gap_extra_test.go is the second additive, strictly-TDD behavior suite for
// the internal/cli package (coverage-70-and-lint, D1 cycle 2 gap-fill, issue #46).
//
// It targets the statements left uncovered by cli_test.go and cli_extra_test.go:
// flag-value parsing branches, the migrate-down path, export equals/write-failure
// paths, sync project-scoped export + empty export, merge-projects sources output,
// reindex vector-guard, doctor orphan/embeddings branches, gc parsing + collection,
// the serve non-loopback guard, and the openApp() error branches shared by every
// app-opening command (exercised safely without ever launching MCP/TUI/HTTP).
//
// Isolation contract (honored by every app-opening test):
//   - unique on-disk SQLite database per test (CORTEX_DATABASE_IN_MEMORY=false)
//   - isolated HOME/USERPROFILE via t.Setenv so config.Load never reads or writes
//     real user configuration
//   - embedding providers neutralized (provider "none") so app.Open never
//     auto-starts Ollama or touches the network, except the two reindex/doctor
//     cases that set provider "ollama" purely to construct a non-nil embedder
//     (no Embed() call ever happens, so no network contact occurs)
//   - controlled stdout/stderr buffers; no MCP stdio, TUI, or server is launched
//   - no t.Parallel (environment mutation must not run concurrently)

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/app"
	"github.com/lleontor705/cortex/internal/domain"
)

// setGapFailingApp isolates HOME and points CORTEX_DATABASE_PATH at a path whose
// parent is an existing regular file. app.Open calls os.MkdirAll on that parent,
// which fails, so every app-opening command takes its openApp() error branch and
// returns 1 without launching any service.
func setGapFailingApp(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_SEARCH_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	t.Setenv("CORTEX_DATABASE_PATH", filepath.Join(blocker, "cortex.db"))
}

// setGapEnvWithProvider isolates HOME/database and writes a cortex.yaml under
// $HOME/.cortex setting search.embedding_provider. The provider env var is NOT
// viper-bound (no SetDefault/BindEnv for it), so only a config file can select a
// non-"none" provider. provider "ollama" constructs a non-nil embedder without
// any network contact (Embed() is never called by reindex/doctor guard paths).
func setGapEnvWithProvider(t *testing.T, provider string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_DATABASE_PATH", filepath.Join(home, "cortex.db"))
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")
	cfgDir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	yaml := "search:\n  embedding_provider: " + provider + "\n  ollama_auto_start: false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "cortex.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// backdateCreated sets an observation's created_at to a fixed past timestamp via
// direct SQL. created_at is stored to second precision, so sub-second sleeps are
// unreliable for ordering; this makes timeline/gc ordering deterministic.
func backdateCreated(t *testing.T, db *sql.DB, id int64, ts string) {
	t.Helper()
	if _, err := db.Exec("UPDATE observations SET created_at = ? WHERE id = ?", ts, id); err != nil {
		t.Fatalf("backdate created_at for obs %d: %v", id, err)
	}
}

// ---------------------------------------------------------------------------
// search: --type / --scope / valid --limit value branches + result printing
// ---------------------------------------------------------------------------

func TestGapSearchAllFlagsAndResult(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "Searchable", "body content", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "search", "Searchable", "--type", "manual", "--scope", "project", "--limit", "5", "--project", "demo")
	if code != 0 {
		t.Fatalf("search code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Found 1 memories:") {
		t.Fatalf("search stdout = %q", out)
	}
	if !strings.Contains(out, "Searchable") {
		t.Fatalf("search stdout missing title = %q", out)
	}
	if !strings.Contains(out, "project: demo") {
		t.Fatalf("search stdout missing project suffix = %q", out)
	}
}

// ---------------------------------------------------------------------------
// save: --scope and --topic value branches
// ---------------------------------------------------------------------------

func TestGapSaveScopeAndTopic(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "save", "Topic obs", "body", "--scope", "personal", "--topic", "architecture/auth")
	if code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Memory saved:") {
		t.Fatalf("save stdout = %q", out)
	}
	// Verify the topic/scope were persisted by reading the observation back.
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer func() { _ = a.Close() }()
	obs, err := a.Stores.Observations.GetByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("get obs: %v", err)
	}
	if obs.Scope != "personal" {
		t.Fatalf("scope = %q, want personal", obs.Scope)
	}
	if obs.TopicKey != "architecture/auth" {
		t.Fatalf("topic_key = %q, want architecture/auth", obs.TopicKey)
	}
}

func TestGapSaveRejectsEmptyTitle(t *testing.T) {
	setCLIEnv(t)
	code, _, errB := run(t, "cortex", "save", "", "body")
	if code != 1 {
		t.Fatalf("save empty-title code = %d, want 1", code)
	}
	if !strings.Contains(errB, "title is required") {
		t.Fatalf("save empty-title stderr = %q", errB)
	}
}

// ---------------------------------------------------------------------------
// context: --scope value branch + ended session status
// ---------------------------------------------------------------------------

func TestGapContextScopeFiltersResults(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "ProjObs", "body", "--project", "demo", "--scope", "project"); code != 0 {
		t.Fatalf("save project code = %d, stderr = %q", code, errB)
	}
	if code, _, errB := run(t, "cortex", "save", "PersObs", "body", "--project", "demo", "--scope", "personal"); code != 0 {
		t.Fatalf("save personal code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "context", "demo", "--scope", "personal")
	if code != 0 {
		t.Fatalf("context code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "PersObs") {
		t.Fatalf("context personal missing PersObs = %q", out)
	}
	if strings.Contains(out, "ProjObs") {
		t.Fatalf("context personal should not include project-scoped obs = %q", out)
	}
}

func TestGapContextEndedSessionStatus(t *testing.T) {
	setCLIEnv(t)
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	ctx := context.Background()
	if err := a.Stores.Sessions.Create(ctx, &domain.Session{ID: "ended-1", Project: "gapdemo", Directory: "."}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := a.Stores.Sessions.End(ctx, "ended-1", "wrapped up"); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
	code, out, errB := run(t, "cortex", "context", "gapdemo")
	if code != 0 {
		t.Fatalf("context code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "ended-1") {
		t.Fatalf("context missing ended session id = %q", out)
	}
	if !strings.Contains(out, "ended") {
		t.Fatalf("context missing ended status = %q", out)
	}
}

// ---------------------------------------------------------------------------
// timeline: valid --before/--after, invalid --after, and before/after loops
// ---------------------------------------------------------------------------

func TestGapTimelineInvalidAfter(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "T", "b", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, _, errB := run(t, "cortex", "timeline", "1", "--after", "xx")
	if code != 1 || !strings.Contains(errB, "invalid --after") {
		t.Fatalf("timeline bad after code=%d stderr=%q", code, errB)
	}
}

func TestGapTimelineReportsMissingObservation(t *testing.T) {
	setCLIEnv(t)
	code, _, errB := run(t, "cortex", "timeline", "999")
	if code != 1 {
		t.Fatalf("timeline missing observation code = %d, want 1", code)
	}
	if !strings.Contains(errB, "not found") {
		t.Fatalf("timeline missing observation stderr = %q", errB)
	}
}

func TestGapTimelineBeforeAfterWindows(t *testing.T) {
	setCLIEnv(t)
	for _, title := range []string{"GapOld", "GapMid", "GapNew"} {
		if code, _, errB := run(t, "cortex", "save", title, "body", "--project", "demo"); code != 0 {
			t.Fatalf("save %q code = %d, stderr = %q", title, code, errB)
		}
	}
	// created_at is stored to second precision, so backdate deterministically:
	// GapOld < GapMid < GapNew(now).
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	db := a.DB.DB()
	backdateCreated(t, db, 1, time.Now().Add(-2*time.Hour).Format("2006-01-02 15:04:05"))
	backdateCreated(t, db, 2, time.Now().Add(-1*time.Hour).Format("2006-01-02 15:04:05"))
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	// Middle observation is #2; exercise valid --before/--after value parsing
	// and the before/after printing loops.
	code, out, errB := run(t, "cortex", "timeline", "2", "--before", "2", "--after", "2")
	if code != 0 {
		t.Fatalf("timeline code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, ">>> #2") {
		t.Fatalf("timeline missing target highlight = %q", out)
	}
	if !strings.Contains(out, "GapOld") {
		t.Fatalf("timeline missing older (before) entry = %q", out)
	}
	if !strings.Contains(out, "GapNew") {
		t.Fatalf("timeline missing newer (after) entry = %q", out)
	}
}

// ---------------------------------------------------------------------------
// revisions: --limit parsing, limit<=0 reset, reason fallback, malformed JSON,
// content printing, and count==0 fallback
// ---------------------------------------------------------------------------

func TestGapRevisionsLimitAndVariants(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "RevTarget", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}

	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	repo := a.Stores.TemporalSnapshots

	// Valid snapshot with empty reason -> "revision" fallback + content printing.
	emptyReason := &domain.TemporalSnapshot{
		SnapshotKey:       "gap-empty-reason",
		Timestamp:         time.Now(),
		Description:       `{"reason":"","previous":{"title":"OldTitle","content":"old body text","revision_count":1}}`,
		RootObservationID: 1,
	}
	if err := repo.CreateSnapshot(ctx, emptyReason); err != nil {
		t.Fatalf("create empty-reason snapshot: %v", err)
	}
	// Malformed-description snapshot attached to root 1 -> unmarshal error,
	// skipped (continue), so only the empty-reason snapshot is rendered.
	malformedRoot1 := &domain.TemporalSnapshot{
		SnapshotKey:       "gap-malformed-r1",
		Timestamp:         time.Now().Add(time.Second),
		Description:       `:::not json:::`,
		RootObservationID: 1,
	}
	if err := repo.CreateSnapshot(ctx, malformedRoot1); err != nil {
		t.Fatalf("create malformed-r1 snapshot: %v", err)
	}
	latestValid := &domain.TemporalSnapshot{
		SnapshotKey:       "gap-latest-valid",
		Timestamp:         time.Now().Add(2 * time.Second),
		Description:       `{"reason":"updated","previous":{"title":"NewestPrevious","content":"","revision_count":2}}`,
		RootObservationID: 1,
	}
	if err := repo.CreateSnapshot(ctx, latestValid); err != nil {
		t.Fatalf("create latest-valid snapshot: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	t.Run("limit parses and prints revision with fallback reason", func(t *testing.T) {
		// observation 1 has emptyReason (parses) + malformed-r1 (skipped).
		code, out, errB := run(t, "cortex", "revisions", "1", "--limit", "5")
		if code != 0 {
			t.Fatalf("revisions code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "Revision history for observation #1") {
			t.Fatalf("revisions missing header = %q", out)
		}
		if !strings.Contains(out, "[revision]") {
			t.Fatalf("revisions missing fallback reason = %q", out)
		}
		if !strings.Contains(out, "old body text") {
			t.Fatalf("revisions missing previous content = %q", out)
		}
	})

	t.Run("limit zero resets to default and still prints", func(t *testing.T) {
		code, out, errB := run(t, "cortex", "revisions", "1", "--limit", "0")
		if code != 0 {
			t.Fatalf("revisions --limit 0 code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "[revision]") {
			t.Fatalf("revisions --limit 0 missing entry = %q", out)
		}
	})

	t.Run("limit one stops after first rendered revision", func(t *testing.T) {
		code, out, errB := run(t, "cortex", "revisions", "1", "--limit", "1")
		if code != 0 {
			t.Fatalf("revisions --limit 1 code = %d, stderr = %q", code, errB)
		}
		if strings.Count(out, "[updated]")+strings.Count(out, "[revision]") != 1 {
			t.Fatalf("revisions --limit 1 rendered multiple revisions = %q", out)
		}
	})
}

func TestGapRevisionsAllMalformedCountZero(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "OnlyMal", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	ctx := context.Background()
	if err := a.Stores.TemporalSnapshots.CreateSnapshot(ctx, &domain.TemporalSnapshot{
		SnapshotKey:       "gap-only-bad",
		Timestamp:         time.Now(),
		Description:       `,,,broken,,,`,
		RootObservationID: 1,
	}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
	code, out, errB := run(t, "cortex", "revisions", "1")
	if code != 0 {
		t.Fatalf("revisions code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "No revision history found for observation #1") {
		t.Fatalf("revisions count-zero fallback stdout = %q", out)
	}
}

func TestGapRevisionsReportsMissingObservation(t *testing.T) {
	setCLIEnv(t)
	code, _, errB := run(t, "cortex", "revisions", "999")
	if code != 1 {
		t.Fatalf("revisions missing observation code = %d, want 1", code)
	}
	if !strings.Contains(errB, "not found") {
		t.Fatalf("revisions missing observation stderr = %q", errB)
	}
}

// ---------------------------------------------------------------------------
// migrate: down success + invalid --target
// ---------------------------------------------------------------------------

func TestGapMigrateDown(t *testing.T) {
	t.Run("down to zero succeeds", func(t *testing.T) {
		setCLIEnv(t)
		code, out, errB := run(t, "cortex", "migrate", "down")
		if code != 0 {
			t.Fatalf("migrate down code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "Migrations rolled back to version 0") {
			t.Fatalf("migrate down stdout = %q", out)
		}
	})
	t.Run("down invalid target exit1", func(t *testing.T) {
		setCLIEnv(t)
		code, _, errB := run(t, "cortex", "migrate", "down", "--target", "notanum")
		if code != 1 || !strings.Contains(errB, "invalid target version") {
			t.Fatalf("migrate down bad target code=%d stderr=%q", code, errB)
		}
	})
	t.Run("down valid target parses", func(t *testing.T) {
		setCLIEnv(t)
		code, out, _ := run(t, "cortex", "migrate", "down", "--target", "1")
		if code != 0 {
			t.Fatalf("migrate down --target 1 code = %d", code)
		}
		if !strings.Contains(out, "Migrations rolled back to version 1") {
			t.Fatalf("migrate down --target 1 stdout = %q", out)
		}
	})
}

// ---------------------------------------------------------------------------
// export: --output= equals form + write-failure path
// ---------------------------------------------------------------------------

func TestGapExportOutputEqualsForm(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "EqOut", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	outFile := filepath.Join(t.TempDir(), "equals.json")
	code, out, errB := run(t, "cortex", "export", "--project=demo", "--output="+outFile)
	if code != 0 {
		t.Fatalf("export code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Exported 1 observations") {
		t.Fatalf("export stdout = %q", out)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	var got []domain.Observation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("export file invalid JSON: %v", err)
	}
	if len(got) != 1 || got[0].Title != "EqOut" {
		t.Fatalf("export persisted rows = %+v", got)
	}
}

func TestGapExportWriteFailure(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "Wf", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	// Output path under a non-existent directory -> os.WriteFile fails.
	badOut := filepath.Join(t.TempDir(), "missing-dir", "out.json")
	code, _, errB := run(t, "cortex", "export", "--project", "demo", "--output", badOut)
	if code != 1 || !strings.Contains(errB, "failed to write file") {
		t.Fatalf("export write-fail code=%d stderr=%q", code, errB)
	}
}

// ---------------------------------------------------------------------------
// sync: project-scoped export + empty (nothing-new) export
// ---------------------------------------------------------------------------

func TestGapSyncProjectExportThenEmpty(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "SyncProj", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "sync", "--project", "demo")
	if code != 0 {
		t.Fatalf("sync export code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Exported chunk") {
		t.Fatalf("sync export stdout = %q", out)
	}
	code, out, errB = run(t, "cortex", "sync", "--project", "demo")
	if code != 0 {
		t.Fatalf("sync second code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Nothing new to sync.") {
		t.Fatalf("sync empty stdout = %q", out)
	}
}

func TestGapSyncAutoDetectsProjectOnEmptyDatabase(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "sync")
	if code != 0 {
		t.Fatalf("sync auto-detect code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Nothing new to sync.") {
		t.Fatalf("sync auto-detect empty stdout = %q", out)
	}
}

// ---------------------------------------------------------------------------
// merge-projects: sources output + empty-entry trimming
// ---------------------------------------------------------------------------

func TestGapMergeProjectsSourcesOutput(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "From alpha", "a", "--project", "alpha"); code != 0 {
		t.Fatalf("save alpha code = %d, stderr = %q", code, errB)
	}
	if code, _, errB := run(t, "cortex", "save", "From beta", "b", "--project", "beta"); code != 0 {
		t.Fatalf("save beta code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "merge-projects", "--from", "alpha,beta", "--to", "gamma")
	if code != 0 {
		t.Fatalf("merge code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Merged into") {
		t.Fatalf("merge stdout missing header = %q", out)
	}
	if !strings.Contains(out, "Sources merged:") || !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("merge stdout missing sources = %q", out)
	}
}

func TestGapMergeProjectsEmptyEntryTrimming(t *testing.T) {
	code, out, errB := run(t, "cortex", "merge-projects", "--from", " , alpha , , ", "--to", "canonical", "--dry-run")
	if code != 0 {
		t.Fatalf("merge dry-run code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "would merge [alpha]") {
		t.Fatalf("merge trimming stdout = %q", out)
	}
}

// ---------------------------------------------------------------------------
// import JSON: rejected rows are reported and do not inflate the saved count
// ---------------------------------------------------------------------------

func TestGapImportJSONReportsRejectedObservation(t *testing.T) {
	setCLIEnv(t)
	input := filepath.Join(t.TempDir(), "invalid-observation.json")
	if err := os.WriteFile(input, []byte(`[{"title":"","content":"body","type":"manual"}]`), 0o600); err != nil {
		t.Fatalf("write JSON import fixture: %v", err)
	}
	code, out, errB := run(t, "cortex", "import", "--from-json", "--path", input)
	if code != 0 {
		t.Fatalf("import rejected observation code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(errB, "warning: skipped") {
		t.Fatalf("import rejected observation stderr = %q", errB)
	}
	if !strings.Contains(out, "Imported 0 of 1 observations from JSON") {
		t.Fatalf("import rejected observation stdout = %q", out)
	}
}

// ---------------------------------------------------------------------------
// stats: projects discovered from observations when no sessions exist
// ---------------------------------------------------------------------------

func TestGapStatsFallsBackToObservationProjects(t *testing.T) {
	setCLIEnv(t)
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	// Seed a legacy orphan row so session statistics remain empty and runStats
	// must fall back to observation projects. Constrain the pool so the PRAGMA
	// and insert use the same SQLite connection.
	db := a.DB.DB()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		_ = a.Close()
		t.Fatalf("disable foreign keys for legacy fixture: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO observations (session_id, type, title, content, project, scope)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"missing-session", "manual", "Observation project", "body", "observation-only", "project"); err != nil {
		_ = a.Close()
		t.Fatalf("seed sessionless observation: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	code, out, errB := run(t, "cortex", "stats")
	if code != 0 {
		t.Fatalf("stats code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Projects:     observation-only") {
		t.Fatalf("stats did not use observation projects = %q", out)
	}
}

// ---------------------------------------------------------------------------
// reindex: vector-store-not-available guard (provider ollama, no network)
// ---------------------------------------------------------------------------

func TestGapReindexVectorStoreUnavailable(t *testing.T) {
	// provider "ollama" (via config file) constructs a non-nil embedder without
	// contacting the network; reindex returns at the vector-store guard before
	// any Embed() call.
	setGapEnvWithProvider(t, "ollama")
	code, _, errB := run(t, "cortex", "reindex")
	if code != 1 {
		t.Fatalf("reindex code = %d, want 1 (vector store unavailable)", code)
	}
	if !strings.Contains(errB, "vector store not available") {
		t.Fatalf("reindex stderr = %q", errB)
	}
}

// ---------------------------------------------------------------------------
// doctor: orphan check with data + embeddings-configured branch
// ---------------------------------------------------------------------------

func TestGapDoctorWithDataReportsOrphans(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "DocObs", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "doctor")
	if code != 0 {
		t.Fatalf("doctor code = %d, stderr = %q", code, errB)
	}
	// 1 orphan of 1 observation = 100% > 50% -> WARN branch is taken.
	if !strings.Contains(out, "[WARN] Orphans: 1 (100%)") {
		t.Fatalf("doctor missing orphan WARN line = %q", out)
	}
}

func TestGapDoctorEmbeddingsConfigured(t *testing.T) {
	setGapEnvWithProvider(t, "ollama")
	code, out, errB := run(t, "cortex", "doctor")
	if code != 0 {
		t.Fatalf("doctor code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "[OK]   Embeddings:") {
		t.Fatalf("doctor missing embeddings-configured line = %q", out)
	}
}

func TestGapDoctorReportsConnectedObservationsAsNonOrphans(t *testing.T) {
	setCLIEnv(t)
	for _, title := range []string{"Connected A", "Connected B"} {
		if code, _, errB := run(t, "cortex", "save", title, "body", "--project", "demo"); code != 0 {
			t.Fatalf("save %q code = %d, stderr = %q", title, code, errB)
		}
	}
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	if err := a.Stores.Graph.CreateEdge(context.Background(), &domain.Edge{
		FromObsID: 1, ToObsID: 2, RelationType: "references", Weight: 1,
	}); err != nil {
		_ = a.Close()
		t.Fatalf("connect observations: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	code, out, errB := run(t, "cortex", "doctor")
	if code != 0 {
		t.Fatalf("doctor code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "[OK]   Orphans: 0 (0%)") {
		t.Fatalf("doctor missing connected-orphans result = %q", out)
	}
}

// ---------------------------------------------------------------------------
// gc: --days parsing (invalid ignored, valid applied) + actual collection
// ---------------------------------------------------------------------------

func TestGapGCDaysParsing(t *testing.T) {
	t.Run("invalid days ignored defaults to 90", func(t *testing.T) {
		setCLIEnv(t)
		code, out, errB := run(t, "cortex", "gc", "--days", "abc")
		if code != 0 {
			t.Fatalf("gc invalid-days code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "Nothing to collect.") {
			t.Fatalf("gc invalid-days stdout = %q", out)
		}
	})
	t.Run("valid days applied", func(t *testing.T) {
		setCLIEnv(t)
		code, out, errB := run(t, "cortex", "gc", "--days", "5")
		if code != 0 {
			t.Fatalf("gc valid-days code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "Nothing to collect.") {
			t.Fatalf("gc valid-days stdout = %q", out)
		}
	})
}

func TestGapGCCollectsArchivedObservation(t *testing.T) {
	setCLIEnv(t)
	// Disable the background auto-archival goroutine (enabled by default) so the
	// backdated observation stays non-deleted until gc explicitly collects it.
	// lifecycle.enable_auto_archive is viper-bound, so this env override applies.
	t.Setenv("CORTEX_LIFECYCLE_ENABLE_AUTO_ARCHIVE", "false")
	// Save with a non-empty topic_key: ListArchivable scans topic_key into a
	// string and errors on NULL, so a topic is required for the row to be usable.
	if code, _, errB := run(t, "cortex", "save", "Collect Me", "body", "--project", "demo", "--topic", "gc-topic"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	ctx := context.Background()
	db := a.DB.DB()
	// ListArchivable selects non-deleted observations older than the cutoff whose
	// importance score is NULL or below the (hardcoded 0) threshold. Saving seeds
	// a default positive score, so remove the row to make it an unscored candidate.
	old := time.Now().AddDate(0, 0, -120).Format("2006-01-02 15:04:05")
	if _, err := db.Exec("UPDATE observations SET created_at = ? WHERE id = 1", old); err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}
	if _, err := db.Exec("DELETE FROM importance_scores WHERE observation_id = 1"); err != nil {
		t.Fatalf("delete importance score: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
	code, out, errB := run(t, "cortex", "gc", "--days", "30")
	if code != 0 {
		t.Fatalf("gc code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Garbage collected 1 observations") {
		t.Fatalf("gc stdout = %q", out)
	}
	// Confirm the observation was actually hard-deleted.
	a2, err := app.Open(ctx, app.Options{})
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer func() { _ = a2.Close() }()
	if _, err := a2.Stores.Observations.GetByID(ctx, 1); err == nil {
		t.Fatal("observation #1 still present after gc")
	}
}

// ---------------------------------------------------------------------------
// serve: non-loopback host guard (returns 1 without launching the server)
// ---------------------------------------------------------------------------

func TestGapServeNonLoopbackGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_DATABASE_PATH", filepath.Join(home, "cortex.db"))
	t.Setenv("CORTEX_SEARCH_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("CORTEX_HTTP_HOST", "0.0.0.0")
	t.Setenv("CORTEX_HTTP_TOKEN", "")
	code, _, errB := run(t, "cortex", "serve")
	if code != 1 {
		t.Fatalf("serve non-loopback code = %d, want 1", code)
	}
	if !strings.Contains(errB, "refusing to expose HTTP API") {
		t.Fatalf("serve stderr = %q", errB)
	}
}

// ---------------------------------------------------------------------------
// openApp() error branches: every app-opening command returns 1 when the
// database cannot be opened, without launching any service.
// ---------------------------------------------------------------------------

func TestGapOpenAppFailureBranches(t *testing.T) {
	setGapFailingApp(t)

	// A valid JSON file so import --from-json reaches openApp after decoding.
	jsonFile := filepath.Join(t.TempDir(), "obs.json")
	if err := os.WriteFile(jsonFile, []byte(`[{"title":"x","content":"y","type":"manual"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
	}{
		{"search", []string{"cortex", "search", "x"}},
		{"save", []string{"cortex", "save", "t", "c"}},
		{"context", []string{"cortex", "context"}},
		{"stats", []string{"cortex", "stats"}},
		{"timeline", []string{"cortex", "timeline", "1"}},
		{"revisions", []string{"cortex", "revisions", "1"}},
		{"mcp", []string{"cortex", "mcp"}},
		{"tui", []string{"cortex", "tui"}},
		{"serve", []string{"cortex", "serve"}},
		{"import-json", []string{"cortex", "import", "--from-json", "--path", jsonFile}},
		{"migrate-status", []string{"cortex", "migrate", "status"}},
		{"export", []string{"cortex", "export"}},
		{"sync-status", []string{"cortex", "sync", "--status"}},
		{"merge-projects", []string{"cortex", "merge-projects", "--from", "a", "--to", "b"}},
		{"reindex", []string{"cortex", "reindex"}},
		{"doctor", []string{"cortex", "doctor"}},
		{"gc", []string{"cortex", "gc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errB := run(t, tc.args...)
			if code != 1 {
				t.Fatalf("%s code = %d, want 1 (openApp should fail)", tc.name, code)
			}
			if !strings.Contains(errB, "cortex:") {
				t.Fatalf("%s stderr = %q, want substring 'cortex:'", tc.name, errB)
			}
		})
	}
}
