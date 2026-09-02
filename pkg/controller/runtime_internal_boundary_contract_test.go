package controller

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/server"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	controllersync "github.com/WuKongIM/WuKongIM/pkg/controller/sync"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRuntimeRaftFacadePropagatesAConstructedButUnstartedService(t *testing.T) {
	runtime := &Runtime{raft: &controllerraft.Service{}}
	require.Zero(t, runtime.LeaderID())
	require.ErrorIs(t, runtime.ProbePropose(context.Background()), ErrNotStarted)
	status, err := runtime.ControllerRaftStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, controllerraft.RoleUnknown, status.Role)
	_, err = runtime.CompactControllerRaftLog(context.Background())
	require.ErrorIs(t, err, ErrNotStarted)
	require.ErrorIs(t, runtime.Step(context.Background(), raftpb.Message{}), ErrNotStarted)
	_, err = runtime.LogEntries(context.Background(), LogEntriesOptions{})
	require.ErrorIs(t, err, ErrNotStarted)
	require.NoError(t, runtime.Stop(context.Background()))
}

func TestRuntimeControllerVoterHelpersPreserveCallerOwnedMembership(t *testing.T) {
	require.Nil(t, copyOptionalUint64s(nil))
	ids := []uint64{4, 1, 3}
	copiedIDs := copyOptionalUint64s(ids)
	copiedIDs[0] = 99
	require.Equal(t, uint64(4), ids[0])

	voters := []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}}
	copiedVoters := copyVoters(voters)
	copiedVoters[0].Addr = "changed"
	require.Equal(t, "n1", voters[0].Addr)
	require.True(t, containsRuntimeUint64(ids, 1))
	require.False(t, containsRuntimeUint64(ids, 9))
	require.True(t, sameRuntimeUint64Set([]uint64{1, 4}, []uint64{4, 1}))
	require.False(t, sameRuntimeUint64Set([]uint64{1}, []uint64{1, 4}))
	require.False(t, sameRuntimeUint64Set([]uint64{1, 4}, []uint64{1, 3}))
	require.Equal(t, []uint64{1, 3, 4}, cloneSortedUint64s(ids))
	require.Equal(t, []uint64{4, 1, 3}, ids, "sorting the proof set must not mutate its caller")
}

func TestRuntimeClearsPreparedControllerVoterResourcesAsOneLifecycleUnit(t *testing.T) {
	runtime := &Runtime{
		raft:       &controllerraft.Service{},
		sm:         mustRuntimeUnitStateMachine(t),
		server:     &server.Server{},
		syncServer: controllersync.NewServer(controllersync.ServerConfig{}),
	}
	runtime.clearControllerVoterRuntimeFields()
	require.Nil(t, runtime.raft)
	require.Nil(t, runtime.sm)
	require.Nil(t, runtime.server)
	require.Nil(t, runtime.syncServer)
}

func TestRuntimeSyncAdapterRejectsMissingOrUnavailableClient(t *testing.T) {
	_, err := (syncClientAdapter{}).SyncOnce(context.Background())
	require.ErrorContains(t, err, "sync client is required")

	client := controllersync.NewClient(controllersync.ClientConfig{
		ClusterID: "wk-sync-adapter",
		Store:     statefile.New(filepath.Join(t.TempDir(), "cluster-state.json")),
		Peers:     runtimeUnitPeerPicker{},
	})
	_, err = (syncClientAdapter{client: client}).SyncOnce(context.Background())
	require.ErrorIs(t, err, controllersync.ErrNoReachablePeer)
}

func TestPublicStateSyncServerConstructorRetainsAuthoritativeCallbacks(t *testing.T) {
	state := runtimeContractState(t, 14)
	endpoint := NewStateSyncServer(StateSyncServerConfig{
		NodeID:    1,
		ClusterID: state.ClusterID,
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot: func(context.Context) (ClusterState, error) {
			return state, nil
		},
	})
	response, err := endpoint.GetState(context.Background(), GetStateRequest{ClusterID: state.ClusterID})
	require.NoError(t, err)
	require.Equal(t, state.Revision, response.Revision)
}

func mustRuntimeUnitStateMachine(t *testing.T) *fsm.StateMachine {
	t.Helper()
	sm, err := fsm.New(statefile.New(filepath.Join(t.TempDir(), "cluster-state.json")))
	require.NoError(t, err)
	return sm
}
