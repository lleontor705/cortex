package embedding

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
)

// DefaultCacheCapacity is the default number of embeddings cached in memory.
const DefaultCacheCapacity = 1000

// CachedService wraps any Service with a high-performance, thread-safe LRU cache.
type CachedService struct {
	inner     Service
	capacity  int
	mu        sync.RWMutex
	items     map[string]*list.Element
	evictList *list.List
}

type cacheEntry struct {
	key    string
	vector []float32
}

// NewCachedService creates a new CachedService wrapping inner with the given capacity.
// If capacity <= 0, DefaultCacheCapacity (1000) is used.
func NewCachedService(inner Service, capacity int) *CachedService {
	if inner == nil {
		return nil
	}
	if capacity <= 0 {
		capacity = DefaultCacheCapacity
	}
	return &CachedService{
		inner:     inner,
		capacity:  capacity,
		items:     make(map[string]*list.Element, capacity),
		evictList: list.New(),
	}
}

func hashKey(model, text string) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// Embed returns a vector embedding for the given text, serving from LRU cache if available.
func (c *CachedService) Embed(ctx context.Context, text string) ([]float32, error) {
	key := hashKey(c.inner.Model(), text)

	// Check cache (read lock)
	c.mu.RLock()
	if elem, ok := c.items[key]; ok {
		c.mu.RUnlock()
		c.mu.Lock()
		c.evictList.MoveToFront(elem)
		vec := elem.Value.(*cacheEntry).vector
		res := make([]float32, len(vec))
		copy(res, vec)
		c.mu.Unlock()
		return res, nil
	}
	c.mu.RUnlock()

	// Generate embedding from inner service
	vec, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// Store in cache (write lock)
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check again in case another goroutine populated it while we computed
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		return vec, nil
	}

	if c.evictList.Len() >= c.capacity {
		oldest := c.evictList.Back()
		if oldest != nil {
			c.evictList.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheEntry).key)
		}
	}

	stored := make([]float32, len(vec))
	copy(stored, vec)
	elem := c.evictList.PushFront(&cacheEntry{key: key, vector: stored})
	c.items[key] = elem

	return vec, nil
}

// Dimensions returns the embedding dimension size.
func (c *CachedService) Dimensions() int {
	return c.inner.Dimensions()
}

// Model returns the model identifier.
func (c *CachedService) Model() string {
	return c.inner.Model()
}

// Close closes the underlying service if it implements io.Closer.
func (c *CachedService) Close() error {
	if closer, ok := c.inner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Len returns current number of cached entries.
func (c *CachedService) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
