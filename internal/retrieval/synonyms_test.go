package retrieval_test

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"¿que cambios se hiso?", "que cambios se hizo"},
		{"erro en el despliege", "error en el despliegue"},
		{"simple query", "simple query"},
	}

	for _, tt := range tests {
		got := retrieval.NormalizeQuery(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExpandQuerySynonyms(t *testing.T) {
	got := retrieval.ExpandQuerySynonyms("worktree hiso")
	if got == "" {
		t.Fatal("ExpandQuerySynonyms returned empty string")
	}
}
