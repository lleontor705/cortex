package external

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// CodeGraphSource is the read-only side of an AST reindex operation.
type CodeGraphSource interface {
	GetCodeGraph(context.Context, string) (*code.CodeGraph, error)
}

// CodeGraphTarget atomically replaces a scoped AST corpus.
type CodeGraphTarget interface {
	SaveCodeGraph(context.Context, *code.CodeGraph) error
}

// ReindexCode copies one trusted project corpus. The target replacement is
// checksum-idempotent; this coordinator rejects source scope drift first.
func ReindexCode(ctx context.Context, source CodeGraphSource, target CodeGraphTarget, project string) error {
	if source == nil || target == nil {
		return errors.New("external: code reindex requires source and target")
	}
	project = strings.TrimSpace(project)
	projectID, err := uuid.Parse(project)
	if err != nil {
		return fmt.Errorf("external: code reindex requires project public UUID: %w", err)
	}
	graph, err := source.GetCodeGraph(ctx, projectID.String())
	if err != nil {
		return fmt.Errorf("external: code reindex read: %w", err)
	}
	if graph == nil {
		return errors.New("external: code reindex source returned nil graph")
	}
	graphProject, err := uuid.Parse(strings.TrimSpace(graph.Project))
	if err != nil || graphProject != projectID {
		return errors.New("external: code reindex source crossed project scope")
	}
	if err := target.SaveCodeGraph(ctx, graph); err != nil {
		return fmt.Errorf("external: code reindex publish: %w", err)
	}
	return nil
}
