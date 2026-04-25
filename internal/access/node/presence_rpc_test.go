package node

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestPresenceRPCClientFollowsNotLeaderRedirect(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 2},
	)

	node1 := network.cluster(1)
	node2 := network.cluster(2)

	presence1 := presence.New(presence.Options{LocalNodeID: 1, GatewayBootID: 11})
	presence2 := presence.New(presence.Options{LocalNodeID: 2, GatewayBootID: 22})
	New(Options{Cluster: node1, Presence: presence1, Online: online.NewRegistry(), GatewayBootID: 11})
	New(Options{Cluster: node2, Presence: presence2, Online: online.NewRegistry(), GatewayBootID: 22})

	client := NewClient(node1)
	_, err := client.RegisterAuthoritative(context.Background(), presence.RegisterAuthoritativeCommand{
		SlotID: 1,
		Route: presence.Route{
			UID:         "u1",
			NodeID:      2,
			BootID:      22,
			SessionID:   100,
			DeviceID:    "d1",
			DeviceFlag:  uint8(frame.APP),
			DeviceLevel: uint8(frame.DeviceLevelMaster),
			Listener:    "tcp",
		},
	})
	require.NoError(t, err)
	require.Empty(t, requirePresenceEndpoints(t, presence1, "u1"))
	require.Len(t, requirePresenceEndpoints(t, presence2, "u1"), 1)
}

func TestPresenceRPCRegisterRoundTrip(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)

	presenceApp := presence.New(presence.Options{
		LocalNodeID:   1,
		GatewayBootID: 11,
		Now:           func() time.Time { return time.Unix(200, 0) },
	})
	New(Options{Cluster: node1, Presence: presenceApp, Online: online.NewRegistry(), GatewayBootID: 11})

	client := NewClient(node1)
	result, err := client.RegisterAuthoritative(context.Background(), presence.RegisterAuthoritativeCommand{
		SlotID: 1,
		Route: presence.Route{
			UID:         "u1",
			NodeID:      1,
			BootID:      11,
			SessionID:   100,
			DeviceID:    "d1",
			DeviceFlag:  uint8(frame.APP),
			DeviceLevel: uint8(frame.DeviceLevelMaster),
			Listener:    "tcp",
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Actions)
	require.Len(t, requirePresenceEndpoints(t, presenceApp, "u1"), 1)
}

func TestPresenceRPCUnregisterRoundTrip(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)

	presenceApp := presence.New(presence.Options{
		LocalNodeID:   1,
		GatewayBootID: 11,
		Now:           func() time.Time { return time.Unix(200, 0) },
	})
	New(Options{Cluster: node1, Presence: presenceApp, Online: online.NewRegistry(), GatewayBootID: 11})

	client := NewClient(node1)
	route := presence.Route{
		UID:         "u1",
		NodeID:      1,
		BootID:      11,
		SessionID:   100,
		DeviceID:    "d1",
		DeviceFlag:  uint8(frame.APP),
		DeviceLevel: uint8(frame.DeviceLevelMaster),
		Listener:    "tcp",
	}
	_, err := client.RegisterAuthoritative(context.Background(), presence.RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  route,
	})
	require.NoError(t, err)

	require.NoError(t, client.UnregisterAuthoritative(context.Background(), presence.UnregisterAuthoritativeCommand{
		SlotID: 1,
		Route:  route,
	}))
	require.Empty(t, requirePresenceEndpoints(t, presenceApp, "u1"))
}

func TestPresenceRPCReplayRoundTrip(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)

	presenceApp := presence.New(presence.Options{
		LocalNodeID:   1,
		GatewayBootID: 11,
		Now:           func() time.Time { return time.Unix(200, 0) },
	})
	New(Options{Cluster: node1, Presence: presenceApp, Online: online.NewRegistry(), GatewayBootID: 11})

	client := NewClient(node1)
	err := client.ReplayAuthoritative(context.Background(), presence.ReplayAuthoritativeCommand{
		Lease: presence.GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  11,
			LeaseUntilUnix: 230,
		},
		Routes: []presence.Route{{
			UID:         "u1",
			NodeID:      1,
			BootID:      11,
			SessionID:   100,
			DeviceID:    "d1",
			DeviceFlag:  uint8(frame.APP),
			DeviceLevel: uint8(frame.DeviceLevelMaster),
			Listener:    "tcp",
		}},
	})
	require.NoError(t, err)
	require.Len(t, requirePresenceEndpoints(t, presenceApp, "u1"), 1)
}

func TestPresenceRPCApplyActionRoundTrip(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)

	onlineReg := online.NewRegistry()
	require.NoError(t, onlineReg.Register(testOnlineConn(11, "u1", 1)))
	presenceApp := presence.New(presence.Options{
		LocalNodeID:   1,
		GatewayBootID: 11,
		Online:        onlineReg,
	})
	New(Options{Cluster: node1, Presence: presenceApp, Online: onlineReg, GatewayBootID: 11})

	client := NewClient(node1)
	require.NoError(t, client.ApplyRouteAction(context.Background(), presence.RouteAction{
		UID:       "u1",
		NodeID:    1,
		BootID:    11,
		SessionID: 11,
		Kind:      "close",
	}))

	conn, ok := onlineReg.Connection(11)
	require.True(t, ok)
	require.Equal(t, online.LocalRouteStateClosing, conn.State)
}

func TestPresenceRPCEndpointsByUIDsRoundTrip(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)

	presenceApp := presence.New(presence.Options{
		LocalNodeID:   1,
		GatewayBootID: 11,
		Now:           func() time.Time { return time.Unix(200, 0) },
	})
	New(Options{Cluster: node1, Presence: presenceApp, Online: online.NewRegistry(), GatewayBootID: 11})

	client := NewClient(node1)
	_, err := client.RegisterAuthoritative(context.Background(), presence.RegisterAuthoritativeCommand{
		SlotID: 1,
		Route: presence.Route{
			UID:         "u1",
			NodeID:      1,
			BootID:      11,
			SessionID:   100,
			DeviceID:    "d1",
			DeviceFlag:  uint8(frame.APP),
			DeviceLevel: uint8(frame.DeviceLevelMaster),
			Listener:    "tcp",
		},
	})
	require.NoError(t, err)
	_, err = client.RegisterAuthoritative(context.Background(), presence.RegisterAuthoritativeCommand{
		SlotID: 1,
		Route: presence.Route{
			UID:         "u2",
			NodeID:      1,
			BootID:      11,
			SessionID:   200,
			DeviceID:    "d2",
			DeviceFlag:  uint8(frame.WEB),
			DeviceLevel: uint8(frame.DeviceLevelMaster),
			Listener:    "tcp",
		},
	})
	require.NoError(t, err)

	endpoints, err := client.EndpointsByUIDs(context.Background(), []string{"u2", "u1"})
	require.NoError(t, err)
	require.Len(t, endpoints["u1"], 1)
	require.Equal(t, uint64(100), endpoints["u1"][0].SessionID)
	require.Len(t, endpoints["u2"], 1)
	require.Equal(t, uint64(200), endpoints["u2"][0].SessionID)
}

type fakeClusterNetwork struct {
	mu        sync.Mutex
	peers     map[uint64][]uint64
	leaders   map[uint64]uint64
	muxByNode map[uint64]*transport.RPCMux
}

func newFakeClusterNetwork(peers map[uint64][]uint64, leaders map[uint64]uint64) *fakeClusterNetwork {
	return &fakeClusterNetwork{
		peers:     peers,
		leaders:   leaders,
		muxByNode: make(map[uint64]*transport.RPCMux),
	}
}

func (n *fakeClusterNetwork) cluster(localNodeID uint64) *fakeCluster {
	return &fakeCluster{localNodeID: localNodeID, network: n}
}

func (n *fakeClusterNetwork) mux(nodeID uint64) *transport.RPCMux {
	n.mu.Lock()
	defer n.mu.Unlock()

	if mux := n.muxByNode[nodeID]; mux != nil {
		return mux
	}
	mux := transport.NewRPCMux()
	n.muxByNode[nodeID] = mux
	return mux
}

type fakeCluster struct {
	localNodeID uint64
	network     *fakeClusterNetwork
}

func (c *fakeCluster) RPCMux() *transport.RPCMux {
	return c.network.mux(c.localNodeID)
}

func (c *fakeCluster) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	leaderID, ok := c.network.leaders[uint64(slotID)]
	if !ok {
		return 0, raftcluster.ErrNoLeader
	}
	return multiraft.NodeID(leaderID), nil
}

func (c *fakeCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return c.localNodeID == uint64(nodeID)
}

func (c *fakeCluster) SlotForKey(key string) multiraft.SlotID {
	return multiraft.SlotID(1)
}

func (c *fakeCluster) RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	return c.network.mux(uint64(nodeID)).HandleRPC(ctx, append([]byte{serviceID}, payload...))
}

func (c *fakeCluster) PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID {
	peers := c.network.peers[uint64(slotID)]
	out := make([]multiraft.NodeID, 0, len(peers))
	for _, peer := range peers {
		out = append(out, multiraft.NodeID(peer))
	}
	return out
}

func requirePresenceEndpoints(t *testing.T, app *presence.App, uid string) []presence.Route {
	t.Helper()

	endpoints, err := app.EndpointsByUID(context.Background(), uid)
	require.NoError(t, err)
	return endpoints
}
