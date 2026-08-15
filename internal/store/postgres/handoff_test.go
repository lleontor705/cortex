package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/internal/domain"
)

// The PostgreSQL handoff executor must satisfy the transport-neutral domain
// contract so R7 can expose it behind AuthorizedStore without adapters.
func TestPostgresHandoffExecutorImplementsDomainExecutor(t *testing.T) {
	var _ domain.HandoffExecutor = (*Store)(nil)
}

// Validation must fail closed before any database access: an invalid scope,
// key, or hash/description mismatch can never open a transaction.
func TestPostgresHandoffExecutorValidationPrecedesDatabase(t *testing.T) {
	store := &Store{}
	canonical := domain.CanonicalHandoff{
		Observation: domain.SaveObservationInput{Title: "t", Content: "c", Type: domain.TypeDecision},
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	ctx := context.Background()

	cases := []struct {
		name      string
		scope     domain.HandoffScope
		key       string
		hash      [32]byte
		canonical domain.CanonicalHandoff
		want      error
	}{
		{"empty scope", "", "key", hash, canonical, domain.ErrHandoffValidation},
		{"empty key", "project:a", "", hash, canonical, domain.ErrHandoffValidation},
		{"hash mismatch", "project:a", "key", [32]byte{1}, canonical, domain.ErrHandoffValidation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.ExecuteHandoff(ctx, tc.scope, tc.key, tc.canonical, tc.hash); !errors.Is(err, tc.want) {
				t.Fatalf("ExecuteHandoff error=%v, want %v", err, tc.want)
			}
		})
	}
}

// Receipt comparison must decide replay/conflict/retry from the full canonical
// bytes plus the SHA-256, never from the hash alone.
func TestPostgresReceiptPayloadDecision(t *testing.T) {
	payload := []byte(`{"observation":{"title":"a"}}`)
	hash := sha256.Sum256(payload)
	sameHashWrongBytes := append([]byte(nil), payload...)
	sameHashWrongBytes[len(sameHashWrongBytes)-2] = 'z'
	otherPayload := []byte(`{"observation":{"title":"b"}}`)
	otherHash := sha256.Sum256(otherPayload)

	cases := []struct {
		name          string
		state         handoffReceiptState
		receiptHash   [32]byte
		receiptBytes  []byte
		claimHash     [32]byte
		claimBytes    []byte
		observationID *int64
		wantRe        bool
		wantErr       error
	}{
		{"committed identical", handoffReceiptCommitted, hash, payload, hash, payload, ptrInt64(7), true, nil},
		{"committed same hash different bytes", handoffReceiptCommitted, hash, payload, hash, sameHashWrongBytes, ptrInt64(7), false, domain.ErrHandoffConflict},
		{"committed different hash", handoffReceiptCommitted, hash, payload, otherHash, otherPayload, ptrInt64(7), false, domain.ErrHandoffConflict},
		{"committed identical without observation", handoffReceiptCommitted, hash, payload, hash, payload, nil, false, domain.ErrHandoffPersistence},
		{"pending identical", handoffReceiptPending, hash, payload, hash, payload, nil, false, domain.ErrHandoffUnavailable},
		{"pending different bytes", handoffReceiptPending, hash, payload, otherHash, otherPayload, nil, false, domain.ErrHandoffConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay, err := classifyReceiptPayload(handoffReceipt{State: tc.state, PayloadHash: tc.receiptHash, CanonicalPayload: tc.receiptBytes, ObservationID: tc.observationID}, tc.claimHash, tc.claimBytes)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v, want %v", err, tc.wantErr)
			}
			if replay != tc.wantRe {
				t.Fatalf("replay=%v, want %v", replay, tc.wantRe)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }

// A committed receipt always carries the observation reference; the executor
// must refuse to report a replay without it.
func TestPostgresCommittedReceiptRequiresObservation(t *testing.T) {
	payload := []byte(`{}`)
	hash := sha256.Sum256(payload)
	replay, err := classifyReceiptPayload(handoffReceipt{State: handoffReceiptCommitted, PayloadHash: hash, CanonicalPayload: payload}, hash, payload)
	if replay || !errors.Is(err, domain.ErrHandoffPersistence) {
		t.Fatalf("replay=%v err=%v, want persistence failure", replay, err)
	}
}

// The driver taxonomy must map every contention SQLSTATE to a retryable
// unavailable error and keep the remaining codes stable.
func TestPostgresHandoffPgErrorTaxonomy(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantCode     domain.HandoffErrorCode
		wantRetry    bool
		wantSentinel error
	}{
		{"lock not available 55P03", &pgconn.PgError{Code: "55P03"}, domain.HandoffErrorUnavailable, true, domain.ErrHandoffUnavailable},
		{"deadlock detected 40P01", &pgconn.PgError{Code: "40P01"}, domain.HandoffErrorUnavailable, true, domain.ErrHandoffUnavailable},
		{"serialization failure 40001", &pgconn.PgError{Code: "40001"}, domain.HandoffErrorUnavailable, true, domain.ErrHandoffUnavailable},
		{"unique violation 23505", &pgconn.PgError{Code: "23505"}, domain.HandoffErrorConflict, false, domain.ErrHandoffConflict},
		{"foreign key 23503", &pgconn.PgError{Code: "23503"}, domain.HandoffErrorValidation, false, domain.ErrHandoffValidation},
		{"principal rejected 28000", &pgconn.PgError{Code: "28000"}, domain.HandoffErrorUnauthorized, false, domain.ErrHandoffUnauthorized},
		{"password rejected 28P01", &pgconn.PgError{Code: "28P01"}, domain.HandoffErrorUnauthorized, false, domain.ErrHandoffUnauthorized},
		{"rls denied 42501", &pgconn.PgError{Code: "42501"}, domain.HandoffErrorForbidden, false, domain.ErrHandoffForbidden},
		{"context canceled", context.Canceled, domain.HandoffErrorTimeout, true, domain.ErrHandoffTimeout},
		{"generic driver error", errors.New("connection refused"), domain.HandoffErrorPersistence, true, domain.ErrHandoffPersistence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := handoffPgError(tc.err, "taxonomy")
			var typed *domain.HandoffError
			if !errors.As(got, &typed) {
				t.Fatalf("error %v is not a HandoffError", got)
			}
			if typed.Code != tc.wantCode || typed.Retryable != tc.wantRetry {
				t.Fatalf("code=%s retryable=%v, want %s/%v", typed.Code, typed.Retryable, tc.wantCode, tc.wantRetry)
			}
			if !errors.Is(got, tc.wantSentinel) {
				t.Fatalf("error %v does not match sentinel %v", got, tc.wantSentinel)
			}
		})
	}
}

// --- offline driver-boundary stub --------------------------------------------
//
// stubPgTx records every statement issued through a pgx.Tx and answers with
// scripted rows, so the executor and save core can be driven without a
// database. Only methods the package uses are implemented; the rest are
// inherited from the embedded nil interface and must not be called.

type stubQuery struct {
	sql  string
	args []any
}

type stubPgTx struct {
	pgx.Tx
	queries   []stubQuery
	commits   int
	rollbacks int

	newestSession   string
	claimHash       [32]byte
	claimPayload    []byte
	receiptExists   bool
	observationID   int64
	observationUUID string
	relationTarget  int64
}

func (t *stubPgTx) record(sql string, args []any) {
	t.queries = append(t.queries, stubQuery{sql: sql, args: append([]any(nil), args...)})
}

func (t *stubPgTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.record(sql, args)
	return pgconn.NewCommandTag("OK"), nil
}

func (t *stubPgTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.record(sql, args)
	return &stubRows{tx: t, sql: sql}, nil
}

func (t *stubPgTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.record(sql, args)
	return &stubRow{tx: t, sql: sql}
}

func (t *stubPgTx) Commit(context.Context) error   { t.commits++; return nil }
func (t *stubPgTx) Rollback(context.Context) error { t.rollbacks++; return nil }

func (t *stubPgTx) queryIndex(substr string) int {
	for i, q := range t.queries {
		if strings.Contains(q.sql, substr) {
			return i
		}
	}
	return -1
}

func (t *stubPgTx) requireQuery(t2 *testing.T, substr string) stubQuery {
	t2.Helper()
	i := t.queryIndex(substr)
	if i < 0 {
		t2.Fatalf("no issued query contains %q; issued=%s", substr, t.issuedSQL())
	}
	return t.queries[i]
}

func (t *stubPgTx) forbidQuery(t2 *testing.T, substr string) {
	t2.Helper()
	if i := t.queryIndex(substr); i >= 0 {
		t2.Fatalf("forbidden query issued: %q", t.queries[i].sql)
	}
}

func (t *stubPgTx) issuedSQL() string {
	parts := make([]string, 0, len(t.queries))
	for _, q := range t.queries {
		parts = append(parts, q.sql)
	}
	return strings.Join(parts, "\n")
}

type stubRow struct {
	tx  *stubPgTx
	sql string
}

func (r *stubRow) Scan(dest ...any) error {
	switch {
	case strings.Contains(r.sql, "UPDATE handoff_receipts"):
		id, now := r.tx.observationID, time.Now().UTC()
		status := "created"
		return assignReceipt(dest, "project:stub", "stub-key", r.tx.claimHash, r.tx.claimPayload, "committed", &id, &status, &now)
	case strings.Contains(r.sql, "FOR UPDATE"):
		id, now := r.tx.observationID, time.Now().UTC()
		status := "created"
		return assignReceipt(dest, "project:stub", "stub-key", r.tx.claimHash, r.tx.claimPayload, "committed", &id, &status, &now)
	case strings.Contains(r.sql, "INSERT INTO observations"):
		*dest[0].(*int64) = r.tx.observationID
		*dest[1].(*string) = r.tx.observationUUID
		return nil
	case strings.Contains(r.sql, "topic_key=$"):
		// topic lookup: no existing observation for a fresh stub
		return pgx.ErrNoRows
	case strings.Contains(r.sql, "SELECT id FROM observations") && strings.Contains(r.sql, "public_id=$1::uuid"):
		// relation target resolution
		*dest[0].(*int64) = r.tx.relationTarget
		return nil
	case strings.Contains(r.sql, "SELECT public_id::text FROM observations"):
		// receipt replay observation reference
		*dest[0].(*string) = r.tx.observationUUID
		return nil
	case strings.Contains(r.sql, "FROM sessions"):
		if r.tx.newestSession == "" {
			return pgx.ErrNoRows
		}
		*dest[0].(*string) = r.tx.newestSession
		return nil
	default:
		return pgx.ErrNoRows
	}
}

func assignReceipt(dest []any, scope domain.HandoffScope, key string, hash [32]byte, payload []byte, state string, obsID *int64, status *string, committedAt *time.Time) error {
	hashBytes := hash
	*dest[0].(*domain.HandoffScope) = scope
	*dest[1].(*string) = key
	*dest[2].(*[]byte) = hashBytes[:]
	*dest[3].(*[]byte) = payload
	*dest[4].(*string) = state
	*dest[5].(**int64) = obsID
	*dest[6].(**string) = status
	*dest[7].(**time.Time) = committedAt
	*dest[8].(*time.Time) = time.Now().UTC()
	return nil
}

type stubRows struct {
	pgx.Rows
	tx      *stubPgTx
	sql     string
	scanned bool
}

func (r *stubRows) Next() bool {
	if strings.Contains(r.sql, "INSERT INTO handoff_receipts") {
		return !r.tx.receiptExists && !r.scanned
	}
	return !r.scanned
}

func (r *stubRows) Scan(dest ...any) error {
	r.scanned = true
	if strings.Contains(r.sql, "INSERT INTO handoff_receipts") {
		return assignReceipt(dest, "project:stub", "stub-key", r.tx.claimHash, r.tx.claimPayload, "pending", nil, nil, nil)
	}
	return assignReceipt(dest, "project:stub", "stub-key", r.tx.claimHash, r.tx.claimPayload, "committed", &r.tx.observationID, nil, nil)
}

func (r *stubRows) Err() error             { return nil }
func (r *stubRows) Close()                 {}
func (r *stubRows) Values() ([]any, error) { return nil, nil }

// stubStore builds a workspace-bound Store whose transaction reuses the stub
// tx installed in the context, exercising the exact code path the receipt
// primitives rely on (txFromContext).
func stubStore(t *testing.T, workspace bool) (*Store, *stubPgTx) {
	t.Helper()
	tx := &stubPgTx{
		newestSession:   uuid.NewString(),
		observationID:   101,
		observationUUID: uuid.NewString(),
		relationTarget:  202,
	}
	tenant := &domain.TenantContext{TenantID: uuid.NewString()}
	if workspace {
		tenant.WorkspaceID = uuid.NewString()
	}
	store := &Store{tenant: tenant, principal: domain.Principal{Subject: uuid.NewString(), OrgID: tenant.TenantID}}
	return store, tx
}

func ctxWithStubTx(tx pgx.Tx) context.Context {
	return context.WithValue(context.Background(), txKey{}, tx)
}

func stubCanonical(t *testing.T, relation bool) (domain.CanonicalHandoff, [32]byte) {
	t.Helper()
	c := domain.CanonicalHandoff{
		Observation: domain.SaveObservationInput{Title: "stub handoff", Content: "plain stub content", Type: domain.TypeDecision, Project: "stub"},
	}
	if relation {
		pid := uuid.New()
		c.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{PublicID: &pid}, Type: domain.RelationReferences, Weight: 1, Confidence: 1}
	}
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	return c, hash
}

// stubExecutorReady wires a workspace-bound stub store whose receipt echo
// matches the canonical payload of a fresh executor request.
func stubExecutorReady(t *testing.T, relation bool) (*Store, *stubPgTx, domain.CanonicalHandoff, [32]byte) {
	t.Helper()
	store, tx := stubStore(t, true)
	canonical, hash := stubCanonical(t, relation)
	tx.claimHash, tx.claimPayload = hash, mustJSON(t, canonical)
	return store, tx, canonical, hash
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Every handoff effect must stay inside the caller's workspace: a store
// without a workspace binding must refuse before touching the database.
func TestPostgresHandoffExecutorRequiresWorkspaceBoundStore(t *testing.T) {
	store, tx := stubStore(t, false)
	canonical, hash := stubCanonical(t, false)
	result, err := store.ExecuteHandoff(ctxWithStubTx(tx), "project:a", "key", canonical, hash)
	if !errors.Is(err, domain.ErrHandoffValidation) {
		t.Fatalf("error=%v, want validation refusal for workspace-less store", err)
	}
	if result.Status != "" {
		t.Fatalf("result=%+v leaked from refused handoff", result)
	}
	if len(tx.queries) != 0 {
		t.Fatalf("workspace-less store issued %d statements before validation: %s", len(tx.queries), tx.issuedSQL())
	}
}

// Session resolution and relation targets must be workspace scoped: the
// executor may only resolve sessions and address targets inside the bound
// workspace of the same tenant.
func TestPostgresHandoffExecutorScopesQueriesToWorkspace(t *testing.T) {
	store, tx, canonical, hash := stubExecutorReady(t, true)
	if _, err := store.ExecuteHandoff(ctxWithStubTx(tx), "project:a", "key", canonical, hash); err != nil {
		t.Fatalf("ExecuteHandoff error=%v\nissued=%s", err, tx.issuedSQL())
	}
	session := tx.requireQuery(t, "FROM sessions")
	if !strings.Contains(session.sql, "workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$") {
		t.Fatalf("session resolution is not workspace scoped: %s", session.sql)
	}
	relation := tx.requireQuery(t, "SELECT id FROM observations")
	if !strings.Contains(relation.sql, "session_id IN (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=") {
		t.Fatalf("relation target lookup is not workspace scoped: %s", relation.sql)
	}
}

// REM-SAVE-001: the interactive save path preserves the legacy observable —
// topic upsert plus unconditional insert. Manual duplicates must never hit a
// dedup replay on the PostgreSQL backend.
func TestPostgresSaveLegacyPathPreservesBaselineObservable(t *testing.T) {
	store, tx := stubStore(t, true)
	repo := store.observations()
	ctx := ctxWithStubTx(tx)
	first := &domain.Observation{SessionID: tx.newestSession, Project: "p", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "legacy duplicate", Content: "same bytes"}
	effect, err := repo.SaveWithEffect(ctx, first)
	if err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("first save effect=%+v err=%v", effect, err)
	}
	second := &domain.Observation{SessionID: tx.newestSession, Project: "p", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "legacy duplicate", Content: "same bytes"}
	effect, err = repo.SaveWithEffect(ctx, second)
	if err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("duplicate save must insert unconditionally, effect=%+v err=%v", effect, err)
	}
	tx.forbidQuery(t, "interval '15 minutes'")
	legacy := &domain.Observation{SessionID: tx.newestSession, Project: "p", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "legacy duplicate", Content: "same bytes"}
	if err := repo.Save(ctx, legacy); err != nil {
		t.Fatalf("legacy Save duplicate error=%v", err)
	}
	tx.forbidQuery(t, "interval '15 minutes'")
}

// The topic key is normalized once before lookup and persistence, and the
// topic branch must serialize concurrent first upserts with an advisory
// transaction lock so the active-topic unique index cannot race.
func TestPostgresSaveNormalizesTopicKeyAndSerializesTopicUpserts(t *testing.T) {
	store, tx := stubStore(t, true)
	repo := store.observations()
	obs := &domain.Observation{SessionID: tx.newestSession, Project: "p", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "topic", Content: "v1", TopicKey: "  topic/key  "}
	effect, err := repo.SaveWithEffect(ctxWithStubTx(tx), obs)
	if err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("topic save effect=%+v err=%v", effect, err)
	}
	lock := tx.queryIndex("pg_advisory_xact_lock")
	if lock < 0 {
		t.Fatalf("topic upsert was not serialized by an advisory lock; issued=%s", tx.issuedSQL())
	}
	if topic := tx.queryIndex("topic_key=$"); topic >= 0 && topic < lock {
		t.Fatalf("topic lookup ran before the advisory lock")
	}
	insert := tx.requireQuery(t, "INSERT INTO observations")
	topicArg, _ := insert.args[7].(string)
	if topicArg != "topic/key" {
		t.Fatalf("persisted topic bytes=%q, want trimmed %q (args=%v)", topicArg, "topic/key", insert.args)
	}
	if obs.TopicKey != "topic/key" {
		t.Fatalf("observable TopicKey=%q, want normalized", obs.TopicKey)
	}
}

// The fresh-transaction path of Store.transaction must install the pgx.Tx in
// the context so receipt primitives can resolve it: ExecuteHandoff opens its
// own transaction, and claim/finalize read txFromContext.
func TestPostgresStoreTransactionInstallsTxInContext(t *testing.T) {
	data, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "func (s *Store) transaction(")
	end := strings.Index(src, "type txHandle struct")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("Store.transaction not found in store.go")
	}
	body := src[start:end]
	fresh := strings.Index(body, "s.pool.BeginTx")
	if fresh < 0 {
		t.Fatal("fresh transaction branch not found")
	}
	if !strings.Contains(body[fresh:], "context.WithValue(ctx, txKey{}, tx)") {
		t.Fatalf("Store.transaction begins a fresh tx without installing txKey; primitives requiring txFromContext fail:\n%s", body)
	}
}

// The happy path must reach the receipt primitives on the ambient pgx.Tx:
// claim inserts the pending receipt, effects materialize, finalize commits
// the receipt, and both execution seams fire in order.
func TestPostgresHandoffExecutorHappyPathReachesClaimAndFinalize(t *testing.T) {
	store, tx, canonical, hash := stubExecutorReady(t, true)
	var seams []string
	handoffFailpoints = func(seam string) error {
		seams = append(seams, seam)
		return nil
	}
	t.Cleanup(func() { handoffFailpoints = func(string) error { return nil } })

	result, err := store.ExecuteHandoff(ctxWithStubTx(tx), "project:a", "key", canonical, hash)
	if err != nil {
		t.Fatalf("ExecuteHandoff error=%v\nissued=%s", err, tx.issuedSQL())
	}
	if result.Status != domain.WriteStatusCreated || result.Ref.PublicID == nil {
		t.Fatalf("result=%+v, want created with public ref", result)
	}
	tx.requireQuery(t, "INSERT INTO handoff_receipts")
	tx.requireQuery(t, "UPDATE handoff_receipts")
	if len(seams) != 2 || seams[0] != "after-edge" || seams[1] != "before-commit" {
		t.Fatalf("execution seams=%v, want [after-edge before-commit]", seams)
	}
}

// The after-edge seam proves rollback atomicity for the optional relation:
// once it fires, the receipt must not finalize inside the aborted attempt.
func TestPostgresHandoffExecutorFailpointAfterEdgeAbortsBeforeFinalize(t *testing.T) {
	store, tx, canonical, hash := stubExecutorReady(t, true)
	handoffFailpoints = func(seam string) error {
		if seam == "after-edge" {
			return errors.New("seam failure: after-edge")
		}
		return nil
	}
	t.Cleanup(func() { handoffFailpoints = func(string) error { return nil } })

	_, err := store.ExecuteHandoff(ctxWithStubTx(tx), "project:a", "key", canonical, hash)
	var typed *domain.HandoffError
	if !errors.As(err, &typed) || typed.Code != domain.HandoffErrorPersistence {
		t.Fatalf("error=%v, want persistence-classified seam failure", err)
	}
	tx.requireQuery(t, "INSERT INTO edges")
	if i := tx.queryIndex("UPDATE handoff_receipts"); i >= 0 {
		t.Fatalf("finalize ran after an after-edge failure")
	}
	if tx.commits != 0 {
		t.Fatalf("failed attempt committed %d times", tx.commits)
	}
}

// The before-commit seam proves the executor can surface a lost commit just
// before finalization is returned: every effect ran, yet the caller still
// receives a retryable error instead of a fabricated success.
func TestPostgresHandoffExecutorFailpointBeforeCommitSurfacesRetryable(t *testing.T) {
	store, tx, canonical, hash := stubExecutorReady(t, false)
	handoffFailpoints = func(seam string) error {
		if seam == "before-commit" {
			return errors.New("seam failure: before-commit")
		}
		return nil
	}
	t.Cleanup(func() { handoffFailpoints = func(string) error { return nil } })

	_, err := store.ExecuteHandoff(ctxWithStubTx(tx), "project:a", "key", canonical, hash)
	var typed *domain.HandoffError
	if !errors.As(err, &typed) || typed.Code != domain.HandoffErrorPersistence || !typed.Retryable {
		t.Fatalf("error=%v, want retryable persistence classification", err)
	}
	tx.requireQuery(t, "UPDATE handoff_receipts")
	if tx.commits != 0 || tx.rollbacks != 0 {
		t.Fatalf("ambient transaction lifecycle violated: commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

// A committed receipt must replay without re-materializing any observation
// and without writing the optional relation again.
func TestPostgresHandoffExecutorReplaysCommittedReceiptWithoutEffects(t *testing.T) {
	store, tx, canonical, hash := stubExecutorReady(t, true)
	tx.receiptExists = true

	result, err := store.ExecuteHandoff(ctxWithStubTx(tx), "project:a", "key", canonical, hash)
	if err != nil {
		t.Fatalf("replay error=%v\nissued=%s", err, tx.issuedSQL())
	}
	if result.Status != domain.WriteStatusReplayed || result.Ref.PublicID == nil {
		t.Fatalf("result=%+v, want replayed with public ref", result)
	}
	tx.forbidQuery(t, "INSERT INTO observations")
	tx.forbidQuery(t, "INSERT INTO edges")
	tx.requireQuery(t, "FOR UPDATE")
}
