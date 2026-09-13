package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q failed: %v", query, err)
	}
	return count
}

func assertPayloadFree(t *testing.T, err error, privErr *privacy.Error, canary string) {
	t.Helper()
	data, _ := json.Marshal(privErr)
	txt, _ := privErr.MarshalText()
	for _, s := range []string{err.Error(), privErr.Field, privErr.Message, string(data), string(txt)} {
		if strings.Contains(s, canary) {
			t.Errorf("diagnostic leaked canary: %q", s)
		}
	}
}

func TestSessionPrivacy_SummaryRedactionAndPersistence(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const canaryCreate = "canary_sess_create_secret_7722"
	s1 := &domain.Session{ID: "sess-1", Project: "cortex", Directory: "/home/user/cortex", Summary: "Session started with <private>" + canaryCreate + "</private> work"}
	if err := store.Create(ctx, s1); err != nil || s1.Summary != "Session started with [REDACTED] work" {
		t.Fatalf("Create failed or not redacted: %v, summary=%q", err, s1.Summary)
	}
	var dbSummary string
	if err := store.db.QueryRow("SELECT summary FROM sessions WHERE id = ?", s1.ID).Scan(&dbSummary); err != nil || dbSummary != "Session started with [REDACTED] work" || strings.Contains(dbSummary, canaryCreate) {
		t.Fatalf("db summary invalid: %v, got=%q", err, dbSummary)
	}

	const canaryOptional = "canary_optional_pure_secret_3311"
	s2 := &domain.Session{ID: "sess-2", Project: "cortex", Directory: "/home/user/cortex", Summary: "<private>" + canaryOptional + "</private>"}
	if err := store.Create(ctx, s2); err != nil || s2.Summary != "[REDACTED]" {
		t.Fatalf("Create pure private failed: %v, summary=%q", err, s2.Summary)
	}

	const canaryEnd = "canary_sess_end_secret_9944"
	if err := store.End(ctx, s1.ID, "Ended with <private>"+canaryEnd+"</private> done"); err != nil {
		t.Fatalf("End failed: %v", err)
	}
	if err := store.db.QueryRow("SELECT summary FROM sessions WHERE id = ?", s1.ID).Scan(&dbSummary); err != nil || dbSummary != "Ended with [REDACTED] done" || strings.Contains(dbSummary, canaryEnd) {
		t.Fatalf("ended summary invalid: %v, got=%q", err, dbSummary)
	}

	s3 := &domain.Session{ID: "sess-3", Project: "cortex", Directory: "/home/user/cortex"}
	if err := store.Create(ctx, s3); err != nil || store.End(ctx, s3.ID, "<private>only_secret_end</private>") != nil {
		t.Fatalf("Create or End s3 failed")
	}
	if ret3, err := store.GetByID(ctx, s3.ID); err != nil || ret3.Summary != "[REDACTED]" {
		t.Errorf("retrieved sess3 summary unexpected: %v, summary=%q", err, ret3.Summary)
	}
}

func TestSessionPrivacy_LiteralPublicPlaceholders(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const canary = "canary_sess_lit_sec_9911"
	createCases := []struct {
		name, want string
		session    *domain.Session
	}{
		{"literal alone", "[REDACTED]", &domain.Session{ID: "s-lit-1", Project: "p", Directory: "/d", Summary: "[REDACTED]"}},
		{"literal with marker", "Session [REDACTED] [REDACTED]", &domain.Session{ID: "s-lit-2", Project: "p", Directory: "/d", Summary: "Session [REDACTED] <private>" + canary + "</private>"}},
		{"literal inside marker alone", "[REDACTED]", &domain.Session{ID: "s-lit-3", Project: "p", Directory: "/d", Summary: "<private>[REDACTED]</private>"}},
		{"literal in metadata", "", &domain.Session{ID: "[REDACTED]", Project: "[REDACTED]", Directory: "/dir/[REDACTED]"}},
	}
	for _, tc := range createCases {
		t.Run("Create "+tc.name, func(t *testing.T) {
			if err := store.Create(ctx, tc.session); err != nil || tc.session.Summary != tc.want {
				t.Fatalf("Create failed: %v, summary=%q", err, tc.session.Summary)
			}
			if got, err := store.GetByID(ctx, tc.session.ID); err != nil || got.Summary != tc.want {
				t.Errorf("GetByID failed: %v, summary=%q", err, got.Summary)
			}
		})
	}

	if list, err := store.List(ctx, "[REDACTED]"); err != nil || len(list) != 1 {
		t.Errorf("List by [REDACTED] failed: %v", err)
	}
	if recent, err := store.Recent(ctx, "[REDACTED]", 5); err != nil || len(recent) != 1 {
		t.Errorf("Recent by [REDACTED] failed: %v", err)
	}
	if ws, err := store.GetWithStats(ctx, "[REDACTED]"); err != nil || ws.Session.ID != "[REDACTED]" {
		t.Errorf("GetWithStats failed: %v", err)
	}
	if curr, err := store.GetCurrent(ctx, "[REDACTED]"); err != nil || curr.ID != "[REDACTED]" {
		t.Errorf("GetCurrent failed: %v", err)
	}

	endCases := []struct {
		name, sessID, summary, want string
	}{
		{"literal alone", "s-lit-1", "[REDACTED]", "[REDACTED]"},
		{"literal with marker", "s-lit-2", "End [REDACTED] <private>" + canary + "</private>", "End [REDACTED] [REDACTED]"},
		{"literal inside marker", "s-lit-3", "<private>[REDACTED]</private>", "[REDACTED]"},
	}
	for _, tc := range endCases {
		t.Run("End "+tc.name, func(t *testing.T) {
			if err := store.End(ctx, tc.sessID, tc.summary); err != nil {
				t.Fatalf("End failed: %v", err)
			}
			if got, err := store.GetByID(ctx, tc.sessID); err != nil || got.Summary != tc.want || got.EndedAt == nil {
				t.Errorf("GetByID after End failed: %v, summary=%q", err, got.Summary)
			}
		})
	}
}

func TestSessionPrivacy_CreateRejections_NoMutationZeroEffects(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := &domain.Session{ID: "base-sess", Project: "base-proj", Directory: "/base-dir"}
	if err := store.Create(ctx, base); err != nil {
		t.Fatalf("setup baseline failed: %v", err)
	}

	const canary = "canary_leak_proof_session_xyz"
	cases := []struct {
		name, wantField string
		session         *domain.Session
		wantCode        privacy.ErrorCode
	}{
		{"unclosed marker in summary", "summary", &domain.Session{ID: "s-1", Project: "p", Directory: "/d", Summary: "Public <private>" + canary}, privacy.ErrCodeInvalidMarker},
		{"stray closing marker in summary", "summary", &domain.Session{ID: "s-1", Project: "p", Directory: "/d", Summary: "Public </private>"}, privacy.ErrCodeInvalidMarker},
		{"nested marker in summary", "summary", &domain.Session{ID: "s-1", Project: "p", Directory: "/d", Summary: "Public <private>a <private>b</private></private>"}, privacy.ErrCodeInvalidMarker},
		{"malformed marker in summary", "summary", &domain.Session{ID: "s-1", Project: "p", Directory: "/d", Summary: "Public <private bad>" + canary + "</private>"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in summary", "summary", &domain.Session{ID: "s-1", Project: "p", Directory: "/d", Summary: "Public \xff " + canary}, privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in id", "metadata", &domain.Session{ID: "s/<private" + canary, Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"stray closing tag in id", "metadata", &domain.Session{ID: "s/</private" + canary, Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"malformed tag in id", "metadata", &domain.Session{ID: "s/<private bad>" + canary, Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"spaced tag in id", "metadata", &domain.Session{ID: "s/< private >" + canary, Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"spaced close tag in id", "metadata", &domain.Session{ID: "s/< / private >" + canary, Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"exact marker in id", "metadata", &domain.Session{ID: "s/<private>" + canary + "</private>", Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in id", "metadata", &domain.Session{ID: "s\xff" + canary, Project: "p", Directory: "/d"}, privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in project", "metadata", &domain.Session{ID: "s-1", Project: "p/<private" + canary, Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"malformed tag in project", "metadata", &domain.Session{ID: "s-1", Project: "p/<private bad=\"x\">" + canary, Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"exact marker in project", "metadata", &domain.Session{ID: "s-1", Project: "p/<private>" + canary + "</private>", Directory: "/d"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in project", "metadata", &domain.Session{ID: "s-1", Project: "p\xff" + canary, Directory: "/d"}, privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in directory", "metadata", &domain.Session{ID: "s-1", Project: "p", Directory: "/d/<private" + canary}, privacy.ErrCodeInvalidMarker},
		{"malformed tag in directory", "metadata", &domain.Session{ID: "s-1", Project: "p", Directory: "/d/<private bad>" + canary}, privacy.ErrCodeInvalidMarker},
		{"exact marker in directory", "metadata", &domain.Session{ID: "s-1", Project: "p", Directory: "/d/<private>" + canary + "</private>"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in directory", "metadata", &domain.Session{ID: "s-1", Project: "p", Directory: "/d\xff" + canary}, privacy.ErrCodeInvalidUTF8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := *tc.session
			err := store.Create(ctx, tc.session)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var privErr *privacy.Error
			if !errors.As(err, &privErr) || privErr.Code != tc.wantCode || privErr.Field != tc.wantField {
				t.Fatalf("expected code %q, field %q, got: %v (field=%s)", tc.wantCode, tc.wantField, err, privErr.Field)
			}
			assertPayloadFree(t, err, privErr, canary)
			if *tc.session != orig {
				t.Errorf("caller session struct was mutated on rejection")
			}
			if countRows(t, store.db, "SELECT count(*) FROM sessions") != 1 {
				t.Errorf("durable state dirty: rows changed on rejected create")
			}
			if s, err := store.GetByID(ctx, base.ID); err != nil || s.Project != "base-proj" {
				t.Errorf("baseline session corrupted: %v", err)
			}
		})
	}
}

func TestSessionPrivacy_EndRejections_NoMutationZeroEffects(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	activeSess := &domain.Session{ID: "sess-active", Project: "cortex", Directory: "/home/user/cortex"}
	if err := store.Create(ctx, activeSess); err != nil {
		t.Fatalf("setup active session failed: %v", err)
	}

	const canary = "canary_leak_proof_end_xyz"
	cases := []struct {
		name, id, summary, wantField string
		wantCode                     privacy.ErrorCode
	}{
		{"unclosed marker in summary", "sess-active", "Summary <private>" + canary, "summary", privacy.ErrCodeInvalidMarker},
		{"stray closing marker in summary", "sess-active", "Summary </private>", "summary", privacy.ErrCodeInvalidMarker},
		{"nested marker in summary", "sess-active", "Summary <private><private></private></private>", "summary", privacy.ErrCodeInvalidMarker},
		{"malformed marker in summary", "sess-active", "Summary <private bad>" + canary + "</private>", "summary", privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in summary", "sess-active", "Summary \xff " + canary, "summary", privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in id", "sess/<private" + canary, "Safe summary", "metadata", privacy.ErrCodeInvalidMarker},
		{"stray closing tag in id", "sess/</private" + canary, "Safe summary", "metadata", privacy.ErrCodeInvalidMarker},
		{"malformed tag in id", "sess/<private bad>" + canary, "Safe summary", "metadata", privacy.ErrCodeInvalidMarker},
		{"spaced tag in id", "sess/< private >" + canary, "Safe summary", "metadata", privacy.ErrCodeInvalidMarker},
		{"spaced close tag in id", "sess/< / private >" + canary, "Safe summary", "metadata", privacy.ErrCodeInvalidMarker},
		{"exact marker in id", "sess/<private>" + canary + "</private>", "Safe summary", "metadata", privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in id", "sess\xff" + canary, "Safe summary", "metadata", privacy.ErrCodeInvalidUTF8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.End(ctx, tc.id, tc.summary)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var privErr *privacy.Error
			if !errors.As(err, &privErr) || privErr.Code != tc.wantCode || privErr.Field != tc.wantField {
				t.Fatalf("expected code %q, field %q, got: %v (field=%s)", tc.wantCode, tc.wantField, err, privErr.Field)
			}
			assertPayloadFree(t, err, privErr, canary)

			s, err := store.GetByID(ctx, "sess-active")
			if err != nil || s.EndedAt != nil || s.Summary != "" {
				t.Errorf("active session was modified on rejected End(): %+v", s)
			}
			if countRows(t, store.db, "SELECT count(*) FROM sessions WHERE ended_at IS NOT NULL") != 0 {
				t.Errorf("prior state dirty: ended session persisted on rejected End()")
			}
		})
	}
}
