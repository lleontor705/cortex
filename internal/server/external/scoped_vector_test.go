package external

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

type scopedRecordingIndex struct {
	points      []domain.VectorPoint
	query       domain.VectorQuery
	deleteCalls int
}

func (*scopedRecordingIndex) ID() string { return "recording" }
func (i *scopedRecordingIndex) Upsert(_ context.Context, p []domain.VectorPoint) error {
	i.points = p
	return nil
}
func (i *scopedRecordingIndex) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	i.query = q
	return nil, nil
}
func (i *scopedRecordingIndex) Delete(context.Context, []int64) error { i.deleteCalls++; return nil }
func (*scopedRecordingIndex) Close() error                            { return nil }
func (*scopedRecordingIndex) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}
func (*scopedRecordingIndex) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}

func TestServerScopedVectorIndexOverwritesCallerBoundary(t *testing.T) {
	inner := &scopedRecordingIndex{}
	idx, err := NewServerScopedVectorIndex(inner, "tenant-authority", "workspace-authority")
	if err != nil {
		t.Fatal(err)
	}
	point := domain.VectorPoint{ID: 1, Vector: []float32{1}, Metadata: map[string]any{"tenant_id": "evil", "workspace_id": "evil", "project_id": "10000000-a000-0000-0000-000000000003", "project": "p"}}
	if err := idx.Upsert(context.Background(), []domain.VectorPoint{point}); err != nil {
		t.Fatal(err)
	}
	_, err = idx.Search(context.Background(), domain.VectorQuery{Vector: []float32{1}, Filters: map[string]any{"tenant_id": "evil", "workspace_id": "evil", "project_id": "10000000-a000-0000-0000-000000000003", "project": "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if inner.points[0].Metadata["tenant_id"] != "tenant-authority" || inner.points[0].Metadata["workspace_id"] != "workspace-authority" {
		t.Fatalf("upsert boundary=%v", inner.points[0].Metadata)
	}
	if inner.query.Filters["tenant_id"] != "tenant-authority" || inner.query.Filters["workspace_id"] != "workspace-authority" {
		t.Fatalf("search boundary=%v", inner.query.Filters)
	}
	if point.Metadata["tenant_id"] != "evil" {
		t.Fatal("wrapper mutated caller metadata")
	}
}

func TestServerScopedVectorIndexRejectsMissingBoundary(t *testing.T) {
	for _, b := range [][2]string{{"", "w"}, {"t", ""}} {
		if _, err := NewServerScopedVectorIndex(&scopedRecordingIndex{}, b[0], b[1]); err == nil {
			t.Fatalf("accepted boundary %q/%q", b[0], b[1])
		}
	}
}

func TestServerScopedVectorIndexDeleteFailsClosedBeforeAdapter(t *testing.T) {
	inner := &scopedRecordingIndex{}
	idx, err := NewServerScopedVectorIndex(inner, "tenant", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Delete(context.Background(), []int64{42}); !errors.Is(err, ErrServerScopedDeleteUnsupported) {
		t.Fatalf("unscoped delete error=%v", err)
	}
	if inner.deleteCalls != 0 {
		t.Fatalf("inner delete calls=%d, want 0", inner.deleteCalls)
	}
}

func TestServerScopedVectorIndexRequiresProjectIdentityForServerTraffic(t *testing.T) {
	inner := &scopedRecordingIndex{}
	idx, err := NewServerScopedVectorIndex(inner, "tenant", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Upsert(context.Background(), []domain.VectorPoint{{ID: 1, Vector: []float32{1}, Metadata: map[string]any{"project": "duplicate"}}}); err == nil {
		t.Fatal("upsert without project_id succeeded")
	}
	if _, err := idx.Search(context.Background(), domain.VectorQuery{Vector: []float32{1}, Filters: map[string]any{"project": "duplicate"}}); err == nil {
		t.Fatal("search without project_id succeeded")
	}
}

func TestServerScopedVectorIndexAllowsBroadTenantWorkspaceSearch(t *testing.T) {
	inner := &scopedRecordingIndex{}
	idx, err := NewServerScopedVectorIndex(inner, "tenant", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Search(context.Background(), domain.VectorQuery{Vector: []float32{1}}); err != nil {
		t.Fatalf("broad scoped search failed: %v", err)
	}
	if inner.query.Filters["tenant_id"] != "tenant" || inner.query.Filters["workspace_id"] != "workspace" {
		t.Fatalf("broad search filters=%v", inner.query.Filters)
	}
}
func TestRequestScopedVectorIndexRequiresVerifiedContextAndOverwritesFilters(t *testing.T) {
	inner := &scopedRecordingIndex{}
	idx, err := NewRequestScopedVectorIndex(inner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Search(context.Background(), domain.VectorQuery{Vector: []float32{1}}); err == nil {
		t.Fatal("unscoped request vector search succeeded")
	}

	ctx := WithRequestVectorScope(context.Background(), "tenant-authority", "workspace-authority")
	_, err = idx.Search(ctx, domain.VectorQuery{Vector: []float32{1}, Filters: map[string]any{
		"tenant_id": "caller-controlled", "workspace_id": "caller-controlled",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if inner.query.Filters["tenant_id"] != "tenant-authority" || inner.query.Filters["workspace_id"] != "workspace-authority" {
		t.Fatalf("request scope filters=%v", inner.query.Filters)
	}
}
