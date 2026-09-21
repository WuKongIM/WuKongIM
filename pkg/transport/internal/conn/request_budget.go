package conn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

// NegotiateRequestBudgets uses a normal version-1 RPC so older servers can
// explicitly reject the reserved service. Failure never replays a business RPC.
func (c *Conn) NegotiateRequestBudgets(ctx context.Context) error {
	for {
		if c.capability.Load() != 0 {
			return nil
		}
		c.capabilityMu.Lock()
		if c.capability.Load() != 0 {
			c.capabilityMu.Unlock()
			return nil
		}
		if ready := c.capabilityReady; ready != nil {
			c.capabilityMu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-c.ctx.Done():
				return core.ErrStopped
			}
		}
		ready := make(chan struct{})
		c.capabilityReady = ready
		c.capabilityMu.Unlock()
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		response, err := c.Call(probeCtx, Outbound{ServiceID: wire.CapabilityServiceID, Priority: core.PriorityControl, Payload: core.NewOwnedBuffer([]byte(wire.CapabilityRequestBudgets), nil)})
		cancel()
		state := uint32(0)
		if err == nil {
			if string(response) == wire.CapabilityRequestBudgets {
				state = 2
			} else {
				err = fmt.Errorf("%w: invalid capability response", core.ErrInvalidFrame)
			}
		} else {
			var remote core.RemoteError
			if errors.As(err, &remote) && (remote.Code == core.RemoteErrorCodeServiceNotFound || (remote.Code == core.RemoteErrorCodeGeneric && remote.Message == fmt.Sprintf("transport: service %d not found", wire.CapabilityServiceID))) {
				state = 1
				err = nil
			}
		}
		c.capabilityMu.Lock()
		c.capability.Store(state)
		c.capabilityReady = nil
		close(ready)
		c.capabilityMu.Unlock()
		return err
	}
}

// TrackInbound binds a request budget to this exact connection generation.
// The returned finish function removes tracking and must run on every terminal path.
func (c *Conn) TrackInbound(id uint64, budget time.Duration) (context.Context, func(), error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if budget > 0 {
		ctx, cancel = context.WithTimeout(c.ctx, budget)
	} else {
		ctx, cancel = context.WithCancel(c.ctx)
	}
	c.inboundMu.Lock()
	if c.ctx.Err() != nil {
		c.inboundMu.Unlock()
		cancel()
		return nil, nil, core.ErrStopped
	}
	if c.inboundRequests == nil {
		c.inboundRequests = make(map[uint64]context.CancelFunc)
	}
	if _, exists := c.inboundRequests[id]; exists {
		c.inboundMu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("%w: duplicate request id", core.ErrInvalidFrame)
	}
	c.inboundRequests[id] = cancel
	c.inboundMu.Unlock()
	finish := func() { c.inboundMu.Lock(); delete(c.inboundRequests, id); c.inboundMu.Unlock(); cancel() }
	return ctx, finish, nil
}

// CancelInbound affects only a currently tracked request on this connection.
func (c *Conn) CancelInbound(id uint64) {
	c.inboundMu.Lock()
	cancel := c.inboundRequests[id]
	c.inboundMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Conn) cancelInboundRequests() {
	c.inboundMu.Lock()
	requests := c.inboundRequests
	c.inboundRequests = nil
	c.inboundMu.Unlock()
	for _, cancel := range requests {
		cancel()
	}
}
