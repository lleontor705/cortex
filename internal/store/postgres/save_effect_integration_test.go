//go:build postgres_integration

package postgres

import (
	"context"
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

// Concurrent first upserts of one topic key must serialize through the
// advisory transaction lock: exactly one row is created and every later save
// updates it, with no 23505 leaking from observations_topic_key_active_uq.
func TestPostgresObservationSaveConcurrentTopicUpsert(t *testing.T) {
	h := newPostgresHarness(t)
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
