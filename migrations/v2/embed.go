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

// ServerIdentityGraphSQL adds request-scoped principals and closes the graph
// metadata gap without changing the immutable version 100 migration.
//
//go:embed 101_identity_graph.sql
var ServerIdentityGraphSQL string

// ServerSyncSQL adds idempotency keys and the incremental replication feed.
//
//go:embed 102_sync.sql
var ServerSyncSQL string

// ServerSyncIdentitySQL canonicalizes server-native sync identities and
// backfills the initial change feed without modifying migration 102.
//
//go:embed 103_sync_identity.sql
var ServerSyncIdentitySQL string
