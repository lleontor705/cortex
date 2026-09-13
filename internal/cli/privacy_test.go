package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/app"
	"github.com/lleontor705/cortex/v2/internal/domain"
	_ "modernc.org/sqlite"
)

func setupCLITestDB(t *testing.T) (string, func() *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cortex_privacy.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_SYNC_ENABLED", "false")

	openDB := func() *sql.DB {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("open test db: %v", err)
		}
		return db
	}
	return dbPath, openDB
}

func countDBRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return 0
		}
		t.Fatalf("count table %s: %v", table, err)
	}
	return count
}

func TestExactCLIPrivacy(t *testing.T) {
	t.Run("SaveRedactedProseAndNoCanaryLeak", TestCLIPrivacy_SaveRedactedProseAndNoCanaryLeak)
	t.Run("SaveRejectionZeroEffects", TestCLIPrivacy_SaveRejectionZeroEffects)
	t.Run("SaveRawEmptyWhitespaceRejectionZeroEffects", TestCLIPrivacy_SaveRawEmptyWhitespaceRejectionZeroEffects)
	t.Run("ImportAtomicRejection", TestCLIPrivacy_ImportAtomicRejection)
	t.Run("ImportRedacted", TestCLIPrivacy_ImportRedacted)
	t.Run("ImportValidFirstInvalidSecondZeroEffects", TestCLIPrivacy_ImportValidFirstInvalidSecondZeroEffects)
	t.Run("ImportInvalidFieldsDoNotCreateSession", TestCLIPrivacy_ImportInvalidFieldsDoNotCreateSession)
	t.Run("BatchCallerInputUnchangedOnLaterInvalid", TestCLIPrivacy_BatchCallerInputUnchangedOnLaterInvalid)
}

func TestCLIPrivacy_ImportAtomicRejection(t *testing.T) {
	const canary = "canary_import_atomic_secret_7766"

	cases := []struct {
		name     string
		jsonData string
	}{
		{
			name: "unclosed title in second item",
			jsonData: `[
				{"title":"Valid Title 1","content":"Valid content 1","project":"p1","type":"manual"},
				{"title":"Bad Title <private>` + canary + `","content":"Body","project":"p1","type":"manual"}
			]`,
		},
		{
			name: "unclosed content in first item",
			jsonData: `[
				{"title":"Title 1","content":"Body with unclosed <private>` + canary + `","project":"p1","type":"manual"},
				{"title":"Valid Title 2","content":"Valid content 2","project":"p1","type":"manual"}
			]`,
		},
		{
			name: "pure private title",
			jsonData: `[
				{"title":"<private>` + canary + `</private>","content":"Body","project":"p1","type":"manual"}
			]`,
		},
		{
			name: "pure private content",
			jsonData: `[
				{"title":"Title 1","content":"<private>` + canary + `</private>","project":"p1","type":"manual"}
			]`,
		},
		{
			name: "marker in project metadata",
			jsonData: `[
				{"title":"Title 1","content":"Body 1","project":"<private>` + canary + `</private>"}
			]`,
		},
		{
			name: "marker in tags metadata",
			jsonData: `[
				{"title":"Title 1","content":"Body 1","project":"p1","tags":["<private>` + canary + `</private>"]}
			]`,
		},
		{
			name: "marker in session_id metadata",
			jsonData: `[
				{"title":"Title 1","content":"Body 1","project":"p1","session_id":"<private>` + canary + `</private>"}
			]`,
		},
		{
			name: "nil observation item in array",
			jsonData: `[
				{"title":"Valid Title 1","content":"Valid content 1","project":"p1","type":"manual"},
				null
			]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, openDB := setupCLITestDB(t)
			// Initialize database schema first with a clean app.Open
			a, err := app.Open(context.Background(), app.Options{})
			if err != nil {
				t.Fatalf("init db: %v", err)
			}
			_ = a.Close()

			jsonFile := filepath.Join(t.TempDir(), "atomic_import.json")
			if err := os.WriteFile(jsonFile, []byte(tc.jsonData), 0o600); err != nil {
				t.Fatalf("write json file: %v", err)
			}

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := Run([]string{"cortex", "import", "--from-json", "--path", jsonFile}, stdout, stderr)
			if code != 1 {
				t.Fatalf("expected exit code 1 on rejected import, got %d (out=%q err=%q)", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), canary) {
				t.Fatalf("stderr leaked canary: %q", stderr.String())
			}

			db := openDB()
			defer func() { _ = db.Close() }()

			if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
				t.Fatalf("atomic import failed: %d observations persisted despite rejection", obsCount)
			}
			if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
				t.Fatalf("atomic import failed: %d sessions persisted despite rejection", sessCount)
			}
		})
	}
}

func TestCLIPrivacy_ImportRedacted(t *testing.T) {
	_, openDB := setupCLITestDB(t)
	const canary1 = "canary_import_redact_5544"
	const canary2 = "canary_import_redact_8899"

	jsonData := `[
		{"title":"Import <private>` + canary1 + `</private> Title","content":"Body with <private>` + canary1 + `</private>","project":"p1","type":"manual","session_id":"s-priv-1"},
		{"title":"Second <private>` + canary2 + `</private> Note","content":"Second body <private>` + canary2 + `</private> details","project":"p1","type":"manual","session_id":"s-priv-2"}
	]`
	jsonFile := filepath.Join(t.TempDir(), "import_redact.json")
	if err := os.WriteFile(jsonFile, []byte(jsonData), 0o600); err != nil {
		t.Fatalf("write json file: %v", err)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{"cortex", "import", "--from-json", "--path", jsonFile}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if strings.Contains(outStr, canary1) || strings.Contains(outStr, canary2) {
		t.Fatalf("stdout leaked canary: %s", outStr)
	}
	if !strings.Contains(outStr, "Imported 2 of 2 observations from JSON") {
		t.Fatalf("unexpected stdout: %s", outStr)
	}

	db := openDB()
	defer func() { _ = db.Close() }()

	if obsCount := countDBRows(t, db, "observations"); obsCount != 2 {
		t.Fatalf("expected 2 observations, got %d", obsCount)
	}
	if sessCount := countDBRows(t, db, "sessions"); sessCount != 2 {
		t.Fatalf("expected 2 sessions, got %d", sessCount)
	}

	rows, err := db.Query("SELECT title, content FROM observations ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query observations: %v", err)
	}
	defer func() { _ = rows.Close() }()

	expected := [][2]string{
		{"Import [REDACTED] Title", "Body with [REDACTED]"},
		{"Second [REDACTED] Note", "Second body [REDACTED] details"},
	}
	idx := 0
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			t.Fatalf("scan row %d: %v", idx, err)
		}
		if strings.Contains(title, canary1) || strings.Contains(title, canary2) ||
			strings.Contains(content, canary1) || strings.Contains(content, canary2) {
			t.Fatalf("persisted canary leak at row %d: title=%q content=%q", idx, title, content)
		}
		if title != expected[idx][0] || content != expected[idx][1] {
			t.Fatalf("unexpected row %d: title=%q content=%q, want %+v", idx, title, content, expected[idx])
		}
		idx++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}
}

func TestCLIPrivacy_BatchCallerInputUnchangedOnLaterInvalid(t *testing.T) {
	cases := []struct {
		name    string
		badObs  *domain.Observation
		wantErr string
	}{
		{
			name: "second item unclosed marker",
			badObs: &domain.Observation{
				SessionID: "sess-batch-bad1", Title: "Bad Title <private>unclosed",
				Content: "Valid content", Project: "p1", Type: "manual", Scope: "project",
			},
			wantErr: "privacy_invalid_marker",
		},
		{
			name: "second item pure private content",
			badObs: &domain.Observation{
				SessionID: "sess-batch-bad2", Title: "Valid Title",
				Content: "<private>canary_pure_private</private>", Project: "p1", Type: "manual", Scope: "project",
			},
			wantErr: "privacy_required_empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, openDB := setupCLITestDB(t)

			const canaryT = "canary_batch_title_secret"
			const canaryC = "canary_batch_content_secret"

			batch := []*domain.Observation{
				{
					SessionID: "sess-batch-valid", Title: "Valid <private>" + canaryT + "</private> Title",
					Content: "Valid <private>" + canaryC + "</private> Content",
					Project: "p1", Type: "manual", Scope: "project", Tags: []string{"tag1", "tag2"},
				},
				tc.badObs,
			}

			orig0Title, orig0Content := batch[0].Title, batch[0].Content
			orig0Proj, orig0Sess := batch[0].Project, batch[0].SessionID
			orig0Tags := append([]string(nil), batch[0].Tags...)
			orig1Title, orig1Content := batch[1].Title, batch[1].Content

			staged, err := preflightObservations(batch)
			if err == nil {
				t.Fatalf("expected preflight error for %s, got nil", tc.name)
			}
			if staged != nil {
				t.Fatalf("expected nil staged results on rejection, got %v", staged)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}

			if batch[0].Title != orig0Title || batch[0].Content != orig0Content ||
				batch[0].Project != orig0Proj || batch[0].SessionID != orig0Sess ||
				len(batch[0].Tags) != len(orig0Tags) || batch[0].Tags[0] != orig0Tags[0] || batch[0].Tags[1] != orig0Tags[1] {
				t.Fatalf("caller batch[0] mutated on rejection: got (title=%q, content=%q), want (title=%q, content=%q)",
					batch[0].Title, batch[0].Content, orig0Title, orig0Content)
			}
			if batch[1].Title != orig1Title || batch[1].Content != orig1Content {
				t.Fatalf("caller batch[1] mutated on rejection: got (title=%q, content=%q), want (title=%q, content=%q)",
					batch[1].Title, batch[1].Content, orig1Title, orig1Content)
			}

			db := openDB()
			defer func() { _ = db.Close() }()
			if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
				t.Fatalf("expected 0 observations in DB after rejection, got %d", obsCount)
			}
			if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
				t.Fatalf("expected 0 sessions in DB after rejection, got %d", sessCount)
			}
		})
	}

	t.Run("valid batch uses exact protected copies and leaves caller input unchanged", func(t *testing.T) {
		const canary1 = "canary_valid_b1"
		const canary2 = "canary_valid_b2"
		batch := []*domain.Observation{
			{Title: "Valid 1 <private>" + canary1 + "</private>", Content: "Body 1", Project: "p1", Type: "manual"},
			{Title: "Valid 2", Content: "Body 2 <private>" + canary2 + "</private>", Project: "p1", Type: "manual"},
		}
		orig0Title := batch[0].Title
		orig1Content := batch[1].Content

		staged, err := preflightObservations(batch)
		if err != nil {
			t.Fatalf("unexpected error on valid batch: %v", err)
		}
		if batch[0].Title != orig0Title || batch[1].Content != orig1Content {
			t.Fatalf("caller input mutated on valid preflight: title=%q content=%q", batch[0].Title, batch[1].Content)
		}
		if staged[0].Title != "Valid 1 [REDACTED]" || staged[1].Content != "Body 2 [REDACTED]" {
			t.Fatalf("staged protected copy mismatch: %+v, %+v", staged[0], staged[1])
		}
		if strings.Contains(staged[0].Title, canary1) || strings.Contains(staged[1].Content, canary2) {
			t.Fatalf("staged copy leaked canary")
		}
	})
}

func TestCLIPrivacy_ImportValidFirstInvalidSecondZeroEffects(t *testing.T) {
	const canaryT = "canary_import_v1_title_9988"
	const canaryC = "canary_import_v1_content_9988"

	cases := []struct {
		name      string
		badTitle  string
		badBody   string
		badJSON   string
		isRawJSON bool
	}{
		{
			name:     "pure private title in second item",
			badTitle: "<private>pure_secret_title</private>",
			badBody:  "Valid Body 2",
		},
		{
			name:     "pure private content in second item",
			badTitle: "Valid Title 2",
			badBody:  "<private>pure_secret_body</private>",
		},
		{
			name:     "unclosed marker title in second item",
			badTitle: "Bad Title <private>unclosed",
			badBody:  "Valid Body 2",
		},
		{
			name:     "unclosed marker content in second item",
			badTitle: "Valid Title 2",
			badBody:  "Valid Body 2 <private>unclosed",
		},
		{
			name:      "marker in project metadata in second item",
			isRawJSON: true,
			badJSON: `[
				{"title":"Valid Title <private>` + canaryT + `</private>","content":"Valid content <private>` + canaryC + `</private>","project":"p1","type":"manual","session_id":"s-imp-v1"},
				{"title":"Valid Title 2","content":"Valid Body 2","project":"<private>secret_proj</private>","type":"manual","session_id":"s-imp-v2"}
			]`,
		},
		{
			name:      "nil observation in second item",
			isRawJSON: true,
			badJSON: `[
				{"title":"Valid Title <private>` + canaryT + `</private>","content":"Valid content <private>` + canaryC + `</private>","project":"p1","type":"manual","session_id":"s-imp-v1"},
				null
			]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, openDB := setupCLITestDB(t)
			a, err := app.Open(context.Background(), app.Options{})
			if err != nil {
				t.Fatalf("init db: %v", err)
			}
			_ = a.Close()

			jsonContent := tc.badJSON
			if !tc.isRawJSON {
				item1 := domain.Observation{
					Title:     "Valid Title <private>" + canaryT + "</private>",
					Content:   "Valid content <private>" + canaryC + "</private>",
					Project:   "p1",
					Type:      "manual",
					SessionID: "s-imp-v1",
				}
				item2 := domain.Observation{
					Title:     tc.badTitle,
					Content:   tc.badBody,
					Project:   "p1",
					Type:      "manual",
					SessionID: "s-imp-bad2",
				}
				data, mErr := json.Marshal([]domain.Observation{item1, item2})
				if mErr != nil {
					t.Fatalf("marshal json: %v", mErr)
				}
				jsonContent = string(data)

				// Also verify programmatic preflightObservations ensures caller is unchanged
				batch := []*domain.Observation{&item1, &item2}
				orig0Title, orig0Content := item1.Title, item1.Content
				orig1Title, orig1Content := item2.Title, item2.Content

				staged, pErr := preflightObservations(batch)
				if pErr == nil {
					t.Fatalf("expected preflight error, got nil")
				}
				if staged != nil {
					t.Fatalf("expected nil staged on error, got %v", staged)
				}
				if batch[0].Title != orig0Title || batch[0].Content != orig0Content {
					t.Fatalf("caller batch[0] mutated: got (title=%q, content=%q), want (title=%q, content=%q)",
						batch[0].Title, batch[0].Content, orig0Title, orig0Content)
				}
				if batch[1].Title != orig1Title || batch[1].Content != orig1Content {
					t.Fatalf("caller batch[1] mutated: got (title=%q, content=%q), want (title=%q, content=%q)",
						batch[1].Title, batch[1].Content, orig1Title, orig1Content)
				}
			}

			jsonFile := filepath.Join(t.TempDir(), "atomic_valid_first_import.json")
			if err := os.WriteFile(jsonFile, []byte(jsonContent), 0o600); err != nil {
				t.Fatalf("write json file: %v", err)
			}

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := Run([]string{"cortex", "import", "--from-json", "--path", jsonFile}, stdout, stderr)
			if code != 1 {
				t.Fatalf("expected exit code 1 on rejected import, got %d (out=%q err=%q)", code, stdout.String(), stderr.String())
			}
			errStr := stderr.String()
			if strings.Contains(errStr, canaryT) || strings.Contains(errStr, canaryC) {
				t.Fatalf("stderr leaked canary: %q", errStr)
			}

			db := openDB()
			defer func() { _ = db.Close() }()

			if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
				t.Fatalf("expected 0 observations after rejected import, got %d", obsCount)
			}
			if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
				t.Fatalf("expected 0 sessions after rejected import, got %d", sessCount)
			}
		})
	}
}

func TestCLIPrivacy_ImportInvalidFieldsDoNotCreateSession(t *testing.T) {
	_, openDB := setupCLITestDB(t)
	a, err := app.Open(context.Background(), app.Options{})
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	_ = a.Close()

	jsonData := `[
		{"title":"","content":"Valid body 1","project":"p1","type":"manual","session_id":"orphan-s1"},
		{"title":"   ","content":"Valid body 2","project":"p1","type":"manual","session_id":"orphan-s2"},
		{"title":"Valid title 3","content":"","project":"p1","type":"manual","session_id":"orphan-s3"},
		{"title":"Valid title 4","content":" \t \n ","project":"p1","type":"manual","session_id":"orphan-s4"}
	]`

	jsonFile := filepath.Join(t.TempDir(), "invalid_fields_no_sessions.json")
	if err := os.WriteFile(jsonFile, []byte(jsonData), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{"cortex", "import", "--from-json", "--path", jsonFile}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d (err=%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cortex: import rejected:") {
		t.Fatalf("stderr missing rejection message, got: %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "Imported") {
		t.Fatalf("stdout should not contain imported summary on rejection, got: %s", stdout.String())
	}

	db := openDB()
	defer func() { _ = db.Close() }()

	if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
		t.Fatalf("expected 0 observations in DB, got %d", obsCount)
	}
	if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
		t.Fatalf("expected 0 sessions in DB (no orphan sessions), got %d", sessCount)
	}
}
