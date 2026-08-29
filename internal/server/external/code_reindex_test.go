package external

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

type fakeCodeGraphSource struct {
	graph *code.CodeGraph
	calls int
}

func (s *fakeCodeGraphSource) GetCodeGraph(context.Context, string) (*code.CodeGraph, error) {
	s.calls++
	return s.graph, nil
}

type fakeCodeGraphTarget struct {
	graphs []*code.CodeGraph
}

func (t *fakeCodeGraphTarget) SaveCodeGraph(_ context.Context, graph *code.CodeGraph) error {
	t.graphs = append(t.graphs, graph)
	return nil
}

func TestReindexCodeIsRepeatableAndRejectsScopeMismatch(t *testing.T) {
	project := "27b908fb-a84a-40b5-a0b9-f3128e2b77c2"
	source := &fakeCodeGraphSource{graph: &code.CodeGraph{Project: project}}
	target := &fakeCodeGraphTarget{}

	for range 2 {
		if err := ReindexCode(context.Background(), source, target, project); err != nil {
			t.Fatalf("ReindexCode: %v", err)
		}
	}
	if source.calls != 2 || len(target.graphs) != 2 {
		t.Fatalf("calls source=%d target=%d, want 2/2", source.calls, len(target.graphs))
	}

	source.graph = &code.CodeGraph{Project: "different-project"}
	if err := ReindexCode(context.Background(), source, target, project); err == nil {
		t.Fatal("expected mismatched graph project to fail closed")
	}
}
