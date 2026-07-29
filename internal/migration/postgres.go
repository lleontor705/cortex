package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	servermigrations "github.com/lleontor705/cortex/migrations/v2"
)

// PostgresServerMigration is the isolated server-wave migration. It must not
// be registered with the SQLite migrator: the SQL uses PostgreSQL-only DDL.
type PostgresServerMigration struct {
	sql      string
	checksum string
}

// NewPostgresServerMigration loads the embedded, checksummed server schema.
func NewPostgresServerMigration() (*PostgresServerMigration, error) {
	if servermigrations.ServerSQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL server SQL is empty")
	}
	sum := sha256.Sum256([]byte(servermigrations.ServerSQL))
	return &PostgresServerMigration{
		sql:      servermigrations.ServerSQL,
		checksum: hex.EncodeToString(sum[:]),
	}, nil
}

func (m *PostgresServerMigration) Version() int     { return 100 }
func (m *PostgresServerMigration) Name() string     { return "server" }
func (m *PostgresServerMigration) SQL() string      { return m.sql }
func (m *PostgresServerMigration) Checksum() string { return m.checksum }

// Apply runs the server migration atomically. A migration record stores the
// exact embedded checksum, so changing an applied migration fails closed.
// The transaction-scoped advisory lock is safe with transaction poolers.
func (m *PostgresServerMigration) Apply(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("migration: PostgreSQL database connection is nil")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration: begin PostgreSQL transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('cortex:v2:server-migrations'))`); err != nil {
		return fmt.Errorf("migration: lock PostgreSQL server migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS cortex_server_migrations (
		version integer PRIMARY KEY,
		name text NOT NULL,
		checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migration: create PostgreSQL migration ledger: %w", err)
	}
	var checksum string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM cortex_server_migrations WHERE version = $1`, m.Version()).Scan(&checksum)
	switch {
	case err == nil && checksum != m.Checksum():
		return fmt.Errorf("migration: PostgreSQL migration %d checksum mismatch", m.Version())
	case err == nil:
		return tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("migration: read PostgreSQL migration ledger: %w", err)
	}
	if _, err := tx.ExecContext(ctx, m.SQL()); err != nil {
		return fmt.Errorf("migration: apply PostgreSQL server schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cortex_server_migrations (version, name, checksum) VALUES ($1, $2, $3)`, m.Version(), m.Name(), m.Checksum()); err != nil {
		return fmt.Errorf("migration: record PostgreSQL server migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration: commit PostgreSQL server migration: %w", err)
	}
	return nil
}
