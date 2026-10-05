package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

type stubVectorIndex struct {
	status string
}

func (s stubVectorIndex) ID() string { return "stub/" + s.status }

func (s stubVectorIndex) Upsert(context.Context, []domain.VectorPoint) error { return nil }

func (s stubVectorIndex) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}

func (s stubVectorIndex) Delete(context.Context, []int64) error { return nil }

func (s stubVectorIndex) Health(context.Context) domain.Health {
	return domain.Health{Status: s.status, Message: "stub"}
}

func (s stubVectorIndex) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{IndexType: "stub"}, nil
}

func (s stubVectorIndex) Close() error { return nil }

func statusVectorIndex(t *testing.T, stores *Stores) string {
	t.Helper()
	result := callTool(t, handleGetStatus(stores), map[string]interface{}{})
	var payload struct {
		VectorIndex string `json:"vector_index"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &payload); err != nil {
		t.Fatalf("parse status output: %v", err)
	}
	return payload.VectorIndex
}

func TestStatusAdvertisesVectorIndexState(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		vectors domain.VectorIndex
		want    string
	}{
		{"healthy index reports enabled", stubVectorIndex{status: domain.StatusHealthy}, VectorIndexEnabled},
		{"degraded index reports degraded", stubVectorIndex{status: domain.StatusDegraded}, VectorIndexDegraded},
		{"unhealthy index reports disabled", stubVectorIndex{status: domain.StatusUnhealthy}, VectorIndexDisabled},
		{"absent index reports disabled", nil, VectorIndexDisabled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stores := setupTestStores(t)
			stores.Vectors = tc.vectors

			if got := statusVectorIndex(t, stores); got != tc.want {
				t.Errorf("status vector_index = %q, want %q", got, tc.want)
			}
			if got := VectorIndexState(ctx, tc.vectors); got != tc.want {
				t.Errorf("VectorIndexState() = %q, want %q", got, tc.want)
			}
		})
	}
}
