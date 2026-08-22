// Package purego provides a zero-CGO, pure Go VectorIndex implementation.
// It performs in-memory cosine similarity scanning over vector embeddings,
// allowing fully functional semantic search without external database extensions or build tags.
package purego

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/lleontor705/cortex/internal/domain"
)

const adapterID = "purego_cosine"

type pointEntry struct {
	ID        int64
	Vector    []float32
	Model     domain.ModelInfo
	Magnitude float64
}

// Adapter implements domain.VectorIndex in pure Go.
type Adapter struct {
	mu     sync.RWMutex
	points map[int64]*pointEntry
	caps   domain.Capabilities
}

// New creates a new pure Go vector index adapter.
func New() *Adapter {
	return &Adapter{
		points: make(map[int64]*pointEntry),
		caps: domain.Capabilities{
			IndexType:       adapterID,
			DistanceMetrics: []string{"cosine"},
			MaxDimensions:   4096,
			Filters:         "PreFilter",
			Hybrid:          "supported",
			Namespaces:      "supported",
			Consistency:     "strong",
			BatchUpsert:     true,
			MaxBatchSize:    1000,
		},
	}
}

// ID returns the adapter identifier.
func (a *Adapter) ID() string { return adapterID }

// Capabilities returns the adapter's capabilities.
func (a *Adapter) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return a.caps, nil
}

// Health always reports healthy for the pure Go in-memory index.
func (a *Adapter) Health(_ context.Context) domain.Health {
	return domain.Health{
		Status:  "healthy",
		Message: "pure Go cosine index active",
	}
}

// Close releases resources.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.points = make(map[int64]*pointEntry)
	return nil
}

// Upsert stores or updates vector points in memory.
func (a *Adapter) Upsert(_ context.Context, points []domain.VectorPoint) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, p := range points {
		if p.ModelInfo.Dimension > 0 && len(p.Vector) != p.ModelInfo.Dimension {
			return domain.NewDimensionMismatchError(p.ModelInfo.Dimension, len(p.Vector), p.ModelInfo.Name)
		}

		var sumSq float64
		for _, v := range p.Vector {
			sumSq += float64(v) * float64(v)
		}
		mag := math.Sqrt(sumSq)

		a.points[p.ID] = &pointEntry{
			ID:        p.ID,
			Vector:    p.Vector,
			Model:     p.ModelInfo,
			Magnitude: mag,
		}
	}
	return nil
}

// Search performs cosine similarity calculation against indexed points.
func (a *Adapter) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(q.Vector) == 0 || len(a.points) == 0 {
		return []domain.VectorCandidate{}, nil
	}

	var querySumSq float64
	for _, v := range q.Vector {
		querySumSq += float64(v) * float64(v)
	}
	queryMag := math.Sqrt(querySumSq)
	if queryMag == 0 {
		return []domain.VectorCandidate{}, nil
	}

	type scoredPoint struct {
		id    int64
		score float64
	}

	var scored []scoredPoint
	for _, pt := range a.points {
		if len(pt.Vector) != len(q.Vector) {
			continue
		}
		if pt.Magnitude == 0 {
			continue
		}

		var dotProduct float64
		for i := range q.Vector {
			dotProduct += float64(q.Vector[i]) * float64(pt.Vector[i])
		}

		cosSim := dotProduct / (queryMag * pt.Magnitude)
		// Normalize to [0.0, 1.0] range
		normScore := (cosSim + 1.0) / 2.0
		if normScore >= q.Threshold {
			scored = append(scored, scoredPoint{id: pt.ID, score: normScore})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > len(scored) {
		limit = len(scored)
	}

	candidates := make([]domain.VectorCandidate, limit)
	for i := 0; i < limit; i++ {
		candidates[i] = domain.VectorCandidate{
			ID:         scored[i].id,
			Score:      scored[i].score,
			Provenance: adapterID,
		}
	}
	return candidates, nil
}

// Delete removes vectors by ID.
func (a *Adapter) Delete(_ context.Context, ids []int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, id := range ids {
		delete(a.points, id)
	}
	return nil
}

// Ensure VectorIndex interface conformance
var _ domain.VectorIndex = (*Adapter)(nil)
