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

func (s *Store) Observations() *ObservationRepository { return &ObservationRepository{s} }

var _ domain.ObservationRepository = (*ObservationRepository)(nil)

func (r *ObservationRepository) Save(ctx context.Context, o *domain.Observation) error {
	if o == nil || strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Content) == "" {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if o.TopicKey != "" {
			var id int64
			err := tx.QueryRow(ctx, `SELECT id FROM observations WHERE topic_key=$1 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, strings.TrimSpace(o.TopicKey)).Scan(&id)
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
		err := tx.QueryRow(ctx, `INSERT INTO observations (tenant_id, session_id, type, title, content, topic_key, created_at, updated_at, created_by, updated_by) VALUES (public.cortex_current_tenant(), $1, $2, $3, $4, NULLIF($5,''), $6, $6, NULLIF($7,'')::uuid, NULLIF($7,'')::uuid) RETURNING id`, o.SessionID, o.Type, o.Title, o.Content, o.TopicKey, o.CreatedAt, r.principal.Subject).Scan(&id)
		if err != nil {
			return fmt.Errorf("postgres observations: insert: %w", err)
		}
		o.ID = id
		return nil
	})
}

func (r *ObservationRepository) updateInTx(ctx context.Context, tx pgx.Tx, o *domain.Observation, id int64) error {
	now := time.Now().UTC()
	var created time.Time
	err := tx.QueryRow(ctx, `SELECT created_at FROM observations WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("observation", id)
	}
	if err != nil {
		return err
	}
	var revision int
	err = tx.QueryRow(ctx, `UPDATE observations SET type=$1,title=$2,content=$3,topic_key=NULLIF($4,''),revision_count=revision_count+1,updated_at=$5,updated_by=NULLIF($6,'')::uuid WHERE id=$7 AND deleted_at IS NULL RETURNING revision_count`, o.Type, o.Title, o.Content, o.TopicKey, now, r.principal.Subject, id).Scan(&revision)
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
		return tx.QueryRow(ctx, observationSelect+` WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&o.ID, &o.SessionID, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt)
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
		return tx.QueryRow(ctx, observationSelect+` WHERE topic_key=$1 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, key).Scan(&o.ID, &o.SessionID, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("observation", key)
	}
	return &o, err
}
func (r *ObservationRepository) Delete(ctx context.Context, id int64) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE observations SET deleted_at=now(),updated_at=now(),updated_by=NULLIF($1,'')::uuid WHERE id=$2 AND deleted_at IS NULL`, r.principal.Subject, id)
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
		q := observationSelect + ` WHERE deleted_at IS NULL`
		args := []any{}
		n := 1
		if f.Type != "" {
			q += fmt.Sprintf(" AND type=$%d", n)
			args = append(args, f.Type)
			n++
		}
		if f.SessionID != "" {
			q += fmt.Sprintf(" AND session_id=$%d", n)
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
			if e := rows.Scan(&o.ID, &o.SessionID, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt); e != nil {
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
		return tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE deleted_at IS NULL`).Scan(&n)
	})
	return
}
func (r *ObservationRepository) CountByRoot(ctx context.Context, id int64) (n int, err error) {
	return r.CountEdgesAsObs(ctx, id)
}
func (r *ObservationRepository) CountEdgesAsObs(ctx context.Context, id int64) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&n)
	})
	return
}
func (r *ObservationRepository) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	return r.List(ctx, domain.ObservationFilter{Source: source, Limit: limit})
}
func (r *ObservationRepository) GetByType(ctx context.Context, typ string, limit int) ([]*domain.Observation, error) {
	return r.List(ctx, domain.ObservationFilter{Type: typ, Limit: limit})
}

const observationSelect = `SELECT id, session_id::text, type, title, content, COALESCE(topic_key,''), created_at, updated_at FROM observations`

type SessionRepository struct{ *Store }

func (s *Store) Sessions() *SessionRepository { return &SessionRepository{s} }

var _ domain.SessionRepository = (*SessionRepository)(nil)

func (r *SessionRepository) Create(ctx context.Context, s *domain.Session) error {
	if s == nil {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO sessions(tenant_id,workspace_id,started_at,summary,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,$3,NULLIF($4,'')::uuid,NULLIF($4,'')::uuid) RETURNING id`, s.ID, s.StartedAt, s.Summary, r.principal.Subject).Scan(&id)
		if err != nil {
			return err
		}
		s.ID = fmt.Sprint(id)
		return nil
	})
}
func (r *SessionRepository) GetByID(ctx context.Context, id string) (*domain.Session, error) {
	var s domain.Session
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id::text,started_at,ended_at,COALESCE(summary,'') FROM sessions WHERE id=$1`, id).Scan(&s.ID, &s.StartedAt, &s.EndedAt, &s.Summary)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("session", id)
	}
	return &s, err
}
func (r *SessionRepository) End(ctx context.Context, id, summary string) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `UPDATE sessions SET ended_at=now(),summary=$1,updated_at=now() WHERE id=$2 AND ended_at IS NULL`, summary, id)
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
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id::text,started_at,ended_at,COALESCE(summary,'') FROM sessions ORDER BY started_at DESC`)
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
