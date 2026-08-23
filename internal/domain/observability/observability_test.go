package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// --- Mock implementations ---

type mockMetricsRepo struct {
	CreateMetricFn       func(ctx context.Context, metric *domain.Metrics) error
	GetTemporalMetricsFn func(ctx context.Context, sessionID string, from, to time.Time) ([]*domain.Metrics, error)
}

func (m *mockMetricsRepo) CreateMetric(ctx context.Context, metric *domain.Metrics) error {
	if m.CreateMetricFn != nil {
		return m.CreateMetricFn(ctx, metric)
	}
	return nil
}

func (m *mockMetricsRepo) GetTemporalMetrics(ctx context.Context, sessionID string, from, to time.Time) ([]*domain.Metrics, error) {
	if m.GetTemporalMetricsFn != nil {
		return m.GetTemporalMetricsFn(ctx, sessionID, from, to)
	}
	return nil, nil
}

func (m *mockMetricsRepo) GetByOperationType(ctx context.Context, operationType string, from, to time.Time) ([]*domain.Metrics, error) {
	return nil, nil
}

func (m *mockMetricsRepo) GetAggregatedMetrics(ctx context.Context, from, to time.Time) (*domain.AggregatedMetrics, error) {
	return nil, nil
}

type mockQualityRepo struct {
	CreateQualityMetricFn func(ctx context.Context, quality *domain.QualityMetrics) error
}

func (m *mockQualityRepo) CreateQualityMetric(ctx context.Context, quality *domain.QualityMetrics) error {
	if m.CreateQualityMetricFn != nil {
		return m.CreateQualityMetricFn(ctx, quality)
	}
	return nil
}

func (m *mockQualityRepo) GetBySession(ctx context.Context, sessionID string, limit int) ([]*domain.QualityMetrics, error) {
	return nil, nil
}

func (m *mockQualityRepo) GetByType(ctx context.Context, evaluationType string, from, to time.Time) ([]*domain.QualityMetrics, error) {
	return nil, nil
}

func (m *mockQualityRepo) GetLatest(ctx context.Context, limit int) ([]*domain.QualityMetrics, error) {
	return nil, nil
}

type mockTemporalSnapshotRepo struct {
	GetSnapshotsInRangeFn func(ctx context.Context, from, to time.Time) ([]*domain.TemporalSnapshot, error)
}

func (m *mockTemporalSnapshotRepo) CreateSnapshot(ctx context.Context, snapshot *domain.TemporalSnapshot) error {
	return nil
}

func (m *mockTemporalSnapshotRepo) GetByID(ctx context.Context, id int64) (*domain.TemporalSnapshot, error) {
	return nil, nil
}

func (m *mockTemporalSnapshotRepo) GetBySnapshotKey(ctx context.Context, snapshotKey string) ([]*domain.TemporalSnapshot, error) {
	return nil, nil
}

func (m *mockTemporalSnapshotRepo) GetSnapshotsInRange(ctx context.Context, from, to time.Time) ([]*domain.TemporalSnapshot, error) {
	if m.GetSnapshotsInRangeFn != nil {
		return m.GetSnapshotsInRangeFn(ctx, from, to)
	}
	return nil, nil
}

func (m *mockTemporalSnapshotRepo) GetByRootObservation(ctx context.Context, rootObsID int64) ([]*domain.TemporalSnapshot, error) {
	return nil, nil
}

type mockGraphRepo struct {
	CountAllEdgesFn           func(ctx context.Context) (int, error)
	GetContradictionsFn       func(ctx context.Context, from, to time.Time) ([]*domain.Edge, error)
	CountEdgesByObservationFn func(ctx context.Context, obsID int64) (int, error)
}

func (m *mockGraphRepo) CreateEdge(ctx context.Context, edge *domain.Edge) error { return nil }
func (m *mockGraphRepo) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	return nil, nil
}
func (m *mockGraphRepo) DeleteEdge(ctx context.Context, id int64) error { return nil }
func (m *mockGraphRepo) GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	return nil, nil
}
func (m *mockGraphRepo) GetEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	return nil, nil
}
func (m *mockGraphRepo) GetEvolutionChain(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error) {
	return nil, nil
}
func (m *mockGraphRepo) UpdateEdge(ctx context.Context, edge *domain.Edge) error { return nil }

func (m *mockGraphRepo) CountEdgesByObservation(ctx context.Context, obsID int64) (int, error) {
	if m.CountEdgesByObservationFn != nil {
		return m.CountEdgesByObservationFn(ctx, obsID)
	}
	return 0, nil
}

func (m *mockGraphRepo) CountAllEdges(ctx context.Context) (int, error) {
	if m.CountAllEdgesFn != nil {
		return m.CountAllEdgesFn(ctx)
	}
	return 0, nil
}

func (m *mockGraphRepo) GetContradictions(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
	if m.GetContradictionsFn != nil {
		return m.GetContradictionsFn(ctx, from, to)
	}
	return nil, nil
}

type mockObservationRepo struct {
	CountAllFn func(ctx context.Context) (int, error)
}

func (m *mockObservationRepo) Save(ctx context.Context, obs *domain.Observation) error { return nil }
func (m *mockObservationRepo) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) GetByTopicKey(ctx context.Context, project, topicKey string) (*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) Update(ctx context.Context, obs *domain.Observation) error { return nil }
func (m *mockObservationRepo) Delete(ctx context.Context, id int64) error                { return nil }
func (m *mockObservationRepo) List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) CountByRoot(ctx context.Context, rootObsID int64) (int, error) {
	return 0, nil
}
func (m *mockObservationRepo) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) GetByType(ctx context.Context, obsType string, limit int) ([]*domain.Observation, error) {
	return nil, nil
}

func (m *mockObservationRepo) CountAll(ctx context.Context) (int, error) {
	if m.CountAllFn != nil {
		return m.CountAllFn(ctx)
	}
	return 0, nil
}

// --- Helper to build service with defaults ---

type testDeps struct {
	metrics     *mockMetricsRepo
	quality     *mockQualityRepo
	temporal    *mockTemporalSnapshotRepo
	graph       *mockGraphRepo
	observation *mockObservationRepo
}

func newTestDeps() *testDeps {
	return &testDeps{
		metrics:     &mockMetricsRepo{},
		quality:     &mockQualityRepo{},
		temporal:    &mockTemporalSnapshotRepo{},
		graph:       &mockGraphRepo{},
		observation: &mockObservationRepo{},
	}
}

func (d *testDeps) service() *ObservabilityService {
	return NewObservabilityService(d.metrics, d.quality, d.temporal, d.graph, d.observation)
}

// --- Tests ---

func TestRecordOperation_Success(t *testing.T) {
	deps := newTestDeps()
	var captured *domain.Metrics
	deps.metrics.CreateMetricFn = func(ctx context.Context, metric *domain.Metrics) error {
		captured = metric
		return nil
	}
	svc := deps.service()

	op := &domain.Metrics{
		SessionID:     "sess-1",
		OperationType: "save",
		Duration:      42,
		Success:       true,
	}

	err := svc.RecordOperation(context.Background(), op)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if captured == nil {
		t.Fatal("CreateMetric was not called")
	}
	if captured.OperationType != "save" {
		t.Errorf("expected operation_type 'save', got %q", captured.OperationType)
	}
}

func TestRecordOperation_Error(t *testing.T) {
	deps := newTestDeps()
	wantErr := errors.New("db write failed")
	deps.metrics.CreateMetricFn = func(ctx context.Context, metric *domain.Metrics) error {
		return wantErr
	}
	svc := deps.service()

	err := svc.RecordOperation(context.Background(), &domain.Metrics{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

func TestGetSystemMetrics_Success(t *testing.T) {
	deps := newTestDeps()
	now := time.Now()
	from := now.Add(-1 * time.Hour)

	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{
			{Duration: 100, Success: true, MemoryUsage: 1000, ObservationCount: 5, EdgeCount: 3, QueryComplexity: 0.5, ConfidenceScore: 0.9},
			{Duration: 200, Success: true, MemoryUsage: 2000, ObservationCount: 10, EdgeCount: 7, QueryComplexity: 0.8, ConfidenceScore: 0.7},
			{Duration: 300, Success: false, MemoryUsage: 500, ObservationCount: 2, EdgeCount: 1, QueryComplexity: 0.3, ConfidenceScore: 0.5},
		}, nil
	}

	svc := deps.service()
	result, err := svc.GetSystemMetrics(context.Background(), "sess-1", from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalOperations != 3 {
		t.Errorf("expected 3 total operations, got %d", result.TotalOperations)
	}
	if result.SuccessfulOps != 2 {
		t.Errorf("expected 2 successful ops, got %d", result.SuccessfulOps)
	}
	if result.FailedOps != 1 {
		t.Errorf("expected 1 failed op, got %d", result.FailedOps)
	}
	// avg duration = (100+200+300)/3 = 200
	if result.AvgDurationMs != 200.0 {
		t.Errorf("expected avg duration 200.0, got %f", result.AvgDurationMs)
	}
	if result.TotalMemoryUsage != 3500 {
		t.Errorf("expected total memory 3500, got %d", result.TotalMemoryUsage)
	}
	if result.TotalObservations != 17 {
		t.Errorf("expected 17 total observations, got %d", result.TotalObservations)
	}
	if result.TotalEdges != 11 {
		t.Errorf("expected 11 total edges, got %d", result.TotalEdges)
	}
	if result.SessionID != "sess-1" {
		t.Errorf("expected session_id 'sess-1', got %q", result.SessionID)
	}
}

func TestGetSystemMetrics_Empty(t *testing.T) {
	deps := newTestDeps()
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{}, nil
	}

	svc := deps.service()
	now := time.Now()
	result, err := svc.GetSystemMetrics(context.Background(), "sess-1", now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalOperations != 0 {
		t.Errorf("expected 0 operations, got %d", result.TotalOperations)
	}
	if result.AvgDurationMs != 0.0 {
		t.Errorf("expected avg duration 0, got %f", result.AvgDurationMs)
	}
	if result.SuccessfulOps != 0 {
		t.Errorf("expected 0 successful ops, got %d", result.SuccessfulOps)
	}
	if result.FailedOps != 0 {
		t.Errorf("expected 0 failed ops, got %d", result.FailedOps)
	}
}

func TestEvaluateMemoryQuality_Relevance(t *testing.T) {
	deps := newTestDeps()
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{
			{OperationType: "search", Success: true, ResultCount: 5, Duration: 100, ConfidenceScore: 0.8},
			{OperationType: "search", Success: true, ResultCount: 3, Duration: 200, ConfidenceScore: 0.6},
			{OperationType: "search", Success: false, ResultCount: 0, Duration: 50, ConfidenceScore: 0.0},
		}, nil
	}
	var savedQuality *domain.QualityMetrics
	deps.quality.CreateQualityMetricFn = func(ctx context.Context, quality *domain.QualityMetrics) error {
		savedQuality = quality
		return nil
	}

	svc := deps.service()
	result, err := svc.EvaluateMemoryQuality(context.Background(), "sess-1", "relevance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 successful searches out of 3 total => score = 2/3
	expectedScore := 2.0 / 3.0
	if abs(result.Score-expectedScore) > 0.001 {
		t.Errorf("expected score ~%f, got %f", expectedScore, result.Score)
	}
	if result.TotalQueries != 3 {
		t.Errorf("expected 3 total queries, got %d", result.TotalQueries)
	}
	if result.SuccessfulRetrievals != 2 {
		t.Errorf("expected 2 successful retrievals, got %d", result.SuccessfulRetrievals)
	}
	if result.EvaluationType != "relevance" {
		t.Errorf("expected evaluation_type 'relevance', got %q", result.EvaluationType)
	}
	if savedQuality == nil {
		t.Fatal("CreateQualityMetric was not called")
	}
}

func TestEvaluateMemoryQuality_Consistency(t *testing.T) {
	deps := newTestDeps()
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{}, nil
	}
	deps.graph.GetContradictionsFn = func(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
		return []*domain.Edge{}, nil // 0 contradictions
	}
	deps.graph.CountAllEdgesFn = func(ctx context.Context) (int, error) {
		return 10, nil
	}
	deps.quality.CreateQualityMetricFn = func(ctx context.Context, quality *domain.QualityMetrics) error {
		return nil
	}

	svc := deps.service()
	result, err := svc.EvaluateMemoryQuality(context.Background(), "sess-1", "consistency")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 0 contradictions / 10 edges => consistency = 1.0 - 0/10 = 1.0
	if result.Score != 1.0 {
		t.Errorf("expected consistency score 1.0, got %f", result.Score)
	}
}

func TestEvaluateMemoryQuality_Overall(t *testing.T) {
	deps := newTestDeps()

	// Provide search operations for relevance dimension
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{
			{OperationType: "search", Success: true, ResultCount: 5, Duration: 100, ConfidenceScore: 0.8},
		}, nil
	}

	// Completeness dependencies
	deps.observation.CountAllFn = func(ctx context.Context) (int, error) { return 100, nil }
	deps.graph.CountAllEdgesFn = func(ctx context.Context) (int, error) { return 10, nil }

	// Consistency: 0 contradictions
	deps.graph.GetContradictionsFn = func(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
		return []*domain.Edge{}, nil
	}

	// Temporal accuracy: no snapshots => score 0
	deps.temporal.GetSnapshotsInRangeFn = func(ctx context.Context, from, to time.Time) ([]*domain.TemporalSnapshot, error) {
		return []*domain.TemporalSnapshot{}, nil
	}

	deps.quality.CreateQualityMetricFn = func(ctx context.Context, quality *domain.QualityMetrics) error {
		return nil
	}

	svc := deps.service()
	result, err := svc.EvaluateMemoryQuality(context.Background(), "sess-1", "overall")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Relevance: 1 search, 1 successful => score = 1.0
	// Completeness: no "save" or "get_related" ops => score = 0
	// Consistency: 0 contradictions / 10 edges => 1.0
	// Temporal: no snapshots => 0
	// Weighted: (1.0*0.3) + (0*0.25) + (1.0*0.25) + (0*0.2) = 0.55
	expectedScore := 0.55
	if abs(result.Score-expectedScore) > 0.001 {
		t.Errorf("expected overall score ~%f, got %f", expectedScore, result.Score)
	}
	if result.EvaluationType != "overall" {
		t.Errorf("expected evaluation_type 'overall', got %q", result.EvaluationType)
	}
}

func TestEvaluateMemoryQuality_UnsupportedType(t *testing.T) {
	deps := newTestDeps()
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{}, nil
	}

	svc := deps.service()
	_, err := svc.EvaluateMemoryQuality(context.Background(), "sess-1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unsupported evaluation type, got nil")
	}
	expected := "unsupported evaluation type: nonexistent"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestGetHealthCheck_Healthy(t *testing.T) {
	deps := newTestDeps()
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		return []*domain.Metrics{
			{Success: true, Duration: 50},
			{Success: true, Duration: 100},
			{Success: true, Duration: 80},
			{Success: true, Duration: 60},
			{Success: true, Duration: 90},
		}, nil
	}

	svc := deps.service()
	result, err := svc.GetHealthCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", result.Status)
	}
	if result.TotalOperations != 5 {
		t.Errorf("expected 5 total operations, got %d", result.TotalOperations)
	}
	if result.FailedOperations != 0 {
		t.Errorf("expected 0 failed operations, got %d", result.FailedOperations)
	}
	if result.SlowOperations != 0 {
		t.Errorf("expected 0 slow operations, got %d", result.SlowOperations)
	}
}

func TestGetHealthCheck_Degraded(t *testing.T) {
	deps := newTestDeps()
	// 2 out of 10 failed => 20% failure rate > 10% threshold
	deps.metrics.GetTemporalMetricsFn = func(ctx context.Context, sessionID string, f, to time.Time) ([]*domain.Metrics, error) {
		ops := make([]*domain.Metrics, 10)
		for i := 0; i < 10; i++ {
			ops[i] = &domain.Metrics{Success: true, Duration: 50}
		}
		ops[0].Success = false
		ops[1].Success = false
		return ops, nil
	}

	svc := deps.service()
	result, err := svc.GetHealthCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "degraded" {
		t.Errorf("expected status 'degraded', got %q", result.Status)
	}
	if result.FailedOperations != 2 {
		t.Errorf("expected 2 failed operations, got %d", result.FailedOperations)
	}
}

// abs returns the absolute value of a float64.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
