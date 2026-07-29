package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/domain"
)

type PromptRepository struct{ *Store }

func (s *Store) Prompts() *PromptRepository { return &PromptRepository{s} }

var _ domain.PromptRepository = (*PromptRepository)(nil)

func (r *PromptRepository) Save(ctx context.Context, p *domain.Prompt) error {
	if p == nil || p.Content == "" {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO prompts(tenant_id,session_id,content,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,NULLIF($3,'')::uuid,NULLIF($3,'')::uuid) RETURNING id`, p.SessionID, p.Content, r.principal.Subject).Scan(&id)
		p.ID = id
		return err
	})
}
func (r *PromptRepository) List(ctx context.Context, project string, limit int) (out []*domain.Prompt, err error) {
	if limit <= 0 {
		limit = 20
	}
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id,content,session_id::text,created_at FROM prompts ORDER BY created_at DESC LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			p := new(domain.Prompt)
			if e := rows.Scan(&p.ID, &p.Content, &p.SessionID, &p.CreatedAt); e != nil {
				return e
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return
}

type GraphRepository struct{ *Store }

func (s *Store) Graph() *GraphRepository { return &GraphRepository{s} }

var _ domain.GraphRepository = (*GraphRepository)(nil)

func (r *GraphRepository) CreateEdge(ctx context.Context, e *domain.Edge) error {
	if e == nil || e.FromObsID <= 0 || e.ToObsID <= 0 {
		return domain.ErrInvalidInput
	}
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type,valid_from,valid_until,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($6,'')::uuid) RETURNING id`, e.FromObsID, e.ToObsID, e.RelationType, e.ValidFrom, e.ValidUntil, r.principal.Subject).Scan(&id)
		e.ID = id
		return err
	})
}
func (r *GraphRepository) DeleteEdge(ctx context.Context, id int64) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `DELETE FROM edges WHERE id=$1`, id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return notFound("edge", id)
		}
		return nil
	})
}
func (r *GraphRepository) GetEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	var e domain.Edge
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,from_observation_id,to_observation_id,relation_type,valid_from,valid_until,created_at FROM edges WHERE id=$1`, id).Scan(&e.ID, &e.FromObsID, &e.ToObsID, &e.RelationType, &e.ValidFrom, &e.ValidUntil, &e.CreatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("edge", id)
	}
	return &e, err
}
func (r *GraphRepository) GetEdgesForObservation(ctx context.Context, id int64) (out []*domain.Edge, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id,from_observation_id,to_observation_id,relation_type,valid_from,valid_until,created_at FROM edges WHERE from_observation_id=$1 OR to_observation_id=$1`, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			x := new(domain.Edge)
			if e := rows.Scan(&x.ID, &x.FromObsID, &x.ToObsID, &x.RelationType, &x.ValidFrom, &x.ValidUntil, &x.CreatedAt); e != nil {
				return e
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return
}
func (r *GraphRepository) GetRelated(ctx context.Context, id int64, depth int) (out []*domain.Observation, err error) {
	if depth <= 0 {
		depth = 1
	}
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `WITH RECURSIVE reach(id,d) AS (SELECT $1,0 UNION SELECT CASE WHEN e.from_observation_id=reach.id THEN e.to_observation_id ELSE e.from_observation_id END,d+1 FROM edges e JOIN reach ON e.from_observation_id=reach.id OR e.to_observation_id=reach.id WHERE d<$2) SELECT o.id,o.session_id::text,o.type,o.title,o.content,COALESCE(o.topic_key,''),o.created_at,o.updated_at FROM observations o JOIN reach r ON r.id=o.id WHERE r.d>0 AND o.deleted_at IS NULL`, id, depth)
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
func (r *GraphRepository) GetEvolutionChain(ctx context.Context, a, b int64) ([]*domain.Edge, error) {
	return r.GetEdgesForObservation(ctx, a)
}
func (r *GraphRepository) CountEdgesByObservation(ctx context.Context, id int64) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM edges WHERE from_observation_id=$1 OR to_observation_id=$1`, id).Scan(&n)
	})
	return
}
func (r *GraphRepository) CountAllEdges(ctx context.Context) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM edges`).Scan(&n)
	})
	return
}
func (r *GraphRepository) GetContradictions(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
	return r.GetEdgesForObservation(ctx, 0)
}
func (r *GraphRepository) UpdateEdge(ctx context.Context, e *domain.Edge) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE edges SET relation_type=$1,valid_from=$2,valid_until=$3,updated_at=now() WHERE id=$4`, e.RelationType, e.ValidFrom, e.ValidUntil, e.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("edge", e.ID)
		}
		return nil
	})
}

type EntityRepository struct{ *Store }

func (s *Store) Entities() *EntityRepository { return &EntityRepository{s} }

var _ domain.EntityRepository = (*EntityRepository)(nil)

func (r *EntityRepository) SaveLinks(ctx context.Context, links []*domain.EntityLink) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, l := range links {
			var eid int64
			if err := tx.QueryRow(ctx, `INSERT INTO entities(tenant_id,entity_type,entity_key,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,NULLIF($3,'')::uuid,NULLIF($3,'')::uuid) ON CONFLICT(tenant_id,entity_type,entity_key) DO UPDATE SET updated_at=now() RETURNING id`, l.EntityType, l.EntityValue, r.principal.Subject).Scan(&eid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO observation_entities(tenant_id,observation_id,entity_id,created_by) VALUES(public.cortex_current_tenant(),$1,$2,NULLIF($3,'')::uuid) ON CONFLICT DO NOTHING`, l.ObservationID, eid, r.principal.Subject); err != nil {
				return err
			}
			l.ID = eid
		}
		return nil
	})
}
func (r *EntityRepository) GetByObservation(ctx context.Context, id int64) (out []*domain.EntityLink, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT oe.entity_id,oe.observation_id,e.entity_type,e.entity_key,e.created_at FROM observation_entities oe JOIN entities e ON e.id=oe.entity_id WHERE oe.observation_id=$1`, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			l := new(domain.EntityLink)
			if e := rows.Scan(&l.ID, &l.ObservationID, &l.EntityType, &l.EntityValue, &l.CreatedAt); e != nil {
				return e
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return
}
func (r *EntityRepository) FindByEntity(ctx context.Context, typ, val string) (out []*domain.EntityLink, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT oe.entity_id,oe.observation_id,e.entity_type,e.entity_key,e.created_at FROM observation_entities oe JOIN entities e ON e.id=oe.entity_id WHERE e.entity_type=$1 AND e.entity_key=$2`, typ, val)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			l := new(domain.EntityLink)
			if e := rows.Scan(&l.ID, &l.ObservationID, &l.EntityType, &l.EntityValue, &l.CreatedAt); e != nil {
				return e
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return
}
func (r *EntityRepository) DeleteByObservation(ctx context.Context, id int64) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM observation_entities WHERE observation_id=$1`, id)
		return e
	})
}

type SearchRepository struct{ *Store }

func (s *Store) Search() *SearchRepository { return &SearchRepository{s} }

var _ domain.SearchRepository = (*SearchRepository)(nil)

func (r *SearchRepository) Search(ctx context.Context, query string, opts domain.SearchOptions) (out []*domain.SearchResult, err error) {
	if query != "" {
		opts.Query = query
	}
	if opts.Query == "" {
		return nil, domain.ErrInvalidInput
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT o.id,o.session_id::text,o.type,o.title,o.content,COALESCE(o.topic_key,''),o.created_at,o.updated_at,ts_rank_cd(o.search_vector,websearch_to_tsquery('simple',$1)) FROM observations o WHERE o.deleted_at IS NULL AND o.search_vector @@ websearch_to_tsquery('simple',$1) ORDER BY 9 DESC,o.created_at DESC LIMIT $2`, opts.Query, opts.Limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			x := new(domain.SearchResult)
			if e := rows.Scan(&x.ID, &x.SessionID, &x.Type, &x.Title, &x.Content, &x.TopicKey, &x.CreatedAt, &x.UpdatedAt, &x.Rank); e != nil {
				return e
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return
}
