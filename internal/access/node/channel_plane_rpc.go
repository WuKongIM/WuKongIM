package node

import (
	"context"
	"errors"
	"fmt"

	runtimechannelplane "github.com/WuKongIM/WuKongIM/internal/runtime/channelplane"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type channelPlaneLocalOwner interface {
	AppendLocalBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
	MetaSnapshot(key channel.ChannelKey) (channel.Meta, bool)
}

func (a *Adapter) handleChannelPlaneAppendBatchesRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeChannelPlaneAppendBatchesRequest(body)
	if err != nil {
		return nil, err
	}
	results := make([]runtimechannelplane.AppendBatchRemoteResult, len(req.Batches))
	for i, batch := range req.Batches {
		results[i] = a.handleChannelPlaneAppendEnvelope(ctx, batch)
	}
	return encodeChannelPlaneAppendBatchesResponse(runtimechannelplane.AppendBatchesResponse{Results: results})
}

func (a *Adapter) handleChannelPlaneAppendEnvelope(ctx context.Context, env runtimechannelplane.AppendBatchEnvelope) runtimechannelplane.AppendBatchRemoteResult {
	owner, ok := a.channelLog.(channelPlaneLocalOwner)
	if !ok || owner == nil || a.localNodeID == 0 {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusInvalid}
	}
	if env.Request.ChannelID.ID == "" || env.Request.ChannelID.Type == 0 || len(env.Request.Messages) == 0 {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusInvalid}
	}
	key := channelhandler.KeyFromChannelID(env.Request.ChannelID)
	meta, ok := owner.MetaSnapshot(key)
	if !ok {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusNotReady}
	}
	if meta.Status != channel.StatusActive {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusNotReady, Leader: meta.Leader}
	}
	if meta.Leader == 0 {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusNotReady, Leader: meta.Leader}
	}
	if uint64(meta.Leader) != a.localNodeID {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusNotLeader, Leader: meta.Leader}
	}
	if meta.RouteGeneration != env.RouteEpoch.RouteGeneration || meta.Epoch != env.RouteEpoch.ChannelEpoch || meta.LeaderEpoch != env.RouteEpoch.LeaderEpoch {
		return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusStaleRoute, Leader: meta.Leader}
	}
	result, err := owner.AppendLocalBatch(ctx, env.Request)
	if err != nil {
		return runtimechannelplane.AppendBatchRemoteResult{Status: channelPlaneAppendStatusFromError(err), Leader: meta.Leader}
	}
	return runtimechannelplane.AppendBatchRemoteResult{Status: runtimechannelplane.RemoteAppendStatusOK, Result: result}
}

func channelPlaneAppendStatusFromError(err error) string {
	switch {
	case err == nil:
		return runtimechannelplane.RemoteAppendStatusOK
	case errors.Is(err, channel.ErrNotLeader):
		return runtimechannelplane.RemoteAppendStatusNotLeader
	case errors.Is(err, channel.ErrStaleMeta):
		return runtimechannelplane.RemoteAppendStatusStaleRoute
	case errors.Is(err, channel.ErrLeaseExpired):
		return runtimechannelplane.RemoteAppendStatusLeaseExpired
	case errors.Is(err, channel.ErrWriteFenced):
		return runtimechannelplane.RemoteAppendStatusWriteFenced
	case errors.Is(err, channel.ErrNotReady):
		return runtimechannelplane.RemoteAppendStatusNotReady
	case errors.Is(err, runtimechannelplane.ErrOverloaded), errors.Is(err, runtimechannelplane.ErrPeerBackpressured):
		return runtimechannelplane.RemoteAppendStatusBackpressure
	case errors.Is(err, channel.ErrInvalidArgument), errors.Is(err, runtimechannelplane.ErrInvalidRequest):
		return runtimechannelplane.RemoteAppendStatusInvalid
	default:
		return runtimechannelplane.RemoteAppendStatusInvalid
	}
}

func (c *Client) AppendBatches(ctx context.Context, nodeID channel.NodeID, req runtimechannelplane.AppendBatchesRequest) (runtimechannelplane.AppendBatchesResponse, error) {
	if c == nil || c.cluster == nil {
		return runtimechannelplane.AppendBatchesResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return runtimechannelplane.AppendBatchesResponse{}, channel.ErrNotLeader
	}
	body, err := encodeChannelPlaneAppendBatchesRequest(req)
	if err != nil {
		return runtimechannelplane.AppendBatchesResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, channelPlaneAppendRPCServiceID, body)
	if err != nil {
		return runtimechannelplane.AppendBatchesResponse{}, err
	}
	return decodeChannelPlaneAppendBatchesResponse(respBody)
}
