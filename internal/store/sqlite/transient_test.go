package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain/payload"
	_ "modernc.org/sqlite"
)

func newTestTransientDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTransientPayloadStore(t *testing.T) {
	db := newTestTransientDB(t)
	store, err := NewTransientPayloadStore(db)
	if err != nil {
		t.Fatalf("NewTransientPayloadStore failed: %v", err)
	}

	ctx := context.Background()

	// 1. Create and Save Payload
	content := "Line 1: Error database connection refused\nLine 2: Retrying in 5 seconds\nLine 3: Connected successfully."
	p := payload.NewTransientPayload("sess-abc", "proj-1", "cortex_execute", content)

	if err := store.Save(ctx, p); err != nil {
		t.Fatalf("Save payload failed: %v", err)
	}

	// 2. Get Payload
	retrieved, err := store.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get payload failed: %v", err)
	}
	if retrieved.ID != p.ID {
		t.Errorf("ID mismatch: got %s, want %s", retrieved.ID, p.ID)
	}
	if retrieved.Content != content {
		t.Errorf("Content mismatch: got %q, want %q", retrieved.Content, content)
	}
	if retrieved.SessionID != "sess-abc" {
		t.Errorf("SessionID mismatch: got %s, want sess-abc", retrieved.SessionID)
	}

	// 3. Search Payload with FTS5
	results, err := store.Search(ctx, p.ID, "database", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Expected search results for 'database', got 0")
	}
	if !strings.Contains(results[0].Snippet, "database") {
		t.Errorf("Snippet does not contain match: %s", results[0].Snippet)
	}

	// 4. Check Stats
	saved, bytesTotal, count, err := store.Stats(ctx, "sess-abc")
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}
	if bytesTotal != len(content) {
		t.Errorf("BytesTotal = %d, want %d", bytesTotal, len(content))
	}
	if saved != p.TokensSaved {
		t.Errorf("SavedTokens = %d, want %d", saved, p.TokensSaved)
	}

	// 5. Purge Session
	if err := store.PurgeSession(ctx, "sess-abc"); err != nil {
		t.Fatalf("PurgeSession failed: %v", err)
	}

	_, err = store.Get(ctx, p.ID)
	if err == nil {
		t.Fatal("Expected error retrieving purged payload, got nil")
	}

	// Search after purge should return 0
	postPurgeResults, err := store.Search(ctx, p.ID, "database", 5)
	if err != nil {
		t.Fatalf("Search post purge failed: %v", err)
	}
	if len(postPurgeResults) != 0 {
		t.Errorf("Expected 0 results post purge, got %d", len(postPurgeResults))
	}
}
