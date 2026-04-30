package node

import (
	"context"
	"errors"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustMarshal(t, DeliveryPushBatchCommand{
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
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 7, SessionID: 10}}, resp.Accepted)
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

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustMarshal(t, DeliveryPushBatchCommand{
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
	require.Len(t, resp.Accepted, 2)

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

	_, err := adapter.handleDeliveryPushRPC(context.Background(), mustMarshal(t, DeliveryPushBatchCommand{
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

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustMarshal(t, deliveryPushRequest{
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
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 2, BootID: 7, SessionID: 10}}, resp.Dropped)
}

type writeErrorSession struct {
	recordingSession
	err error
}

func (s *writeErrorSession) WriteFrame(f frame.Frame) error {
	_ = f
	return s.err
}
