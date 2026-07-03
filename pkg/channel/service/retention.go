package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// RetentionView returns one local Channel runtime's retention progress.
func (c *cluster) RetentionView(ctx context.Context, id ch.ChannelID) (ch.RetentionView, error) {
	if c == nil || c.group == nil {
		return ch.RetentionView{}, ch.ErrClosed
	}
	return c.group.RetentionView(ctx, id)
}

// ApplyRetentionBoundary adopts and safely applies a local retention boundary.
func (c *cluster) ApplyRetentionBoundary(ctx context.Context, req ch.RetentionApplyRequest) (ch.RetentionApplyResult, error) {
	if c == nil || c.group == nil {
		return ch.RetentionApplyResult{}, ch.ErrClosed
	}
	return c.group.ApplyRetentionBoundary(ctx, req)
}

var _ ch.RetentionRuntime = (*cluster)(nil)
