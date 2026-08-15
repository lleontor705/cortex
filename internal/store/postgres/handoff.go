package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/internal/domain"
)

// Durable handoff receipts (REM-HANDOFF-001/002). Every primitive below is
// transaction scoped: it must run inside store.transaction or WithinTx so the
// tenant RLS binding from cortex_bind_principal is active and the workspace
// bigint from bindWorkspace is resolved. Receipts are keyed by (tenant,
// workspace, scope, key) — two workspaces of one tenant hold independent
// idempotent namespaces — and comparison always uses the SHA-256 and the full
// canonical bytes so a hash collision can never silently replay different
// content.

type handoffReceiptState string

const (
	handoffReceiptPending   handoffReceiptState = "pending"
	handoffReceiptCommitted handoffReceiptState = "committed"
)

var errHandoffTxRequired = errors.New("postgres handoff: transaction context is required")

// handoffFailpoints, when replaced, is consulted at named execution seams so
// tests can prove rollback atomicity without a database. Inside the claimed
// path the seams are, in order: "after-save" (observation materialized, no
// relation yet), "after-edge" (optional relation written, receipt still
// pending), and "before-commit" (receipt finalized, transaction about to
// commit). The default never fails.
var handoffFailpoints = func(seam string) error { return nil }

type handoffReceipt struct {
	Scope            domain.HandoffScope
	Key              string
	PayloadHash      [32]byte
	CanonicalPayload []byte
	State            handoffReceiptState
	ObservationID    *int64
	InitialStatus    *domain.WriteStatus
	CreatedAt        time.Time
	CommittedAt      *time.Time
}

const handoffReceiptColumns = `scope, key, payload_hash, canonical_payload, state, observation_id, initial_status, committed_at, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanHandoffReceipt(row rowScanner) (handoffReceipt, error) {
	var (
		receipt       handoffReceipt
		payloadHash   []byte
		state         string
		initialStatus *string
	)
	if err := row.Scan(&receipt.Scope, &receipt.Key, &payloadHash, &receipt.CanonicalPayload, &state, &receipt.ObservationID, &initialStatus, &receipt.CommittedAt, &receipt.CreatedAt); err != nil {
		return handoffReceipt{}, err
	}
	if len(payloadHash) != sha256.Size {
		return handoffReceipt{}, fmt.Errorf("postgres handoff: receipt %q has invalid payload hash length", receipt.Key)
	}
	copy(receipt.PayloadHash[:], payloadHash)
	receipt.State = handoffReceiptState(state)
	if initialStatus != nil {
		status := domain.WriteStatus(*initialStatus)
		receipt.InitialStatus = &status
	}
	return receipt, nil
}

// classifyReceiptPayload decides replay, conflict, or retryable-unavailable
// from the durable receipt. Hash and full canonical bytes must both match; a
// pending receipt with identical bytes means an earlier attempt is still
// resolving, which is retryable, never replayable.
func classifyReceiptPayload(receipt handoffReceipt, hash [32]byte, payload []byte) (bool, error) {
	if receipt.PayloadHash != hash || !bytes.Equal(receipt.CanonicalPayload, payload) {
		return false, &domain.HandoffError{Code: domain.HandoffErrorConflict, Message: domain.ErrHandoffConflict.Message, Operation: "claim", Context: "receipt payload differs"}
	}
	switch receipt.State {
	case handoffReceiptCommitted:
		if receipt.ObservationID == nil {
			return false, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "claim", Context: "committed receipt lacks observation reference"}
		}
		return true, nil
	case handoffReceiptPending:
		return false, &domain.HandoffError{Code: domain.HandoffErrorUnavailable, Message: domain.ErrHandoffUnavailable.Message, Retryable: true, Operation: "claim", Context: "receipt claim is still pending"}
	default:
		return false, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "claim", Context: "receipt state is not recognizable"}
	}
}

func handoffTx(ctx context.Context) (pgx.Tx, error) {
	tx, ok := txFromContext(ctx)
	if !ok || tx == nil {
		return nil, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: errHandoffTxRequired.Error(), Operation: "receipt", Context: "transaction context is required"}
	}
	return tx, nil
}

// setHandoffLockTimeout bounds lock waits for the rest of the transaction.
func setHandoffLockTimeout(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, boundedLockTimeout); err != nil {
		return &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "receipt", Context: "lock timeout could not be installed"}
	}
	return nil
}

// workspaceFromTx returns the transaction-resolved workspace bigint or a
// fail-closed error: receipts without a resolved workspace binding would
// silently fall back to a tenant-wide namespace, which migration 105 removed.
func workspaceFromTx(ctx context.Context, operation string) (int64, error) {
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return 0, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: operation, Context: "workspace binding is not resolved in this transaction"}
	}
	return ws, nil
}

// claimHandoffReceipt inserts a pending receipt for (current tenant, bound
// workspace, scope, key) or observes the durable one. claimed is true only
// for the transaction that materialized the pending row; otherwise the
// receipt describes the committed outcome to replay. Conflicting bytes never
// mutate anything.
func (s *Store) claimHandoffReceipt(ctx context.Context, scope domain.HandoffScope, key string, hash [32]byte, payload []byte) (handoffReceipt, bool, error) {
	if scope == "" || key == "" {
		return handoffReceipt{}, false, domain.ErrHandoffValidation
	}
	tx, err := handoffTx(ctx)
	if err != nil {
		return handoffReceipt{}, false, err
	}
	ws, err := workspaceFromTx(ctx, "claim")
	if err != nil {
		return handoffReceipt{}, false, err
	}
	if err := setHandoffLockTimeout(ctx, tx); err != nil {
		return handoffReceipt{}, false, err
	}
	// ON CONFLICT DO NOTHING serializes concurrent first claims: exactly one
	// transaction inserts and becomes the materializer; the others fall
	// through to the durable row once the winner commits or aborts.
	rows, err := tx.Query(ctx, `
		INSERT INTO handoff_receipts (tenant_id, workspace_id, scope, key, payload_hash, canonical_payload, state)
		VALUES (public.cortex_current_tenant(), $1, $2, $3, $4, $5, 'pending')
		ON CONFLICT (tenant_id, workspace_id, scope, key) DO NOTHING
		RETURNING `+handoffReceiptColumns, ws, scope, key, hash[:], payload)
	if err != nil {
		return handoffReceipt{}, false, handoffPgError(err, "claim")
	}
	defer rows.Close()
	if rows.Next() {
		receipt, err := scanHandoffReceipt(rows)
		if err != nil {
			return handoffReceipt{}, false, handoffPgError(err, "claim")
		}
		return receipt, true, rows.Err()
	}
	if err := rows.Err(); err != nil {
		return handoffReceipt{}, false, handoffPgError(err, "claim")
	}
	receipt, err := scanHandoffReceipt(tx.QueryRow(ctx, `
		SELECT `+handoffReceiptColumns+` FROM handoff_receipts
		WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND scope=$2 AND key=$3
		FOR UPDATE`, ws, scope, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return handoffReceipt{}, false, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "claim", Context: "receipt disappeared after conflict"}
	}
	if err != nil {
		return handoffReceipt{}, false, handoffPgError(err, "claim")
	}
	// The durable receipt exists, so this attempt never claimed it: identical
	// committed bytes replay through the receipt (claimed=false), and any
	// difference already failed closed inside classifyReceiptPayload. The
	// legacy `replay` classification result is intentionally not returned as
	// `claimed`; conflating the two made valid replays re-materialize effects.
	if _, err := classifyReceiptPayload(receipt, hash, payload); err != nil {
		return handoffReceipt{}, false, err
	}
	return receipt, false, nil
}

// readHandoffReceipt returns the durable receipt visible to the current
// tenant inside the bound workspace. RLS hides other tenants' receipts and
// the workspace predicate hides sibling workspaces; both surface as
// not-found.
//
//nolint:unused // exercised by the postgres_integration suite, which is the only caller namespace
func (s *Store) readHandoffReceipt(ctx context.Context, scope domain.HandoffScope, key string) (handoffReceipt, error) {
	if scope == "" || key == "" {
		return handoffReceipt{}, domain.ErrHandoffValidation
	}
	tx, err := handoffTx(ctx)
	if err != nil {
		return handoffReceipt{}, err
	}
	ws, err := workspaceFromTx(ctx, "read")
	if err != nil {
		return handoffReceipt{}, err
	}
	receipt, err := scanHandoffReceipt(tx.QueryRow(ctx, `
		SELECT `+handoffReceiptColumns+` FROM handoff_receipts
		WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND scope=$2 AND key=$3`, ws, scope, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return handoffReceipt{}, notFound("handoff receipt", key)
	}
	if err != nil {
		return handoffReceipt{}, handoffPgError(err, "read")
	}
	return receipt, nil
}

// finalizeHandoffReceipt commits a claimed receipt together with the durable
// effects of the same transaction. Finalizing a receipt owned by another
// tenant, workspace, or scope matches zero rows and fails closed.
func (s *Store) finalizeHandoffReceipt(ctx context.Context, scope domain.HandoffScope, key string, observationID int64, status domain.WriteStatus) (handoffReceipt, error) {
	if scope == "" || key == "" || observationID <= 0 || !validHandoffWriteStatus(status) {
		return handoffReceipt{}, domain.ErrHandoffValidation
	}
	tx, err := handoffTx(ctx)
	if err != nil {
		return handoffReceipt{}, err
	}
	ws, err := workspaceFromTx(ctx, "finalize")
	if err != nil {
		return handoffReceipt{}, err
	}
	if err := setHandoffLockTimeout(ctx, tx); err != nil {
		return handoffReceipt{}, err
	}
	receipt, err := scanHandoffReceipt(tx.QueryRow(ctx, `
		UPDATE handoff_receipts
		SET state='committed', observation_id=$4, initial_status=$5::text, committed_at=now()
		WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND scope=$2 AND key=$3 AND state='pending'
		RETURNING `+handoffReceiptColumns, ws, scope, key, observationID, string(status)))
	if errors.Is(err, pgx.ErrNoRows) {
		return handoffReceipt{}, &domain.HandoffError{Code: domain.HandoffErrorConflict, Message: domain.ErrHandoffConflict.Message, Operation: "finalize", Context: "receipt is not claimable"}
	}
	if err != nil {
		return handoffReceipt{}, handoffPgError(err, "finalize")
	}
	return receipt, nil
}

func validHandoffWriteStatus(status domain.WriteStatus) bool {
	return status == domain.WriteStatusCreated || status == domain.WriteStatusReplayed || status == domain.WriteStatusUpdated
}

// handoffRelationAuthorizer revalidates the optional relation target INSIDE
// the executor transaction: it must resolve the target with a row lock in the
// transaction-bound workspace and authorize the locked attributes before the
// executor proceeds. The type is unexported so no raw transaction or
// repository escapes the package boundary (REM-AUTH-001, review R7 fix 1).
type handoffRelationAuthorizer func(ctx context.Context, tx pgx.Tx, relation *domain.HandoffRelationInput) error

// ExecuteHandoff implements domain.HandoffExecutor for the PostgreSQL
// namespace. Claim/read, observation materialization, optional relation, and
// receipt finalize all share one authorized transaction: any failure rolls
// every effect back, and a retry with the same key replays the receipt.
// Relation targets are not revalidated here; use executeHandoff with an
// in-transaction authorizer (the AuthorizedStore path) for that.
func (s *Store) ExecuteHandoff(ctx context.Context, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, hash [32]byte) (domain.ObservationWriteResult, error) {
	return s.executeHandoff(ctx, scope, key, canonical, hash, nil)
}

// executeHandoff runs the executor with an optional in-transaction relation
// authorization callback. When the callback is set and the canonical request
// carries a relation, it runs FIRST inside the transaction — target row
// locked, attributes revalidated — so the receipt claim, the observation
// materialization, and the relation write all happen under the very
// attributes that were authorized: no resolve-authorize-mutate TOCTOU window.
func (s *Store) executeHandoff(ctx context.Context, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, hash [32]byte, authorizeRelation handoffRelationAuthorizer) (domain.ObservationWriteResult, error) {
	if scope == "" || key == "" || strings.TrimSpace(canonical.Observation.Title) == "" || strings.TrimSpace(canonical.Observation.Content) == "" {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}
	// Handoff effects (sessions, observations, dedup, relation targets) are
	// workspace scoped; a store without a validated workspace binding cannot
	// isolate them, so it must fail closed before any database access.
	if s.tenant == nil || s.tenant.WorkspaceID == "" {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}
	if _, err := uuid.Parse(s.tenant.WorkspaceID); err != nil {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}
	if sha256.Sum256(payload) != hash {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}
	var result domain.ObservationWriteResult
	txErr := s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if authorizeRelation != nil && canonical.Relation != nil {
			if err := authorizeRelation(ctx, tx, canonical.Relation); err != nil {
				return err
			}
		}
		receipt, claimed, err := s.claimHandoffReceipt(ctx, scope, key, hash, payload)
		if err != nil {
			return err
		}
		if !claimed {
			publicID, err := handoffObservationPublicID(ctx, tx, *receipt.ObservationID)
			if err != nil {
				return err
			}
			ref, err := domain.NewPublicObservationRef(publicID)
			if err != nil {
				return &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "replay", Context: "receipt observation reference is invalid"}
			}
			result = domain.ObservationWriteResult{Ref: ref, Status: domain.WriteStatusReplayed}
			return nil
		}
		effect, err := s.materializeHandoffObservation(ctx, tx, canonical.Observation)
		if err != nil {
			return err
		}
		if err := handoffFailpoints("after-save"); err != nil {
			return err
		}
		if canonical.Relation != nil {
			if err := s.createHandoffRelationInTx(ctx, tx, effect.Observation.ID, canonical.Relation); err != nil {
				return err
			}
		}
		if err := handoffFailpoints("after-edge"); err != nil {
			return err
		}
		if _, err := s.finalizeHandoffReceipt(ctx, scope, key, effect.Observation.ID, effect.Status); err != nil {
			return err
		}
		publicID, err := uuid.Parse(effect.Observation.PublicID)
		if err != nil {
			return &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "materialize", Context: "materialized observation has no public id"}
		}
		result = domain.ObservationWriteResult{Ref: domain.ObservationRef{PublicID: &publicID}, Status: effect.Status}
		if err := handoffFailpoints("before-commit"); err != nil {
			return err
		}
		return nil
	})
	if txErr != nil {
		return domain.ObservationWriteResult{}, handoffPgError(txErr, "execute")
	}
	return result, nil
}

// materializeHandoffObservation resolves a session for the canonical input and
// saves the observation with its durable effect inside the handoff transaction.
func (s *Store) materializeHandoffObservation(ctx context.Context, tx pgx.Tx, input domain.SaveObservationInput) (domain.SaveEffect, error) {
	observation := &domain.Observation{
		Title:      input.Title,
		Content:    input.Content,
		Type:       input.Type,
		Project:    input.Project,
		Scope:      input.Scope,
		SessionID:  input.SessionID,
		TopicKey:   input.TopicKey,
		Confidence: input.Confidence,
		Source:     input.Source,
		Tags:       append([]string(nil), input.Tags...),
	}
	sessionID, err := s.resolveHandoffSession(ctx, tx, observation.Project, input.SessionID)
	if err != nil {
		return domain.SaveEffect{}, err
	}
	observation.SessionID = sessionID
	effect, err := s.observations().saveWithEffectInTx(ctx, tx, observation)
	if errors.Is(err, errDedupSkipped) {
		// The canonical content already exists durably; the handoff receipt
		// still finalizes in this transaction with the replayed status.
		return effect, nil
	}
	return effect, err
}

// resolveHandoffSession pins the NOT NULL observations.session_id: an explicit
// session must exist inside the bound workspace; otherwise the newest project
// session of the same workspace is reused, and a missing one is created in the
// same transaction. Sessions of another workspace in the same tenant are
// invisible and fail closed as validation errors.
func (s *Store) resolveHandoffSession(ctx context.Context, tx pgx.Tx, project, sessionPublicID string) (string, error) {
	validation := &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "session", Context: "handoff session could not be resolved"}
	if sessionPublicID != "" {
		query := `SELECT public_id::text FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid`
		args := []any{sessionPublicID}
		filter, args := s.workspaceSessionPredicate(ctx, args)
		var publicID string
		err := tx.QueryRow(ctx, query+filter, args...).Scan(&publicID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", validation
		}
		if err != nil {
			return "", handoffPgError(err, "session")
		}
		return publicID, nil
	}
	query := `SELECT public_id::text FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1`
	args := []any{project}
	filter, args := s.workspaceSessionPredicate(ctx, args)
	var publicID string
	err := tx.QueryRow(ctx, query+filter+` ORDER BY started_at DESC, id DESC LIMIT 1`, args...).Scan(&publicID)
	if err == nil {
		return publicID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", handoffPgError(err, "session")
	}
	if s.tenant == nil || s.tenant.WorkspaceID == "" {
		return "", validation
	}
	// The session is created directly in the transaction-resolved workspace
	// when available; ambient transactions fall back to the UUID subquery.
	insertWorkspace := `(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid)`
	insertArgs := []any{s.tenant.WorkspaceID, project, actorFromContext(ctx)}
	if ws, ok := workspaceFromContext(ctx); ok {
		insertWorkspace = `$1::bigint`
		insertArgs[0] = ws
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO sessions (tenant_id, workspace_id, project_key, started_at, created_by, updated_by)
		VALUES (public.cortex_current_tenant(), `+insertWorkspace+`, $2, now(), $3, $3)
		RETURNING public_id::text`, insertArgs...).Scan(&publicID); err != nil {
		return "", handoffPgError(err, "session")
	}
	return publicID, nil
}

var handoffRelationTypes = map[string]bool{
	domain.RelationReferences:  true,
	domain.RelationRelatesTo:   true,
	domain.RelationFollows:     true,
	domain.RelationContradicts: true,
	domain.RelationSupersedes:  true,
}

// createHandoffRelationInTx writes the optional relation edge inside the same
// transaction. Server handoffs address targets only in the public namespace
// and only inside the bound workspace, isolated by the explicit
// observations.workspace_id column (migration 105); no relation means no
// implicit edge is created.
func (s *Store) createHandoffRelationInTx(ctx context.Context, tx pgx.Tx, fromID int64, relation *domain.HandoffRelationInput) error {
	if relation == nil || relation.Target.PublicID == nil || relation.Target.LocalID != nil {
		return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "relation", Context: "relation target must use the public namespace"}
	}
	if !handoffRelationTypes[relation.Type] {
		return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "relation", Context: "relation type is not permitted"}
	}
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "relation", Context: "workspace binding is not resolved in this transaction"}
	}
	var targetID int64
	err := tx.QueryRow(ctx, `SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid AND deleted_at IS NULL AND workspace_id=$2`, relation.Target.PublicID.String(), ws).Scan(&targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "relation", Context: "relation target was not found"}
	}
	if err != nil {
		return handoffPgError(err, "relation")
	}
	if targetID == fromID {
		return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "relation", Context: "relation target equals the handoff observation"}
	}
	weight, confidence := relation.Weight, relation.Confidence
	if weight == 0 {
		weight = 1
	}
	if confidence == 0 {
		confidence = 1
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO edges (tenant_id, from_observation_id, to_observation_id, relation_type, weight, confidence, source, reasoning, assertion_kind, assertion_status, created_by, updated_by)
		VALUES (public.cortex_current_tenant(), $1, $2, $3, $4, $5, 'handoff', $6, 'asserted', 'accepted', $7, $7)`,
		fromID, targetID, relation.Type, weight, confidence, relation.Reasoning, actorFromContext(ctx)); err != nil {
		return handoffPgError(err, "relation")
	}
	return nil
}

// handoffObservationPublicID resolves the replayed observation reference
// inside the bound workspace, isolated by the explicit
// observations.workspace_id column (migration 105): an observation that left
// the workspace (or the tenant) is reported as a retryable persistence
// failure, never replayed.
func handoffObservationPublicID(ctx context.Context, tx pgx.Tx, observationID int64) (uuid.UUID, error) {
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return uuid.Nil, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "replay", Context: "workspace binding is not resolved in this transaction"}
	}
	var publicID string
	err := tx.QueryRow(ctx, `SELECT public_id::text FROM observations WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL AND workspace_id=$2`, observationID, ws).Scan(&publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "replay", Context: "receipt observation no longer exists"}
	}
	if err != nil {
		return uuid.Nil, handoffPgError(err, "replay")
	}
	id, err := uuid.Parse(publicID)
	if err != nil {
		return uuid.Nil, &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "replay", Context: "receipt observation reference is invalid"}
	}
	return id, nil
}

// handoffPgError converts driver failures into the stable handoff taxonomy.
// Already-classified handoff errors pass through unchanged.
func handoffPgError(err error, operation string) error {
	if err == nil {
		return nil
	}
	var typed *domain.HandoffError
	if errors.As(err, &typed) {
		return typed
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "55P03", "40P01", "40001":
			return &domain.HandoffError{Code: domain.HandoffErrorUnavailable, Message: domain.ErrHandoffUnavailable.Message, Retryable: true, Operation: operation, Context: "database contention"}
		case "23505":
			return &domain.HandoffError{Code: domain.HandoffErrorConflict, Message: domain.ErrHandoffConflict.Message, Operation: operation, Context: "unique constraint"}
		case "23503":
			return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: operation, Context: "referenced row is missing"}
		case "28000", "28P01":
			return &domain.HandoffError{Code: domain.HandoffErrorUnauthorized, Message: domain.ErrHandoffUnauthorized.Message, Operation: operation, Context: "principal binding failed"}
		case "42501":
			return &domain.HandoffError{Code: domain.HandoffErrorForbidden, Message: domain.ErrHandoffForbidden.Message, Operation: operation, Context: "row level security denied access"}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &domain.HandoffError{Code: domain.HandoffErrorTimeout, Message: domain.ErrHandoffTimeout.Message, Retryable: true, Operation: operation, Context: "deadline exceeded"}
	}
	return &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: operation, Context: "database error"}
}
