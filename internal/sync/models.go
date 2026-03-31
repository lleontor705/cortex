package sync

// Manifest tracks all sync chunks and their metadata.
type Manifest struct {
	Version int          `json:"version"`
	Chunks  []ChunkEntry `json:"chunks,omitempty"`
}

// ChunkEntry describes a single sync chunk in the manifest.
type ChunkEntry struct {
	ID        string `json:"id"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Sessions  int    `json:"sessions,omitempty"`
	Memories  int    `json:"memories,omitempty"`
	Prompts   int    `json:"prompts,omitempty"`
}
