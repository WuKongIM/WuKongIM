package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (c *cluster) RuntimeSnapshot(ctx context.Context) (ch.RuntimeSnapshot, error) {
	return c.group.RuntimeSnapshot(ctx)
}

func (c *cluster) RuntimeProbe(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	return c.group.RuntimeProbe(ctx, selector)
}

func (c *cluster) DrainChannel(ctx context.Context, req ch.DrainChannelRequest) (ch.DrainChannelResult, error) {
	return c.group.DrainChannel(ctx, req)
}

func (c *cluster) RuntimeEvict(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error) {
	return c.group.RuntimeEvict(ctx, selector)
}

var _ ch.RuntimeBench = (*cluster)(nil)
var _ ch.RuntimeDrain = (*cluster)(nil)
