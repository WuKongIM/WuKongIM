package message

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const permissionCacheMaxEntries = 65536

type permissionCache struct {
	next PermissionStore
	ttl  time.Duration
	now  func() time.Time

	mu       sync.Mutex
	channels map[permissionCacheChannelKey]permissionCacheEntry[metadb.Channel]
	contains map[permissionCacheContainsKey]permissionCacheEntry[bool]
	hasAny   map[permissionCacheChannelKey]permissionCacheEntry[bool]
}

type permissionCacheChannelKey struct {
	channelID   string
	channelType int64
}

func (k permissionCacheChannelKey) String() string {
	return k.channelID + "#" + strconv.FormatInt(k.channelType, 10)
}

type permissionCacheContainsKey struct {
	channelID   string
	channelType int64
	uid         string
}

func (k permissionCacheContainsKey) String() string {
	return permissionCacheChannelKey{channelID: k.channelID, channelType: k.channelType}.String() + "#" + k.uid
}

type permissionCacheEntry[T any] struct {
	value     T
	err       error
	expiresAt time.Time
}

func newPermissionCache(next PermissionStore, ttl time.Duration, now func() time.Time) PermissionStore {
	if next == nil || ttl <= 0 {
		return next
	}
	if now == nil {
		now = time.Now
	}
	return &permissionCache{
		next:     next,
		ttl:      ttl,
		now:      now,
		channels: make(map[permissionCacheChannelKey]permissionCacheEntry[metadb.Channel]),
		contains: make(map[permissionCacheContainsKey]permissionCacheEntry[bool]),
		hasAny:   make(map[permissionCacheChannelKey]permissionCacheEntry[bool]),
	}
}

func (c *permissionCache) GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	key := permissionCacheChannelKey{channelID: channelID, channelType: channelType}
	now := c.now()
	if value, err, ok := permissionCacheGet(c, c.channels, key, now); ok {
		return value, err
	}

	value, err := c.next.GetChannelForPermission(ctx, channelID, channelType)
	if errors.Is(err, metadb.ErrNotFound) {
		permissionCachePut(c, c.channels, key, metadb.Channel{}, err, now.Add(c.ttl))
		return metadb.Channel{}, err
	}
	if err != nil {
		return metadb.Channel{}, err
	}
	permissionCachePut(c, c.channels, key, value, nil, now.Add(c.ttl))
	return value, nil
}

func (c *permissionCache) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	key := permissionCacheContainsKey{channelID: channelID, channelType: channelType, uid: uid}
	now := c.now()
	if value, _, ok := permissionCacheGet(c, c.contains, key, now); ok {
		return value, nil
	}

	value, err := c.next.ContainsChannelSubscriber(ctx, channelID, channelType, uid)
	if err != nil {
		return false, err
	}
	permissionCachePut(c, c.contains, key, value, nil, now.Add(c.ttl))
	return value, nil
}

func (c *permissionCache) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	key := permissionCacheChannelKey{channelID: channelID, channelType: channelType}
	now := c.now()
	if value, _, ok := permissionCacheGet(c, c.hasAny, key, now); ok {
		return value, nil
	}

	value, err := c.next.HasChannelSubscribers(ctx, channelID, channelType)
	if err != nil {
		return false, err
	}
	permissionCachePut(c, c.hasAny, key, value, nil, now.Add(c.ttl))
	return value, nil
}

func permissionCacheGet[K comparable, V any](c *permissionCache, values map[K]permissionCacheEntry[V], key K, now time.Time) (V, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := values[key]
	if !ok || !now.Before(entry.expiresAt) {
		var zero V
		if ok {
			delete(values, key)
		}
		return zero, nil, false
	}
	return entry.value, entry.err, true
}

func permissionCachePut[K comparable, V any](c *permissionCache, values map[K]permissionCacheEntry[V], key K, value V, err error, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(values) >= permissionCacheMaxEntries {
		clear(values)
	}
	values[key] = permissionCacheEntry[V]{value: value, err: err, expiresAt: expiresAt}
}
