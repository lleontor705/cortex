package temporal

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// --- Mock repositories ---

// mockGraphRepo implements only the GraphRepository methods that temporal.go calls.
type mockGraphRepo struct {
	createEdgeFunc              func(ctx context.Context, edge *domain.Edge) error
	getEdgesForObservationFunc  func(ctx context.Context, obsID int64) ([]*domain.Edge, error)
	getEdgeFunc                 func(ctx context.Context, id int64) (*domain.Edge, error)
	getEvolutionChainFunc       func(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error)
	countEdgesByObservationFunc func(ctx context.Context, obsID int64) (int, error)
	updateEdgeFunc              func(ctx context.Context, edge *domain.Edge) error
}

func (m *mockGraphRepo) CreateEdge(ctx context.Context, edge *domain.Edge) error {
	if m.createEdgeFunc != nil {
		return m.createEdgeFunc(ctx, edge)
	}
	return nil
}

func (m *mockGraphRepo) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	return nil, nil
}

func (m *mockGraphRepo) DeleteEdge(ctx context.Context, id int64) error {
	return nil
}

func (m *mockGraphRepo) GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	if m.getEdgesForObservationFunc != nil {
		return m.getEdgesForObservationFunc(ctx, obsID)
	}
	return nil, nil
}

func (m *mockGraphRepo) GetEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	if m.getEdgeFunc != nil {
		return m.getEdgeFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockGraphRepo) GetEvolutionChain(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error) {
	if m.getEvolutionChainFunc != nil {
		return m.getEvolutionChainFunc(ctx, fromObsID, toObsID)
	}
	return nil, nil
}

func (m *mockGraphRepo) CountEdgesByObservation(ctx context.Context, obsID int64) (int, error) {
	if m.countEdgesByObservationFunc != nil {
		return m.countEdgesByObservationFunc(ctx, obsID)
	}
	return 0, nil
}

func (m *mockGraphRepo) CountAllEdges(ctx context.Context) (int, error) {
	return 0, nil
}

func (m *mockGraphRepo) GetContradictions(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
	return nil, nil
}

func (m *mockGraphRepo) UpdateEdge(ctx context.Context, edge *domain.Edge) error {
	if m.updateEdgeFunc != nil {
		return m.updateEdgeFunc(ctx, edge)
	}
	return nil
}

// mockObservationRepo implements only the ObservationRepository methods that temporal.go calls.
type mockObservationRepo struct {
	countByRootFunc func(ctx context.Context, rootObsID int64) (int, error)
	getByIDFunc     func(ctx context.Context, id int64) (*domain.Observation, error)
}

func (m *mockObservationRepo) Save(ctx context.Context, obs *domain.Observation) error { return nil }
func (m *mockObservationRepo) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, fmt.Errorf("not found")
}
func (m *mockObservationRepo) GetByTopicKey(ctx context.Context, project, topicKey string) (*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) Update(ctx context.Context, obs *domain.Observation) error { return nil }
func (m *mockObservationRepo) Delete(ctx context.Context, id int64) error                { return nil }
func (m *mockObservationRepo) List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) CountAll(ctx context.Context) (int, error) { return 0, nil }
func (m *mockObservationRepo) CountByRoot(ctx context.Context, rootObsID int64) (int, error) {
	if m.countByRootFunc != nil {
		return m.countByRootFunc(ctx, rootObsID)
	}
	return 0, nil
}
func (m *mockObservationRepo) GetBySource(ctx context.Context, source string, limit int) ([]*domain.Observation, error) {
	return nil, nil
}
func (m *mockObservationRepo) GetByType(ctx context.Context, obsType string, limit int) ([]*domain.Observation, error) {
	return nil, nil
}

// mockSnapshotRepo implements only the TemporalSnapshotRepository method that temporal.go calls.
type mockSnapshotRepo struct {
	createSnapshotFunc func(ctx context.Context, snapshot *domain.TemporalSnapshot) error
}

func (m *mockSnapshotRepo) CreateSnapshot(ctx context.Context, snapshot *domain.TemporalSnapshot) error {
	if m.createSnapshotFunc != nil {
		return m.createSnapshotFunc(ctx, snapshot)
	}
	return nil
}

func (m *mockSnapshotRepo) GetByID(ctx context.Context, id int64) (*domain.TemporalSnapshot, error) {
	return nil, nil
}

func (m *mockSnapshotRepo) GetBySnapshotKey(ctx context.Context, snapshotKey string) ([]*domain.TemporalSnapshot, error) {
	return nil, nil
}

func (m *mockSnapshotRepo) GetSnapshotsInRange(ctx context.Context, from, to time.Time) ([]*domain.TemporalSnapshot, error) {
	return nil, nil
}

func (m *mockSnapshotRepo) GetByRootObservation(ctx context.Context, rootObsID int64) ([]*domain.TemporalSnapshot, error) {
	return nil, nil
}

// mockMetricsRepo implements only the MetricsRepository method that temporal.go calls.
type mockMetricsRepo struct {
	getTemporalMetricsFunc func(ctx context.Context, sessionID string, from, to time.Time) ([]*domain.Metrics, error)
}

func (m *mockMetricsRepo) CreateMetric(ctx context.Context, metric *domain.Metrics) error {
	return nil
}

func (m *mockMetricsRepo) GetTemporalMetrics(ctx context.Context, sessionID string, from, to time.Time) ([]*domain.Metrics, error) {
	if m.getTemporalMetricsFunc != nil {
		return m.getTemporalMetricsFunc(ctx, sessionID, from, to)
	}
	return nil, nil
}

func (m *mockMetricsRepo) GetByOperationType(ctx context.Context, operationType string, from, to time.Time) ([]*domain.Metrics, error) {
	return nil, nil
}

func (m *mockMetricsRepo) GetAggregatedMetrics(ctx context.Context, from, to time.Time) (*domain.AggregatedMetrics, error) {
	return nil, nil
}

// --- Helper ---

func newTestService(
	graphRepo *mockGraphRepo,
	obsRepo *mockObservationRepo,
	snapRepo *mockSnapshotRepo,
	metricsRepo *mockMetricsRepo,
) *TemporalService {
	if graphRepo == nil {
		graphRepo = &mockGraphRepo{}
	}
	if obsRepo == nil {
		obsRepo = &mockObservationRepo{}
	}
	if snapRepo == nil {
		snapRepo = &mockSnapshotRepo{}
	}
	if metricsRepo == nil {
		metricsRepo = &mockMetricsRepo{}
	}
	return NewTemporalService(graphRepo, obsRepo, snapRepo, metricsRepo)
}

// --- Tests ---

func TestCreateTemporalEdge_Success(t *testing.T) {
	var savedEdge *domain.Edge
	graphRepo := &mockGraphRepo{
		createEdgeFunc: func(ctx context.Context, edge *domain.Edge) error {
			savedEdge = edge
			return nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	edge := &domain.Edge{
		FromObsID:    1,
		ToObsID:      2,
		RelationType: domain.RelationReferences,
		Weight:       1.0,
	}

	err := svc.CreateTemporalEdge(context.Background(), edge)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if savedEdge == nil {
		t.Fatal("expected edge to be saved")
	}

	// Verify ValidFrom was set to a default (non-nil)
	if savedEdge.ValidFrom == nil {
		t.Error("expected ValidFrom to be set by default")
	}

	// Verify EvolutionType defaults to "original"
	if savedEdge.EvolutionType != domain.EvolutionOriginal {
		t.Errorf("expected EvolutionType=%q, got %q", domain.EvolutionOriginal, savedEdge.EvolutionType)
	}

	// Verify FactState defaults to "current"
	if savedEdge.FactState != domain.FactStateCurrent {
		t.Errorf("expected FactState=%q, got %q", domain.FactStateCurrent, savedEdge.FactState)
	}
}

func TestCreateTemporalEdge_InvalidTimeRange(t *testing.T) {
	graphRepo := &mockGraphRepo{
		createEdgeFunc: func(ctx context.Context, edge *domain.Edge) error {
			t.Fatal("CreateEdge should not be called for invalid time range")
			return nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	validFrom := time.Now()
	invalidAt := validFrom.Add(-1 * time.Hour) // Before ValidFrom

	edge := &domain.Edge{
		FromObsID:    1,
		ToObsID:      2,
		RelationType: domain.RelationReferences,
		ValidFrom:    &validFrom,
		InvalidAt:    &invalidAt,
	}

	err := svc.CreateTemporalEdge(context.Background(), edge)
	if err == nil {
		t.Fatal("expected error for invalid time range, got nil")
	}

	expectedMsg := "invalid_at must be after valid_from"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestGetTemporalEdges_Success(t *testing.T) {
	now := time.Now()
	pastHour := now.Add(-1 * time.Hour)
	futureHour := now.Add(1 * time.Hour)

	edges := []*domain.Edge{
		{
			ID:           1,
			FromObsID:    10,
			ToObsID:      20,
			RelationType: domain.RelationReferences,
			ValidFrom:    &pastHour,
			InvalidAt:    &futureHour,
		},
		{
			ID:           2,
			FromObsID:    10,
			ToObsID:      30,
			RelationType: domain.RelationFollows,
			ValidFrom:    &pastHour,
			// No InvalidAt -- still valid
		},
	}

	graphRepo := &mockGraphRepo{
		getEdgesForObservationFunc: func(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
			if obsID == 10 {
				return edges, nil
			}
			return nil, nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	result, err := svc.GetTemporalEdges(context.Background(), 10, now)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(result))
	}
}

func TestGetTemporalEdges_FiltersExpired(t *testing.T) {
	now := time.Now()
	pastTwo := now.Add(-2 * time.Hour)
	pastOne := now.Add(-1 * time.Hour)
	futureOne := now.Add(1 * time.Hour)

	edges := []*domain.Edge{
		{
			ID:           1,
			FromObsID:    10,
			ToObsID:      20,
			RelationType: domain.RelationReferences,
			ValidFrom:    &pastTwo,
			InvalidAt:    &pastOne, // Already expired
		},
		{
			ID:           2,
			FromObsID:    10,
			ToObsID:      30,
			RelationType: domain.RelationFollows,
			ValidFrom:    &pastTwo,
			InvalidAt:    &futureOne, // Still valid
		},
	}

	graphRepo := &mockGraphRepo{
		getEdgesForObservationFunc: func(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
			return edges, nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	result, err := svc.GetTemporalEdges(context.Background(), 10, now)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 valid edge (expired edge filtered), got %d", len(result))
	}

	if result[0].ID != 2 {
		t.Errorf("expected edge ID=2 (still valid), got ID=%d", result[0].ID)
	}
}

func TestHandleTemporalContradiction_Success(t *testing.T) {
	var createdEdges []*domain.Edge
	var updatedEdge *domain.Edge

	graphRepo := &mockGraphRepo{
		createEdgeFunc: func(ctx context.Context, edge *domain.Edge) error {
			createdEdges = append(createdEdges, edge)
			return nil
		},
		updateEdgeFunc: func(ctx context.Context, edge *domain.Edge) error {
			updatedEdge = edge
			return nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	olderTime := time.Now().Add(-2 * time.Hour)
	newerTime := time.Now().Add(-1 * time.Hour)

	factA := &domain.Edge{
		ID:           1,
		FromObsID:    10,
		ToObsID:      20,
		RelationType: domain.RelationReferences,
		Weight:       1.0,
		Confidence:   0.8,
		CreatedAt:    olderTime,
	}
	factB := &domain.Edge{
		ID:           2,
		FromObsID:    10,
		ToObsID:      30,
		RelationType: domain.RelationReferences,
		Weight:       1.5,
		Confidence:   0.9,
		CreatedAt:    newerTime,
	}

	result, err := svc.HandleTemporalContradiction(context.Background(), factA, factB)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// A contradiction edge should be created for the older fact
	if len(createdEdges) != 1 {
		t.Fatalf("expected 1 created contradiction edge, got %d", len(createdEdges))
	}

	contradictionEdge := createdEdges[0]
	if contradictionEdge.RelationType != domain.RelationContradicts {
		t.Errorf("expected relation=%q, got %q", domain.RelationContradicts, contradictionEdge.RelationType)
	}
	if contradictionEdge.EvolutionType != domain.EvolutionContradicted {
		t.Errorf("expected evolution=%q, got %q", domain.EvolutionContradicted, contradictionEdge.EvolutionType)
	}
	if contradictionEdge.FactState != domain.FactStateDeprecated {
		t.Errorf("expected fact_state=%q, got %q", domain.FactStateDeprecated, contradictionEdge.FactState)
	}
	// Contradiction edge should reference the older fact (factA)
	if contradictionEdge.FromObsID != factA.FromObsID || contradictionEdge.ToObsID != factA.ToObsID {
		t.Error("contradiction edge should reference the older fact's observation IDs")
	}

	// The superseded edge should be returned and updated
	if result == nil {
		t.Fatal("expected superseded edge result")
	}
	if updatedEdge == nil {
		t.Fatal("expected UpdateEdge to be called")
	}
	if updatedEdge.EvolutionType != domain.EvolutionSuperseded {
		t.Errorf("expected updated edge evolution=%q, got %q", domain.EvolutionSuperseded, updatedEdge.EvolutionType)
	}
	if updatedEdge.FactState != domain.FactStateCurrent {
		t.Errorf("expected updated edge fact_state=%q, got %q", domain.FactStateCurrent, updatedEdge.FactState)
	}
}

func TestCreateTemporalSnapshot_Success(t *testing.T) {
	var savedSnapshot *domain.TemporalSnapshot

	obsRepo := &mockObservationRepo{
		countByRootFunc: func(ctx context.Context, rootObsID int64) (int, error) {
			return 42, nil
		},
	}

	graphRepo := &mockGraphRepo{
		countEdgesByObservationFunc: func(ctx context.Context, obsID int64) (int, error) {
			return 15, nil
		},
	}

	snapRepo := &mockSnapshotRepo{
		createSnapshotFunc: func(ctx context.Context, snapshot *domain.TemporalSnapshot) error {
			savedSnapshot = snapshot
			return nil
		},
	}

	svc := newTestService(graphRepo, obsRepo, snapRepo, nil)

	result, err := svc.CreateTemporalSnapshot(context.Background(), "snap-v1", 100, "First snapshot")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected snapshot result")
	}

	if savedSnapshot == nil {
		t.Fatal("expected snapshot to be persisted")
	}

	if savedSnapshot.SnapshotKey != "snap-v1" {
		t.Errorf("expected SnapshotKey=%q, got %q", "snap-v1", savedSnapshot.SnapshotKey)
	}
	if savedSnapshot.Description != "First snapshot" {
		t.Errorf("expected Description=%q, got %q", "First snapshot", savedSnapshot.Description)
	}
	if savedSnapshot.ObservationCount != 42 {
		t.Errorf("expected ObservationCount=42, got %d", savedSnapshot.ObservationCount)
	}
	if savedSnapshot.EdgeCount != 15 {
		t.Errorf("expected EdgeCount=15, got %d", savedSnapshot.EdgeCount)
	}
	if savedSnapshot.RootObservationID != 100 {
		t.Errorf("expected RootObservationID=100, got %d", savedSnapshot.RootObservationID)
	}
	if savedSnapshot.Timestamp.IsZero() {
		t.Error("expected Timestamp to be set")
	}
}

func TestGetEvolutionPath_Success(t *testing.T) {
	evolutionID := int64(99)

	baseEdge := &domain.Edge{
		ID:          1,
		FromObsID:   10,
		ToObsID:     20,
		EvolutionID: &evolutionID,
	}

	chainEdges := []*domain.Edge{
		{
			ID:            1,
			FromObsID:     10,
			ToObsID:       20,
			EvolutionID:   &evolutionID,
			EvolutionType: domain.EvolutionOriginal,
		},
		{
			ID:            2,
			FromObsID:     10,
			ToObsID:       20,
			EvolutionID:   &evolutionID,
			EvolutionType: domain.EvolutionModified,
		},
		{
			ID:            3,
			FromObsID:     10,
			ToObsID:       20,
			EvolutionID:   func() *int64 { v := int64(77); return &v }(), // Different evolution chain
			EvolutionType: domain.EvolutionOriginal,
		},
	}

	graphRepo := &mockGraphRepo{
		getEdgeFunc: func(ctx context.Context, id int64) (*domain.Edge, error) {
			if id == 1 {
				return baseEdge, nil
			}
			return nil, fmt.Errorf("not found")
		},
		getEvolutionChainFunc: func(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error) {
			return chainEdges, nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	result, err := svc.GetEvolutionPath(context.Background(), 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should only include edges with matching EvolutionID=99 (edges 1 and 2, not 3)
	if len(result) != 2 {
		t.Fatalf("expected 2 edges in evolution path, got %d", len(result))
	}

	for _, e := range result {
		if e.EvolutionID == nil || *e.EvolutionID != evolutionID {
			t.Errorf("expected EvolutionID=%d, got %v", evolutionID, e.EvolutionID)
		}
	}
}

func TestGetCurrentFactState_Success(t *testing.T) {
	now := time.Now()
	pastHour := now.Add(-1 * time.Hour)
	futureHour := now.Add(1 * time.Hour)
	pastTwo := now.Add(-2 * time.Hour)

	edges := []*domain.Edge{
		{
			ID:           1,
			FromObsID:    10,
			ToObsID:      20,
			RelationType: domain.RelationReferences,
			ValidFrom:    &pastHour,
			InvalidAt:    &futureHour,
			FactState:    domain.FactStateCurrent,
		},
		{
			ID:           2,
			FromObsID:    10,
			ToObsID:      30,
			RelationType: domain.RelationFollows,
			ValidFrom:    &pastHour,
			FactState:    domain.FactStateDeprecated, // Not current
		},
		{
			ID:           3,
			FromObsID:    10,
			ToObsID:      40,
			RelationType: domain.RelationReferences,
			ValidFrom:    &pastTwo,
			InvalidAt:    &pastHour, // Expired
			FactState:    domain.FactStateCurrent,
		},
	}

	graphRepo := &mockGraphRepo{
		getEdgesForObservationFunc: func(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
			return edges, nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	result, err := svc.GetCurrentFactState(context.Background(), 10)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Only edge 1 is both valid at now AND has FactStateCurrent
	// Edge 2 is valid but deprecated; Edge 3 is current but expired
	if len(result) != 1 {
		t.Fatalf("expected 1 current fact, got %d", len(result))
	}

	key := fmt.Sprintf("%s_%d", domain.RelationReferences, int64(20))
	if _, ok := result[key]; !ok {
		t.Errorf("expected key %q in result map", key)
	}
}

func TestGetTemporalEdges_RepoError(t *testing.T) {
	expectedErr := errors.New("db connection failed")

	graphRepo := &mockGraphRepo{
		getEdgesForObservationFunc: func(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
			return nil, expectedErr
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	_, err := svc.GetTemporalEdges(context.Background(), 10, time.Now())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestCreateTemporalSnapshot_CountByRootError(t *testing.T) {
	expectedErr := errors.New("count failed")

	obsRepo := &mockObservationRepo{
		countByRootFunc: func(ctx context.Context, rootObsID int64) (int, error) {
			return 0, expectedErr
		},
	}

	svc := newTestService(nil, obsRepo, nil, nil)

	_, err := svc.CreateTemporalSnapshot(context.Background(), "snap", 1, "desc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestCreateTemporalEdge_PreservesExistingValues(t *testing.T) {
	var savedEdge *domain.Edge
	graphRepo := &mockGraphRepo{
		createEdgeFunc: func(ctx context.Context, edge *domain.Edge) error {
			savedEdge = edge
			return nil
		},
	}

	svc := newTestService(graphRepo, nil, nil, nil)

	validFrom := time.Now().Add(-1 * time.Hour)
	edge := &domain.Edge{
		FromObsID:     1,
		ToObsID:       2,
		RelationType:  domain.RelationFollows,
		ValidFrom:     &validFrom,
		EvolutionType: domain.EvolutionModified,
		FactState:     domain.FactStateHistorical,
	}

	err := svc.CreateTemporalEdge(context.Background(), edge)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should preserve the explicitly set values, not overwrite with defaults
	if savedEdge.ValidFrom != &validFrom {
		t.Error("expected ValidFrom to be preserved")
	}
	if savedEdge.EvolutionType != domain.EvolutionModified {
		t.Errorf("expected EvolutionType=%q, got %q", domain.EvolutionModified, savedEdge.EvolutionType)
	}
	if savedEdge.FactState != domain.FactStateHistorical {
		t.Errorf("expected FactState=%q, got %q", domain.FactStateHistorical, savedEdge.FactState)
	}
}
