//go:build cortex_vectors

// Package sqlite_blob: full adapter conformance suite (W8.4).
//
// Under the cortex_vectors build tag, the underlying sqlite.VectorStore is the
// full O(N) cosine BLOB scan. This file wires the SHARED conformance.RunSuite
// to sqlite_blob so the cross-adapter parity assertion covers 3/3 adapters
// (sqlite_blob, qdrant, pgvector) — closing the gap identified in the W8.4
// verify report.
//
// Each RunSuite sub-test constructs a FRESH adapter backed by an ISOLATED
// in-memory database with the production schema (sessions, observations,
// observation_vectors) and seeded observations for the fixture point IDs.
// The database is cleaned up via t.Cleanup when the test completes.
//
// Run with:
//
//	go test -tags cortex_vectors ./internal/vector/sqlite_blob/ -v -count=1
package sqlite_blob

import (
	"context"
	"fmt"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/vector/conformance"
	"github.com/lleontor705/cortex/testutil"
)

// conformanceSchemaSQL creates the minimal production-compatible schema needed
// for the vector store to round-trip: sessions + observations (with FK) +
// observation_vectors (with FK to observations). The column names match the
// production migrations (001, 005) so SearchByVector's JOIN scan works.
const conformanceSchemaSQL = `
CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	project    TEXT NOT NULL,
	directory  TEXT NOT NULL,
	started_at TEXT NOT NULL DEFAULT (datetime('now')),
	ended_at   TEXT,
	summary    TEXT
);

CREATE TABLE IF NOT EXISTS observations (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT    NOT NULL,
	type       TEXT    NOT NULL,
	title      TEXT    NOT NULL,
	content    TEXT    NOT NULL,
	project    TEXT,
	scope      TEXT    NOT NULL DEFAULT 'project',
	topic_key  TEXT,
	created_at TEXT    NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
	deleted_at TEXT,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE TABLE IF NOT EXISTS observation_vectors (
	observation_id  INTEGER PRIMARY KEY,
	embedding       BLOB,
	embedding_model TEXT,
	dimensions      INTEGER,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
);
`

// newConformanceDB creates a fresh, ISOLATED in-memory database with the
// production-compatible schema and seeded observations for the fixture point
// IDs (1-5). The SearchByVector query JOINs observation_vectors → observations,
// so the observations MUST exist for search results to surface.
//
// The database is cleaned up via t.Cleanup registered by testutil.NewTestDB.
func newConformanceDB(t *testing.T) *testutil.TestDB {
	t.Helper()
	db := testutil.NewTestDB(t)

	// Apply schema.
	db.MustExec(conformanceSchemaSQL)

	// Seed a session (observations FK to sessions).
	db.MustExec(`INSERT INTO sessions (id, project, directory) VALUES (?, ?, ?)`,
		"conformance-session", "conformance", "/test")

	// Seed observations with IDs matching the fixture points (1-5).
	// The conformance suite's DefaultFixtures uses IDs 1-5.
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := db.DB().ExecContext(ctx, `
			INSERT INTO observations (id, session_id, type, title, content, project, scope)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, i, "conformance-session", "decision",
			fmt.Sprintf("conformance-obs-%d", i),
			fmt.Sprintf("content for observation %d", i),
			"conformance", "project")
		if err != nil {
			t.Fatalf("seed observation %d: %v", i, err)
		}
	}

	return db
}

// TestSqliteBlob_ConformanceSuite runs the SHARED conformance suite against the
// sqlite_blob adapter backed by an isolated in-memory database (REQ-VEC-002
// parity). This is the cross-adapter parity assertion: identical fixtures must
// produce the same eligible candidate set as qdrant and pgvector.
//
// Each sub-test constructs a FRESH adapter with an ISOLATED database so tests
// are independent and leave no state behind.
func TestSqliteBlob_ConformanceSuite(t *testing.T) {
	conformance.RunSuite(t, func(t *testing.T, _ int, _ domain.ModelInfo) (domain.VectorIndex, error) {
		db := newConformanceDB(t)
		return New(db.DB()), nil
	})
}
