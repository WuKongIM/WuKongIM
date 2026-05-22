package channelplane

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// RouteSource performs the underlying authoritative route lookup.
type RouteSource interface {
	ResolveRoute(ctx context.Context, id channel.ChannelID) (ChannelRoute, error)
}

// Resolver coalesces concurrent authoritative route misses for the same channel.
type Resolver struct {
	source RouteSource
	mu     sync.Mutex
	calls  map[channel.ChannelID]*routeCall
	cache  map[channel.ChannelID]ChannelRoute
}

type routeCall struct {
	done  chan struct{}
	route ChannelRoute
	err   error
}

// NewRouteResolver wraps a source with per-channel singleflight behavior.
func NewRouteResolver(source RouteSource) *Resolver {
	return &Resolver{source: source, calls: make(map[channel.ChannelID]*routeCall), cache: make(map[channel.ChannelID]ChannelRoute)}
}

// ResolveRoute returns an authoritative write route, coalescing concurrent misses.
func (r *Resolver) ResolveRoute(ctx context.Context, id channel.ChannelID) (ChannelRoute, error) {
	if r == nil || r.source == nil {
		return ChannelRoute{}, channel.ErrInvalidConfig
	}
	r.mu.Lock()
	if route, ok := r.cache[id]; ok {
		r.mu.Unlock()
		return route, nil
	}
	if call := r.calls[id]; call != nil {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.route, call.err
		case <-ctx.Done():
			return ChannelRoute{}, ctx.Err()
		}
	}
	call := &routeCall{done: make(chan struct{})}
	r.calls[id] = call
	r.mu.Unlock()

	call.route, call.err = r.source.ResolveRoute(ctx, id)

	r.mu.Lock()
	if call.err == nil {
		r.cache[id] = call.route
	}
	delete(r.calls, id)
	r.mu.Unlock()
	close(call.done)
	return call.route, call.err
}

// InvalidateRoute drops cached state for one channel route.
func (r *Resolver) InvalidateRoute(id channel.ChannelID, _ uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.cache, id)
	r.mu.Unlock()
}
