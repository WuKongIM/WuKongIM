package app

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

type managerChannelReplicaStatusReader struct {
	channelLog interface {
		Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	}
}

func (r managerChannelReplicaStatusReader) ChannelRuntimeStatus(_ context.Context, id channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	if r.channelLog == nil {
		return channel.ChannelRuntimeStatus{}, channel.ErrInvalidConfig
	}
	return r.channelLog.Status(id)
}

type managerChannelRuntimeMetaGetter interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

type managerChannelLeaderRepairer interface {
	RepairIfNeeded(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error)
}

type managerChannelLeaderRepairOperator struct {
	metas    managerChannelRuntimeMetaGetter
	repairer managerChannelLeaderRepairer
}

func (o managerChannelLeaderRepairOperator) RepairChannelLeader(ctx context.Context, req managementusecase.RepairChannelClusterLeaderRequest) (managementusecase.RepairChannelClusterLeaderResult, error) {
	if o.metas == nil || o.repairer == nil {
		return managementusecase.RepairChannelClusterLeaderResult{}, channel.ErrInvalidConfig
	}
	meta, err := o.metas.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return managementusecase.RepairChannelClusterLeaderResult{}, err
	}
	repaired, changed, err := o.repairer.RepairIfNeeded(ctx, meta, req.Reason)
	if err != nil {
		return managementusecase.RepairChannelClusterLeaderResult{}, err
	}
	return managementusecase.RepairChannelClusterLeaderResult{Changed: changed, Meta: repaired}, nil
}
