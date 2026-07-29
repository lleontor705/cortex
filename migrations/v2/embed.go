// Package v2 embeds the Cortex v2 baseline SQL migration bundle.
//
// The embedded SQL is the complete forward-only schema baseline for Cortex
// 2.0.0 (REQ-DB-001). It is applied atomically inside a single transaction
// by the migration runner in internal/migration.
package v2

import _ "embed"

// BaselineSQL is the complete v2 baseline schema DDL.
// It consolidates the final-state schema of v1 migrations 001-014 plus the
// v2 corrections (corrected type registry, valid_until, outbox, audit,
// tenant columns, cortex_meta identity).
//
//go:embed 001_init.sql
var BaselineSQL string

// ServerSQL is the PostgreSQL-only server schema migration. It is deliberately
// a separate embed so local SQLite builds never execute or import it.
//
//go:embed 100_server.sql
var ServerSQL string
