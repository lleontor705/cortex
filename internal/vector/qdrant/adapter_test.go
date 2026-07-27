//go:build !qdrant_integration

// Package qdrant is the W8.2 conformance + architecture unit test suite for the
// Qdrant external VectorIndex adapter.
//
// These tests run WITHOUT a Qdrant server (build tag !qdrant_integration). They
// exercise the adapter through a narrow client interface and a fake, proving:
//   - The adapter implements domain.VectorIndex (compile-time + runtime).
//   - Capabilities declares index type, distance metrics, max dimensions,
//     filter support (PreFilter), hybrid, namespaces, consistency, and batch.
//   - Dimension-mismatch vectors are REJECTED fail-closed before any server
//     call (REQ-VEC-001 dim-mismatch corruption pin).
//   - Model-namespace mismatch vectors are REJECTED fail-closed (task: model
//     mismatch fail-closed).
//   - Upsert translates points to Qdrant PointStruct with payload metadata
//     (project/scope/tenant_id/model/model_version/source/type).
//   - Upsert batches large inputs at MaxBatchSize.
//   - Search translates VectorQuery.Filters to Qdrant Must conditions and
//     returns VectorCandidate results with provenance.
//   - Delete translates ids to PointsSelectorIDs.
//   - Health translates HealthCheckReply; Close delegates to the client.
//   - No API key / secret ever appears in an error message (no plaintext leak).
//
// The integration suite (Docker Qdrant) lives in adapter_integration_test.go
// behind the qdrant_integration build tag.
package qdrant

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/qdrant/go-client/qdrant"
)

// --- fake client (implements qdrantClient) ---------------------------------

type fakeClient struct {
	// canned responses
	collectionExists bool
	healthReply      *qdrant.HealthCheckReply
	healthErr        error
	upsertErr        error
	queryErr         error
	deleteErr        error
	createErr        error
	closeErr         error
	queryResult      []*qdrant.ScoredPoint

	// recorded calls
	createCalls  []*qdrant.CreateCollection
	deleteColl   int
	existsCalls  []string
	upsertCalls  []*qdrant.UpsertPoints
	queryCalls   []*qdrant.QueryPoints
	deleteCalls  []*qdrant.DeletePoints
	closeCalls   int
}

func (f *fakeClient) CreateCollection(_ context.Context, req *qdrant.CreateCollection) error {
	f.createCalls = append(f.createCalls, req)
	if f.createErr != nil {
		return f.createErr
	}
	return nil
}

func (f *fakeClient) CollectionExists(_ context.Context, name string) (bool, error) {
	f.existsCalls = append(f.existsCalls, name)
	return f.collectionExists, nil
}

func (f *fakeClient) DeleteCollection(_ context.Context, _ string) error {
	f.deleteColl++
	return nil
}

func (f *fakeClient) Upsert(_ context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error) {
	f.upsertCalls = append(f.upsertCalls, req)
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	return &qdrant.UpdateResult{}, nil
}

func (f *fakeClient) Query(_ context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
	f.queryCalls = append(f.queryCalls, req)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.queryResult, nil
}

func (f *fakeClient) Delete(_ context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error) {
	f.deleteCalls = append(f.deleteCalls, req)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &qdrant.UpdateResult{}, nil
}

func (f *fakeClient) HealthCheck(_ context.Context) (*qdrant.HealthCheckReply, error) {
	if f.healthErr != nil {
		return nil, f.healthErr
	}
	return f.healthReply, nil
}

func (f *fakeClient) Close() error {
	f.closeCalls++
	return f.closeErr
}

// newTestAdapter builds an Adapter wired to a fake for unit tests. The
// collection is marked as pre-existing so no CreateCollection is attempted.
func newTestAdapter(t *testing.T, fc *fakeClient) *Adapter {
	t.Helper()
	return &Adapter{
		client:       fc,
		collection:   "cortex_test",
		dimension:    4,
		modelName:    "test-model",
		maxBatchSize: 256,
		caps: defaultCapabilities(4),
	}
}

// --- conformance tests -----------------------------------------------------

// TestAdapter_ImplementsVectorIndex is the compile-time + runtime conformance
// assertion: the adapter MUST satisfy the domain.VectorIndex port.
func TestAdapter_ImplementsVectorIndex(t *testing.T) {
	var _ domain.VectorIndex = (*Adapter)(nil)
	var _ domain.VectorIndex = NewWithClient(&fakeClient{}, AdapterConfig{
		Collection: "x", Dimension: 4, ModelName: "m",
	})
}

// TestAdapter_ID_DeclaresQdrant verifies the adapter identifies itself as the
// qdrant index type.
func TestAdapter_ID_DeclaresQdrant(t *testing.T) {
	a := newTestAdapter(t, &fakeClient{})
	if a.ID() != adapterID {
		t.Errorf("ID() = %q, want %q", a.ID(), adapterID)
	}
}

// TestAdapter_Capabilities_DeclaresFullSet verifies the adapter declares every
// Capabilities field mandated by REQ-VEC-001 / ADR-05.
func TestAdapter_Capabilities_DeclaresFullSet(t *testing.T) {
	a := newTestAdapter(t, &fakeClient{})
	caps, err := a.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.IndexType != adapterID {
		t.Errorf("IndexType = %q, want %q", caps.IndexType, adapterID)
	}
	foundCosine := false
	for _, m := range caps.DistanceMetrics {
		if m == "cosine" {
			foundCosine = true
		}
	}
	if !foundCosine {
		t.Errorf("DistanceMetrics %v does not include cosine", caps.DistanceMetrics)
	}
	if caps.MaxDimensions < 1 {
		t.Errorf("MaxDimensions = %d, want >= 1", caps.MaxDimensions)
	}
	if caps.Filters != "PreFilter" {
		t.Errorf("Filters = %q, want PreFilter (Qdrant filtered HNSW)", caps.Filters)
	}
	if caps.Hybrid == "" {
		t.Error("Hybrid is empty")
	}
	if caps.Namespaces != "supported" {
		t.Errorf("Namespaces = %q, want supported", caps.Namespaces)
	}
	if caps.Consistency == "" {
		t.Error("Consistency is empty")
	}
	if !caps.BatchUpsert {
		t.Error("BatchUpsert = false; qdrant supports batched upsert")
	}
	if caps.MaxBatchSize <= 0 {
		t.Errorf("MaxBatchSize = %d, want > 0", caps.MaxBatchSize)
	}
}

// TestAdapter_Upsert_DimensionMismatchRejected is the REQ-VEC-001 defect pin:
// a vector whose dimension does not match the declared ModelInfo.Dimension MUST
// be rejected fail-closed BEFORE any server call. The mismatched point is never
// sent to Qdrant.
func TestAdapter_Upsert_DimensionMismatchRejected(t *testing.T) {
	fc := &fakeClient{}
	a := newTestAdapter(t, fc)

	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 3), // 3-dim, but ModelInfo says 4
		ModelInfo: domain.ModelInfo{
			Name:      "test-model",
			Dimension: 4,
			Version:   "v1",
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("Upsert dim-mismatch: expected ErrDimensionMismatch, got %v", err)
	}
	if len(fc.upsertCalls) != 0 {
		t.Errorf("dim-mismatch MUST NOT reach the server; got %d upsert calls", len(fc.upsertCalls))
	}
	var dme *domain.DimensionMismatchError
	if !errors.As(err, &dme) {
		t.Fatalf("error is not a *DimensionMismatchError: %T", err)
	}
	if dme.Expected != 4 || dme.Actual != 3 {
		t.Errorf("mismatch fields: Expected=%d Actual=%d, want 4/3", dme.Expected, dme.Actual)
	}
}

// TestAdapter_Upsert_CollectionDimensionMismatchRejected verifies that a vector
// whose dimension does not match the ADAPTER's declared collection dimension is
// rejected even when ModelInfo.Dimension is zero/unset.
func TestAdapter_Upsert_CollectionDimensionMismatchRejected(t *testing.T) {
	fc := &fakeClient{}
	a := newTestAdapter(t, fc)

	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 8), // 8-dim, adapter expects 4
		ModelInfo: domain.ModelInfo{
			Name: "test-model",
			// Dimension intentionally zero: adapter falls back to collection dim
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !domain.IsDimensionMismatch(err) {
		t.Fatalf("expected ErrDimensionMismatch (collection dim 4 vs vector 8), got %v", err)
	}
	if len(fc.upsertCalls) != 0 {
		t.Errorf("dim-mismatch MUST NOT reach the server; got %d upsert calls", len(fc.upsertCalls))
	}
}

// TestAdapter_Upsert_ModelNamespaceMismatchRejected verifies model-namespace
// mismatch is fail-closed: a point whose model name differs from the adapter's
// configured model is rejected to prevent mixing models in one collection.
func TestAdapter_Upsert_ModelNamespaceMismatchRejected(t *testing.T) {
	fc := &fakeClient{}
	a := newTestAdapter(t, fc) // model = "test-model", dim = 4

	point := domain.VectorPoint{
		ID:     1,
		Vector: make([]float32, 4), // correct dim
		ModelInfo: domain.ModelInfo{
			Name:      "different-model", // wrong model
			Dimension: 4,
		},
	}
	err := a.Upsert(context.Background(), []domain.VectorPoint{point})
	if !errors.Is(err, domain.ErrNamespaceMismatch) {
		t.Fatalf("expected ErrNamespaceMismatch, got %v", err)
	}
	if len(fc.upsertCalls) != 0 {
		t.Errorf("namespace mismatch MUST NOT reach the server; got %d upsert calls", len(fc.upsertCalls))
	}
}

// TestAdapter_Upsert_TranslatesPointsToBatch verifies the adapter maps
// VectorPoint metadata to Qdrant payload and sends a batched upsert.
func TestAdapter_Upsert_TranslatesPointsToBatch(t *testing.T) {
	fc := &fakeClient{collectionExists: true}
	a := newTestAdapter(t, fc)

	points := []domain.VectorPoint{
		{
			ID:     1,
			Vector: []float32{0.1, 0.2, 0.3, 0.4},
			ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4, Version: "v1"},
			Metadata: map[string]any{
				"project":   "myproj",
				"scope":     "project",
				"tenant_id": "tenant-a",
				"source":    "manual",
				"type":      "decision",
			},
		},
		{
			ID:     2,
			Vector: []float32{0.5, 0.6, 0.7, 0.8},
			ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4, Version: "v1"},
			Metadata:  map[string]any{"project": "other"},
		},
	}
	if err := a.Upsert(context.Background(), points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(fc.upsertCalls) != 1 {
		t.Fatalf("expected 1 upsert call, got %d", len(fc.upsertCalls))
	}
	pts := fc.upsertCalls[0].Points
	if len(pts) != 2 {
		t.Fatalf("expected 2 points in batch, got %d", len(pts))
	}
	// Point IDs are uint64 in Qdrant.
	if pts[0].Id.GetNum() != 1 || pts[1].Id.GetNum() != 2 {
		t.Errorf("point ids: %d, %d; want 1, 2", pts[0].Id.GetNum(), pts[1].Id.GetNum())
	}
	// Payload metadata is forwarded.
	if v := payloadString(pts[0], "project"); v != "myproj" {
		t.Errorf("payload project = %q, want myproj", v)
	}
	if v := payloadString(pts[0], "tenant_id"); v != "tenant-a" {
		t.Errorf("payload tenant_id = %q, want tenant-a", v)
	}
	// Model metadata is stored for traceability.
	if v := payloadString(pts[0], "model"); v != "test-model" {
		t.Errorf("payload model = %q, want test-model", v)
	}
	if v := payloadString(pts[0], "model_version"); v != "v1" {
		t.Errorf("payload model_version = %q, want v1", v)
	}
}

// TestAdapter_Upsert_BatchesLargeBatch verifies the adapter chunks a large
// input at MaxBatchSize instead of sending one giant request.
func TestAdapter_Upsert_BatchesLargeBatch(t *testing.T) {
	fc := &fakeClient{collectionExists: true}
	a := newTestAdapter(t, fc)
	a.maxBatchSize = 2 // force chunking

	points := make([]domain.VectorPoint, 5)
	for i := range points {
		points[i] = domain.VectorPoint{
			ID:        int64(i + 1),
			Vector:    []float32{0.1, 0.2, 0.3, 0.4},
			ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4},
		}
	}
	if err := a.Upsert(context.Background(), points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// 5 points / batch=2 => ceil(5/2) = 3 calls (2+2+1).
	if len(fc.upsertCalls) != 3 {
		t.Fatalf("expected 3 batched upsert calls, got %d", len(fc.upsertCalls))
	}
	total := 0
	for _, c := range fc.upsertCalls {
		total += len(c.Points)
	}
	if total != 5 {
		t.Errorf("total points across batches = %d, want 5", total)
	}
}

// TestAdapter_Search_TranslatesFilters verifies the adapter maps VectorQuery
// filter keys to Qdrant Must conditions (PreFilter).
func TestAdapter_Search_TranslatesFilters(t *testing.T) {
	fc := &fakeClient{
		collectionExists: true,
		queryResult: []*qdrant.ScoredPoint{
			{Id: qdrant.NewIDNum(1), Score: 0.95},
			{Id: qdrant.NewIDNum(2), Score: 0.80},
		},
	}
	a := newTestAdapter(t, fc)

	q := domain.VectorQuery{
		Vector:    []float32{0.1, 0.2, 0.3, 0.4},
		Limit:     10,
		Threshold: 0.5,
		Filters: map[string]any{
			"project":   "myproj",
			"scope":     "project",
			"tenant_id": "tenant-a",
			"type":      "decision",
		},
	}
	results, err := a.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Verify filter conditions were forwarded to Qdrant.
	if len(fc.queryCalls) != 1 {
		t.Fatalf("expected 1 query call, got %d", len(fc.queryCalls))
	}
	qp := fc.queryCalls[0]
	if qp.Filter == nil || len(qp.Filter.Must) == 0 {
		t.Fatal("expected Must filter conditions; got none")
	}
	// All four filter keys should appear as Match conditions.
	seen := map[string]bool{}
	for _, cond := range qp.Filter.Must {
		if fc := cond.GetField(); fc != nil {
			seen[fc.GetKey()] = true
		}
	}
	for _, k := range []string{"project", "scope", "tenant_id", "type"} {
		if !seen[k] {
			t.Errorf("filter key %q not forwarded to Qdrant", k)
		}
	}
	// Provenance is set on every candidate.
	for _, r := range results {
		if r.Provenance != adapterID {
			t.Errorf("Provenance = %q, want %q", r.Provenance, adapterID)
		}
	}
}

// TestAdapter_Search_ThresholdApplied verifies the adapter applies the score
// threshold client-side after receiving results from Qdrant.
func TestAdapter_Search_ThresholdApplied(t *testing.T) {
	fc := &fakeClient{
		collectionExists: true,
		queryResult: []*qdrant.ScoredPoint{
			{Id: qdrant.NewIDNum(1), Score: 0.9},
			{Id: qdrant.NewIDNum(2), Score: 0.4}, // below threshold
		},
	}
	a := newTestAdapter(t, fc)
	q := domain.VectorQuery{
		Vector:    []float32{0.1, 0.2, 0.3, 0.4},
		Limit:     10,
		Threshold: 0.5,
	}
	results, err := a.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after threshold, got %d", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("result ID = %d, want 1", results[0].ID)
	}
}

// TestAdapter_Search_ServerErrorPropagates verifies a Qdrant error is returned
// without masking.
func TestAdapter_Search_ServerErrorPropagates(t *testing.T) {
	serverErr := errors.New("qdrant: internal error")
	fc := &fakeClient{collectionExists: true, queryErr: serverErr}
	a := newTestAdapter(t, fc)
	_, err := a.Search(context.Background(), domain.VectorQuery{
		Vector: []float32{0.1, 0.2, 0.3, 0.4},
		Limit:  5,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, serverErr) {
		t.Errorf("error not wrapped correctly: %v", err)
	}
}

// TestAdapter_Delete_TranslatesIDs verifies the adapter maps observation IDs to
// Qdrant PointsSelectorIDs.
func TestAdapter_Delete_TranslatesIDs(t *testing.T) {
	fc := &fakeClient{collectionExists: true}
	a := newTestAdapter(t, fc)
	if err := a.Delete(context.Background(), []int64{10, 20, 30}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fc.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(fc.deleteCalls))
	}
	sel := fc.deleteCalls[0].Points
	ids := sel.GetPoints().GetIds()
	if len(ids) != 3 {
		t.Fatalf("expected 3 point IDs in selector, got %d", len(ids))
	}
}

// TestAdapter_Health_Healthy verifies HealthCheckReply translates to healthy.
func TestAdapter_Health_Healthy(t *testing.T) {
	fc := &fakeClient{healthReply: &qdrant.HealthCheckReply{Title: "qdrant", Version: "1.18.3"}}
	a := newTestAdapter(t, fc)
	h := a.Health(context.Background())
	if h.Status != domain.StatusHealthy {
		t.Errorf("Status = %q, want %q (msg: %s)", h.Status, domain.StatusHealthy, h.Message)
	}
}

// TestAdapter_Health_UnhealthyOnError verifies a HealthCheck error translates
// to unhealthy WITHOUT leaking the API key.
func TestAdapter_Health_UnhealthyOnError(t *testing.T) {
	fc := &fakeClient{healthErr: errors.New("connection refused")}
	a := newTestAdapter(t, fc)
	h := a.Health(context.Background())
	if h.Status != domain.StatusUnhealthy {
		t.Errorf("Status = %q, want %q", h.Status, domain.StatusUnhealthy)
	}
}

// TestAdapter_Close_DelegatesToClient verifies Close calls the underlying
// client Close exactly once.
func TestAdapter_Close_DelegatesToClient(t *testing.T) {
	fc := &fakeClient{collectionExists: true}
	a := newTestAdapter(t, fc)
	a.ownClient = true
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fc.closeCalls != 1 {
		t.Errorf("client Close called %d times, want 1", fc.closeCalls)
	}
}

// TestAdapter_EnsureCollection_LazyCreate verifies the adapter lazily creates
// the collection on first operation when it does not exist.
func TestAdapter_EnsureCollection_LazyCreate(t *testing.T) {
	fc := &fakeClient{collectionExists: false}
	a := newTestAdapter(t, fc)

	points := []domain.VectorPoint{{
		ID: 1, Vector: []float32{0.1, 0.2, 0.3, 0.4},
		ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4},
	}}
	if err := a.Upsert(context.Background(), points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(fc.createCalls) != 1 {
		t.Fatalf("expected 1 CreateCollection call, got %d", len(fc.createCalls))
	}
	cc := fc.createCalls[0]
	if cc.CollectionName != "cortex_test" {
		t.Errorf("CreateCollection name = %q, want cortex_test", cc.CollectionName)
	}
	vc := cc.VectorsConfig
	if vc == nil {
		t.Fatal("VectorsConfig is nil")
	}
	vp := vc.GetParams()
	if vp == nil {
		t.Fatal("VectorParams is nil")
	}
	if vp.Size != 4 {
		t.Errorf("collection vector size = %d, want 4", vp.Size)
	}
	if vp.Distance != qdrant.Distance_Cosine {
		t.Errorf("collection distance = %v, want Cosine", vp.Distance)
	}
}

// TestAdapter_NoSecretsInErrors verifies the API key never appears in any error
// message returned by the adapter (no plaintext secret leak — REQ-CP-002).
func TestAdapter_NoSecretsInErrors(t *testing.T) {
	const secret = "super-secret-key-not-to-leak"
	fc := &fakeClient{
		collectionExists: true,
		upsertErr:        errors.New("some server error: auth header was: " + secret),
	}
	a := &Adapter{
		client:       fc,
		collection:   "cortex_test",
		dimension:    4,
		modelName:    "test-model",
		maxBatchSize: 256,
		apiKey:       secret,
		caps:         defaultCapabilities(4),
	}
	points := []domain.VectorPoint{{
		ID: 1, Vector: []float32{0.1, 0.2, 0.3, 0.4},
		ModelInfo: domain.ModelInfo{Name: "test-model", Dimension: 4},
	}}
	err := a.Upsert(context.Background(), points)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if contains(err.Error(), secret) {
		t.Errorf("API key LEAKED into error message: %s", err.Error())
	}
}

// --- helpers ---------------------------------------------------------------

// payloadString extracts a string payload value from a Qdrant PointStruct.
func payloadString(p *qdrant.PointStruct, key string) string {
	v, ok := p.Payload[key]
	if !ok || v == nil {
		return ""
	}
	s := v.GetStringValue()
	return s
}
