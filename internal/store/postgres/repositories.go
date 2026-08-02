package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/domain"
)

type ObservationRepository struct{ *Store }

func (s *Store) observations() *ObservationRepository { return &ObservationRepository{s} }

var _ domain.ObservationRepository = (*ObservationRepository)(nil)

func (r *ObservationRepository) Save(ctx context.Context, o *domain.Observation) error {
	if o == nil || strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Content) == "" {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if o.TopicKey != "" {
			var id int64
			err := tx.QueryRow(ctx, `SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1 AND topic_key=$2 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, o.Project, strings.TrimSpace(o.TopicKey)).Scan(&id)
			if err == nil {
				return r.updateInTx(ctx, tx, o, id)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("postgres observations: topic lookup: %w", err)
			}
		}
		now := time.Now().UTC()
		if o.CreatedAt.IsZero() {
			o.CreatedAt = now
		}
		o.UpdatedAt = now
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO observations (tenant_id, session_id, project_key, scope, source, type, title, content, topic_key, created_at, updated_at, created_by, updated_by) VALUES (public.cortex_current_tenant(), (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid), $2, COALESCE(NULLIF($3,''),'project'), COALESCE(NULLIF($4,''),'manual'), $5, $6, $7, NULLIF($8,''), $9, $9, $10, $10) RETURNING id,public_id::text`, o.SessionID, o.Project, o.Scope, o.Source, o.Type, o.Title, o.Content, o.TopicKey, o.CreatedAt, actorFromContext(ctx)).Scan(&id, &o.PublicID)
		if err != nil {
			return fmt.Errorf("postgres observations: insert: %w", err)
		}
		o.ID = id
		if _, err := tx.Exec(ctx, `UPDATE observations SET owner_subject=$1,classification=COALESCE(NULLIF(scope,''),'project') WHERE tenant_id=public.cortex_current_tenant() AND id=$2`, r.principal.Subject, id); err != nil {
			return fmt.Errorf("postgres observations: metadata: %w", err)
		}
		return nil
	})
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
			if o.TopicKey != "" {
				var id int64
				err := tx.QueryRow(ctx, `SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND project_key=$1 AND topic_key=$2 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, o.Project, strings.TrimSpace(o.TopicKey)).Scan(&id)
				if err == nil {
					if err := r.updateInTx(ctx, tx, o, id); err != nil {
						return err
					}
					continue
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			now := time.Now().UTC()
			if o.CreatedAt.IsZero() {
				o.CreatedAt = now
			}
			o.UpdatedAt = now
			if err := tx.QueryRow(ctx, `INSERT INTO observations (tenant_id, session_id, project_key, scope, source, type, title, content, topic_key, created_at, updated_at, created_by, updated_by) VALUES (public.cortex_current_tenant(), (SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid), $2, COALESCE(NULLIF($3,''),'project'), COALESCE(NULLIF($4,''),'manual'), $5, $6, $7, NULLIF($8,''), $9, $9, $10, $10) RETURNING id,public_id::text`, o.SessionID, o.Project, o.Scope, o.Source, o.Type, o.Title, o.Content, o.TopicKey, o.CreatedAt, actorFromContext(ctx)).Scan(&o.ID, &o.PublicID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE observations SET owner_subject=$1,classification=COALESCE(NULLIF(scope,''),'project') WHERE tenant_id=public.cortex_current_tenant() AND id=$2`, r.principal.Subject, o.ID); err != nil {
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
	if strings.TrimSpace(key) == "" {
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
