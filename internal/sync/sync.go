package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

// ChunkData is the content of a single sync chunk.
type ChunkData struct {
	Sessions     []*domain.Session     `json:"sessions"`
	Observations []*domain.Observation `json:"observations"`
	Prompts      []*domain.Prompt      `json:"prompts"`
}

// SyncResult is returned after an export operation.
type SyncResult struct {
	ChunkID              string `json:"chunk_id,omitempty"`
	SessionsExported     int    `json:"sessions_exported"`
	ObservationsExported int    `json:"observations_exported"`
	PromptsExported      int    `json:"prompts_exported"`
	IsEmpty              bool   `json:"is_empty"`
}

// ImportResult is returned after an import operation.
type ImportResult struct {
	ChunksImported       int `json:"chunks_imported"`
	ChunksSkipped        int `json:"chunks_skipped"`
	SessionsImported     int `json:"sessions_imported"`
	ObservationsImported int `json:"observations_imported"`
	PromptsImported      int `json:"prompts_imported"`
}

// SyncStore defines the store methods needed by the Syncer.
type SyncStore interface {
	ExportAll(ctx context.Context) (*sqlitestore.ExportData, error)
	ImportData(ctx context.Context, data *sqlitestore.ExportData) (*sqlitestore.SyncImportResult, error)
	GetSyncedChunks(ctx context.Context) (map[string]bool, error)
	RecordSyncedChunk(ctx context.Context, chunkID string) error
}

// Syncer handles chunk-based memory synchronization.
type Syncer struct {
	store     SyncStore
	transport Transport
}

// NewSyncer creates a new Syncer with the given store and transport.
func NewSyncer(store SyncStore, transport Transport) *Syncer {
	return &Syncer{store: store, transport: transport}
}

// Export creates a new sync chunk from database contents.
func (sy *Syncer) Export(ctx context.Context, createdBy, project string) (*SyncResult, error) {
	// Read current manifest
	manifest, err := sy.transport.ReadManifest()
	if err != nil {
		return nil, fmt.Errorf("sync export: read manifest: %w", err)
	}

	// Get known chunk IDs
	synced, err := sy.store.GetSyncedChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync export: get synced chunks: %w", err)
	}

	// Export all data
	exported, err := sy.store.ExportAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync export: export data: %w", err)
	}

	// Filter by project if specified
	chunk := &ChunkData{
		Sessions:     exported.Sessions,
		Observations: exported.Observations,
		Prompts:      exported.Prompts,
	}
	if project != "" {
		chunk = filterByProject(chunk, project)
	}

	// Filter to new data only (after last chunk timestamp)
	lastTime := lastChunkTime(manifest)
	if lastTime != "" {
		chunk = filterNewData(chunk, lastTime)
	}

	// Nothing new to sync
	if len(chunk.Sessions) == 0 && len(chunk.Observations) == 0 && len(chunk.Prompts) == 0 {
		return &SyncResult{IsEmpty: true}, nil
	}

	// Serialize chunk
	data, err := json.Marshal(chunk)
	if err != nil {
		return nil, fmt.Errorf("sync export: marshal chunk: %w", err)
	}

	// Generate content-addressed chunk ID (8 hex chars of SHA256)
	hash := sha256.Sum256(data)
	chunkID := hex.EncodeToString(hash[:])[:8]

	// Check if already exists (content-addressed dedup)
	if synced[chunkID] {
		return &SyncResult{IsEmpty: true, ChunkID: chunkID}, nil
	}

	// Write chunk via transport
	entry := ChunkEntry{
		ID:        chunkID,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Sessions:  len(chunk.Sessions),
		Memories:  len(chunk.Observations),
		Prompts:   len(chunk.Prompts),
	}
	if err := sy.transport.WriteChunk(chunkID, data, entry); err != nil {
		return nil, fmt.Errorf("sync export: write chunk: %w", err)
	}

	// Update manifest
	manifest.Chunks = append(manifest.Chunks, entry)
	if err := sy.transport.WriteManifest(manifest); err != nil {
		return nil, fmt.Errorf("sync export: write manifest: %w", err)
	}

	// Record in DB
	if err := sy.store.RecordSyncedChunk(ctx, chunkID); err != nil {
		return nil, fmt.Errorf("sync export: record chunk: %w", err)
	}

	return &SyncResult{
		ChunkID:              chunkID,
		SessionsExported:     len(chunk.Sessions),
		ObservationsExported: len(chunk.Observations),
		PromptsExported:      len(chunk.Prompts),
	}, nil
}

// Import reads chunks from the manifest and imports new ones into the database.
func (sy *Syncer) Import(ctx context.Context) (*ImportResult, error) {
	manifest, err := sy.transport.ReadManifest()
	if err != nil {
		return nil, fmt.Errorf("sync import: read manifest: %w", err)
	}

	synced, err := sy.store.GetSyncedChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync import: get synced chunks: %w", err)
	}

	result := &ImportResult{}
	for _, entry := range manifest.Chunks {
		if synced[entry.ID] {
			result.ChunksSkipped++
			continue
		}

		data, err := sy.transport.ReadChunk(entry.ID)
		if err != nil {
			return nil, fmt.Errorf("sync import: read chunk %s: %w", entry.ID, err)
		}

		var chunk ChunkData
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil, fmt.Errorf("sync import: parse chunk %s: %w", entry.ID, err)
		}

		importData := &sqlitestore.ExportData{
			Sessions:     chunk.Sessions,
			Observations: chunk.Observations,
			Prompts:      chunk.Prompts,
		}
		importResult, err := sy.store.ImportData(ctx, importData)
		if err != nil {
			return nil, fmt.Errorf("sync import: import chunk %s: %w", entry.ID, err)
		}

		if err := sy.store.RecordSyncedChunk(ctx, entry.ID); err != nil {
			return nil, fmt.Errorf("sync import: record chunk %s: %w", entry.ID, err)
		}

		result.ChunksImported++
		result.SessionsImported += importResult.SessionsImported
		result.ObservationsImported += importResult.ObservationsImported
		result.PromptsImported += importResult.PromptsImported
	}

	return result, nil
}

// Status returns sync status: local chunks, remote chunks, pending imports.
func (sy *Syncer) Status(ctx context.Context) (local, remote, pending int, err error) {
	synced, err := sy.store.GetSyncedChunks(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	manifest, err := sy.transport.ReadManifest()
	if err != nil {
		return 0, 0, 0, err
	}

	local = len(synced)
	remote = len(manifest.Chunks)
	for _, c := range manifest.Chunks {
		if !synced[c.ID] {
			pending++
		}
	}
	return local, remote, pending, nil
}

// GetUsername returns the current username for chunk metadata.
func GetUsername() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	h, err := os.Hostname()
	if err == nil && h != "" {
		return h
	}
	return "unknown"
}

// --- Filtering helpers ---

func lastChunkTime(m *Manifest) string {
	if len(m.Chunks) == 0 {
		return ""
	}
	return m.Chunks[len(m.Chunks)-1].CreatedAt
}

func filterByProject(chunk *ChunkData, project string) *ChunkData {
	project = strings.ToLower(strings.TrimSpace(project))
	result := &ChunkData{}
	sessionIDs := make(map[string]bool)

	for _, s := range chunk.Sessions {
		if strings.ToLower(s.Project) == project {
			result.Sessions = append(result.Sessions, s)
			sessionIDs[s.ID] = true
		}
	}
	for _, o := range chunk.Observations {
		if strings.ToLower(o.Project) == project || sessionIDs[o.SessionID] {
			result.Observations = append(result.Observations, o)
		}
	}
	for _, p := range chunk.Prompts {
		if strings.ToLower(p.Project) == project || sessionIDs[p.SessionID] {
			result.Prompts = append(result.Prompts, p)
		}
	}
	return result
}

func filterNewData(chunk *ChunkData, afterTime string) *ChunkData {
	cutoff, err := time.Parse(time.RFC3339, afterTime)
	if err != nil {
		return chunk // can't parse, return everything
	}
	result := &ChunkData{}
	for _, s := range chunk.Sessions {
		if s.StartedAt.After(cutoff) {
			result.Sessions = append(result.Sessions, s)
		}
	}
	for _, o := range chunk.Observations {
		if o.CreatedAt.After(cutoff) {
			result.Observations = append(result.Observations, o)
		}
	}
	for _, p := range chunk.Prompts {
		if p.CreatedAt.After(cutoff) {
			result.Prompts = append(result.Prompts, p)
		}
	}
	return result
}
