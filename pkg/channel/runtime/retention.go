package runtime

import (
	"context"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *runtime) ApplyRetentionBoundary(ctx context.Context, key core.ChannelKey, throughSeq uint64) error {
	ch, ok := r.lookupChannel(key)
	if !ok {
		return ErrChannelNotFound
	}
	return ch.replica.ApplyRetentionBoundary(ctx, throughSeq)
}

func (r *runtime) RetentionView(key core.ChannelKey) (core.RetentionView, error) {
	ch, ok := r.lookupChannel(key)
	if !ok {
		return core.RetentionView{}, ErrChannelNotFound
	}
	return ch.replica.RetentionView()
}
