package postgrestest

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const migrationRoleBootstrapLock int64 = 0x434f525445585047

// EnsureMigrationRoles serializes the cluster-global role bootstrap used by
// PostgreSQL integration packages. The session lock covers the check/create
// sequence across databases and processes on the same test cluster.
func EnsureMigrationRoles(ctx context.Context, dsn string) error {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse PostgreSQL bootstrap DSN: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("connect PostgreSQL bootstrap: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationRoleBootstrapLock); err != nil {
		return fmt.Errorf("lock PostgreSQL role bootstrap: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationRoleBootstrapLock)
	}()

	if _, err := conn.Exec(ctx, `DO $bootstrap$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_app') THEN
        CREATE ROLE cortex_app NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_admin') THEN
        CREATE ROLE cortex_admin NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_migration') THEN
        CREATE ROLE cortex_migration NOLOGIN BYPASSRLS;
    END IF;
END
$bootstrap$`); err != nil {
		return fmt.Errorf("ensure PostgreSQL migration roles: %w", err)
	}
	return nil
}
