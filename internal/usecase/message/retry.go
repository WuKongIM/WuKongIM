package message

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func sendWithEnsuredMeta(
	ctx context.Context,
	localNodeID uint64,
	now func() time.Time,
	sendLogger wklog.Logger,
	cluster ChannelCluster,
	remote RemoteAppender,
	refresher MetaRefresher,
	req channel.AppendRequest,
) (channel.AppendResult, error) {
	logFields := func(event string, extra ...wklog.Field) []wklog.Field {
		fields := append([]wklog.Field{wklog.Event(event)}, messageLogFields(req.ChannelID, req.Message.FromUID)...)
		return append(fields, extra...)
	}

	if refresher == nil {
		sendLogger.Error("meta refresher required", logFields("message.send.refresher.required")...)
		return channel.AppendResult{}, ErrMetaRefresherRequired
	}

	// 1. 激活 channel runtime，获取最新 meta
	meta, err := refresher.RefreshChannelMeta(ctx, req.ChannelID)
	if err != nil {
		sendLogger.Error("refresh channel metadata failed", logFields("message.send.refresh.failed", wklog.Error(err))...)
		return channel.AppendResult{}, err
	}

	metaFields := logFields("message.send.meta.resolved",
		wklog.LeaderNodeID(uint64(meta.Leader)),
		wklog.Uint64("channelEpoch", meta.Epoch),
		wklog.Uint64("leaderEpoch", meta.LeaderEpoch),
		wklog.Int("replicaCount", len(meta.Replicas)),
	)
	if now != nil && !meta.LeaseUntil.IsZero() {
		metaFields = append(metaFields, wklog.Duration("leaseRemaining", meta.LeaseUntil.Sub(now())))
	}
	sendLogger.Debug("resolved channel metadata", metaFields...)

	req.ExpectedChannelEpoch = meta.Epoch
	req.ExpectedLeaderEpoch = meta.LeaderEpoch

	// 2. leader 在远端，直接转发
	if meta.Leader != 0 && uint64(meta.Leader) != localNodeID {
		if remote == nil {
			sendLogger.Error("remote appender required for forwarding", logFields("message.send.remote.required",
				wklog.LeaderNodeID(uint64(meta.Leader)))...)
			return channel.AppendResult{}, ErrRemoteAppenderRequired
		}
		result, err := remote.AppendToLeader(ctx, uint64(meta.Leader), req)
		if err != nil {
			sendLogger.Error("forward append to leader failed", logFields("message.send.forward.failed",
				wklog.LeaderNodeID(uint64(meta.Leader)), wklog.Error(err))...)
		}
		return result, err
	}

	// 3. leader 在本地，本地 append
	result, err := cluster.Append(ctx, req)
	if err != nil {
		sendLogger.Error("local append failed", logFields("message.send.append.failed", wklog.Error(err))...)
	}
	return result, err
}
