package bundle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	"github.com/lleontor705/cortex/v2/internal/migration"
	entitystore "github.com/lleontor705/cortex/v2/internal/store/entity"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func buildPrivacyFullStores(t *testing.T, db *sql.DB) *Stores {
	t.Helper()
	return &Stores{
		Observations: sqlitestore.NewStore(db),
		Outbox:       sqlitestore.NewOutboxStore(db),
		Entities:     entitystore.NewStore(db),
		UnitOfWork:   NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig()),
	}
}

func setupPrivacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatalf("new v2 baseline: %v", err)
	}
	if err := baseline.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply v2 baseline: %v", err)
	}
	return db
}

func outboxIntentCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	return countTableRows(t, db, "index_outbox")
}

type spyCollaboratorObserver struct {
	invocations int
	observed    []*domain.Observation
	failErr     error
}

func (s *spyCollaboratorObserver) Observe(obs *domain.Observation) error {
	s.invocations++
	if obs != nil {
		cp := *obs
		s.observed = append(s.observed, &cp)
	}
	return s.failErr
}

type spyPrivacyUOW struct {
	inner         domain.UnitOfWork
	doCalls       int
	failAfterFunc error
}

func (s *spyPrivacyUOW) Do(ctx context.Context, tctx *domain.TenantContext, participants []domain.TxParticipant, fn func(context.Context) error) error {
	s.doCalls++
	return s.inner.Do(ctx, tctx, participants, func(txCtx context.Context) error {
		if err := fn(txCtx); err != nil {
			return err
		}
		return s.failAfterFunc
	})
}

func countTableRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); err != nil {
		t.Fatalf("count rows in %s: %v", table, err)
	}
	return count
}

func TestBundlePrivacy_RedactionHappyPathAndEntityProtection(t *testing.T) {
	db := setupPrivacyDB(t)
	stores := buildPrivacyFullStores(t, db)
	ctx := context.Background()

	const canaryTitle, canarySecret, canaryURL, canaryFile = "sec_title_9182", "canary_tok_8271", "https://canary-vault.internal/secret", "secret_creds.env"
	obs := &domain.Observation{
		SessionID: "sess-priv-1", Type: domain.TypeManual, Project: "cortex", Scope: "project",
		Title:   "Cluster <private>" + canaryTitle + "</private> note",
		Content: "Read valid_config.yaml and connect to <private>" + canaryURL + "</private> with file <private>" + canaryFile + "</private> key <private>" + canarySecret + "</private>",
	}

	effect, err := SaveWithEffect(ctx, stores, obs)
	if err != nil || effect.Status != domain.WriteStatusCreated || effect.Observation == nil || effect.Observation.ID == 0 {
		t.Fatalf("SaveWithEffect failed: effect=%+v, err=%v", effect, err)
	}

	for _, text := range []string{effect.Observation.Title, effect.Observation.Content} {
		if strings.Contains(text, canaryTitle) || strings.Contains(text, canarySecret) || strings.Contains(text, canaryURL) || strings.Contains(text, canaryFile) {
			t.Fatalf("durable observation leaked canary: %q", text)
		}
	}
	if !strings.Contains(effect.Observation.Title, "[REDACTED]") || !strings.Contains(effect.Observation.Content, "[REDACTED]") {
		t.Fatalf("durable effect missing [REDACTED]: %+v", effect.Observation)
	}
	if !strings.Contains(obs.Title, canaryTitle) || !strings.Contains(obs.Content, canarySecret) {
		t.Fatal("successful save mutated caller-owned prose")
	}

	var savedTitle, savedContent string
	if err := db.QueryRow("SELECT title, content FROM observations WHERE id = ?", obs.ID).Scan(&savedTitle, &savedContent); err != nil {
		t.Fatalf("query observation: %v", err)
	}
	if strings.Contains(savedContent, canarySecret) || strings.Contains(savedTitle, canaryTitle) {
		t.Fatalf("database observation leaked secrets: title=%q content=%q", savedTitle, savedContent)
	}

	var entityCount, publicFileCount int
	_ = db.QueryRow("SELECT count(*) FROM entity_links WHERE entity_value LIKE '%canary%' OR entity_value LIKE '%secret%'").Scan(&entityCount)
	_ = db.QueryRow("SELECT count(*) FROM entity_links WHERE entity_value = 'valid_config.yaml'").Scan(&publicFileCount)
	if entityCount != 0 || publicFileCount != 1 {
		t.Fatalf("unexpected entity links: canary=%d, valid=%d", entityCount, publicFileCount)
	}
	if count := outboxIntentCount(t, db); count != 1 {
		t.Fatalf("expected 1 outbox intent, got %d", count)
	}
}

func TestBundlePrivacy_RejectionZeroEffects(t *testing.T) {
	const canary = "canary_leak_check_9941"
	cases := []struct {
		name     string
		obs      *domain.Observation
		wantCode privacy.ErrorCode
	}{
		{"unclosed marker", &domain.Observation{Title: "T", Content: "Public <private>" + canary, Project: "p", Scope: "project"}, privacy.ErrCodeInvalidMarker},
		{"stray closing marker", &domain.Observation{Title: "T", Content: "Public </private>", Project: "p", Scope: "project"}, privacy.ErrCodeInvalidMarker},
		{"nested marker", &domain.Observation{Title: "T", Content: "Public <private>a <private>b</private></private>", Project: "p", Scope: "project"}, privacy.ErrCodeInvalidMarker},
		{"attribute marker", &domain.Observation{Title: "T", Content: "Public <private kind=\"x\">" + canary + "</private>", Project: "p", Scope: "project"}, privacy.ErrCodeInvalidMarker},
		{"empty public residual", &domain.Observation{Title: "T", Content: "<private>" + canary + "</private>", Project: "p", Scope: "project"}, privacy.ErrCodeRequiredEmpty},
		{"whitespace public residual", &domain.Observation{Title: "T", Content: "  \t \n <private>" + canary + "</private> \n ", Project: "p", Scope: "project"}, privacy.ErrCodeRequiredEmpty},
		{"invalid utf8", &domain.Observation{Title: "T", Content: "Public \xff\xfe " + canary, Project: "p", Scope: "project"}, privacy.ErrCodeInvalidUTF8},
		{"marker in tag", &domain.Observation{Title: "T", Content: "Public", Tags: []string{"<private>" + canary + "</private>"}, Project: "p", Scope: "project"}, privacy.ErrCodeInvalidMarker},
		{"marker in project", &domain.Observation{Title: "T", Content: "Public", Project: "<private>" + canary + "</private>", Scope: "project"}, privacy.ErrCodeInvalidMarker},
		{"marker in topic_key", &domain.Observation{Title: "T", Content: "Public", TopicKey: "k/<private>" + canary + "</private>", Project: "p", Scope: "project"}, privacy.ErrCodeInvalidMarker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupPrivacyDB(t)
			stores := buildPrivacyFullStores(t, db)
			origTitle, origContent := tc.obs.Title, tc.obs.Content

			effect, err := SaveWithEffect(context.Background(), stores, tc.obs)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			var privErr *privacy.Error
			if !errors.As(err, &privErr) || privErr.Code != tc.wantCode {
				t.Fatalf("expected *privacy.Error with code %q, got err=%v (%T)", tc.wantCode, err, err)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("error diagnostic leaked canary secret: %s", err.Error())
			}
			if effect != (domain.SaveEffect{}) {
				t.Fatalf("expected zero SaveEffect, got %+v", effect)
			}
			if tc.obs.Title != origTitle || tc.obs.Content != origContent || tc.obs.ID != 0 {
				t.Fatalf("caller observation struct mutated on reject")
			}
			if countTableRows(t, db, "observations") != 0 || countTableRows(t, db, "index_outbox") != 0 || countTableRows(t, db, "entity_links") != 0 {
				t.Fatal("persisted state dirty on rejected save")
			}
		})
	}
}

func TestBundlePrivacy_ProtectedInputFakesAndZeroEffectsOnReject(t *testing.T) {
	db := setupPrivacyDB(t)
	realStores := buildPrivacyFullStores(t, db)
	ctx := context.Background()
	const (
		canaryTitle  = "spy_title_secret_9981"
		canarySecret = "fake_spy_canary_secret_4412"
	)

	// Case 1: Zero observer invocation on invalid input (fails closed before UnitOfWork / downstream effects).
	obsBad := &domain.Observation{Title: "Spy", Content: "Public <private>" + canarySecret, Project: "p", Scope: "project"}
	spyObserverBad := &spyCollaboratorObserver{}
	spyUOW := &spyPrivacyUOW{inner: realStores.UnitOfWork}
	storesWithSpy := &Stores{
		Observations: realStores.Observations,
		Outbox:       realStores.Outbox,
		Entities:     realStores.Entities,
		UnitOfWork:   spyUOW,
	}

	effect, err := saveWithEffect(ctx, storesWithSpy, obsBad, spyObserverBad.Observe)
	if err == nil || effect != (domain.SaveEffect{}) {
		t.Fatalf("expected fail-closed error on invalid input: err=%v, effect=%+v", err, effect)
	}
	if spyObserverBad.invocations != 0 {
		t.Fatalf("expected zero observer invocations on invalid input, got %d", spyObserverBad.invocations)
	}
	if len(spyObserverBad.observed) != 0 {
		t.Fatalf("expected zero observed arguments on invalid input, got %d", len(spyObserverBad.observed))
	}
	if spyUOW.doCalls != 0 {
		t.Fatalf("expected zero UnitOfWork calls on invalid input, got %d", spyUOW.doCalls)
	}
	if countTableRows(t, db, "observations") != 0 || countTableRows(t, db, "index_outbox") != 0 || countTableRows(t, db, "entity_links") != 0 {
		t.Fatal("persisted state dirty on rejected save")
	}
	if obsBad.Title != "Spy" || obsBad.Content != "Public <private>"+canarySecret || obsBad.ID != 0 {
		t.Fatal("caller-owned observation struct was mutated on rejected save")
	}

	// Case 2: Actual collaborator effect argument spy asserts exact protected values with no canary.
	obsGood := &domain.Observation{
		Title:   "Spy <private>" + canaryTitle + "</private>",
		Content: "Public <private>" + canarySecret + "</private> ok",
		Project: "p",
		Scope:   "project",
	}
	const (
		wantProtectedTitle   = "Spy [REDACTED]"
		wantProtectedContent = "Public [REDACTED] ok"
	)
	spyObserverGood := &spyCollaboratorObserver{}
	spyUOW.doCalls = 0

	effectGood, errGood := saveWithEffect(ctx, storesWithSpy, obsGood, spyObserverGood.Observe)
	if errGood != nil {
		t.Fatalf("expected successful save, got %v", errGood)
	}
	if effectGood.Status != domain.WriteStatusCreated {
		t.Fatalf("expected WriteStatusCreated, got %v", effectGood.Status)
	}
	if spyUOW.doCalls != 1 {
		t.Fatalf("expected exactly 1 UnitOfWork call, got %d", spyUOW.doCalls)
	}
	if spyObserverGood.invocations != 1 {
		t.Fatalf("expected exactly 1 observer invocation on success, got %d", spyObserverGood.invocations)
	}
	if len(spyObserverGood.observed) != 1 {
		t.Fatalf("expected exactly 1 captured observation, got %d", len(spyObserverGood.observed))
	}

	// Assert exact protected title/content with no canary on the actual collaborator effect argument.
	observed := spyObserverGood.observed[0]
	if observed.Title != wantProtectedTitle {
		t.Fatalf("collaborator received unexpected title: got %q, want %q", observed.Title, wantProtectedTitle)
	}
	if observed.Content != wantProtectedContent {
		t.Fatalf("collaborator received unexpected content: got %q, want %q", observed.Content, wantProtectedContent)
	}
	if strings.Contains(observed.Title, canaryTitle) || strings.Contains(observed.Title, canarySecret) {
		t.Fatalf("collaborator effect argument leaked canary in title: %q", observed.Title)
	}
	if strings.Contains(observed.Content, canarySecret) || strings.Contains(observed.Content, canaryTitle) {
		t.Fatalf("collaborator effect argument leaked canary in content: %q", observed.Content)
	}

	// Verify caller pointer was NOT mutated into redacted prose (caller retains its own prose).
	if !strings.Contains(obsGood.Title, canaryTitle) || !strings.Contains(obsGood.Content, canarySecret) {
		t.Fatal("caller-owned prose was mutated")
	}

	// Case 3: Rollback preservation via controlled observer error.
	spyObserverRollback := &spyCollaboratorObserver{failErr: errors.New("observer simulated rollback error")}
	spyUOW.doCalls = 0

	obsRollback := &domain.Observation{
		Title:   "Rollback <private>" + canaryTitle + "</private>",
		Content: "Public <private>" + canarySecret + "</private> rollback test",
		Project: "p",
		Scope:   "project",
	}
	const (
		wantRollbackTitle   = "Rollback [REDACTED]"
		wantRollbackContent = "Public [REDACTED] rollback test"
	)
	effectRollback, errRollback := saveWithEffect(ctx, storesWithSpy, obsRollback, spyObserverRollback.Observe)
	if errRollback == nil || effectRollback != (domain.SaveEffect{}) {
		t.Fatalf("expected rollback error from observer, got effect=%+v, err=%v", effectRollback, errRollback)
	}
	if spyUOW.doCalls != 1 {
		t.Fatalf("expected 1 UnitOfWork call on rollback attempt, got %d", spyUOW.doCalls)
	}
	if spyObserverRollback.invocations != 1 {
		t.Fatalf("expected 1 observer invocation before rollback, got %d", spyObserverRollback.invocations)
	}
	rbObs := spyObserverRollback.observed[0]
	if rbObs.Title != wantRollbackTitle {
		t.Fatalf("observer received unexpected title: got %q, want %q", rbObs.Title, wantRollbackTitle)
	}
	if rbObs.Content != wantRollbackContent {
		t.Fatalf("observer received unexpected content: got %q, want %q", rbObs.Content, wantRollbackContent)
	}
	if strings.Contains(rbObs.Title, canaryTitle) || strings.Contains(rbObs.Content, canarySecret) {
		t.Fatalf("observer received canary in title before rollback: %q", rbObs.Title)
	}
	if strings.Contains(rbObs.Content, canarySecret) || strings.Contains(rbObs.Content, canaryTitle) {
		t.Fatalf("observer received canary in content before rollback: %q", rbObs.Content)
	}
	// Verify rollback preservation: only the 1 observation and 1 outbox row from Case 2 committed, nothing from Case 3.
	if n := countTableRows(t, db, "observations"); n != 1 {
		t.Fatalf("expected exactly 1 observation committed from Case 2, got %d (rollback failed)", n)
	}
	if n := countTableRows(t, db, "index_outbox"); n != 1 {
		t.Fatalf("expected exactly 1 outbox intent committed from Case 2, got %d (rollback failed)", n)
	}
	if n := countTableRows(t, db, "entity_links"); n != 0 {
		t.Fatalf("expected 0 entity links committed, got %d (rollback failed)", n)
	}
}
