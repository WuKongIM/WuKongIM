package channels

import (
	"slices"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// MetaCacheObserver receives append metadata cache observations.
type MetaCacheObserver interface {
	ObserveChannelMetaCache(result string)
}

// channelMetaCache retains a monotonic metadata floor for every resolved
// channel while hiding explicitly invalidated versions from append routing.
type channelMetaCache struct {
	// mu serializes metadata-floor installation and version invalidation.
	mu sync.RWMutex
	// items stores the newest complete or generation-bearing metadata observed.
	items map[ch.ChannelID]ch.Meta
	// stale marks an installed exact version unusable until a fresh resolve
	// confirms the same complete version or installs a newer one.
	stale map[ch.ChannelID]struct{}
}

// get returns one appendable, non-stale cached metadata snapshot.
func (c *channelMetaCache) get(id ch.ChannelID) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.items == nil {
		return ch.Meta{}, false
	}
	meta, ok := c.items[id]
	_, invalidated := c.stale[id]
	if !ok || invalidated || !cacheableAppendMeta(id, meta) {
		return ch.Meta{}, false
	}
	return cloneMeta(meta), true
}

// installIfNewer advances the monotonic cache floor and returns the selected
// newest snapshot.
func (c *channelMetaCache) installIfNewer(id ch.ChannelID, meta ch.Meta) ch.Meta {
	c.mu.Lock()
	defer c.mu.Unlock()
	installed, _ := c.installIfNewerLocked(id, meta)
	return installed
}

// selectResolved compares one authoritative resolve result against the
// monotonic floor and reports whether the result is fresh enough to use.
// Appendability is deliberately separate: known non-appendable metadata is
// retained as a floor but must not become a hot route.
func (c *channelMetaCache) selectResolved(id ch.ChannelID, meta ch.Meta) (ch.Meta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, ok := c.items[id]; ok && !appendMetaNewer(current, meta) {
		accepted := sameCompleteAppendMeta(current, meta)
		if accepted {
			delete(c.stale, id)
		}
		_, invalidated := c.stale[id]
		appendable := !invalidated && cacheableAppendMeta(id, current)
		return cloneMeta(current), accepted || appendable
	}
	// Modern projections carry an exact route generation, so even a deleting
	// or temporarily non-appendable record is a useful monotonic floor. Legacy
	// generation-zero invalid metadata stays uncached so a corrected record at
	// the same legacy fence can recover.
	if cacheableAppendMeta(id, meta) || meta.RouteGeneration != 0 {
		if c.items == nil {
			c.items = make(map[ch.ChannelID]ch.Meta)
		}
		c.items[id] = cloneMeta(meta)
		delete(c.stale, id)
	}
	return cloneMeta(meta), true
}

// installIfNewerLocked advances the cache floor while the caller holds mu and
// reports whether the candidate was accepted as the selected exact version.
func (c *channelMetaCache) installIfNewerLocked(id ch.ChannelID, meta ch.Meta) (ch.Meta, bool) {
	if c.items == nil {
		c.items = make(map[ch.ChannelID]ch.Meta)
	}
	if current, ok := c.items[id]; ok && !appendMetaNewer(current, meta) {
		accepted := sameCompleteAppendMeta(current, meta)
		if accepted {
			delete(c.stale, id)
		}
		return cloneMeta(current), accepted
	}
	installed := cloneMeta(meta)
	c.items[id] = installed
	delete(c.stale, id)
	return cloneMeta(installed), true
}

// preferCurrent keeps a cached monotonic floor when a delayed candidate is not
// newer, without changing the candidate's provenance.
func (c *channelMetaCache) preferCurrent(id ch.ChannelID, candidate ch.Meta) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	current, ok := c.items[id]
	if ok && !appendMetaNewer(current, candidate) {
		return cloneMeta(current), false
	}
	return cloneMeta(candidate), true
}

// appendMetaNewer reports whether candidate is strictly newer under the
// append-authority epoch, leader-epoch, leader, and route-generation order.
func appendMetaNewer(current ch.Meta, candidate ch.Meta) bool {
	if current.RouteGeneration != 0 && candidate.RouteGeneration == 0 {
		return false
	}
	if current.Epoch != candidate.Epoch {
		return candidate.Epoch > current.Epoch
	}
	if current.LeaderEpoch != candidate.LeaderEpoch {
		return candidate.LeaderEpoch > current.LeaderEpoch
	}
	if current.Leader != candidate.Leader {
		return false
	}
	if candidate.RouteGeneration == 0 {
		return false
	}
	if current.RouteGeneration == 0 {
		return true
	}
	return candidate.RouteGeneration > current.RouteGeneration
}

// invalidateUsedMeta marks only the exact metadata version used by a failed
// append as stale, preserving a concurrently installed newer version.
func (c *channelMetaCache) invalidateUsedMeta(id ch.ChannelID, used ch.Meta) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		return false
	}
	current, ok := c.items[id]
	if !ok || !sameUsedAppendMeta(current, used) {
		return false
	}
	return c.markStaleLocked(id)
}

// invalidateAuthority marks a cached version stale only when every supplied
// authority fence matches the installed metadata.
func (c *channelMetaCache) invalidateAuthority(id ch.ChannelID, leader ch.NodeID, epoch uint64, leaderEpoch uint64, routeGeneration uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		return false
	}
	meta, ok := c.items[id]
	if !ok || meta.Leader != leader || meta.Epoch != epoch || meta.LeaderEpoch != leaderEpoch ||
		(routeGeneration == 0 && meta.RouteGeneration != 0) ||
		(routeGeneration != 0 && meta.RouteGeneration != routeGeneration) {
		return false
	}
	return c.markStaleLocked(id)
}

// markStaleLocked records one exact invalidation while the caller holds mu.
func (c *channelMetaCache) markStaleLocked(id ch.ChannelID) bool {
	if c.stale == nil {
		c.stale = make(map[ch.ChannelID]struct{})
	}
	if _, exists := c.stale[id]; exists {
		return false
	}
	c.stale[id] = struct{}{}
	return true
}

func sameUsedAppendMeta(current ch.Meta, used ch.Meta) bool {
	if used.RouteGeneration != 0 {
		return current.Epoch == used.Epoch &&
			current.LeaderEpoch == used.LeaderEpoch &&
			current.Leader == used.Leader &&
			current.RouteGeneration == used.RouteGeneration
	}
	return sameCompleteAppendMeta(current, used)
}

func sameCompleteAppendMeta(current ch.Meta, used ch.Meta) bool {
	return current.Key == used.Key &&
		current.ID == used.ID &&
		current.Epoch == used.Epoch &&
		current.LeaderEpoch == used.LeaderEpoch &&
		current.RouteGeneration == used.RouteGeneration &&
		current.Leader == used.Leader &&
		slices.Equal(current.Replicas, used.Replicas) &&
		slices.Equal(current.ISR, used.ISR) &&
		current.MinISR == used.MinISR &&
		current.LeaseUntil.Equal(used.LeaseUntil) &&
		current.RetentionThroughSeq == used.RetentionThroughSeq &&
		sameWriteFence(current.WriteFence, used.WriteFence) &&
		current.Status == used.Status
}

func sameWriteFence(current ch.WriteFence, used ch.WriteFence) bool {
	return current.Token == used.Token &&
		current.Version == used.Version &&
		current.Reason == used.Reason &&
		current.Until.Equal(used.Until)
}

func cacheableAppendMeta(id ch.ChannelID, meta ch.Meta) bool {
	if !validAppendMetaIdentity(id, meta) || meta.Leader == 0 {
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

func validAppendMetaIdentity(id ch.ChannelID, meta ch.Meta) bool {
	return meta.ID == id && meta.Key == ch.ChannelKeyForID(id)
}
