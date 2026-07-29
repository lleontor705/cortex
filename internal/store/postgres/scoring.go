package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/domain"
	scoringdomain "github.com/lleontor705/cortex/internal/domain/scoring"
)

// GetScore retrieves the tenant-scoped score for an observation.
func (r *Store) GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error) {
	var score domain.ImportanceScore
	var last *time.Time
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT observation_id,score,access_count,last_accessed,updated_at FROM importance_scores WHERE observation_id=$1`, obsID).
			Scan(&score.ObservationID, &score.Score, &score.AccessCount, &last, &score.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &domain.NotFoundError{Type: "importance_score", ID: obsID}
	}
	if err != nil {
		return nil, fmt.Errorf("postgres scoring: get score: %w", err)
	}
	if last != nil {
		score.LastAccessed = *last
	}
	return &score, nil
}

// UpdateScore adjusts a score and clamps it to the domain range.
func (r *Store) UpdateScore(ctx context.Context, obsID int64, increment float64) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var score float64
		if err := tx.QueryRow(ctx, `SELECT score FROM importance_scores WHERE observation_id=$1 FOR UPDATE`, obsID).Scan(&score); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &domain.NotFoundError{Type: "importance_score", ID: obsID}
			}
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE importance_scores SET score=$1,updated_at=now() WHERE observation_id=$2`, math.Max(0, math.Min(5, score+increment)), obsID)
		return err
	})
}

// GetTop retrieves tenant-scoped scores for a project.
func (r *Store) GetTop(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	return r.GetTopByScore(ctx, project, limit)
}

func (r *Store) RecordAccess(ctx context.Context, obsID int64) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE importance_scores SET access_count=access_count+1,last_accessed=now(),updated_at=now() WHERE observation_id=$1`, obsID)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return &domain.NotFoundError{Type: "importance_score", ID: obsID}
		}
		return nil
	})
}

func (r *Store) SetScore(ctx context.Context, obsID int64, score float64) error {
	score = math.Max(0, math.Min(5, score))
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `INSERT INTO importance_scores(tenant_id,workspace_id,project_id,project_key,observation_id,score) SELECT public.cortex_current_tenant(),w.id,o.project_id,o.project_key,o.id,$1 FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.id=s.workspace_id WHERE o.id=$2 AND o.deleted_at IS NULL ON CONFLICT (tenant_id,observation_id) DO UPDATE SET score=EXCLUDED.score,updated_at=now()`, score, obsID)
		if err != nil {
			return fmt.Errorf("postgres scoring: set score: %w", err)
		}
		if result.RowsAffected() == 0 {
			return &domain.NotFoundError{Type: "observation", ID: obsID}
		}
		return nil
	})
}

func (r *Store) GetAllScores(ctx context.Context) ([]*domain.ImportanceScore, error) {
	var out []*domain.ImportanceScore
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT observation_id,score,access_count,last_accessed,updated_at FROM importance_scores WHERE workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid) ORDER BY score DESC`, r.tenant.WorkspaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s domain.ImportanceScore
			var last *time.Time
			if err := rows.Scan(&s.ObservationID, &s.Score, &s.AccessCount, &last, &s.UpdatedAt); err != nil {
				return err
			}
			if last != nil {
				s.LastAccessed = *last
			}
			out = append(out, &s)
		}
		return rows.Err()
	})
	return out, err
}

func (r *Store) GetTopByScore(ctx context.Context, project string, limit int) ([]*domain.ImportanceScore, error) {
	if limit <= 0 {
		limit = 10
	}
	var out []*domain.ImportanceScore
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT s.observation_id,s.score,s.access_count,s.last_accessed,s.updated_at FROM importance_scores s JOIN observations o ON o.tenant_id=s.tenant_id AND o.id=s.observation_id WHERE s.workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid) AND o.deleted_at IS NULL AND ($2='' OR o.project_key=$2) ORDER BY s.score DESC,s.observation_id LIMIT $3`, r.tenant.WorkspaceID, project, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s domain.ImportanceScore
			var last *time.Time
			if err := rows.Scan(&s.ObservationID, &s.Score, &s.AccessCount, &last, &s.UpdatedAt); err != nil {
				return err
			}
			if last != nil {
				s.LastAccessed = *last
			}
			out = append(out, &s)
		}
		return rows.Err()
	})
	return out, err
}

func (r *Store) GetIncomingEdgeCount(ctx context.Context, obsID int64) (int, error) {
	var count int
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM edges WHERE to_observation_id=$1`, obsID).Scan(&count)
	})
	return count, err
}

func (r *Store) GetObservation(ctx context.Context, obsID int64) (*domain.Observation, error) {
	var o domain.Observation
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,session_id::text,COALESCE(project_key,''),COALESCE(scope,''),COALESCE(source,''),type,title,content,COALESCE(topic_key,''),created_at,updated_at FROM observations WHERE id=$1 AND deleted_at IS NULL`, obsID).
			Scan(&o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &domain.NotFoundError{Type: "observation", ID: obsID}
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

var _ scoringdomain.Repository = (*Store)(nil)
