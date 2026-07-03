package channels

import (
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// MetaCacheObserver receives append metadata cache observations.
type MetaCacheObserver interface {
	ObserveChannelMetaCache(result string)
}

type channelMetaCache struct {
	mu    sync.RWMutex
	items map[ch.ChannelID]ch.Meta
}

func (c *channelMetaCache) get(id ch.ChannelID) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.items == nil {
		return ch.Meta{}, false
	}
	meta, ok := c.items[id]
	if !ok || !cacheableAppendMeta(id, meta) {
		return ch.Meta{}, false
	}
	return cloneMeta(meta), true
}

func (c *channelMetaCache) put(id ch.ChannelID, meta ch.Meta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[ch.ChannelID]ch.Meta)
	}
	c.items[id] = cloneMeta(meta)
}

func (c *channelMetaCache) invalidate(id ch.ChannelID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		return
	}
	delete(c.items, id)
}

func cacheableAppendMeta(id ch.ChannelID, meta ch.Meta) bool {
	if meta.ID != id || meta.Key != ch.ChannelKeyForID(id) || meta.Leader == 0 {
		return false
	}
	if meta.Status != ch.StatusActive && meta.Status != ch.StatusCreating {
		return false
	}
	if len(meta.Replicas) == 0 || len(meta.ISR) == 0 || meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		return false
	}
	replicas := make(map[ch.NodeID]struct{}, len(meta.Replicas))
	for _, replica := range meta.Replicas {
		replicas[replica] = struct{}{}
	}
	if _, ok := replicas[meta.Leader]; !ok {
		return false
	}
	leaderInISR := false
	for _, replica := range meta.ISR {
		if _, ok := replicas[replica]; !ok {
			return false
		}
		if replica == meta.Leader {
			leaderInISR = true
		}
	}
	return leaderInISR
}
