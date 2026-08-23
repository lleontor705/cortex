// SQLite handoff executor: exactly-once materialization of canonical handoffs
// on the local backend (REM-HANDOFF-001, REM-HANDOFF-002, design RD3/RD4).
package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// sqliteHandoffStage names the observable seams of the SQLite handoff path.
// They exist exclusively as failpoint injection points for tests (RD4); the
// production executor never sets a failpoint.
type sqliteHandoffStage string

const (
	handoffAfterSave    sqliteHandoffStage = "after-save"
	handoffAfterEdge    sqliteHandoffStage = "after-edge"
	handoffBeforeCommit sqliteHandoffStage = "before-commit"
)

// SQLiteHandoffExecutor materializes a canonical handoff exactly once per
// (scope, key) on the SQLite backend. One UnitOfWork transaction covers the
// receipt claim/read, SaveWithEffect, the optional relation edge, and the
// committed receipt row. Every participant enlists on the SAME *sql.Tx
// stashed by SQLiteUnitOfWork.Do, so any failure — at any failpoint — rolls
// back all effects atomically: no observation, edge, or receipt row survives,
// and a pending receipt is never left behind.
//
// Replay and conflict (RD3): a committed receipt whose canonical bytes (and
// hash) equal the incoming payload replays without re-materialization,
// returning the original observation ref with WriteStatusReplayed. The same
// (scope, key) with different canonical bytes — even under an equal SHA-256 —
// is a conflict that mutates nothing.
//
// Concurrency/restart (RD4): SQLite serializes writers, so racing callers
// lose their snapshot at the first write; the UnitOfWork's busy retry reruns
// the whole handoff, the loser then observes the committed receipt and
// replays. An unknown commit is resolved by retrying with the same key.
type SQLiteHandoffExecutor struct {
	stores *Stores

	// failpoint is an unexported test seam (RD4). When non-nil it is invoked
	// at each stage; a returned error aborts and rolls back the handoff.
	failpoint func(sqliteHandoffStage) error
}

// Ensure SQLiteHandoffExecutor satisfies the domain executor port.
var _ domain.HandoffExecutor = (*SQLiteHandoffExecutor)(nil)

// NewSQLiteHandoffExecutor builds an executor over a fully wired Stores
// bundle: Observations, Graph, and UnitOfWork must all share the same
// *sql.DB.
func NewSQLiteHandoffExecutor(stores *Stores) *SQLiteHandoffExecutor {
	return &SQLiteHandoffExecutor{stores: stores}
}

// ExecuteHandoff runs the single-transaction handoff for (scope, key).
func (e *SQLiteHandoffExecutor) ExecuteHandoff(ctx context.Context, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, hash [32]byte) (domain.ObservationWriteResult, error) {
	if e == nil || e.stores == nil || e.stores.Observations == nil || e.stores.Graph == nil || e.stores.UnitOfWork == nil {
		return domain.ObservationWriteResult{}, domain.ErrHandoffUnavailable
	}
	if scope == "" || key == "" {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}
	// Re-derive the canonical form through the domain normalizer so the
	// persisted payload bytes are exactly CanonicalizeHandoff's deterministic
	// encoding (e.g. capability-tuple object keys sorted). The caller's hash is
	// stored as given; replay/conflict is decided by full canonical bytes.
	canonical, payload, _, err := domain.CanonicalizeHandoff(domain.HandoffRequest{
		IdempotencyKey:  key,
		Observation:     canonical.Observation,
		Relation:        canonical.Relation,
		CapabilityTuple: canonical.CapabilityTuple,
	})
	if err != nil {
		return domain.ObservationWriteResult{}, err
	}
	// Integrity gate: the caller hash must equal the SHA-256 of the recomputed
	// canonical payload. A discordant hash is rejected BEFORE any receipt or
	// effect — the hash is an integrity claim, not an advisory hint.
	if recomputed := sha256.Sum256(payload); !bytes.Equal(recomputed[:], hash[:]) {
		return domain.ObservationWriteResult{}, domain.ErrHandoffValidation
	}

	var result domain.ObservationWriteResult
	err = e.stores.UnitOfWork.Do(ctx, nil, []domain.TxParticipant{e.stores.Observations, e.stores.Graph}, func(txCtx context.Context) error {
		r, err := e.executeInTx(txCtx, scope, key, canonical, hash[:], payload)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return domain.ObservationWriteResult{}, err
	}
	return result, nil
}

// executeInTx runs the entire handoff inside the shared *sql.Tx stashed by
// SQLiteUnitOfWork.Do (retrieved via TxHandle). It never commits or rolls
// back — the UnitOfWork owns the transaction lifecycle.
func (e *SQLiteHandoffExecutor) executeInTx(ctx context.Context, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, hash, payload []byte) (domain.ObservationWriteResult, error) {
	tx, _ := TxHandle(ctx).(*sql.Tx)
	if tx == nil {
		return domain.ObservationWriteResult{}, fmt.Errorf("sqlite handoff: no shared transaction in context")
	}

	// 1. Claim/read: an existing receipt decides replay vs conflict.
	var gotHash, gotPayload []byte
	var state string
	var observationID sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT payload_hash, canonical_payload, state, observation_id FROM handoff_receipts WHERE scope=? AND key=?`,
		string(scope), key,
	).Scan(&gotHash, &gotPayload, &state, &observationID)
	switch {
	case err == nil:
		if state == "committed" && observationID.Valid && bytes.Equal(gotPayload, payload) && bytes.Equal(gotHash, hash) {
			ref, rerr := domain.NewLocalObservationRef(observationID.Int64)
			if rerr != nil {
				return domain.ObservationWriteResult{}, rerr
			}
			return domain.ObservationWriteResult{Ref: ref, Status: domain.WriteStatusReplayed}, nil
		}
		// Same (scope, key) with different canonical bytes — or a receipt not
		// in committed form — is a conflict; nothing is mutated.
		return domain.ObservationWriteResult{}, domain.ErrHandoffConflict
	case errors.Is(err, sql.ErrNoRows):
		// First materialization under this key; continue.
	default:
		return domain.ObservationWriteResult{}, fmt.Errorf("sqlite handoff: read receipt: %w", err)
	}

	// 2. Materialize the observation via SaveWithEffect on the SAME tx.
	effect, err := e.saveObservation(ctx, tx, canonical)
	if err != nil {
		return domain.ObservationWriteResult{}, err
	}
	if e.failpoint != nil {
		if ferr := e.failpoint(handoffAfterSave); ferr != nil {
			return domain.ObservationWriteResult{}, ferr
		}
	}

	// 3. Optional relation edge, atomically in the same tx.
	if canonical.Relation != nil {
		if err := e.createRelation(ctx, tx, canonical.Relation, effect.Observation.ID); err != nil {
			return domain.ObservationWriteResult{}, err
		}
	}
	if e.failpoint != nil {
		if ferr := e.failpoint(handoffAfterEdge); ferr != nil {
			return domain.ObservationWriteResult{}, ferr
		}
	}

	// 4. Finalize: the committed receipt records the exact payload and ref.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO handoff_receipts
			(scope, key, payload_hash, canonical_payload, state, observation_id, initial_status, committed_at)
		 VALUES (?,?,?,?, 'committed', ?, ?, datetime('now'))`,
		string(scope), key, hash, payload, effect.Observation.ID, string(effect.Status),
	); err != nil {
		return domain.ObservationWriteResult{}, fmt.Errorf("sqlite handoff: insert receipt: %w", err)
	}
	if e.failpoint != nil {
		if ferr := e.failpoint(handoffBeforeCommit); ferr != nil {
			return domain.ObservationWriteResult{}, ferr
		}
	}

	ref, err := domain.NewLocalObservationRef(effect.Observation.ID)
	if err != nil {
		return domain.ObservationWriteResult{}, err
	}
	return domain.ObservationWriteResult{Ref: ref, Status: effect.Status}, nil
}

// saveObservation enlists the observation store on the shared tx and runs the
// specialized handoff save primitive there (1 MiB domain envelope — the
// canonical payload was already size-bounded at the domain layer). A
// ClassDedupSkipped outcome is a successful replay classification, not a
// failure: the effect carries the loaded observation with WriteStatusReplayed,
// and the dedup increment commits with the receipt.
func (e *SQLiteHandoffExecutor) saveObservation(ctx context.Context, tx *sql.Tx, canonical domain.CanonicalHandoff) (domain.SaveEffect, error) {
	in := canonical.Observation
	obs := &domain.Observation{
		Title:      in.Title,
		Content:    in.Content,
		Type:       in.Type,
		Project:    in.Project,
		Scope:      in.Scope,
		SessionID:  in.SessionID,
		TopicKey:   in.TopicKey,
		Confidence: in.Confidence,
		Source:     in.Source,
		Tags:       in.Tags,
	}
	var effect domain.SaveEffect
	err := e.stores.Observations.WithinTx(ctx, tx, func(c context.Context) error {
		var serr error
		effect, serr = e.stores.Observations.SaveHandoffWithEffect(c, obs)
		return serr
	})
	if err != nil && !domain.IsClass(err, domain.ClassDedupSkipped) {
		return domain.SaveEffect{}, err
	}
	if effect.Observation == nil {
		return domain.SaveEffect{}, domain.ErrHandoffPersistence
	}
	return effect, nil
}

// createRelation enlists the graph store on the shared tx and materializes
// the optional edge from the new observation to the referenced target. The
// SQLite namespace is local-only; public refs are a server concern.
//
// ErrAlreadyExists is tolerated ONLY when the durable current fact for the
// exact (from, to, type) triple inside this SAME transaction matches the
// requested weight, confidence, and reasoning exactly — a true idempotent
// duplicate. A fact from a different origin (the v2 one-current-fact index is
// keyed on (to, type, tenant, workspace)) or with differing attributes is a
// conflict that rolls back the whole handoff; if exactness cannot be proven,
// the executor fails closed to conflict.
func (e *SQLiteHandoffExecutor) createRelation(ctx context.Context, tx *sql.Tx, relation *domain.HandoffRelationInput, fromID int64) error {
	if relation.Target.LocalID == nil {
		return domain.ErrHandoffValidation
	}
	edge := &domain.Edge{
		FromObsID:    fromID,
		ToObsID:      *relation.Target.LocalID,
		RelationType: relation.Type,
		Weight:       relation.Weight,
		Confidence:   relation.Confidence,
		Reasoning:    relation.Reasoning,
	}
	return e.stores.Graph.WithinTx(ctx, tx, func(c context.Context) error {
		err := e.stores.Graph.CreateEdgeInTx(c, edge)
		if err == nil {
			return nil
		}
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return err
		}
		current, qerr := e.stores.Graph.CurrentEdgeByPairInTx(c, edge.FromObsID, edge.ToObsID, edge.RelationType)
		if qerr != nil {
			// Exactness cannot be proven inside this transaction: conflict.
			return domain.ErrHandoffConflict
		}
		if current.FromObsID != edge.FromObsID || current.ToObsID != edge.ToObsID ||
			current.RelationType != edge.RelationType || current.Weight != edge.Weight ||
			current.Confidence != edge.Confidence || current.Reasoning != edge.Reasoning {
			return domain.ErrHandoffConflict
		}
		return nil
	})
}
