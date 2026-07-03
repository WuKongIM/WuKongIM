package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
)

const defaultDeliveryPresenceCacheMaxEntries = 65536

type deliveryPresenceCache struct {
	target     presence.Authoritative
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.RWMutex
	entries map[string]deliveryPresenceCacheEntry
}

type deliveryPresenceCacheEntry struct {
	routes    []presence.Route
	expiresAt time.Time
}

func newDeliveryPresenceCache(target presence.Authoritative, ttl time.Duration, maxEntries int, now func() time.Time) presence.Authoritative {
	if target == nil || ttl <= 0 {
		return target
	}
	if maxEntries <= 0 {
		maxEntries = defaultDeliveryPresenceCacheMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	return &deliveryPresenceCache{
		target:     target,
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        now,
		entries:    make(map[string]deliveryPresenceCacheEntry),
	}
}

func (c *deliveryPresenceCache) RegisterAuthoritative(ctx context.Context, cmd presence.RegisterAuthoritativeCommand) (presence.RegisterAuthoritativeResult, error) {
	result, err := c.target.RegisterAuthoritative(ctx, cmd)
	c.invalidateUID(cmd.Route.UID)
	return result, err
}

func (c *deliveryPresenceCache) UnregisterAuthoritative(ctx context.Context, cmd presence.UnregisterAuthoritativeCommand) error {
	err := c.target.UnregisterAuthoritative(ctx, cmd)
	c.invalidateUID(cmd.Route.UID)
	return err
}

func (c *deliveryPresenceCache) HeartbeatAuthoritative(ctx context.Context, cmd presence.HeartbeatAuthoritativeCommand) (presence.HeartbeatAuthoritativeResult, error) {
	return c.target.HeartbeatAuthoritative(ctx, cmd)
}

func (c *deliveryPresenceCache) ReplayAuthoritative(ctx context.Context, cmd presence.ReplayAuthoritativeCommand) error {
	err := c.target.ReplayAuthoritative(ctx, cmd)
	c.clear()
	return err
}

func (c *deliveryPresenceCache) EndpointsByUID(ctx context.Context, uid string) ([]presence.Route, error) {
	now := c.now()
	c.mu.RLock()
	entry, ok := c.entries[uid]
	if ok && now.Before(entry.expiresAt) {
		routes := clonePresenceRoutes(entry.routes)
		c.mu.RUnlock()
		return routes, nil
	}
	c.mu.RUnlock()

	routes, err := c.target.EndpointsByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	cached := clonePresenceRoutes(routes)
	out := clonePresenceRoutes(cached)
	expiresAt := now.Add(c.ttl)
	c.mu.Lock()
	if len(c.entries)+1 > c.maxEntries {
		c.entries = make(map[string]deliveryPresenceCacheEntry)
	}
	if len(cached) == 0 {
		delete(c.entries, uid)
	} else {
		c.entries[uid] = deliveryPresenceCacheEntry{
			routes:    cached,
			expiresAt: expiresAt,
		}
	}
	c.mu.Unlock()
	return out, nil
}

func (c *deliveryPresenceCache) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error) {
	if len(uids) == 0 {
		return map[string][]presence.Route{}, nil
	}
	now := c.now()
	out := make(map[string][]presence.Route, len(uids))
	missing := make([]string, 0, len(uids))

	c.mu.RLock()
	for _, uid := range uids {
		entry, ok := c.entries[uid]
		if ok && now.Before(entry.expiresAt) {
			out[uid] = clonePresenceRoutes(entry.routes)
			continue
		}
		missing = append(missing, uid)
	}
	c.mu.RUnlock()
	if len(missing) == 0 {
		return out, nil
	}

	fresh, err := c.target.EndpointsByUIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	expiresAt := now.Add(c.ttl)
	c.mu.Lock()
	if len(c.entries)+len(fresh) > c.maxEntries {
		c.entries = make(map[string]deliveryPresenceCacheEntry)
	}
	for _, uid := range missing {
		routes := clonePresenceRoutes(fresh[uid])
		out[uid] = routes
		if len(routes) == 0 {
			delete(c.entries, uid)
			continue
		}
		c.entries[uid] = deliveryPresenceCacheEntry{
			routes:    clonePresenceRoutes(routes),
			expiresAt: expiresAt,
		}
	}
	c.mu.Unlock()
	return out, nil
}

func (c *deliveryPresenceCache) invalidateUID(uid string) {
	if c == nil || uid == "" {
		return
	}
	c.mu.Lock()
	delete(c.entries, uid)
	c.mu.Unlock()
}

func (c *deliveryPresenceCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]deliveryPresenceCacheEntry)
	c.mu.Unlock()
}

func clonePresenceRoutes(routes []presence.Route) []presence.Route {
	if len(routes) == 0 {
		return nil
	}
	return append([]presence.Route(nil), routes...)
}
