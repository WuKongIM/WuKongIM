package channels

import (
	"slices"
	"strings"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// MetaCacheObserver receives append metadata cache observations.
type MetaCacheObserver interface {
	ObserveChannelMetaCache(result string)
}

const (
	appendMetaCacheMaxEntries = 32_768
	appendMetaCacheMaxBytes   = 16 << 20
	// Charge the fixed entry, list and map overhead, then add owned payloads.
	appendMetaCacheEntryBytes = 384
)

// channelMetaCache bounds retained reloadable routes and pins their version
// floor across authority reads and runtime application.
type channelMetaCache struct {
	// mu serializes metadata-floor installation and version invalidation.
	mu    sync.RWMutex
	items map[ch.ChannelID]*appendMetaEntry
	// Only idle entries are linked, in order of most recent use.
	newest, oldest *appendMetaEntry
	entries        int
	bytes          int
	evicted        bool
	// canReload limits eviction to identities that the authority can re-read.
	canReload func(ch.ChannelID) bool
}

type appendMetaEntry struct {
	id           ch.ChannelID
	floor        appendMetaFloor
	invalidated  appendRouteVersion
	invalidation uint64
	newer, older *appendMetaEntry
	bytes        int
	users        int
	reloadable   bool
	known        bool
	blocked      bool
	// transient retains an oversized floor only while concurrent callers are pinned.
	transient bool
}

// appendRouteVersion records the highest published version invalidated while
// an evicted entry is being loaded. It owns no route payload.
type appendRouteVersion struct {
	epoch, leaderEpoch, generation uint64
	leader                         ch.NodeID
}

func (v appendRouteVersion) meta() ch.Meta {
	return ch.Meta{Epoch: v.epoch, LeaderEpoch: v.leaderEpoch, RouteGeneration: v.generation, Leader: v.leader}
}

func routeVersionOf(meta ch.Meta) appendRouteVersion {
	return appendRouteVersion{epoch: meta.Epoch, leaderEpoch: meta.LeaderEpoch, generation: meta.RouteGeneration, leader: meta.Leader}
}

func (entry *appendMetaEntry) allows(meta ch.Meta, readStarted uint64) bool {
	return !entry.blocked && (entry.invalidated == (appendRouteVersion{}) ||
		appendMetaNewer(entry.invalidated.meta(), meta) ||
		(entry.invalidation == readStarted && entry.invalidated == routeVersionOf(meta)))
}

// pin protects a channel's floor across an RPC or runtime apply. A cache miss
// after eviction needs a fresh authority read before it can publish.
func (c *channelMetaCache) pin(id ch.ChannelID) (cold bool, readStarted uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.items[id]
	if entry == nil {
		entry = c.addLocked(id)
		if entry == nil {
			return false, 0, false
		}
	}
	if entry.blocked {
		return false, 0, false
	}
	c.unlinkIdleLocked(entry)
	entry.users++
	return entry.reloadable && (entry.transient || (c.evicted && !entry.known)), entry.invalidation, true
}

func (c *channelMetaCache) unpin(id ch.ChannelID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.items[id]
	entry.users--
	if entry.users == 0 {
		if !entry.known || entry.blocked || entry.transient {
			c.removeLocked(entry)
		} else if entry.reloadable {
			c.linkNewestLocked(entry)
		}
	}
}

func (c *channelMetaCache) addLocked(id ch.ChannelID) *appendMetaEntry {
	reloadable := c.canReload == nil || c.canReload(id)
	charge := 0
	if reloadable {
		charge = appendMetaCacheEntryBytes + len(id.ID)
		if !c.makeRoomLocked(1, charge) {
			return nil
		}
		c.entries++
		c.bytes += charge
	}
	if c.items == nil {
		c.items = make(map[ch.ChannelID]*appendMetaEntry)
	}
	id.ID = strings.Clone(id.ID)
	entry := &appendMetaEntry{id: id, bytes: charge, reloadable: reloadable}
	c.items[id] = entry
	return entry
}

func (c *channelMetaCache) makeRoomLocked(entries, bytes int) bool {
	if bytes > appendMetaCacheMaxBytes {
		return false
	}
	for c.entries+entries > appendMetaCacheMaxEntries || c.bytes+bytes > appendMetaCacheMaxBytes {
		if c.oldest == nil {
			return false
		}
		c.removeLocked(c.oldest)
		c.evicted = true
	}
	return true
}

func (c *channelMetaCache) removeLocked(entry *appendMetaEntry) {
	c.unlinkIdleLocked(entry)
	delete(c.items, entry.id)
	if entry.reloadable {
		c.entries--
		c.bytes -= entry.bytes
	}
}

func (c *channelMetaCache) linkNewestLocked(entry *appendMetaEntry) {
	entry.older = c.newest
	if c.newest == nil {
		c.oldest = entry
	} else {
		c.newest.newer = entry
	}
	c.newest = entry
}

func (c *channelMetaCache) unlinkIdleLocked(entry *appendMetaEntry) {
	if entry.newer == nil && c.newest != entry {
		return
	}
	if entry.newer == nil {
		c.newest = entry.older
	} else {
		entry.newer.older = entry.older
	}
	if entry.older == nil {
		c.oldest = entry.newer
	} else {
		entry.older.newer = entry.newer
	}
	entry.newer, entry.older = nil, nil
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

func (c *channelMetaCache) storeLocked(id ch.ChannelID, meta ch.Meta, appendable bool) bool {
	sameRoster := slices.Equal(meta.Replicas, meta.ISR)
	rosterSize := len(meta.Replicas)
	if !sameRoster {
		rosterSize += len(meta.ISR)
	}
	entry := c.items[id]
	if entry == nil {
		entry = c.addLocked(id)
		if entry == nil {
			return false
		}
	}
	c.unlinkIdleLocked(entry)
	if entry.reloadable {
		charge := appendMetaCacheEntryBytes + len(id.ID) + len(meta.Key) + len(meta.WriteFence.Token) + rosterSize*8
		if charge > appendMetaCacheMaxBytes {
			// Preserve the exact floor for in-flight readers, then require
			// authority again once the last pin is released.
			c.evicted = true
			if entry.users == 0 {
				c.removeLocked(entry)
				return true
			}
			charge = appendMetaCacheEntryBytes + len(id.ID)
			entry.transient = true
		} else if !c.makeRoomLocked(0, charge-entry.bytes) {
			c.evicted = true
			if entry.users == 0 {
				c.removeLocked(entry)
			} else {
				// A newer version was seen but could not be installed. Reject
				// older results until a new authority read can start.
				entry.blocked = true
			}
			return false
		} else {
			entry.transient = false
		}
		c.bytes += charge - entry.bytes
		entry.bytes = charge
	}
	floor := appendMetaFloor{
		key: ch.ChannelKey(strings.Clone(string(meta.Key))), epoch: meta.Epoch, leaderEpoch: meta.LeaderEpoch, generation: meta.RouteGeneration,
		leader: meta.Leader, retentionThroughSeq: meta.RetentionThroughSeq, minISR: meta.MinISR,
		leaseUntil: meta.LeaseUntil, status: meta.Status, appendable: appendable,
		isrMatchesReplicas: sameRoster,
	}
	if meta.WriteFence != (ch.WriteFence{}) {
		fence := meta.WriteFence
		if !entry.transient {
			fence.Token = strings.Clone(fence.Token)
		}
		floor.writeFence = &fence
	}
	roster := make([]ch.NodeID, rosterSize)
	n := copy(roster, meta.Replicas)
	if !floor.isrMatchesReplicas {
		copy(roster[n:], meta.ISR)
	}
	floor.replicas = roster[:n]
	entry.floor, entry.known, entry.invalidated = floor, true, appendRouteVersion{}
	if entry.reloadable && entry.users == 0 {
		c.linkNewestLocked(entry)
	}
	return true
}

// get returns one appendable, non-stale cached metadata snapshot.
func (c *channelMetaCache) get(id ch.ChannelID) (ch.Meta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.items[id]
	if entry == nil || !entry.known || entry.blocked || entry.transient || entry.floor.stale || !entry.floor.appendable {
		return ch.Meta{}, false
	}
	if entry.reloadable && entry.users == 0 && c.newest != entry {
		c.unlinkIdleLocked(entry)
		c.linkNewestLocked(entry)
	}
	return cloneMeta(entry.floor.view(id)), true
}

// installIfNewer advances the monotonic cache floor and returns the selected
// newest snapshot.
func (c *channelMetaCache) installIfNewer(id ch.ChannelID, meta ch.Meta, readStarted uint64) ch.Meta {
	c.mu.Lock()
	defer c.mu.Unlock()
	installed, _ := c.installIfNewerLocked(id, meta, readStarted)
	return installed
}

// selectResolved compares one authoritative resolve result against the
// monotonic floor and reports whether the result is fresh enough to use.
// Appendability is deliberately separate: known non-appendable metadata is
// retained as a floor but must not become a hot route.
func (c *channelMetaCache) selectResolved(id ch.ChannelID, meta ch.Meta, readStarted uint64) (ch.Meta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.items[id]
	if entry != nil && !entry.allows(meta, readStarted) {
		return ch.Meta{}, false
	}
	if entry != nil && entry.known && !appendMetaNewer(entry.floor.view(id), meta) {
		current := entry.floor.view(id)
		accepted := sameCompleteAppendMeta(current, meta)
		if accepted {
			entry.floor.stale = false
		}
		return cloneMeta(current), accepted || (!entry.floor.stale && entry.floor.appendable)
	}
	// Modern projections carry an exact route generation, so even a deleting
	// or temporarily non-appendable record is a useful monotonic floor. Legacy
	// generation-zero invalid metadata stays uncached so a corrected record at
	// the same legacy fence can recover.
	appendable := cacheableAppendMeta(id, meta)
	if appendable || meta.RouteGeneration != 0 {
		if !c.storeLocked(id, meta, appendable) {
			return ch.Meta{}, false
		}
	}
	return cloneMeta(meta), true
}

// installIfNewerLocked advances the cache floor while the caller holds mu and
// reports whether the candidate was accepted as the selected exact version.
func (c *channelMetaCache) installIfNewerLocked(id ch.ChannelID, meta ch.Meta, readStarted uint64) (ch.Meta, bool) {
	entry := c.items[id]
	if entry != nil && !entry.allows(meta, readStarted) {
		return ch.Meta{}, false
	}
	if entry != nil && entry.known && !appendMetaNewer(entry.floor.view(id), meta) {
		current := entry.floor.view(id)
		accepted := sameCompleteAppendMeta(current, meta)
		if accepted {
			entry.floor.stale = false
		}
		return cloneMeta(current), accepted
	}
	return cloneMeta(meta), c.storeLocked(id, meta, cacheableAppendMeta(id, meta))
}

// selectForApply keeps a cached monotonic floor when a delayed candidate is
// not newer, without changing the candidate's provenance.
func (c *channelMetaCache) selectForApply(id ch.ChannelID, candidate ch.Meta, readStarted uint64) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry := c.items[id]
	if entry != nil && !entry.allows(candidate, readStarted) {
		return ch.Meta{}, false
	}
	if entry != nil && entry.known && !appendMetaNewer(entry.floor.view(id), candidate) {
		return cloneMeta(entry.floor.view(id)), true
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
	entry := c.items[id]
	if entry == nil {
		return false
	}
	if !entry.known {
		c.recordInvalidationLocked(entry, routeVersionOf(used))
		return false
	}
	if !sameUsedAppendMeta(entry.floor.view(id), used) {
		return false
	}
	c.recordInvalidationLocked(entry, routeVersionOf(entry.floor.view(id)))
	return markAppendMetaStale(entry)
}

// invalidateAuthority marks a cached version stale only when every supplied
// authority fence matches the installed metadata.
func (c *channelMetaCache) invalidateAuthority(id ch.ChannelID, leader ch.NodeID, epoch uint64, leaderEpoch uint64, routeGeneration uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.items[id]
	if entry == nil {
		return false
	}
	if !entry.known {
		c.recordInvalidationLocked(entry, appendRouteVersion{epoch: epoch, leaderEpoch: leaderEpoch, generation: routeGeneration, leader: leader})
		return false
	}
	if entry.floor.leader != leader || entry.floor.epoch != epoch ||
		entry.floor.leaderEpoch != leaderEpoch || entry.floor.generation != routeGeneration {
		return false
	}
	c.recordInvalidationLocked(entry, routeVersionOf(entry.floor.view(id)))
	return markAppendMetaStale(entry)
}

func (c *channelMetaCache) recordInvalidationLocked(entry *appendMetaEntry, version appendRouteVersion) {
	if !entry.reloadable || (!entry.known && !c.evicted) || version.leader == 0 {
		return
	}
	if entry.invalidated == (appendRouteVersion{}) || entry.invalidated == version ||
		appendMetaNewer(entry.invalidated.meta(), version.meta()) {
		entry.invalidated = version
		entry.invalidation++
	}
}

// markAppendMetaStale records one exact invalidation while the caller holds mu.
func markAppendMetaStale(entry *appendMetaEntry) bool {
	if entry.floor.stale {
		return false
	}
	entry.floor.stale = true
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
