package conversationactive

import (
	"sync"
)

// HotCache 是带 LRU 淘汰策略的热缓存
type HotCache struct {
	shards     []*CacheShard
	maxEntries int
	mu         sync.Mutex
}

func newHotCache(numShards int, maxEntries int) *HotCache {
	shards := make([]*CacheShard, numShards)
	for i := 0; i < numShards; i++ {
		shards[i] = newCacheShard()
	}

	return &HotCache{
		shards:     shards,
		maxEntries: maxEntries,
	}
}

// getShard 根据 uid 获取对应的分片
func (h *HotCache) getShard(uid string) *CacheShard {
	idx := shardIndex(uid, uint32(len(h.shards)))
	return h.shards[idx]
}

// set 设置缓存条目
func (h *HotCache) set(patch ActivePatch, version uint64, hashSlot uint16, hasHashSlot bool) {
	shard := h.getShard(patch.UID)
	shard.set(patch, version, hashSlot, hasHashSlot)

	// 检查是否需要淘汰
	h.evictIfNeeded()
}

// get 获取缓存条目
func (h *HotCache) get(uid string, key conversationKey) (*cacheEntry, bool) {
	shard := h.getShard(uid)
	return shard.get(uid, key)
}

// remove 删除缓存条目
func (h *HotCache) remove(uid string, key conversationKey) {
	shard := h.getShard(uid)
	shard.remove(uid, key)
}

// totalCount 返回所有分片的总条目数
func (h *HotCache) totalCount() int {
	total := 0
	for _, shard := range h.shards {
		total += shard.count()
	}
	return total
}

// evictIfNeeded 检查并执行淘汰
func (h *HotCache) evictIfNeeded() {
	h.mu.Lock()
	defer h.mu.Unlock()

	total := h.totalCount()
	if total <= h.maxEntries {
		return
	}

	// 需要淘汰的数量
	toEvict := total - h.maxEntries

	// 从每个分片平均淘汰
	perShard := (toEvict + len(h.shards) - 1) / len(h.shards)
	if perShard == 0 {
		perShard = 1
	}

	for _, shard := range h.shards {
		h.evictFromShard(shard, perShard)
		if h.totalCount() <= h.maxEntries {
			break
		}
	}
}

// evictFromShard 从分片中淘汰指定数量的条目
// 简化实现：删除每个 uid 的第一个会话
func (h *HotCache) evictFromShard(shard *CacheShard, count int) {
	if count <= 0 {
		return
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	evicted := 0
	for uid, byChannel := range shard.entries {
		if evicted >= count {
			break
		}

		// 删除这个 uid 的第一个会话
		for key := range byChannel {
			delete(byChannel, key)
			evicted++
			break
		}

		// 如果该 uid 没有会话了，删除该 uid
		if len(byChannel) == 0 {
			delete(shard.entries, uid)
		}

		if evicted >= count {
			break
		}
	}
}
