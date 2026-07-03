package node

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestChannelRetentionBinaryCodecRoundTrip(t *testing.T) {
	req := channelRetentionRequest{Request: ChannelRetentionAdvanceRequest{
		ChannelID:  channel.ChannelID{ID: "room-1", Type: 2},
		ThroughSeq: 10,
		DryRun:     true,
	}}

	body, err := encodeChannelRetentionRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelRetentionRequestBinary(body))
	gotReq, err := decodeChannelRetentionRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelRetentionResponse{
		Status:   rpcStatusOK,
		LeaderID: 3,
		Result: ChannelRetentionAdvanceResult{
			ChannelID:           channel.ChannelID{ID: "room-1", Type: 2},
			RequestedThroughSeq: 10,
			AdvancedThroughSeq:  8,
			MinAvailableSeq:     9,
			Status:              "advanced",
			BlockedReason:       "",
		},
	}
	respBody, err := encodeChannelRetentionResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelRetentionResponseBinary(respBody))
	gotResp, err := decodeChannelRetentionResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelRetentionRPCDelegatesOnLocalLeader(t *testing.T) {
	id := channel.ChannelID{ID: "room-1", Type: 2}
	provider := &fakeChannelRetentionProvider{result: ChannelRetentionAdvanceResult{
		ChannelID:           id,
		RequestedThroughSeq: 10,
		AdvancedThroughSeq:  8,
		MinAvailableSeq:     9,
		Status:              "advanced",
	}}
	adapter := New(Options{
		LocalNodeID:      2,
		ChannelRetention: provider,
		ChannelMeta: &stubNodeMetaRefresher{meta: channel.Meta{
			ID:     id,
			Leader: 2,
		}},
	})

	body := mustEncodeChannelRetentionRequest(t, channelRetentionRequest{Request: ChannelRetentionAdvanceRequest{
		ChannelID:  id,
		ThroughSeq: 10,
	}})
	respBody, err := adapter.handleChannelRetentionRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeChannelRetentionResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, provider.result, resp.Result)
	require.Equal(t, ChannelRetentionAdvanceRequest{ChannelID: id, ThroughSeq: 10}, provider.lastReq)
}

func TestChannelRetentionRPCReturnsNotLeader(t *testing.T) {
	id := channel.ChannelID{ID: "room-1", Type: 2}
	provider := &fakeChannelRetentionProvider{}
	adapter := New(Options{
		LocalNodeID:      2,
		ChannelRetention: provider,
		ChannelMeta: &stubNodeMetaRefresher{meta: channel.Meta{
			ID:     id,
			Leader: 3,
		}},
	})

	body := mustEncodeChannelRetentionRequest(t, channelRetentionRequest{Request: ChannelRetentionAdvanceRequest{
		ChannelID:  id,
		ThroughSeq: 10,
	}})
	respBody, err := adapter.handleChannelRetentionRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeChannelRetentionResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusNotLeader, resp.Status)
	require.Equal(t, uint64(3), resp.LeaderID)
	require.Zero(t, provider.calls)
}

func TestChannelRetentionClientRetriesLeaderHint(t *testing.T) {
	id := channel.ChannelID{ID: "room-1", Type: 2}
	cluster := &fakeChannelRetentionCluster{responses: map[uint64]channelRetentionResponse{
		2: {Status: rpcStatusNotLeader, LeaderID: 3},
		3: {Status: rpcStatusOK, Result: ChannelRetentionAdvanceResult{ChannelID: id, RequestedThroughSeq: 10, AdvancedThroughSeq: 10, MinAvailableSeq: 11, Status: "advanced"}},
	}}
	client := NewClient(cluster)

	got, err := client.AdvanceChannelRetention(context.Background(), 2, ChannelRetentionAdvanceRequest{ChannelID: id, ThroughSeq: 10})

	require.NoError(t, err)
	require.Equal(t, []uint64{2, 3}, cluster.calls)
	require.Equal(t, uint64(10), got.AdvancedThroughSeq)
}

type fakeChannelRetentionProvider struct {
	lastReq ChannelRetentionAdvanceRequest
	result  ChannelRetentionAdvanceResult
	calls   int
}

func (f *fakeChannelRetentionProvider) AdvanceChannelRetention(_ context.Context, req ChannelRetentionAdvanceRequest) (ChannelRetentionAdvanceResult, error) {
	f.calls++
	f.lastReq = req
	return f.result, nil
}

type fakeChannelRetentionCluster struct {
	responses map[uint64]channelRetentionResponse
	calls     []uint64
}

func (f *fakeChannelRetentionCluster) RPCMux() *transport.RPCMux { return nil }
func (f *fakeChannelRetentionCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, raftcluster.ErrNoLeader
}
func (f *fakeChannelRetentionCluster) IsLocal(multiraft.NodeID) bool                    { return false }
func (f *fakeChannelRetentionCluster) SlotForKey(string) multiraft.SlotID               { return 0 }
func (f *fakeChannelRetentionCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID { return nil }
func (f *fakeChannelRetentionCluster) RPCService(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, _ []byte) ([]byte, error) {
	f.calls = append(f.calls, uint64(nodeID))
	if serviceID != channelRetentionRPCServiceID {
		return nil, fmt.Errorf("unexpected service id %d", serviceID)
	}
	resp := f.responses[uint64(nodeID)]
	return encodeChannelRetentionResponse(resp)
}

func mustEncodeChannelRetentionRequest(t *testing.T, req channelRetentionRequest) []byte {
	t.Helper()
	body, err := encodeChannelRetentionRequestBinary(req)
	require.NoError(t, err)
	return body
}
