package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

func TestSync_ExportPrivacy(t *testing.T) {
	ctx := context.Background()
	transport := NewFileTransport(t.TempDir())
	store := newMockStore()
	now := time.Now().UTC()

	sessSummaryOriginal := "Sum <private>canary_sess</private> text"
	obsTitleOriginal := "Obs <private>canary_title</private> title"
	obsContentOriginal := "Obs <private>canary_content</private> body"
	promptContentOriginal := "P <private>canary_prompt</private> query"

	store.exportData = &sqlitestore.ExportData{
		Sessions: []*domain.Session{
			{
				ID:        "s1",
				Project:   "p",
				Directory: "/d",
				Summary:   sessSummaryOriginal,
				StartedAt: now,
			},
		},
		Observations: []*domain.Observation{
			{
				ID:        1,
				SessionID: "s1",
				Title:     obsTitleOriginal,
				Content:   obsContentOriginal,
				Project:   "p",
				Scope:     "project",
				Tags:      []string{"alpha", "beta"},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		Prompts: []*domain.Prompt{
			{
				ID:        1,
				SessionID: "s1",
				Project:   "p",
				Content:   promptContentOriginal,
				CreatedAt: now,
			},
		},
	}

	sy := NewSyncer(store, transport)

	res, err := sy.Export(ctx, "u", "p")
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if res == nil || res.IsEmpty {
		t.Fatalf("expected non-empty export result, got %+v", res)
	}
	if res.SessionsExported != 1 || res.ObservationsExported != 1 || res.PromptsExported != 1 {
		t.Fatalf("unexpected export counts: %+v", res)
	}

	// 1. Source nonmutation: ensure the in-memory source data was untouched by preflight/export.
	if store.exportData.Sessions[0].Summary != sessSummaryOriginal {
		t.Fatalf("source session summary mutated: got %q, want %q", store.exportData.Sessions[0].Summary, sessSummaryOriginal)
	}
	if store.exportData.Observations[0].Title != obsTitleOriginal {
		t.Fatalf("source observation title mutated: got %q, want %q", store.exportData.Observations[0].Title, obsTitleOriginal)
	}
	if store.exportData.Observations[0].Content != obsContentOriginal {
		t.Fatalf("source observation content mutated: got %q, want %q", store.exportData.Observations[0].Content, obsContentOriginal)
	}
	if store.exportData.Prompts[0].Content != promptContentOriginal {
		t.Fatalf("source prompt content mutated: got %q, want %q", store.exportData.Prompts[0].Content, promptContentOriginal)
	}

	// 2. Exact protected bytes and fields in chunk file.
	chunkBytes, err := transport.ReadChunk(res.ChunkID)
	if err != nil {
		t.Fatalf("ReadChunk failed: %v", err)
	}
	for _, canary := range []string{"canary_sess", "canary_title", "canary_content", "canary_prompt", "<private>", "</private>"} {
		if strings.Contains(string(chunkBytes), canary) {
			t.Fatalf("private canary %q leaked in chunk bytes: %s", canary, string(chunkBytes))
		}
	}

	var exportedChunk ChunkData
	if err := json.Unmarshal(chunkBytes, &exportedChunk); err != nil {
		t.Fatalf("failed to parse chunk bytes: %v", err)
	}
	if len(exportedChunk.Sessions) != 1 || exportedChunk.Sessions[0].Summary != "Sum [REDACTED] text" {
		t.Fatalf("exported session summary mismatch: got %q", exportedChunk.Sessions[0].Summary)
	}
	if len(exportedChunk.Observations) != 1 || exportedChunk.Observations[0].Title != "Obs [REDACTED] title" || exportedChunk.Observations[0].Content != "Obs [REDACTED] body" {
		t.Fatalf("exported observation mismatch: title=%q content=%q", exportedChunk.Observations[0].Title, exportedChunk.Observations[0].Content)
	}
	if len(exportedChunk.Prompts) != 1 || exportedChunk.Prompts[0].Content != "P [REDACTED] query" {
		t.Fatalf("exported prompt mismatch: content=%q", exportedChunk.Prompts[0].Content)
	}

	// 3. Exact hash calculation.
	h := sha256.Sum256(chunkBytes)
	expectedChunkID := hex.EncodeToString(h[:])[:8]
	if res.ChunkID != expectedChunkID {
		t.Fatalf("chunk ID mismatch: got %q, want %q", res.ChunkID, expectedChunkID)
	}

	// 4. Exact manifest entries.
	manifest, err := transport.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest failed: %v", err)
	}
	if len(manifest.Chunks) != 1 {
		t.Fatalf("expected 1 chunk in manifest, got %d", len(manifest.Chunks))
	}
	mEntry := manifest.Chunks[0]
	if mEntry.ID != res.ChunkID || mEntry.CreatedBy != "u" || mEntry.Sessions != 1 || mEntry.Memories != 1 || mEntry.Prompts != 1 {
		t.Fatalf("manifest entry mismatch: %+v", mEntry)
	}

	// 5. Exact markers.
	if !store.syncedChunks[res.ChunkID] || len(store.syncedChunks) != 1 {
		t.Fatalf("synced chunk marker mismatch: %+v", store.syncedChunks)
	}

	// 6. Preflight failure precedes serialization, write, manifest, and markers (zero effects).
	futureTime := now.Add(2 * time.Hour)
	badContent := "Bad <private>unclosed"
	store.exportData.Observations = []*domain.Observation{
		{
			ID:        2,
			SessionID: "s1",
			Title:     "T",
			Content:   badContent,
			Project:   "p",
			Scope:     "project",
			CreatedAt: futureTime,
			UpdatedAt: futureTime,
		},
	}
	resBad, err := sy.Export(ctx, "u", "p")
	if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker on unclosed marker, got: %v", err)
	}
	if resBad != nil {
		t.Fatalf("expected nil result on export failure, got %+v", resBad)
	}
	// Source was not mutated on failure
	if store.exportData.Observations[0].Content != badContent {
		t.Fatalf("source observation mutated on rejection: got %q, want %q", store.exportData.Observations[0].Content, badContent)
	}
	// Manifest was not modified (no new chunk added)
	mAfterFail, err := transport.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest after failure failed: %v", err)
	}
	if len(mAfterFail.Chunks) != 1 || mAfterFail.Chunks[0].ID != res.ChunkID {
		t.Fatalf("manifest modified on failed export: %+v", mAfterFail)
	}
	// Marker was not added
	if len(store.syncedChunks) != 1 || !store.syncedChunks[res.ChunkID] {
		t.Fatalf("markers modified on failed export: %+v", store.syncedChunks)
	}

	// 7. Preflight failure on metadata private marker.
	metaBadProject := "proj<private>bad</private>"
	store.exportData.Observations = []*domain.Observation{
		{
			ID:        3,
			SessionID: "s1",
			Title:     "T",
			Content:   "Valid content",
			Project:   metaBadProject,
			Scope:     "project",
			CreatedAt: futureTime.Add(time.Hour),
			UpdatedAt: futureTime.Add(time.Hour),
		},
	}
	resMeta, err := sy.Export(ctx, "u", metaBadProject)
	if err == nil || !errors.Is(err, privacy.ErrMetadataRejected) {
		t.Fatalf("expected ErrMetadataRejected, got: %v", err)
	}
	if resMeta != nil {
		t.Fatalf("expected nil result on metadata failure, got %+v", resMeta)
	}
	if store.exportData.Observations[0].Project != metaBadProject {
		t.Fatalf("source project mutated on rejection: got %q", store.exportData.Observations[0].Project)
	}
	mAfterMeta, _ := transport.ReadManifest()
	if len(mAfterMeta.Chunks) != 1 {
		t.Fatalf("manifest modified on metadata failure: %+v", mAfterMeta)
	}
	if len(store.syncedChunks) != 1 {
		t.Fatalf("markers modified on metadata failure: %+v", store.syncedChunks)
	}
}

func TestSync_ImportPrivacy_PriorChunksRemainOnLaterInvalid(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("mock_store_invariance", func(t *testing.T) {
		transport := NewFileTransport(t.TempDir())
		store := newMockStore()

		mk := func(c *ChunkData) (string, []byte) {
			d, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("json.Marshal chunk: %v", err)
			}
			h := sha256.Sum256(d)
			id := hex.EncodeToString(h[:])[:8]
			entry := ChunkEntry{
				ID:        id,
				Sessions:  len(c.Sessions),
				Memories:  len(c.Observations),
				Prompts:   len(c.Prompts),
				CreatedBy: "alice",
				CreatedAt: now.Format(time.RFC3339),
			}
			if err := transport.WriteChunk(id, d, entry); err != nil {
				t.Fatalf("WriteChunk: %v", err)
			}
			return id, d
		}

		c1ObsTitle := "Good title <private>c1_title</private>"
		c1ObsContent := "Good obs <private>c1</private> content"
		c1SessSummary := "Good sess <private>c1_sess</private>"
		c1PromptContent := "Good prompt <private>c1_prompt</private>"

		chunk1 := &ChunkData{
			Sessions: []*domain.Session{
				{
					ID:        "s1",
					Project:   "p",
					Directory: "/d",
					Summary:   c1SessSummary,
					StartedAt: now,
				},
			},
			Observations: []*domain.Observation{
				{
					ID:        1,
					SessionID: "s1",
					Title:     c1ObsTitle,
					Content:   c1ObsContent,
					Project:   "p",
					Scope:     "project",
					CreatedAt: now,
					UpdatedAt: now,
				},
			},
			Prompts: []*domain.Prompt{
				{
					ID:        1,
					SessionID: "s1",
					Project:   "p",
					Content:   c1PromptContent,
					CreatedAt: now,
				},
			},
		}

		c2ObsContent := "Bad <private>unclosed"
		chunk2 := &ChunkData{
			Observations: []*domain.Observation{
				{
					ID:        2,
					SessionID: "s2",
					Title:     "Bad Title",
					Content:   c2ObsContent,
					Project:   "p",
					Scope:     "project",
					CreatedAt: now,
					UpdatedAt: now,
				},
			},
		}

		id1, _ := mk(chunk1)
		id2, _ := mk(chunk2)

		manifest := &Manifest{
			Version: 1,
			Chunks: []ChunkEntry{
				{ID: id1, Sessions: 1, Memories: 1, Prompts: 1, CreatedBy: "alice", CreatedAt: now.Format(time.RFC3339)},
				{ID: id2, Memories: 1, CreatedBy: "alice", CreatedAt: now.Add(time.Minute).Format(time.RFC3339)},
			},
		}
		if err := transport.WriteManifest(manifest); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}

		sy := NewSyncer(store, transport)
		res, err := sy.Import(ctx)
		if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
			t.Fatalf("expected ErrInvalidMarker on chunk 2, got: %v", err)
		}
		if res != nil {
			t.Fatalf("expected nil result on failure, got %+v", res)
		}

		// Source nonmutation on input structs
		if chunk1.Observations[0].Content != c1ObsContent || chunk2.Observations[0].Content != c2ObsContent {
			t.Fatal("source ChunkData structs were mutated during import")
		}

		// Preserves previous completed chunks on later failure
		if !store.syncedChunks[id1] {
			t.Fatalf("expected chunk 1 %q to remain recorded in synced chunks", id1)
		}
		if store.syncedChunks[id2] {
			t.Fatalf("chunk 2 %q must NOT be recorded in synced chunks", id2)
		}
		if len(store.syncedChunks) != 1 {
			t.Fatalf("expected exactly 1 synced chunk, got %d", len(store.syncedChunks))
		}

		// Exact protected bytes in imported data
		if store.importedData == nil {
			t.Fatal("expected importedData from chunk 1 to be present")
		}
		if len(store.importedData.Sessions) != 1 || store.importedData.Sessions[0].Summary != "Good sess [REDACTED]" {
			t.Fatalf("imported session summary mismatch: %+v", store.importedData.Sessions)
		}
		if len(store.importedData.Observations) != 1 || store.importedData.Observations[0].Title != "Good title [REDACTED]" || store.importedData.Observations[0].Content != "Good obs [REDACTED] content" {
			t.Fatalf("imported observation mismatch: %+v", store.importedData.Observations)
		}
		if len(store.importedData.Prompts) != 1 || store.importedData.Prompts[0].Content != "Good prompt [REDACTED]" {
			t.Fatalf("imported prompt mismatch: %+v", store.importedData.Prompts)
		}

		// Status verification: local=1, remote=2, pending=1
		local, remote, pending, err := sy.Status(ctx)
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		if local != 1 || remote != 2 || pending != 1 {
			t.Fatalf("status mismatch: local=%d remote=%d pending=%d (want 1, 2, 1)", local, remote, pending)
		}
	})

	t.Run("sqlite_backed_effects", func(t *testing.T) {
		db := setupRemotePrivacyDB(t)
		store := sqlitestore.NewStore(db)
		transport := NewFileTransport(t.TempDir())

		mk := func(c *ChunkData) string {
			d, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			h := sha256.Sum256(d)
			id := hex.EncodeToString(h[:])[:8]
			entry := ChunkEntry{
				ID:        id,
				Sessions:  len(c.Sessions),
				Memories:  len(c.Observations),
				Prompts:   len(c.Prompts),
				CreatedBy: "bob",
				CreatedAt: now.Format(time.RFC3339),
			}
			if err := transport.WriteChunk(id, d, entry); err != nil {
				t.Fatalf("WriteChunk: %v", err)
			}
			return id
		}

		id1 := mk(&ChunkData{
			Sessions: []*domain.Session{
				{ID: "s-sql-1", Project: "cortex", Directory: "/dir", Summary: "Sess <private>s_sec</private>", StartedAt: now},
			},
			Observations: []*domain.Observation{
				{SessionID: "s-sql-1", Title: "T1 <private>t_sec</private>", Content: "Data <private>c_sec</private> value", Project: "cortex", Scope: "project", CreatedAt: now, UpdatedAt: now},
			},
			Prompts: []*domain.Prompt{
				{SessionID: "s-sql-1", Content: "Prompt <private>p_sec</private>", Project: "cortex", CreatedAt: now},
			},
		})
		id2 := mk(&ChunkData{
			Observations: []*domain.Observation{
				{SessionID: "s-sql-2", Title: "T2", Content: "Corrupted <private>unclosed", Project: "cortex", Scope: "project", CreatedAt: now, UpdatedAt: now},
			},
		})

		_ = transport.WriteManifest(&Manifest{
			Version: 1,
			Chunks: []ChunkEntry{
				{ID: id1, Sessions: 1, Memories: 1, Prompts: 1, CreatedBy: "bob", CreatedAt: now.Format(time.RFC3339)},
				{ID: id2, Memories: 1, CreatedBy: "bob", CreatedAt: now.Add(time.Minute).Format(time.RFC3339)},
			},
		})

		sy := NewSyncer(store, transport)
		_, err := sy.Import(ctx)
		if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
			t.Fatalf("expected ErrInvalidMarker on chunk 2, got: %v", err)
		}

		// Verify DB effects: chunk 1 was persisted with exact redacted strings; chunk 2 had ZERO effects.
		var obsTitle, obsContent, sessSummary, promptContent string
		var obsCount, sessCount, promptCount, syncedCount int
		_ = db.QueryRow("SELECT title, content FROM observations WHERE session_id='s-sql-1'").Scan(&obsTitle, &obsContent)
		_ = db.QueryRow("SELECT summary FROM sessions WHERE id='s-sql-1'").Scan(&sessSummary)
		_ = db.QueryRow("SELECT content FROM user_prompts WHERE session_id='s-sql-1'").Scan(&promptContent)
		_ = db.QueryRow("SELECT count(*) FROM observations").Scan(&obsCount)
		_ = db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessCount)
		_ = db.QueryRow("SELECT count(*) FROM user_prompts").Scan(&promptCount)
		_ = db.QueryRow("SELECT count(*) FROM sync_chunks").Scan(&syncedCount)

		if obsCount != 1 || obsTitle != "T1 [REDACTED]" || obsContent != "Data [REDACTED] value" {
			t.Fatalf("observation DB mismatch: count=%d title=%q content=%q", obsCount, obsTitle, obsContent)
		}
		if sessCount != 1 || sessSummary != "Sess [REDACTED]" {
			t.Fatalf("session DB mismatch: count=%d summary=%q", sessCount, sessSummary)
		}
		if promptCount != 1 || promptContent != "Prompt [REDACTED]" {
			t.Fatalf("prompt DB mismatch: count=%d content=%q", promptCount, promptContent)
		}
		if syncedCount != 1 {
			t.Fatalf("expected 1 row in sync_chunks, got %d", syncedCount)
		}
		var recordedChunkID string
		_ = db.QueryRow("SELECT chunk_id FROM sync_chunks").Scan(&recordedChunkID)
		if recordedChunkID != id1 {
			t.Fatalf("recorded chunk ID mismatch: got %q, want %q", recordedChunkID, id1)
		}

		// Check status matches
		l, r, p, err := sy.Status(ctx)
		if err != nil || l != 1 || r != 2 || p != 1 {
			t.Fatalf("status mismatch: l=%d r=%d p=%d err=%v", l, r, p, err)
		}

		// Re-run import: chunk 1 is skipped, chunk 2 fails again, DB remains unchanged.
		_, err = sy.Import(ctx)
		if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
			t.Fatalf("second import expected ErrInvalidMarker, got: %v", err)
		}
		var obsCount2 int
		_ = db.QueryRow("SELECT count(*) FROM observations").Scan(&obsCount2)
		if obsCount2 != 1 {
			t.Fatalf("second import modified DB: count=%d", obsCount2)
		}
	})
}
