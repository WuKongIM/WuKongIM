package conversationactive

import (
	"hash/fnv"
	"sync"
)

// CacheShard 是一个独立的缓存分片，拥有独立的锁
type CacheShard struct {
	mu      sync.RWMutex
	entries map[string]map[conversationKey]*cacheEntry
}

func newCacheShard() *CacheShard {
	return &CacheShard{
		entries: make(map[string]map[conversationKey]*cacheEntry),
	}
}

// shardIndex 计算 uid 对应的分片索引
func shardIndex(uid string, numShards uint32) uint32 {
	h := fnv.New32a()
	h.Write([]byte(uid))
	return h.Sum32() % numShards
}

// set 在分片中设置一个 entry
func (s *CacheShard) set(patch ActivePatch, version uint64, hashSlot uint16, hasHashSlot bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := conversationKey{
		kind:        patch.Kind,
		channelID:   patch.ChannelID,
		channelType: patch.ChannelType,
	}

	byChannel := s.entries[patch.UID]
	if byChannel == nil {
		byChannel = make(map[conversationKey]*cacheEntry)
		s.entries[patch.UID] = byChannel
	}

	entry := byChannel[key]
	if entry == nil {
		entry = &cacheEntry{}
		byChannel[key] = entry
	}

	entry.patch = patch
	entry.version = version
	entry.hashSlot = hashSlot
	entry.hasHashSlot = hasHashSlot
}

// get 从分片中获取一个 entry
func (s *CacheShard) get(uid string, key conversationKey) (*cacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byChannel := s.entries[uid]
	if byChannel == nil {
		return nil, false
	}

	entry, ok := byChannel[key]
	return entry, ok
}

// remove 从分片中删除一个 entry
func (s *CacheShard) remove(uid string, key conversationKey) {
	s.mu.Lock()
	defer s.mu.Unlock()

	byChannel := s.entries[uid]
	if byChannel == nil {
		return
	}

	delete(byChannel, key)
	if len(byChannel) == 0 {
		delete(s.entries, uid)
	}
}

// count 返回分片中的 entry 数量
func (s *CacheShard) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := 0
	for _, byChannel := range s.entries {
		total += len(byChannel)
	}
	return total
}
