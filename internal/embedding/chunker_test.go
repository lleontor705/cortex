package embedding

import (
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestPrepareObservationText(t *testing.T) {
	obs := &domain.Observation{
		Title:    "PostgreSQL RLS Implementation",
		Type:     "decision",
		Project:  "cortex",
		TopicKey: "architecture/security",
		Content:  "Enforced tenant-level isolation using cortex_tenant_context and verified principal tokens.",
	}

	enriched := PrepareObservationText(obs)
	if !strings.Contains(enriched, "PostgreSQL RLS Implementation") {
		t.Errorf("expected title in prepared text, got: %s", enriched)
	}
	if !strings.Contains(enriched, "type=decision") || !strings.Contains(enriched, "project=cortex") {
		t.Errorf("expected contextual headers in prepared text, got: %s", enriched)
	}
	if !strings.Contains(enriched, "Enforced tenant-level isolation") {
		t.Errorf("expected content in prepared text, got: %s", enriched)
	}
}

func TestChunkText(t *testing.T) {
	short := "Short text."
	chunks := ChunkText(short, 100, 20)
	if len(chunks) != 1 || chunks[0] != short {
		t.Errorf("expected 1 chunk, got %v", chunks)
	}

	long := strings.Repeat("Sentence one. Sentence two.\n", 50)
	chunks = ChunkText(long, 200, 40)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 300 {
			t.Errorf("chunk %d is unexpectedly large: %d chars", i, len(c))
		}
	}
}
