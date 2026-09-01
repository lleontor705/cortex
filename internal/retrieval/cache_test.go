package retrieval

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestScopedCache_SetAndGet(t *testing.T) {
	cache := NewScopedCache[string](10, 100*time.Millisecond)

	cache.Set("key1", "value1")
	val, ok := cache.Get("key1")
	if !ok || val != "value1" {
		t.Fatalf("expected value1, got %v (ok=%v)", val, ok)
	}

	time.Sleep(150 * time.Millisecond)
	_, ok = cache.Get("key1")
	if ok {
		t.Fatalf("expected expired entry to not be returned")
	}
}

func TestScopedCache_LRUEviction(t *testing.T) {
	cache := NewScopedCache[int](3, time.Minute)

	cache.Set("k1", 1)
	cache.Set("k2", 2)
	cache.Set("k3", 3)

	// Access k1 to make k2 the least recently used
	cache.Get("k1")

	// Adding k4 should evict k2
	cache.Set("k4", 4)

	if _, ok := cache.Get("k2"); ok {
		t.Fatalf("expected k2 to be evicted")
	}
	if val, ok := cache.Get("k1"); !ok || val != 1 {
		t.Fatalf("expected k1 to be present")
	}
	if val, ok := cache.Get("k3"); !ok || val != 3 {
		t.Fatalf("expected k3 to be present")
	}
	if val, ok := cache.Get("k4"); !ok || val != 4 {
		t.Fatalf("expected k4 to be present")
	}
}

func TestScopedCache_PurgePrefix(t *testing.T) {
	cache := NewScopedCache[string](10, time.Minute)

	cache.Set("t1/ws1/proj1/q1", "res1")
	cache.Set("t1/ws1/proj1/q2", "res2")
	cache.Set("t1/ws1/proj2/q1", "res3")
	cache.Set("t2/ws1/proj1/q1", "res4")

	cache.PurgePrefix("t1/ws1/proj1/")

	if _, ok := cache.Get("t1/ws1/proj1/q1"); ok {
		t.Fatalf("expected t1/ws1/proj1/q1 to be purged")
	}
	if _, ok := cache.Get("t1/ws1/proj1/q2"); ok {
		t.Fatalf("expected t1/ws1/proj1/q2 to be purged")
	}
	if _, ok := cache.Get("t1/ws1/proj2/q1"); !ok {
		t.Fatalf("expected t1/ws1/proj2/q1 to remain")
	}
	if _, ok := cache.Get("t2/ws1/proj1/q1"); !ok {
		t.Fatalf("expected t2/ws1/proj1/q1 to remain")
	}
}

func TestScopedCache_ConcurrentAccess(t *testing.T) {
	cache := NewScopedCache[int](100, 500*time.Millisecond)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := "key" + strconv.Itoa(id%10)
			cache.Set(key, id)
			cache.Get(key)
			if id%5 == 0 {
				cache.PurgePrefix("key")
			}
		}(i)
	}

	wg.Wait()
}
