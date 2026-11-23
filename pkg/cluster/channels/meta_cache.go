package channels

import (
	"slices"
	"sync"
	"time"

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
	// items retains the latest exact route even after the runtime cools down.
	items map[ch.ChannelID]appendMetaFloor
}

// appendMetaFloor keeps the identity in the map key, the invalidation bit in
// the same entry, and rare write fences out of the common route footprint.
type appendMetaFloor struct {
	key                            ch.ChannelKey
	epoch, leaderEpoch, generation uint64
	leader                         ch.NodeID
	retentionThroughSeq            uint64
	minISR                         int
	leaseUntil                     time.Time
	writeFence                     *ch.WriteFence
	// replicas owns its array. A distinct ISR is stored in the capacity tail.
	replicas                       []ch.NodeID
	status                         ch.Status
	isrMatchesReplicas, appendable bool
	stale                          bool
}

// view borrows immutable rosters while the caller owns the cache lock. Return
// a clone to any consumer outside the cache.
func (f appendMetaFloor) view(id ch.ChannelID) ch.Meta {
	isr := f.replicas[len(f.replicas):cap(f.replicas)]
	if f.isrMatchesReplicas {
		isr = f.replicas
	}
	meta := ch.Meta{
		ID: id, Key: f.key, Epoch: f.epoch, LeaderEpoch: f.leaderEpoch, RouteGeneration: f.generation,
		Leader: f.leader, Replicas: f.replicas, ISR: isr, MinISR: f.minISR, LeaseUntil: f.leaseUntil,
		RetentionThroughSeq: f.retentionThroughSeq, Status: f.status,
	}
	if f.writeFence != nil {
		meta.WriteFence = *f.writeFence
	}
	return meta
}

func (c *channelMetaCache) storeLocked(id ch.ChannelID, meta ch.Meta, appendable bool) {
	floor := appendMetaFloor{
		key: meta.Key, epoch: meta.Epoch, leaderEpoch: meta.LeaderEpoch, generation: meta.RouteGeneration,
		leader: meta.Leader, retentionThroughSeq: meta.RetentionThroughSeq, minISR: meta.MinISR,
		leaseUntil: meta.LeaseUntil, status: meta.Status, appendable: appendable,
		isrMatchesReplicas: slices.Equal(meta.Replicas, meta.ISR),
	}
	if meta.WriteFence != (ch.WriteFence{}) {
		fence := meta.WriteFence
		floor.writeFence = &fence
	}
	rosterSize := len(meta.Replicas)
	if !floor.isrMatchesReplicas {
		rosterSize += len(meta.ISR)
	}
	roster := make([]ch.NodeID, rosterSize)
	n := copy(roster, meta.Replicas)
	if !floor.isrMatchesReplicas {
		copy(roster[n:], meta.ISR)
	}
	floor.replicas = roster[:n]
	if c.items == nil {
		c.items = make(map[ch.ChannelID]appendMetaFloor)
	}
	c.items[id] = floor
}

// get returns one appendable, non-stale cached metadata snapshot.
func (c *channelMetaCache) get(id ch.ChannelID) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	floor, ok := c.items[id]
	if !ok || floor.stale || !floor.appendable {
		return ch.Meta{}, false
	}
	return cloneMeta(floor.view(id)), true
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
	floor, exists := c.items[id]
	current := floor.view(id)
	if exists && !appendMetaNewer(current, meta) {
		accepted := sameCompleteAppendMeta(current, meta)
		if accepted && floor.stale {
			floor.stale = false
			c.items[id] = floor
		}
		return cloneMeta(current), accepted || (!floor.stale && floor.appendable)
	}
	// Modern projections carry an exact route generation, so even a deleting
	// or temporarily non-appendable record is a useful monotonic floor. Legacy
	// generation-zero invalid metadata stays uncached so a corrected record at
	// the same legacy fence can recover.
	appendable := cacheableAppendMeta(id, meta)
	if appendable || meta.RouteGeneration != 0 {
		c.storeLocked(id, meta, appendable)
	}
	return cloneMeta(meta), true
}

// installIfNewerLocked advances the cache floor while the caller holds mu and
// reports whether the candidate was accepted as the selected exact version.
func (c *channelMetaCache) installIfNewerLocked(id ch.ChannelID, meta ch.Meta) (ch.Meta, bool) {
	floor, exists := c.items[id]
	current := floor.view(id)
	if exists && !appendMetaNewer(current, meta) {
		accepted := sameCompleteAppendMeta(current, meta)
		if accepted && floor.stale {
			floor.stale = false
			c.items[id] = floor
		}
		return cloneMeta(current), accepted
	}
	c.storeLocked(id, meta, cacheableAppendMeta(id, meta))
	return cloneMeta(meta), true
}

// preferCurrent keeps a cached monotonic floor when a delayed candidate is not
// newer, without changing the candidate's provenance.
func (c *channelMetaCache) preferCurrent(id ch.ChannelID, candidate ch.Meta) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	floor, ok := c.items[id]
	current := floor.view(id)
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
	floor, ok := c.items[id]
	if !ok || !sameUsedAppendMeta(floor.view(id), used) {
		return false
	}
	return c.markStaleLocked(id, floor)
}

// invalidateAuthority marks a cached version stale only when every supplied
// authority fence matches the installed metadata.
func (c *channelMetaCache) invalidateAuthority(id ch.ChannelID, leader ch.NodeID, epoch uint64, leaderEpoch uint64, routeGeneration uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	floor, ok := c.items[id]
	if !ok || floor.leader != leader || floor.epoch != epoch || floor.leaderEpoch != leaderEpoch ||
		floor.generation != routeGeneration {
		return false
	}
	return c.markStaleLocked(id, floor)
}

// markStaleLocked records one exact invalidation while the caller holds mu.
func (c *channelMetaCache) markStaleLocked(id ch.ChannelID, floor appendMetaFloor) bool {
	if floor.stale {
		return false
	}
	floor.stale = true
	c.items[id] = floor
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
