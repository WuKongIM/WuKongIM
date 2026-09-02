package controller

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRuntimeFacadeBeforeStartPreservesLocalStateAndLifecycle(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:    1,
		StateDir:  t.TempDir(),
		ClusterID: "wk-runtime-contract",
		Voters:    []Voter{{NodeID: 1, Addr: "n1"}},
	})
	require.NoError(t, err)
	require.NoError(t, runtime.publishState(runtimeContractState(t, 7)))

	local, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(7), local.Revision)
	require.Equal(t, uint64(1), runtime.LeaderID())
	local.Nodes[0].Name = "mutated-copy"
	local.Controllers[0].Addr = "mutated-copy"

	again, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.Empty(t, again.Nodes[0].Name)
	require.Equal(t, "n1", again.Controllers[0].Addr)
	require.ErrorIs(t, runtime.ProbePropose(context.Background()), ErrNotStarted)
	_, err = runtime.ControllerRaftStatus(context.Background())
	require.ErrorIs(t, err, ErrNotStarted)
	_, err = runtime.CompactControllerRaftLog(context.Background())
	require.ErrorIs(t, err, ErrNotStarted)
	require.NoError(t, runtime.Step(context.Background(), raftpb.Message{}))
	response, err := runtime.GetState(context.Background(), GetStateRequest{})
	require.NoError(t, err)
	require.True(t, response.NotReady)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runtime.LocalState(canceled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRuntimeWatchRetainsNewestStateUnderPressure(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:    1,
		StateDir:  t.TempDir(),
		ClusterID: "wk-runtime-contract",
		Voters:    []Voter{{NodeID: 1, Addr: "n1"}},
	})
	require.NoError(t, err)
	runtime.watch = make(chan StateEvent, 1)

	require.NoError(t, runtime.publishState(runtimeContractState(t, 7)))
	require.NoError(t, runtime.publishState(runtimeContractState(t, 8)))

	select {
	case event := <-runtime.Watch():
		require.Equal(t, uint64(8), event.State.Revision)
		event.State.Nodes[0].Name = "mutated-event"
	default:
		t.Fatal("latest state event was not retained")
	}
	local, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(8), local.Revision)
	require.Empty(t, local.Nodes[0].Name)
}

func TestRuntimeRefreshesSameRevisionWhenMaterializedChecksumChanges(t *testing.T) {
	ctx := context.Background()
	visible := runtimeContractState(t, 7)
	visible.Nodes[0].Name = "stale-visible-state"
	visible.Checksum = runtimeContractChecksum(t, visible)
	durable := runtimeContractState(t, 7)
	durable.Nodes[0].Name = "durable-state"
	durable.Checksum = runtimeContractChecksum(t, durable)

	sm, err := fsm.New(statefile.New(filepath.Join(t.TempDir(), "cluster-state.json")))
	require.NoError(t, err)
	require.NoError(t, sm.Restore(ctx, durable))
	runtime := &Runtime{
		state: visible.Clone(),
		sm:    sm,
		watch: make(chan StateEvent, 1),
	}

	require.NoError(t, runtime.publishIfChanged(ctx, durable.Revision))
	local, err := runtime.LocalState(ctx)
	require.NoError(t, err)
	require.Equal(t, durable.Revision, local.Revision)
	require.Equal(t, durable.Checksum, local.Checksum)
	require.Equal(t, "durable-state", local.Nodes[0].Name)
}

func runtimeContractState(t *testing.T, revision uint64) ClusterState {
	t.Helper()
	st := ClusterState{
		SchemaVersion:    CurrentSchemaVersion,
		ClusterID:        "wk-runtime-contract",
		Revision:         revision,
		AppliedRaftIndex: revision,
		Config: ClusterConfig{
			SlotCount:             1,
			HashSlotCount:         4,
			ReplicaCount:          1,
			DefaultCapacityWeight: 1,
		},
		Controllers: []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes: []Node{{
			NodeID:         1,
			Addr:           "n1",
			Roles:          []NodeRole{NodeRoleControllerVoter, NodeRoleData},
			JoinState:      NodeJoinStateActive,
			Status:         NodeStatusAlive,
			CapacityWeight: 1,
		}},
		Slots: []SlotAssignment{{
			SlotID:          1,
			DesiredPeers:    []uint64{1},
			ConfigEpoch:     1,
			PreferredLeader: 1,
		}},
		HashSlots: HashSlotTable{
			Version:   CurrentHashSlotTableVersion,
			SlotCount: 4,
			Ranges:    []HashSlotRange{{From: 0, To: 3, SlotID: 1}},
		},
	}
	require.NoError(t, st.Validate())
	st.Checksum = runtimeContractChecksum(t, st)
	return st
}

func runtimeContractChecksum(t *testing.T, st ClusterState) string {
	t.Helper()
	checksum, err := cv2state.Checksum(st)
	require.NoError(t, err)
	return checksum
}
