package cache

import "sync"

// Tiny is a small bounded map-backed cache for explicit domain hot paths.
type Tiny[K comparable, V any] struct {
	mu       sync.RWMutex
	capacity int
	items    map[K]V
}

// NewTiny creates a bounded cache. Non-positive capacity disables storage.
func NewTiny[K comparable, V any](capacity int) *Tiny[K, V] {
	return &Tiny[K, V]{capacity: capacity, items: make(map[K]V)}
}

// Get returns a cached value.
func (c *Tiny[K, V]) Get(key K) (V, bool) {
	var zero V
	if c == nil || c.capacity <= 0 {
		return zero, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, ok := c.items[key]
	return value, ok
}

// Set stores a cached value and evicts one arbitrary entry when full.
func (c *Tiny[K, V]) Set(key K, value V) {
	if c == nil || c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; !exists && len(c.items) >= c.capacity {
		for evict := range c.items {
			delete(c.items, evict)
			break
		}
	}
	c.items[key] = value
}

// Delete removes one cached value.
func (c *Tiny[K, V]) Delete(key K) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Clear removes all cached values.
func (c *Tiny[K, V]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[K]V)
}

// Len returns the current number of cached entries.
func (c *Tiny[K, V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
