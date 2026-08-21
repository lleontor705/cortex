//go:build postgres_integration

package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
)

func newReceiptTestStore(t *testing.T, h *postgresHarness) (*AuthorizedStore, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "receipt", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	return store, tenant
}

func applyReceiptMigration(t *testing.T) {
	t.Helper()
	db, err := sql.Open("pgx", migrationDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close migration database: %v", err)
		}
	})
	if err := migration.ApplyPostgresServerMigrations(context.Background(), db); err != nil {
		t.Fatalf("apply receipt migration: %v", err)
	}
}

// saveReceiptObservation materializes the fixture effect observation for the
// 'receipt' project. Migration 105 requires every observation to resolve a
// session inside the bound workspace (the BEFORE trigger rejects a session
// that does not exist there), so the fixture first provisions — idempotently,
// through the partial client-id unique index — one 'receipt' session per
// (tenant, workspace) and then resolves the session inside that same bound
// workspace. This keeps the effect usable from any harness store, including
// sibling workspaces of one tenant, without weakening the trigger contract.
func saveReceiptObservation(ctx context.Context, title string) (domain.SaveEffect, error) {
	tx, ok := txFromContext(ctx)
	if !ok {
		return domain.SaveEffect{}, errors.New("receipt test effect requires transaction")
	}
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return domain.SaveEffect{}, errors.New("receipt test effect requires a bound workspace")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (tenant_id, workspace_id, client_id, project_key, started_at, created_by, updated_by)
		VALUES (public.cortex_current_tenant(), $1::bigint, 'receipt-fixture', 'receipt', now(), $2, $2)
		ON CONFLICT (tenant_id, workspace_id, client_id) WHERE client_id IS NOT NULL DO NOTHING`,
		ws, actorFromContext(ctx)); err != nil {
		return domain.SaveEffect{}, err
	}
	observation := &domain.Observation{Project: "receipt", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: title, Content: "receipt effect"}
	if err := tx.QueryRow(ctx, `INSERT INTO observations(tenant_id,session_id,project_key,scope,source,type,title,content,created_by,updated_by) VALUES(public.cortex_current_tenant(),(SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1::bigint AND project_key=$2 ORDER BY id DESC LIMIT 1),$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,public_id::text`, ws, observation.Project, observation.Scope, observation.Source, observation.Type, observation.Title, observation.Content, actorFromContext(ctx)).Scan(&observation.ID, &observation.PublicID); err != nil {
		return domain.SaveEffect{}, err
	}
	return domain.SaveEffect{Observation: observation, Status: domain.WriteStatusCreated}, nil
}

func TestPostgresHandoffReceiptClaimReadFinalizeReplayAndConflict(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorized, _ := newReceiptTestStore(t, h)
	store := authorized.store
	ctx := context.Background()
	payload := []byte(`{"observation":{"title":"full","content":"bytes"},"capability_tuple":{"opaque":true}}`)
	hash := sha256.Sum256(payload)

	var committed handoffReceipt
	if err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		receipt, claimed, err := store.claimHandoffReceipt(ctx, domain.HandoffScope("project:a"), "claim-read", hash, payload)
		if err != nil {
			return err
		}
		if !claimed || receipt.State != handoffReceiptPending || !bytes.Equal(receipt.CanonicalPayload, payload) {
			return fmt.Errorf("pending receipt=%+v claimed=%v", receipt, claimed)
		}
		effect, err := saveReceiptObservation(ctx, "claim-read")
		if err != nil {
			return err
		}
		committed, err = store.finalizeHandoffReceipt(ctx, domain.HandoffScope("project:a"), "claim-read", effect.Observation.ID, effect.Status)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if committed.State != handoffReceiptCommitted || committed.ObservationID == nil || committed.InitialStatus == nil || committed.CommittedAt == nil {
		t.Fatalf("committed receipt=%+v", committed)
	}

	if err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		replay, claimed, err := store.claimHandoffReceipt(ctx, domain.HandoffScope("project:a"), "claim-read", hash, append([]byte(nil), payload...))
		if err != nil {
			return err
		}
		if claimed || replay.ObservationID == nil || *replay.ObservationID != *committed.ObservationID {
			return fmt.Errorf("replay=%+v claimed=%v", replay, claimed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	collisionPayload := append([]byte(nil), payload...)
	collisionPayload[len(collisionPayload)-2] = 'f'
	err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		_, _, err := store.claimHandoffReceipt(ctx, domain.HandoffScope("project:a"), "claim-read", hash, collisionPayload)
		return err
	})
	if !errors.Is(err, domain.ErrHandoffConflict) {
		t.Fatalf("same hash with different full payload error=%v, want conflict", err)
	}
}

func TestPostgresHandoffReceiptRollbackRemovesPendingAndEffects(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorized, tenant := newReceiptTestStore(t, h)
	store := authorized.store
	ctx := context.Background()
	payload := []byte(`{"rollback":true}`)
	hash := sha256.Sum256(payload)
	wantErr := errors.New("fail before commit")
	err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		if _, claimed, err := store.claimHandoffReceipt(ctx, "project", "rollback", hash, payload); err != nil || !claimed {
			return fmt.Errorf("claim=%v err=%w", claimed, err)
		}
		if _, err := saveReceiptObservation(ctx, "rollback-effect"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("rollback error=%v", err)
	}
	var receipts, observations int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='rollback'`, tenant).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='rollback-effect'`, tenant).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || observations != 0 {
		t.Fatalf("after rollback receipts=%d observations=%d", receipts, observations)
	}
}

func TestPostgresHandoffReceiptTenantAndScopeIsolation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorizedA, _ := newReceiptTestStore(t, h)
	authorizedB, _ := newReceiptTestStore(t, h)
	ctx := context.Background()
	payload := []byte(`{"isolated":true}`)
	hash := sha256.Sum256(payload)

	claim := func(store *Store, scope domain.HandoffScope) error {
		return store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
			_, claimed, err := store.claimHandoffReceipt(ctx, scope, "same-key", hash, payload)
			if err == nil && !claimed {
				return errors.New("receipt leaked across tenant or scope")
			}
			return err
		})
	}
	if err := claim(authorizedA.store, "scope:a"); err != nil {
		t.Fatal(err)
	}
	if err := claim(authorizedA.store, "scope:b"); err != nil {
		t.Fatal(err)
	}
	if err := claim(authorizedB.store, "scope:a"); err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"other tenant": authorizedB.store, "other scope": authorizedA.store} {
		scope := domain.HandoffScope("scope:b")
		if name == "other scope" {
			scope = "scope:missing"
		}
		err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
			_, err := store.finalizeHandoffReceipt(ctx, scope, "same-key", 1, domain.WriteStatusCreated)
			return err
		})
		if err == nil {
			t.Fatalf("%s finalized an isolated receipt", name)
		}
	}
	err := authorizedB.store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		_, err := authorizedB.store.readHandoffReceipt(ctx, "scope:b", "same-key")
		return err
	})
	if err == nil {
		t.Fatal("tenant B observed tenant A scope B receipt")
	}
}

func TestPostgresHandoffReceiptConcurrentSameKey(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorized, tenant := newReceiptTestStore(t, h)
	store := authorized.store
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	payload := []byte(`{"concurrent":"same"}`)
	hash := sha256.Sum256(payload)
	start := make(chan struct{})
	results := make(chan domain.WriteStatus, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var status domain.WriteStatus
			err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
				receipt, claimed, err := store.claimHandoffReceipt(ctx, "project", "concurrent", hash, payload)
				if err != nil {
					return err
				}
				if !claimed {
					if receipt.State != handoffReceiptCommitted || receipt.ObservationID == nil {
						return fmt.Errorf("non-final replay receipt=%+v", receipt)
					}
					status = domain.WriteStatusReplayed
					return nil
				}
				effect, err := saveReceiptObservation(ctx, "concurrent-effect")
				if err != nil {
					return err
				}
				if _, err := store.finalizeHandoffReceipt(ctx, "project", "concurrent", effect.Observation.ID, effect.Status); err != nil {
					return err
				}
				status = domain.WriteStatusCreated
				return nil
			})
			if err != nil {
				errs <- err
				return
			}
			results <- status
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	close(results)
	created, replayed := 0, 0
	for status := range results {
		switch status {
		case domain.WriteStatusCreated:
			created++
		case domain.WriteStatusReplayed:
			replayed++
		}
	}
	var receiptCount, observationCount int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='concurrent'`, tenant).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='concurrent-effect'`, tenant).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if created != 1 || replayed != 7 || receiptCount != 1 || observationCount != 1 {
		t.Fatalf("created=%d replayed=%d receipts=%d observations=%d", created, replayed, receiptCount, observationCount)
	}

	conflictPayload := []byte(`{"concurrent":"different"}`)
	conflictHash := sha256.Sum256(conflictPayload)
	start = make(chan struct{})
	errs = make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
				_, _, err := store.claimHandoffReceipt(ctx, "project", "concurrent", conflictHash, conflictPayload)
				return err
			})
			if !errors.Is(err, domain.ErrHandoffConflict) {
				errs <- fmt.Errorf("concurrent conflict error=%v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// Workspace isolation for the receipt namespace inside one tenant: the same
// (scope, key) in two workspaces of one tenant must claim, finalize, and
// replay independently — migration 105 made the receipt primary key
// workspace scoped — while a sibling workspace can neither read nor finalize
// the other's receipt.
func TestPostgresHandoffReceiptWorkspaceNamespaceIsolation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspaceA, workspaceB := uuid.New(), uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []uuid.UUID{workspaceA, workspaceB} {
		if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, ws, ws.String()); err != nil {
			t.Fatal(err)
		}
	}
	storeA := newAuthorizedTestStore(t, h, tenant, workspaceA, uuid.New())
	storeB := newAuthorizedTestStore(t, h, tenant, workspaceB, uuid.New())
	payload := []byte(`{"workspace":"scoped"}`)
	hash := sha256.Sum256(payload)

	claim := func(store *Store, title string) (int64, error) {
		var observationID int64
		err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
			_, claimed, err := store.claimHandoffReceipt(ctx, domain.HandoffScope("project:iso"), "same-key", hash, payload)
			if err != nil {
				return err
			}
			if !claimed {
				return errors.New("sibling workspace receipt was not independently claimable")
			}
			effect, err := saveReceiptObservation(ctx, title)
			if err != nil {
				return err
			}
			observationID = effect.Observation.ID
			_, err = store.finalizeHandoffReceipt(ctx, domain.HandoffScope("project:iso"), "same-key", effect.Observation.ID, effect.Status)
			return err
		})
		return observationID, err
	}
	observationA, err := claim(storeA.store, "workspace-a-receipt")
	if err != nil {
		t.Fatal(err)
	}
	observationB, err := claim(storeB.store, "workspace-b-receipt")
	if err != nil {
		t.Fatal(err)
	}
	if observationA == observationB {
		t.Fatalf("sibling workspaces reused observation %d for the same key", observationA)
	}

	// Replays stay inside their own workspace namespace.
	replay := func(store *Store) error {
		return store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
			receipt, claimed, err := store.claimHandoffReceipt(ctx, domain.HandoffScope("project:iso"), "same-key", hash, payload)
			if err != nil {
				return err
			}
			if claimed || receipt.State != handoffReceiptCommitted || receipt.ObservationID == nil {
				return fmt.Errorf("workspace replay receipt=%+v claimed=%v", receipt, claimed)
			}
			return nil
		})
	}
	if err := replay(storeA.store); err != nil {
		t.Fatalf("workspace A replay: %v", err)
	}
	if err := replay(storeB.store); err != nil {
		t.Fatalf("workspace B replay: %v", err)
	}

	// A sibling workspace cannot read or finalize the other's receipt.
	err = storeB.store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		_, err := storeB.store.readHandoffReceipt(ctx, domain.HandoffScope("project:missing"), "same-key")
		return err
	})
	if err == nil {
		t.Fatal("workspace B read a receipt outside its namespace")
	}
	err = storeB.store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		_, err := storeB.store.finalizeHandoffReceipt(ctx, domain.HandoffScope("project:iso"), "same-key", observationA, domain.WriteStatusCreated)
		return err
	})
	if err == nil {
		t.Fatal("workspace B finalized workspace A's committed receipt")
	}

	var receiptsA, receiptsB int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM handoff_receipts r JOIN workspaces w ON w.tenant_id=r.tenant_id AND w.id=r.workspace_id WHERE r.tenant_id=$1 AND r.scope='project:iso' AND w.public_id=$2`, tenant, workspaceA).Scan(&receiptsA); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM handoff_receipts r JOIN workspaces w ON w.tenant_id=r.tenant_id AND w.id=r.workspace_id WHERE r.tenant_id=$1 AND r.scope='project:iso' AND w.public_id=$2`, tenant, workspaceB).Scan(&receiptsB); err != nil {
		t.Fatal(err)
	}
	if receiptsA != 1 || receiptsB != 1 {
		t.Fatalf("receipts per workspace A=%d B=%d, want 1/1", receiptsA, receiptsB)
	}
}

// The failpoint matrix proves rollback atomicity at every execution seam
// against a real database: after-save (observation materialized, no relation
// yet), after-edge (relation written, receipt pending), and before-commit
// (receipt finalized, commit imminent). Each seam leaves zero durable
// effects — receipts, observations, and edges all roll back — and a restart
// (fresh pools and store instances) retries the same key to exactly one
// materialization that then replays.
func TestPostgresHandoffExecutorFailpointMatrix(t *testing.T) {
	for _, seam := range []string{"after-save", "after-edge", "before-commit"} {
		t.Run(seam, func(t *testing.T) {
			h := newPostgresHarness(t)
			applyReceiptMigration(t)
			authorized, tenant, workspace, subject := newExecutorTestStore(t, h)
			store := authorized.store
			ctx := context.Background()
			key := "matrix-" + seam

			var targetPublic uuid.UUID
			if err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
				effect, err := saveReceiptObservation(ctx, "matrix-target")
				if err != nil {
					return err
				}
				targetPublic, err = uuid.Parse(effect.Observation.PublicID)
				return err
			}); err != nil {
				t.Fatal(err)
			}

			canonical := domain.CanonicalHandoff{
				Observation: domain.SaveObservationInput{Title: seam + " handoff", Content: "matrix payload", Type: domain.TypeDecision, Project: "handoff-exec"},
				Relation:    &domain.HandoffRelationInput{Target: domain.ObservationRef{PublicID: &targetPublic}, Type: domain.RelationReferences, Weight: 1, Confidence: 1, Reasoning: "matrix relation"},
			}
			_, hash := executorCanonical(t, canonical)

			handoffFailpoints = func(s string) error {
				if s == seam {
					return errors.New("seam failure: " + seam)
				}
				return nil
			}
			t.Cleanup(func() { handoffFailpoints = func(string) error { return nil } })

			if _, err := store.ExecuteHandoff(ctx, "project:matrix", key, canonical, hash); err == nil {
				t.Fatalf("%s failpoint did not fail the handoff", seam)
			}
			for name, query := range map[string]string{
				"receipts":     `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='` + key + `'`,
				"observations": `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='` + seam + ` handoff'`,
				"edges":        `SELECT count(*) FROM edges WHERE tenant_id=$1 AND reasoning='matrix relation'`,
			} {
				if n := countForTenant(t, ctx, h, query, tenant); n != 0 {
					t.Fatalf("%s seam left durable %s=%d, want 0", seam, name, n)
				}
			}

			// Restart: brand-new pools and store instances against the same
			// database; the failed attempt must not poison the retry.
			handoffFailpoints = func(string) error { return nil }
			restartedHarness := newPostgresHarness(t)
			applyReceiptMigration(t)
			restartedStore := newAuthorizedTestStore(t, restartedHarness, tenant, workspace, subject)
			created, err := restartedStore.store.ExecuteHandoff(ctx, "project:matrix", key, canonical, hash)
			if err != nil || created.Status != domain.WriteStatusCreated || created.Ref.PublicID == nil {
				t.Fatalf("%s restart created=%+v err=%v", seam, created, err)
			}
			for name, query := range map[string]string{
				"receipts":     `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='` + key + `'`,
				"observations": `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='` + seam + ` handoff'`,
				"edges":        `SELECT count(*) FROM edges WHERE tenant_id=$1 AND reasoning='matrix relation'`,
			} {
				if n := countForTenant(t, ctx, restartedHarness, query, tenant); n != 1 {
					t.Fatalf("%s restart durable %s=%d, want 1", seam, name, n)
				}
			}
			replayed, err := restartedStore.store.ExecuteHandoff(ctx, "project:matrix", key, canonical, hash)
			if err != nil || replayed.Status != domain.WriteStatusReplayed || *replayed.Ref.PublicID != *created.Ref.PublicID {
				t.Fatalf("%s replay=%+v err=%v", seam, replayed, err)
			}
		})
	}
}

// newExecutorTestStore provisions an isolated tenant with one session and
// returns the identifiers needed to rebuild the same authorized store after a
// simulated restart on a fresh connection pool.
func newExecutorTestStore(t *testing.T, h *postgresHarness) (*AuthorizedStore, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tenant, workspace, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, subject)
	session := &domain.Session{Project: "handoff-exec", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	return store, tenant, workspace, subject
}

func executorCanonical(t *testing.T, canonical domain.CanonicalHandoff) ([]byte, [32]byte) {
	t.Helper()
	payload, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return payload, sha256.Sum256(payload)
}

func countForTenant(t *testing.T, ctx context.Context, h *postgresHarness, query string, args ...any) int {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostgresHandoffExecutorCreateReplayConflictAndRestart(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorized, tenant, workspace, subject := newExecutorTestStore(t, h)
	store := authorized.store
	ctx := context.Background()

	var targetPublic uuid.UUID
	if err := store.transaction(ctx, func(ctx context.Context, _ pgx.Tx) error {
		effect, err := saveReceiptObservation(ctx, "relation-target")
		if err != nil {
			return err
		}
		targetPublic, err = uuid.Parse(effect.Observation.PublicID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	canonical := domain.CanonicalHandoff{
		Observation: domain.SaveObservationInput{Title: "executor handoff", Content: "full payload Î© canary", Type: domain.TypeDecision, Project: "handoff-exec"},
		Relation:    &domain.HandoffRelationInput{Target: domain.ObservationRef{PublicID: &targetPublic}, Type: domain.RelationReferences, Weight: 1, Confidence: 0.9, Reasoning: "executor relation"},
	}
	_, hash := executorCanonical(t, canonical)

	created, err := store.ExecuteHandoff(ctx, "project:executor", "exec-key", canonical, hash)
	if err != nil || created.Status != domain.WriteStatusCreated || created.Ref.PublicID == nil || created.Ref.LocalID != nil {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	receipts := countForTenant(t, ctx, h, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='exec-key' AND state='committed'`, tenant)
	observations := countForTenant(t, ctx, h, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='executor handoff'`, tenant)
	edges := countForTenant(t, ctx, h, `SELECT count(*) FROM edges WHERE tenant_id=$1 AND reasoning='executor relation'`, tenant)
	if receipts != 1 || observations != 1 || edges != 1 {
		t.Fatalf("after create receipts=%d observations=%d edges=%d", receipts, observations, edges)
	}

	replayed, err := store.ExecuteHandoff(ctx, "project:executor", "exec-key", canonical, hash)
	if err != nil || replayed.Status != domain.WriteStatusReplayed || replayed.Ref.PublicID == nil || *replayed.Ref.PublicID != *created.Ref.PublicID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}

	// Restart: brand-new pools and store instances against the same database.
	restartedHarness := newPostgresHarness(t)
	applyReceiptMigration(t)
	restartedStore := newAuthorizedTestStore(t, restartedHarness, tenant, workspace, subject)
	postRestart, err := restartedStore.store.ExecuteHandoff(ctx, "project:executor", "exec-key", canonical, hash)
	if err != nil || postRestart.Status != domain.WriteStatusReplayed || *postRestart.Ref.PublicID != *created.Ref.PublicID {
		t.Fatalf("postRestart=%+v err=%v", postRestart, err)
	}
	if n := countForTenant(t, ctx, restartedHarness, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='executor handoff'`, tenant); n != 1 {
		t.Fatalf("restart rematerialized observations=%d", n)
	}

	conflicting := canonical
	conflicting.Observation.Content = "different full payload"
	_, conflictingHash := executorCanonical(t, conflicting)
	if _, err := restartedStore.store.ExecuteHandoff(ctx, "project:executor", "exec-key", conflicting, conflictingHash); !errors.Is(err, domain.ErrHandoffConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if n := countForTenant(t, ctx, restartedHarness, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='exec-key'`, tenant); n != 1 {
		t.Fatalf("conflict mutated receipts=%d", n)
	}
}

func TestPostgresHandoffExecutorValidationLeavesZeroEffects(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorized, tenant, _, _ := newExecutorTestStore(t, h)
	store := authorized.store
	ctx := context.Background()

	localID := int64(1)
	canonical := domain.CanonicalHandoff{
		Observation: domain.SaveObservationInput{Title: "zero effects", Content: "never materialized", Type: domain.TypeDecision, Project: "handoff-exec"},
		Relation:    &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &localID}, Type: domain.RelationReferences},
	}
	_, hash := executorCanonical(t, canonical)
	if _, err := store.ExecuteHandoff(ctx, "project:executor", "zero-effects", canonical, hash); !errors.Is(err, domain.ErrHandoffValidation) {
		t.Fatalf("local-namespace target error=%v, want validation", err)
	}
	receipts := countForTenant(t, ctx, h, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='zero-effects'`, tenant)
	observations := countForTenant(t, ctx, h, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='zero effects'`, tenant)
	if receipts != 0 || observations != 0 {
		t.Fatalf("validation left effects receipts=%d observations=%d", receipts, observations)
	}
}

func TestPostgresHandoffExecutorConcurrentSameKey(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	authorized, tenant, _, _ := newExecutorTestStore(t, h)
	store := authorized.store
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	canonical := domain.CanonicalHandoff{
		Observation: domain.SaveObservationInput{Title: "concurrent executor", Content: "single materialization Î©", Type: domain.TypeDecision, Project: "handoff-exec"},
	}
	_, hash := executorCanonical(t, canonical)

	start := make(chan struct{})
	results := make(chan domain.WriteStatus, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := store.ExecuteHandoff(ctx, "project:concurrent", "exec-concurrent", canonical, hash)
			if err != nil {
				errs <- err
				return
			}
			results <- result.Status
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	close(results)
	created, replayed := 0, 0
	for status := range results {
		switch status {
		case domain.WriteStatusCreated:
			created++
		case domain.WriteStatusReplayed:
			replayed++
		}
	}
	receipts := countForTenant(t, ctx, h, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='exec-concurrent'`, tenant)
	observations := countForTenant(t, ctx, h, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='concurrent executor'`, tenant)
	if created != 1 || replayed != 7 || receipts != 1 || observations != 1 {
		t.Fatalf("created=%d replayed=%d receipts=%d observations=%d", created, replayed, receipts, observations)
	}
}

// Workspace isolation inside one tenant: handoff session resolution, relation
// targets, and dedup candidates from a sibling workspace must be invisible to
// a store bound to another workspace, while the same flows succeed inside the
// bound workspace.
func TestPostgresHandoffExecutorWorkspaceIsolation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspaceA, workspaceB := uuid.New(), uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []uuid.UUID{workspaceA, workspaceB} {
		if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, ws, ws.String()); err != nil {
			t.Fatal(err)
		}
	}
	storeA := newAuthorizedTestStore(t, h, tenant, workspaceA, uuid.New())
	storeB := newAuthorizedTestStore(t, h, tenant, workspaceB, uuid.New())

	sessionA := &domain.Session{Project: "iso", StartedAt: time.Now().UTC()}
	if err := storeA.sessions().Create(ctx, sessionA); err != nil {
		t.Fatal(err)
	}
	sessionB := &domain.Session{Project: "iso", StartedAt: time.Now().UTC()}
	if err := storeB.sessions().Create(ctx, sessionB); err != nil {
		t.Fatal(err)
	}
	// A manual observation already lives in workspace B with identical bytes
	// to the upcoming handoff payload, plus a relation target in B.
	shared := &domain.Observation{SessionID: sessionB.ID, Project: "iso", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "shared", Content: "shared bytes"}
	if _, err := storeB.observations().SaveWithEffect(ctx, shared); err != nil {
		t.Fatal(err)
	}
	targetB := &domain.Observation{SessionID: sessionB.ID, Project: "iso", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "b-target", Content: "b-target"}
	if _, err := storeB.observations().SaveWithEffect(ctx, targetB); err != nil {
		t.Fatal(err)
	}
	targetA := &domain.Observation{SessionID: sessionA.ID, Project: "iso", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "a-target", Content: "a-target"}
	if _, err := storeA.observations().SaveWithEffect(ctx, targetA); err != nil {
		t.Fatal(err)
	}

	executor := func(session, title string, target *uuid.UUID) (domain.CanonicalHandoff, [32]byte) {
		c := domain.CanonicalHandoff{Observation: domain.SaveObservationInput{Title: title, Content: title + " bytes", Type: domain.TypeManual, Project: "iso", SessionID: session}}
		if target != nil {
			c.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{PublicID: target}, Type: domain.RelationReferences, Weight: 1, Confidence: 1}
		}
		_, hash := executorCanonical(t, c)
		return c, hash
	}

	// An explicit session of workspace B is invisible to the A-bound store.
	crossSession, hash := executor(sessionB.ID, "cross-session", nil)
	if _, err := storeA.store.ExecuteHandoff(ctx, "project:iso", "cross-session", crossSession, hash); !errors.Is(err, domain.ErrHandoffValidation) {
		t.Fatalf("cross-workspace session error=%v, want validation", err)
	}
	// A relation target of workspace B is invisible to the A-bound store.
	targetRef, _ := uuid.Parse(targetB.PublicID)
	crossTarget, hash := executor(sessionA.ID, "cross-target", &targetRef)
	if _, err := storeA.store.ExecuteHandoff(ctx, "project:iso", "cross-target", crossTarget, hash); !errors.Is(err, domain.ErrHandoffValidation) {
		t.Fatalf("cross-workspace relation target error=%v, want validation", err)
	}

	// Dedup is workspace scoped: B's identical manual bytes must not turn the
	// A handoff into a dedup replay; the observation materializes in A.
	dedup, hash := executor(sessionA.ID, "shared", nil)
	created, err := storeA.store.ExecuteHandoff(ctx, "project:iso", "dedup-cross", dedup, hash)
	if err != nil || created.Status != domain.WriteStatusCreated {
		t.Fatalf("cross-workspace dedup result=%+v err=%v, want created", created, err)
	}

	// Same flows succeed inside the bound workspace.
	targetARef, _ := uuid.Parse(targetA.PublicID)
	inWorkspace, hash := executor(sessionA.ID, "in-workspace", &targetARef)
	related, err := storeA.store.ExecuteHandoff(ctx, "project:iso", "in-workspace", inWorkspace, hash)
	if err != nil || related.Status != domain.WriteStatusCreated {
		t.Fatalf("in-workspace result=%+v err=%v", related, err)
	}

	var sharedRows, targetARows int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id WHERE o.tenant_id=$1 AND o.title='shared' AND w.public_id=$2`, tenant, workspaceA).Scan(&sharedRows); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM edges WHERE tenant_id=$1 AND to_observation_id=(SELECT id FROM observations WHERE tenant_id=$1 AND title='a-target' LIMIT 1) AND source='handoff'`, tenant).Scan(&targetARows); err != nil {
		t.Fatal(err)
	}
	if sharedRows != 1 {
		t.Fatalf("workspace A shared rows=%d, want exactly the handoff materialization", sharedRows)
	}
	if targetARows != 1 {
		t.Fatalf("in-workspace handoff edges=%d, want 1", targetARows)
	}
}
