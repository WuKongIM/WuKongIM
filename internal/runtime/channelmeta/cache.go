package channelmeta

import (
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

const (
	channelMetaPositiveCacheTTL = 5 * time.Second
	channelMetaNegativeCacheTTL = time.Second
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

// ActivationCache stores short-lived channel activation results and coalesces concurrent loads.
type ActivationCache struct {
	mu       sync.Mutex
	positive map[channel.ChannelKey]cachedChannelMeta
	negative map[channel.ChannelKey]cachedChannelMetaError
	calls    map[channel.ChannelKey]*activationCall
}

// LoadPositive returns a cached channel metadata result when it has not expired.
func (c *ActivationCache) LoadPositive(key channel.ChannelKey, now time.Time) (channel.Meta, bool) {
	entry, ok := c.loadPositiveEntry(key, now)
	if !ok {
		return channel.Meta{}, false
	}
	return entry.meta, true
}

func (c *ActivationCache) loadPositiveEntry(key channel.ChannelKey, now time.Time) (cachedChannelMeta, bool) {
	if c == nil {
		return cachedChannelMeta{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.positive[key]
	if !ok {
		return cachedChannelMeta{}, false
	}
	if now.After(entry.expiresAt) {
		delete(c.positive, key)
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.positive == nil {
		c.positive = make(map[channel.ChannelKey]cachedChannelMeta)
	}
	if c.negative != nil {
		delete(c.negative, key)
	}
	c.positive[key] = entry
}

// LoadNegative returns a cached not-found activation error when it has not expired.
func (c *ActivationCache) LoadNegative(key channel.ChannelKey, now time.Time) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.negative[key]
	if !ok {
		return nil
	}
	if now.After(entry.expiresAt) {
		delete(c.negative, key)
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.negative == nil {
		c.negative = make(map[channel.ChannelKey]cachedChannelMetaError)
	}
	if c.positive != nil {
		delete(c.positive, key)
	}
	c.negative[key] = cachedChannelMetaError{err: err, expiresAt: now.Add(channelMetaNegativeCacheTTL)}
}

// Clear drops cached activation successes and not-found errors.
func (c *ActivationCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.positive = nil
	c.negative = nil
	c.mu.Unlock()
}

// Invalidate drops cached activation results for a single channel key.
func (c *ActivationCache) Invalidate(key channel.ChannelKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.positive != nil {
		delete(c.positive, key)
	}
	if c.negative != nil {
		delete(c.negative, key)
	}
	c.mu.Unlock()
}
