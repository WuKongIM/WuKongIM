package node

import (
	"context"
	"testing"
	"time"

	deliverytagruntime "github.com/WuKongIM/WuKongIM/internal/runtime/deliverytag"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestDeliveryTagRPCRejectsNonLeaderUpdate(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 2},
	)
	provider := &recordingDeliveryTagAuthority{}
	adapter := New(Options{
		Cluster:       network.cluster(1),
		ChannelLog:    &stubDeliveryTagChannelLog{status: channel.ChannelRuntimeStatus{Leader: 2}},
		DeliveryTag:   provider,
		Online:        online.NewRegistry(),
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 11,
		LocalNodeID:   1,
	})

	body, err := adapter.handleDeliveryTagRPC(context.Background(), mustEncodeDeliveryTagRequest(t, DeliveryTagRequest{
		Op:          deliveryTagOpUpdate,
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		Tag: deliverytagruntime.DeliveryTag{
			Key:                       "tag-1",
			TagVersion:                1,
			SubscriberMutationVersion: 1,
		},
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryTagResponse(t, body)
	require.Equal(t, rpcStatusNotLeader, resp.Status)
	require.Equal(t, uint64(2), resp.LeaderID)
	require.Empty(t, provider.updateRequests)
}

func TestDeliveryTagRPCReturnsOnlyLocalPartitionForFetch(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	provider := &recordingDeliveryTagAuthority{
		getResponse: DeliveryTagResponse{
			Status: rpcStatusOK,
			Tag: deliverytagruntime.DeliveryTag{
				Key:                       "tag-1",
				ChannelKey:                "group-1",
				TagVersion:                7,
				SubscriberMutationVersion: 9,
				Partitions: []deliverytagruntime.NodePartition{
					{NodeID: 1, UIDs: []string{"u1", "u3"}},
					{NodeID: 2, UIDs: []string{"u2"}},
					{NodeID: 3, UIDs: []string{"u4"}},
				},
			},
		},
	}
	adapter := New(Options{
		Cluster:       network.cluster(1),
		ChannelLog:    &stubDeliveryTagChannelLog{status: channel.ChannelRuntimeStatus{Leader: 1}},
		DeliveryTag:   provider,
		Online:        online.NewRegistry(),
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 11,
		LocalNodeID:   1,
	})

	body, err := adapter.handleDeliveryTagRPC(context.Background(), mustEncodeDeliveryTagRequest(t, DeliveryTagRequest{
		Op:           deliveryTagOpGet,
		ChannelID:    "group-1",
		ChannelType:  frame.ChannelTypeGroup,
		TagKey:       "tag-1",
		TagVersion:   7,
		TargetNodeID: 2,
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryTagResponse(t, body)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, "tag-1", resp.Tag.Key)
	require.Equal(t, uint64(7), resp.Tag.TagVersion)
	require.Len(t, resp.Tag.Partitions, 1)
	require.Equal(t, deliverytagruntime.NodePartition{NodeID: 2, UIDs: []string{"u2"}}, resp.Tag.Partitions[0])
	require.Len(t, provider.getRequests, 1)
	require.Equal(t, uint64(2), provider.getRequests[0].TargetNodeID)
}

func TestDeliveryTagRPCReturnsEmptyLocalPartitionWhenTargetHasNoSubscribers(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	provider := &recordingDeliveryTagAuthority{
		getResponse: DeliveryTagResponse{
			Status: rpcStatusOK,
			Tag: deliverytagruntime.DeliveryTag{
				Key:                       "tag-1",
				ChannelKey:                "group-1",
				TagVersion:                7,
				SubscriberMutationVersion: 9,
				Partitions: []deliverytagruntime.NodePartition{
					{NodeID: 1, UIDs: []string{"u1"}},
				},
			},
		},
	}
	adapter := New(Options{
		Cluster:       network.cluster(1),
		ChannelLog:    &stubDeliveryTagChannelLog{status: channel.ChannelRuntimeStatus{Leader: 1}},
		DeliveryTag:   provider,
		Online:        online.NewRegistry(),
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 11,
		LocalNodeID:   1,
	})

	body, err := adapter.handleDeliveryTagRPC(context.Background(), mustEncodeDeliveryTagRequest(t, DeliveryTagRequest{
		Op:           deliveryTagOpGet,
		ChannelID:    "group-1",
		ChannelType:  frame.ChannelTypeGroup,
		TagKey:       "tag-1",
		TagVersion:   7,
		TargetNodeID: 2,
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryTagResponse(t, body)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, uint64(2), resp.Tag.Partitions[0].NodeID)
	require.Empty(t, resp.Tag.Partitions[0].UIDs)
}

func TestDeliveryTagRPCConvertsMismatchedOKFenceToRetryable(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	provider := &recordingDeliveryTagAuthority{
		getResponse: DeliveryTagResponse{
			Status: rpcStatusOK,
			Tag: deliverytagruntime.DeliveryTag{
				Key:                       "leader-b-1",
				ChannelKey:                "group-1",
				TagVersion:                1,
				SubscriberMutationVersion: 9,
				Partitions:                []deliverytagruntime.NodePartition{{NodeID: 1, UIDs: []string{"u1"}}},
			},
		},
	}
	adapter := New(Options{
		Cluster:       network.cluster(1),
		ChannelLog:    &stubDeliveryTagChannelLog{status: channel.ChannelRuntimeStatus{Leader: 1}},
		DeliveryTag:   provider,
		Online:        online.NewRegistry(),
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 11,
		LocalNodeID:   1,
	})

	body, err := adapter.handleDeliveryTagRPC(context.Background(), mustEncodeDeliveryTagRequest(t, DeliveryTagRequest{
		Op:          deliveryTagOpGet,
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		TagKey:      "leader-a-8",
		TagVersion:  8,
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryTagResponse(t, body)
	require.Equal(t, rpcStatusRetryable, resp.Status)
	require.Equal(t, rpcStatusTagNotCurrent, resp.Result)
	require.Equal(t, "leader-b-1", resp.Tag.Key)
}

func TestDeliveryTagRPCConvertsVersionMismatchOKFenceToRetryable(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	provider := &recordingDeliveryTagAuthority{
		getResponse: DeliveryTagResponse{
			Status: rpcStatusOK,
			Tag: deliverytagruntime.DeliveryTag{
				Key:                       "tag-1",
				ChannelKey:                "group-1",
				TagVersion:                8,
				SubscriberMutationVersion: 9,
				Partitions:                []deliverytagruntime.NodePartition{{NodeID: 1, UIDs: []string{"u1"}}},
			},
		},
	}
	adapter := New(Options{
		Cluster:       network.cluster(1),
		ChannelLog:    &stubDeliveryTagChannelLog{status: channel.ChannelRuntimeStatus{Leader: 1}},
		DeliveryTag:   provider,
		Online:        online.NewRegistry(),
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 11,
		LocalNodeID:   1,
	})

	body, err := adapter.handleDeliveryTagRPC(context.Background(), mustEncodeDeliveryTagRequest(t, DeliveryTagRequest{
		Op:          deliveryTagOpGet,
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		TagKey:      "tag-1",
		TagVersion:  7,
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryTagResponse(t, body)
	require.Equal(t, rpcStatusRetryable, resp.Status)
	require.Equal(t, rpcStatusTagVersionMismatch, resp.Result)
	require.Equal(t, uint64(8), resp.Tag.TagVersion)
}

func TestDeliveryTagBinaryCodecRoundTrip(t *testing.T) {
	now := time.Unix(200, 0)
	req := DeliveryTagRequest{
		Op:           deliveryTagOpGet,
		ChannelID:    "group-1",
		ChannelType:  frame.ChannelTypeGroup,
		TagKey:       "tag-1",
		TagVersion:   7,
		TargetNodeID: 2,
		Tag: deliverytagruntime.DeliveryTag{
			Key:                             "tag-1",
			ChannelKey:                      "group-1",
			TagVersion:                      7,
			SubscriberMutationVersion:       9,
			SourceChannelKey:                "group-0",
			SourceSubscriberMutationVersion: 8,
			Topology: deliverytagruntime.PartitionTopologyVersion{
				HashSlotTableVersion: 4,
				SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
					{SlotID: 1, LeaderNodeID: 2, ConfigEpoch: 7, BalanceVersion: 9},
					{SlotID: 2, LeaderNodeID: 3, ConfigEpoch: 8, BalanceVersion: 10},
				},
			},
			Partitions: []deliverytagruntime.NodePartition{{NodeID: 2, UIDs: []string{"u2"}}},
			CreatedAt:  now,
			LastAccess: now.Add(time.Second),
		},
	}

	reqBody, err := encodeDeliveryTagRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isDeliveryTagRequestBinary(reqBody))

	gotReq, err := decodeDeliveryTagRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := DeliveryTagResponse{
		Status: rpcStatusOK,
		Tag:    req.Tag,
	}
	respBody, err := encodeDeliveryTagResponseBinary(resp)
	require.NoError(t, err)
	require.True(t, isDeliveryTagResponseBinary(respBody))

	gotResp, err := decodeDeliveryTagResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestDeliveryTagClientPreservesRetryableStatuses(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	provider := &recordingDeliveryTagAuthority{
		getResponse: DeliveryTagResponse{
			Status: rpcStatusTagVersionMismatch,
			Result: rpcStatusTagVersionMismatch,
			Tag: deliverytagruntime.DeliveryTag{
				Key:                       "tag-1",
				TagVersion:                8,
				SubscriberMutationVersion: 9,
			},
		},
	}
	adapter := New(Options{
		Cluster:       network.cluster(1),
		ChannelLog:    &stubDeliveryTagChannelLog{status: channel.ChannelRuntimeStatus{Leader: 1}},
		DeliveryTag:   provider,
		Online:        online.NewRegistry(),
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 11,
		LocalNodeID:   1,
	})
	_ = adapter

	client := NewClient(network.cluster(1))
	resp, err := client.GetDeliveryTag(context.Background(), 1, DeliveryTagRequest{
		Op:          deliveryTagOpGet,
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		TagKey:      "tag-1",
		TagVersion:  7,
	})
	require.NoError(t, err)
	require.Equal(t, rpcStatusRetryable, resp.Status)
	require.Equal(t, rpcStatusTagVersionMismatch, resp.Result)
	require.Equal(t, uint64(8), resp.Tag.TagVersion)
}

type recordingDeliveryTagAuthority struct {
	getRequests    []DeliveryTagRequest
	updateRequests []DeliveryTagRequest
	getResponse    DeliveryTagResponse
	updateResponse DeliveryTagResponse
}

func (r *recordingDeliveryTagAuthority) GetDeliveryTag(_ context.Context, req DeliveryTagRequest) (DeliveryTagResponse, error) {
	r.getRequests = append(r.getRequests, req)
	return r.getResponse, nil
}

func (r *recordingDeliveryTagAuthority) UpdateDeliveryTag(_ context.Context, req DeliveryTagRequest) (DeliveryTagResponse, error) {
	r.updateRequests = append(r.updateRequests, req)
	return r.updateResponse, nil
}

type stubDeliveryTagChannelLog struct {
	status channel.ChannelRuntimeStatus
}

func (s *stubDeliveryTagChannelLog) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return s.status, nil
}

func (s *stubDeliveryTagChannelLog) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return channel.FetchResult{}, nil
}

func (s *stubDeliveryTagChannelLog) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return channel.AppendResult{}, nil
}

func (s *stubDeliveryTagChannelLog) AppendBatch(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return channel.AppendBatchResult{}, nil
}

func mustEncodeDeliveryTagRequest(t *testing.T, req DeliveryTagRequest) []byte {
	t.Helper()
	body, err := encodeDeliveryTagRequestBinary(req)
	require.NoError(t, err)
	return body
}

func mustDecodeDeliveryTagResponse(t *testing.T, body []byte) DeliveryTagResponse {
	t.Helper()
	resp, err := decodeDeliveryTagResponse(body)
	require.NoError(t, err)
	return resp
}
