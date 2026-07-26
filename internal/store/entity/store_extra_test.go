package entity

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil"
)

// setupEntityTestStore provisions an in-memory SQLite database whose schema
// reproduces migration 006 (entity_links) plus the observations table it
// references via foreign key. The shared database.Manager enables
// "foreign_keys=ON" through its DSN, so entity_links rows must reference real
// observations exactly as they do in production.
func setupEntityTestStore(t *testing.T) (*Store, *sql.DB, func()) {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1,
		Name:    "observations_minimal",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS observations (
				id INTEGER PRIMARY KEY AUTOINCREMENT
			);
		`,
		DownSQL: `DROP TABLE IF EXISTS observations;`,
	})
	registry.Register(migration.Migration{
		Version: 6,
		Name:    "entities",
		UpSQL: `
			CREATE TABLE entity_links (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				observation_id INTEGER NOT NULL,
				entity_type TEXT NOT NULL,
				entity_value TEXT NOT NULL,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (observation_id) REFERENCES observations(id) ON DELETE CASCADE
			);

			CREATE INDEX idx_entity_obs ON entity_links(observation_id);
			CREATE INDEX idx_entity_type ON entity_links(entity_type);
			CREATE INDEX idx_entity_value ON entity_links(entity_value);
			CREATE UNIQUE INDEX idx_entity_unique ON entity_links(observation_id, entity_type, entity_value);
		`,
		DownSQL: `
			DROP INDEX IF EXISTS idx_entity_unique;
			DROP INDEX IF EXISTS idx_entity_value;
			DROP INDEX IF EXISTS idx_entity_type;
			DROP INDEX IF EXISTS idx_entity_obs;
			DROP TABLE IF EXISTS entity_links;
		`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	store := NewStore(testDB.DB())
	return store, testDB.DB(), func() { testDB.Cleanup() }
}

// createObservation inserts a single observation row and returns its ID.
func createObservation(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO observations DEFAULT VALUES`)
	if err != nil {
		t.Fatalf("create observation: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// insertEntityLinkRaw inserts an entity_links row with an explicit created_at,
// bypassing the store so ordering/timestamp parsing contracts can be asserted.
func insertEntityLinkRaw(t *testing.T, db *sql.DB, obsID int64, entityType, entityValue, createdAt string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO entity_links (observation_id, entity_type, entity_value, created_at) VALUES (?, ?, ?, ?)`,
		obsID, entityType, entityValue, createdAt,
	)
	if err != nil {
		t.Fatalf("insert raw entity link: %v", err)
	}
}

// mustLinks is a test helper that fails on any SaveLinks error.
func mustSaveLinks(t *testing.T, store *Store, ctx context.Context, links []*domain.EntityLink) {
	t.Helper()
	if err := store.SaveLinks(ctx, links); err != nil {
		t.Fatalf("SaveLinks: %v", err)
	}
}

func TestSaveLinks(t *testing.T) {
	ctx := context.Background()

	t.Run("empty slice is a no-op", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		if err := store.SaveLinks(ctx, nil); err != nil {
			t.Fatalf("SaveLinks(nil): unexpected error: %v", err)
		}
		if err := store.SaveLinks(ctx, []*domain.EntityLink{}); err != nil {
			t.Fatalf("SaveLinks(empty): unexpected error: %v", err)
		}

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected no links after no-op save, got %d", len(got))
		}
	})

	t.Run("single and multiple links are persisted", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "main.go"},
		})

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 link, got %d", len(got))
		}
		if got[0].EntityType != domain.EntityFile || got[0].EntityValue != "main.go" {
			t.Errorf("unexpected link: %+v", got[0])
		}
		if got[0].ID == 0 {
			t.Errorf("expected non-zero ID")
		}

		obs2 := createObservation(t, db)
		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obs2, EntityType: domain.EntityPackage, EntityValue: "encoding/json"},
			{ObservationID: obs2, EntityType: domain.EntitySymbol, EntityValue: "Marshal"},
			{ObservationID: obs2, EntityType: domain.EntityURL, EntityValue: "https://example.com"},
		})
		got2, err := store.GetByObservation(ctx, obs2)
		if err != nil {
			t.Fatalf("GetByObservation obs2: %v", err)
		}
		if len(got2) != 3 {
			t.Fatalf("expected 3 links for obs2, got %d", len(got2))
		}
	})

	t.Run("duplicate links are ignored", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		link := &domain.EntityLink{ObservationID: obsID, EntityType: domain.EntityConcept, EntityValue: "TDD"}
		mustSaveLinks(t, store, ctx, []*domain.EntityLink{link})

		// Saving the exact same (observation_id, entity_type, entity_value) a
		// second time must not error and must not duplicate the row because of
		// the unique index idx_entity_unique.
		mustSaveLinks(t, store, ctx, []*domain.EntityLink{link})

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected duplicate to be ignored (1 link), got %d", len(got))
		}
	})

	t.Run("cancelled context returns an error", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		err := store.SaveLinks(cancelled, []*domain.EntityLink{
			{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "x.go"},
		})
		if err == nil {
			t.Fatalf("expected error for SaveLinks on cancelled context, got nil")
		}
	})
}

func TestGetByObservation(t *testing.T) {
	ctx := context.Background()

	t.Run("returns links ordered by type then value", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		// Insert deliberately out of order to assert the ORDER BY clause.
		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obsID, EntityType: domain.EntitySymbol, EntityValue: "zeta"},
			{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "b.go"},
			{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "a.go"},
		})

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 links, got %d", len(got))
		}
		// ORDER BY entity_type, entity_value => file/a.go, file/b.go, symbol/zeta
		if got[0].EntityType != domain.EntityFile || got[0].EntityValue != "a.go" {
			t.Errorf("expected file/a.go first, got %+v", got[0])
		}
		if got[1].EntityType != domain.EntityFile || got[1].EntityValue != "b.go" {
			t.Errorf("expected file/b.go second, got %+v", got[1])
		}
		if got[2].EntityType != domain.EntitySymbol || got[2].EntityValue != "zeta" {
			t.Errorf("expected symbol/zeta third, got %+v", got[2])
		}
	})

	t.Run("unknown observation returns empty slice", func(t *testing.T) {
		store, _, cleanup := setupEntityTestStore(t)
		defer cleanup()

		got, err := store.GetByObservation(ctx, 999999)
		if err != nil {
			t.Fatalf("GetByObservation(unknown): unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 links for unknown observation, got %d", len(got))
		}
	})

	t.Run("cancelled context returns an error", func(t *testing.T) {
		store, _, cleanup := setupEntityTestStore(t)
		defer cleanup()

		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := store.GetByObservation(cancelled, 1); err == nil {
			t.Fatalf("expected error for GetByObservation on cancelled context, got nil")
		}
	})
}

func TestFindByEntity(t *testing.T) {
	ctx := context.Background()

	t.Run("filter by type only", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obs1 := createObservation(t, db)
		obs2 := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obs1, EntityType: domain.EntityFile, EntityValue: "a.go"},
			{ObservationID: obs1, EntityType: domain.EntitySymbol, EntityValue: "Func"},
			{ObservationID: obs2, EntityType: domain.EntityFile, EntityValue: "b.go"},
		})

		got, err := store.FindByEntity(ctx, domain.EntityFile, "")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 file links, got %d", len(got))
		}
		for _, l := range got {
			if l.EntityType != domain.EntityFile {
				t.Errorf("expected only file type, got %q", l.EntityType)
			}
		}
	})

	t.Run("filter by exact value only", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obs1 := createObservation(t, db)
		obs2 := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obs1, EntityType: domain.EntityFile, EntityValue: "main.go"},
			{ObservationID: obs2, EntityType: domain.EntitySymbol, EntityValue: "main.go"},
		})

		got, err := store.FindByEntity(ctx, "", "main.go")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 links matching value main.go, got %d", len(got))
		}
	})

	t.Run("filter by type and exact value", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obs1 := createObservation(t, db)
		obs2 := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obs1, EntityType: domain.EntityFile, EntityValue: "main.go"},
			{ObservationID: obs2, EntityType: domain.EntitySymbol, EntityValue: "main.go"},
		})

		got, err := store.FindByEntity(ctx, domain.EntityFile, "main.go")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 file link matching main.go, got %d", len(got))
		}
		if got[0].EntityType != domain.EntityFile {
			t.Errorf("expected file type, got %q", got[0].EntityType)
		}
	})

	t.Run("wildcard value uses LIKE", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obs1 := createObservation(t, db)
		obs2 := createObservation(t, db)
		obs3 := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obs1, EntityType: domain.EntityFile, EntityValue: "internal/store/entity/store.go"},
			{ObservationID: obs2, EntityType: domain.EntityFile, EntityValue: "internal/store/sqlite/memory.go"},
			{ObservationID: obs3, EntityType: domain.EntityFile, EntityValue: "main.go"},
		})

		// A value containing "%" switches the query to LIKE.
		got, err := store.FindByEntity(ctx, domain.EntityFile, "%/store/%")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 LIKE matches, got %d", len(got))
		}
		for _, l := range got {
			if l.EntityValue == "main.go" {
				t.Errorf("LIKE should not match main.go")
			}
		}
	})

	t.Run("empty type and value returns all", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obs1 := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obs1, EntityType: domain.EntityFile, EntityValue: "a.go"},
			{ObservationID: obs1, EntityType: domain.EntityConcept, EntityValue: "coverage"},
		})

		got, err := store.FindByEntity(ctx, "", "")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected all 2 links, got %d", len(got))
		}
	})

	t.Run("results ordered by created_at descending", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		// Insert with explicit timestamps so ORDER BY created_at DESC is
		// deterministic regardless of insert timing.
		insertEntityLinkRaw(t, db, obsID, domain.EntityFile, "oldest.go", "2024-01-01 00:00:00")
		insertEntityLinkRaw(t, db, obsID, domain.EntityFile, "newest.go", "2024-03-01 00:00:00")
		insertEntityLinkRaw(t, db, obsID, domain.EntityFile, "middle.go", "2024-02-01 00:00:00")

		got, err := store.FindByEntity(ctx, domain.EntityFile, "")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 links, got %d", len(got))
		}
		if got[0].EntityValue != "newest.go" {
			t.Errorf("expected newest first, got %q", got[0].EntityValue)
		}
		if got[2].EntityValue != "oldest.go" {
			t.Errorf("expected oldest last, got %q", got[2].EntityValue)
		}
	})

	t.Run("no matches returns empty slice", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)
		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "a.go"},
		})

		got, err := store.FindByEntity(ctx, domain.EntityPackage, "missing")
		if err != nil {
			t.Fatalf("FindByEntity: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(got))
		}
	})

	t.Run("cancelled context returns an error", func(t *testing.T) {
		store, _, cleanup := setupEntityTestStore(t)
		defer cleanup()

		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := store.FindByEntity(cancelled, domain.EntityFile, "x"); err == nil {
			t.Fatalf("expected error for FindByEntity on cancelled context, got nil")
		}
	})
}

func TestDeleteByObservation(t *testing.T) {
	ctx := context.Background()

	t.Run("removes all links for an observation", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "a.go"},
			{ObservationID: obsID, EntityType: domain.EntitySymbol, EntityValue: "F"},
		})

		if err := store.DeleteByObservation(ctx, obsID); err != nil {
			t.Fatalf("DeleteByObservation: %v", err)
		}

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation after delete: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 links after delete, got %d", len(got))
		}
	})

	t.Run("does not affect other observations", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		keep := createObservation(t, db)
		remove := createObservation(t, db)

		mustSaveLinks(t, store, ctx, []*domain.EntityLink{
			{ObservationID: keep, EntityType: domain.EntityFile, EntityValue: "keep.go"},
			{ObservationID: remove, EntityType: domain.EntityFile, EntityValue: "remove.go"},
		})

		if err := store.DeleteByObservation(ctx, remove); err != nil {
			t.Fatalf("DeleteByObservation: %v", err)
		}

		if got, err := store.GetByObservation(ctx, keep); err != nil || len(got) != 1 {
			t.Fatalf("other observation should keep its link: got=%v err=%v", got, err)
		}
	})

	t.Run("unknown observation is a no-op without error", func(t *testing.T) {
		store, _, cleanup := setupEntityTestStore(t)
		defer cleanup()

		if err := store.DeleteByObservation(ctx, 999999); err != nil {
			t.Fatalf("DeleteByObservation(unknown): unexpected error: %v", err)
		}
	})

	t.Run("cancelled context returns an error", func(t *testing.T) {
		store, _, cleanup := setupEntityTestStore(t)
		defer cleanup()

		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if err := store.DeleteByObservation(cancelled, 1); err == nil {
			t.Fatalf("expected error for DeleteByObservation on cancelled context, got nil")
		}
	})
}

func TestScanEntityLinks_TimeParsing(t *testing.T) {
	ctx := context.Background()

	t.Run("malformed created_at keeps link with zero time and no error", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		// An unparseable, non-empty created_at exercises the error-tolerant
		// branch of scanEntityLinks: the row is still returned (zero time) and
		// no error is surfaced to the caller.
		insertEntityLinkRaw(t, db, obsID, domain.EntityConcept, "bad-time", "not-a-valid-date")

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation: unexpected error on bad timestamp: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected link to still be returned despite bad time, got %d", len(got))
		}
		if !got[0].CreatedAt.IsZero() {
			t.Errorf("expected zero CreatedAt for unparseable timestamp, got %v", got[0].CreatedAt)
		}
		if got[0].EntityValue != "bad-time" {
			t.Errorf("unexpected entity value: %q", got[0].EntityValue)
		}
	})

	// Characterization: the modernc.org/sqlite driver returns DATETIME columns
	// as RFC3339 strings (e.g. "2024-06-15T12:30:45Z"), but scanEntityLinks
	// parses with the "2006-01-02 15:04:05" layout, so the two never match.
	// This pins the *currently observed* behavior: a legitimately stored
	// datetime still surfaces as a zero CreatedAt without error. If the parser
	// layout is ever corrected, this assertion will flip and force a conscious
	// update rather than silently regressing.
	t.Run("stored datetime is currently surfaced with zero CreatedAt", func(t *testing.T) {
		store, db, cleanup := setupEntityTestStore(t)
		defer cleanup()
		obsID := createObservation(t, db)

		insertEntityLinkRaw(t, db, obsID, domain.EntityFile, "store.go", "2024-06-15 12:30:45")

		got, err := store.GetByObservation(ctx, obsID)
		if err != nil {
			t.Fatalf("GetByObservation: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 link, got %d", len(got))
		}
		if !got[0].CreatedAt.IsZero() {
			t.Errorf("characterization: expected zero CreatedAt under current format mismatch, got %v", got[0].CreatedAt)
		}
		if got[0].EntityValue != "store.go" {
			t.Errorf("unexpected entity value: %q", got[0].EntityValue)
		}
	})
}

// TestStore_ImplementsRepository documents and verifies that the concrete Store
// satisfies the domain.EntityRepository contract at compile time and runtime.
func TestStore_ImplementsRepository(t *testing.T) {
	var _ domain.EntityRepository = (*Store)(nil)

	store, db, cleanup := setupEntityTestStore(t)
	defer cleanup()
	obsID := createObservation(t, db)

	var repo domain.EntityRepository = store
	ctx := context.Background()
	if err := repo.SaveLinks(ctx, []*domain.EntityLink{
		{ObservationID: obsID, EntityType: domain.EntityFile, EntityValue: "x.go"},
	}); err != nil {
		t.Fatalf("SaveLinks via interface: %v", err)
	}
	links, err := repo.GetByObservation(ctx, obsID)
	if err != nil {
		t.Fatalf("GetByObservation via interface: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link via interface, got %d", len(links))
	}
	if _, err := repo.FindByEntity(ctx, domain.EntityFile, "x.go"); err != nil {
		t.Fatalf("FindByEntity via interface: %v", err)
	}
	if err := repo.DeleteByObservation(ctx, obsID); err != nil {
		t.Fatalf("DeleteByObservation via interface: %v", err)
	}
}
