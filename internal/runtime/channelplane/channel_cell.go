package channelplane

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

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
		} else if c.reactor.plane.opts.RemoteAppender != nil {
			res, err = c.reactor.plane.opts.RemoteAppender.AppendRemoteBatch(effectContext(cmd), route.Leader, req, route)
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
		c.reactor.plane.opts.Resolver.InvalidateRoute(done.cmd.req.ChannelID, c.route.RouteGeneration)
		c.route = nil
	}
	observeAppendCompleted(c.reactor.plane.opts.Observer, done.cmd.req, done.route, done.err)
	done.cmd.future.complete(done.res, done.err)
	c.inflight = nil
	c.tryStart()
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
	return errors.Is(err, ErrStaleRoute) || errors.Is(err, channel.ErrStaleMeta) || errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrLeaseExpired)
}
