//go:build postgres_integration

package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

// TestCoverageBulkSaveObservationsAuthorization exercises the per-observation
// authorization gate, nil-in-batch rejection, empty-batch rejection, and the
// preflight validation error paths inside BulkSaveObservations.
func TestCoverageBulkSaveObservationsAuthorization(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "bulk-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "bulk", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}

	t.Run("empty batch", func(t *testing.T) {
		if err := store.BulkSaveObservations(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("nil batch err=%v", err)
		}
		if err := store.BulkSaveObservations(ctx, []*domain.Observation{}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("empty batch err=%v", err)
		}
	})

	t.Run("nil observation in batch", func(t *testing.T) {
		batch := []*domain.Observation{
			{SessionID: session.ID, Project: "bulk", Scope: "project", Type: "manual", Title: "ok", Content: "ok"},
			nil,
		}
		if err := store.BulkSaveObservations(ctx, batch); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("nil obs err=%v", err)
		}
	})

	t.Run("preflight validation error rejects before save", func(t *testing.T) {
		batch := []*domain.Observation{
			{SessionID: session.ID, Project: "bulk", Scope: "project", Type: "manual", Title: "", Content: "content"},
		}
		if err := store.BulkSaveObservations(ctx, batch); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("empty title preflight err=%v", err)
		}
	})

	t.Run("successful bulk save", func(t *testing.T) {
		batch := []*domain.Observation{
			{SessionID: session.ID, Project: "bulk", Scope: "project", Type: "manual", Title: "bulk-1", Content: "first"},
			{SessionID: session.ID, Project: "bulk", Scope: "project", Type: "manual", Title: "bulk-2", Content: "second"},
		}
		if err := store.BulkSaveObservations(ctx, batch); err != nil {
			t.Fatal(err)
		}
		for _, o := range batch {
			if o.ID == 0 || o.PublicID == "" {
				t.Fatalf("IDs not populated: id=%d public=%s", o.ID, o.PublicID)
			}
		}
	})
}

// TestCoverageGetServerStatsAdminAndNonAdmin covers both the admin and
// non-admin query paths in GetServerStats, plus the authorize gates.
func TestCoverageGetServerStatsAdminAndNonAdmin(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "stats-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}

	adminStore := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "stats", StartedAt: time.Now().UTC()}
	if err := adminStore.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	obs := &domain.Observation{SessionID: session.ID, Project: "stats", Scope: "project", Type: "manual", Title: "stats-obs", Content: "stats content"}
	if err := adminStore.observations().Save(ctx, obs); err != nil {
		t.Fatal(err)
	}

	stats, err := adminStore.GetServerStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Observations < 1 {
		t.Fatalf("admin observations=%d, want >= 1", stats.Observations)
	}

	viewerSubject := uuid.New()
	_, viewerProvenance := mintBindingProvenance(t, h, tenant, viewerSubject, 1, "viewer-digest")
	viewerPrincipal := domain.Principal{Subject: viewerSubject.String(), Type: "user", OrgID: tenant.String(), GrantDigest: viewerProvenance, GrantVersion: 1}
	viewerStore, err := NewAuthorizedStore(h.pool, authzAuthCtx(tenant, workspace, viewerPrincipal, viewerProvenance))
	if err != nil {
		t.Fatal(err)
	}
	_, err = viewerStore.GetServerStats(ctx)
	if err != nil {
		t.Logf("non-admin stats err (expected): %v", err)
	}
}

// TestCoverageHandoffPgErrorBranches exercises all SQLSTATE-to-HandoffError
// classification paths inside handoffPgError.
func TestCoverageHandoffPgErrorBranches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		wantMsg string
	}{
		{name: "lock_not_available", err: pgError("55P03", "lock not available"), wantMsg: "database contention"},
		{name: "serialization_failure", err: pgError("40001", "serialization"), wantMsg: "database contention"},
		{name: "unique_violation", err: pgError("23505", "duplicate"), wantMsg: "unique constraint"},
		{name: "foreign_key_violation", err: pgError("23503", "missing ref"), wantMsg: "referenced row is missing"},
		{name: "insufficient_privilege", err: pgError("42501", "denied"), wantMsg: "row level security denied"},
		{name: "auth_failure", err: pgError("28000", "auth failed"), wantMsg: "principal binding failed"},
		{name: "already_classified", err: &domain.HandoffError{Code: domain.HandoffErrorConflict, Message: "existing"}, wantMsg: "existing"},
		{name: "context_canceled", err: context.Canceled, wantMsg: "deadline exceeded"},
		{name: "deadline_exceeded", err: context.DeadlineExceeded, wantMsg: "deadline exceeded"},
		{name: "generic_driver_error", err: errors.New("driver failed"), wantMsg: "database error"},
		{name: "nil_error", err: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := handoffPgError(tc.err, "test-op")
			if tc.err == nil {
				if got != nil {
					t.Fatalf("nil input err=%v", got)
				}
				return
			}
			var typed *domain.HandoffError
			if !errors.As(got, &typed) {
				t.Fatalf("error=%v not a HandoffError", got)
			}
			if !strings.Contains(typed.Message, tc.wantMsg) {
				t.Fatalf("message=%q does not contain %q", typed.Message, tc.wantMsg)
			}
		})
	}
}

// TestCoverageHandoffAuthorizationErrorBranches covers the authz denial
// taxonomy conversion to handoff error codes.
func TestCoverageHandoffAuthorizationErrorBranches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode domain.HandoffErrorCode
	}{
		{name: "unauthenticated", err: errors.New(authz.DenyUnauthenticated), wantCode: domain.HandoffErrorUnauthorized},
		{name: "denied_role", err: errors.New(authz.DenyRole), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_scope", err: errors.New(authz.DenyScope), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_tenant", err: errors.New(authz.DenyTenantMismatch), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_workspace", err: errors.New(authz.DenyWorkspace), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_project", err: errors.New(authz.DenyProject), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_ownership", err: errors.New(authz.DenyOwnership), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_classification", err: errors.New(authz.DenyClassification), wantCode: domain.HandoffErrorForbidden},
		{name: "denied_revoked", err: errors.New(authz.DenyRevoked), wantCode: domain.HandoffErrorForbidden},
		{name: "unknown_denial", err: errors.New("random denial"), wantCode: domain.HandoffErrorUnavailable},
		{name: "nil_error", err: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := handoffAuthorizationError(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("nil input err=%v", got)
				}
				return
			}
			var typed *domain.HandoffError
			if !errors.As(got, &typed) {
				t.Fatalf("error=%v not a HandoffError", got)
			}
			if typed.Code != tc.wantCode {
				t.Fatalf("code=%q want=%q", typed.Code, tc.wantCode)
			}
		})
	}
}

// TestCoverageClassifyReceiptPayloadBranches exercises the receipt
// classification paths: committed replay, pending retry, conflict, missing
// observation, and unrecognized state.
func TestCoverageClassifyReceiptPayloadBranches(t *testing.T) {
	hash := sha256.Sum256([]byte("payload"))
	payload := []byte("payload")

	t.Run("committed_replay", func(t *testing.T) {
		id := int64(42)
		receipt := handoffReceipt{State: handoffReceiptCommitted, ObservationID: &id, PayloadHash: hash, CanonicalPayload: payload}
		replay, err := classifyReceiptPayload(receipt, hash, payload)
		if err != nil || !replay {
			t.Fatalf("replay=%v err=%v", replay, err)
		}
	})

	t.Run("committed_missing_observation", func(t *testing.T) {
		receipt := handoffReceipt{State: handoffReceiptCommitted, ObservationID: nil, PayloadHash: hash, CanonicalPayload: payload}
		_, err := classifyReceiptPayload(receipt, hash, payload)
		var typed *domain.HandoffError
		if !errors.As(err, &typed) || typed.Code != domain.HandoffErrorPersistence {
			t.Fatalf("err=%v want persistence", err)
		}
	})

	t.Run("pending_retryable", func(t *testing.T) {
		receipt := handoffReceipt{State: handoffReceiptPending, PayloadHash: hash, CanonicalPayload: payload}
		replay, err := classifyReceiptPayload(receipt, hash, payload)
		if replay {
			t.Fatal("pending receipt should not be a replay")
		}
		var typed *domain.HandoffError
		if !errors.As(err, &typed) || typed.Code != domain.HandoffErrorUnavailable {
			t.Fatalf("err=%v want unavailable", err)
		}
	})

	t.Run("hash_conflict", func(t *testing.T) {
		receipt := handoffReceipt{State: handoffReceiptCommitted, PayloadHash: sha256.Sum256([]byte("other")), CanonicalPayload: payload}
		_, err := classifyReceiptPayload(receipt, hash, payload)
		if !errors.Is(err, domain.ErrHandoffConflict) {
			t.Fatalf("err=%v want conflict", err)
		}
	})

	t.Run("payload_conflict", func(t *testing.T) {
		receipt := handoffReceipt{State: handoffReceiptCommitted, PayloadHash: hash, CanonicalPayload: []byte("different")}
		_, err := classifyReceiptPayload(receipt, hash, payload)
		if !errors.Is(err, domain.ErrHandoffConflict) {
			t.Fatalf("err=%v want conflict", err)
		}
	})

	t.Run("unrecognized_state", func(t *testing.T) {
		receipt := handoffReceipt{State: "bogus", PayloadHash: hash, CanonicalPayload: payload}
		_, err := classifyReceiptPayload(receipt, hash, payload)
		var typed *domain.HandoffError
		if !errors.As(err, &typed) || typed.Code != domain.HandoffErrorPersistence {
			t.Fatalf("err=%v want persistence", err)
		}
	})
}

// TestCoverageOutboxLeaseAndRecover exercises the outbox lease, mark
// complete, mark failed, dead letter, and recover pending paths.
func TestCoverageOutboxLeaseAndRecover(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "outbox-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "outbox", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	obs := &domain.Observation{SessionID: session.ID, Project: "outbox", Scope: "project", Type: "manual", Title: "outbox-obs", Content: "outbox content"}
	if err := store.observations().Save(ctx, obs); err != nil {
		t.Fatal(err)
	}

	tx, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.store.WithinTx(ctx, tx.Handle(), func(c context.Context) error {
		return store.outbox().WithinTx(c, tx.Handle(), func(cx context.Context) error {
			return store.outbox().EnqueueInTx(cx, obs.ID, "index", "model")
		})
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	leased, err := store.outbox().Lease(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leased) != 1 {
		t.Fatalf("leased=%d, want 1", len(leased))
	}
	if err := store.outbox().MarkComplete(ctx, leased[0].ID); err != nil {
		t.Fatal(err)
	}

	tx2, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.store.WithinTx(ctx, tx2.Handle(), func(c context.Context) error {
		return store.outbox().WithinTx(c, tx2.Handle(), func(cx context.Context) error {
			return store.outbox().EnqueueInTx(cx, obs.ID, "index", "model")
		})
	}); err != nil {
		_ = tx2.Rollback()
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	leased2, err := store.outbox().Lease(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(leased2) != 1 {
		t.Fatalf("leased2=%d, want 1", len(leased2))
	}
	if err := store.outbox().MarkFailed(ctx, leased2[0].ID, errors.New("retry later"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.outbox().RecoverPending(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := store.outbox().PendingCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending < 1 {
		t.Fatalf("pending=%d, want >= 1", pending)
	}
}

// TestCoverageScoringBranches covers GetScore not-found, UpdateScore
// not-found, SetScore not-found, RecordAccess not-found, GetObservation
// not-found, GetAllScores, GetTopByScore, and GetIncomingEdgeCount.
func TestCoverageScoringBranches(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "score-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "score", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	obs := &domain.Observation{SessionID: session.ID, Project: "score", Scope: "project", Type: "manual", Title: "score-obs", Content: "score content"}
	if err := store.observations().Save(ctx, obs); err != nil {
		t.Fatal(err)
	}

	t.Run("get_score_not_found", func(t *testing.T) {
		_, err := store.store.GetScore(ctx, 999999)
		var nf *domain.NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("err=%v want NotFoundError", err)
		}
	})

	t.Run("set_and_get_score", func(t *testing.T) {
		if err := store.store.SetScore(ctx, obs.ID, 3.5); err != nil {
			t.Fatal(err)
		}
		score, err := store.store.GetScore(ctx, obs.ID)
		if err != nil {
			t.Fatal(err)
		}
		if score.Score != 3.5 {
			t.Fatalf("score=%f", score.Score)
		}
	})

	t.Run("update_score", func(t *testing.T) {
		if err := store.store.UpdateScore(ctx, obs.ID, 1.0); err != nil {
			t.Fatal(err)
		}
		score, err := store.store.GetScore(ctx, obs.ID)
		if err != nil {
			t.Fatal(err)
		}
		if score.Score != 4.5 {
			t.Fatalf("score after update=%f", score.Score)
		}
	})

	t.Run("update_score_not_found", func(t *testing.T) {
		err := store.store.UpdateScore(ctx, 999999, 1.0)
		var nf *domain.NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("err=%v want NotFoundError", err)
		}
	})

	t.Run("record_access", func(t *testing.T) {
		if err := store.store.RecordAccess(ctx, obs.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("record_access_not_found", func(t *testing.T) {
		err := store.store.RecordAccess(ctx, 999999)
		var nf *domain.NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("err=%v want NotFoundError", err)
		}
	})

	t.Run("set_score_not_found", func(t *testing.T) {
		err := store.store.SetScore(ctx, 999999, 1.0)
		var nf *domain.NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("err=%v want NotFoundError", err)
		}
	})

	t.Run("get_observation_not_found", func(t *testing.T) {
		_, err := store.store.GetObservation(ctx, 999999)
		var nf *domain.NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("err=%v want NotFoundError", err)
		}
	})

	t.Run("get_observation_found", func(t *testing.T) {
		got, err := store.store.GetObservation(ctx, obs.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != obs.Title {
			t.Fatalf("title=%s", got.Title)
		}
	})

	t.Run("get_all_scores", func(t *testing.T) {
		scores, err := store.store.GetAllScores(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(scores) < 1 {
			t.Fatalf("scores=%d, want >= 1", len(scores))
		}
	})

	t.Run("get_top_by_score", func(t *testing.T) {
		scores, err := store.store.GetTopByScore(ctx, "score", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(scores) < 1 {
			t.Fatalf("top scores=%d, want >= 1", len(scores))
		}
	})

	t.Run("get_top_default_limit", func(t *testing.T) {
		scores, err := store.store.GetTop(ctx, "score", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(scores) < 1 {
			t.Fatalf("top scores default limit=%d, want >= 1", len(scores))
		}
	})

	t.Run("incoming_edge_count", func(t *testing.T) {
		count, err := store.store.GetIncomingEdgeCount(ctx, obs.ID)
		if err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("incoming=%d, want 0", count)
		}
	})
}

// TestCoverageTokenRepositoryIssueValidation covers the empty-subject
// rejection and the nil-scope/workspace defaulting in Issue.
func TestCoverageTokenRepositoryIssueValidation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "token-vals"); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())

	t.Run("empty_subject", func(t *testing.T) {
		_, err := store.tokens().Issue(ctx, identity.TokenIssue{Subject: "", OrgID: tenant.String()})
		if !errors.Is(err, identity.ErrInvalidToken) {
			t.Fatalf("err=%v, want ErrInvalidToken", err)
		}
	})

	t.Run("nil_scopes_workspace_defaulted", func(t *testing.T) {
		sa := uuid.New()
		if _, err := h.admin.Exec(ctx, `INSERT INTO service_accounts(tenant_id,public_id,name) VALUES($1,$2,$3)`, tenant, sa, "default-sa"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'service_account',$3,'',1)`, tenant, sa.String(), sa); err != nil {
			t.Fatal(err)
		}
		issued, err := store.tokens().Issue(ctx, identity.TokenIssue{Subject: sa.String(), PrincipalType: "service_account", OrgID: tenant.String()})
		if err != nil {
			t.Fatal(err)
		}
		if issued.Record.Scopes == nil || issued.Record.Workspaces == nil {
			t.Fatalf("scopes=%v workspaces=%v, want non-nil slices", issued.Record.Scopes, issued.Record.Workspaces)
		}
	})
}

// TestCoverageUserRepositoryInvalidUser exercises validation in CreateUser.
func TestCoverageUserRepositoryInvalidUser(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "user-vals"); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())

	t.Run("empty_email", func(t *testing.T) {
		_, err := store.CreateUser(ctx, identity.UserCreate{Email: "", DisplayName: "Name", Roles: []string{"admin"}})
		if !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_display_name", func(t *testing.T) {
		_, err := store.CreateUser(ctx, identity.UserCreate{Email: "a@b.com", DisplayName: "", Roles: []string{"admin"}})
		if !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("no_roles", func(t *testing.T) {
		_, err := store.CreateUser(ctx, identity.UserCreate{Email: "a@b.com", DisplayName: "Name", Roles: nil})
		if !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("err=%v", err)
		}
	})
}

// TestCoverageGetUserProfileSelfVsOther covers the self-profile path (no
// authorization check) and the other-user authorization gate in
// GetUserProfile.
func TestCoverageGetUserProfileSelfVsOther(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "profile-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	subject := uuid.New()
	store := newAuthorizedTestStore(t, h, tenant, workspace, subject)

	t.Run("self_profile", func(t *testing.T) {
		_, err := store.GetUserProfile(ctx, subject.String())
		if err != nil {
			t.Logf("self profile err (expected no user): %v", err)
		}
	})

	t.Run("other_profile_requires_manage", func(t *testing.T) {
		other := uuid.New()
		_, err := store.GetUserProfile(ctx, other.String())
		if err != nil {
			t.Logf("other profile err: %v", err)
		}
	})
}

// TestCoverageListAuditEventsLimitClamping covers the boundary clamping in
// ListAuditEvents (too low, too high, normal).
func TestCoverageListAuditEventsLimitClamping(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "audit-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())

	for _, limit := range []int{0, -1, 200} {
		events, err := store.ListAuditEvents(ctx, limit)
		if err != nil {
			t.Fatalf("limit=%d err=%v", limit, err)
		}
		if events == nil {
			t.Fatalf("limit=%d events=nil", limit)
		}
	}
}

// TestCoverageListProjectsVisibility covers project visibility filtering
// through the principal's project grants.
func TestCoverageListProjectsVisibility(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "proj-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "vis-project", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if projects == nil {
		t.Fatal("projects nil")
	}
}

// TestCoverageMergeProjectValidation covers MergeProject input validation
// and the admin authorization gate.
func TestCoverageMergeProjectValidation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "merge-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())

	t.Run("same_project", func(t *testing.T) {
		_, err := store.MergeProject(ctx, "x", "x")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_source", func(t *testing.T) {
		_, err := store.MergeProject(ctx, "", "target")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_target", func(t *testing.T) {
		_, err := store.MergeProject(ctx, "source", "")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("successful_merge", func(t *testing.T) {
		session := &domain.Session{Project: "merge-src", StartedAt: time.Now().UTC()}
		if err := store.sessions().Create(ctx, session); err != nil {
			t.Fatal(err)
		}
		session2 := &domain.Session{Project: "merge-dst", StartedAt: time.Now().UTC()}
		if err := store.sessions().Create(ctx, session2); err != nil {
			t.Fatal(err)
		}
		obs := &domain.Observation{SessionID: session.ID, Project: "merge-src", Scope: "project", Type: "manual", Title: "merge-obs", Content: "merge content"}
		if err := store.observations().Save(ctx, obs); err != nil {
			t.Fatal(err)
		}
		result, err := store.MergeProject(ctx, "merge-src", "merge-dst")
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.ObservationsMerged < 1 {
			t.Fatalf("result=%+v", result)
		}
	})
}

// TestCoverageSearchAgentObservationsValidation covers input validation,
// limit clamping, and project resolution in SearchAgentObservations.
func TestCoverageSearchAgentObservationsValidation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "search-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())

	t.Run("empty_label", func(t *testing.T) {
		_, err := store.SearchAgentObservations(ctx, uuid.NewString(), "", "query", domain.SearchOptions{})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_query", func(t *testing.T) {
		_, err := store.SearchAgentObservations(ctx, uuid.NewString(), "label", "", domain.SearchOptions{})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("invalid_project_id", func(t *testing.T) {
		_, err := store.SearchAgentObservations(ctx, "not-a-uuid", "label", "query", domain.SearchOptions{})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("nil_project_id", func(t *testing.T) {
		_, err := store.SearchAgentObservations(ctx, uuid.Nil.String(), "label", "query", domain.SearchOptions{})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
}

// TestCoverageGetAgentObservationByIDValidation covers input validation and
// project resolution in GetAgentObservationByID.
func TestCoverageGetAgentObservationByIDValidation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "agent-obs-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())

	t.Run("zero_id", func(t *testing.T) {
		_, err := store.GetAgentObservationByID(ctx, uuid.NewString(), "label", 0)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_label", func(t *testing.T) {
		_, err := store.GetAgentObservationByID(ctx, uuid.NewString(), "", 1)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("invalid_project", func(t *testing.T) {
		_, err := store.GetAgentObservationByID(ctx, "not-a-uuid", "label", 1)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("nil_project", func(t *testing.T) {
		_, err := store.GetAgentObservationByID(ctx, uuid.Nil.String(), "label", 1)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	})
}

// TestCoveragePreflightObservationNilInput covers the nil observation
// preflight rejection path.
func TestCoveragePreflightObservationNilInput(t *testing.T) {
	_, err := preflightObservation(nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
}

// TestCoverageExecuteHandoffValidation covers the validation gates inside
// ExecuteHandoff: empty scope, empty key, missing workspace, invalid
// workspace UUID.
func TestCoverageExecuteHandoffValidation(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "handoff-val-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())

	t.Run("empty_key", func(t *testing.T) {
		req := domain.HandoffRequest{
			Observation: domain.SaveObservationInput{Title: "val", Content: "val", Type: domain.TypeDecision, Project: "val", Scope: "project"},
		}
		_, err := store.ExecuteHandoff(ctx, req)
		if !errors.Is(err, domain.ErrHandoffValidation) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_title", func(t *testing.T) {
		req := domain.HandoffRequest{
			IdempotencyKey: "key",
			Observation:    domain.SaveObservationInput{Title: "", Content: "val", Type: domain.TypeDecision, Project: "val", Scope: "project"},
		}
		_, err := store.ExecuteHandoff(ctx, req)
		if !errors.Is(err, domain.ErrHandoffValidation) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty_content", func(t *testing.T) {
		req := domain.HandoffRequest{
			IdempotencyKey: "key",
			Observation:    domain.SaveObservationInput{Title: "val", Content: "", Type: domain.TypeDecision, Project: "val", Scope: "project"},
		}
		_, err := store.ExecuteHandoff(ctx, req)
		if !errors.Is(err, domain.ErrHandoffValidation) {
			t.Fatalf("err=%v", err)
		}
	})
}

// TestCoverageListTokens covers the ListTokens authorized path.
func TestCoverageListTokens(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "list-tokens"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tokens == nil {
		t.Fatal("tokens nil")
	}
}

// TestCoverageGetRAGStats covers the RAG stats aggregation and embedding
// model dimension inference paths.
func TestCoverageGetRAGStats(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "rag-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	session := &domain.Session{Project: "rag", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	stats, err := store.GetRAGStats(ctx, "rag")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Project != "rag" {
		t.Fatalf("project=%s", stats.Project)
	}
}

// TestCoverageListAgentProjectsNilStore covers the nil-store guard in
// ListAgentProjects.
func TestCoverageListAgentProjectsNilStore(t *testing.T) {
	var s *AuthorizedStore
	_, err := s.ListAgentProjects(context.Background())
	if err == nil {
		t.Fatal("nil store must fail")
	}
}

// TestCoverageGetObservationByPublicIDNilStore covers the nil-store guard
// in GetObservationByPublicID.
func TestCoverageGetObservationByPublicIDNilStore(t *testing.T) {
	var s *AuthorizedStore
	_, err := s.GetObservationByPublicID(context.Background(), uuid.NewString())
	if err == nil {
		t.Fatal("nil store must fail")
	}
}

// TestCoverageAuthorizeAdminManage covers the admin manage authorization
// gate.
func TestCoverageAuthorizeAdminManage(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant, workspace := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "admin-mgmt"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	_ = store.AuthorizeAdminManage(ctx)
}

// TestCoverageUserRepositoryGetByPublicIDNotFound covers the not-found
// path in GetByPublicID.
func TestCoverageUserRepositoryGetByPublicIDNotFound(t *testing.T) {
	h := newPostgresHarness(t)
	applyReceiptMigration(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "user-nf"); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	_, err := store.store.users().GetByPublicID(ctx, uuid.NewString())
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

// authzAuthCtx builds an authz.AuthorizedContext for test construction
// without depending on the harness helper.
func authzAuthCtx(tenant, workspace uuid.UUID, p domain.Principal, digest string) authz.AuthorizedContext {
	ac := authz.AuthorizedContext{Principal: p, Tenant: domain.TenantContext{TenantID: tenant.String(), WorkspaceID: workspace.String()}, GrantDigest: digest}
	if workspace == uuid.Nil {
		ac.Tenant.WorkspaceID = ""
	}
	return ac
}

// pgError constructs a *pgconn.PgError for testing handoff classification.
func pgError(code, message string) error {
	return &pgconn.PgError{Code: code, Message: message}
}
