package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

type reqOpsTestTx struct {
	fakeOperations
	passedEdge *domain.Edge
}

func (r *reqOpsTestTx) CreateGraphEdge(_ context.Context, edge *domain.Edge) error {
	copy := *edge
	copy.ID = 777
	copy.PublicID = "00000000-0000-0000-0000-000000000777"
	copy.CreatedAt = time.Now().UTC()
	r.passedEdge = &copy
	*edge = copy
	return nil
}

func TestServerPrivacy_RequestOperations_CreateGraphEdge_Redaction(t *testing.T) {
	mockOps := &reqOpsTestTx{}
	ctx := withOperations(context.Background(), mockOps)
	ops := requestOperations{}

	edge := &domain.Edge{
		FromObsID:    1,
		ToObsID:      2,
		RelationType: "relates_to",
		Source:       "manual",
		Reasoning:    "Testing <private>canary-edge-reason</private> text",
	}

	if err := ops.CreateGraphEdge(ctx, edge); err != nil {
		t.Fatalf("CreateGraphEdge failed: %v", err)
	}

	if edge.ID != 777 || edge.PublicID != "00000000-0000-0000-0000-000000000777" {
		t.Fatalf("expected populated IDs, got ID=%d, PublicID=%s", edge.ID, edge.PublicID)
	}
	if strings.Contains(edge.Reasoning, "canary-edge-reason") {
		t.Fatalf("canary leaked into returned edge reasoning: %q", edge.Reasoning)
	}
	if !strings.Contains(edge.Reasoning, privacy.RedactedPlaceholder) {
		t.Fatalf("expected placeholder in edge reasoning: %q", edge.Reasoning)
	}
	if mockOps.passedEdge == nil || strings.Contains(mockOps.passedEdge.Reasoning, "canary-edge-reason") {
		t.Fatalf("canary leaked to downstream operations: %+v", mockOps.passedEdge)
	}
}

func TestServerPrivacy_RequestOperations_CreateGraphEdge_ImmutabilityOnFailure(t *testing.T) {
	mockOps := &reqOpsTestTx{}
	ctx := withOperations(context.Background(), mockOps)
	ops := requestOperations{}

	origReasoning := "Invalid <private>unclosed-canary"
	edge := &domain.Edge{
		FromObsID:    1,
		ToObsID:      2,
		RelationType: "relates_to",
		Source:       "manual",
		Reasoning:    origReasoning,
	}

	err := ops.CreateGraphEdge(ctx, edge)
	if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker, got %v", err)
	}
	if edge.Reasoning != origReasoning {
		t.Fatalf("caller's edge was mutated on failure: %q != %q", edge.Reasoning, origReasoning)
	}
	if edge.ID != 0 || edge.PublicID != "" {
		t.Fatalf("caller's edge IDs were mutated on failure: ID=%d, PublicID=%s", edge.ID, edge.PublicID)
	}
	if mockOps.passedEdge != nil {
		t.Fatalf("downstream operation was called despite validation failure")
	}
}

func TestServerPrivacy_RequestOperations_CreateGraphEdge_MetadataValidation(t *testing.T) {
	mockOps := &reqOpsTestTx{}
	ctx := withOperations(context.Background(), mockOps)
	ops := requestOperations{}

	tests := []struct {
		name     string
		relation string
		source   string
	}{
		{"marker_in_relation", "relates/<private>c1</private>", "manual"},
		{"marker_in_source", "relates_to", "agent/<private>c2</private>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			edge := &domain.Edge{
				FromObsID:    1,
				ToObsID:      2,
				RelationType: tc.relation,
				Source:       tc.source,
				Reasoning:    "Valid reasoning",
			}
			err := ops.CreateGraphEdge(ctx, edge)
			if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
				t.Fatalf("expected ErrInvalidMarker on metadata violation, got %v", err)
			}
			if mockOps.passedEdge != nil {
				t.Fatalf("downstream operation called on metadata violation")
			}
		})
	}
}

func TestServerPrivacy_RequestOperations_CreateGraphEdge_NilAndEmpty(t *testing.T) {
	mockOps := &reqOpsTestTx{}
	ctx := withOperations(context.Background(), mockOps)
	ops := requestOperations{}

	if err := ops.CreateGraphEdge(ctx, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil edge, got %v", err)
	}

	emptyEdge := &domain.Edge{
		FromObsID:    1,
		ToObsID:      2,
		RelationType: "relates_to",
	}
	if err := ops.CreateGraphEdge(ctx, emptyEdge); err != nil {
		t.Fatalf("CreateGraphEdge failed with empty reasoning: %v", err)
	}
	if emptyEdge.Reasoning != "" {
		t.Fatalf("expected empty reasoning, got %q", emptyEdge.Reasoning)
	}
}
