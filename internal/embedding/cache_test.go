package embedding

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

type mockService struct {
	model      string
	dims       int
	calls      int32
	embedFunc  func(text string) []float32
}

func (m *mockService) Embed(ctx context.Context, text string) ([]float32, error) {
	atomic.AddInt32(&m.calls, 1)
	if m.embedFunc != nil {
		return m.embedFunc(text), nil
	}
	return []float32{1.0, 2.0, 3.0}, nil
}

func (m *mockService) Dimensions() int { return m.dims }
func (m *mockService) Model() string   { return m.model }

func TestCachedService_Basic(t *testing.T) {
	mock := &mockService{model: "test-model", dims: 3}
	cached := NewCachedService(mock, 2)

	if cached.Dimensions() != 3 {
		t.Fatalf("expected 3 dims, got %d", cached.Dimensions())
	}
	if cached.Model() != "test-model" {
		t.Fatalf("expected test-model, got %s", cached.Model())
	}

	ctx := context.Background()

	// First call -> compute
	vec1, err := cached.Embed(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", mock.calls)
	}
	if len(vec1) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(vec1))
	}

	// Second call -> cached
	vec2, err := cached.Embed(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&mock.calls) != 1 {
		t.Fatalf("expected still 1 call (cache hit), got %d", mock.calls)
	}
	if vec1[0] != vec2[0] {
		t.Fatalf("mismatched vectors")
	}

	// Mutating returned vector should not mutate cache
	vec2[0] = 999.0
	vec3, _ := cached.Embed(ctx, "hello")
	if vec3[0] == 999.0 {
		t.Fatalf("cache was mutated by caller")
	}
}

func TestCachedService_LRUEviction(t *testing.T) {
	mock := &mockService{model: "test-model", dims: 3}
	cached := NewCachedService(mock, 2) // capacity 2
	ctx := context.Background()

	_, _ = cached.Embed(ctx, "key1") // 1 (len: 1, MRU: key1)
	_, _ = cached.Embed(ctx, "key2") // 2 (len: 2, MRU: key2, LRU: key1)
	if cached.Len() != 2 {
		t.Fatalf("expected len 2, got %d", cached.Len())
	}

	// Access key1 to make it MRU (MRU: key1, LRU: key2)
	_, _ = cached.Embed(ctx, "key1")

	// Insert key3 -> should evict key2
	_, _ = cached.Embed(ctx, "key3")
	if cached.Len() != 2 {
		t.Fatalf("expected len 2, got %d", cached.Len())
	}

	callsBefore := atomic.LoadInt32(&mock.calls) // 3

	// key1 should be cached (hit)
	_, _ = cached.Embed(ctx, "key1")
	if atomic.LoadInt32(&mock.calls) != callsBefore {
		t.Fatalf("expected cache hit for key1")
	}

	// key2 should have been evicted (miss -> compute)
	_, _ = cached.Embed(ctx, "key2")
	if atomic.LoadInt32(&mock.calls) != callsBefore+1 {
		t.Fatalf("expected cache miss for evicted key2")
	}
}

func TestCachedService_Concurrent(t *testing.T) {
	mock := &mockService{model: "test-model", dims: 3}
	cached := NewCachedService(mock, 100)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = cached.Embed(ctx, "concurrent-key")
			}
		}(i)
	}
	wg.Wait()

	if cached.Len() != 1 {
		t.Fatalf("expected 1 unique key, got %d", cached.Len())
	}
}
