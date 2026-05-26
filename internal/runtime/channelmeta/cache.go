package channelmeta

import (
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	channelMetaPositiveCacheTTL          = 5 * time.Second
	channelMetaNegativeCacheTTL          = time.Second
	channelMetaActivationCacheShards     = 16
	channelMetaActivationCacheMaxEntries = 4096
)

type cachedChannelMeta struct {
	meta             channel.Meta
	authoritative    metadb.ChannelRuntimeMeta
	hasAuthoritative bool
	expiresAt        time.Time
}

type cachedChannelMetaError struct {
	err       error
	expiresAt time.Time
}

// ActivationCacheGeneration identifies the cache generation observed by one activation load.
type ActivationCacheGeneration struct {
	global uint64
	key    uint64
}

type activationCallKey struct {
	key        channel.ChannelKey
	business   bool
	generation ActivationCacheGeneration
}

// ActivationCache stores short-lived channel activation results and coalesces concurrent loads.
type ActivationCache struct {
	// mu protects singleflight calls and global generation changes.
	mu               sync.Mutex
	calls            map[activationCallKey]*activationCall
	globalGeneration uint64
	pruneMu          sync.Mutex
	lastPruneAt      time.Time
	shards           [channelMetaActivationCacheShards]activationCacheShard
}

type activationCacheShard struct {
	mu          sync.Mutex
	positive    map[channel.ChannelKey]cachedChannelMeta
	negative    map[channel.ChannelKey]cachedChannelMetaError
	generations map[channel.ChannelKey]uint64
	lastPruneAt time.Time
}

// LoadPositive returns a cached channel metadata result when it has not expired.
func (c *ActivationCache) LoadPositive(key channel.ChannelKey, now time.Time) (channel.Meta, bool) {
	entry, ok := c.loadPositiveEntry(key, now)
	if !ok {
		return channel.Meta{}, false
	}
	if entry.meta.WriteFence.Token != "" {
		c.Invalidate(key)
		return channel.Meta{}, false
	}
	return entry.meta, true
}

func (c *ActivationCache) loadPositiveEntry(key channel.ChannelKey, now time.Time) (cachedChannelMeta, bool) {
	if c == nil {
		return cachedChannelMeta{}, false
	}
	shard := c.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	entry, ok := shard.positive[key]
	if !ok {
		return cachedChannelMeta{}, false
	}
	if now.After(entry.expiresAt) {
		delete(shard.positive, key)
		return cachedChannelMeta{}, false
	}
	return entry, true
}

// StorePositive caches a successful activation result and clears stale negative state.
func (c *ActivationCache) StorePositive(key channel.ChannelKey, meta channel.Meta, now time.Time) {
	c.storePositiveEntry(key, cachedChannelMeta{
		meta:      meta,
		expiresAt: now.Add(channelMetaPositiveCacheTTL),
	})
}

// StoreAuthoritativePositive caches an applied routing view with its authoritative source record.
func (c *ActivationCache) StoreAuthoritativePositive(key channel.ChannelKey, applied channel.Meta, authoritative metadb.ChannelRuntimeMeta, now time.Time) {
	authoritative = metadb.NormalizeChannelRuntimeMeta(authoritative)
	c.storePositiveEntry(key, cachedChannelMeta{
		meta:             applied,
		authoritative:    authoritative,
		hasAuthoritative: true,
		expiresAt:        now.Add(channelMetaPositiveCacheTTL),
	})
}

func (c *ActivationCache) storePositiveEntry(key channel.ChannelKey, entry cachedChannelMeta) {
	if c == nil {
		return
	}
	if entry.meta.WriteFence.Token != "" {
		c.Invalidate(key)
		return
	}
	shard := c.shard(key)
	shard.mu.Lock()
	if shard.positive == nil {
		shard.positive = make(map[channel.ChannelKey]cachedChannelMeta)
	}
	if shard.negative != nil {
		delete(shard.negative, key)
	}
	shard.positive[key] = entry
	now := entry.expiresAt.Add(-channelMetaPositiveCacheTTL)
	c.pruneShardForWriteLocked(shard, now)
	shard.mu.Unlock()
	c.pruneForWrite(now)
}

func (c *ActivationCache) storeAuthoritativePositiveAtGeneration(key channel.ChannelKey, applied channel.Meta, authoritative metadb.ChannelRuntimeMeta, now time.Time, generation ActivationCacheGeneration) {
	authoritative = metadb.NormalizeChannelRuntimeMeta(authoritative)
	c.storePositiveEntryAtGeneration(key, cachedChannelMeta{
		meta:             applied,
		authoritative:    authoritative,
		hasAuthoritative: true,
		expiresAt:        now.Add(channelMetaPositiveCacheTTL),
	}, generation)
}

func (c *ActivationCache) storePositiveEntryAtGeneration(key channel.ChannelKey, entry cachedChannelMeta, generation ActivationCacheGeneration) {
	if c == nil {
		return
	}
	if entry.meta.WriteFence.Token != "" {
		c.Invalidate(key)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	shard := c.shard(key)
	shard.mu.Lock()
	if !c.isCurrentGenerationLocked(shard, key, generation) {
		shard.mu.Unlock()
		return
	}
	if shard.positive == nil {
		shard.positive = make(map[channel.ChannelKey]cachedChannelMeta)
	}
	if shard.negative != nil {
		delete(shard.negative, key)
	}
	shard.positive[key] = entry
	now := entry.expiresAt.Add(-channelMetaPositiveCacheTTL)
	c.pruneShardForWriteLocked(shard, now)
	shard.mu.Unlock()
	c.pruneForWrite(now)
}

// LoadNegative returns a cached not-found activation error when it has not expired.
func (c *ActivationCache) LoadNegative(key channel.ChannelKey, now time.Time) error {
	if c == nil {
		return nil
	}
	shard := c.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	entry, ok := shard.negative[key]
	if !ok {
		return nil
	}
	if now.After(entry.expiresAt) {
		delete(shard.negative, key)
		return nil
	}
	return entry.err
}

// StoreNegative caches only not-found activation errors and clears stale positive state.
func (c *ActivationCache) StoreNegative(key channel.ChannelKey, err error, now time.Time) {
	if c == nil || err == nil {
		return
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return
	}
	shard := c.shard(key)
	shard.mu.Lock()
	if shard.negative == nil {
		shard.negative = make(map[channel.ChannelKey]cachedChannelMetaError)
	}
	if shard.positive != nil {
		delete(shard.positive, key)
	}
	shard.negative[key] = cachedChannelMetaError{err: err, expiresAt: now.Add(channelMetaNegativeCacheTTL)}
	c.pruneShardForWriteLocked(shard, now)
	shard.mu.Unlock()
	c.pruneForWrite(now)
}

func (c *ActivationCache) storeNegativeAtGeneration(key channel.ChannelKey, err error, now time.Time, generation ActivationCacheGeneration) {
	if c == nil || err == nil {
		return
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	shard := c.shard(key)
	shard.mu.Lock()
	if !c.isCurrentGenerationLocked(shard, key, generation) {
		shard.mu.Unlock()
		return
	}
	if entry, ok := shard.positive[key]; ok {
		if now.After(entry.expiresAt) {
			delete(shard.positive, key)
		} else {
			shard.mu.Unlock()
			return
		}
	}
	if shard.negative == nil {
		shard.negative = make(map[channel.ChannelKey]cachedChannelMetaError)
	}
	shard.negative[key] = cachedChannelMetaError{err: err, expiresAt: now.Add(channelMetaNegativeCacheTTL)}
	c.pruneShardForWriteLocked(shard, now)
	shard.mu.Unlock()
	c.pruneForWrite(now)
}

// Clear drops cached activation successes and not-found errors.
func (c *ActivationCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.globalGeneration++
	c.mu.Unlock()
	for i := range c.shards {
		shard := &c.shards[i]
		shard.mu.Lock()
		shard.positive = nil
		shard.negative = nil
		shard.generations = nil
		shard.lastPruneAt = time.Time{}
		shard.mu.Unlock()
	}
}

// Invalidate drops cached activation results for a single channel key.
func (c *ActivationCache) Invalidate(key channel.ChannelKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	shard := c.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.positive != nil {
		delete(shard.positive, key)
	}
	if shard.negative != nil {
		delete(shard.negative, key)
	}
	if shard.generations == nil {
		shard.generations = make(map[channel.ChannelKey]uint64)
	}
	shard.generations[key]++
}

func (c *ActivationCache) isCurrentGeneration(key channel.ChannelKey, generation ActivationCacheGeneration) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	shard := c.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return c.isCurrentGenerationLocked(shard, key, generation)
}

func (c *ActivationCache) invalidateIfWriteFenceChanged(key channel.ChannelKey, authoritative metadb.ChannelRuntimeMeta, now time.Time) {
	if c == nil {
		return
	}
	authoritative = metadb.NormalizeChannelRuntimeMeta(authoritative)
	shard := c.shard(key)
	shard.mu.Lock()
	entry, ok := shard.positive[key]
	changed := !ok && hasAuthoritativeWriteFence(authoritative)
	changed = changed || (ok && (!entry.hasAuthoritative ||
		entry.authoritative.RouteGeneration != authoritative.RouteGeneration ||
		entry.meta.RouteGeneration != authoritative.RouteGeneration ||
		entry.authoritative.WriteFenceVersion != authoritative.WriteFenceVersion ||
		entry.authoritative.WriteFenceToken != authoritative.WriteFenceToken ||
		entry.authoritative.WriteFenceReason != authoritative.WriteFenceReason ||
		entry.authoritative.WriteFenceUntilMS != authoritative.WriteFenceUntilMS ||
		entry.meta.WriteFence.Token != ""))
	shard.mu.Unlock()
	if changed {
		c.Invalidate(key)
	}
}

func hasAuthoritativeWriteFence(meta metadb.ChannelRuntimeMeta) bool {
	return meta.WriteFenceVersion != 0 ||
		meta.WriteFenceToken != "" ||
		meta.WriteFenceReason != 0 ||
		meta.WriteFenceUntilMS != 0
}

func (c *ActivationCache) currentGenerationLocked(key channel.ChannelKey) ActivationCacheGeneration {
	shard := c.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return c.currentGenerationForShardLocked(shard, key)
}

func (c *ActivationCache) currentGenerationForShardLocked(shard *activationCacheShard, key channel.ChannelKey) ActivationCacheGeneration {
	var keyGeneration uint64
	if shard.generations != nil {
		keyGeneration = shard.generations[key]
	}
	return ActivationCacheGeneration{global: c.globalGeneration, key: keyGeneration}
}

func (c *ActivationCache) isCurrentGenerationLocked(shard *activationCacheShard, key channel.ChannelKey, generation ActivationCacheGeneration) bool {
	return c.currentGenerationForShardLocked(shard, key) == generation
}

func (c *ActivationCache) shard(key channel.ChannelKey) *activationCacheShard {
	return &c.shards[activationCacheShardIndex(key)]
}

func activationCacheShardIndex(key channel.ChannelKey) int {
	hash := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= 16777619
	}
	return int(hash % channelMetaActivationCacheShards)
}

func (c *ActivationCache) pruneShardForWriteLocked(shard *activationCacheShard, now time.Time) {
	if shouldPruneActivationCacheShard(shard.lastPruneAt, now) {
		shard.lastPruneAt = now
		pruneActivationCacheShardExpiredLocked(shard, now)
	}
	capActivationCacheShardLocked(shard)
}

func (c *ActivationCache) pruneForWrite(now time.Time) {
	if c == nil || !c.pruneMu.TryLock() {
		return
	}
	defer c.pruneMu.Unlock()
	if !shouldPruneActivationCacheShard(c.lastPruneAt, now) {
		return
	}
	c.lastPruneAt = now
	for i := range c.shards {
		shard := &c.shards[i]
		shard.mu.Lock()
		pruneActivationCacheShardExpiredLocked(shard, now)
		capActivationCacheShardLocked(shard)
		shard.lastPruneAt = now
		shard.mu.Unlock()
	}
}

func shouldPruneActivationCacheShard(lastPruneAt, now time.Time) bool {
	if lastPruneAt.IsZero() || now.Before(lastPruneAt) {
		return true
	}
	return now.Sub(lastPruneAt) >= channelMetaNegativeCacheTTL
}

func pruneActivationCacheShardExpiredLocked(shard *activationCacheShard, now time.Time) {
	for key, entry := range shard.positive {
		if now.After(entry.expiresAt) {
			delete(shard.positive, key)
		}
	}
	for key, entry := range shard.negative {
		if now.After(entry.expiresAt) {
			delete(shard.negative, key)
		}
	}
}

func capActivationCacheShardLocked(shard *activationCacheShard) {
	maxEntries := channelMetaActivationCacheMaxEntries / channelMetaActivationCacheShards
	if maxEntries <= 0 {
		maxEntries = 1
	}
	for activationCacheShardEntryCountLocked(shard) > maxEntries {
		evictOldestActivationCacheEntryLocked(shard)
	}
}

func evictOldestActivationCacheEntryLocked(shard *activationCacheShard) {
	var (
		oldestKey      channel.ChannelKey
		oldestExpires  time.Time
		oldestNegative bool
		found          bool
	)
	for key, entry := range shard.positive {
		if !found || entry.expiresAt.Before(oldestExpires) {
			oldestKey = key
			oldestExpires = entry.expiresAt
			oldestNegative = false
			found = true
		}
	}
	for key, entry := range shard.negative {
		if !found || entry.expiresAt.Before(oldestExpires) {
			oldestKey = key
			oldestExpires = entry.expiresAt
			oldestNegative = true
			found = true
		}
	}
	if !found {
		return
	}
	if oldestNegative {
		delete(shard.negative, oldestKey)
		return
	}
	delete(shard.positive, oldestKey)
}

func activationCacheShardEntryCountLocked(shard *activationCacheShard) int {
	return len(shard.positive) + len(shard.negative)
}

func (c *ActivationCache) entryCount() int {
	if c == nil {
		return 0
	}
	total := 0
	for i := range c.shards {
		shard := &c.shards[i]
		shard.mu.Lock()
		total += activationCacheShardEntryCountLocked(shard)
		shard.mu.Unlock()
	}
	return total
}
