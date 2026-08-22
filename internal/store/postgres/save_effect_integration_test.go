//go:build postgres_integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/domain"
)

// REM-SAVE-001: the interactive save path keeps the legacy observable — topic
// upsert plus unconditional insert. Manual duplicates never dedup-replay on
// the PostgreSQL backend; the dedup classification belongs to the handoff
// materialization core only.
func TestPostgresObservationSaveWithEffect(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	tenant, workspace := uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "save-effect", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	repo := store.observations()

	created := &domain.Observation{SessionID: session.ID, Project: "save-effect", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "created", Content: "content"}
	effect, err := repo.SaveWithEffect(ctx, created)
	if err != nil || effect.Status != domain.WriteStatusCreated || effect.Observation != created || created.PublicID == "" {
		t.Fatalf("created effect=%+v obs=%+v err=%v", effect, created, err)
	}
	// Baseline observable: an identical manual save inserts unconditionally
	// instead of dedup-replaying.
	duplicate := &domain.Observation{SessionID: session.ID, Project: "save-effect", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "created", Content: "content"}
	effect, err = repo.SaveWithEffect(ctx, duplicate)
	if err != nil || effect.Status != domain.WriteStatusCreated || duplicate.PublicID == "" || duplicate.PublicID == created.PublicID {
		t.Fatalf("duplicate must insert unconditionally, effect=%+v obs=%+v err=%v", effect, duplicate, err)
	}

	topic := &domain.Observation{SessionID: session.ID, Project: "save-effect", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "topic", Content: "v1", TopicKey: "save/effect"}
	if effect, err = repo.SaveWithEffect(ctx, topic); err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("topic create effect=%+v err=%v", effect, err)
	}
	updated := &domain.Observation{SessionID: session.ID, Project: "save-effect", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "topic", Content: "v2", TopicKey: " save/effect "}
	if effect, err = repo.SaveWithEffect(ctx, updated); err != nil || effect.Status != domain.WriteStatusUpdated || updated.PublicID != topic.PublicID {
		t.Fatalf("topic upsert with padded key effect=%+v obs=%+v err=%v", effect, updated, err)
	}

	bad := &domain.Observation{SessionID: uuid.NewString(), Project: "save-effect", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "rollback", Content: "content"}
	if effect, err = repo.SaveWithEffect(ctx, bad); err == nil || effect.Observation != nil {
		t.Fatalf("failed effect=%+v err=%v", effect, err)
	}
	var count int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='rollback'`, tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback rows=%d err=%v", count, err)
	}
	var durable int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND title='created'`, tenant).Scan(&durable); err != nil || durable != 2 {
		t.Fatalf("duplicate inserts durable rows=%d err=%v", durable, err)
	}

	legacy := &domain.Observation{SessionID: session.ID, Project: "save-effect", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "created", Content: "content"}
	if err := repo.Save(ctx, legacy); err != nil || legacy.PublicID == "" {
		t.Fatalf("legacy duplicate must succeed unconditionally, err=%v obs=%+v", err, legacy)
	}
}

// Workspace isolation for active topic keys inside one tenant: since
// migration 105 the active-topic unique index is workspace scoped, so the
// same (project, topic) in two workspaces of one tenant must both materialize
// instead of colliding on a tenant-wide 23505.
func TestPostgresObservationSiblingWorkspaceTopicIsolation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	tenant, workspaceA, workspaceB := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
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

	save := func(store *AuthorizedStore) (*domain.Observation, error) {
		session := &domain.Session{Project: "sibling-topic", StartedAt: time.Now().UTC()}
		if err := store.sessions().Create(ctx, session); err != nil {
			return nil, err
		}
		obs := &domain.Observation{SessionID: session.ID, Project: "sibling-topic", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "sibling", Content: "same topic key", TopicKey: "sibling/topic"}
		effect, err := store.observations().SaveWithEffect(ctx, obs)
		if err != nil {
			return nil, err
		}
		if effect.Status != domain.WriteStatusCreated {
			return nil, fmt.Errorf("sibling workspace status=%q, want created", effect.Status)
		}
		return obs, nil
	}
	obsA, err := save(storeA)
	if err != nil {
		t.Fatalf("workspace A sibling topic: %v", err)
	}
	obsB, err := save(storeB)
	if err != nil {
		t.Fatalf("workspace B sibling topic: %v", err)
	}
	if obsA.PublicID == obsB.PublicID {
		t.Fatalf("sibling workspaces reused observation %s", obsA.PublicID)
	}

	// A workspace-scoped update of the same topic stays inside its workspace:
	// workspace A's second save updates only its own row, never B's.
	obsA2 := &domain.Observation{SessionID: obsA.SessionID, Project: "sibling-topic", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "sibling", Content: "updated in A", TopicKey: " sibling/topic "}
	effect, err := storeA.observations().SaveWithEffect(ctx, obsA2)
	if err != nil || effect.Status != domain.WriteStatusUpdated || obsA2.PublicID != obsA.PublicID {
		t.Fatalf("workspace A topic update effect=%+v err=%v, want in-workspace updated", effect, err)
	}

	var rowsA, rowsB int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id WHERE o.tenant_id=$1 AND o.topic_key='sibling/topic' AND o.deleted_at IS NULL AND w.public_id=$2`, tenant, workspaceA).Scan(&rowsA); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id WHERE o.tenant_id=$1 AND o.topic_key='sibling/topic' AND o.deleted_at IS NULL AND w.public_id=$2`, tenant, workspaceB).Scan(&rowsB); err != nil {
		t.Fatal(err)
	}
	if rowsA != 1 || rowsB != 1 {
		t.Fatalf("active topic rows per workspace A=%d B=%d, want 1/1", rowsA, rowsB)
	}
}

// Concurrent first upserts of one topic key must serialize through the
// advisory transaction lock: exactly one row is created and every later save
// updates it, with no 23505 leaking from observations_topic_key_active_uq.
func TestPostgresObservationSaveConcurrentTopicUpsert(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	tenant, workspace := uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "topic-race", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	repo := store.observations()

	const workers = 8
	start := make(chan struct{})
	statuses := make(chan domain.WriteStatus, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			obs := &domain.Observation{SessionID: session.ID, Project: "topic-race", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "race", Content: "race body", TopicKey: "race/topic"}
			effect, err := repo.SaveWithEffect(ctx, obs)
			if err != nil {
				errs <- err
				return
			}
			statuses <- effect.Status
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	close(statuses)
	created, updated := 0, 0
	for status := range statuses {
		switch status {
		case domain.WriteStatusCreated:
			created++
		case domain.WriteStatusUpdated:
			updated++
		}
	}
	if created != 1 || updated != workers-1 {
		t.Fatalf("created=%d updated=%d, want 1/%d", created, updated, workers-1)
	}
	var rows int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND topic_key='race/topic' AND deleted_at IS NULL`, tenant).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("topic rows=%d err=%v, want exactly one live topic observation", rows, err)
	}
}

// Real-contention proof of the bounded topic lock (SQLSTATE 55P03): a
// separate transaction derives exactly the same advisory lock key the save
// path uses (tenant, workspace, project, topic, JSON framed) and holds the
// xact lock longer than lock_timeout. SaveWithEffect must then wait only the
// bounded window, fail with the retryable-unavailable taxonomy, and keep the
// driver detail (SQLSTATE, raw message) out of the surfaced error. Releasing
// the holder must let an identical retry succeed, proving the key derivation
// matched and nothing leaked.
func TestPostgresObservationSaveTopicLockTimeoutUnderHeldAdvisoryLock(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	tenant, workspace := uuid.New(), uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "lock-contention", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	repo := store.observations()

	const project, topic = "lock-contention", "held/lock/topic"
	var workspaceRow int64
	if err := h.admin.QueryRow(ctx, `SELECT id FROM workspaces WHERE tenant_id=$1 AND public_id=$2`, tenant, workspace).Scan(&workspaceRow); err != nil {
		t.Fatal(err)
	}

	// Holder: its own connection and transaction, deriving the identical
	// lock key through the same framing the save path uses.
	holder, err := h.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	release := func() { _ = holder.Rollback(ctx) }
	defer release()
	// Casts mirror the production statement exactly: untyped parameters
	// inside jsonb_build_array cannot be resolved by PostgreSQL (42P18) and
	// the holder must derive the identical lock key the save path takes.
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array($1::text, $2::bigint, $3::text, $4::text)::text, 0))`, tenant.String(), workspaceRow, project, topic); err != nil {
		t.Fatalf("acquire holder lock: %v", err)
	}

	obs := &domain.Observation{SessionID: session.ID, Project: project, Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "contended", Content: "waits behind the holder", TopicKey: topic}
	started := time.Now()
	_, err = repo.SaveWithEffect(ctx, obs)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("save behind a held advisory lock unexpectedly succeeded")
	}
	var typed *domain.HandoffError
	if !errors.As(err, &typed) || typed.Code != domain.HandoffErrorUnavailable || !typed.Retryable {
		t.Fatalf("SaveWithEffect error=%v (%T), want retryable unavailable HandoffError", err, err)
	}
	if !errors.Is(err, domain.ErrHandoffUnavailable) {
		t.Fatalf("error %v does not match the unavailable sentinel", err)
	}
	if typed.Message != domain.ErrHandoffUnavailable.Message {
		t.Fatalf("surfaced message=%q, want the redacted sentinel %q", typed.Message, domain.ErrHandoffUnavailable.Message)
	}
	if leaked := err.Error(); strings.Contains(leaked, "55P03") || strings.Contains(leaked, "lock_not_available") || strings.Contains(strings.ToLower(leaked), "lock timeout") {
		t.Fatalf("raw SQLSTATE/driver detail leaked: %s", leaked)
	}
	const lockTimeout = 5 * time.Second
	if elapsed < lockTimeout-time.Second {
		t.Fatalf("elapsed=%v, want a real wait of ~%v behind the holder", elapsed, lockTimeout)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("elapsed=%v, want the wait bounded well below indefinite", elapsed)
	}

	// Robust cleanup: release the holder and prove an identical retry
	// succeeds through the public API, so the advisory lock and the key
	// derivation are both confirmed recovered.
	release()
	retry := &domain.Observation{SessionID: session.ID, Project: project, Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeDecision, Title: "contended", Content: "retry after release", TopicKey: topic}
	effect, err := repo.SaveWithEffect(ctx, retry)
	if err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("retry after release effect=%+v err=%v, want created", effect, err)
	}
	var rows int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND topic_key=$2 AND deleted_at IS NULL`, tenant, topic).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("post-retry topic rows=%d err=%v, want 1", rows, err)
	}
}
