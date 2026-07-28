// Package entity implements the SQLite entity link store for Cortex.
package entity

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

const sqliteDatetimeFormat = "2006-01-02 15:04:05"

// Store implements the SQLite entity link store.
type Store struct {
	db *sql.DB
	v2 bool
}

func (s *Store) v2Schema(ctx context.Context) bool {
	return s.v2
}

// NewStore creates a new entity store.
func NewStore(db *sql.DB) *Store {
	var ddl string
	_ = db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='entity_links'`).Scan(&ddl)
	return &Store{db: db, v2: strings.Contains(ddl, "normalized_value")}
}

// SaveLinks stores entity links, ignoring duplicates.
func (s *Store) SaveLinks(ctx context.Context, links []*domain.EntityLink) error {
	if len(links) == 0 {
		return nil
	}

	v2 := s.v2Schema(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("entity: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `INSERT OR IGNORE INTO entity_links (observation_id, entity_type, entity_value) VALUES (?, ?, ?)`
	if v2 {
		query = `INSERT OR IGNORE INTO entity_links (observation_id, entity_type, entity_value, normalized_value, provenance) VALUES (?, ?, ?, ?, ?)`
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("entity: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, link := range links {
		if link == nil {
			continue
		}
		if link.NormalizedValue == "" {
			link.NormalizedValue = canonicalKey(link.EntityType, link.EntityValue)
		}
		if link.Provenance == "" {
			link.Provenance = "deterministic"
		}
		var execErr error
		if v2 {
			execErr = func() error {
				_, e := stmt.ExecContext(ctx, link.ObservationID, link.EntityType, link.EntityValue, link.NormalizedValue, link.Provenance)
				return e
			}()
		} else {
			_, execErr = stmt.ExecContext(ctx, link.ObservationID, link.EntityType, link.EntityValue)
		}
		if execErr != nil {
			return fmt.Errorf("entity: save link: %w", execErr)
		}
	}

	return tx.Commit()
}

func canonicalKey(kind, value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("/:@._-", r) {
			b.WriteRune(r)
		}
	}
	return kind + ":" + b.String()
}

// GetByObservation retrieves all entity links for an observation.
func (s *Store) GetByObservation(ctx context.Context, obsID int64) ([]*domain.EntityLink, error) {
	query := `SELECT id, observation_id, entity_type, entity_value, created_at FROM entity_links WHERE observation_id = ? ORDER BY entity_type, entity_value`
	if s.v2Schema(ctx) {
		query = `SELECT id, observation_id, entity_type, entity_value, normalized_value, provenance, created_at FROM entity_links WHERE observation_id = ? ORDER BY entity_type, normalized_value`
	}
	rows, err := s.db.QueryContext(ctx, query, obsID)
	if err != nil {
		return nil, fmt.Errorf("entity: get by observation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntityLinks(rows, s.v2Schema(ctx))
}

// FindByEntity retrieves entity links matching a type and value.
func (s *Store) FindByEntity(ctx context.Context, entityType, entityValue string) ([]*domain.EntityLink, error) {
	v2 := s.v2Schema(ctx)
	query := `SELECT id, observation_id, entity_type, entity_value, created_at FROM entity_links WHERE 1=1`
	if v2 {
		query = `SELECT id, observation_id, entity_type, entity_value, normalized_value, provenance, created_at FROM entity_links WHERE 1=1`
	}
	args := []interface{}{}

	if entityType != "" {
		query += " AND entity_type = ?"
		args = append(args, entityType)
	}

	if entityValue != "" {
		if strings.Contains(entityValue, "%") {
			query += " AND entity_value LIKE ?"
		} else {
			if v2 {
				query += " AND normalized_value = ?"
			} else {
				query += " AND entity_value = ?"
			}
		}
		if v2 && !strings.Contains(entityValue, "%") {
			args = append(args, canonicalKey(entityType, entityValue))
		} else {
			args = append(args, entityValue)
		}
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("entity: find by entity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntityLinks(rows, v2)
}

// DeleteByObservation removes all entity links for an observation.
func (s *Store) DeleteByObservation(ctx context.Context, obsID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM entity_links WHERE observation_id = ?`, obsID)
	if err != nil {
		return fmt.Errorf("entity: delete by observation: %w", err)
	}
	return nil
}

func scanEntityLinks(rows *sql.Rows, v2 ...bool) ([]*domain.EntityLink, error) {
	var links []*domain.EntityLink
	for rows.Next() {
		link := &domain.EntityLink{}
		var createdAt string
		var normalized, provenance sql.NullString
		var err error
		if len(v2) > 0 && v2[0] {
			err = rows.Scan(&link.ID, &link.ObservationID, &link.EntityType, &link.EntityValue, &normalized, &provenance, &createdAt)
			link.NormalizedValue = normalized.String
			link.Provenance = provenance.String
		} else {
			err = rows.Scan(&link.ID, &link.ObservationID, &link.EntityType, &link.EntityValue, &createdAt)
		}
		if err != nil {
			return nil, err
		}
		if t, err := time.Parse(sqliteDatetimeFormat, createdAt); err == nil {
			link.CreatedAt = t
		} else if createdAt != "" {
			log.Printf("entity: failed to parse time %q", createdAt)
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// Ensure Store implements domain.EntityRepository.
var _ domain.EntityRepository = (*Store)(nil)
