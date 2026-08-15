package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	servermigrations "github.com/lleontor705/cortex/migrations/v2"
)

// PostgresServerMigration is the isolated server-wave migration. It must not
// be registered with the SQLite migrator: the SQL uses PostgreSQL-only DDL.
type PostgresServerMigration struct {
	version  int
	name     string
	sql      string
	checksum string
	// maxKnownVersion is the head of the migration line this runtime knows
	// (set by the constructors). Apply refuses ledgers recording versions
	// beyond it: such databases were created by a NEWER runtime.
	maxKnownVersion int
}

// NewPostgresServerMigration loads the embedded, checksummed server baseline
// (version 100). It carries the full runtime head so a standalone Apply also
// refuses databases ledgered by a newer runtime.
func NewPostgresServerMigration() (*PostgresServerMigration, error) {
	all, err := NewPostgresServerMigrations()
	if err != nil {
		return nil, err
	}
	return all[0], nil
}

func normalizeLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// NewPostgresServerMigrations returns every immutable server migration in
// application order. Existing databases apply only versions missing from the
// ledger; checksum mismatches and ledgered versions beyond the runtime head
// fail closed.
func NewPostgresServerMigrations() ([]*PostgresServerMigration, error) {
	if servermigrations.ServerSQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL server SQL is empty")
	}
	sql100 := normalizeLF(servermigrations.ServerSQL)
	sum := sha256.Sum256([]byte(sql100))
	baseline := &PostgresServerMigration{
		version:  100,
		name:     "server",
		sql:      sql100,
		checksum: hex.EncodeToString(sum[:]),
	}
	if servermigrations.ServerIdentityGraphSQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL identity/graph SQL is empty")
	}
	sql101 := normalizeLF(servermigrations.ServerIdentityGraphSQL)
	sum = sha256.Sum256([]byte(sql101))
	identityGraph := &PostgresServerMigration{
		version:  101,
		name:     "identity_graph",
		sql:      sql101,
		checksum: hex.EncodeToString(sum[:]),
	}
	if servermigrations.ServerSyncSQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL sync SQL is empty")
	}
	sql102 := normalizeLF(servermigrations.ServerSyncSQL)
	sum = sha256.Sum256([]byte(sql102))
	syncMigration := &PostgresServerMigration{
		version:  102,
		name:     "sync",
		sql:      sql102,
		checksum: hex.EncodeToString(sum[:]),
	}
	if servermigrations.ServerSyncIdentitySQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL sync identity SQL is empty")
	}
	sql103 := normalizeLF(servermigrations.ServerSyncIdentitySQL)
	sum = sha256.Sum256([]byte(sql103))
	syncIdentityChecksum := hex.EncodeToString(sum[:])
	if servermigrations.ServerHandoffReceiptsSQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL handoff receipts SQL is empty")
	}
	sql104 := normalizeLF(servermigrations.ServerHandoffReceiptsSQL)
	sum104 := sha256.Sum256([]byte(sql104))
	if servermigrations.ServerWorkspaceBindingSQL == "" {
		return nil, errors.New("migration: embedded PostgreSQL workspace binding SQL is empty")
	}
	sql105 := normalizeLF(servermigrations.ServerWorkspaceBindingSQL)
	sum105 := sha256.Sum256([]byte(sql105))
	migrations := []*PostgresServerMigration{baseline, identityGraph, syncMigration, {
		version:  103,
		name:     "sync_identity",
		sql:      sql103,
		checksum: syncIdentityChecksum,
	}, {
		version:  104,
		name:     "handoff_receipts",
		sql:      sql104,
		checksum: hex.EncodeToString(sum104[:]),
	}, {
		version:  105,
		name:     "workspace_binding",
		sql:      sql105,
		checksum: hex.EncodeToString(sum105[:]),
	}}
	// Every migration carries the runtime head so any single Apply refuses
	// databases ledgered by a newer runtime (ErrFutureMigration).
	head := 0
	for _, migration := range migrations {
		if migration.version > head {
			head = migration.version
		}
	}
	for _, migration := range migrations {
		migration.maxKnownVersion = head
	}
	return migrations, nil
}

func ApplyPostgresServerMigrations(ctx context.Context, db *sql.DB) error {
	migrations, err := NewPostgresServerMigrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if err := migration.Apply(ctx, db); err != nil {
			return err
		}
	}
	return nil
}

func (m *PostgresServerMigration) Version() int     { return m.version }
func (m *PostgresServerMigration) Name() string     { return m.name }
func (m *PostgresServerMigration) SQL() string      { return m.sql }
func (m *PostgresServerMigration) Checksum() string { return m.checksum }

// MatchesChecksum checks if the recorded checksum matches the migration checksum,
// including cross-platform line ending normalization (LF vs CRLF).
func (m *PostgresServerMigration) MatchesChecksum(recorded string) bool {
	if recorded == m.checksum {
		return true
	}
	lf := strings.ReplaceAll(m.sql, "\r\n", "\n")
	sumLF := sha256.Sum256([]byte(lf))
	if recorded == hex.EncodeToString(sumLF[:]) {
		return true
	}
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	sumCRLF := sha256.Sum256([]byte(crlf))
	return recorded == hex.EncodeToString(sumCRLF[:])
}

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
	// Future-version guard: a ledger recording a version beyond this
	// runtime's head means the database was created by a NEWER runtime.
	// Applying below that head would silently fork the line: fail closed
	// before reading or writing any migration row (REM-ROLLOUT-001).
	head := m.maxKnownVersion
	if head < m.version {
		head = m.version
	}
	var future sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT max(version) FROM cortex_server_migrations WHERE version > $1`, head,
	).Scan(&future); err != nil {
		return fmt.Errorf("migration: read PostgreSQL migration ledger: %w", err)
	}
	if future.Valid {
		return fmt.Errorf("%w: PostgreSQL ledger records migration %d beyond runtime head %d",
			ErrFutureMigration, future.Int64, head)
	}
	var checksum string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM cortex_server_migrations WHERE version = $1`, m.Version()).Scan(&checksum)
	switch {
	case err == nil && !m.MatchesChecksum(checksum):
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

// Down is the forward-only guard for the PostgreSQL server migration line.
// For EVERY version — 100 through 105, ledgered or unledgered — it returns an
// ErrForwardOnly-wrapped error and executes NO DDL/DML: no transaction, no
// query; schema, data, and the migration ledger remain untouched. There is no
// artifact-cleanup exception: stale unledgered artifacts and newer-runtime
// ledgers are handled by reviewed compensating migrations, never by
// destructive rollback (REM-MIG-001, R1F review). The behavioral matrix with
// real schema/ledger/data snapshots runs in postgres_integration.
func (m *PostgresServerMigration) Down(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("migration: PostgreSQL database connection is nil")
	}
	return fmt.Errorf("%w: PostgreSQL server migration %d cannot be rolled back", ErrForwardOnly, m.version)
}
