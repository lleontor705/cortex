package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

func (s *AuthorizedStore) PushSync(ctx context.Context, batch *domain.SyncBatch) (*domain.SyncResult, error) {
	if batch == nil {
		return nil, domain.ErrInvalidInput
	}
	for _, session := range batch.Sessions {
		if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, session.Project, s.store.principal.Subject, ""); err != nil {
			return nil, err
		}
	}
	for _, observation := range batch.Observations {
		if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, observation.Project, s.store.principal.Subject, observation.Scope); err != nil {
			return nil, err
		}
	}
	if len(batch.Edges) > 0 {
		if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionWrite, "", "", ""); err != nil {
			return nil, err
		}
	}
	accepted := 0
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace := s.store.tenant.WorkspaceID
		if workspace == "" {
			return errors.New("postgres sync: workspace context is required")
		}
		actor := actorFromContext(ctx)
		for _, value := range batch.Sessions {
			if strings.TrimSpace(value.SyncID) == "" || value.StartedAt.IsZero() {
				return domain.ErrInvalidInput
			}
			updated := value.UpdatedAt
			if updated.IsZero() {
				updated = value.StartedAt
			}
			_, err := tx.Exec(ctx, `INSERT INTO sessions(tenant_id,workspace_id,client_id,project_key,started_at,ended_at,summary,created_at,updated_at,created_by,updated_by) VALUES(public.cortex_current_tenant(),(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid),$2,$3,$4,$5,$6,$4,$7,$8,$8) ON CONFLICT(tenant_id,workspace_id,client_id) WHERE client_id IS NOT NULL DO UPDATE SET project_key=EXCLUDED.project_key,ended_at=EXCLUDED.ended_at,summary=EXCLUDED.summary,updated_at=EXCLUDED.updated_at,updated_by=EXCLUDED.updated_by WHERE EXCLUDED.updated_at > sessions.updated_at`, workspace, value.SyncID, value.Project, value.StartedAt, value.EndedAt, value.Summary, updated, actor)
			if err != nil {
				return fmt.Errorf("postgres sync session %q: %w", value.SyncID, err)
			}
			accepted++
		}
		for _, value := range batch.Observations {
			if value.SyncID == "" || value.SessionSyncID == "" || value.Title == "" || value.Content == "" {
				return domain.ErrInvalidInput
			}
			tags, _ := json.Marshal(value.Tags)
			_, err := tx.Exec(ctx, `INSERT INTO observations(tenant_id,session_id,client_id,project_key,scope,classification,source,type,title,content,topic_key,confidence,tags,owner_subject,created_at,updated_at,created_by,updated_by) VALUES(public.cortex_current_tenant(),(SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND client_id=$1),$2,$3,COALESCE(NULLIF($4,''),'project'),COALESCE(NULLIF($4,''),'project'),COALESCE(NULLIF($5,''),'manual'),$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15,$15) ON CONFLICT(tenant_id,client_id) WHERE client_id IS NOT NULL DO UPDATE SET project_key=EXCLUDED.project_key,scope=EXCLUDED.scope,classification=EXCLUDED.classification,source=EXCLUDED.source,type=EXCLUDED.type,title=EXCLUDED.title,content=EXCLUDED.content,topic_key=EXCLUDED.topic_key,confidence=EXCLUDED.confidence,tags=EXCLUDED.tags,updated_at=EXCLUDED.updated_at,updated_by=EXCLUDED.updated_by WHERE EXCLUDED.updated_at > observations.updated_at`, value.SessionSyncID, value.SyncID, value.Project, value.Scope, value.Source, value.Type, value.Title, value.Content, value.TopicKey, value.Confidence, tags, s.store.principal.Subject, value.CreatedAt, value.UpdatedAt, actor)
			if err != nil {
				return fmt.Errorf("postgres sync observation %q: %w", value.SyncID, err)
			}
			if value.Deleted {
				_, err = tx.Exec(ctx, `UPDATE observations SET deleted_at=$1,updated_at=$1 WHERE tenant_id=public.cortex_current_tenant() AND client_id=$2 AND (deleted_at IS NULL OR deleted_at<$1)`, value.UpdatedAt, value.SyncID)
			}
			if err != nil {
				return err
			}
			accepted++
		}
		for _, value := range batch.Prompts {
			if value.SyncID == "" || value.SessionSyncID == "" || value.Content == "" {
				return domain.ErrInvalidInput
			}
			_, err := tx.Exec(ctx, `INSERT INTO prompts(tenant_id,session_id,client_id,project_key,content,created_at,updated_at,created_by,updated_by) VALUES(public.cortex_current_tenant(),(SELECT id FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND client_id=$1),$2,$3,$4,$5,$6,$7,$7) ON CONFLICT(tenant_id,client_id) WHERE client_id IS NOT NULL DO UPDATE SET content=EXCLUDED.content,project_key=EXCLUDED.project_key,updated_at=EXCLUDED.updated_at,updated_by=EXCLUDED.updated_by WHERE EXCLUDED.updated_at > prompts.updated_at`, value.SessionSyncID, value.SyncID, value.Project, value.Content, value.CreatedAt, value.UpdatedAt, actor)
			if err != nil {
				return fmt.Errorf("postgres sync prompt %q: %w", value.SyncID, err)
			}
			accepted++
		}
		for _, value := range batch.Edges {
			if value.SyncID == "" || value.FromSyncID == "" || value.ToSyncID == "" || value.Relation == "" {
				return domain.ErrInvalidInput
			}
			_, err := tx.Exec(ctx, `INSERT INTO edges(tenant_id,client_id,from_observation_id,to_observation_id,relation_type,weight,confidence,source,reasoning,valid_from,valid_until,created_at,updated_at,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,(SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND client_id=$2),(SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND client_id=$3),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13) ON CONFLICT(tenant_id,client_id) WHERE client_id IS NOT NULL DO UPDATE SET relation_type=EXCLUDED.relation_type,weight=EXCLUDED.weight,confidence=EXCLUDED.confidence,source=EXCLUDED.source,reasoning=EXCLUDED.reasoning,valid_from=EXCLUDED.valid_from,valid_until=EXCLUDED.valid_until,updated_at=EXCLUDED.updated_at,updated_by=EXCLUDED.updated_by WHERE EXCLUDED.updated_at > edges.updated_at`, value.SyncID, value.FromSyncID, value.ToSyncID, value.Relation, value.Weight, value.Confidence, value.Source, value.Reasoning, value.ValidFrom, value.ValidUntil, value.CreatedAt, value.UpdatedAt, actor)
			if err != nil {
				return fmt.Errorf("postgres sync edge %q: %w", value.SyncID, err)
			}
			if value.Deleted {
				_, err = tx.Exec(ctx, `UPDATE edges SET deleted_at=$1,updated_at=$1 WHERE tenant_id=public.cortex_current_tenant() AND client_id=$2 AND (deleted_at IS NULL OR deleted_at<$1)`, value.UpdatedAt, value.SyncID)
			}
			if err != nil {
				return err
			}
			accepted++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &domain.SyncResult{Accepted: accepted}, nil
}

type syncChange struct {
	sequence       int64
	kind, publicID string
	syncID         string
	deleted        bool
}

func (s *AuthorizedStore) PullSync(ctx context.Context, cursor int64, limit int) (*domain.SyncPage, error) {
	if cursor < 0 {
		return nil, domain.ErrInvalidInput
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	page := &domain.SyncPage{Cursor: cursor}
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sequence,entity_type,public_id::text,sync_id,deleted FROM sync_changes WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=(SELECT id FROM workspaces WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid) AND sequence>$2 ORDER BY sequence LIMIT $3`, s.store.tenant.WorkspaceID, cursor, limit+1)
		if err != nil {
			return err
		}
		var changes []syncChange
		for rows.Next() {
			var c syncChange
			if err := rows.Scan(&c.sequence, &c.kind, &c.publicID, &c.syncID, &c.deleted); err != nil {
				rows.Close()
				return err
			}
			changes = append(changes, c)
		}
		rows.Close()
		if len(changes) > limit {
			page.HasMore = true
			changes = changes[:limit]
		}
		for _, c := range changes {
			page.Cursor = c.sequence
			switch c.kind {
			case "sessions":
				var v domain.SyncSession
				var updated time.Time
				err = tx.QueryRow(ctx, `SELECT COALESCE(client_id,public_id::text),COALESCE(project_key,''),started_at,ended_at,COALESCE(summary,''),updated_at FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid`, c.publicID).Scan(&v.SyncID, &v.Project, &v.StartedAt, &v.EndedAt, &v.Summary, &updated)
				v.UpdatedAt = updated
				if err == nil {
					page.Sessions = append(page.Sessions, v)
				}
			case "observations":
				var v domain.SyncObservation
				var tags []byte
				var deletedAt *time.Time
				err = tx.QueryRow(ctx, `SELECT COALESCE(o.client_id,o.public_id::text),COALESCE(s.client_id,s.public_id::text),o.title,o.content,o.type,COALESCE(o.project_key,''),COALESCE(o.scope,''),COALESCE(o.topic_key,''),o.confidence,COALESCE(o.source,''),o.tags,o.created_at,o.updated_at,o.deleted_at FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id WHERE o.tenant_id=public.cortex_current_tenant() AND o.public_id=$1::uuid`, c.publicID).Scan(&v.SyncID, &v.SessionSyncID, &v.Title, &v.Content, &v.Type, &v.Project, &v.Scope, &v.TopicKey, &v.Confidence, &v.Source, &tags, &v.CreatedAt, &v.UpdatedAt, &deletedAt)
				_ = json.Unmarshal(tags, &v.Tags)
				v.Deleted = deletedAt != nil
				if err == nil {
					page.Observations = append(page.Observations, v)
				}
			case "prompts":
				var v domain.SyncPrompt
				err = tx.QueryRow(ctx, `SELECT COALESCE(p.client_id,p.public_id::text),COALESCE(s.client_id,s.public_id::text),p.content,COALESCE(p.project_key,''),p.created_at,p.updated_at FROM prompts p JOIN sessions s ON s.tenant_id=p.tenant_id AND s.id=p.session_id WHERE p.tenant_id=public.cortex_current_tenant() AND p.public_id=$1::uuid`, c.publicID).Scan(&v.SyncID, &v.SessionSyncID, &v.Content, &v.Project, &v.CreatedAt, &v.UpdatedAt)
				if err == nil {
					page.Prompts = append(page.Prompts, v)
				}
			case "edges":
				var v domain.SyncEdge
				var deletedAt *time.Time
				err = tx.QueryRow(ctx, `SELECT COALESCE(e.client_id,e.public_id::text),COALESCE(a.client_id,a.public_id::text),COALESCE(b.client_id,b.public_id::text),e.relation_type,e.weight,e.confidence,COALESCE(e.source,''),COALESCE(e.reasoning,''),e.valid_from,e.valid_until,e.created_at,e.updated_at,e.deleted_at FROM edges e JOIN observations a ON a.tenant_id=e.tenant_id AND a.id=e.from_observation_id JOIN observations b ON b.tenant_id=e.tenant_id AND b.id=e.to_observation_id WHERE e.tenant_id=public.cortex_current_tenant() AND e.public_id=$1::uuid`, c.publicID).Scan(&v.SyncID, &v.FromSyncID, &v.ToSyncID, &v.Relation, &v.Weight, &v.Confidence, &v.Source, &v.Reasoning, &v.ValidFrom, &v.ValidUntil, &v.CreatedAt, &v.UpdatedAt, &deletedAt)
				v.Deleted = deletedAt != nil
				if err == nil {
					page.Edges = append(page.Edges, v)
				}
			}
			if errors.Is(err, pgx.ErrNoRows) && c.deleted {
				switch c.kind {
				case "observations":
					page.Observations = append(page.Observations, domain.SyncObservation{SyncID: c.syncID, Deleted: true})
				case "prompts":
					page.Prompts = append(page.Prompts, domain.SyncPrompt{SyncID: c.syncID, Deleted: true})
				case "edges":
					page.Edges = append(page.Edges, domain.SyncEdge{SyncID: c.syncID, Deleted: true})
				}
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		return nil
	})
	return page, err
}
