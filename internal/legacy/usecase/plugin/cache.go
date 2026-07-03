package plugin

import (
	"sync"
	"time"
)

// BindingCacheOptions configures the optional UID binding selection cache.
type BindingCacheOptions struct {
	// TTL bounds how long one UID binding lookup can be reused.
	TTL time.Duration
	// MaxEntries bounds the number of UID entries retained in memory.
	MaxEntries int
	// Clock supplies deterministic timestamps for tests.
	Clock func() time.Time
}

// BindingCache stores short-lived UID binding lookups.
type BindingCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	clock      func() time.Time
	entries    map[string]bindingCacheEntry
}

type bindingCacheEntry struct {
	bindings   []PluginBinding
	selected   ObservedPlugin
	selectedOK bool
	expiresAt  time.Time
	insertedAt time.Time
}

// NewBindingCache creates a bounded TTL cache. A zero TTL or capacity disables it.
func NewBindingCache(opts BindingCacheOptions) *BindingCache {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	cache := &BindingCache{ttl: opts.TTL, maxEntries: opts.MaxEntries, clock: clock}
	if opts.TTL > 0 && opts.MaxEntries > 0 {
		cache.entries = make(map[string]bindingCacheEntry)
	}
	return cache
}

// Get returns cached UID bindings and Receive selection when the entry is fresh.
func (c *BindingCache) Get(uid string) ([]PluginBinding, ObservedPlugin, bool, bool) {
	if c == nil || uid == "" || !c.enabled() {
		return nil, ObservedPlugin{}, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[uid]
	if !ok {
		return nil, ObservedPlugin{}, false, false
	}
	if !c.now().Before(entry.expiresAt) {
		delete(c.entries, uid)
		return nil, ObservedPlugin{}, false, false
	}
	return clonePluginBindings(entry.bindings), cloneObserved(entry.selected), entry.selectedOK, true
}

// Set stores UID bindings and the selected Receive plugin when the cache is enabled.
func (c *BindingCache) Set(uid string, bindings []PluginBinding, selected ObservedPlugin, selectedOK bool) {
	if c == nil || uid == "" || !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.entries[uid] = bindingCacheEntry{
		bindings:   clonePluginBindings(bindings),
		selected:   cloneObserved(selected),
		selectedOK: selectedOK,
		expiresAt:  now.Add(c.ttl),
		insertedAt: now,
	}
	c.evictOverflowLocked()
}

// Invalidate removes one UID cache entry.
func (c *BindingCache) Invalidate(uid string) {
	if c == nil || uid == "" || !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, uid)
}

func (c *BindingCache) enabled() bool {
	return c.ttl > 0 && c.maxEntries > 0 && c.entries != nil
}

func (c *BindingCache) now() time.Time {
	return c.clock().UTC()
}

func (c *BindingCache) evictOverflowLocked() {
	for len(c.entries) > c.maxEntries {
		var oldestUID string
		var oldest bindingCacheEntry
		first := true
		for uid, entry := range c.entries {
			if first || entry.insertedAt.Before(oldest.insertedAt) || (entry.insertedAt.Equal(oldest.insertedAt) && uid < oldestUID) {
				oldestUID = uid
				oldest = entry
				first = false
			}
		}
		delete(c.entries, oldestUID)
	}
}
