// Package entity implements the SQLite entity link store for Cortex.
package entity

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

const sqliteDatetimeFormat = "2006-01-02 15:04:05"

// Store implements the SQLite entity link store.
type Store struct {
	db *sql.DB
}

// NewStore creates a new entity store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// SaveLinks stores entity links, ignoring duplicates.
func (s *Store) SaveLinks(ctx context.Context, links []*domain.EntityLink) error {
	if len(links) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("entity: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO entity_links (observation_id, entity_type, entity_value)
		 VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("entity: prepare: %w", err)
	}
	defer stmt.Close()

	for _, link := range links {
		if _, err := stmt.ExecContext(ctx, link.ObservationID, link.EntityType, link.EntityValue); err != nil {
			return fmt.Errorf("entity: save link: %w", err)
		}
	}

	return tx.Commit()
}

// GetByObservation retrieves all entity links for an observation.
func (s *Store) GetByObservation(ctx context.Context, obsID int64) ([]*domain.EntityLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, observation_id, entity_type, entity_value, created_at
		 FROM entity_links WHERE observation_id = ? ORDER BY entity_type, entity_value`,
		obsID,
	)
	if err != nil {
		return nil, fmt.Errorf("entity: get by observation: %w", err)
	}
	defer rows.Close()
	return scanEntityLinks(rows)
}

// FindByEntity retrieves entity links matching a type and value.
func (s *Store) FindByEntity(ctx context.Context, entityType, entityValue string) ([]*domain.EntityLink, error) {
	query := `SELECT id, observation_id, entity_type, entity_value, created_at FROM entity_links WHERE 1=1`
	args := []interface{}{}

	if entityType != "" {
		query += " AND entity_type = ?"
		args = append(args, entityType)
	}

	if entityValue != "" {
		if strings.Contains(entityValue, "%") {
			query += " AND entity_value LIKE ?"
		} else {
			query += " AND entity_value = ?"
		}
		args = append(args, entityValue)
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("entity: find by entity: %w", err)
	}
	defer rows.Close()
	return scanEntityLinks(rows)
}

// DeleteByObservation removes all entity links for an observation.
func (s *Store) DeleteByObservation(ctx context.Context, obsID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM entity_links WHERE observation_id = ?`, obsID)
	if err != nil {
		return fmt.Errorf("entity: delete by observation: %w", err)
	}
	return nil
}

func scanEntityLinks(rows *sql.Rows) ([]*domain.EntityLink, error) {
	var links []*domain.EntityLink
	for rows.Next() {
		link := &domain.EntityLink{}
		var createdAt string
		if err := rows.Scan(&link.ID, &link.ObservationID, &link.EntityType, &link.EntityValue, &createdAt); err != nil {
			return nil, err
		}
		link.CreatedAt, _ = time.Parse(sqliteDatetimeFormat, createdAt)
		links = append(links, link)
	}
	return links, rows.Err()
}

// Ensure Store implements domain.EntityRepository.
var _ domain.EntityRepository = (*Store)(nil)
