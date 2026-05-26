package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderRepairBinaryCodecRoundTrip(t *testing.T) {
	req := ChannelLeaderRepairRequest{
		ChannelID:            channel.ChannelID{ID: "repair-binary", Type: 2},
		ObservedChannelEpoch: 11,
		ObservedLeaderEpoch:  10,
		Reason:               "leader_dead",
	}

	reqBody, err := encodeChannelLeaderRepairRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelLeaderRepairRequestBinary(reqBody))

	gotReq, err := decodeChannelLeaderRepairRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelLeaderRepairResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Result: &ChannelLeaderRepairResult{
			Meta:    testChannelRuntimeMeta("repair-binary"),
			Changed: true,
		},
	}
	respBody, err := encodeChannelLeaderRepairResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelLeaderRepairResponseBinary(respBody))

	gotResp, err := decodeChannelLeaderRepairResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelLeaderEvaluateBinaryCodecRoundTrip(t *testing.T) {
	req := ChannelLeaderEvaluateRequest{Meta: testChannelRuntimeMeta("evaluate-binary")}

	reqBody, err := encodeChannelLeaderEvaluateRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelLeaderEvaluateRequestBinary(reqBody))

	gotReq, err := decodeChannelLeaderEvaluateRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelLeaderEvaluateResponse{
		Status: rpcStatusOK,
		Report: &ChannelLeaderPromotionReport{
			NodeID:              2,
			Exists:              true,
			ChannelEpoch:        11,
			LocalLEO:            12,
			LocalCheckpointHW:   10,
			LocalOffsetEpoch:    9,
			CommitReadyNow:      true,
			ProjectedSafeHW:     10,
			ProjectedTruncateTo: 9,
			CanLead:             true,
			Reason:              "safe",
		},
	}
	respBody, err := encodeChannelLeaderEvaluateResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelLeaderEvaluateResponseBinary(respBody))

	gotResp, err := decodeChannelLeaderEvaluateResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelLeaderTransferBinaryCodecRoundTrip(t *testing.T) {
	req := ChannelLeaderTransferRequest{
		ChannelID:            channel.ChannelID{ID: "transfer-binary", Type: 2},
		ObservedChannelEpoch: 11,
		ObservedLeaderEpoch:  10,
		TargetNodeID:         3,
	}

	reqBody, err := encodeChannelLeaderTransferRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelLeaderTransferRequestBinary(reqBody))

	gotReq, err := decodeChannelLeaderTransferRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelLeaderTransferResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Result: &ChannelLeaderTransferResult{
			Meta:    testChannelRuntimeMeta("transfer-binary"),
			Changed: true,
		},
	}
	respBody, err := encodeChannelLeaderTransferResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelLeaderTransferResponseBinary(respBody))

	gotResp, err := decodeChannelLeaderTransferResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelLeaderRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{
		Presence:              presence.New(presence.Options{}),
		Online:                online.NewRegistry(),
		LocalNodeID:           2,
		ChannelLeaderRepair:   &stubNodeChannelLeaderRepairer{},
		ChannelLeaderTransfer: &stubNodeChannelLeaderTransferer{},
		ChannelLeaderEvaluate: &stubNodeChannelLeaderEvaluator{},
	})

	repairBody := []byte(`{"channel_id":{"id":"repair-json","type":2}}`)
	_, err := adapter.handleChannelLeaderRepairRPC(context.Background(), repairBody)
	require.Error(t, err)

	evaluateBody := []byte(`{"meta":{"channel_id":"evaluate-json"}}`)
	_, err = adapter.handleChannelLeaderEvaluateRPC(context.Background(), evaluateBody)
	require.Error(t, err)

	transferBody := []byte(`{"channel_id":{"id":"transfer-json","type":2},"target_node_id":2}`)
	_, err = adapter.handleChannelLeaderTransferRPC(context.Background(), transferBody)
	require.Error(t, err)
}

func testChannelRuntimeMeta(channelID string) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:            channelID,
		ChannelType:          2,
		ChannelEpoch:         11,
		LeaderEpoch:          10,
		Replicas:             []uint64{1, 2, 3},
		ISR:                  []uint64{2, 3},
		Leader:               2,
		MinISR:               2,
		Status:               uint8(channel.StatusActive),
		Features:             7,
		LeaseUntilMS:         1777777777000,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 1777777776000,
	}
}
