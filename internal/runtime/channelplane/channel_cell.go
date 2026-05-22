package channelplane

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const maxRouteInvalidationRetries = 1

type channelCell struct {
	reactor  *reactor
	key      channel.ChannelKey
	route    *ChannelRoute
	pending  []*appendCommand
	inflight *appendCommand
}

func newChannelCell(reactor *reactor, key channel.ChannelKey) *channelCell {
	return &channelCell{reactor: reactor, key: key}
}

func (c *channelCell) enqueue(cmd *appendCommand) {
	if len(c.pending) >= c.reactor.plane.opts.MaxPendingPerChannel {
		cmd.future.complete(channel.AppendBatchResult{}, ErrOverloaded)
		return
	}
	c.pending = append(c.pending, cmd)
}

func (c *channelCell) tryStart() {
	if c.inflight != nil {
		return
	}
	for len(c.pending) > 0 {
		cmd := c.pending[0]
		c.pending[0] = nil
		c.pending = c.pending[1:]
		if err := effectContext(cmd).Err(); err != nil {
			cmd.future.complete(channel.AppendBatchResult{}, err)
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
	go func() {
		route, err := c.reactor.plane.opts.Resolver.ResolveRoute(effectContext(cmd), cmd.req.ChannelID)
		c.reactor.post(reactorEvent{kind: reactorEventResolveComplete, completion: effectCompletion{key: c.key, cmd: cmd, route: route, err: err}})
	}()
}

func (c *channelCell) handleResolveComplete(done effectCompletion) {
	if c.inflight != done.cmd {
		return
	}
	if done.err != nil {
		done.cmd.future.complete(channel.AppendBatchResult{}, done.err)
		c.inflight = nil
		c.tryStart()
		return
	}
	c.route = &done.route
	c.startAppend(done.cmd, done.route)
}

func (c *channelCell) startAppend(cmd *appendCommand, route ChannelRoute) {
	go func() {
		req := route.applyTo(cmd.req)
		var (
			res channel.AppendBatchResult
			err error
		)
		if route.IsLocal(c.reactor.plane.opts.LocalNode) {
			res, err = c.reactor.plane.opts.LocalOwner.AppendLocalBatch(effectContext(cmd), req)
		} else if c.reactor.plane.peer != nil {
			res, err = c.reactor.plane.peer.AppendRemoteBatch(effectContext(cmd), route.Leader, req, route)
		} else {
			err = ErrNoRemoteAppender
		}
		c.reactor.post(reactorEvent{kind: reactorEventAppendComplete, completion: effectCompletion{key: c.key, cmd: cmd, route: route, res: res, err: err}})
	}()
}

func (c *channelCell) handleAppendComplete(done effectCompletion) {
	if c.inflight != done.cmd {
		return
	}
	if isRouteInvalidationError(done.err) && c.route != nil {
		c.reactor.plane.opts.Resolver.InvalidateRoute(done.cmd.req.ChannelID, done.route.RouteGeneration)
		c.route = nil
		if c.retryAfterRouteInvalidation(done.cmd) {
			return
		}
	}
	observeAppendCompleted(c.reactor.plane.opts.Observer, done.cmd.req, done.route, done.err)
	done.cmd.future.complete(done.res, done.err)
	c.inflight = nil
	c.tryStart()
}

// retryAfterRouteInvalidation gives a command one fresh route lookup after a fenced append attempt.
func (c *channelCell) retryAfterRouteInvalidation(cmd *appendCommand) bool {
	if cmd.routeInvalidationRetries >= maxRouteInvalidationRetries {
		return false
	}
	if err := effectContext(cmd).Err(); err != nil {
		return false
	}
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

func (c *channelCell) failAll(err error) {
	if c.inflight != nil {
		c.inflight.future.complete(channel.AppendBatchResult{}, err)
		c.inflight = nil
	}
	for _, cmd := range c.pending {
		cmd.future.complete(channel.AppendBatchResult{}, err)
	}
	c.pending = nil
}

func isRouteInvalidationError(err error) bool {
	return errors.Is(err, ErrStaleRoute) || errors.Is(err, channel.ErrStaleMeta) || errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrLeaseExpired) || errors.Is(err, channel.ErrWriteFenced)
}
