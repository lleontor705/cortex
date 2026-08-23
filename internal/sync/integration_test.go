package sync

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

// mockSyncStore implements SyncStore for testing.
type mockSyncStore struct {
	exportData   *sqlitestore.ExportData
	importedData *sqlitestore.ExportData
	syncedChunks map[string]bool
	exportErr    error
	importErr    error
}

func newMockStore() *mockSyncStore {
	return &mockSyncStore{
		syncedChunks: make(map[string]bool),
	}
}

func (m *mockSyncStore) ExportAll(ctx context.Context) (*sqlitestore.ExportData, error) {
	if m.exportErr != nil {
		return nil, m.exportErr
	}
	return m.exportData, nil
}

func (m *mockSyncStore) ImportData(ctx context.Context, data *sqlitestore.ExportData) (*sqlitestore.SyncImportResult, error) {
	if m.importErr != nil {
		return nil, m.importErr
	}
	m.importedData = data
	return &sqlitestore.SyncImportResult{
		SessionsImported:     len(data.Sessions),
		ObservationsImported: len(data.Observations),
		PromptsImported:      len(data.Prompts),
	}, nil
}

func (m *mockSyncStore) GetSyncedChunks(ctx context.Context) (map[string]bool, error) {
	return m.syncedChunks, nil
}

func (m *mockSyncStore) RecordSyncedChunk(ctx context.Context, chunkID string) error {
	m.syncedChunks[chunkID] = true
	return nil
}

// testExportData returns sample export data for tests.
func testExportData() *sqlitestore.ExportData {
	now := time.Now().UTC()
	return &sqlitestore.ExportData{
		Version:    "1",
		ExportedAt: now.Format(time.RFC3339),
		Sessions: []*domain.Session{
			{
				ID:        "sess-001",
				Project:   "alpha",
				Directory: "/workspace/alpha",
				StartedAt: now,
			},
		},
		Observations: []*domain.Observation{
			{
				ID:        1,
				Title:     "First observation",
				Content:   "Some content here",
				Type:      "manual",
				Project:   "alpha",
				SessionID: "sess-001",
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        2,
				Title:     "Second observation",
				Content:   "More content",
				Type:      "decision",
				Project:   "alpha",
				SessionID: "sess-001",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		Prompts: []*domain.Prompt{
			{
				ID:        1,
				Content:   "What is the architecture?",
				Project:   "alpha",
				SessionID: "sess-001",
				CreatedAt: now,
			},
		},
	}
}

func TestIntegration_ExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	syncDir := t.TempDir()

	// Setup source store with test data
	srcStore := newMockStore()
	srcStore.exportData = testExportData()

	transport := NewFileTransport(syncDir)
	syncer := NewSyncer(srcStore, transport)

	// Export
	exportRes, err := syncer.Export(ctx, "test-user", "")
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if exportRes.IsEmpty {
		t.Fatal("Export should not be empty")
	}
	if exportRes.SessionsExported != 1 {
		t.Errorf("SessionsExported = %d, want 1", exportRes.SessionsExported)
	}
	if exportRes.ObservationsExported != 2 {
		t.Errorf("ObservationsExported = %d, want 2", exportRes.ObservationsExported)
	}
	if exportRes.PromptsExported != 1 {
		t.Errorf("PromptsExported = %d, want 1", exportRes.PromptsExported)
	}
	if exportRes.ChunkID == "" {
		t.Error("ChunkID should not be empty")
	}

	// Verify chunk file exists on disk
	chunkPath := syncDir + "/chunks/" + exportRes.ChunkID + ".jsonl.gz"
	if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
		t.Errorf("Chunk file not found at %s", chunkPath)
	}

	// Import into a new empty store using the same transport
	dstStore := newMockStore()
	importSyncer := NewSyncer(dstStore, transport)

	importRes, err := importSyncer.Import(ctx)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if importRes.ChunksImported != 1 {
		t.Errorf("ChunksImported = %d, want 1", importRes.ChunksImported)
	}
	if importRes.SessionsImported != 1 {
		t.Errorf("SessionsImported = %d, want 1", importRes.SessionsImported)
	}
	if importRes.ObservationsImported != 2 {
		t.Errorf("ObservationsImported = %d, want 2", importRes.ObservationsImported)
	}
	if importRes.PromptsImported != 1 {
		t.Errorf("PromptsImported = %d, want 1", importRes.PromptsImported)
	}

	// Verify imported data matches original
	if dstStore.importedData == nil {
		t.Fatal("importedData should not be nil")
	}
	if len(dstStore.importedData.Sessions) != 1 {
		t.Errorf("imported sessions = %d, want 1", len(dstStore.importedData.Sessions))
	}
	if len(dstStore.importedData.Observations) != 2 {
		t.Errorf("imported observations = %d, want 2", len(dstStore.importedData.Observations))
	}
	if dstStore.importedData.Sessions[0].ID != "sess-001" {
		t.Errorf("imported session ID = %q, want %q", dstStore.importedData.Sessions[0].ID, "sess-001")
	}
	if dstStore.importedData.Observations[0].Title != "First observation" {
		t.Errorf("imported obs title = %q, want %q", dstStore.importedData.Observations[0].Title, "First observation")
	}
}

func TestIntegration_ExportDedup(t *testing.T) {
	ctx := context.Background()
	syncDir := t.TempDir()

	store := newMockStore()
	store.exportData = testExportData()

	transport := NewFileTransport(syncDir)
	syncer := NewSyncer(store, transport)

	// First export
	res1, err := syncer.Export(ctx, "test-user", "")
	if err != nil {
		t.Fatalf("First export failed: %v", err)
	}
	if res1.IsEmpty {
		t.Fatal("First export should not be empty")
	}

	// Second export of same data -- content-addressed dedup should kick in
	res2, err := syncer.Export(ctx, "test-user", "")
	if err != nil {
		t.Fatalf("Second export failed: %v", err)
	}
	if !res2.IsEmpty {
		t.Error("Second export should be empty (dedup)")
	}
}

func TestIntegration_ImportSkipsAlready(t *testing.T) {
	ctx := context.Background()
	syncDir := t.TempDir()

	// Export data
	srcStore := newMockStore()
	srcStore.exportData = testExportData()

	transport := NewFileTransport(syncDir)
	srcSyncer := NewSyncer(srcStore, transport)

	_, err := srcSyncer.Export(ctx, "test-user", "")
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// First import
	dstStore := newMockStore()
	dstSyncer := NewSyncer(dstStore, transport)

	res1, err := dstSyncer.Import(ctx)
	if err != nil {
		t.Fatalf("First import failed: %v", err)
	}
	if res1.ChunksImported != 1 {
		t.Errorf("First import: ChunksImported = %d, want 1", res1.ChunksImported)
	}

	// Second import -- same store already knows the chunk
	res2, err := dstSyncer.Import(ctx)
	if err != nil {
		t.Fatalf("Second import failed: %v", err)
	}
	if res2.ChunksSkipped != 1 {
		t.Errorf("Second import: ChunksSkipped = %d, want 1", res2.ChunksSkipped)
	}
	if res2.ChunksImported != 0 {
		t.Errorf("Second import: ChunksImported = %d, want 0", res2.ChunksImported)
	}
}

func TestIntegration_StatusAccuracy(t *testing.T) {
	ctx := context.Background()
	syncDir := t.TempDir()

	store := newMockStore()
	store.exportData = testExportData()

	transport := NewFileTransport(syncDir)
	syncer := NewSyncer(store, transport)

	// Export a chunk
	_, err := syncer.Export(ctx, "test-user", "")
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Status from the exporting store: local=1, remote=1, pending=0
	local, remote, pending, err := syncer.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if local != 1 {
		t.Errorf("local = %d, want 1", local)
	}
	if remote != 1 {
		t.Errorf("remote = %d, want 1", remote)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0", pending)
	}

	// New store that doesn't know about the chunk
	newStore := newMockStore()
	newSyncer := NewSyncer(newStore, transport)

	local2, remote2, pending2, err := newSyncer.Status(ctx)
	if err != nil {
		t.Fatalf("Status (new store) failed: %v", err)
	}
	if local2 != 0 {
		t.Errorf("new store local = %d, want 0", local2)
	}
	if remote2 != 1 {
		t.Errorf("new store remote = %d, want 1", remote2)
	}
	if pending2 != 1 {
		t.Errorf("new store pending = %d, want 1", pending2)
	}
}

func TestIntegration_ProjectFilter(t *testing.T) {
	ctx := context.Background()
	syncDir := t.TempDir()
	now := time.Now().UTC()

	store := newMockStore()
	store.exportData = &sqlitestore.ExportData{
		Version:    "1",
		ExportedAt: now.Format(time.RFC3339),
		Sessions: []*domain.Session{
			{ID: "sess-alpha", Project: "alpha", StartedAt: now},
			{ID: "sess-beta", Project: "beta", StartedAt: now},
		},
		Observations: []*domain.Observation{
			{
				ID: 1, Title: "Alpha obs", Content: "alpha content",
				Type: "manual", Project: "alpha", SessionID: "sess-alpha",
				CreatedAt: now, UpdatedAt: now,
			},
			{
				ID: 2, Title: "Beta obs", Content: "beta content",
				Type: "manual", Project: "beta", SessionID: "sess-beta",
				CreatedAt: now, UpdatedAt: now,
			},
			{
				ID: 3, Title: "Another alpha", Content: "more alpha",
				Type: "decision", Project: "alpha", SessionID: "sess-alpha",
				CreatedAt: now, UpdatedAt: now,
			},
		},
		Prompts: []*domain.Prompt{
			{ID: 1, Content: "alpha prompt", Project: "alpha", SessionID: "sess-alpha", CreatedAt: now},
			{ID: 2, Content: "beta prompt", Project: "beta", SessionID: "sess-beta", CreatedAt: now},
		},
	}

	transport := NewFileTransport(syncDir)
	syncer := NewSyncer(store, transport)

	// Export with project filter for "alpha"
	res, err := syncer.Export(ctx, "test-user", "alpha")
	if err != nil {
		t.Fatalf("Export with project filter failed: %v", err)
	}
	if res.IsEmpty {
		t.Fatal("Export should not be empty")
	}
	if res.SessionsExported != 1 {
		t.Errorf("SessionsExported = %d, want 1", res.SessionsExported)
	}
	if res.ObservationsExported != 2 {
		t.Errorf("ObservationsExported = %d, want 2 (only alpha)", res.ObservationsExported)
	}
	if res.PromptsExported != 1 {
		t.Errorf("PromptsExported = %d, want 1 (only alpha)", res.PromptsExported)
	}

	// Verify by importing and checking the actual data
	dstStore := newMockStore()
	importSyncer := NewSyncer(dstStore, transport)

	_, err = importSyncer.Import(ctx)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if dstStore.importedData == nil {
		t.Fatal("importedData should not be nil")
	}
	for _, obs := range dstStore.importedData.Observations {
		if obs.Project != "alpha" {
			t.Errorf("imported observation has project %q, want %q", obs.Project, "alpha")
		}
	}
	for _, sess := range dstStore.importedData.Sessions {
		if sess.Project != "alpha" {
			t.Errorf("imported session has project %q, want %q", sess.Project, "alpha")
		}
	}
}
