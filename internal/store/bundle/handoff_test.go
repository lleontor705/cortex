package bundle

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

const handoffTestSession = "sqlite-handoff-session"

var errHandoffFailpoint = errors.New("injected SQLite handoff failure")

func TestSQLiteHandoff_REQ_HANDOFF_001_AtomicFullPayloadAndOptionalRelation(t *testing.T) {
	t.Run("HAPPY_optional_relation_commits_exact_effect_set", func(t *testing.T) {
		db, path := openHandoffDB(t)
		targetID := seedHandoffTarget(t, db)
		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("with relation", "payload with café and final canary Ω")
		canonical.Relation = &domain.HandoffRelationInput{
			Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences,
			Weight: 2.5, Confidence: 0.8, Reasoning: "preserve complete relation payload",
		}

		result := executeHandoff(t, executor, "scope:atomic", "optional-relation", canonical)
		if result.Status != domain.WriteStatusCreated || result.Ref.LocalID == nil {
			t.Fatalf("result=%+v, want created local ref", result)
		}
		assertHandoffState(t, db, "scope:atomic", "optional-relation", canonical, 1, 1, 1)

		closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
			assertHandoffState(t, restarted, "scope:atomic", "optional-relation", canonical, 1, 1, 1)
		})
	})

	t.Run("EDGE_no_relation_preserves_full_payload_without_implicit_edge", func(t *testing.T) {
		db, path := openHandoffDB(t)
		seedHandoffTarget(t, db)
		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("without relation", strings.Repeat("界", 64*1024)+"::TRAILING-CANARY::🧠")
		canonical.CapabilityTuple = []byte(`{"opaque":{"command":"never execute","unicode":"café"}}`)

		result := executeHandoff(t, executor, "scope:atomic", "no-relation", canonical)
		if result.Status != domain.WriteStatusCreated || result.Ref.LocalID == nil {
			t.Fatalf("result=%+v, want created local ref", result)
		}
		assertHandoffState(t, db, "scope:atomic", "no-relation", canonical, 1, 0, 1)

		closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
			assertHandoffState(t, restarted, "scope:atomic", "no-relation", canonical, 1, 0, 1)
		})
	})

	for _, stage := range []sqliteHandoffStage{handoffAfterSave, handoffAfterEdge, handoffBeforeCommit} {
		stage := stage
		t.Run("ERROR_"+string(stage)+"_rolls_back_all_effects_before_and_after_restart", func(t *testing.T) {
			db, path := openHandoffDB(t)
			targetID := seedHandoffTarget(t, db)
			canonical := handoffCanonical("rollback "+string(stage), "must not survive "+string(stage))
			canonical.Relation = &domain.HandoffRelationInput{
				Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences,
				Weight: 1, Confidence: 1, Reasoning: "rollback witness",
			}
			executor := newTestHandoffExecutor(db, func(got sqliteHandoffStage) error {
				if got == stage {
					return errHandoffFailpoint
				}
				return nil
			})
			payload, hash := canonicalPayload(t, canonical)
			_, err := executor.ExecuteHandoff(context.Background(), "scope:rollback", "rollback-"+string(stage), canonical, hash)
			if !errors.Is(err, errHandoffFailpoint) {
				t.Fatalf("ExecuteHandoff error=%v, want injected failpoint", err)
			}
			assertFailedHandoffAbsent(t, db, "scope:rollback", "rollback-"+string(stage), canonical.Observation.Title, payload)

			closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
				assertFailedHandoffAbsent(t, restarted, "scope:rollback", "rollback-"+string(stage), canonical.Observation.Title, payload)
			})
		})
	}
}

func TestSQLiteHandoff_REQ_HANDOFF_002_ReplayConflictConcurrencyAndRestart(t *testing.T) {
	t.Run("HAPPY_exact_replay_before_and_after_restart_returns_same_ref_without_rematerialization", func(t *testing.T) {
		db, path := openHandoffDB(t)
		targetID := seedHandoffTarget(t, db)
		canonical := handoffCanonical("exact replay", "same full payload Ω")
		canonical.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences, Weight: 1, Confidence: 0.9}
		executor := newTestHandoffExecutor(db, nil)
		created := executeHandoff(t, executor, "scope:replay", "same-key", canonical)
		replayed := executeHandoff(t, executor, "scope:replay", "same-key", canonical)
		assertSameReplay(t, created, replayed)
		assertHandoffState(t, db, "scope:replay", "same-key", canonical, 1, 1, 1)

		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		restarted := reopenHandoffDB(t, path)
		defer func() { _ = restarted.Close() }()
		postRestart := executeHandoff(t, newTestHandoffExecutor(restarted, nil), "scope:replay", "same-key", canonical)
		assertSameReplay(t, created, postRestart)
		assertHandoffState(t, restarted, "scope:replay", "same-key", canonical, 1, 1, 1)
	})

	t.Run("EDGE_real_concurrent_same_scope_key_payload_materializes_once", func(t *testing.T) {
		db, _ := openHandoffDB(t)
		db.SetMaxOpenConns(12)
		canonical := handoffCanonical("concurrent exact replay", "one materialization under real SQLite contention")
		executor := newTestHandoffExecutor(db, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		const callers = 8
		start := make(chan struct{})
		results := make(chan domain.ObservationWriteResult, callers)
		errs := make(chan error, callers)
		var wg sync.WaitGroup
		_, hash := canonicalPayload(t, canonical)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, err := executor.ExecuteHandoff(ctx, "scope:concurrent", "same-key", canonical, hash)
				if err != nil {
					errs <- err
					return
				}
				results <- result
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("concurrent ExecuteHandoff: %v", err)
		}
		close(results)
		created, replayed := 0, 0
		var ref int64
		for result := range results {
			if result.Ref.LocalID == nil {
				t.Fatalf("concurrent result missing local ref: %+v", result)
			}
			if ref == 0 {
				ref = *result.Ref.LocalID
			} else if *result.Ref.LocalID != ref {
				t.Fatalf("concurrent refs differ: got %d want %d", *result.Ref.LocalID, ref)
			}
			switch result.Status {
			case domain.WriteStatusCreated:
				created++
			case domain.WriteStatusReplayed:
				replayed++
			default:
				t.Errorf("concurrent status=%q", result.Status)
			}
		}
		if created != 1 || replayed != callers-1 {
			t.Fatalf("created=%d replayed=%d, want 1/%d", created, replayed, callers-1)
		}
		assertHandoffState(t, db, "scope:concurrent", "same-key", canonical, 1, 0, 1)
		// Windows cannot delete the WAL database while pool connections hold
		// it open, which would fail t.TempDir cleanup and mask the verdict.
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})

	for _, tc := range []struct {
		name      string
		mutate    func(*domain.CanonicalHandoff)
		sameHash  bool
		wantError error
	}{
		{name: "different_hash_and_payload", mutate: func(c *domain.CanonicalHandoff) { c.Observation.Content = "changed payload" }, wantError: domain.ErrHandoffConflict},
		// A caller hash that disagrees with the recomputed canonical payload
		// hash is rejected as validation BEFORE any receipt or effect.
		{name: "same_hash_but_different_full_payload", sameHash: true, mutate: func(c *domain.CanonicalHandoff) { c.CapabilityTuple = []byte(`{"collision":"different bytes"}`) }, wantError: domain.ErrHandoffValidation},
	} {
		tc := tc
		t.Run("ERROR_"+tc.name+"_rejected_without_mutation_before_and_after_restart", func(t *testing.T) {
			db, path := openHandoffDB(t)
			original := handoffCanonical("conflict original", "immutable original payload")
			executor := newTestHandoffExecutor(db, nil)
			created := executeHandoff(t, executor, "scope:conflict", "same-key", original)
			before := handoffSnapshot(t, db, "scope:conflict", "same-key")
			conflicting := original
			tc.mutate(&conflicting)
			_, originalHash := canonicalPayload(t, original)
			_, conflictingHash := canonicalPayload(t, conflicting)
			if tc.sameHash {
				conflictingHash = originalHash
			}
			assertHandoffError(t, executor, "scope:conflict", "same-key", conflicting, conflictingHash, tc.wantError)
			if after := handoffSnapshot(t, db, "scope:conflict", "same-key"); !bytes.Equal(before, after) {
				t.Fatalf("conflict mutated durable snapshot\nbefore=%q\nafter=%q", before, after)
			}

			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := reopenHandoffDB(t, path)
			defer func() { _ = restarted.Close() }()
			assertHandoffError(t, newTestHandoffExecutor(restarted, nil), "scope:conflict", "same-key", conflicting, conflictingHash, tc.wantError)
			if after := handoffSnapshot(t, restarted, "scope:conflict", "same-key"); !bytes.Equal(before, after) {
				t.Fatalf("post-restart conflict mutated durable snapshot")
			}
			var observationID int64
			if err := restarted.QueryRow(`SELECT observation_id FROM handoff_receipts WHERE scope=? AND key=?`, "scope:conflict", "same-key").Scan(&observationID); err != nil {
				t.Fatal(err)
			}
			if created.Ref.LocalID == nil || observationID != *created.Ref.LocalID {
				t.Fatalf("confirmed receipt ref changed: receipt=%d created=%+v", observationID, created)
			}
		})
	}
}

func openHandoffDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handoff.db")
	db := reopenHandoffDB(t, path)
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply SQLite migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory) VALUES (?,?,?)`, handoffTestSession, "handoff-project", t.TempDir()); err != nil {
		t.Fatalf("seed handoff session: %v", err)
	}
	return db, path
}

func reopenHandoffDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := path + "?_pragma=foreign_keys=ON&_pragma=journal_mode=WAL&_pragma=busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

func closeAndReopenHandoffDB(t *testing.T, db *sql.DB, path string, check func(*sql.DB)) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := reopenHandoffDB(t, path)
	defer func() { _ = restarted.Close() }()
	check(restarted)
}

func newTestHandoffExecutor(db *sql.DB, failpoint func(sqliteHandoffStage) error) *SQLiteHandoffExecutor {
	observations := sqlitestore.NewStore(db)
	stores := &Stores{
		Observations: observations,
		Graph:        graphstore.NewStore(db),
		UnitOfWork:   NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig()),
	}
	executor := NewSQLiteHandoffExecutor(stores)
	executor.failpoint = failpoint
	return executor
}

func seedHandoffTarget(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	obs := &domain.Observation{SessionID: handoffTestSession, Project: "handoff-project", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "existing target", Content: "relation target"}
	if err := sqlitestore.NewStore(db).Save(context.Background(), obs); err != nil {
		t.Fatalf("seed relation target: %v", err)
	}
	return obs.ID
}

func handoffCanonical(title, content string) domain.CanonicalHandoff {
	return domain.CanonicalHandoff{Observation: domain.SaveObservationInput{
		Title: title, Content: content, Type: domain.TypeDecision, Project: "handoff-project",
		Scope: domain.ScopeProject, SessionID: handoffTestSession, Confidence: 0.75,
		Source: domain.SourceAI, Tags: []string{"handoff", "sqlite"},
	}, CapabilityTuple: []byte(`{"available":true,"name":"shell","command":"never execute"}`)}
}

func canonicalPayload(t *testing.T, canonical domain.CanonicalHandoff) ([]byte, [32]byte) {
	t.Helper()
	req := domain.HandoffRequest{IdempotencyKey: "payload-helper", Observation: canonical.Observation, Relation: canonical.Relation, CapabilityTuple: canonical.CapabilityTuple}
	_, payload, hash, err := domain.CanonicalizeHandoff(req)
	if err != nil {
		t.Fatalf("canonicalize handoff: %v", err)
	}
	return payload, hash
}

func executeHandoff(t *testing.T, executor *SQLiteHandoffExecutor, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff) domain.ObservationWriteResult {
	t.Helper()
	_, hash := canonicalPayload(t, canonical)
	result, err := executor.ExecuteHandoff(context.Background(), scope, key, canonical, hash)
	if err != nil {
		t.Fatalf("ExecuteHandoff(%s): %v", key, err)
	}
	return result
}

func assertSameReplay(t *testing.T, created, replayed domain.ObservationWriteResult) {
	t.Helper()
	if created.Status != domain.WriteStatusCreated || replayed.Status != domain.WriteStatusReplayed || created.Ref.LocalID == nil || replayed.Ref.LocalID == nil || *created.Ref.LocalID != *replayed.Ref.LocalID {
		t.Fatalf("created=%+v replayed=%+v, want same ref and created/replayed", created, replayed)
	}
}

func assertHandoffState(t *testing.T, db *sql.DB, scope, key string, canonical domain.CanonicalHandoff, wantObservations, wantEdges, wantReceipts int) {
	t.Helper()
	payload, hash := canonicalPayload(t, canonical)
	var observations, edges, receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM observations WHERE title=? AND deleted_at IS NULL`, canonical.Observation.Title).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges e JOIN observations o ON o.id=e.from_obs_id WHERE o.title=?`, canonical.Observation.Title).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM handoff_receipts WHERE scope=? AND key=?`, scope, key).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if observations != wantObservations || edges != wantEdges || receipts != wantReceipts {
		t.Fatalf("state observations=%d edges=%d receipts=%d, want %d/%d/%d", observations, edges, receipts, wantObservations, wantEdges, wantReceipts)
	}
	if wantReceipts == 1 {
		var gotPayload, gotHash []byte
		var state, status string
		var observationID sql.NullInt64
		if err := db.QueryRow(`SELECT canonical_payload,payload_hash,state,initial_status,observation_id FROM handoff_receipts WHERE scope=? AND key=?`, scope, key).Scan(&gotPayload, &gotHash, &state, &status, &observationID); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotPayload, payload) || !bytes.Equal(gotHash, hash[:]) || state != "committed" || status == "" || !observationID.Valid {
			t.Fatalf("receipt did not preserve full committed payload: state=%q status=%q observation=%v payload_equal=%v hash_equal=%v", state, status, observationID, bytes.Equal(gotPayload, payload), bytes.Equal(gotHash, hash[:]))
		}
	}
}

func assertFailedHandoffAbsent(t *testing.T, db *sql.DB, scope, key, title string, payload []byte) {
	t.Helper()
	var observations, edges, receipts, payloadReceipts int
	queries := []struct {
		query string
		args  []any
		out   *int
	}{
		{`SELECT COUNT(*) FROM observations WHERE title=?`, []any{title}, &observations},
		{`SELECT COUNT(*) FROM edges e JOIN observations o ON o.id=e.from_obs_id WHERE o.title=?`, []any{title}, &edges},
		{`SELECT COUNT(*) FROM handoff_receipts WHERE scope=? AND key=?`, []any{scope, key}, &receipts},
		{`SELECT COUNT(*) FROM handoff_receipts WHERE canonical_payload=?`, []any{payload}, &payloadReceipts},
	}
	for _, q := range queries {
		if err := db.QueryRow(q.query, q.args...).Scan(q.out); err != nil {
			t.Fatal(err)
		}
	}
	if observations != 0 || edges != 0 || receipts != 0 || payloadReceipts != 0 {
		t.Fatalf("failed handoff left observations=%d edges=%d receipts=%d payload_receipts=%d", observations, edges, receipts, payloadReceipts)
	}
}

func assertHandoffError(t *testing.T, executor *SQLiteHandoffExecutor, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, hash [32]byte, want error) {
	t.Helper()
	if _, err := executor.ExecuteHandoff(context.Background(), scope, key, canonical, hash); !errors.Is(err, want) {
		t.Fatalf("ExecuteHandoff error=%v, want %v", err, want)
	}
}

func handoffSnapshot(t *testing.T, db *sql.DB, scope, key string) []byte {
	t.Helper()
	var receipt []byte
	if err := db.QueryRow(`SELECT hex(payload_hash)||'|'||hex(canonical_payload)||'|'||state||'|'||observation_id||'|'||initial_status||'|'||committed_at FROM handoff_receipts WHERE scope=? AND key=?`, scope, key).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	var observationCount, edgeCount, receiptCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM handoff_receipts`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf("%s|observations=%d|edges=%d|receipts=%d", receipt, observationCount, edgeCount, receiptCount))
}

// ---------------------------------------------------------------------------
// Review R3 findings: exact ErrAlreadyExists tolerance, handoff content
// envelope, caller-hash verification, supersedes atomicity.
// ---------------------------------------------------------------------------

func TestSQLiteHandoff_RelationErrAlreadyExists_ExactMatchDecidesTolerance(t *testing.T) {
	t.Run("ERROR_preexisting_current_fact_from_different_origin_conflicts_and_rolls_back", func(t *testing.T) {
		db, _ := openHandoffDB(t)
		defer func() { _ = db.Close() }()
		targetID := seedHandoffTarget(t, db)
		// A different-origin current fact pointing at the same target with the
		// same relation type occupies the v2 one-current-fact index.
		other := &domain.Observation{SessionID: handoffTestSession, Project: "handoff-project", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "other origin", Content: "other"}
		if err := sqlitestore.NewStore(db).Save(context.Background(), other); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO edges (from_obs_id,to_obs_id,relation_type,weight,confidence,reasoning) VALUES (?,?,?,?,?,?)`, other.ID, targetID, domain.RelationReferences, 9, 0.1, "other origin"); err != nil {
			t.Fatal(err)
		}

		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("fresh handoff", "payload")
		canonical.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences, Weight: 1, Confidence: 0.9, Reasoning: "canonical"}
		assertHandoffError(t, executor, "scope:edge", "other-origin", canonical, hashOf(t, canonical), domain.ErrHandoffConflict)

		var receipts, fresh, pairEdges int
		if err := db.QueryRow(`SELECT COUNT(*) FROM handoff_receipts WHERE scope='scope:edge'`).Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM observations WHERE title='fresh handoff'`).Scan(&fresh); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE to_obs_id=? AND relation_type=?`, targetID, domain.RelationReferences).Scan(&pairEdges); err != nil {
			t.Fatal(err)
		}
		if receipts != 0 || fresh != 0 || pairEdges != 1 {
			t.Fatalf("conflict left receipts=%d fresh=%d pair_edges=%d, want 0/0/1", receipts, fresh, pairEdges)
		}
	})

	t.Run("HAPPY_exact_durable_duplicate_on_reused_observation_is_tolerated", func(t *testing.T) {
		db, _ := openHandoffDB(t)
		defer func() { _ = db.Close() }()
		targetID := seedHandoffTarget(t, db)
		reused := &domain.Observation{SessionID: handoffTestSession, Project: "handoff-project", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "reused base", Content: "base content", TopicKey: "handoff/exact-duplicate"}
		if err := sqlitestore.NewStore(db).Save(context.Background(), reused); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO edges (from_obs_id,to_obs_id,relation_type,weight,confidence,reasoning) VALUES (?,?,?,?,?,?)`, reused.ID, targetID, domain.RelationReferences, 2.5, 0.8, "identical reasoning"); err != nil {
			t.Fatal(err)
		}

		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("reused base", "upserted content")
		canonical.Observation.TopicKey = "handoff/exact-duplicate"
		canonical.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences, Weight: 2.5, Confidence: 0.8, Reasoning: "identical reasoning"}
		result := executeHandoff(t, executor, "scope:edge", "exact-duplicate", canonical)
		if result.Status != domain.WriteStatusUpdated {
			t.Fatalf("result=%+v, want updated via topic reuse", result)
		}
		assertHandoffState(t, db, "scope:edge", "exact-duplicate", canonical, 1, 1, 1)
		var pairEdges, currentFacts int
		if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE from_obs_id=? AND to_obs_id=? AND relation_type=?`, reused.ID, targetID, domain.RelationReferences).Scan(&pairEdges); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE from_obs_id=? AND to_obs_id=? AND relation_type=? AND valid_until IS NULL AND fact_state='current'`, reused.ID, targetID, domain.RelationReferences).Scan(&currentFacts); err != nil {
			t.Fatal(err)
		}
		if pairEdges != 1 || currentFacts != 1 {
			t.Fatalf("tolerated duplicate mutated edges: pair=%d current=%d, want 1/1", pairEdges, currentFacts)
		}
	})

	t.Run("ERROR_preexisting_fact_with_different_attributes_conflicts_and_rolls_back", func(t *testing.T) {
		db, _ := openHandoffDB(t)
		defer func() { _ = db.Close() }()
		targetID := seedHandoffTarget(t, db)
		reused := &domain.Observation{SessionID: handoffTestSession, Project: "handoff-project", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "attr base", Content: "attr base content", TopicKey: "handoff/attr-conflict"}
		if err := sqlitestore.NewStore(db).Save(context.Background(), reused); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO edges (from_obs_id,to_obs_id,relation_type,weight,confidence,reasoning) VALUES (?,?,?,?,?,?)`, reused.ID, targetID, domain.RelationReferences, 7, 0.2, "different reasoning"); err != nil {
			t.Fatal(err)
		}

		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("attr base", "upserted content that must roll back")
		canonical.Observation.TopicKey = "handoff/attr-conflict"
		canonical.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences, Weight: 7, Confidence: 0.9, Reasoning: "different reasoning"}
		assertHandoffError(t, executor, "scope:edge", "attr-conflict", canonical, hashOf(t, canonical), domain.ErrHandoffConflict)

		var content string
		if err := db.QueryRow(`SELECT content FROM observations WHERE id=?`, reused.ID).Scan(&content); err != nil {
			t.Fatal(err)
		}
		if content != "attr base content" {
			t.Fatalf("topic upsert survived conflict rollback: content=%q", content)
		}
		var receipts int
		if err := db.QueryRow(`SELECT COUNT(*) FROM handoff_receipts WHERE scope='scope:edge'`).Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		if receipts != 0 {
			t.Fatalf("conflict left receipts=%d", receipts)
		}
	})
}

func TestSQLiteHandoff_CallerHashDiscordantRejectedBeforeReceipt(t *testing.T) {
	db, _ := openHandoffDB(t)
	defer func() { _ = db.Close() }()
	seedHandoffTarget(t, db)
	executor := newTestHandoffExecutor(db, nil)
	canonical := handoffCanonical("hash guard", "payload under hash guard")
	_, hash := canonicalPayload(t, canonical)
	hash[0] ^= 0xFF
	assertHandoffError(t, executor, "scope:hash", "lying-hash", canonical, hash, domain.ErrHandoffValidation)

	var observations, receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM observations WHERE title='hash guard'`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM handoff_receipts WHERE scope='scope:hash'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if observations != 0 || receipts != 0 {
		t.Fatalf("discordant hash left observations=%d receipts=%d", observations, receipts)
	}

	result := executeHandoff(t, executor, "scope:hash", "honest-hash", canonical)
	if result.Status != domain.WriteStatusCreated {
		t.Fatalf("honest retry result=%+v, want created", result)
	}
}

func TestSQLiteHandoff_SupersedesRelationAtomicity(t *testing.T) {
	db, _ := openHandoffDB(t)
	defer func() { _ = db.Close() }()
	targetID := seedHandoffTarget(t, db)
	reused := &domain.Observation{SessionID: handoffTestSession, Project: "handoff-project", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "supersedes base", Content: "before supersedes", TopicKey: "handoff/supersedes"}
	if err := sqlitestore.NewStore(db).Save(context.Background(), reused); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO edges (from_obs_id,to_obs_id,relation_type,weight,confidence,reasoning) VALUES (?,?,?,?,?,?)`, reused.ID, targetID, domain.RelationSupersedes, 1, 1, "predecessor"); err != nil {
		t.Fatal(err)
	}

	canonical := handoffCanonical("supersedes base", "after supersedes")
	canonical.Observation.TopicKey = "handoff/supersedes"
	canonical.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationSupersedes, Weight: 1, Confidence: 1, Reasoning: "successor"}

	// Failure after the predecessor closure + successor insert: rollback must
	// restore the predecessor as the single current fact.
	rollbackExec := newTestHandoffExecutor(db, func(stage sqliteHandoffStage) error {
		if stage == handoffBeforeCommit {
			return errHandoffFailpoint
		}
		return nil
	})
	if _, err := rollbackExec.ExecuteHandoff(context.Background(), "scope:sup", "sup-key", canonical, hashOf(t, canonical)); !errors.Is(err, errHandoffFailpoint) {
		t.Fatalf("ExecuteHandoff error=%v, want failpoint", err)
	}
	var pairEdges, currentFacts, openPredecessor, receipts int
	counts := func() {
		t.Helper()
		for _, q := range []struct {
			query string
			out   *int
		}{
			{`SELECT COUNT(*) FROM edges WHERE from_obs_id=? AND to_obs_id=? AND relation_type=?`, &pairEdges},
			{`SELECT COUNT(*) FROM edges WHERE from_obs_id=? AND to_obs_id=? AND relation_type=? AND valid_until IS NULL AND fact_state='current'`, &currentFacts},
			{`SELECT COUNT(*) FROM edges WHERE from_obs_id=? AND to_obs_id=? AND relation_type=? AND reasoning='predecessor' AND valid_until IS NULL`, &openPredecessor},
			{`SELECT COUNT(*) FROM handoff_receipts WHERE scope='scope:sup'`, &receipts},
		} {
			if err := db.QueryRow(q.query, reused.ID, targetID, domain.RelationSupersedes).Scan(q.out); err != nil {
				t.Fatal(err)
			}
		}
	}
	counts()
	if pairEdges != 1 || currentFacts != 1 || openPredecessor != 1 || receipts != 0 {
		t.Fatalf("rollback state pair=%d current=%d open_predecessor=%d receipts=%d, want 1/1/1/0", pairEdges, currentFacts, openPredecessor, receipts)
	}

	// Same handoff without the failpoint commits closure + successor atomically.
	result := executeHandoff(t, newTestHandoffExecutor(db, nil), "scope:sup", "sup-key", canonical)
	if result.Status != domain.WriteStatusUpdated {
		t.Fatalf("result=%+v, want updated", result)
	}
	counts()
	if pairEdges != 2 || currentFacts != 1 || openPredecessor != 0 || receipts != 1 {
		t.Fatalf("committed state pair=%d current=%d open_predecessor=%d receipts=%d, want 2/1/0/1", pairEdges, currentFacts, openPredecessor, receipts)
	}
}

func TestSQLiteHandoff_ContentEnvelopeEndToEnd(t *testing.T) {
	t.Run("HAPPY_content_over_legacy_limit_under_handoff_envelope_materializes", func(t *testing.T) {
		db, _ := openHandoffDB(t)
		defer func() { _ = db.Close() }()
		seedHandoffTarget(t, db)
		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("envelope ok", strings.Repeat("e", 200_000))
		result := executeHandoff(t, executor, "scope:envelope", "within-1mib", canonical)
		if result.Status != domain.WriteStatusCreated {
			t.Fatalf("result=%+v, want created", result)
		}
		assertHandoffState(t, db, "scope:envelope", "within-1mib", canonical, 1, 0, 1)
	})

	t.Run("ERROR_content_over_handoff_envelope_rejected_by_domain_bound", func(t *testing.T) {
		db, _ := openHandoffDB(t)
		defer func() { _ = db.Close() }()
		executor := newTestHandoffExecutor(db, nil)
		canonical := handoffCanonical("envelope bad", strings.Repeat("x", domain.MaxHandoffPayloadSize+1))
		assertHandoffError(t, executor, "scope:envelope", "over-1mib", canonical, [32]byte{}, domain.ErrHandoffPayloadTooLarge)
		var observations, receipts int
		if err := db.QueryRow(`SELECT COUNT(*) FROM observations WHERE title='envelope bad'`).Scan(&observations); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM handoff_receipts WHERE scope='scope:envelope'`).Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		if observations != 0 || receipts != 0 {
			t.Fatalf("oversized handoff left observations=%d receipts=%d", observations, receipts)
		}
	})
}

// hashOf derives the canonical payload hash for a CanonicalHandoff.
func hashOf(t *testing.T, canonical domain.CanonicalHandoff) [32]byte {
	t.Helper()
	_, hash := canonicalPayload(t, canonical)
	return hash
}
