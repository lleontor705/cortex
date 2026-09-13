package prompt

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

func TestPromptPrivacy_RedactionAndPersistence(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const canarySecret = "canary_token_prompt_secret_9988"
	p := &domain.Prompt{Content: "Please refactor <private>" + canarySecret + "</private> logic safely", Project: "test-project", SessionID: "sess-1"}
	if err := store.Save(ctx, p); err != nil || p.ID == 0 || p.CreatedAt.IsZero() || p.Content != "Please refactor [REDACTED] logic safely" {
		t.Fatalf("Save failed or content not redacted: %v, content=%q", err, p.Content)
	}

	var dbContent string
	if err := store.db.QueryRow("SELECT content FROM user_prompts WHERE id = ?", p.ID).Scan(&dbContent); err != nil || dbContent != "Please refactor [REDACTED] logic safely" || strings.Contains(dbContent, canarySecret) {
		t.Fatalf("db content invalid: %v, got=%q", err, dbContent)
	}
	if countRows(t, store.db, "SELECT count(*) FROM prompts_fts WHERE prompts_fts MATCH '\"[REDACTED]\"'") != 1 || countRows(t, store.db, "SELECT count(*) FROM prompts_fts WHERE prompts_fts MATCH '\"' || ? || '\"'", canarySecret) != 0 {
		t.Fatalf("FTS index invalid for redacted or canary")
	}
	if ret, err := store.GetByID(ctx, p.ID); err != nil || ret.Content != "Please refactor [REDACTED] logic safely" {
		t.Errorf("GetByID failed: %v, content=%q", err, ret.Content)
	}
	if sCanary, err := store.Search(ctx, canarySecret, "test-project", 10); err != nil || len(sCanary) != 0 {
		t.Errorf("search canary unexpected: %v, len=%d", err, len(sCanary))
	}
	if sRedacted, err := store.Search(ctx, "[REDACTED]", "test-project", 10); err != nil || len(sRedacted) != 1 {
		t.Errorf("search redacted unexpected: %v, len=%d", err, len(sRedacted))
	}
}

func TestPromptPrivacy_LiteralPublicPlaceholders(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const canary = "canary_lit_sec_8811"
	cases := []struct {
		name, want string
		prompt     *domain.Prompt
		wantCode   privacy.ErrorCode
	}{
		{"literal alone", "[REDACTED]", &domain.Prompt{Content: "[REDACTED]", Project: "proj-1", SessionID: "sess-1"}, ""},
		{"literal with marker", "Review [REDACTED] [REDACTED]", &domain.Prompt{Content: "Review [REDACTED] <private>" + canary + "</private>", Project: "proj-1", SessionID: "sess-1"}, ""},
		{"literal inside marker alone", "", &domain.Prompt{Content: "<private>[REDACTED]</private>", Project: "proj-1", SessionID: "sess-1"}, privacy.ErrCodeRequiredEmpty},
		{"literal inside marker with residual", "Prefix [REDACTED] suffix", &domain.Prompt{Content: "Prefix <private>[REDACTED]</private> suffix", Project: "proj-1", SessionID: "sess-1"}, ""},
		{"literal in metadata", "Public prompt", &domain.Prompt{Content: "Public prompt", Project: "[REDACTED]", SessionID: "[REDACTED]", PublicID: "[REDACTED]"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := *tc.prompt
			err := store.Save(ctx, tc.prompt)
			if tc.wantCode != "" {
				var privErr *privacy.Error
				if !errors.As(err, &privErr) || privErr.Code != tc.wantCode || *tc.prompt != orig {
					t.Fatalf("expected code %q and no mutation, got code %v, struct=%+v", tc.wantCode, privErr, *tc.prompt)
				}
				return
			}
			if err != nil || tc.prompt.Content != tc.want {
				t.Fatalf("Save failed or content mismatched: %v, content=%q", err, tc.prompt.Content)
			}
			if got, err := store.GetByID(ctx, tc.prompt.ID); err != nil || got.Content != tc.want {
				t.Errorf("GetByID failed: %v, content=%q", err, got.Content)
			}
		})
	}
	if list, err := store.List(ctx, "[REDACTED]", 10); err != nil || len(list) != 1 {
		t.Errorf("List by [REDACTED] failed: %v", err)
	}
	if list, err := store.ListBySession(ctx, "[REDACTED]"); err != nil || len(list) != 1 {
		t.Errorf("ListBySession by [REDACTED] failed: %v", err)
	}
	if search, err := store.Search(ctx, "[REDACTED]", "", 10); err != nil || len(search) == 0 {
		t.Errorf("Search for [REDACTED] failed: %v", err)
	}
}

func TestPromptPrivacy_Rejections_NoMutationZeroEffects(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := &domain.Prompt{Content: "Persistent baseline content", Project: "base-proj", SessionID: "base-sess"}
	if err := store.Save(ctx, base); err != nil {
		t.Fatalf("setup baseline failed: %v", err)
	}
	const canary = "canary_leak_proof_xyz_4421"
	cases := []struct {
		name, wantField string
		prompt          *domain.Prompt
		wantCode        privacy.ErrorCode
	}{
		{"unclosed marker in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: "Public <private>" + canary}, privacy.ErrCodeInvalidMarker},
		{"stray closing marker in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: "Public </private>"}, privacy.ErrCodeInvalidMarker},
		{"nested marker in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: "Public <private>a <private>b</private></private>"}, privacy.ErrCodeInvalidMarker},
		{"malformed marker in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: "Public <private bad=\"x\">" + canary + "</private>"}, privacy.ErrCodeInvalidMarker},
		{"empty public residual in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: "<private>" + canary + "</private>"}, privacy.ErrCodeRequiredEmpty},
		{"whitespace residual in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: " \t <private>" + canary + "</private> \n"}, privacy.ErrCodeRequiredEmpty},
		{"invalid utf8 in content", "content", &domain.Prompt{SessionID: "s", Project: "p", Content: "Public \xff invalid " + canary}, privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p/<private" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"stray closing tag in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p/</private" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"malformed tag in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p/<private bad>" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"spaced tag in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p/< private >" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"spaced close tag in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p/< / private >" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"exact marker in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p/<private>" + canary + "</private>", Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in project", "metadata", &domain.Prompt{SessionID: "s", Project: "p\xff" + canary, Content: "Public"}, privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in session_id", "metadata", &domain.Prompt{SessionID: "s/<private" + canary, Project: "p", Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"malformed tag in session_id", "metadata", &domain.Prompt{SessionID: "s/<private bad>" + canary, Project: "p", Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"exact marker in session_id", "metadata", &domain.Prompt{SessionID: "s/<private>" + canary + "</private>", Project: "p", Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in session_id", "metadata", &domain.Prompt{SessionID: "s\xff" + canary, Project: "p", Content: "Public"}, privacy.ErrCodeInvalidUTF8},
		{"unclosed tag in public_id", "metadata", &domain.Prompt{SessionID: "s", Project: "p", PublicID: "pub/<private" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"malformed tag in public_id", "metadata", &domain.Prompt{SessionID: "s", Project: "p", PublicID: "pub/<private bad>" + canary, Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"exact marker in public_id", "metadata", &domain.Prompt{SessionID: "s", Project: "p", PublicID: "pub/<private>" + canary + "</private>", Content: "Public"}, privacy.ErrCodeInvalidMarker},
		{"invalid utf8 in public_id", "metadata", &domain.Prompt{SessionID: "s", Project: "p", PublicID: "pub\xff" + canary, Content: "Public"}, privacy.ErrCodeInvalidUTF8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := *tc.prompt
			err := store.Save(ctx, tc.prompt)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var privErr *privacy.Error
			if !errors.As(err, &privErr) || privErr.Code != tc.wantCode || privErr.Field != tc.wantField {
				t.Fatalf("expected code %q, field %q, got: %v (field=%s)", tc.wantCode, tc.wantField, err, privErr.Field)
			}
			assertPayloadFree(t, err, privErr, canary)
			if *tc.prompt != orig {
				t.Errorf("caller prompt struct was mutated on rejection")
			}
			if countRows(t, store.db, "SELECT count(*) FROM user_prompts") != 1 || countRows(t, store.db, "SELECT count(*) FROM prompts_fts") != 1 {
				t.Errorf("durable state dirty: rows changed on rejected save")
			}
			if b, err := store.GetByID(ctx, base.ID); err != nil || b.Content != "Persistent baseline content" {
				t.Errorf("baseline prompt corrupted: %v", err)
			}
		})
	}
}
