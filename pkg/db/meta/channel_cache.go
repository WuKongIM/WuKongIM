package meta

import (
	"container/list"
	"sync"
)

// channelReadCache is a fixed-capacity LRU for immutable business-channel rows.
// It owns a separate lock so high-volume reads do not contend with shard lookup.
type channelReadCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	lru      list.List
}

type channelReadCacheEntry struct {
	key     string
	channel Channel
}

func newChannelReadCache(capacity int) *channelReadCache {
	if capacity < 0 {
		capacity = 0
	}
	return &channelReadCache{capacity: capacity, entries: make(map[string]*list.Element)}
}

func (c *channelReadCache) put(key string, channel Channel) {
	if c == nil || c.capacity == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		element.Value.(*channelReadCacheEntry).channel = channel
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(&channelReadCacheEntry{key: key, channel: channel})
	c.entries[key] = element
	if len(c.entries) <= c.capacity {
		return
	}
	oldest := c.lru.Back()
	entry := oldest.Value.(*channelReadCacheEntry)
	delete(c.entries, entry.key)
	c.lru.Remove(oldest)
}

func (c *channelReadCache) get(key string) (Channel, bool) {
	if c == nil {
		return Channel{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return Channel{}, false
	}
	// Promote hits so a high-cardinality conversation scan cannot evict Channel
	// policy rows that remain hot for permission and lifecycle checks.
	c.lru.MoveToFront(element)
	return element.Value.(*channelReadCacheEntry).channel, true
}

func (c *channelReadCache) remove(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return
	}
	delete(c.entries, key)
	c.lru.Remove(element)
}

func (c *channelReadCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.lru.Init()
}

func (c *channelReadCache) size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
