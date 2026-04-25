package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/stretchr/testify/require"
)

func TestRouteOfflineRPCDropsInflightRoute(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	recorder := &recordingDeliveryOffline{}
	New(Options{
		Cluster:         node2,
		Presence:        presence.New(presence.Options{}),
		Online:          online.NewRegistry(),
		GatewayBootID:   22,
		DeliveryOffline: recorder,
	})

	client := NewClient(node1)
	err := client.NotifyOffline(context.Background(), 2, message.SessionClosedCommand{
		UID:       "u2",
		SessionID: 10,
	})
	require.NoError(t, err)
	require.Equal(t, []message.SessionClosedCommand{{
		UID:       "u2",
		SessionID: 10,
	}}, recorder.calls)
}

type recordingDeliveryOffline struct {
	calls []message.SessionClosedCommand
}

func (r *recordingDeliveryOffline) SessionClosed(_ context.Context, cmd message.SessionClosedCommand) error {
	r.calls = append(r.calls, cmd)
	return nil
}
