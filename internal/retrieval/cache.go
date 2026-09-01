package retrieval

import (
	"container/list"
	"sync"
	"time"
)

// CacheEntry holds a cached value along with its expiration timestamp.
type CacheEntry[T any] struct {
	key       string
	value     T
	expiresAt time.Time
}

// ScopedCache is a generic, thread-safe LRU cache with TTL expiration.
type ScopedCache[T any] struct {
	mu        sync.RWMutex
	capacity  int
	ttl       time.Duration
	items     map[string]*list.Element
	evictList *list.List
}

// NewScopedCache creates a new ScopedCache with a maximum capacity and TTL.
func NewScopedCache[T any](capacity int, ttl time.Duration) *ScopedCache[T] {
	if capacity <= 0 {
		capacity = 256
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &ScopedCache[T]{
		capacity:  capacity,
		ttl:       ttl,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

// Get retrieves a value from the cache if present and not expired.
func (c *ScopedCache[T]) Get(key string) (T, bool) {
	if c == nil {
		var zero T
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.items[key]
	if !exists {
		var zero T
		return zero, false
	}

	entry := elem.Value.(*CacheEntry[T])
	if time.Now().After(entry.expiresAt) {
		c.evictElement(elem)
		var zero T
		return zero, false
	}

	c.evictList.MoveToFront(elem)
	return entry.value, true
}

// Set adds or updates a value in the cache.
func (c *ScopedCache[T]) Set(key string, value T) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.items[key]; exists {
		entry := elem.Value.(*CacheEntry[T])
		entry.value = value
		entry.expiresAt = time.Now().Add(c.ttl)
		c.evictList.MoveToFront(elem)
		return
	}

	if c.evictList.Len() >= c.capacity {
		c.evictOldest()
	}

	entry := &CacheEntry[T]{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.evictList.PushFront(entry)
	c.items[key] = elem
}

// PurgePrefix removes all entries whose key starts with the given prefix.
func (c *ScopedCache[T]) PurgePrefix(prefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, elem := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			c.evictElement(elem)
		}
	}
}

// Clear empties the cache completely.
func (c *ScopedCache[T]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.evictList.Init()
}

// Len returns the current number of items in the cache.
func (c *ScopedCache[T]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

func (c *ScopedCache[T]) evictElement(elem *list.Element) {
	c.evictList.Remove(elem)
	entry := elem.Value.(*CacheEntry[T])
	delete(c.items, entry.key)
}

func (c *ScopedCache[T]) evictOldest() {
	elem := c.evictList.Back()
	if elem != nil {
		c.evictElement(elem)
	}
}
