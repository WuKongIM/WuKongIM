package message

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func sendBatchWithEnsuredMeta(
	ctx context.Context,
	localNodeID uint64,
	now func() time.Time,
	sendLogger wklog.Logger,
	cluster ChannelCluster,
	remote RemoteAppender,
	refresher MetaRefresher,
	metrics messageAppendMetrics,
	req channel.AppendBatchRequest,
) (channel.AppendBatchResult, error) {
	logFields := func(event string, extra ...wklog.Field) []wklog.Field {
		fields := append([]wklog.Field{wklog.Event(event)}, messageLogFields(req.ChannelID, appendBatchFromUID(req))...)
		return append(fields, extra...)
	}
	if refresher == nil {
		sendLogger.Error("meta refresher required", logFields("message.send.refresher.required")...)
		return channel.AppendBatchResult{}, ErrMetaRefresherRequired
	}
	refreshMeta := func() (channel.Meta, error) {
		meta, err := refresher.RefreshChannelMeta(ctx, req.ChannelID)
		if err != nil {
			sendLogger.Error("refresh channel metadata failed", logFields("message.send.refresh.failed", wklog.Error(err))...)
			return channel.Meta{}, err
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
		return meta, nil
	}
	appendWithMeta := func(ctx context.Context, meta channel.Meta, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
		req.ExpectedChannelEpoch = meta.Epoch
		req.ExpectedLeaderEpoch = meta.LeaderEpoch
		if meta.Leader != 0 && uint64(meta.Leader) != localNodeID {
			startedAt := time.Now()
			path := "remote"
			if remote == nil {
				sendLogger.Error("remote appender required for forwarding", logFields("message.send.remote.required",
					wklog.LeaderNodeID(uint64(meta.Leader)))...)
				observeMessageAppend(metrics, path, "error", time.Since(startedAt))
				return channel.AppendBatchResult{}, ErrRemoteAppenderRequired
			}
			result, err := appendBatchRemote(ctx, remote, uint64(meta.Leader), req)
			if err != nil {
				sendLogger.Error("forward append batch to leader failed", logFields("message.send.forward_batch.failed",
					wklog.LeaderNodeID(uint64(meta.Leader)), wklog.Error(err))...)
			}
			observeMessageAppend(metrics, path, appendMetricResult(err), time.Since(startedAt))
			return result, err
		}
		startedAt := time.Now()
		result, err := appendBatchLocal(ctx, cluster, req)
		if err != nil {
			sendLogger.Error("local append batch failed", logFields("message.send.append_batch.failed", wklog.Error(err))...)
		}
		observeMessageAppend(metrics, "local", appendMetricResult(err), time.Since(startedAt))
		return result, err
	}

	meta, err := refreshMeta()
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	result, err := appendWithMeta(ctx, meta, req)
	if err == nil || !isRetryableMetaAppendError(err) {
		return result, err
	}
	if invalidator, ok := refresher.(MetaInvalidator); ok {
		invalidator.InvalidateChannelMeta(req.ChannelID)
	}
	meta, refreshErr := refreshMeta()
	if refreshErr != nil {
		return channel.AppendBatchResult{}, refreshErr
	}
	return appendWithMeta(ctx, meta, req)
}

func appendBatchLocal(ctx context.Context, cluster ChannelCluster, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return cluster.AppendBatch(ctx, req)
}

func appendBatchRemote(ctx context.Context, remote RemoteAppender, nodeID uint64, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	if remote == nil {
		return channel.AppendBatchResult{}, ErrRemoteAppenderRequired
	}
	return remote.AppendBatchToLeader(ctx, nodeID, req)
}

func appendBatchFromUID(req channel.AppendBatchRequest) string {
	if len(req.Messages) == 0 {
		return ""
	}
	return req.Messages[0].FromUID
}

type messageAppendMetrics interface {
	ObserveAppend(path, result string, dur time.Duration)
}

func observeMessageAppend(metrics messageAppendMetrics, path, result string, dur time.Duration) {
	if metrics == nil {
		return
	}
	metrics.ObserveAppend(path, result, dur)
}

func appendMetricResult(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func isRetryableMetaAppendError(err error) bool {
	return errors.Is(err, channel.ErrStaleMeta) ||
		errors.Is(err, channel.ErrNotLeader) ||
		errors.Is(err, channel.ErrLeaseExpired) ||
		errors.Is(err, channel.ErrWriteFenced) ||
		errors.Is(err, raftcluster.ErrNotLeader) ||
		errors.Is(err, raftcluster.ErrRerouted)
}
