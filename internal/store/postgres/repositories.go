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
	"github.com/lleontor705/cortex/internal/domain"
	domainentity "github.com/lleontor705/cortex/internal/domain/entity"
)

type ObservationRepository struct{ *Store }

func (s *Store) observations() *ObservationRepository { return &ObservationRepository{s} }

var _ domain.ObservationRepository = (*ObservationRepository)(nil)

// errDedupSkipped is the in-transaction sentinel signalling that the handoff
// save hit the duplicate path. The handoff executor converts it into a
// replayed effect so the receipt still finalizes in the same transaction.
var errDedupSkipped = errors.New("postgres observations: dedup skipped")

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
		return domain.SaveEffect{}, err
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

// prepareTopicInTx normalizes the topic key once (lookup and persisted bytes
// must agree) and serializes concurrent first upserts of the same
// (tenant, topic) with an advisory transaction lock, so the partial unique
// index observations_topic_key_active_uq can never race two inserts into a
// commit-time 23505. The lock is released with the transaction.
func (r *ObservationRepository) prepareTopicInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation) error {
	o.TopicKey = strings.TrimSpace(o.TopicKey)
	if o.TopicKey == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(public.cortex_current_tenant()::text || ':' || $1, 0))`, o.TopicKey); err != nil {
		return fmt.Errorf("postgres observations: topic lock: %w", err)
	}
	return nil
}

// findTopicObservationInTx locates the current observation for a topic key,
// restricted to the bound workspace when one exists.
func (r *ObservationRepository) findTopicObservationInTx(ctx context.Context, tx pgx.Tx, project, topic string) (int64, bool, error) {
	query := `SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1 AND topic_key=$2 AND deleted_at IS NULL`
	args := []any{project, topic}
	if ws, ok := r.workspaceID(); ok {
		query += fmt.Sprintf(` AND session_id IN (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$%d::uuid))`, len(args)+1)
		args = append(args, ws)
	}
	query += ` ORDER BY updated_at DESC LIMIT 1`
	var id int64
	err := tx.QueryRow(ctx, query, args...).Scan(&id)
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
		query := `
			SELECT id, public_id::text, created_at, updated_at FROM observations
			WHERE tenant_id=public.cortex_current_tenant()
			  AND project_key=$1
			  AND scope=COALESCE(NULLIF($2,''),'project')
			  AND type=$3
			  AND title=$4
			  AND content=$5
			  AND deleted_at IS NULL
			  AND created_at >= now() - interval '15 minutes'`
		args := []any{o.Project, o.Scope, o.Type, o.Title, o.Content}
		if ws, ok := r.workspaceID(); ok {
			query += fmt.Sprintf(`
			  AND session_id IN (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$%d::uuid))`, len(args)+1)
			args = append(args, ws)
		}
		query += `
			ORDER BY created_at DESC
			LIMIT 1`
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
// ownership metadata and entity links. The session foreign key resolves only
// inside the tenant, and inside the bound workspace when one exists.
func (r *ObservationRepository) insertObservationInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation) (domain.SaveEffect, error) {
	now := time.Now().UTC()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	sessionFilter := ""
	args := []any{o.SessionID, o.Project, o.Scope, o.Source, o.Type, o.Title, o.Content, o.TopicKey, o.CreatedAt, actorFromContext(ctx)}
	if ws, ok := r.workspaceID(); ok {
		sessionFilter = fmt.Sprintf(` AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$%d::uuid)`, len(args)+1)
		args = append(args, ws)
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO observations (tenant_id, session_id, project_key, scope, source, type, title, content, topic_key, created_at, updated_at, created_by, updated_by) VALUES (public.cortex_current_tenant(), (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid`+sessionFilter+`), $2, COALESCE(NULLIF($3,''),'project'), COALESCE(NULLIF($4,''),'manual'), $5, $6, $7, NULLIF($8,''), $9, $9, $10, $10) RETURNING id,public_id::text`, args...).Scan(&id, &o.PublicID)
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

func (r *ObservationRepository) updateInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation, id int64) error {
	now := time.Now().UTC()
	var created time.Time
	err := tx.QueryRow(ctx, `SELECT created_at,public_id::text FROM observations WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL`, id).Scan(&created, &o.PublicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("observation", id)
	}
	if err != nil {
		return err
	}
	var revision int
	err = tx.QueryRow(ctx, `UPDATE observations SET project_key=$1,scope=COALESCE(NULLIF($2,''),scope),classification=COALESCE(NULLIF($2,''),classification),source=COALESCE(NULLIF($3,''),source),type=$4,title=$5,content=$6,topic_key=NULLIF($7,''),revision_count=revision_count+1,updated_at=$8,updated_by=$9 WHERE tenant_id=public.cortex_current_tenant() AND id=$11 AND deleted_at IS NULL AND (classification <> 'personal' OR owner_subject=$10) RETURNING revision_count`, o.Project, o.Scope, o.Source, o.Type, o.Title, o.Content, o.TopicKey, now, actorFromContext(ctx), r.principal.Subject, id).Scan(&revision)
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
		return tx.QueryRow(ctx, observationSelect+` WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL`, id).Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt)
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
		return tx.QueryRow(ctx, observationSelect+` WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1 AND topic_key=$2 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, project, key).Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt)
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
		q := observationSelect + ` WHERE tenant_id=public.cortex_current_tenant() AND deleted_at IS NULL`
		args := []any{}
		n := 1
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
			q += fmt.Sprintf(" AND session_id=(SELECT id FROM sessions WHERE public_id=$%d::uuid)", n)
			args = append(args, f.SessionID)
			n++
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
			if e := rows.Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt); e != nil {
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
		return tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=public.cortex_current_tenant() AND deleted_at IS NULL`).Scan(&n)
	})
	return
}
func (r *ObservationRepository) CountByRoot(ctx context.Context, id int64) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `WITH RECURSIVE reach(id) AS (SELECT $1::bigint UNION SELECT CASE WHEN e.from_observation_id=reach.id THEN e.to_observation_id ELSE e.from_observation_id END FROM edges e JOIN reach ON e.from_observation_id=reach.id OR e.to_observation_id=reach.id JOIN observations a ON a.id=e.from_observation_id AND a.tenant_id=public.cortex_current_tenant() AND a.deleted_at IS NULL JOIN observations b ON b.id=e.to_observation_id AND b.tenant_id=public.cortex_current_tenant() AND b.deleted_at IS NULL WHERE e.tenant_id=public.cortex_current_tenant()) SELECT count(*) FROM reach WHERE id<>$1::bigint`, id).Scan(&n)
	})
	return
}
func (r *ObservationRepository) CountEdgesAsObs(ctx context.Context, id int64) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL`, id).Scan(&n)
	})
	return
}
func (r *ObservationRepository) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	return r.List(ctx, domain.ObservationFilter{Source: source, Limit: limit})
}
func (r *ObservationRepository) GetByType(ctx context.Context, typ string, limit int) ([]*domain.Observation, error) {
	return r.List(ctx, domain.ObservationFilter{Type: typ, Limit: limit})
}

const observationSelect = `SELECT public_id::text, id, session_id::text, COALESCE(project_key,''), COALESCE(scope,''), COALESCE(source,''), type, title, content, COALESCE(topic_key,''), created_at, updated_at FROM observations`

func (r *ObservationRepository) GetByPublicID(ctx context.Context, publicID string) (*domain.Observation, error) {
	var o domain.Observation
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, observationSelect+` WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid AND deleted_at IS NULL`, publicID).Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt)
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
		rows, e := tx.Query(ctx, `SELECT public_id::text,started_at,ended_at,COALESCE(summary,'') FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND ($1='' OR project_key=$1) AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$2::uuid) ORDER BY started_at DESC`, project, r.tenant.WorkspaceID)
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
