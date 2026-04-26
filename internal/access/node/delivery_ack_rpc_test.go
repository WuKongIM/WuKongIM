package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/stretchr/testify/require"
)

func TestAckNotifyRPCRoutesAckToOwnerActor(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	recorder := &recordingDeliveryAck{}
	New(Options{
		Cluster:       node2,
		Presence:      presence.New(presence.Options{}),
		Online:        online.NewRegistry(),
		GatewayBootID: 22,
		DeliveryAck:   recorder,
	})

	client := NewClient(node1)
	err := client.NotifyAck(context.Background(), 2, deliveryevents.RouteAck{
		UID:        "u2",
		SessionID:  10,
		MessageID:  88,
		MessageSeq: 9,
	})
	require.NoError(t, err)
	require.Equal(t, []deliveryevents.RouteAck{{
		UID:        "u2",
		SessionID:  10,
		MessageID:  88,
		MessageSeq: 9,
	}}, recorder.calls)
}

type recordingDeliveryAck struct {
	calls []deliveryevents.RouteAck
}

func (r *recordingDeliveryAck) AckRoute(_ context.Context, cmd deliveryevents.RouteAck) error {
	r.calls = append(r.calls, cmd)
	return nil
}
