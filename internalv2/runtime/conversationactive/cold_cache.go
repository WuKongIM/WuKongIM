package conversationactive

import (
	"sync"
)

// ColdCache 存储已刷盘的冷数据，只保留 ActivePatch，不含元数据
type ColdCache struct {
	mu      sync.RWMutex
	entries map[cacheAddress]ActivePatch
}

func newColdCache() *ColdCache {
	return &ColdCache{
		entries: make(map[cacheAddress]ActivePatch),
	}
}

func (c *ColdCache) set(patch ActivePatch) {
	c.mu.Lock()
	defer c.mu.Unlock()

	addr := cacheAddress{
		uid: patch.UID,
		key: conversationKey{
			kind:        patch.Kind,
			channelID:   patch.ChannelID,
			channelType: patch.ChannelType,
		},
	}
	c.entries[addr] = patch
}

func (c *ColdCache) get(uid string, key conversationKey) (ActivePatch, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	addr := cacheAddress{uid: uid, key: key}
	patch, ok := c.entries[addr]
	return patch, ok
}

func (c *ColdCache) remove(uid string, key conversationKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	addr := cacheAddress{uid: uid, key: key}
	delete(c.entries, addr)
}

func (c *ColdCache) count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *ColdCache) batchRemove(addrs []cacheAddress) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, addr := range addrs {
		delete(c.entries, addr)
	}
}
