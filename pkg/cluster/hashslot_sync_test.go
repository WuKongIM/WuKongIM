package cluster

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestControllerClientRefreshAssignmentsUpdatesRouterHashSlotTable(t *testing.T) {
	base, updated, movedHashSlot := makeMovedHashSlotTableFixture(t, 2)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCListAssignments, req.Kind)

		return encodeControllerResponse(controllerRPCListAssignments, controllerRPCResponse{
			Assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1},
			},
			HashSlotTableVersion: updated.Version(),
			HashSlotTable:        updated.Encode(),
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cache := newAssignmentCache()
	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
		router: NewRouter(base, 3, nil),
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 2}}, cache)

	assignments, err := controllerClient.RefreshAssignments(context.Background())
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, multiraft.SlotID(2), cluster.router.hashSlotTable.Load().Lookup(movedHashSlot))
	require.Equal(t, updated.Version(), cluster.HashSlotTableVersion())

	cached := cache.Snapshot()
	require.Len(t, cached, 1)
	require.Equal(t, assignments[0].SlotID, cached[0].SlotID)
}

func TestControllerClientHeartbeatUpdatesRouterHashSlotTable(t *testing.T) {
	base, updated, movedHashSlot := makeMovedHashSlotTableFixture(t, 2)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCHeartbeat, req.Kind)
		require.NotNil(t, req.Report)
		require.Equal(t, base.Version(), req.Report.HashSlotTableVersion)

		return encodeControllerResponse(controllerRPCHeartbeat, controllerRPCResponse{
			HashSlotTableVersion: updated.Version(),
			HashSlotTable:        updated.Encode(),
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
		router: NewRouter(base, 3, nil),
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 2}}, newAssignmentCache())

	report := slotcontrollerReport(cluster, time.Now(), nil)
	require.NoError(t, controllerClient.Report(context.Background(), report))
	require.Equal(t, multiraft.SlotID(2), cluster.router.hashSlotTable.Load().Lookup(movedHashSlot))
	require.Equal(t, updated.Version(), cluster.HashSlotTableVersion())
}

func TestSlotAgentSyncAssignmentsFallbackUpdatesRouterHashSlotTableFromLocalControllerMeta(t *testing.T) {
	base, updated, movedHashSlot := makeMovedHashSlotTableFixture(t, 2)

	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
		ConfigEpoch:  2,
	}
	require.NoError(t, store.UpsertAssignment(context.Background(), assignment))
	require.NoError(t, store.SaveHashSlotTable(context.Background(), updated))

	cache := newAssignmentCache()
	cluster := &Cluster{
		controllerResources: controllerResources{
			controllerMeta:              store,
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
		},
		router: NewRouter(base, 1, nil),
		agentResources: agentResources{
			assignments: cache,
		},
	}
	agent := &slotAgent{
		cluster: cluster,
		client:  fakeControllerClient{assignmentsErr: context.DeadlineExceeded},
		cache:   cache,
	}

	require.NoError(t, agent.SyncAssignments(context.Background()))
	require.Equal(t, multiraft.SlotID(2), cluster.router.hashSlotTable.Load().Lookup(movedHashSlot))
	require.Equal(t, updated.Version(), cluster.HashSlotTableVersion())

	cached := cache.Snapshot()
	require.Len(t, cached, 1)
	require.Equal(t, assignment.SlotID, cached[0].SlotID)
}

func TestListSlotAssignmentsWithoutControllerClientUpdatesRouterHashSlotTableFromLocalControllerMeta(t *testing.T) {
	base, updated, movedHashSlot := makeMovedHashSlotTableFixture(t, 2)

	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 2, 3},
		ConfigEpoch:  3,
	}
	require.NoError(t, store.UpsertAssignment(context.Background(), assignment))
	require.NoError(t, store.SaveHashSlotTable(context.Background(), updated))

	cluster := &Cluster{
		controllerResources: controllerResources{
			controllerMeta: store,
		},
		router: NewRouter(base, 1, nil),
	}

	assignments, err := cluster.ListSlotAssignments(context.Background())
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, multiraft.SlotID(2), cluster.router.hashSlotTable.Load().Lookup(movedHashSlot))
	require.Equal(t, updated.Version(), cluster.HashSlotTableVersion())
}

func makeMovedHashSlotTableFixture(t *testing.T, targetSlot multiraft.SlotID) (*HashSlotTable, *HashSlotTable, uint16) {
	t.Helper()

	base := NewHashSlotTable(8, 2)
	var movedHashSlot uint16
	found := false
	for hashSlot := uint16(0); hashSlot < base.HashSlotCount(); hashSlot++ {
		if base.Lookup(hashSlot) == targetSlot {
			continue
		}
		movedHashSlot = hashSlot
		found = true
		break
	}
	require.True(t, found, "expected to find hash slot outside slot %d", targetSlot)

	updated := base.Clone()
	updated.Reassign(movedHashSlot, targetSlot)
	require.Equal(t, targetSlot, updated.Lookup(movedHashSlot))
	require.NotEqual(t, targetSlot, base.Lookup(movedHashSlot))
	return base, updated, movedHashSlot
}
