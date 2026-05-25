package app

import (
	"context"
	"errors"

	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

type managerChannelLeaderTransferer interface {
	TransferIfSafe(ctx context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error)
}

type managerChannelLeaderTransferOperator struct {
	metas      managerChannelRuntimeMetaGetter
	transferer managerChannelLeaderTransferer
}

func (o managerChannelLeaderTransferOperator) TransferChannelLeader(ctx context.Context, req managementusecase.TransferChannelClusterLeaderRequest) (managementusecase.TransferChannelClusterLeaderResult, error) {
	if o.metas == nil || o.transferer == nil {
		return managementusecase.TransferChannelClusterLeaderResult{}, channel.ErrInvalidConfig
	}
	meta, err := o.metas.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return managementusecase.TransferChannelClusterLeaderResult{}, err
	}
	transferred, changed, err := o.transferer.TransferIfSafe(ctx, meta, req.TargetNodeID)
	if err != nil {
		return managementusecase.TransferChannelClusterLeaderResult{}, mapChannelLeaderTransferError(err)
	}
	return managementusecase.TransferChannelClusterLeaderResult{Changed: changed, Meta: transferred}, nil
}

func mapChannelLeaderTransferError(err error) error {
	switch {
	case errors.Is(err, channelmeta.ErrLeaderTransferTargetNotReplica):
		return managementusecase.ErrChannelLeaderTransferTargetNotReplica
	case errors.Is(err, channelmeta.ErrLeaderTransferTargetNotISR):
		return managementusecase.ErrChannelLeaderTransferTargetNotISR
	case errors.Is(err, channelmeta.ErrLeaderTransferInactiveChannel):
		return managementusecase.ErrChannelLeaderTransferInactiveChannel
	default:
		return err
	}
}
