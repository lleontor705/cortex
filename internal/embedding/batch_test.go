package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

func TestOllamaService_EmbedBatch(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Input) != 2 {
			http.Error(w, "expected 2 inputs", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"embeddings": [][]float64{
				{0.1, 0.2, 0.3},
				{0.4, 0.5, 0.6},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := &ollamaService{
		baseURL:         server.URL,
		model:           "test-model",
		client:          server.Client(),
		maxResponseBody: defaultMaxEmbeddingResponse,
		sem:             make(chan struct{}, 4),
	}

	vecs, err := svc.EmbedBatch(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 3 || len(vecs[1]) != 3 {
		t.Fatalf("unexpected vector length")
	}
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Fatalf("expected 1 HTTP request for batch, got %d", requestCount)
	}
}

func TestOpenAIService_EmbedBatch(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		if r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0.4, 0.5, 0.6}},
				{"index": 0, "embedding": []float32{0.1, 0.2, 0.3}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := &openAIService{
		baseURL:         server.URL,
		apiKey:          "test-key",
		model:           "test-model",
		client:          server.Client(),
		maxResponseBody: defaultMaxEmbeddingResponse,
		sem:             make(chan struct{}, 4),
	}

	vecs, err := svc.EmbedBatch(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	// Verify index ordering
	if vecs[0][0] != 0.1 || vecs[1][0] != 0.4 {
		t.Fatalf("vectors not properly ordered by index: %v", vecs)
	}
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Fatalf("expected 1 HTTP request, got %d", requestCount)
	}
}

type fakeBatchService struct {
	model      string
	dims       int
	embedCount int32
	batchCount int32
}

func (f *fakeBatchService) Embed(ctx context.Context, text string) ([]float32, error) {
	atomic.AddInt32(&f.embedCount, 1)
	return []float32{1.0, 2.0, 3.0}, nil
}

func (f *fakeBatchService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	atomic.AddInt32(&f.batchCount, 1)
	res := make([][]float32, len(texts))
	for i := range texts {
		res[i] = []float32{float32(i), 1.0, 2.0}
	}
	return res, nil
}

func (f *fakeBatchService) Dimensions() int { return f.dims }
func (f *fakeBatchService) Model() string   { return f.model }

func TestCachedService_EmbedBatch(t *testing.T) {
	fake := &fakeBatchService{model: "fake", dims: 3}
	cached := NewCachedService(fake, 10)

	// Populate cache with "cached1"
	_, err := cached.Embed(context.Background(), "cached1")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if atomic.LoadInt32(&fake.embedCount) != 1 {
		t.Fatalf("expected 1 embed call")
	}

	// Request batch with ["cached1", "new1", "new2"]
	results, err := cached.EmbedBatch(context.Background(), []string{"cached1", "new1", "new2"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// cached1 came from cache
	if results[0][0] != 1.0 || results[0][1] != 2.0 {
		t.Fatalf("unexpected cached result: %v", results[0])
	}
	// new1 and new2 came from batch call
	if atomic.LoadInt32(&fake.batchCount) != 1 {
		t.Fatalf("expected 1 batch call for misses, got %d", fake.batchCount)
	}

	// Calling again with all cached
	results2, err := cached.EmbedBatch(context.Background(), []string{"cached1", "new1", "new2"})
	if err != nil {
		t.Fatalf("EmbedBatch 2 failed: %v", err)
	}
	if len(results2) != 3 {
		t.Fatalf("expected 3 results")
	}
	// No additional batch calls
	if atomic.LoadInt32(&fake.batchCount) != 1 {
		t.Fatalf("expected batchCount to remain 1")
	}
}

type fakeObsReader struct {
	obs map[int64]*domain.Observation
}

func (f *fakeObsReader) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	if o, ok := f.obs[id]; ok {
		return o, nil
	}
	return nil, domain.ErrNotFound
}

type fakeOutbox struct {
	intents   []sqlitestore.OutboxIntent
	completed map[int64]bool
	failed    map[int64]error
}

func (f *fakeOutbox) Lease(ctx context.Context, limit int) ([]sqlitestore.OutboxIntent, error) {
	if len(f.intents) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(f.intents) {
		n = len(f.intents)
	}
	res := f.intents[:n]
	f.intents = f.intents[n:]
	return res, nil
}
func (f *fakeOutbox) MarkComplete(ctx context.Context, id int64) error {
	f.completed[id] = true
	return nil
}
func (f *fakeOutbox) MarkFailed(ctx context.Context, id int64, cause error, nextRetryAt time.Time) error {
	f.failed[id] = cause
	return nil
}
func (f *fakeOutbox) DeadLetter(ctx context.Context, id int64, cause error) error {
	f.failed[id] = cause
	return nil
}
func (f *fakeOutbox) RecoverPending(ctx context.Context) error { return nil }
func (f *fakeOutbox) PendingCount(ctx context.Context) (int, error) {
	return len(f.intents), nil
}
func (f *fakeOutbox) UpdateIndexState(ctx context.Context, namespace string, coverage float64, parity int) error {
	return nil
}

func TestWorker_BatchEmbeddingIntegration(t *testing.T) {
	fakeSvc := &fakeBatchService{model: "batch-model", dims: 3}
	obsStore := &fakeObsReader{
		obs: map[int64]*domain.Observation{
			1: {ID: 1, Title: "Obs 1", Content: "Content 1"},
			2: {ID: 2, Title: "Obs 2", Content: "Content 2"},
		},
	}
	outbox := &fakeOutbox{
		intents: []sqlitestore.OutboxIntent{
			{ID: 101, ObservationID: 1},
			{ID: 102, ObservationID: 2},
		},
		completed: make(map[int64]bool),
		failed:    make(map[int64]error),
	}
	vecWriter := newFakeVectorWriter()

	worker := &Worker{
		outbox:     outbox,
		obs:        obsStore,
		embeddings: fakeSvc,
		vectors:    vecWriter,
		config: WorkerConfig{
			Concurrency: 1,
			LeaseBatch:  5,
		},
	}

	processed := worker.processOne(context.Background(), context.Background())
	if !processed {
		t.Fatalf("expected processed=true")
	}

	if atomic.LoadInt32(&fakeSvc.batchCount) != 1 {
		t.Fatalf("expected 1 batch call to fakeSvc, got %d", fakeSvc.batchCount)
	}
	if !vecWriter.has(1) || !vecWriter.has(2) {
		t.Fatalf("expected both observations upserted in batch")
	}
	if !outbox.completed[101] || !outbox.completed[102] {
		t.Fatalf("expected both intents completed, got %+v", outbox.completed)
	}
}

