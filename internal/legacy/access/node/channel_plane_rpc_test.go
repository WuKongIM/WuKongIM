package node

import (
	"context"
	"testing"

	runtimechannelplane "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/channelplane"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestAppendBatchesRPCValidatesRouteEpochBeforeAppend(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	log := &stubNodeChannelLog{
		meta: channel.Meta{
			Key:             "g1",
			ID:              id,
			Leader:          1,
			RouteGeneration: 5,
			Epoch:           7,
			LeaderEpoch:     9,
			Status:          channel.StatusActive,
		},
	}
	adapter := New(Options{ChannelLog: log, LocalNodeID: 1})
	body := mustEncodeChannelPlaneAppendBatchesRequest(t, runtimechannelplane.AppendBatchesRequest{Batches: []runtimechannelplane.AppendBatchEnvelope{{
		RouteEpoch: runtimechannelplane.RouteEpoch{RouteGeneration: 4, ChannelEpoch: 7, LeaderEpoch: 9},
		Request:    channel.AppendBatchRequest{ChannelID: id, Messages: []channel.Message{{ClientMsgNo: "m1"}}},
	}}})

	respBody, err := adapter.handleChannelPlaneAppendBatchesRPC(context.Background(), body)

	require.NoError(t, err)
	resp := mustDecodeChannelPlaneAppendBatchesResponse(t, respBody)
	require.Len(t, resp.Results, 1)
	require.Equal(t, runtimechannelplane.RemoteAppendStatusStaleRoute, resp.Results[0].Status)
	require.Empty(t, log.appendBatchCalls)
}

func TestAppendBatchesRPCDoesNotRefreshOrRedirect(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	log := &stubNodeChannelLog{
		meta: channel.Meta{
			Key:             "g1",
			ID:              id,
			Leader:          2,
			RouteGeneration: 5,
			Epoch:           7,
			LeaderEpoch:     9,
			Status:          channel.StatusActive,
		},
	}
	refresher := &recordingNodeMetaRefresher{}
	adapter := New(Options{ChannelLog: log, ChannelMeta: refresher, LocalNodeID: 1})
	body := mustEncodeChannelPlaneAppendBatchesRequest(t, runtimechannelplane.AppendBatchesRequest{Batches: []runtimechannelplane.AppendBatchEnvelope{{
		RouteEpoch: runtimechannelplane.RouteEpoch{RouteGeneration: 5, ChannelEpoch: 7, LeaderEpoch: 9},
		Request:    channel.AppendBatchRequest{ChannelID: id, Messages: []channel.Message{{ClientMsgNo: "m1"}}},
	}}})

	respBody, err := adapter.handleChannelPlaneAppendBatchesRPC(context.Background(), body)

	require.NoError(t, err)
	resp := mustDecodeChannelPlaneAppendBatchesResponse(t, respBody)
	require.Len(t, resp.Results, 1)
	require.Equal(t, runtimechannelplane.RemoteAppendStatusNotLeader, resp.Results[0].Status)
	require.Equal(t, channel.NodeID(2), resp.Results[0].Leader)
	require.Empty(t, log.appendBatchCalls)
	require.Zero(t, refresher.calls)
}

func mustEncodeChannelPlaneAppendBatchesRequest(t *testing.T, req runtimechannelplane.AppendBatchesRequest) []byte {
	t.Helper()
	body, err := encodeChannelPlaneAppendBatchesRequest(req)
	require.NoError(t, err)
	return body
}

func mustDecodeChannelPlaneAppendBatchesResponse(t *testing.T, body []byte) runtimechannelplane.AppendBatchesResponse {
	t.Helper()
	resp, err := decodeChannelPlaneAppendBatchesResponse(body)
	require.NoError(t, err)
	return resp
}

type recordingNodeMetaRefresher struct {
	calls int
}

func (r *recordingNodeMetaRefresher) RefreshChannelMeta(context.Context, channel.ChannelID) (channel.Meta, error) {
	r.calls++
	return channel.Meta{}, nil
}
