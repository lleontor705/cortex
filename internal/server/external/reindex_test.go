// Package external: reindex tests (W8.4 — replay external vector replica).
//
// These tests verify the reindex function that replays observations from the
// authoritative SQLite store into a configured external VectorIndex. The
// reindex is idempotent (upsert by observation ID), batches at a configurable
// size, and reports progress + explicit failure classification.
package external

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

// fakeReindexSource is a controllable ReindexSource for unit tests. It serves
// observations and (optionally) pre-existing embeddings from in-memory maps.
type fakeReindexSource struct {
	obs        map[int64]*domain.Observation
	embeddings map[int64][]float32 // existing vectors (nil = not stored)
	embedErr   map[int64]error     // per-id embedding retrieval error
	idsByOrder []int64             // deterministic iteration order
}

func (s *fakeReindexSource) List(_ context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	// Apply project filter first, then paginate by Offset/Limit (the reindex
	// uses Offset-based pagination — the fake MUST respect it or the loop
	// never terminates).
	var filtered []*domain.Observation
	for _, id := range s.idsByOrder {
		o := s.obs[id]
		if o == nil {
			continue
		}
		if filter.Project != "" && o.Project != filter.Project {
			continue
		}
		filtered = append(filtered, o)
	}
	start := filter.Offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := len(filtered)
	if filter.Limit > 0 && start+filter.Limit < end {
		end = start + filter.Limit
	}
	return filtered[start:end], nil
}

func (s *fakeReindexSource) GetByID(_ context.Context, id int64) (*domain.Observation, error) {
	if o, ok := s.obs[id]; ok {
		return o, nil
	}
	return nil, domain.ErrNotFound
}

func (s *fakeReindexSource) GetEmbedding(_ context.Context, id int64) ([]float32, string, error) {
	if s.embedErr != nil {
		if err, ok := s.embedErr[id]; ok {
			return nil, "", err
		}
	}
	if vec, ok := s.embeddings[id]; ok {
		return vec, "source-model", nil
	}
	return nil, "", domain.ErrNotFound
}

// fakeEmbeddingProvider generates deterministic vectors for re-embedding.
type fakeEmbeddingProvider struct {
	dim     int
	model   string
	err     error
	calls   int
}

func (p *fakeEmbeddingProvider) Embed(_ context.Context, texts []string) ([][]float32, domain.ModelInfo, error) {
	p.calls++
	if p.err != nil {
		return nil, domain.ModelInfo{}, p.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, p.dim)
		for j := range v {
			v[j] = float32(i + 1)
		}
		out[i] = v
	}
	return out, domain.ModelInfo{Name: p.model, Dimension: p.dim, Version: "v1"}, nil
}

func (p *fakeEmbeddingProvider) ModelInfo() domain.ModelInfo {
	return domain.ModelInfo{Name: p.model, Dimension: p.dim, Version: "v1"}
}

func (p *fakeEmbeddingProvider) Health(_ context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}

// reindexTarget is a domain.VectorIndex that records upserted points.
type reindexTarget struct {
	upserted  []domain.VectorPoint
	upsertErr error
	caps      domain.Capabilities
}

func (t *reindexTarget) ID() string { return "reindex-target" }
func (t *reindexTarget) Upsert(_ context.Context, points []domain.VectorPoint) error {
	if t.upsertErr != nil {
		return t.upsertErr
	}
	t.upserted = append(t.upserted, points...)
	return nil
}
func (t *reindexTarget) Search(_ context.Context, _ domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (t *reindexTarget) Delete(_ context.Context, _ []int64) error { return nil }
func (t *reindexTarget) Close() error                               { return nil }
func (t *reindexTarget) Health(_ context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}
func (t *reindexTarget) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return t.caps, nil
}

// TestReindex_CopiesExistingEmbeddings verifies the happy path: observations
// WITH existing embeddings in the source are copied to the target in a single
// batch upsert. The embedding model from the source is preserved on the
// VectorPoint (namespace enforcement).
func TestReindex_CopiesExistingEmbeddings(t *testing.T) {
	src := &fakeReindexSource{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "a", Content: "alpha", Project: "p", Scope: "project", Type: "decision"},
			2: {ID: 2, Title: "b", Content: "beta", Project: "p", Scope: "project", Type: "decision"},
		},
		embeddings: map[int64][]float32{
			1: {1.0, 0.0, 0.0, 0.0},
			2: {0.0, 1.0, 0.0, 0.0},
		},
		idsByOrder: []int64{1, 2},
	}
	target := &reindexTarget{}
	res, err := Reindex(context.Background(), src, nil, target, ReindexOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if res.Upserted != 2 {
		t.Errorf("Upserted = %d, want 2", res.Upserted)
	}
	if res.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", res.Skipped)
	}
	if len(target.upserted) != 2 {
		t.Fatalf("target upserted = %d points, want 2", len(target.upserted))
	}
	// The model from the source embedding is preserved.
	if target.upserted[0].ModelInfo.Name != "source-model" {
		t.Errorf("ModelInfo.Name = %q, want source-model", target.upserted[0].ModelInfo.Name)
	}
}

// TestReindex_ReEmbedsWhenNoExistingVector verifies that when the source has
// NO embedding for an observation (ErrNotFound), the reindex falls back to
// the EmbeddingProvider to regenerate the vector. The model-version namespace
// comes from the provider's ModelInfo.
func TestReindex_ReEmbedsWhenNoExistingVector(t *testing.T) {
	src := &fakeReindexSource{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "a", Content: "alpha", Project: "p"},
		},
		embeddings: map[int64][]float32{}, // no vectors stored
		idsByOrder: []int64{1},
	}
	provider := &fakeEmbeddingProvider{dim: 4, model: "reembed-model"}
	target := &reindexTarget{}
	res, err := Reindex(context.Background(), src, provider, target, ReindexOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if res.ReEmbedded != 1 {
		t.Errorf("ReEmbedded = %d, want 1", res.ReEmbedded)
	}
	if res.Upserted != 1 {
		t.Errorf("Upserted = %d, want 1", res.Upserted)
	}
	if len(target.upserted) != 1 {
		t.Fatalf("target upserted = %d, want 1", len(target.upserted))
	}
	if target.upserted[0].ModelInfo.Name != "reembed-model" {
		t.Errorf("ModelInfo.Name = %q, want reembed-model", target.upserted[0].ModelInfo.Name)
	}
}

// TestReindex_SkipsWhenNoVectorAndNoProvider verifies that when the source has
// no embedding AND no EmbeddingProvider is supplied, the observation is
// SKIPPED (counted), not errored. The reindex is best-effort: it copies what
// it can and reports what it could not.
func TestReindex_SkipsWhenNoVectorAndNoProvider(t *testing.T) {
	src := &fakeReindexSource{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "a", Content: "alpha"},
		},
		embeddings: map[int64][]float32{},
		idsByOrder: []int64{1},
	}
	target := &reindexTarget{}
	res, err := Reindex(context.Background(), src, nil, target, ReindexOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
	if res.Upserted != 0 {
		t.Errorf("Upserted = %d, want 0", res.Upserted)
	}
	if len(target.upserted) != 0 {
		t.Errorf("target got %d points; expected 0", len(target.upserted))
	}
}

// TestReindex_VectorSearchDisabledSkipsAndReEmbeds verifies that when the
// source returns ErrVectorSearchDisabled (the zero-CGO stub), the reindex
// treats it as "no existing embedding" and falls back to re-embedding via
// the provider. This is the "avoid reading vector BLOB if source contract
// insufficient" path: the stub contract is insufficient, so we regenerate.
func TestReindex_VectorSearchDisabledSkipsAndReEmbeds(t *testing.T) {
	src := &fakeReindexSource{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "a", Content: "alpha"},
		},
		embedErr: map[int64]error{
			1: domain.ErrVectorSearchDisabled,
		},
		idsByOrder: []int64{1},
	}
	provider := &fakeEmbeddingProvider{dim: 4, model: "reembed"}
	target := &reindexTarget{}
	res, err := Reindex(context.Background(), src, provider, target, ReindexOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if res.ReEmbedded != 1 {
		t.Errorf("ReEmbedded = %d, want 1 (disabled source should trigger re-embed)", res.ReEmbedded)
	}
}

// TestReindex_BatchChunking verifies observations are upserted in batches of
// BatchSize, not one giant batch. We verify by setting a small batch size and
// checking the target recorded multiple Upsert calls.
func TestReindex_BatchChunking(t *testing.T) {
	const n = 5
	src := &fakeReindexSource{
		obs:        make(map[int64]*domain.Observation),
		embeddings: make(map[int64][]float32),
	}
	for i := int64(1); i <= n; i++ {
		src.obs[i] = &domain.Observation{ID: i, Title: "t", Content: "c"}
		src.embeddings[i] = []float32{float32(i), 0, 0, 0}
		src.idsByOrder = append(src.idsByOrder, i)
	}
	// Track upsert call count via a wrapper.
	calls := 0
	target := &countingTarget{
		inner:   &reindexTarget{},
		onUpsert: func() { calls++ },
	}
	_, err := Reindex(context.Background(), src, nil, target, ReindexOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	// 5 points, batch=2: expect 3 batches (2 + 2 + 1).
	if calls != 3 {
		t.Errorf("Upsert calls = %d, want 3 (batch=2, 5 points)", calls)
	}
}

// countingTarget wraps a VectorIndex and invokes onUpsert on each Upsert call.
type countingTarget struct {
	inner    domain.VectorIndex
	onUpsert func()
}

func (c *countingTarget) ID() string { return c.inner.ID() }
func (c *countingTarget) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	c.onUpsert()
	return c.inner.Upsert(ctx, points)
}
func (c *countingTarget) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return c.inner.Search(ctx, q)
}
func (c *countingTarget) Delete(ctx context.Context, ids []int64) error  { return c.inner.Delete(ctx, ids) }
func (c *countingTarget) Close() error                                   { return c.inner.Close() }
func (c *countingTarget) Health(ctx context.Context) domain.Health       { return c.inner.Health(ctx) }
func (c *countingTarget) Capabilities(ctx context.Context) (domain.Capabilities, error) {
	return c.inner.Capabilities(ctx)
}

// TestReindex_TargetUpsertFailureIsExplicit verifies a failure from the target
// VectorIndex.Upsert is returned as an explicit error (not silently
// swallowed). The result reflects the count up to the failure point.
func TestReindex_TargetUpsertFailureIsExplicit(t *testing.T) {
	src := &fakeReindexSource{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "a", Content: "alpha"},
		},
		embeddings: map[int64][]float32{1: {1, 0, 0, 0}},
		idsByOrder: []int64{1},
	}
	target := &reindexTarget{
		upsertErr: errors.New("target unavailable"),
	}
	_, err := Reindex(context.Background(), src, nil, target, ReindexOptions{BatchSize: 10})
	if err == nil {
		t.Fatal("expected upsert error to propagate; got nil")
	}
	if !errors.Is(err, target.upsertErr) {
		t.Errorf("error not wrapped correctly: got %v", err)
	}
}

// TestReindex_EmptySourceIsSuccess verifies an empty source produces a
// zero-count success, not an error.
func TestReindex_EmptySourceIsSuccess(t *testing.T) {
	src := &fakeReindexSource{
		obs:        map[int64]*domain.Observation{},
		embeddings: map[int64][]float32{},
	}
	target := &reindexTarget{}
	res, err := Reindex(context.Background(), src, nil, target, ReindexOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Reindex empty: %v", err)
	}
	if res.Total != 0 || res.Upserted != 0 || res.Skipped != 0 {
		t.Errorf("empty result nonzero: %+v", res)
	}
}

// TestReindex_ProgressCallback verifies the progress callback is invoked per
// batch with cumulative counts.
func TestReindex_ProgressCallback(t *testing.T) {
	src := &fakeReindexSource{
		obs:        make(map[int64]*domain.Observation),
		embeddings: make(map[int64][]float32),
	}
	for i := int64(1); i <= 4; i++ {
		src.obs[i] = &domain.Observation{ID: i, Title: "t", Content: "c"}
		src.embeddings[i] = []float32{float32(i), 0, 0, 0}
		src.idsByOrder = append(src.idsByOrder, i)
	}
	target := &reindexTarget{}
	var lastProgress ReindexProgress
	_, err := Reindex(context.Background(), src, nil, target, ReindexOptions{
		BatchSize: 2,
		OnProgress: func(p ReindexProgress) {
			lastProgress = p
		},
	})
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	// After the last batch, progress should show 4 processed.
	if lastProgress.Processed != 4 {
		t.Errorf("last Progress.Processed = %d, want 4", lastProgress.Processed)
	}
	if lastProgress.Upserted != 4 {
		t.Errorf("last Progress.Upserted = %d, want 4", lastProgress.Upserted)
	}
}

// TestReindex_Idempotent verifies running Reindex twice against the same
// target produces the same upsert count (upsert by observation ID is
// idempotent at the adapter level — the replica's PK is the observation ID).
func TestReindex_Idempotent(t *testing.T) {
	src := &fakeReindexSource{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "a", Content: "alpha"},
		},
		embeddings: map[int64][]float32{1: {1, 0, 0, 0}},
		idsByOrder: []int64{1},
	}
	target := &reindexTarget{}
	opts := ReindexOptions{BatchSize: 10}
	r1, err := Reindex(context.Background(), src, nil, target, opts)
	if err != nil {
		t.Fatalf("first Reindex: %v", err)
	}
	// Reset the target's recorded upserts (simulating a fresh view), but the
	// adapter's upsert-by-ID is idempotent.
	target.upserted = nil
	r2, err := Reindex(context.Background(), src, nil, target, opts)
	if err != nil {
		t.Fatalf("second Reindex: %v", err)
	}
	if r1.Upserted != r2.Upserted {
		t.Errorf("idempotency broken: r1=%d r2=%d", r1.Upserted, r2.Upserted)
	}
}

// TestReindex_NilTargetErrors verifies a nil target is an explicit error.
func TestReindex_NilTargetErrors(t *testing.T) {
	src := &fakeReindexSource{}
	_, err := Reindex(context.Background(), src, nil, nil, ReindexOptions{BatchSize: 10})
	if err == nil {
		t.Fatal("expected error for nil target; got nil")
	}
}

// TestReindex_NilSourceErrors verifies a nil source is an explicit error.
func TestReindex_NilSourceErrors(t *testing.T) {
	_, err := Reindex(context.Background(), nil, nil, &reindexTarget{}, ReindexOptions{BatchSize: 10})
	if err == nil {
		t.Fatal("expected error for nil source; got nil")
	}
}
