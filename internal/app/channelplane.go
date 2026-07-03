package app

import (
	"context"

	runtimechannelplane "github.com/WuKongIM/WuKongIM/internal/runtime/channelplane"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

// appChannelPlaneRouteResolver adapts channelmeta refreshes to channelplane route lookups.
type appChannelPlaneRouteResolver struct {
	meta *channelMetaSync
}

func (r appChannelPlaneRouteResolver) ResolveRoute(ctx context.Context, id channel.ChannelID) (runtimechannelplane.ChannelRoute, error) {
	if r.meta == nil {
		return runtimechannelplane.ChannelRoute{}, channel.ErrInvalidConfig
	}
	meta, err := r.meta.RefreshChannelMeta(ctx, id)
	if err != nil {
		return runtimechannelplane.ChannelRoute{}, err
	}
	return appChannelPlaneRouteFromMeta(meta), nil
}

func (r appChannelPlaneRouteResolver) InvalidateRoute(id channel.ChannelID, _ uint64) {
	if r.meta != nil {
		r.meta.InvalidateChannelMeta(id)
	}
}

func appChannelPlaneRouteFromMeta(meta channel.Meta) runtimechannelplane.ChannelRoute {
	return runtimechannelplane.ChannelRoute{
		ChannelID:       meta.ID,
		Leader:          meta.Leader,
		RouteGeneration: meta.RouteGeneration,
		ChannelEpoch:    meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		Replicas:        append([]channel.NodeID(nil), meta.Replicas...),
		ISR:             append([]channel.NodeID(nil), meta.ISR...),
		LeaseUntil:      meta.LeaseUntil,
		Status:          meta.Status,
	}
}
