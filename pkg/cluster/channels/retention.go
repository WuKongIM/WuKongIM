package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// RetentionView returns local retention state when the wrapped runtime supports it.
func (s *Service) RetentionView(ctx context.Context, id ch.ChannelID) (ch.RetentionView, error) {
	retention, ok := s.runtime.(ch.RetentionRuntime)
	if !ok {
		return ch.RetentionView{}, ch.ErrInvalidConfig
	}
	return retention.RetentionView(ctx, id)
}

// ApplyRetentionBoundary adopts a logical boundary and performs bounded physical cleanup when safe.
func (s *Service) ApplyRetentionBoundary(ctx context.Context, req ch.RetentionApplyRequest) (ch.RetentionApplyResult, error) {
	retention, ok := s.runtime.(ch.RetentionRuntime)
	if !ok {
		return ch.RetentionApplyResult{}, ch.ErrInvalidConfig
	}
	return retention.ApplyRetentionBoundary(ctx, req)
}
