package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	_ "modernc.org/sqlite"
)

func TestBundlePrivacyHandoff_RedactedPersistenceAndReceipt(t *testing.T) {
	db, path := openHandoffDB(t)
	targetID := seedHandoffTarget(t, db)
	exec := newTestHandoffExecutor(db, nil)

	canaryT, canaryC, canaryR := "can_t_99", "can_c_88", "can_r_77"
	c := handoffCanonical("T <private>"+canaryT+"</private>", "C <private>"+canaryC+"</private>")
	c.Relation = &domain.HandoffRelationInput{
		Target:    domain.ObservationRef{LocalID: &targetID},
		Type:      domain.RelationReferences,
		Reasoning: "R <private>" + canaryR + "</private>",
	}

	res := executeHandoff(t, exec, "scope:priv", "k1", c)
	if res.Status != domain.WriteStatusCreated || res.Ref.LocalID == nil {
		t.Fatalf("unexpected res: %+v", res)
	}
	assertPrivacyHandoffDB(t, db, *res.Ref.LocalID, targetID, "scope:priv", "k1", canaryT, canaryC, canaryR)
	closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
		assertPrivacyHandoffDB(t, restarted, *res.Ref.LocalID, targetID, "scope:priv", "k1", canaryT, canaryC, canaryR)
	})
}

func TestBundlePrivacyHandoff_ReplayIdempotence(t *testing.T) {
	db, path := openHandoffDB(t)
	targetID := seedHandoffTarget(t, db)
	exec := newTestHandoffExecutor(db, nil)

	canary := "can_rep_55"
	raw := handoffCanonical("Replay <private>"+canary+"</private>", "C <private>"+canary+"</private>")
	raw.Relation = &domain.HandoffRelationInput{
		Target:    domain.ObservationRef{LocalID: &targetID},
		Type:      domain.RelationReferences,
		Reasoning: "R <private>" + canary + "</private>",
	}

	res1 := executeHandoff(t, exec, "s:rep", "k1", raw)
	_, rawHash := canonicalPayload(t, raw)
	res2, err := exec.ExecuteHandoff(context.Background(), "s:rep", "k1", raw, rawHash)
	if err != nil || res2.Status != domain.WriteStatusReplayed || *res2.Ref.LocalID != *res1.Ref.LocalID {
		t.Fatalf("raw replay failed: %+v, %v", res2, err)
	}

	redacted := handoffCanonical("Replay [REDACTED]", "C [REDACTED]")
	redacted.Relation = &domain.HandoffRelationInput{
		Target:    domain.ObservationRef{LocalID: &targetID},
		Type:      domain.RelationReferences,
		Reasoning: "R [REDACTED]",
	}
	_, redHash := canonicalPayload(t, redacted)
	res3, err := exec.ExecuteHandoff(context.Background(), "s:rep", "k1", redacted, redHash)
	if err != nil || res3.Status != domain.WriteStatusReplayed || *res3.Ref.LocalID != *res1.Ref.LocalID {
		t.Fatalf("redacted replay failed: %+v, %v", res3, err)
	}
	if countTableRows(t, db, "observations") != 2 || countTableRows(t, db, "edges") != 1 || countTableRows(t, db, "handoff_receipts") != 1 {
		t.Fatal("unexpected row counts after replay")
	}

	closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
		restartedExec := newTestHandoffExecutor(restarted, nil)
		res4, err := restartedExec.ExecuteHandoff(context.Background(), "s:rep", "k1", raw, rawHash)
		if err != nil || res4.Status != domain.WriteStatusReplayed || *res4.Ref.LocalID != *res1.Ref.LocalID {
			t.Fatalf("post-reopen raw replay failed: %+v, %v", res4, err)
		}
		res5, err := restartedExec.ExecuteHandoff(context.Background(), "s:rep", "k1", redacted, redHash)
		if err != nil || res5.Status != domain.WriteStatusReplayed || *res5.Ref.LocalID != *res1.Ref.LocalID {
			t.Fatalf("post-reopen redacted replay failed: %+v, %v", res5, err)
		}
		if countTableRows(t, restarted, "observations") != 2 || countTableRows(t, restarted, "edges") != 1 || countTableRows(t, restarted, "handoff_receipts") != 1 {
			t.Fatal("unexpected row counts after post-reopen replay")
		}
		assertPrivacyHandoffDB(t, restarted, *res1.Ref.LocalID, targetID, "s:rep", "k1", canary)
	})
}

func TestBundlePrivacyHandoff_ConflictZeroEffectsAndReopen(t *testing.T) {
	db, path := openHandoffDB(t)
	targetID := seedHandoffTarget(t, db)
	exec := newTestHandoffExecutor(db, nil)

	const canary = "can_conf_11"
	original := handoffCanonical("Orig <private>"+canary+"</private>", "Content <private>"+canary+"</private>")
	original.Relation = &domain.HandoffRelationInput{
		Target:    domain.ObservationRef{LocalID: &targetID},
		Type:      domain.RelationReferences,
		Reasoning: "Reason <private>" + canary + "</private>",
	}
	resOrig := executeHandoff(t, exec, "s:conf", "k-conf", original)
	_, origHash := canonicalPayload(t, original)
	beforeSnapshot := handoffSnapshot(t, db, "s:conf", "k-conf")

	// 1. Conflict on different content
	diffContent := handoffCanonical("Orig [REDACTED]", "Different Content <private>other_canary</private>")
	diffContent.Relation = original.Relation
	_, diffContentHash := canonicalPayload(t, diffContent)
	if _, err := exec.ExecuteHandoff(context.Background(), "s:conf", "k-conf", diffContent, diffContentHash); !errors.Is(err, domain.ErrHandoffConflict) {
		t.Fatalf("expected conflict on different content, got %v", err)
	}
	if after := handoffSnapshot(t, db, "s:conf", "k-conf"); !bytes.Equal(beforeSnapshot, after) {
		t.Fatal("conflict mutated durable snapshot")
	}

	// 2. Conflict on different relation reasoning
	diffReasoning := original
	diffReasoning.Relation = &domain.HandoffRelationInput{
		Target:    domain.ObservationRef{LocalID: &targetID},
		Type:      domain.RelationReferences,
		Reasoning: "Other reason <private>other_canary</private>",
	}
	_, diffReasonHash := canonicalPayload(t, diffReasoning)
	if _, err := exec.ExecuteHandoff(context.Background(), "s:conf", "k-conf", diffReasoning, diffReasonHash); !errors.Is(err, domain.ErrHandoffConflict) {
		t.Fatalf("expected conflict on different reasoning, got %v", err)
	}
	if after := handoffSnapshot(t, db, "s:conf", "k-conf"); !bytes.Equal(beforeSnapshot, after) {
		t.Fatal("conflict mutated durable snapshot")
	}

	// 3. Discordant caller hash rejected as validation error
	corruptHash := origHash
	corruptHash[0] ^= 0xff
	if _, err := exec.ExecuteHandoff(context.Background(), "s:conf", "k-conf", original, corruptHash); !errors.Is(err, domain.ErrHandoffValidation) {
		t.Fatalf("expected validation error on hash mismatch, got %v", err)
	}
	if after := handoffSnapshot(t, db, "s:conf", "k-conf"); !bytes.Equal(beforeSnapshot, after) {
		t.Fatal("validation failure mutated durable snapshot")
	}

	// 4. Reopen and verify conflict rejection and snapshot integrity persist
	closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
		restartedExec := newTestHandoffExecutor(restarted, nil)
		if after := handoffSnapshot(t, restarted, "s:conf", "k-conf"); !bytes.Equal(beforeSnapshot, after) {
			t.Fatal("post-reopen snapshot mismatch")
		}
		if _, err := restartedExec.ExecuteHandoff(context.Background(), "s:conf", "k-conf", diffContent, diffContentHash); !errors.Is(err, domain.ErrHandoffConflict) {
			t.Fatalf("expected post-reopen conflict, got %v", err)
		}
		if after := handoffSnapshot(t, restarted, "s:conf", "k-conf"); !bytes.Equal(beforeSnapshot, after) {
			t.Fatal("post-reopen conflict mutated durable snapshot")
		}
		// Confirm original still replays cleanly
		resReplay, err := restartedExec.ExecuteHandoff(context.Background(), "s:conf", "k-conf", original, origHash)
		if err != nil || resReplay.Status != domain.WriteStatusReplayed || *resReplay.Ref.LocalID != *resOrig.Ref.LocalID {
			t.Fatalf("replay failed post-reopen: %+v, %v", resReplay, err)
		}
		if after := handoffSnapshot(t, restarted, "s:conf", "k-conf"); !bytes.Equal(beforeSnapshot, after) {
			t.Fatal("replay mutated durable snapshot")
		}
	})
}

func TestBundlePrivacyHandoff_MalformedAndRequiredEmptyZeroEffects(t *testing.T) {
	const canary = "can_zero_44"
	targetID := int64(1)
	cases := []struct {
		name string
		c    domain.CanonicalHandoff
		code privacy.ErrorCode
	}{
		{"unclosed content", handoffCanonical("T", "C <private>"+canary), privacy.ErrCodeInvalidMarker},
		{"unclosed reasoning", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: "references", Reasoning: "<private>" + canary}
			return c
		}(), privacy.ErrCodeInvalidMarker},
		{"pure priv content", handoffCanonical("T", "<private>"+canary+"</private>"), privacy.ErrCodeRequiredEmpty},
		{"pure priv title", handoffCanonical("<private>"+canary+"</private>", "C"), privacy.ErrCodeRequiredEmpty},
		{"priv marker in tag", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.Observation.Tags = []string{"<private>" + canary + "</private>"}
			return c
		}(), privacy.ErrCodeInvalidMarker},
		{"priv marker in capability tuple value", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.CapabilityTuple = []byte(`{"token":"<private>` + canary + `</private>"}`)
			return c
		}(), privacy.ErrCodeInvalidMarker},
		{"priv marker in nested capability tuple", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.CapabilityTuple = []byte(`{"nested":{"token":"<private>` + canary + `</private>"}}`)
			return c
		}(), privacy.ErrCodeInvalidMarker},
		{"priv marker in capability tuple key", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.CapabilityTuple = []byte(`{"<private>` + canary + `</private>":"val"}`)
			return c
		}(), privacy.ErrCodeInvalidMarker},
		{"priv marker in capability tuple array", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.CapabilityTuple = []byte(`["item","<private>` + canary + `</private>"]`)
			return c
		}(), privacy.ErrCodeInvalidMarker},
		{"priv marker in decoded nested JSON", func() domain.CanonicalHandoff {
			c := handoffCanonical("T", "C")
			c.CapabilityTuple = []byte(`{"raw":"{\"secret\":\"<private>` + canary + `</private>\"}"}`)
			return c
		}(), privacy.ErrCodeInvalidMarker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := openHandoffDB(t)
			defer func() { _ = db.Close() }()
			seedHandoffTarget(t, db)
			exec := newTestHandoffExecutor(db, nil)
			_, err := exec.ExecuteHandoff(context.Background(), "s:err", "k-err", tc.c, [32]byte{})
			var privErr *privacy.Error
			if !errors.As(err, &privErr) || privErr.Code != tc.code || strings.Contains(err.Error(), canary) {
				t.Fatalf("expected error code %v without leak, got err=%v", tc.code, err)
			}
			if countTableRows(t, db, "observations") != 1 || countTableRows(t, db, "edges") != 0 || countTableRows(t, db, "handoff_receipts") != 0 {
				t.Fatal("dirty tables on rejection")
			}
		})
	}
}

func TestBundlePrivacyHandoff_UnitOfWorkRollbackPreservesPrivacy(t *testing.T) {
	for _, stage := range []sqliteHandoffStage{handoffAfterSave, handoffAfterEdge, handoffBeforeCommit} {
		stage := stage
		t.Run("failpoint_"+string(stage), func(t *testing.T) {
			db, path := openHandoffDB(t)
			targetID := seedHandoffTarget(t, db)
			canary := "can_rb_" + strings.ReplaceAll(string(stage), "-", "_")
			c := handoffCanonical("Rollback <private>"+canary+"</private>", "Content <private>"+canary+"</private>")
			c.Relation = &domain.HandoffRelationInput{
				Target:    domain.ObservationRef{LocalID: &targetID},
				Type:      domain.RelationReferences,
				Reasoning: "Reason <private>" + canary + "</private>",
			}
			exec := newTestHandoffExecutor(db, func(got sqliteHandoffStage) error {
				if got == stage {
					return errHandoffFailpoint
				}
				return nil
			})
			_, hash := canonicalPayload(t, c)
			if _, err := exec.ExecuteHandoff(context.Background(), "s:rb", "k-rb-"+string(stage), c, hash); !errors.Is(err, errHandoffFailpoint) {
				t.Fatalf("expected failpoint error, got %v", err)
			}
			assertRollbackClean(t, db, canary)
			closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
				assertRollbackClean(t, restarted, canary)
			})
		})
	}

	t.Run("relation_target_not_found_rolls_back", func(t *testing.T) {
		db, path := openHandoffDB(t)
		seedHandoffTarget(t, db)
		nonExistentID := int64(999999)
		canary := "can_rb_fk_404"
		c := handoffCanonical("Rollback FK <private>"+canary+"</private>", "Content <private>"+canary+"</private>")
		c.Relation = &domain.HandoffRelationInput{
			Target:    domain.ObservationRef{LocalID: &nonExistentID},
			Type:      domain.RelationReferences,
			Reasoning: "Reason <private>" + canary + "</private>",
		}
		exec := newTestHandoffExecutor(db, nil)
		_, hash := canonicalPayload(t, c)
		_, err := exec.ExecuteHandoff(context.Background(), "s:rb", "k-rb-fk", c, hash)
		if err == nil {
			t.Fatal("expected error on non-existent relation target")
		}
		var notFound *domain.NotFoundError
		if !errors.As(err, &notFound) && !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Fatalf("expected NotFoundError or FOREIGN KEY, got %v", err)
		}
		assertRollbackClean(t, db, canary)
		closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
			assertRollbackClean(t, restarted, canary)
		})
	})
}

func TestBundlePrivacyHandoff_CapabilityTupleReplayAndReceiptProtection(t *testing.T) {
	db, path := openHandoffDB(t)
	seedHandoffTarget(t, db)
	exec := newTestHandoffExecutor(db, nil)

	validCanonical := handoffCanonical("Valid Title", "Valid Content")
	validCanonical.CapabilityTuple = []byte(`{"agent":"worker","scopes":["read"]}`)
	res1 := executeHandoff(t, exec, "scope:tuple-prot", "k-prot", validCanonical)
	if res1.Status != domain.WriteStatusCreated || res1.Ref.LocalID == nil {
		t.Fatalf("expected created write result, got %+v", res1)
	}

	obsCount := countTableRows(t, db, "observations")
	receiptCount := countTableRows(t, db, "handoff_receipts")
	if obsCount != 2 || receiptCount != 1 {
		t.Fatalf("expected 2 observations and 1 receipt, got obs=%d receipts=%d", obsCount, receiptCount)
	}

	const canary = "canary_bundle_replay_protect_99"
	badCanonical := validCanonical
	badCanonical.CapabilityTuple = []byte(`{"agent":"worker","token":"<private>` + canary + `</private>"}`)
	_, badHash := canonicalPayload(t, validCanonical)
	_, err := exec.ExecuteHandoff(context.Background(), "scope:tuple-prot", "k-prot", badCanonical, badHash)
	var privErr *privacy.Error
	if !errors.As(err, &privErr) || privErr.Code != privacy.ErrCodeInvalidMarker {
		t.Fatalf("expected ErrCodeInvalidMarker, got %v", err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error leaked canary: %v", err)
	}

	if countTableRows(t, db, "observations") != obsCount || countTableRows(t, db, "handoff_receipts") != receiptCount {
		t.Fatal("database rows changed after rejected replay attempt")
	}

	_, validHash := canonicalPayload(t, validCanonical)
	resReplay, err := exec.ExecuteHandoff(context.Background(), "scope:tuple-prot", "k-prot", validCanonical, validHash)
	if err != nil || resReplay.Status != domain.WriteStatusReplayed || *resReplay.Ref.LocalID != *res1.Ref.LocalID {
		t.Fatalf("valid replay failed: res=%+v err=%v", resReplay, err)
	}
	if countTableRows(t, db, "observations") != obsCount || countTableRows(t, db, "handoff_receipts") != receiptCount {
		t.Fatal("rows changed after valid replay")
	}

	// Verify durability across close and reopen
	closeAndReopenHandoffDB(t, db, path, func(restarted *sql.DB) {
		restartedExec := newTestHandoffExecutor(restarted, nil)
		resReplayAfter, err := restartedExec.ExecuteHandoff(context.Background(), "scope:tuple-prot", "k-prot", validCanonical, validHash)
		if err != nil || resReplayAfter.Status != domain.WriteStatusReplayed || *resReplayAfter.Ref.LocalID != *res1.Ref.LocalID {
			t.Fatalf("post-reopen valid replay failed: res=%+v err=%v", resReplayAfter, err)
		}
		if countTableRows(t, restarted, "observations") != obsCount || countTableRows(t, restarted, "handoff_receipts") != receiptCount {
			t.Fatal("rows changed after post-reopen replay")
		}
	})
}

func assertPrivacyHandoffDB(t *testing.T, db *sql.DB, obsID, targetID int64, scope, key string, canaries ...string) {
	t.Helper()
	var title, content, reasoning, payload string
	if err := db.QueryRow("SELECT title, content FROM observations WHERE id = ?", obsID).Scan(&title, &content); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT reasoning FROM edges WHERE from_obs_id = ? AND to_obs_id = ?", obsID, targetID).Scan(&reasoning); err != nil {
		t.Fatal(err)
	}
	var gotHash []byte
	var state, initialStatus, committedAt string
	var receiptObsID int64
	if err := db.QueryRow("SELECT canonical_payload, payload_hash, state, initial_status, observation_id, committed_at FROM handoff_receipts WHERE scope = ? AND key = ?", scope, key).Scan(&payload, &gotHash, &state, &initialStatus, &receiptObsID, &committedAt); err != nil {
		t.Fatal(err)
	}
	if state != "committed" || initialStatus != "created" || receiptObsID != obsID || committedAt == "" {
		t.Fatalf("receipt metadata invalid: state=%q status=%q obsID=%d committedAt=%q", state, initialStatus, receiptObsID, committedAt)
	}
	expectedHash := sha256.Sum256([]byte(payload))
	if !bytes.Equal(gotHash, expectedHash[:]) {
		t.Fatalf("receipt payload_hash does not match sha256 of canonical_payload")
	}
	for _, text := range []string{title, content, reasoning, payload} {
		if !strings.Contains(text, "[REDACTED]") {
			t.Fatalf("missing [REDACTED]: %q", text)
		}
		for _, c := range canaries {
			if strings.Contains(text, c) {
				t.Fatalf("leaked canary %q in %q", c, text)
			}
		}
	}
}

func assertRollbackClean(t *testing.T, db *sql.DB, canary string) {
	t.Helper()
	if countTableRows(t, db, "observations") != 1 || countTableRows(t, db, "edges") != 0 || countTableRows(t, db, "handoff_receipts") != 0 {
		t.Fatalf("rollback leaked rows into tables: obs=%d edges=%d receipts=%d",
			countTableRows(t, db, "observations"), countTableRows(t, db, "edges"), countTableRows(t, db, "handoff_receipts"))
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM observations WHERE title LIKE ? OR content LIKE ?", "%"+canary+"%", "%"+canary+"%").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("canary leaked into observations table")
	}
}
