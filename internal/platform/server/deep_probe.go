package server

// Deep-diagnostic PostgreSQL probe seam for `cortex doctor --deep` (issue
// #158). internal/cli is local composition and MUST NOT import pgx
// (REQ-FOUND-001 arch gate); cmd/cortex is the sole bridge to the server
// composition, so the opener that links the pgx stdlib driver lives HERE and
// is injected into the CLI at process start. Without injection the deep
// storage checks report BLOCKED — honest degradation, never a fake PASS.

import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// DeepProbeDBOpener returns an opener over the registered pgx stdlib driver
// that opens a pool and pings before returning, so connectivity failures
// surface at the probe call site with driver fidelity.
func DeepProbeDBOpener() func(ctx context.Context, dsn string) (*sql.DB, error) {
	return func(ctx context.Context, dsn string) (*sql.DB, error) {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}
}
