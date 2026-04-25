package node

import (
	"context"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

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
