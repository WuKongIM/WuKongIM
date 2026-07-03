package runtime

import (
	"context"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func (r *runtime) ApplyRetentionBoundary(ctx context.Context, key core.ChannelKey, throughSeq uint64) error {
	ch, ok := r.lookupChannel(key)
	if !ok {
		return ErrChannelNotFound
	}
	if !ch.beginUse() {
		return ErrChannelNotFound
	}
	defer ch.endUse()
	return ch.replica.ApplyRetentionBoundary(ctx, throughSeq)
}

func (r *runtime) RetentionView(key core.ChannelKey) (core.RetentionView, error) {
	ch, ok := r.lookupChannel(key)
	if !ok {
		return core.RetentionView{}, ErrChannelNotFound
	}
	if !ch.beginUse() {
		return core.RetentionView{}, ErrChannelNotFound
	}
	defer ch.endUse()
	return ch.replica.RetentionView()
}
