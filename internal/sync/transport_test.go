package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileTransport_ManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTransport(dir)

	// Read non-existent manifest returns empty
	m, err := ft.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Version != 1 || len(m.Chunks) != 0 {
		t.Fatalf("expected empty manifest, got %+v", m)
	}

	// Write and read back
	m.Chunks = append(m.Chunks, ChunkEntry{ID: "abc12345", Sessions: 2, Memories: 5})
	if err := ft.WriteManifest(m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	m2, err := ft.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest after write: %v", err)
	}
	if len(m2.Chunks) != 1 || m2.Chunks[0].ID != "abc12345" {
		t.Fatalf("manifest mismatch: %+v", m2)
	}
}

func TestFileTransport_ChunkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTransport(dir)

	data := []byte(`{"sessions":[],"observations":[],"prompts":[]}`)
	entry := ChunkEntry{ID: "test1234"}

	if err := ft.WriteChunk("test1234", data, entry); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, "chunks", "test1234.jsonl.gz")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("chunk file not found: %v", err)
	}

	// Read back
	got, err := ft.ReadChunk("test1234")
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("chunk data mismatch: got %q, want %q", got, data)
	}
}

func TestFileTransport_ReadChunk_NotFound(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTransport(dir)

	_, err := ft.ReadChunk("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent chunk")
	}
}
