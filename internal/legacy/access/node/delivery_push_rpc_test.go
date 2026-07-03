package node

import (
	"context"
	"errors"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestPushBatchRPCProcessesBatchItemsAndAggregatesResults(t *testing.T) {
	reg := online.NewRegistry()
	active := newRecordingSession(10, "tcp")
	failing := &writeErrorSession{recordingSession: *newRecordingSession(11, "tcp"), err: errors.New("write failed")}
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   10,
		UID:         "u2",
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     active,
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u3",
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     failing,
	}))

	ackIndex := deliveryruntime.NewAckIndex()
	adapter := New(Options{
		Presence:         presence.New(presence.Options{}),
		GatewayBootID:    7,
		LocalNodeID:      1,
		Online:           reg,
		DeliveryAckIndex: ackIndex,
	})

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustEncodeDeliveryPushBatchCommand(t, DeliveryPushBatchCommand{
		OwnerNodeID: 2,
		Items: []DeliveryPushItem{
			{
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				MessageID:   88,
				MessageSeq:  9,
				Routes: []deliveryruntime.RouteKey{
					{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10},
					{UID: "u-mismatch", NodeID: 99, BootID: 7, SessionID: 12},
				},
				Frame: mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
			},
			{
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				MessageID:   89,
				MessageSeq:  10,
				Routes: []deliveryruntime.RouteKey{
					{UID: "u3", NodeID: 1, BootID: 7, SessionID: 11},
				},
				Frame: mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 89, MessageSeq: 10}),
			},
		},
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryPushResponse(t, body)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.True(t, resp.AcceptedCountSet)
	require.Equal(t, uint64(1), resp.AcceptedCount)
	require.Empty(t, resp.Accepted)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u3", NodeID: 1, BootID: 7, SessionID: 11}}, resp.Retryable)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u-mismatch", NodeID: 99, BootID: 7, SessionID: 12}}, resp.Dropped)
	require.Len(t, active.WrittenFrames(), 1)

	binding, ok := ackIndex.Lookup(10, 88)
	require.True(t, ok)
	require.Equal(t, uint64(2), binding.OwnerNodeID)
	require.Equal(t, "g1", binding.ChannelID)
	_, ok = ackIndex.Lookup(11, 89)
	require.False(t, ok)
}

func TestDeliveryPushBinaryCodecRoundTrip(t *testing.T) {
	cmd := DeliveryPushBatchCommand{
		OwnerNodeID: 2,
		Items: []DeliveryPushItem{{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   88,
			MessageSeq:  9,
			Routes: []deliveryruntime.RouteKey{
				{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10},
				{UID: "u3", NodeID: 1, BootID: 7, SessionID: 11},
			},
			Frame: mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
		}},
	}

	body, err := encodeDeliveryPushBatchCommandBinary(cmd)
	require.NoError(t, err)
	require.True(t, isDeliveryPushRequestBinary(body))

	req, binaryRequest, err := decodeDeliveryPushRequest(body)
	require.NoError(t, err)
	require.True(t, binaryRequest)
	require.True(t, req.acceptsAcceptedCount)
	require.Equal(t, cmd.OwnerNodeID, req.OwnerNodeID)
	require.Equal(t, cmd.Items, req.Items)

	respBody, err := encodeDeliveryPushResponseBinary(DeliveryPushResponse{
		Status:           rpcStatusOK,
		AcceptedCount:    1,
		AcceptedCountSet: true,
		Dropped:          []deliveryruntime.RouteKey{{UID: "u3", NodeID: 1, BootID: 7, SessionID: 11}},
	})
	require.NoError(t, err)
	require.True(t, isDeliveryPushResponseBinary(respBody))

	resp, err := decodeDeliveryPushResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.True(t, resp.AcceptedCountSet)
	require.Equal(t, uint64(1), resp.AcceptedCount)
	require.Empty(t, resp.Accepted)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u3", NodeID: 1, BootID: 7, SessionID: 11}}, resp.Dropped)
}

func TestDeliveryPushBinaryCodecRejectsOverflowCounts(t *testing.T) {
	reqBody := append([]byte{}, deliveryPushRequestMagic[:]...)
	reqBody = appendUvarint(reqBody, 1)
	reqBody = appendUvarint(reqBody, uint64(1)<<63)

	var reqErr error
	require.NotPanics(t, func() {
		_, _, reqErr = decodeDeliveryPushRequest(reqBody)
	})
	require.Error(t, reqErr)

	respBody := append([]byte{}, deliveryPushResponseMagic[:]...)
	respBody = appendString(respBody, rpcStatusOK)
	respBody = appendUvarint(respBody, uint64(1)<<63)

	var respErr error
	require.NotPanics(t, func() {
		_, respErr = decodeDeliveryPushResponse(respBody)
	})
	require.Error(t, respErr)
}

func TestPushBatchRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 7,
		LocalNodeID:   1,
		Online:        online.NewRegistry(),
	})

	_, err := adapter.handleDeliveryPushRPC(context.Background(), []byte(`{"owner_node_id":2}`))
	require.Error(t, err)
}

func TestPushBatchRPCAcceptsBinaryRequestAndReturnsBinaryResponse(t *testing.T) {
	reg := online.NewRegistry()
	active := newRecordingSession(10, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   10,
		UID:         "u2",
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     active,
	}))

	adapter := New(Options{
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 7,
		LocalNodeID:   1,
		Online:        reg,
	})
	reqBody, err := encodeDeliveryPushBatchCommandBinary(DeliveryPushBatchCommand{
		OwnerNodeID: 2,
		Items: []DeliveryPushItem{{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   88,
			MessageSeq:  9,
			Routes:      []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10}},
			Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
		}},
	})
	require.NoError(t, err)

	respBody, err := adapter.handleDeliveryPushRPC(context.Background(), reqBody)
	require.NoError(t, err)
	require.True(t, isDeliveryPushResponseBinary(respBody))
	resp := mustDecodeDeliveryPushResponse(t, respBody)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.True(t, resp.AcceptedCountSet)
	require.Equal(t, uint64(1), resp.AcceptedCount)
	require.Empty(t, resp.Accepted)
	require.Len(t, active.WrittenFrames(), 1)
}

func TestPushBatchRPCPreservesPersonItemChannelViews(t *testing.T) {
	reg := online.NewRegistry()
	sender := newRecordingSession(10, "tcp")
	recipient := newRecordingSession(11, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   10,
		UID:         "u1",
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     sender,
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u2",
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     recipient,
	}))

	adapter := New(Options{
		Presence:      presence.New(presence.Options{}),
		GatewayBootID: 7,
		LocalNodeID:   1,
		Online:        reg,
	})

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustEncodeDeliveryPushBatchCommand(t, DeliveryPushBatchCommand{
		OwnerNodeID: 2,
		Items: []DeliveryPushItem{
			{
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				MessageID:   88,
				MessageSeq:  9,
				Routes:      []deliveryruntime.RouteKey{{UID: "u1", NodeID: 1, BootID: 7, SessionID: 10}},
				Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "u2", ChannelType: frame.ChannelTypePerson, MessageID: 88, MessageSeq: 9}),
			},
			{
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				MessageID:   88,
				MessageSeq:  9,
				Routes:      []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 7, SessionID: 11}},
				Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "u1", ChannelType: frame.ChannelTypePerson, MessageID: 88, MessageSeq: 9}),
			},
		},
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryPushResponse(t, body)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.True(t, resp.AcceptedCountSet)
	require.Equal(t, uint64(2), resp.AcceptedCount)
	require.Empty(t, resp.Accepted)

	senderWrites := sender.WrittenFrames()
	recipientWrites := recipient.WrittenFrames()
	require.Len(t, senderWrites, 1)
	require.Len(t, recipientWrites, 1)
	require.Equal(t, "u2", senderWrites[0].(*frame.RecvPacket).ChannelID)
	require.Equal(t, "u1", recipientWrites[0].(*frame.RecvPacket).ChannelID)
}

func TestPushBatchRPCDecodesAllItemsBeforeWriting(t *testing.T) {
	reg := online.NewRegistry()
	active := newRecordingSession(10, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   10,
		UID:         "u2",
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     active,
	}))

	ackIndex := deliveryruntime.NewAckIndex()
	adapter := New(Options{
		Presence:         presence.New(presence.Options{}),
		GatewayBootID:    7,
		LocalNodeID:      1,
		Online:           reg,
		DeliveryAckIndex: ackIndex,
	})

	_, err := adapter.handleDeliveryPushRPC(context.Background(), mustEncodeDeliveryPushBatchCommand(t, DeliveryPushBatchCommand{
		OwnerNodeID: 2,
		Items: []DeliveryPushItem{
			{
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				MessageID:   88,
				MessageSeq:  9,
				Routes:      []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10}},
				Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
			},
			{
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				MessageID:   89,
				MessageSeq:  10,
				Routes:      []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10}},
				Frame:       []byte{0xff},
			},
		},
	}))
	require.Error(t, err)
	require.Empty(t, active.WrittenFrames())
	_, ok := ackIndex.Lookup(10, 88)
	require.False(t, ok)
}

func TestPushBatchRPCRejectsBootMismatchAndClosingRoutes(t *testing.T) {
	reg := online.NewRegistry()
	active := newRecordingSession(10, "tcp")
	closing := newRecordingSession(11, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   10,
		UID:         "u2",
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     active,
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u2",
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     closing,
	}))
	_, ok := reg.MarkClosing(11)
	require.True(t, ok)

	ackIndex := deliveryruntime.NewAckIndex()
	adapter := New(Options{
		Presence:         presence.New(presence.Options{}),
		GatewayBootID:    7,
		LocalNodeID:      1,
		Online:           reg,
		DeliveryAckIndex: ackIndex,
	})

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustEncodeDeliveryPushRequest(t, deliveryPushRequest{
		OwnerNodeID: 2,
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   88,
		MessageSeq:  9,
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10},
			{UID: "u2", NodeID: 1, BootID: 99, SessionID: 10},
			{UID: "u2", NodeID: 1, BootID: 7, SessionID: 11},
		},
		Frame: mustEncodeFrame(t, &frame.RecvPacket{MessageID: 88, MessageSeq: 9}),
	}))
	require.NoError(t, err)

	resp := mustDecodeDeliveryPushResponse(t, body)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10}}, resp.Accepted)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 1, BootID: 99, SessionID: 10},
		{UID: "u2", NodeID: 1, BootID: 7, SessionID: 11},
	}, resp.Dropped)
	require.Len(t, active.WrittenFrames(), 1)
	require.Empty(t, closing.WrittenFrames())

	binding, ok := ackIndex.Lookup(10, 88)
	require.True(t, ok)
	require.Equal(t, uint64(2), binding.OwnerNodeID)
	require.Equal(t, "u2", binding.ChannelID)
}

func TestDeliveryPushClientPushBatchItemsUsesDeliveryRPCService(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	New(Options{
		Cluster:       network.cluster(2),
		Presence:      presence.New(presence.Options{}),
		LocalNodeID:   2,
		GatewayBootID: 7,
		Online:        online.NewRegistry(),
	})
	client := NewClient(network.cluster(1))

	resp, err := client.PushBatchItems(context.Background(), 2, DeliveryPushBatchCommand{
		OwnerNodeID: 1,
		Items: []DeliveryPushItem{{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   88,
			MessageSeq:  9,
			Routes:      []deliveryruntime.RouteKey{{UID: "u2", NodeID: 2, BootID: 7, SessionID: 10}},
			Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
		}},
	})
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.True(t, resp.AcceptedCountSet)
	require.Equal(t, uint64(0), resp.AcceptedCount)
	require.Empty(t, resp.Accepted)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 2, BootID: 7, SessionID: 10}}, resp.Dropped)
}

func TestDeliveryPushClientPushBatchItemsUsesBinaryRPCPayload(t *testing.T) {
	cluster := &capturingDeliveryPushCluster{
		response: mustDeliveryPushBinaryResponse(t, DeliveryPushResponse{
			Status:           rpcStatusOK,
			AcceptedCount:    1,
			AcceptedCountSet: true,
		}),
	}
	client := NewClient(cluster)

	resp, err := client.PushBatchItems(context.Background(), 2, DeliveryPushBatchCommand{
		OwnerNodeID: 1,
		Items: []DeliveryPushItem{{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   88,
			MessageSeq:  9,
			Routes:      []deliveryruntime.RouteKey{{UID: "u2", NodeID: 2, BootID: 7, SessionID: 10}},
			Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
		}},
	})
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.True(t, resp.AcceptedCountSet)
	require.Equal(t, uint64(1), resp.AcceptedCount)
	require.Empty(t, resp.Accepted)
	require.True(t, isDeliveryPushRequestBinary(cluster.payload))
	require.Equal(t, deliveryPushRPCServiceID, cluster.serviceID)
	require.Equal(t, multiraft.NodeID(2), cluster.nodeID)
	req, _, err := decodeDeliveryPushRequest(cluster.payload)
	require.NoError(t, err)
	require.True(t, req.acceptsAcceptedCount)
}

func TestDeliveryPushClientPushBatchItemsFallsBackToLegacyRequestWhenPeerRejectsV2(t *testing.T) {
	route := deliveryruntime.RouteKey{UID: "u2", NodeID: 2, BootID: 7, SessionID: 10}
	cluster := &legacyOnlyDeliveryPushCluster{
		response: mustDeliveryPushBinaryResponse(t, DeliveryPushResponse{
			Status:   rpcStatusOK,
			Accepted: []deliveryruntime.RouteKey{route},
		}),
	}
	client := NewClient(cluster)

	resp, err := client.PushBatchItems(context.Background(), 2, DeliveryPushBatchCommand{
		OwnerNodeID: 1,
		Items: []DeliveryPushItem{{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   88,
			MessageSeq:  9,
			Routes:      []deliveryruntime.RouteKey{route},
			Frame:       mustEncodeFrame(t, &frame.RecvPacket{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 88, MessageSeq: 9}),
		}},
	})

	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{route}, resp.Accepted)
	require.False(t, resp.AcceptedCountSet)
	require.Len(t, cluster.payloads, 2)
	require.True(t, hasMagic(cluster.payloads[0], deliveryPushRequestMagicV2[:]))
	require.True(t, hasMagic(cluster.payloads[1], deliveryPushRequestMagicV1[:]))
}

type writeErrorSession struct {
	recordingSession
	err error
}

func (s *writeErrorSession) WriteFrame(f frame.Frame) error {
	_ = f
	return s.err
}

func mustDeliveryPushBinaryResponse(t *testing.T, resp DeliveryPushResponse) []byte {
	t.Helper()
	body, err := encodeDeliveryPushResponseBinary(resp)
	require.NoError(t, err)
	return body
}

func mustEncodeDeliveryPushBatchCommand(t *testing.T, cmd DeliveryPushBatchCommand) []byte {
	t.Helper()
	body, err := encodeDeliveryPushBatchCommandBinary(cmd)
	require.NoError(t, err)
	return body
}

func mustEncodeDeliveryPushRequest(t *testing.T, req deliveryPushRequest) []byte {
	t.Helper()
	return encodeDeliveryPushRequestBinary(req)
}

type capturingDeliveryPushCluster struct {
	nodeID    multiraft.NodeID
	serviceID uint8
	payload   []byte
	response  []byte
}

func (c *capturingDeliveryPushCluster) RPCMux() *transport.RPCMux { return nil }

func (c *capturingDeliveryPushCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}

func (c *capturingDeliveryPushCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c *capturingDeliveryPushCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *capturingDeliveryPushCluster) RPCService(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	c.nodeID = nodeID
	c.serviceID = serviceID
	c.payload = append([]byte(nil), payload...)
	return c.response, nil
}

func (c *capturingDeliveryPushCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

type legacyOnlyDeliveryPushCluster struct {
	payloads [][]byte
	response []byte
}

func (c *legacyOnlyDeliveryPushCluster) RPCMux() *transport.RPCMux { return nil }

func (c *legacyOnlyDeliveryPushCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}

func (c *legacyOnlyDeliveryPushCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c *legacyOnlyDeliveryPushCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *legacyOnlyDeliveryPushCluster) RPCService(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
	c.payloads = append(c.payloads, append([]byte(nil), payload...))
	if hasMagic(payload, deliveryPushRequestMagicV2[:]) {
		return nil, errors.New("access/node: invalid delivery push request codec")
	}
	return c.response, nil
}

func (c *legacyOnlyDeliveryPushCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}
