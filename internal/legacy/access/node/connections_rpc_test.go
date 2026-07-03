package node

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestConnectionsRPCReturnsLocalConnections(t *testing.T) {
	reg := online.NewRegistry()
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   101,
		UID:         "u1",
		DeviceID:    "device-a",
		DeviceFlag:  frame.WEB,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      9,
		State:       online.LocalRouteStateActive,
		Listener:    "ws",
		ConnectedAt: time.Unix(200, 0).UTC(),
		Session:     newRecordingSession(101, "ws"),
	}))
	adapter := New(Options{LocalNodeID: 2, Online: reg})
	reqBody, err := encodeConnectionsRequestBinary(connectionsRequest{NodeID: 2})
	require.NoError(t, err)

	body, err := adapter.handleConnectionsRPC(context.Background(), reqBody)

	require.NoError(t, err)
	resp, err := decodeConnectionsResponse(body)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []Connection{{
		NodeID:      2,
		SessionID:   101,
		UID:         "u1",
		DeviceID:    "device-a",
		DeviceFlag:  "web",
		DeviceLevel: "master",
		SlotID:      9,
		State:       "active",
		Listener:    "ws",
		ConnectedAt: time.Unix(200, 0).UTC(),
	}}, resp.Items)
}

func TestConnectionDetailClientCallsRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	reg := online.NewRegistry()
	require.NoError(t, reg.Register(testOnlineConn(202, "u2", 3)))
	New(Options{Cluster: node2, LocalNodeID: 2, Online: reg})

	got, err := NewClient(node1).Connection(context.Background(), 2, 202)

	require.NoError(t, err)
	require.Equal(t, uint64(2), got.NodeID)
	require.Equal(t, uint64(202), got.SessionID)
	require.Equal(t, "u2", got.UID)
}
