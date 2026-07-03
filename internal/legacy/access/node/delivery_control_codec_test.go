package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/deliveryevents"
	"github.com/stretchr/testify/require"
)

func TestDeliveryControlBinaryCodecRoundTrip(t *testing.T) {
	ackReq := deliveryAckRequest{Command: deliveryevents.RouteAck{
		UID:        "u2",
		SessionID:  10,
		MessageID:  88,
		MessageSeq: 9,
	}}
	ackBody, err := encodeDeliveryAckRequestBinary(ackReq)
	require.NoError(t, err)
	require.True(t, isDeliveryAckRequestBinary(ackBody))

	gotAckReq, err := decodeDeliveryAckRequest(ackBody)
	require.NoError(t, err)
	require.Equal(t, ackReq, gotAckReq)

	offlineReq := deliveryOfflineRequest{Command: deliveryevents.SessionClosed{
		UID:       "u2",
		SessionID: 10,
	}}
	offlineBody, err := encodeDeliveryOfflineRequestBinary(offlineReq)
	require.NoError(t, err)
	require.True(t, isDeliveryOfflineRequestBinary(offlineBody))

	gotOfflineReq, err := decodeDeliveryOfflineRequest(offlineBody)
	require.NoError(t, err)
	require.Equal(t, offlineReq, gotOfflineReq)

	resp := deliveryResponse{Status: rpcStatusOK}
	respBody, err := encodeDeliveryResponse(resp)
	require.NoError(t, err)
	require.True(t, isDeliveryResponseBinary(respBody))

	gotResp, err := decodeDeliveryResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestDeliveryAckBatchBinaryCodecRoundTrip(t *testing.T) {
	req := deliveryAckRequest{Commands: []deliveryevents.RouteAck{
		{UID: "u1", SessionID: 10, MessageID: 88, MessageSeq: 9},
		{UID: "u2", SessionID: 11, MessageID: 89, MessageSeq: 10},
	}}

	body, err := encodeDeliveryAckRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isDeliveryAckRequestBinary(body))

	got, err := decodeDeliveryAckRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestDeliveryControlRPCsRejectJSONPayload(t *testing.T) {
	ackRecorder := &recordingDeliveryAck{}
	offlineRecorder := &recordingDeliveryOffline{}
	adapter := New(Options{
		DeliveryAck:     ackRecorder,
		DeliveryOffline: offlineRecorder,
	})

	_, err := adapter.handleDeliveryAckRPC(context.Background(), []byte(`{"command":{"UID":"u2","SessionID":10,"MessageID":88,"MessageSeq":9}}`))
	require.Error(t, err)
	require.Empty(t, ackRecorder.calls)

	_, err = adapter.handleDeliveryOfflineRPC(context.Background(), []byte(`{"command":{"UID":"u2","SessionID":10}}`))
	require.Error(t, err)
	require.Empty(t, offlineRecorder.calls)
}
