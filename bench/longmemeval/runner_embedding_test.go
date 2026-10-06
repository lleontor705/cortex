package longmemeval

import (
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/embedding"
)

// An unusable EmbeddingCfg must abort before any scoring: a run that silently
// degraded to lexical-only would still publish vector-enabled report rows.
func TestRunRejectsUnusableEmbeddingProvider(t *testing.T) {
	_, err := Run(Config{
		DataPath:     writeDatasetJSON(t, datasetFixture()),
		EmbeddingCfg: &embedding.Config{Provider: "bogus"},
	})
	if err == nil {
		t.Fatal("Run with unusable embedding provider: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create embedding service") {
		t.Fatalf("error = %q, want embedding service construction failure", err)
	}
}

// A nil EmbeddingCfg keeps the lexical-only store path reachable so existing
// no-embedding runs stay byte-identical behind the new conditional.
func TestRunWithoutEmbeddingCfgStillScores(t *testing.T) {
	result, err := Run(Config{DataPath: writeDatasetJSON(t, datasetFixture()), Limit: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
}
