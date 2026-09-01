package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/server/external"
)

// PostgresReindexSource is a read-only view of one project corpus. Its
// internal project key is resolved once from a granted public UUID inside the
// principal-bound transaction and is never accepted from CLI input.
type PostgresReindexSource struct {
	store             *AuthorizedStore
	scope             external.ReindexScope
	internalProjectID int64
	canonicalLabel    string
}

func NewPostgresReindexSource(ctx context.Context, store *AuthorizedStore, projectPublicID string) (*PostgresReindexSource, error) {
	if store == nil || store.store == nil {
		return nil, ErrAuthorizedStoreRequired
	}
	parsed, err := uuid.Parse(strings.TrimSpace(projectPublicID))
	if err != nil || parsed == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	projectPublicID = parsed.String()
	if err := store.authorize(ctx, authz.ResourceAdmin, authz.ActionManage, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	source := &PostgresReindexSource{store: store, scope: external.ReindexScope{
		TenantID: store.store.tenant.TenantID, WorkspaceID: store.store.tenant.WorkspaceID,
		ProjectID: projectPublicID,
	}}
	err = store.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT id,name FROM projects WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND public_id=$2::uuid`, workspace, projectPublicID).Scan(&source.internalProjectID, &source.canonicalLabel)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if source.internalProjectID <= 0 || strings.TrimSpace(source.canonicalLabel) == "" {
		return nil, errors.New("postgres reindex: invalid durable project binding")
	}
	return source, nil
}

func (s *PostgresReindexSource) ReindexScope() external.ReindexScope { return s.scope }
func (s *PostgresReindexSource) CanonicalProjectLabel() string       { return s.canonicalLabel }

func (s *PostgresReindexSource) validateScope(scope external.ReindexScope) error {
	if s == nil || s.store == nil || scope != s.scope {
		return errors.New(authz.DenyProject)
	}
	return nil
}

func (s *PostgresReindexSource) DescribeCorpus(ctx context.Context, scope external.ReindexScope) (external.ReindexCorpusDescriptor, error) {
	if err := s.validateScope(scope); err != nil {
		return external.ReindexCorpusDescriptor{}, err
	}
	descriptor := external.ReindexCorpusDescriptor{}
	var maxID int64
	var maxUpdated time.Time
	hash := sha256.New()
	err := s.store.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		statement := `SELECT o.id,o.public_id::text,o.type,o.title,o.content,COALESCE(o.topic_key,''),COALESCE(o.scope,''),COALESCE(o.source,''),COALESCE(o.owner_subject,''),COALESCE(o.classification,''),o.created_at,o.updated_at FROM observations o WHERE o.tenant_id=public.cortex_current_tenant() AND o.workspace_id=$1 AND o.project_id=$2 AND o.deleted_at IS NULL`
		args := []any{workspace, s.internalProjectID}
		statement, args = s.store.store.appendObservationVisibilityPredicate(statement, args, true)
		statement += ` ORDER BY o.id ASC`
		rows, err := tx.Query(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		encoder := json.NewEncoder(hash)
		for rows.Next() {
			var row struct {
				ID             int64     `json:"id"`
				PublicID       string    `json:"public_id"`
				Type           string    `json:"type"`
				Title          string    `json:"title"`
				Content        string    `json:"content"`
				TopicKey       string    `json:"topic_key"`
				Scope          string    `json:"scope"`
				Source         string    `json:"source"`
				Owner          string    `json:"owner"`
				Classification string    `json:"classification"`
				CreatedAt      time.Time `json:"created_at"`
				UpdatedAt      time.Time `json:"updated_at"`
			}
			if err := rows.Scan(&row.ID, &row.PublicID, &row.Type, &row.Title, &row.Content, &row.TopicKey, &row.Scope, &row.Source, &row.Owner, &row.Classification, &row.CreatedAt, &row.UpdatedAt); err != nil {
				return err
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
			descriptor.Count++
			if row.ID > maxID {
				maxID = row.ID
			}
			if row.UpdatedAt.After(maxUpdated) {
				maxUpdated = row.UpdatedAt
			}
		}
		return rows.Err()
	})
	if err != nil {
		return external.ReindexCorpusDescriptor{}, err
	}
	descriptor.Generation = fmt.Sprintf("%d:%d:%d", descriptor.Count, maxID, maxUpdated.UTC().UnixNano())
	descriptor.Checksum = hex.EncodeToString(hash.Sum(nil))
	return descriptor, nil
}

func (s *PostgresReindexSource) List(ctx context.Context, scope external.ReindexScope, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	if err := s.validateScope(scope); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 || filter.Limit > 1000 || filter.Offset < 0 || !filter.OrderAsc || filter.IncludeArchived || filter.Project != "" {
		return nil, domain.ErrInvalidInput
	}
	items := make([]*domain.Observation, 0, filter.Limit)
	err := s.store.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		statement := `
			SELECT o.public_id::text,o.id,se.public_id::text,$3::text,
			       COALESCE(o.scope,''),COALESCE(o.source,''),o.type,o.title,o.content,
			       COALESCE(o.topic_key,''),o.created_at,o.updated_at,COALESCE(o.owner_subject,'')
			  FROM observations o
			  JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id AND se.workspace_id=o.workspace_id
			 WHERE o.tenant_id=public.cortex_current_tenant() AND o.workspace_id=$1
			   AND o.project_id=$2 AND o.deleted_at IS NULL`
		args := []any{workspace, s.internalProjectID, s.canonicalLabel}
		statement, args = s.store.store.appendObservationVisibilityPredicate(statement, args, true)
		statement += fmt.Sprintf(" ORDER BY o.id ASC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, filter.Limit, filter.Offset)
		rows, err := tx.Query(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o := new(domain.Observation)
			if err := rows.Scan(&o.PublicID, &o.ID, &o.SessionID, &o.Project, &o.Scope, &o.Source, &o.Type, &o.Title, &o.Content, &o.TopicKey, &o.CreatedAt, &o.UpdatedAt, &o.OwnerSubject); err != nil {
				return err
			}
			items = append(items, o)
		}
		return rows.Err()
	})
	return items, err
}

func (s *PostgresReindexSource) Scope(ctx context.Context, observationID int64) (external.ReindexScope, error) {
	if s == nil || s.store == nil || observationID <= 0 {
		return external.ReindexScope{}, domain.ErrNotFound
	}
	found := false
	err := s.store.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		statement := `SELECT EXISTS(SELECT 1 FROM observations o WHERE o.tenant_id=public.cortex_current_tenant() AND o.workspace_id=$1 AND o.project_id=$2 AND o.id=$3 AND o.deleted_at IS NULL`
		args := []any{workspace, s.internalProjectID, observationID}
		statement, args = s.store.store.appendObservationVisibilityPredicate(statement, args, true)
		statement += `)`
		return tx.QueryRow(ctx, statement, args...).Scan(&found)
	})
	if err != nil {
		return external.ReindexScope{}, err
	}
	if !found {
		return external.ReindexScope{}, domain.ErrNotFound
	}
	return s.scope, nil
}

func (s *PostgresReindexSource) GetEmbedding(ctx context.Context, scope external.ReindexScope, observationID int64) ([]float32, string, error) {
	if err := s.validateScope(scope); err != nil {
		return nil, "", err
	}
	if _, err := s.Scope(ctx, observationID); err != nil {
		return nil, "", fmt.Errorf("postgres reindex embedding scope: %w", err)
	}
	// PostgreSQL observations are authoritative text; the external vector
	// adapter is the replica. There is deliberately no second embedding table
	// to trust, so the caller regenerates the vector through the configured
	// hardened provider.
	return nil, "", domain.ErrNotFound
}

var _ external.ReindexSource = (*PostgresReindexSource)(nil)
