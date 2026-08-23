package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestStore_SaveWithEffect_DecidesInTransaction(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	createTestSession(t, db, "save-effect", "project")

	created := &domain.Observation{SessionID: "save-effect", Project: "project", Type: domain.TypeManual, Title: "created", Content: "content"}
	effect, err := store.SaveWithEffect(ctx, created)
	if err != nil || effect.Status != domain.WriteStatusCreated || effect.Observation != created || created.ID == 0 {
		t.Fatalf("created effect=%+v obs=%+v err=%v", effect, created, err)
	}

	replay := &domain.Observation{SessionID: "save-effect", Project: "project", Type: domain.TypeManual, Title: "created", Content: "content"}
	effect, err = store.SaveWithEffect(ctx, replay)
	if !domain.IsClass(err, domain.ClassDedupSkipped) || effect.Status != domain.WriteStatusReplayed || replay.ID != created.ID {
		t.Fatalf("replayed effect=%+v obs=%+v err=%v", effect, replay, err)
	}

	topicCreated := &domain.Observation{SessionID: "save-effect", Project: "project", Type: domain.TypeDecision, Title: "topic", Content: "v1", TopicKey: "architecture/save"}
	effect, err = store.SaveWithEffect(ctx, topicCreated)
	if err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("topic create effect=%+v err=%v", effect, err)
	}
	topicUpdated := &domain.Observation{SessionID: "save-effect", Project: "project", Type: domain.TypeDecision, Title: "topic", Content: "v2", TopicKey: "architecture/save"}
	effect, err = store.SaveWithEffect(ctx, topicUpdated)
	if err != nil || effect.Status != domain.WriteStatusUpdated || topicUpdated.ID != topicCreated.ID {
		t.Fatalf("updated effect=%+v obs=%+v err=%v", effect, topicUpdated, err)
	}
}

func TestStore_SaveWithEffect_RollsBackAndLegacySavePreservesDedup(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	bad := &domain.Observation{SessionID: "missing", Project: "project", Type: domain.TypeManual, Title: "rollback", Content: "content"}
	if effect, err := store.SaveWithEffect(ctx, bad); err == nil || effect.Observation != nil {
		t.Fatalf("failed effect=%+v err=%v", effect, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM observations WHERE title='rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback rows=%d err=%v", count, err)
	}

	createTestSession(t, db, "legacy", "project")
	first := &domain.Observation{SessionID: "legacy", Project: "project", Type: domain.TypeManual, Title: "legacy", Content: "same"}
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &domain.Observation{SessionID: "legacy", Project: "project", Type: domain.TypeManual, Title: "legacy", Content: "same"}
	if err := store.Save(ctx, second); !domain.IsClass(err, domain.ClassDedupSkipped) || second.ID != first.ID {
		t.Fatalf("legacy err=%v first=%d second=%d", err, first.ID, second.ID)
	}
}

func TestStore_SaveWithEffect_UsesEnlistedTransaction(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	createTestSession(t, db, "enlisted", "project")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	obs := &domain.Observation{SessionID: "enlisted", Project: "project", Type: domain.TypeDecision, Title: "enlisted", Content: "content"}
	err = store.WithinTx(ctx, tx, func(txctx context.Context) error {
		effect, err := store.SaveWithEffect(txctx, obs)
		if err != nil || effect.Status != domain.WriteStatusCreated {
			t.Fatalf("effect=%+v err=%v", effect, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByID(ctx, obs.ID); !domain.IsNotFoundError(err) {
		t.Fatalf("enlisted write survived rollback: %v", err)
	}
}

// TestStore_SaveWithEffect_MatchesLegacySaveContentLimit pins REM-SAVE-001
// reconciliation: SaveWithEffect exposes the SAME 64 KiB interactive envelope
// as legacy Save — no ambiguous dual ceiling on the public save surface.
func TestStore_SaveWithEffect_MatchesLegacySaveContentLimit(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	createTestSession(t, db, "limits", "project")

	over := &domain.Observation{SessionID: "limits", Project: "project", Type: domain.TypeManual, Title: "over legacy", Content: strings.Repeat("a", 64*1024+1)}
	if err := store.Save(ctx, over); err == nil || !strings.Contains(err.Error(), "content exceeds 65536") {
		t.Fatalf("legacy Save err=%v, want 64KiB validation", err)
	}
	effect, err := store.SaveWithEffect(ctx, over)
	if err == nil || !strings.Contains(err.Error(), "content exceeds 65536") || effect.Observation != nil {
		t.Fatalf("SaveWithEffect err=%v effect=%+v, want 64KiB validation with zero effect", err, effect)
	}

	within := &domain.Observation{SessionID: "limits", Project: "project", Type: domain.TypeManual, Title: "within legacy", Content: strings.Repeat("a", 64*1024)}
	if effect, err = store.SaveWithEffect(ctx, within); err != nil || effect.Status != domain.WriteStatusCreated {
		t.Fatalf("within-limit effect=%+v err=%v", effect, err)
	}
}

// TestStore_SaveHandoffWithEffect_HandoffEnvelope pins the specialized handoff
// primitive: the domain 1 MiB bound replaces the interactive 64 KiB ceiling
// because the canonical payload was already size-bounded at the domain layer.
func TestStore_SaveHandoffWithEffect_HandoffEnvelope(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	createTestSession(t, db, "limits", "project")

	over64k := &domain.Observation{SessionID: "limits", Project: "project", Type: domain.TypeDecision, Title: "handoff envelope", Content: strings.Repeat("h", 200_000)}
	effect, err := store.SaveHandoffWithEffect(ctx, over64k)
	if err != nil || effect.Status != domain.WriteStatusCreated || effect.Observation == nil || effect.Observation.ID == 0 {
		t.Fatalf("handoff-envelope effect=%+v err=%v", effect, err)
	}

	over1mib := &domain.Observation{SessionID: "limits", Project: "project", Type: domain.TypeDecision, Title: "over handoff", Content: strings.Repeat("x", domain.MaxHandoffPayloadSize+1)}
	effect, err = store.SaveHandoffWithEffect(ctx, over1mib)
	if err == nil || !strings.Contains(err.Error(), "content exceeds 1048576") || effect.Observation != nil {
		t.Fatalf("handoff-envelope over-limit err=%v effect=%+v, want 1MiB validation with zero effect", err, effect)
	}
}
