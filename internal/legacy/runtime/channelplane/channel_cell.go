package channelplane

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const maxRouteInvalidationRetries = 1

type channelCell struct {
	reactor  *reactor
	key      channel.ChannelID
	route    *ChannelRoute
	pending  []*appendCommand
	inflight *appendCommand
	// lastActive records the last reactor-owned activity time for idle eviction.
	lastActive time.Time
}

func newChannelCell(reactor *reactor, key channel.ChannelID) *channelCell {
	return &channelCell{reactor: reactor, key: key}
}

func (c *channelCell) enqueue(cmd *appendCommand) bool {
	if err := effectContext(cmd).Err(); err != nil {
		c.complete(cmd, channel.AppendBatchResult{}, err, ChannelRoute{})
		return false
	}
	c.touch()
	c.compactCanceledPending()
	if len(c.pending) >= c.reactor.plane.opts.MaxPendingPerChannel {
		c.complete(cmd, channel.AppendBatchResult{}, ErrOverloaded, ChannelRoute{})
		return false
	}
	c.pending = append(c.pending, cmd)
	return true
}

func (c *channelCell) tryStart() {
	if c.inflight != nil {
		return
	}
	for len(c.pending) > 0 {
		c.touch()
		cmd := c.pending[0]
		c.pending[0] = nil
		c.pending = c.pending[1:]
		if err := effectContext(cmd).Err(); err != nil {
			c.complete(cmd, channel.AppendBatchResult{}, err, ChannelRoute{})
			continue
		}
		c.inflight = cmd
		if c.route == nil {
			c.startResolve(cmd)
			return
		}
		if c.cachedRouteExpired(*c.route) {
			c.reactor.plane.opts.Resolver.InvalidateRoute(cmd.req.ChannelID, c.route.RouteGeneration)
			c.route = nil
			c.startResolve(cmd)
			return
		}
		c.startAppend(cmd, *c.route)
		return
	}
}

func (c *channelCell) startResolve(cmd *appendCommand) {
	err := c.reactor.plane.effects.submit(effectContext(cmd), func() {
		route, err := c.reactor.plane.opts.Resolver.ResolveRoute(effectContext(cmd), cmd.req.ChannelID)
		c.reactor.post(reactorEvent{kind: reactorEventResolveComplete, completion: effectCompletion{key: c.key, cmd: cmd, route: route, err: err}})
	})
	if err != nil {
		c.handleResolveComplete(effectCompletion{key: c.key, cmd: cmd, err: err})
	}
}

func (c *channelCell) handleResolveComplete(done effectCompletion) {
	if c.inflight != done.cmd {
		return
	}
	c.touch()
	if done.err != nil {
		c.complete(done.cmd, channel.AppendBatchResult{}, done.err, done.route)
		c.inflight = nil
		c.scheduleIfPending()
		return
	}
	c.route = &done.route
	c.startAppend(done.cmd, done.route)
}

func (c *channelCell) startAppend(cmd *appendCommand, route ChannelRoute) {
	if !route.IsLocal(c.reactor.plane.opts.LocalNode) {
		c.startRemoteAppend(cmd, route)
		return
	}
	c.startLocalAppend(cmd, route)
}

func (c *channelCell) startLocalAppend(cmd *appendCommand, route ChannelRoute) {
	err := c.reactor.plane.effects.submit(effectContext(cmd), func() {
		req := route.applyTo(cmd.req)
		res, err := c.reactor.plane.opts.LocalOwner.AppendLocalBatch(effectContext(cmd), req)
		c.reactor.post(reactorEvent{kind: reactorEventAppendComplete, completion: effectCompletion{key: c.key, cmd: cmd, route: route, res: res, err: err}})
	})
	if err != nil {
		c.handleAppendComplete(effectCompletion{key: c.key, cmd: cmd, route: route, err: err})
	}
}

func (c *channelCell) startRemoteAppend(cmd *appendCommand, route ChannelRoute) {
	if c.reactor.plane.peer == nil {
		c.handleAppendComplete(effectCompletion{key: c.key, cmd: cmd, route: route, err: ErrNoRemoteAppender})
		return
	}
	req := route.applyTo(cmd.req)
	err := c.reactor.plane.peer.AppendRemoteBatchAsync(effectContext(cmd), route.Leader, req, route, func(res channel.AppendBatchResult, err error) {
		c.reactor.post(reactorEvent{kind: reactorEventAppendComplete, completion: effectCompletion{key: c.key, cmd: cmd, route: route, res: res, err: err}})
	})
	if err != nil {
		c.handleAppendComplete(effectCompletion{key: c.key, cmd: cmd, route: route, err: err})
	}
}

func (c *channelCell) handleAppendComplete(done effectCompletion) {
	if c.inflight != done.cmd {
		return
	}
	c.touch()
	if isRouteInvalidationError(done.err) && c.route != nil {
		c.reactor.plane.opts.Resolver.InvalidateRoute(done.cmd.req.ChannelID, done.route.RouteGeneration)
		c.route = nil
		if c.retryAfterRouteInvalidation(done.cmd) {
			return
		}
	}
	c.complete(done.cmd, done.res, done.err, done.route)
	c.inflight = nil
	c.scheduleIfPending()
}

// retryAfterRouteInvalidation gives a command one fresh route lookup after a fenced append attempt.
func (c *channelCell) retryAfterRouteInvalidation(cmd *appendCommand) bool {
	if cmd.routeInvalidationRetries >= maxRouteInvalidationRetries {
		return false
	}
	if err := effectContext(cmd).Err(); err != nil {
		return false
	}
	c.touch()
	cmd.routeInvalidationRetries++
	c.startResolve(cmd)
	return true
}

// cachedRouteExpired reports whether a remembered route lease can no longer admit a write.
func (c *channelCell) cachedRouteExpired(route ChannelRoute) bool {
	if route.LeaseUntil.IsZero() {
		return false
	}
	return !c.reactor.plane.opts.Now().Before(route.LeaseUntil)
}

func (c *channelCell) complete(cmd *appendCommand, res channel.AppendBatchResult, err error, route ChannelRoute) {
	if cmd.future.complete(res, err) {
		observeAppendCompleted(c.reactor.plane.opts.Observer, cmd.req, route, err)
	}
}

func (c *channelCell) compactCanceledPending() {
	if len(c.pending) == 0 {
		return
	}
	write := 0
	for _, cmd := range c.pending {
		if cmd == nil {
			continue
		}
		if err := effectContext(cmd).Err(); err != nil {
			c.complete(cmd, channel.AppendBatchResult{}, err, ChannelRoute{})
			continue
		}
		c.pending[write] = cmd
		write++
	}
	for i := write; i < len(c.pending); i++ {
		c.pending[i] = nil
	}
	c.pending = c.pending[:write]
}

func (c *channelCell) scheduleIfPending() {
	c.compactCanceledPending()
	if len(c.pending) > 0 {
		c.reactor.markReady(c.key)
	}
}

func (c *channelCell) touch() {
	c.lastActive = c.reactor.plane.opts.Now()
}

func (c *channelCell) idleExpired(now time.Time, ttl time.Duration) bool {
	return c.inflight == nil && len(c.pending) == 0 && ttl > 0 && !c.lastActive.IsZero() && now.Sub(c.lastActive) >= ttl
}

func (c *channelCell) failAll(err error) {
	if c.inflight != nil {
		c.complete(c.inflight, channel.AppendBatchResult{}, err, ChannelRoute{})
		c.inflight = nil
	}
	for _, cmd := range c.pending {
		c.complete(cmd, channel.AppendBatchResult{}, err, ChannelRoute{})
	}
	c.pending = nil
}

func isRouteInvalidationError(err error) bool {
	return errors.Is(err, ErrStaleRoute) || errors.Is(err, channel.ErrStaleMeta) || errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrLeaseExpired) || errors.Is(err, channel.ErrWriteFenced)
}
