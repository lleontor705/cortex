package external

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

type serverScopedVectorIndex struct {
	inner                 domain.VectorIndex
	tenantID, workspaceID string
}

var ErrServerScopedDeleteUnsupported = errors.New("external: server-scoped vector delete requires a scoped adapter contract")

func NewServerScopedVectorIndex(inner domain.VectorIndex, tenantID, workspaceID string) (domain.VectorIndex, error) {
	if inner == nil {
		return nil, errors.New("external: server vector index is required")
	}
	tenantID, workspaceID = strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID)
	if tenantID == "" || workspaceID == "" {
		return nil, errors.New("external: server vector tenant and workspace are required")
	}
	return &serverScopedVectorIndex{inner: inner, tenantID: tenantID, workspaceID: workspaceID}, nil
}

func (s *serverScopedVectorIndex) ID() string { return "server-scoped/" + s.inner.ID() }
func (s *serverScopedVectorIndex) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	scoped := make([]domain.VectorPoint, len(points))
	for i, point := range points {
		if !validProjectID(metadataString(point.Metadata, "project_id")) {
			return fmt.Errorf("external: server vector upsert requires trusted project_id")
		}
		scoped[i] = point
		scoped[i].Metadata = cloneMetadata(point.Metadata)
		scoped[i].Metadata["tenant_id"] = s.tenantID
		scoped[i].Metadata["workspace_id"] = s.workspaceID
	}
	return s.inner.Upsert(ctx, scoped)
}
func (s *serverScopedVectorIndex) Search(ctx context.Context, query domain.VectorQuery) ([]domain.VectorCandidate, error) {
	projectID := metadataString(query.Filters, "project_id")
	projectLabel := strings.TrimSpace(metadataString(query.Filters, "project"))
	// Broad tenant/workspace queries may omit project identity. A query that
	// selects a display label, however, must carry the canonical UUID too;
	// labels are non-unique and cannot define an authorization boundary.
	if (strings.TrimSpace(projectID) != "" && !validProjectID(projectID)) || (projectLabel != "" && !validProjectID(projectID)) {
		return nil, errors.New("external: server vector project filter requires trusted project_id")
	}
	query.Filters = cloneMetadata(query.Filters)
	query.Filters["tenant_id"] = s.tenantID
	query.Filters["workspace_id"] = s.workspaceID
	return s.inner.Search(ctx, query)
}

func validProjectID(value string) bool {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed != uuid.Nil
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}
func (s *serverScopedVectorIndex) Delete(ctx context.Context, ids []int64) error {
	return ErrServerScopedDeleteUnsupported
}
func (s *serverScopedVectorIndex) Health(ctx context.Context) domain.Health {
	return s.inner.Health(ctx)
}
func (s *serverScopedVectorIndex) Capabilities(ctx context.Context) (domain.Capabilities, error) {
	return s.inner.Capabilities(ctx)
}
func (s *serverScopedVectorIndex) Close() error { return s.inner.Close() }

func cloneMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for key, value := range in {
		out[key] = value
	}
	return out
}

type requestVectorScopeKey struct{}

type requestVectorScope struct {
	tenantID    string
	workspaceID string
}

// WithRequestVectorScope carries an already-authorized tenant/workspace
// boundary from server authentication to vector operations. It is package
// private at the storage boundary: callers cannot pass vector filters as an
// alternative authority source.
func WithRequestVectorScope(ctx context.Context, tenantID, workspaceID string) context.Context {
	return context.WithValue(ctx, requestVectorScopeKey{}, requestVectorScope{
		tenantID: tenantID, workspaceID: workspaceID,
	})
}

// NewRequestScopedVectorIndex enforces the request's verified scope at the
// final vector boundary. It is used by SaaS server mode; a missing context
// fails closed rather than falling back to an unscoped collection query.
func NewRequestScopedVectorIndex(inner domain.VectorIndex) (domain.VectorIndex, error) {
	if inner == nil {
		return nil, errors.New("external: server vector index is required")
	}
	return &requestScopedVectorIndex{inner: inner}, nil
}

type requestScopedVectorIndex struct{ inner domain.VectorIndex }

func (s *requestScopedVectorIndex) ID() string { return "request-scoped/" + s.inner.ID() }
func (s *requestScopedVectorIndex) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	scoped, err := s.scoped(ctx)
	if err != nil {
		return err
	}
	return scoped.Upsert(ctx, points)
}
func (s *requestScopedVectorIndex) Search(ctx context.Context, query domain.VectorQuery) ([]domain.VectorCandidate, error) {
	scoped, err := s.scoped(ctx)
	if err != nil {
		return nil, err
	}
	return scoped.Search(ctx, query)
}
func (s *requestScopedVectorIndex) Delete(context.Context, []int64) error {
	return ErrServerScopedDeleteUnsupported
}
func (s *requestScopedVectorIndex) Health(ctx context.Context) domain.Health {
	return s.inner.Health(ctx)
}
func (s *requestScopedVectorIndex) Capabilities(ctx context.Context) (domain.Capabilities, error) {
	return s.inner.Capabilities(ctx)
}
func (s *requestScopedVectorIndex) Close() error { return s.inner.Close() }

func (s *requestScopedVectorIndex) scoped(ctx context.Context) (*serverScopedVectorIndex, error) {
	scope, ok := ctx.Value(requestVectorScopeKey{}).(requestVectorScope)
	if !ok {
		return nil, errors.New("external: server vector request scope is required")
	}
	index, err := NewServerScopedVectorIndex(s.inner, scope.tenantID, scope.workspaceID)
	if err != nil {
		return nil, errors.New("external: server vector request scope is required")
	}
	return index.(*serverScopedVectorIndex), nil
}
