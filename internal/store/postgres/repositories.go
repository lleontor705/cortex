package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/domain"
	domainentity "github.com/lleontor705/cortex/v2/internal/domain/entity"
)

type ObservationRepository struct{ *Store }

func (s *Store) observations() *ObservationRepository { return &ObservationRepository{s} }

var _ domain.ObservationRepository = (*ObservationRepository)(nil)

// errDedupSkipped is the in-transaction sentinel signalling that the handoff
// save hit the duplicate path. The handoff executor converts it into a
// replayed effect so the receipt still finalizes in the same transaction.
var errDedupSkipped = errors.New("postgres observations: dedup skipped")

// classifySaveError converts driver failures escaping the interactive save
// path into the stable handoff taxonomy: a bounded topic-lock timeout
// (SQLSTATE 55P03) or any other contention surfaces as a redacted,
// retryable-unavailable save error instead of leaking driver internals.
// Non-driver errors (validation, sentinels) pass through unchanged.
func classifySaveError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return handoffPgError(err, "save")
	}
	return err
}

// SaveWithEffect persists the observation and reports the durable write
// status decided inside the same transaction. REM-SAVE-001 compatibility: the
// interactive path preserves the exact legacy Save observable — topic upsert
// plus unconditional insert — and never performs dedup replay; manual
// duplicates always produce a second durable row. Dedup classification is a
// handoff-only concern (saveWithEffectInTx).
func (r *ObservationRepository) SaveWithEffect(ctx context.Context, o *domain.Observation) (domain.SaveEffect, error) {
	if o == nil || strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Content) == "" {
		return domain.SaveEffect{}, domain.ErrInvalidInput
	}
	var effect domain.SaveEffect
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		eff, err := r.saveLegacyInTx(ctx, tx, o)
		if err != nil {
			return err
		}
		effect = eff
		return nil
	})
	if err != nil {
		return domain.SaveEffect{}, classifySaveError(err)
	}
	return effect, nil
}

// Save keeps the legacy contract and delegates to SaveWithEffect, preserving
// signature, topic upsert, and unconditional duplicate inserts (REM-SAVE-001,
// RD2).
func (r *ObservationRepository) Save(ctx context.Context, o *domain.Observation) error {
	_, err := r.SaveWithEffect(ctx, o)
	return err
}

// workspaceID reports the validated workspace binding used to scope session,
// observation, dedup, and topic lookups. Compatibility stores without a
// workspace keep the legacy tenant-wide visibility.
func (s *Store) workspaceID() (string, bool) {
	if s.tenant == nil || s.tenant.WorkspaceID == "" {
		return "", false
	}
	if _, err := uuid.Parse(s.tenant.WorkspaceID); err != nil {
		return "", false
	}
	return s.tenant.WorkspaceID, true
}

// workspaceSessionPredicate scopes session rows to the bound workspace. It
// prefers the transaction-resolved workspace bigint (bindWorkspace) and falls
// back to the public UUID subquery only for ambient transactions that predate
// the binding, so isolated workspaces of one tenant stay invisible to each
// other on every schema revision.
func (s *Store) workspaceSessionPredicate(ctx context.Context, args []any) (string, []any) {
	if ws, ok := workspaceFromContext(ctx); ok {
		return fmt.Sprintf(` AND workspace_id=$%d`, len(args)+1), append(args, ws)
	}
	if id, ok := s.workspaceID(); ok {
		return fmt.Sprintf(` AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$%d::uuid)`, len(args)+1), append(args, id)
	}
	return "", args
}

// errWorkspaceBindingRequired fails closed when a workspace-scoped
// observation statement runs without the transaction-resolved workspace
// binding: after migration 105 there is no safe tenant-wide fallback for
// topic isolation, dedup, or the topic advisory lock.
var errWorkspaceBindingRequired = errors.New("postgres observations: workspace binding is required (migration 105)")

// errWorkspaceScopeRequired fails closed when a workspace-scoped read —
// list, search, topic lookup, count, or graph/prompt subquery — runs
// without the transaction-resolved workspace binding (SEC-01): sibling
// workspaces of one tenant must never become visible through a tenant-wide
// read fallback, so a missing binding is an error, never a degradation.
var errWorkspaceScopeRequired = errors.New("postgres observations: workspace scope is required for tenant reads")

// requireWorkspaceScope resolves the transaction workspace binding for read
// paths. Every observation list/search/topic/count and supporting
// prompt/graph subquery is bound to the resolved workspace bigint.
func requireWorkspaceScope(ctx context.Context) (int64, error) {
	if ws, ok := workspaceFromContext(ctx); ok {
		return ws, nil
	}
	return 0, errWorkspaceScopeRequired
}

// prepareTopicInTx normalizes the project and topic key exactly once — the
// lock key, the lookup, and the persisted bytes must all agree — and
// serializes concurrent first upserts of the same (tenant, workspace,
// project, topic) with a bounded advisory transaction lock, so the
// workspace-scoped partial unique index observations_topic_key_active_uq
// can never race two inserts into a commit-time 23505. The workspace
// binding is mandatory before the lock is taken: there is no NULL/tenant
// -wide fallback namespace. The lock key is JSON framed (no concatenation
// collisions), taken only after installing a lock_timeout, and released
// with the transaction.
func (r *ObservationRepository) prepareTopicInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation) error {
	o.Project = strings.TrimSpace(o.Project)
	o.TopicKey = strings.TrimSpace(o.TopicKey)
	if o.TopicKey == "" {
		return nil
	}
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return errWorkspaceBindingRequired
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, boundedLockTimeout); err != nil {
		return fmt.Errorf("postgres observations: topic lock timeout: %w", err)
	}
	// The advisory key parameters are explicitly cast: PostgreSQL cannot
	// infer a type for bind parameters used only inside jsonb_build_array,
	// and an untyped $n there fails with SQLSTATE 42P18 before the lock is
	// ever taken.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array(public.cortex_current_tenant()::text, $1::bigint, $2::text, $3::text)::text, 0))`, ws, o.Project, o.TopicKey); err != nil {
		return fmt.Errorf("postgres observations: topic lock: %w", err)
	}
	return nil
}

// findTopicObservationInTx locates the current observation for a topic key,
// isolated by the explicit observations.workspace_id column (migration 105).
// A missing workspace binding fails closed instead of degrading to a
// tenant-wide lookup.
func (r *ObservationRepository) findTopicObservationInTx(ctx context.Context, tx pgx.Tx, project, topic string) (int64, bool, error) {
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return 0, false, errWorkspaceBindingRequired
	}
	query := `SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1 AND topic_key=$2 AND deleted_at IS NULL AND workspace_id=$3`
	var id int64
	err := tx.QueryRow(ctx, query, project, topic, ws).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("postgres observations: topic lookup: %w", err)
	}
	return id, true, nil
}

// saveLegacyInTx is the transactional legacy core: topic upsert followed by
// an unconditional insert. It MUST run inside a transaction owned by the
// caller and never begins or commits one itself.
func (r *ObservationRepository) saveLegacyInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation) (domain.SaveEffect, error) {
	if err := r.prepareTopicInTx(ctx, tx, o); err != nil {
		return domain.SaveEffect{}, err
	}
	if o.TopicKey != "" {
		id, found, err := r.findTopicObservationInTx(ctx, tx, o.Project, o.TopicKey)
		if err != nil {
			return domain.SaveEffect{}, err
		}
		if found {
			if err := r.updateInTx(ctx, tx, o, id); err != nil {
				return domain.SaveEffect{}, err
			}
			return domain.SaveEffect{Observation: o, Status: domain.WriteStatusUpdated}, nil
		}
	}
	return r.insertObservationInTx(ctx, tx, o)
}

// saveWithEffectInTx is the handoff materialization core: validation, topic
// upsert, dedup replay, and insert. It MUST run inside a transaction owned by
// the caller (the handoff executor) and never begins or commits one itself.
// Unlike the legacy core it performs the manual dedup lookup and reports
// WriteStatusReplayed with the errDedupSkipped sentinel for identical
// in-window content.
func (r *ObservationRepository) saveWithEffectInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation) (domain.SaveEffect, error) {
	if o == nil || strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Content) == "" {
		return domain.SaveEffect{}, domain.ErrInvalidInput
	}
	// Dedup eligibility uses the caller-supplied type before defaulting,
	// mirroring the local SQLite store semantics.
	callerType := o.Type
	if o.Type == "" {
		o.Type = domain.TypeManual
	}
	if err := r.prepareTopicInTx(ctx, tx, o); err != nil {
		return domain.SaveEffect{}, err
	}
	if o.TopicKey != "" {
		id, found, err := r.findTopicObservationInTx(ctx, tx, o.Project, o.TopicKey)
		if err != nil {
			return domain.SaveEffect{}, err
		}
		if found {
			if err := r.updateInTx(ctx, tx, o, id); err != nil {
				return domain.SaveEffect{}, err
			}
			return domain.SaveEffect{Observation: o, Status: domain.WriteStatusUpdated}, nil
		}
	}
	if callerType == domain.TypeManual {
		ws, ok := workspaceFromContext(ctx)
		if !ok {
			return domain.SaveEffect{}, errWorkspaceBindingRequired
		}
		query := `
			SELECT id, public_id::text, created_at, updated_at FROM observations
			WHERE tenant_id=public.cortex_current_tenant()
			  AND project_key=$1
			  AND scope=COALESCE(NULLIF($2,''),'project')
			  AND type=$3
			  AND title=$4
			  AND content=$5
			  AND deleted_at IS NULL
			  AND workspace_id=$6
			  AND created_at >= now() - interval '15 minutes'
			ORDER BY created_at DESC
			LIMIT 1`
		args := []any{o.Project, o.Scope, o.Type, o.Title, o.Content, ws}
		err := tx.QueryRow(ctx, query, args...).Scan(&o.ID, &o.PublicID, &o.CreatedAt, &o.UpdatedAt)
		if err == nil {
			return domain.SaveEffect{Observation: o, Status: domain.WriteStatusReplayed}, errDedupSkipped
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.SaveEffect{}, fmt.Errorf("postgres observations: dedup lookup: %w", err)
		}
	}
	return r.insertObservationInTx(ctx, tx, o)
}

// insertObservationInTx performs the unconditional insert together with
// ownership metadata and entity links. With a resolved workspace binding the
// observation is written with an explicit observations.workspace_id value
// (migration 105); the session foreign key still resolves only inside the
// tenant. Compatibility transactions without a binding keep the legacy
// session-derived insert the 105 trigger accepts.
func (r *ObservationRepository) insertObservationInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation) (domain.SaveEffect, error) {
	now := time.Now().UTC()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	args := []any{o.SessionID, o.Project, o.Scope, o.Source, o.Type, o.Title, o.Content, o.TopicKey, o.CreatedAt, actorFromContext(ctx)}
	columns := `tenant_id, session_id, project_key, scope, source, type, title, content, topic_key, created_at, updated_at, created_by, updated_by`
	values := `public.cortex_current_tenant(), (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid), $2, COALESCE(NULLIF($3,''),'project'), COALESCE(NULLIF($4,''),'manual'), $5, $6, $7, NULLIF($8,''), $9, $9, $10, $10`
	if ws, ok := workspaceFromContext(ctx); ok {
		columns = `tenant_id, workspace_id, session_id, project_key, scope, source, type, title, content, topic_key, created_at, updated_at, created_by, updated_by`
		values = `public.cortex_current_tenant(), $11::bigint, (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid), $2, COALESCE(NULLIF($3,''),'project'), COALESCE(NULLIF($4,''),'manual'), $5, $6, $7, NULLIF($8,''), $9, $9, $10, $10`
		args = append(args, ws)
	} else if id, hasWS := r.workspaceID(); hasWS {
		values = `public.cortex_current_tenant(), (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$11::uuid)), $2, COALESCE(NULLIF($3,''),'project'), COALESCE(NULLIF($4,''),'manual'), $5, $6, $7, NULLIF($8,''), $9, $9, $10, $10`
		args = append(args, id)
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO observations (`+columns+`) VALUES (`+values+`) RETURNING id,public_id::text`, args...).Scan(&id, &o.PublicID)
	if err != nil {
		return domain.SaveEffect{}, fmt.Errorf("postgres observations: insert: %w", err)
	}
	o.ID = id
	if _, err := tx.Exec(ctx, `UPDATE observations SET owner_subject=$1,classification=COALESCE(NULLIF(scope,''),'project') WHERE tenant_id=public.cortex_current_tenant() AND id=$2`, r.principal.Subject, id); err != nil {
		return domain.SaveEffect{}, fmt.Errorf("postgres observations: metadata: %w", err)
	}
	if err := r.replaceEntityLinksInTx(ctx, tx, o); err != nil {
		return domain.SaveEffect{}, err
	}
	return domain.SaveEffect{Observation: o, Status: domain.WriteStatusCreated}, nil
}

// SaveBulk persists an already-authorized batch in one transaction. Authorization
// belongs to the facade; this repository method only provides atomic persistence.
func (r *ObservationRepository) SaveBulk(ctx context.Context, observations []*domain.Observation) error {
	if len(observations) == 0 {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, o := range observations {
			if o == nil || strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Content) == "" {
				return domain.ErrInvalidInput
			}
			if err := r.prepareTopicInTx(ctx, tx, o); err != nil {
				return err
			}
			if o.TopicKey != "" {
				id, found, err := r.findTopicObservationInTx(ctx, tx, o.Project, o.TopicKey)
				if err != nil {
					return err
				}
				if found {
					if err := r.updateInTx(ctx, tx, o, id); err != nil {
						return err
					}
					continue
				}
			}
			if _, err := r.insertObservationInTx(ctx, tx, o); err != nil {
				return err
			}
		}
		return nil
	})
}

// updateInTx updates the durable topic row. The lookup and the UPDATE are
// both scoped by the explicit observations.workspace_id column (migration
// 105) whenever the transaction resolved a workspace binding, so an update
// can never cross workspaces of one tenant.
func (r *ObservationRepository) updateInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation, id int64) error {
	now := time.Now().UTC()
	var created time.Time
	selectQuery := `SELECT created_at,public_id::text FROM observations WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL`
	selectArgs := []any{id}
	// The workspace predicate must extend the WHERE clause BEFORE RETURNING
	// is appended: tacking it onto the finished statement produced
	// `RETURNING revision_count AND workspace_id=$12`, which PostgreSQL
	// parses as a boolean expression over an integer (SQLSTATE 42804) and
	// rejected on every workspace-bound topic update.
	updateQuery := `UPDATE observations SET project_key=$1,scope=COALESCE(NULLIF($2,''),scope),classification=COALESCE(NULLIF($2,''),classification),source=COALESCE(NULLIF($3,''),source),type=$4,title=$5,content=$6,topic_key=NULLIF($7,''),revision_count=revision_count+1,updated_at=$8,updated_by=$9 WHERE tenant_id=public.cortex_current_tenant() AND id=$11 AND deleted_at IS NULL`
	updateArgs := []any{o.Project, o.Scope, o.Source, o.Type, o.Title, o.Content, o.TopicKey, now, actorFromContext(ctx), r.principal.Subject, id}
	if ws, ok := workspaceFromContext(ctx); ok {
		selectQuery += ` AND workspace_id=$2`
		selectArgs = append(selectArgs, ws)
		updateQuery += ` AND workspace_id=$12`
		updateArgs = append(updateArgs, ws)
	}
	updateQuery += ` AND (classification <> 'personal' OR owner_subject=$10) RETURNING revision_count`
	err := tx.QueryRow(ctx, selectQuery, selectArgs...).Scan(&created, &o.PublicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("observation", id)
	}
	if err != nil {
		return err
	}
	var revision int
	err = tx.QueryRow(ctx, updateQuery, updateArgs...).Scan(&revision)
	if err != nil {
		return fmt.Errorf("postgres observations: update: %w", err)
	}
	payload, _ := json.Marshal(o)
	if _, err = tx.Exec(ctx, `INSERT INTO observation_revisions(tenant_id,observation_id,revision,payload,reason) VALUES(public.cortex_current_tenant(),$1,$2,$3,'update')`, id, revision, payload); err != nil {
		return fmt.Errorf("postgres observations: revision: %w", err)
	}
	o.ID, o.CreatedAt, o.UpdatedAt = id, created, now
	return r.replaceEntityLinksInTx(ctx, tx, o)
}

func (r *ObservationRepository) replaceEntityLinksInTx(ctx context.Context, tx pgx.Tx, observation *domain.Observation) error {
	if _, err := tx.Exec(ctx, `DELETE FROM observation_entities WHERE tenant_id=public.cortex_current_tenant() AND observation_id=$1`, observation.ID); err != nil {
		return err
	}
	for _, link := range domainentity.Extract(observation) {
		var entityID int64
		if err := tx.QueryRow(ctx, `INSERT INTO entities(tenant_id,entity_type,entity_key,normalized_value,provenance,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,$3,$4,$5,$5) ON CONFLICT(tenant_id,entity_type,entity_key) DO UPDATE SET normalized_value=EXCLUDED.normalized_value,provenance=EXCLUDED.provenance,updated_at=now(),updated_by=EXCLUDED.updated_by RETURNING id`, link.EntityType, link.EntityValue, link.NormalizedValue, link.Provenance, actorFromContext(ctx)).Scan(&entityID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO observation_entities(tenant_id,observation_id,entity_id,confidence,created_by) VALUES(public.cortex_current_tenant(),$1,$2,1,$3)`, observation.ID, entityID, actorFromContext(ctx)); err != nil {
			return err
		}
	}
	return nil
}
func (r *ObservationRepository) Update(ctx context.Context, o *domain.Observation) error {
	if o == nil || o.ID <= 0 {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error { return r.updateInTx(ctx, tx, o, o.ID) })
}
func (r *ObservationRepository) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	var o domain.Observation
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, observationSelect+` WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL`, id).Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt, &o.OwnerSubject)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("observation", id)
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}
func (r *ObservationRepository) GetByTopicKey(ctx context.Context, project, key string) (*domain.Observation, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrInvalidInput
	}
	var o domain.Observation
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, observationSelect+` WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1 AND topic_key=$2 AND deleted_at IS NULL AND workspace_id=$3 ORDER BY updated_at DESC LIMIT 1`, project, key, ws).Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt, &o.OwnerSubject)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("observation", key)
	}
	return &o, err
}
func (r *ObservationRepository) Delete(ctx context.Context, id int64) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE observations SET deleted_at=now(),updated_at=now(),updated_by=$1 WHERE tenant_id=public.cortex_current_tenant() AND id=$2 AND deleted_at IS NULL AND (classification <> 'personal' OR owner_subject=$3)`, actorFromContext(ctx), id, r.principal.Subject)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("observation", id)
		}
		return nil
	})
}
func (r *ObservationRepository) List(ctx context.Context, f domain.ObservationFilter) (out []*domain.Observation, err error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// SEC-01: the transaction-resolved workspace binding scopes the
		// list before any predicate is built; a missing binding fails
		// closed rather than degrading to tenant-wide visibility.
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		q := observationSelect + ` WHERE tenant_id=public.cortex_current_tenant() AND deleted_at IS NULL`
		args := []any{}
		n := 1
		q += fmt.Sprintf(" AND workspace_id=$%d", n)
		args = append(args, ws)
		n++
		if projects, wildcard := r.projectGrantFilter(); r.authorized && !wildcard {
			if len(projects) == 0 {
				q += ` AND FALSE`
			} else {
				q += fmt.Sprintf(" AND project_key = ANY($%d)", n)
				args = append(args, projects)
				n++
			}
		}
		if r.authorized {
			if classes, wildcard := r.classificationGrantFilter(); !wildcard {
				if len(classes) == 0 {
					q += ` AND classification NOT IN ('restricted','confidential')`
				} else {
					q += fmt.Sprintf(" AND (classification = ANY($%d) OR classification NOT IN ('restricted','confidential'))", n)
					args = append(args, classes)
					n++
				}
			}
			q += fmt.Sprintf(" AND (classification <> 'personal' OR owner_subject=$%d)", n)
			args = append(args, r.principal.Subject)
			n++
		}
		if f.OwnerSubject != "" {
			q += fmt.Sprintf(" AND owner_subject=$%d", n)
			args = append(args, f.OwnerSubject)
			n++
		}
		if f.Type != "" {
			q += fmt.Sprintf(" AND type=$%d", n)
			args = append(args, f.Type)
			n++
		}
		for _, clause := range []struct{ value, column string }{{f.Project, "project_key"}, {f.Scope, "scope"}, {f.Source, "source"}} {
			if clause.value != "" {
				q += fmt.Sprintf(" AND %s=$%d", clause.column, n)
				args = append(args, clause.value)
				n++
			}
		}
		if f.SessionID != "" {
			// Session joins must agree on the workspace: the referenced
			// session resolves inside the bound workspace only.
			q += fmt.Sprintf(" AND session_id=(SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$%d::uuid AND workspace_id=$%d)", n, n+1)
			args = append(args, f.SessionID, ws)
			n += 2
		}
		if f.CreatedAfter != nil {
			q += fmt.Sprintf(" AND created_at>$%d", n)
			args = append(args, *f.CreatedAfter)
			n++
		}
		if f.CreatedBefore != nil {
			q += fmt.Sprintf(" AND created_at<$%d", n)
			args = append(args, *f.CreatedBefore)
			n++
		}
		order := "DESC"
		if f.OrderAsc {
			order = "ASC"
		}
		q += fmt.Sprintf(" ORDER BY created_at %s LIMIT $%d OFFSET $%d", order, n, n+1)
		args = append(args, f.Limit, f.Offset)
		rows, e := tx.Query(ctx, q, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			o := new(domain.Observation)
			if e := rows.Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt, &o.OwnerSubject); e != nil {
				return e
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return
}
func (r *ObservationRepository) CountAll(ctx context.Context) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=public.cortex_current_tenant() AND deleted_at IS NULL AND workspace_id=$1`, ws).Scan(&n)
	})
	return
}
func (r *ObservationRepository) CountByRoot(ctx context.Context, id int64) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		// The recursion may only traverse edges and observations of the
		// bound workspace (SEC-01 defense in depth: migration 107 already
		// keeps edges inside one workspace).
		return tx.QueryRow(ctx, `WITH RECURSIVE reach(id) AS (SELECT $1::bigint UNION SELECT CASE WHEN e.from_observation_id=reach.id THEN e.to_observation_id ELSE e.from_observation_id END FROM edges e JOIN reach ON e.from_observation_id=reach.id OR e.to_observation_id=reach.id JOIN observations a ON a.id=e.from_observation_id AND a.tenant_id=public.cortex_current_tenant() AND a.deleted_at IS NULL AND a.workspace_id=$2 JOIN observations b ON b.id=e.to_observation_id AND b.tenant_id=public.cortex_current_tenant() AND b.deleted_at IS NULL AND b.workspace_id=$2 WHERE e.tenant_id=public.cortex_current_tenant() AND e.workspace_id=$2) SELECT count(*) FROM reach WHERE id<>$1::bigint`, id, ws).Scan(&n)
	})
	return
}
func (r *ObservationRepository) CountEdgesAsObs(ctx context.Context, id int64) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL AND workspace_id=$2`, id, ws).Scan(&n)
	})
	return
}
func (r *ObservationRepository) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	return r.List(ctx, domain.ObservationFilter{Source: source, Limit: limit})
}
func (r *ObservationRepository) GetByType(ctx context.Context, typ string, limit int) ([]*domain.Observation, error) {
	return r.List(ctx, domain.ObservationFilter{Type: typ, Limit: limit})
}

const observationSelect = `SELECT public_id::text, id, session_id::text, COALESCE(project_key,''), COALESCE(scope,''), COALESCE(source,''), type, title, content, COALESCE(topic_key,''), created_at, updated_at, COALESCE(owner_subject,'') FROM observations`

func (r *ObservationRepository) GetByPublicID(ctx context.Context, publicID string) (*domain.Observation, error) {
	var o domain.Observation
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, observationSelect+` WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid AND deleted_at IS NULL`, publicID).Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt, &o.OwnerSubject)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("observation", publicID)
	}
	return &o, err
}

// ListArchivable returns old, low-importance observations for lifecycle jobs.
// Importance is currently represented by the supplied score threshold; the
// server schema keeps lifecycle filtering tenant-scoped inside the transaction.
func (r *ObservationRepository) ListArchivable(ctx context.Context, cutoff time.Time, minScore float64, limit int) ([]*domain.Observation, error) {
	if limit <= 0 {
		limit = 500
	}
	var out []*domain.Observation
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT o.public_id::text,o.id,o.session_id::text,COALESCE(o.project_key,''),COALESCE(o.scope,''),COALESCE(o.source,''),o.type,o.title,o.content,COALESCE(o.topic_key,''),o.created_at,o.updated_at FROM observations o LEFT JOIN importance_scores s ON s.tenant_id=o.tenant_id AND s.observation_id=o.id WHERE o.tenant_id=public.cortex_current_tenant() AND o.session_id IN (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$4::uuid)) AND o.deleted_at IS NULL AND o.created_at < $1 AND (s.score IS NULL OR s.score < $2) ORDER BY o.created_at LIMIT $3`, cutoff, minScore, limit, r.tenant.WorkspaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o := new(domain.Observation)
			if err := rows.Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt); err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

type SessionRepository struct{ *Store }

var _ domain.SessionRepository = (*SessionRepository)(nil)

func (r *SessionRepository) Create(ctx context.Context, s *domain.Session) error {
	if s == nil {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var publicID string
		workspace := r.tenant.WorkspaceID
		if workspace == "" {
			return fmt.Errorf("postgres sessions: workspace context is required")
		}
		err := tx.QueryRow(ctx, `INSERT INTO sessions(tenant_id,workspace_id,project_key,started_at,summary,created_by,updated_by) VALUES(public.cortex_current_tenant(),(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid),$2,$3,$4,$5,$5) RETURNING public_id::text`, workspace, s.Project, s.StartedAt, s.Summary, actorFromContext(ctx)).Scan(&publicID)
		if err != nil {
			return err
		}
		s.ID = publicID
		return nil
	})
}
func (r *SessionRepository) GetByID(ctx context.Context, id string) (*domain.Session, error) {
	var s domain.Session
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT public_id::text,started_at,ended_at,COALESCE(summary,'') FROM sessions WHERE public_id=$1::uuid`, id).Scan(&s.ID, &s.StartedAt, &s.EndedAt, &s.Summary)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("session", id)
	}
	return &s, err
}
func (r *SessionRepository) End(ctx context.Context, id, summary string) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `UPDATE sessions SET ended_at=now(),summary=$1,updated_at=now() WHERE public_id=$2::uuid AND ended_at IS NULL`, summary, id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return notFound("session", id)
		}
		return nil
	})
}
func (r *SessionRepository) List(ctx context.Context, project string) (out []*domain.Session, err error) {
	out = make([]*domain.Session, 0)
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		query := `SELECT public_id::text,started_at,ended_at,COALESCE(summary,'') FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND ($1='' OR project_key=$1) AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$2::uuid)`
		args := []any{project, r.tenant.WorkspaceID}
		if r.authorized && !r.isAdmin() {
			query += ` AND (created_by::text=$3 OR updated_by::text=$3)`
			args = append(args, r.principal.Subject)
		}
		query += ` ORDER BY started_at DESC`
		rows, e := tx.Query(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			s := new(domain.Session)
			if e := rows.Scan(&s.ID, &s.StartedAt, &s.EndedAt, &s.Summary); e != nil {
				return e
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return
}
