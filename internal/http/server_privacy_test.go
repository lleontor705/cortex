package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

type spyObservationImporter struct {
	ObservationStore
	importDataCalls int
	importedData    []*sqlitestore.ExportData
}

func (s *spyObservationImporter) ImportData(ctx context.Context, data *sqlitestore.ExportData) (*sqlitestore.SyncImportResult, error) {
	s.importDataCalls++
	if data != nil {
		s.importedData = append(s.importedData, cloneExportData(data))
	}
	if s.ObservationStore != nil {
		return s.ObservationStore.ImportData(ctx, data)
	}
	return &sqlitestore.SyncImportResult{}, nil
}

func cloneExportData(data *sqlitestore.ExportData) *sqlitestore.ExportData {
	if data == nil {
		return nil
	}
	cp := *data
	if len(data.Observations) > 0 {
		cp.Observations = make([]*domain.Observation, len(data.Observations))
		for i, o := range data.Observations {
			if o != nil {
				obsCp := *o
				if len(o.Tags) > 0 {
					obsCp.Tags = append([]string(nil), o.Tags...)
				}
				cp.Observations[i] = &obsCp
			}
		}
	}
	if len(data.Sessions) > 0 {
		cp.Sessions = make([]*domain.Session, len(data.Sessions))
		for i, sess := range data.Sessions {
			if sess != nil {
				sessCp := *sess
				cp.Sessions[i] = &sessCp
			}
		}
	}
	if len(data.Prompts) > 0 {
		cp.Prompts = make([]*domain.Prompt, len(data.Prompts))
		for i, p := range data.Prompts {
			if p != nil {
				promptCp := *p
				cp.Prompts[i] = &promptCp
			}
		}
	}
	return &cp
}

func assertZeroPersisted(t *testing.T, srv *Server) {
	t.Helper()
	ctx := context.Background()
	obsList, err := srv.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list observations failed: %v", err)
	}
	sessions, err := srv.deps.Sessions.List(ctx, "")
	if err != nil {
		t.Fatalf("list sessions failed: %v", err)
	}
	var obsCount, sessCount, promptCount int
	if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM observations").Scan(&obsCount); err != nil {
		t.Fatalf("count observations failed: %v", err)
	}
	if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&sessCount); err != nil {
		t.Fatalf("count sessions failed: %v", err)
	}
	if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM user_prompts").Scan(&promptCount); err != nil {
		t.Fatalf("count user_prompts failed: %v", err)
	}
	if len(obsList) != 0 || len(sessions) != 0 || obsCount != 0 || sessCount != 0 || promptCount != 0 {
		t.Fatalf("expected zero persisted records, got %d obs (%d db), %d sess (%d db), %d prompts",
			len(obsList), obsCount, len(sessions), sessCount, promptCount)
	}
}

func TestHTTPPrivacy_ImportAtomicRejection(t *testing.T) {
	const canary = "canary_http_import_secret_4455"
	srv := setupTestServer(t)
	spy := &spyObservationImporter{ObservationStore: srv.deps.Observations}
	srv.deps.Observations = spy
	ctx := context.Background()
	body := `{"sessions":[{"id":"s1","project":"demo","directory":"."}],"observations":[{"session_id":"s1","title":"Valid 1","content":"Body 1","project":"demo","type":"manual"},{"session_id":"s1","title":"Bad Title <private>` + canary + `","content":"Body 2","project":"demo","type":"manual"}]}`
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/import", bytes.NewBufferString(body)))
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), canary) {
		t.Fatalf("expected 400 without canary, got %d: %s", w.Code, w.Body.String())
	}
	if spy.importDataCalls != 0 || len(spy.importedData) != 0 {
		t.Fatalf("expected 0 ImportData collaborator calls on rejected import, got %d", spy.importDataCalls)
	}
	obsList, _ := srv.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 10})
	sessions, _ := srv.deps.Sessions.List(ctx, "")
	var promptCount int
	if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM user_prompts").Scan(&promptCount); err != nil {
		t.Fatalf("count user_prompts failed: %v", err)
	}
	if len(obsList) != 0 || len(sessions) != 0 || promptCount != 0 {
		t.Fatalf("atomic import failed: %d obs, %d sess, %d prompts", len(obsList), len(sessions), promptCount)
	}
	assertZeroPersisted(t, srv)
}

func TestHTTPPrivacy_ImportRedacted(t *testing.T) {
	const canary = "canary_http_import_redact_6677"
	srv := setupTestServer(t)
	spy := &spyObservationImporter{ObservationStore: srv.deps.Observations}
	srv.deps.Observations = spy
	ctx := context.Background()

	exportData := sqlitestore.ExportData{
		Version:    "0.1.0",
		ExportedAt: "2026-09-11T12:00:00Z",
		Sessions: []*domain.Session{
			{ID: "s-redact-1", Project: "demo", Directory: "."},
		},
		Observations: []*domain.Observation{
			{SessionID: "s-redact-1", Title: "Import <private>" + canary + "</private> Title", Content: "Body with <private>" + canary + "</private>", Project: "demo", Type: "manual"},
		},
	}
	body, _ := json.Marshal(exportData)
	req := httptest.NewRequest("POST", "/api/import", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), canary) {
		t.Fatalf("response leaked canary: %s", w.Body.String())
	}
	if spy.importDataCalls != 1 || len(spy.importedData) != 1 {
		t.Fatalf("expected exactly 1 ImportData collaborator call, got %d", spy.importDataCalls)
	}
	if len(spy.importedData[0].Observations) != 1 {
		t.Fatalf("expected 1 imported observation in collaborator args, got %d", len(spy.importedData[0].Observations))
	}
	if spy.importedData[0].Observations[0].Title != "Import [REDACTED] Title" || spy.importedData[0].Observations[0].Content != "Body with [REDACTED]" {
		t.Fatalf("collaborator spy received unexpected text: title=%q content=%q", spy.importedData[0].Observations[0].Title, spy.importedData[0].Observations[0].Content)
	}
	if strings.Contains(spy.importedData[0].Observations[0].Title, canary) || strings.Contains(spy.importedData[0].Observations[0].Content, canary) {
		t.Fatalf("collaborator spy argument leaked canary: title=%q content=%q", spy.importedData[0].Observations[0].Title, spy.importedData[0].Observations[0].Content)
	}

	obsList, err := srv.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 10})
	if err != nil || len(obsList) != 1 {
		t.Fatalf("expected 1 observation, got %d (err=%v)", len(obsList), err)
	}
	if strings.Contains(obsList[0].Title, canary) || strings.Contains(obsList[0].Content, canary) {
		t.Fatalf("persisted canary leak: title=%q content=%q", obsList[0].Title, obsList[0].Content)
	}
	if obsList[0].Title != "Import [REDACTED] Title" || obsList[0].Content != "Body with [REDACTED]" {
		t.Fatalf("unexpected persisted text: title=%q content=%q", obsList[0].Title, obsList[0].Content)
	}
}

func TestHTTPPrivacy_ImportCollaboratorSpy(t *testing.T) {
	const (
		canaryT = "canary_http_spy_title_1234"
		canaryC = "canary_http_spy_content_5678"
		canaryS = "canary_http_spy_summary_9012"
		canaryP = "canary_http_spy_prompt_3456"
	)

	t.Run("ValidPositiveControlExactProtectedArguments", func(t *testing.T) {
		srv := setupTestServer(t)
		spy := &spyObservationImporter{ObservationStore: srv.deps.Observations}
		srv.deps.Observations = spy
		ctx := context.Background()

		validPayload := sqlitestore.ExportData{
			Version:    "0.1.0",
			ExportedAt: "2026-09-11T12:00:00Z",
			Sessions: []*domain.Session{
				{ID: "s-spy-1", Project: "proj-spy", Directory: ".", Summary: "Session summary with <private>" + canaryS + "</private>"},
			},
			Observations: []*domain.Observation{
				{
					SessionID: "s-spy-1",
					Title:     "Valid <private>" + canaryT + "</private> Title",
					Content:   "Valid <private>" + canaryC + "</private> Content",
					Project:   "proj-spy",
					Type:      "manual",
					Scope:     "project",
					Tags:      []string{"privacy", "tested"},
				},
				{
					SessionID: "s-spy-1",
					Title:     "Second Plain Title",
					Content:   "Second Plain Content",
					Project:   "proj-spy",
					Type:      "manual",
					Scope:     "project",
				},
			},
			Prompts: []*domain.Prompt{
				{
					SessionID: "s-spy-1",
					Project:   "proj-spy",
					Content:   "Prompt text with <private>" + canaryP + "</private> info",
				},
			},
		}

		bodyBytes, err := json.Marshal(validPayload)
		if err != nil {
			t.Fatalf("marshal export payload: %v", err)
		}

		w := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/import", bytes.NewReader(bodyBytes)))

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		for _, canary := range []string{canaryT, canaryC, canaryS, canaryP} {
			if strings.Contains(w.Body.String(), canary) {
				t.Fatalf("response leaked canary %q: %s", canary, w.Body.String())
			}
		}

		if spy.importDataCalls != 1 {
			t.Fatalf("expected exactly 1 ImportData call on valid import, got %d", spy.importDataCalls)
		}
		if len(spy.importedData) != 1 {
			t.Fatalf("expected exactly 1 captured ImportData payload, got %d", len(spy.importedData))
		}

		captured := spy.importedData[0]
		if len(captured.Observations) != 2 {
			t.Fatalf("expected 2 observations passed to ImportData, got %d", len(captured.Observations))
		}
		if captured.Observations[0].Title != "Valid [REDACTED] Title" {
			t.Fatalf("expected protected title %q, got %q", "Valid [REDACTED] Title", captured.Observations[0].Title)
		}
		if captured.Observations[0].Content != "Valid [REDACTED] Content" {
			t.Fatalf("expected protected content %q, got %q", "Valid [REDACTED] Content", captured.Observations[0].Content)
		}
		if captured.Observations[1].Title != "Second Plain Title" || captured.Observations[1].Content != "Second Plain Content" {
			t.Fatalf("unexpected second observation text: %+v", captured.Observations[1])
		}

		if len(captured.Sessions) != 1 {
			t.Fatalf("expected 1 session passed to ImportData, got %d", len(captured.Sessions))
		}
		if captured.Sessions[0].Summary != "Session summary with [REDACTED]" {
			t.Fatalf("expected protected session summary %q, got %q", "Session summary with [REDACTED]", captured.Sessions[0].Summary)
		}

		if len(captured.Prompts) != 1 {
			t.Fatalf("expected 1 prompt passed to ImportData, got %d", len(captured.Prompts))
		}
		if captured.Prompts[0].Content != "Prompt text with [REDACTED] info" {
			t.Fatalf("expected protected prompt content %q, got %q", "Prompt text with [REDACTED] info", captured.Prompts[0].Content)
		}

		for _, canary := range []string{canaryT, canaryC, canaryS, canaryP} {
			for i, obs := range captured.Observations {
				if strings.Contains(obs.Title, canary) || strings.Contains(obs.Content, canary) {
					t.Fatalf("captured observation[%d] leaked canary %q: title=%q content=%q", i, canary, obs.Title, obs.Content)
				}
			}
			for i, sess := range captured.Sessions {
				if strings.Contains(sess.Summary, canary) {
					t.Fatalf("captured session[%d] leaked canary %q: summary=%q", i, canary, sess.Summary)
				}
			}
			for i, p := range captured.Prompts {
				if strings.Contains(p.Content, canary) {
					t.Fatalf("captured prompt[%d] leaked canary %q: content=%q", i, canary, p.Content)
				}
			}
		}

		persistedObs, err := srv.deps.Observations.List(ctx, domain.ObservationFilter{Project: "proj-spy", Limit: 10})
		if err != nil || len(persistedObs) != 2 {
			t.Fatalf("expected 2 persisted observations, got %d (err=%v)", len(persistedObs), err)
		}
		persistedSessions, err := srv.deps.Sessions.List(ctx, "proj-spy")
		if err != nil || len(persistedSessions) != 1 {
			t.Fatalf("expected 1 persisted session, got %d (err=%v)", len(persistedSessions), err)
		}
		if persistedSessions[0].Summary != "Session summary with [REDACTED]" {
			t.Fatalf("unexpected persisted summary: %q", persistedSessions[0].Summary)
		}
		persistedPrompts, err := srv.deps.Prompts.List(ctx, "proj-spy", 10)
		if err != nil || len(persistedPrompts) != 1 {
			t.Fatalf("expected 1 persisted prompt, got %d (err=%v)", len(persistedPrompts), err)
		}
		if persistedPrompts[0].Content != "Prompt text with [REDACTED] info" {
			t.Fatalf("unexpected persisted prompt content: %q", persistedPrompts[0].Content)
		}
		var promptCount int
		if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM user_prompts").Scan(&promptCount); err != nil || promptCount != 1 {
			t.Fatalf("expected 1 user_prompts row in DB, got %d (err=%v)", promptCount, err)
		}
		var dbHash string
		if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT normalized_hash FROM observations WHERE id = ?", persistedObs[0].ID).Scan(&dbHash); err != nil {
			t.Fatalf("query normalized_hash: %v", err)
		}
		normExpected := strings.ToLower(strings.TrimSpace("Valid [REDACTED] Content"))
		normExpected = strings.Join(strings.Fields(normExpected), " ")
		h := sha256.Sum256([]byte(normExpected))
		wantHash := fmt.Sprintf("%x", h)
		if dbHash != wantHash {
			t.Fatalf("hash oracle mismatch for imported obs: got %q, want %q", dbHash, wantHash)
		}
	})

	t.Run("InvalidSecondRecordZeroCollaboratorInvocations", func(t *testing.T) {
		const canary = "canary_http_spy_malformed_7890"

		cases := []struct {
			name     string
			rawJSON  string
			wantCode string
		}{
			{
				name:     "observation unclosed title",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Bad <private>` + canary + `","content":"Body"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "observation stray closing title",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Bad </private>","content":"Body"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "observation unclosed content",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Title","content":"Body <private>` + canary + `"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "observation nested content",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Title","content":"<private>a <private>b</private></private>"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "observation pure private title",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"<private>` + canary + `</private>","content":"Body"}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "observation empty title",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"","content":"Body"}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "observation whitespace title",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"   ","content":"Body"}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "observation pure private content",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Title","content":"<private>` + canary + `</private>"}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "observation empty content",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Title","content":""}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "observation whitespace content",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Title","content":"   "}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "observation marker in project metadata",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"<private>` + canary + `</private>","type":"manual","title":"Title","content":"Body"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "observation marker in tags metadata",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},{"session_id":"s1","project":"p","type":"manual","title":"Title","content":"Body","tags":["<private>` + canary + `</private>"]}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "observation nil second record",
				rawJSON:  `{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"},null]}`,
				wantCode: "nil_envelope",
			},
			{
				name:     "session second record marker in project",
				rawJSON:  `{"sessions":[{"id":"s1","project":"p","directory":"."},{"id":"s2","project":"<private>` + canary + `</private>","directory":"."}],"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "session second record nil",
				rawJSON:  `{"sessions":[{"id":"s1","project":"p","directory":"."},null],"observations":[{"session_id":"s1","project":"p","type":"manual","title":"Valid <private>` + canary + `</private> Title","content":"Body"}]}`,
				wantCode: "nil_envelope",
			},
			{
				name:     "prompt second record pure private content",
				rawJSON:  `{"sessions":[{"id":"s1","project":"p","directory":"."}],"prompts":[{"session_id":"s1","project":"p","content":"Valid <private>` + canary + `</private> Prompt"},{"session_id":"s1","project":"p","content":"<private>` + canary + `</private>"}]}`,
				wantCode: "privacy_required_empty",
			},
			{
				name:     "prompt second record unclosed marker",
				rawJSON:  `{"sessions":[{"id":"s1","project":"p","directory":"."}],"prompts":[{"session_id":"s1","project":"p","content":"Valid <private>` + canary + `</private> Prompt"},{"session_id":"s1","project":"p","content":"Prompt <private>` + canary + `"}]}`,
				wantCode: "privacy_invalid_marker",
			},
			{
				name:     "prompt second record nil",
				rawJSON:  `{"sessions":[{"id":"s1","project":"p","directory":"."}],"prompts":[{"session_id":"s1","project":"p","content":"Valid <private>` + canary + `</private> Prompt"},null]}`,
				wantCode: "nil_envelope",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := setupTestServer(t)
				spy := &spyObservationImporter{ObservationStore: srv.deps.Observations}
				srv.deps.Observations = spy
				ctx := context.Background()

				w := httptest.NewRecorder()
				srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/import", bytes.NewBufferString(tc.rawJSON)))

				if w.Code != http.StatusBadRequest {
					t.Fatalf("expected 400 Bad Request, got %d: %s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), canary) {
					t.Fatalf("response leaked canary: %s", w.Body.String())
				}
				if tc.wantCode != "" && !strings.Contains(w.Body.String(), tc.wantCode) {
					t.Fatalf("response missing expected code %q: %s", tc.wantCode, w.Body.String())
				}

				if spy.importDataCalls != 0 {
					t.Fatalf("expected exactly 0 ImportData collaborator calls, got %d", spy.importDataCalls)
				}
				if len(spy.importedData) != 0 {
					t.Fatalf("expected 0 captured ImportData payloads, got %d", len(spy.importedData))
				}

				obsList, err := srv.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 10})
				if err != nil {
					t.Fatalf("list observations: %v", err)
				}
				sessions, err := srv.deps.Sessions.List(ctx, "")
				if err != nil {
					t.Fatalf("list sessions: %v", err)
				}
				var promptCount int
				if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM user_prompts").Scan(&promptCount); err != nil {
					t.Fatalf("count user_prompts: %v", err)
				}
				if len(obsList) != 0 || len(sessions) != 0 || promptCount != 0 {
					t.Fatalf("database side effects present: %d obs, %d sess, %d prompts", len(obsList), len(sessions), promptCount)
				}
				assertZeroPersisted(t, srv)
			})
		}
	})
}
