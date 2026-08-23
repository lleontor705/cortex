package obsidian

import (
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestRenderFrontmatterRequiresCortexIDAndEscapesYAML(t *testing.T) {
	o := &domain.Observation{ID: 42, Title: "A: note", Project: "proj", Scope: "project", Type: "decision", Tags: []string{"a", "b"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)}
	s, err := RenderFrontmatter(o, FrontmatterOptions{Aliases: []string{"alias: x"}, Provenance: "cortex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cortex_id: \"42\"", "project: proj", "scope: project", "type: decision", "tags:", "aliases:", "provenance: cortex", "lifecycle:", "temporal:", "revision:"} {
		if !strings.Contains(s, want) {
			t.Errorf("frontmatter missing %q in %s", want, s)
		}
	}
}

func TestRenderFrontmatterRejectsMissingID(t *testing.T) {
	if _, err := RenderFrontmatter(&domain.Observation{}, FrontmatterOptions{}); err == nil {
		t.Fatal("expected missing cortex_id error")
	}
}
