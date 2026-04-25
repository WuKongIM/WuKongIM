package runtime

import (
	"context"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type ActivationSource string

const (
	ActivationSourceBusiness ActivationSource = "business"
	ActivationSourceFetch    ActivationSource = "fetch"
	ActivationSourceProbe    ActivationSource = "probe"
	ActivationSourceLaneOpen ActivationSource = "lane_open"
)

type Activator interface {
	ActivateByKey(ctx context.Context, key core.ChannelKey, source ActivationSource) (core.Meta, error)
}

func (r *runtime) ensureChannelForIngress(ctx context.Context, key core.ChannelKey, source ActivationSource) (*channel, bool, error) {
	if ch, ok := r.lookupChannel(key); ok {
		return ch, false, nil
	}
	if r == nil || r.cfg.Activator == nil {
		return nil, false, ErrChannelNotFound
	}
	if _, err := r.cfg.Activator.ActivateByKey(ctx, key, source); err != nil {
		return nil, false, err
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return nil, false, ErrChannelNotFound
	}
	return ch, true, nil
}
