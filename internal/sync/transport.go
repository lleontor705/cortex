// Package sync provides git-friendly memory synchronization via compressed chunks.
package sync

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Transport defines the interface for reading/writing sync chunks and manifest.
type Transport interface {
	ReadManifest() (*Manifest, error)
	WriteManifest(m *Manifest) error
	WriteChunk(chunkID string, data []byte, entry ChunkEntry) error
	ReadChunk(chunkID string) ([]byte, error)
}

// FileTransport implements Transport using the local filesystem.
// Chunks are stored as gzipped JSONL files in a chunks/ subdirectory.
type FileTransport struct {
	syncDir string
}

// NewFileTransport creates a FileTransport rooted at the given sync directory.
func NewFileTransport(syncDir string) *FileTransport {
	return &FileTransport{syncDir: syncDir}
}

// ReadManifest reads manifest.json from the sync directory.
// Returns an empty manifest (Version=1) if the file doesn't exist.
func (ft *FileTransport) ReadManifest() (*Manifest, error) {
	path := filepath.Join(ft.syncDir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Version: 1}, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// WriteManifest writes the manifest to manifest.json with pretty-printing.
func (ft *FileTransport) WriteManifest(m *Manifest) error {
	if err := os.MkdirAll(ft.syncDir, 0700); err != nil {
		return fmt.Errorf("create sync dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	path := filepath.Join(ft.syncDir, "manifest.json")
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// WriteChunk gzips data and writes to chunks/{chunkID}.jsonl.gz.
func (ft *FileTransport) WriteChunk(chunkID string, data []byte, _ ChunkEntry) error {
	chunksDir := filepath.Join(ft.syncDir, "chunks")
	if err := os.MkdirAll(chunksDir, 0700); err != nil {
		return fmt.Errorf("create chunks dir: %w", err)
	}
	path := filepath.Join(chunksDir, chunkID+".jsonl.gz")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create chunk file: %w", err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	if _, err := gz.Write(data); err != nil {
		gz.Close()
		return fmt.Errorf("write gzip data: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	return nil
}

// ReadChunk reads and decompresses a gzipped chunk file.
func (ft *FileTransport) ReadChunk(chunkID string) ([]byte, error) {
	path := filepath.Join(ft.syncDir, "chunks", chunkID+".jsonl.gz")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open chunk %s: %w", chunkID, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader for chunk %s: %w", chunkID, err)
	}
	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("read chunk %s: %w", chunkID, err)
	}
	return data, nil
}
