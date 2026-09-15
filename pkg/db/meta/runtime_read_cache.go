package meta

import (
	"container/list"
	"slices"
	"strings"
	"sync"
)

const (
	runtimeReadCacheEntries = 8192
	runtimeReadCacheBytes   = 8 << 20
)

type runtimeReadKey struct {
	hashSlot    HashSlot
	channelID   string
	channelType int64
}

type runtimeReadEntry struct {
	key        runtimeReadKey
	meta       ChannelRuntimeMeta
	generation uint64
	bytes      int
}

// runtimeReadCache retains decoded storage rows, never distributed authority.
// Every hash-slot mutation advances its generation before releasing ownership;
// a reader that overlaps the mutation cannot republish a stale row afterward.
// Entry and retained-byte limits also bound large channel IDs and replica sets.
type runtimeReadCache struct {
	mu          sync.Mutex
	entries     map[runtimeReadKey]*list.Element
	generations map[HashSlot]uint64
	lru         list.List
	bytes       int
}

func newRuntimeReadCache() *runtimeReadCache {
	return &runtimeReadCache{entries: make(map[runtimeReadKey]*list.Element), generations: make(map[HashSlot]uint64)}
}

func cloneRuntimeReadMeta(m ChannelRuntimeMeta) ChannelRuntimeMeta {
	m.Replicas = slices.Clone(m.Replicas)
	m.ISR = slices.Clone(m.ISR)
	return m
}

func (c *runtimeReadCache) get(key runtimeReadKey) (ChannelRuntimeMeta, bool, uint64) {
	if c == nil {
		return ChannelRuntimeMeta{}, false, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	generation := c.generations[key.hashSlot]
	if e := c.entries[key]; e != nil {
		row := e.Value.(*runtimeReadEntry)
		if row.generation == generation {
			c.lru.MoveToFront(e)
			return cloneRuntimeReadMeta(row.meta), true, generation
		}
		c.remove(e)
	}
	return ChannelRuntimeMeta{}, false, generation
}

func (c *runtimeReadCache) put(key runtimeReadKey, m ChannelRuntimeMeta, generation uint64) {
	if c == nil {
		return
	}
	// Include conservative map/list/struct overhead and owned string/slice data.
	size := 512 + 2*len(key.channelID) + len(m.WriteFenceToken) + 8*(len(m.Replicas)+len(m.ISR))
	if size > runtimeReadCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generations[key.hashSlot] {
		return
	}
	if e := c.entries[key]; e != nil {
		c.remove(e)
	}
	key.channelID = strings.Clone(key.channelID)
	m = cloneRuntimeReadMeta(m)
	m.ChannelID = key.channelID
	m.WriteFenceToken = strings.Clone(m.WriteFenceToken)
	e := c.lru.PushFront(&runtimeReadEntry{key: key, meta: m, generation: generation, bytes: size})
	c.entries[key] = e
	c.bytes += size
	for len(c.entries) > runtimeReadCacheEntries || c.bytes > runtimeReadCacheBytes {
		c.remove(c.lru.Back())
	}
}

// invalidate also fences in-flight misses; no per-channel tombstones or scans
// are needed. HashSlot is uint16, so generation bookkeeping is bounded.
func (c *runtimeReadCache) invalidate(hashSlots []HashSlot) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, hs := range hashSlots {
		c.generations[hs]++
	}
}

// remove requires mu and updates both capacity dimensions.
func (c *runtimeReadCache) remove(e *list.Element) {
	r := e.Value.(*runtimeReadEntry)
	delete(c.entries, r.key)
	c.bytes -= r.bytes
	c.lru.Remove(e)
}

func (c *runtimeReadCache) usage() (entries, bytes int) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.bytes
}
