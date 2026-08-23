package domain_test

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ---------------------------------------------------------------------------
// Mock implementations — prove each port interface is concrete and satisfiable.
// ---------------------------------------------------------------------------

type mockTx struct{}

func (mockTx) Commit() error   { return nil }
func (mockTx) Rollback() error { return nil }

// Handle returns the backend-specific transaction handle.
// For SQLite this is *sql.Tx; for Postgres it is pgx.Tx.
// The `any` return is intentional so domain stays free of backend imports.
func (mockTx) Handle() any { return nil }

type mockTxParticipant struct{}

func (mockTxParticipant) WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error {
	return fn(ctx)
}

type mockUnitOfWork struct{}

func (mockUnitOfWork) Do(ctx context.Context, tctx *domain.TenantContext, participants []domain.TxParticipant, fn func(context.Context) error) error {
	return fn(ctx)
}

type mockStorage struct{}

func (mockStorage) Backend() string { return "sqlite" }
func (mockStorage) BeginTx(ctx context.Context) (domain.Tx, error) {
	return mockTx{}, nil
}
func (mockStorage) Health(ctx context.Context) domain.Health {
	return domain.Health{Status: "healthy", Message: "ok"}
}

type mockEmbeddingProvider struct{}

func (mockEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float32, domain.ModelInfo, error) {
	return nil, domain.ModelInfo{}, nil
}
func (mockEmbeddingProvider) ModelInfo() domain.ModelInfo {
	return domain.ModelInfo{Name: "test-model", Dimension: 128, Version: "v1", Normalized: true}
}
func (mockEmbeddingProvider) Health(ctx context.Context) domain.Health {
	return domain.Health{Status: "healthy"}
}

type mockVectorIndex struct{}

func (mockVectorIndex) ID() string { return "mock-vec" }
func (mockVectorIndex) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	return nil
}
func (mockVectorIndex) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (mockVectorIndex) Delete(ctx context.Context, ids []int64) error { return nil }
func (mockVectorIndex) Health(ctx context.Context) domain.Health {
	return domain.Health{Status: "healthy"}
}
func (mockVectorIndex) Capabilities(ctx context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{IndexType: "mock", MaxDimensions: 128}, nil
}
func (mockVectorIndex) Close() error { return nil }

// Compile-time interface satisfaction — fails to compile if any port is wrong.
var (
	_ domain.Tx                = mockTx{}
	_ domain.TxParticipant     = mockTxParticipant{}
	_ domain.UnitOfWork        = mockUnitOfWork{}
	_ domain.Storage           = mockStorage{}
	_ domain.EmbeddingProvider = mockEmbeddingProvider{}
	_ domain.VectorIndex       = mockVectorIndex{}
)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestPortsCompile asserts every domain port interface is satisfied by a mock.
func TestPortsCompile(t *testing.T) {
	// Re-instantiate to prove the types are usable at runtime, not just compile-time.
	var _ domain.Tx = mockTx{}
	var _ domain.TxParticipant = mockTxParticipant{}
	var _ domain.UnitOfWork = mockUnitOfWork{}
	var _ domain.Storage = mockStorage{}
	var _ domain.EmbeddingProvider = mockEmbeddingProvider{}
	var _ domain.VectorIndex = mockVectorIndex{}
}

// TestNilTenantContextSafety verifies that a nil *TenantContext can be threaded
// through UnitOfWork.Do without panicking.
// REQ-FOUND-001: local mode MUST thread nil TenantContext safely.
func TestNilTenantContextSafety(t *testing.T) {
	tests := []struct {
		name string
		tctx *domain.TenantContext
	}{
		{name: "nil pointer", tctx: nil},
		{name: "zero-value struct", tctx: &domain.TenantContext{}},
		{name: "populated", tctx: &domain.TenantContext{
			TenantID: "t1", WorkspaceID: "w1", OwnerSubject: "u1",
		}},
	}

	uow := mockUnitOfWork{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			err := uow.Do(context.Background(), tt.tctx, nil, func(ctx context.Context) error {
				called = true
				return nil
			})
			if err != nil {
				t.Fatalf("Do returned error: %v", err)
			}
			if !called {
				t.Fatal("fn was not called")
			}
		})
	}
}

// TestStorageBackend confirms the Backend identifier.
func TestStorageBackend(t *testing.T) {
	s := mockStorage{}
	if got := s.Backend(); got != "sqlite" {
		t.Errorf("Backend() = %q, want %q", got, "sqlite")
	}
}

// TestStorageHealth confirms Health returns a populated Health struct.
func TestStorageHealth(t *testing.T) {
	s := mockStorage{}
	h := s.Health(context.Background())
	if h.Status != "healthy" {
		t.Errorf("Health().Status = %q, want %q", h.Status, "healthy")
	}
}

// TestStorageBeginTx confirms BeginTx returns a usable Tx.
func TestStorageBeginTx(t *testing.T) {
	s := mockStorage{}
	tx, err := s.BeginTx(context.Background())
	if err != nil {
		t.Fatalf("BeginTx error: %v", err)
	}
	if tx.Handle() != nil {
		t.Error("expected nil handle from mock")
	}
}

// TestEmbeddingProvider confirms the embedding port round-trips.
func TestEmbeddingProvider(t *testing.T) {
	ep := mockEmbeddingProvider{}
	mi := ep.ModelInfo()
	if mi.Name != "test-model" {
		t.Errorf("ModelInfo().Name = %q", mi.Name)
	}
	if mi.Dimension != 128 {
		t.Errorf("ModelInfo().Dimension = %d, want 128", mi.Dimension)
	}
}

// TestVectorIndexRoundTrip exercises Upsert/Search/Delete on the mock.
func TestVectorIndexRoundTrip(t *testing.T) {
	vi := mockVectorIndex{}
	points := []domain.VectorPoint{
		{ID: 1, Vector: []float32{0.1}, ModelInfo: domain.ModelInfo{Name: "m", Dimension: 1}},
	}
	if err := vi.Upsert(context.Background(), points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	results, err := vi.Search(context.Background(), domain.VectorQuery{
		Vector: []float32{0.1}, Limit: 5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results from mock, got %d", len(results))
	}
	if err := vi.Delete(context.Background(), []int64{1}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := vi.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestVectorCapabilities verifies the Capabilities value type.
func TestVectorCapabilities(t *testing.T) {
	caps := domain.Capabilities{
		IndexType:       "sqlite_blob",
		DistanceMetrics: []string{"cosine"},
		MaxDimensions:   768,
		Filters:         "PostFilter",
		Hybrid:          "disabled",
		Namespaces:      "supported",
		Consistency:     "strong",
		BatchUpsert:     true,
		MaxBatchSize:    500,
	}
	if caps.IndexType != "sqlite_blob" {
		t.Errorf("IndexType = %q", caps.IndexType)
	}
	if !caps.BatchUpsert {
		t.Error("BatchUpsert should be true")
	}
	if caps.MaxBatchSize != 500 {
		t.Errorf("MaxBatchSize = %d, want 500", caps.MaxBatchSize)
	}
}

// TestVectorPointConstruction verifies the VectorPoint value type.
func TestVectorPointConstruction(t *testing.T) {
	p := domain.VectorPoint{
		ID:     42,
		Vector: []float32{0.1, 0.2, 0.3},
		ModelInfo: domain.ModelInfo{
			Name: "test-model", Dimension: 3, Version: "v1", Normalized: true,
		},
		Metadata: map[string]any{"project": "cortex"},
	}
	if p.ID != 42 {
		t.Errorf("ID = %d, want 42", p.ID)
	}
	if len(p.Vector) != 3 {
		t.Errorf("Vector len = %d, want 3", len(p.Vector))
	}
	if !p.ModelInfo.Normalized {
		t.Error("ModelInfo.Normalized should be true")
	}
}

// TestVectorQuery verifies the VectorQuery value type.
func TestVectorQuery(t *testing.T) {
	q := domain.VectorQuery{
		Vector:    []float32{0.5, 0.5},
		Limit:     10,
		Threshold: 0.8,
		Filters:   map[string]any{"project": "test"},
		Namespace: "default",
	}
	if q.Limit != 10 {
		t.Errorf("Limit = %d, want 10", q.Limit)
	}
	if q.Namespace != "default" {
		t.Errorf("Namespace = %q", q.Namespace)
	}
}

// TestVectorCandidate verifies the VectorCandidate value type.
func TestVectorCandidate(t *testing.T) {
	c := domain.VectorCandidate{
		ID:         1,
		Score:      0.95,
		Provenance: "sqlite_blob",
	}
	if c.Score != 0.95 {
		t.Errorf("Score = %f, want 0.95", c.Score)
	}
	if c.Provenance != "sqlite_blob" {
		t.Errorf("Provenance = %q", c.Provenance)
	}
}

// TestSearchID verifies the SearchID type.
func TestSearchID(t *testing.T) {
	var sid domain.SearchID = "search-123"
	if sid != "search-123" {
		t.Errorf("SearchID = %q", sid)
	}
}

// TestPrincipal verifies the Principal value type.
func TestPrincipal(t *testing.T) {
	p := domain.Principal{
		Subject:      "user-1",
		Type:         "user",
		OrgID:        "org-1",
		WorkspaceIDs: []string{"ws-1"},
		Roles:        []string{"member"},
		Scopes:       []string{"cortex.memory.read"},
		AuthMethod:   "oidc",
		GrantDigest:  "abc123",
	}
	if p.Subject != "user-1" {
		t.Errorf("Subject = %q", p.Subject)
	}
	if len(p.WorkspaceIDs) != 1 {
		t.Errorf("WorkspaceIDs len = %d, want 1", len(p.WorkspaceIDs))
	}
	if len(p.Scopes) != 1 {
		t.Errorf("Scopes len = %d, want 1", len(p.Scopes))
	}
}

// TestModelInfo verifies the ModelInfo value type.
func TestModelInfo(t *testing.T) {
	mi := domain.ModelInfo{Name: "nomic-embed", Dimension: 768, Version: "v1.0", Normalized: false}
	if mi.Dimension != 768 {
		t.Errorf("Dimension = %d, want 768", mi.Dimension)
	}
}

// TestHealth verifies the Health value type.
func TestHealth(t *testing.T) {
	h := domain.Health{Status: "degraded", Message: "high latency"}
	if h.Status != "degraded" {
		t.Errorf("Status = %q", h.Status)
	}
}
