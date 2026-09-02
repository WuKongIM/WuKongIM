package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/server"
	controllerstate "github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/stretchr/testify/require"
)

type runtimeUnitPeerPicker struct {
	ids       []uint64
	endpoints map[uint64]Endpoint
}

func (p runtimeUnitPeerPicker) Endpoint(nodeID uint64) (Endpoint, bool) {
	ep, ok := p.endpoints[nodeID]
	return ep, ok
}

func (p runtimeUnitPeerPicker) PeerIDs() []uint64 {
	return append([]uint64(nil), p.ids...)
}

type runtimeUnitEndpoint struct {
	response GetStateResponse
	err      error
	requests []GetStateRequest
}

func (e *runtimeUnitEndpoint) GetState(_ context.Context, req GetStateRequest) (GetStateResponse, error) {
	e.requests = append(e.requests, req)
	return e.response, e.err
}

type runtimeUnitServerSync struct {
	state ClusterState
	err   error
	calls int
}

func (s *runtimeUnitServerSync) SyncOnce(context.Context) (controllerstate.ClusterState, error) {
	s.calls++
	return s.state.Clone(), s.err
}

func TestRuntimeMirrorStartPublishesValidatedLeaderSnapshotWithoutNetworkListener(t *testing.T) {
	state := runtimeContractState(t, 7)
	payload, err := controllerstate.Encode(state)
	require.NoError(t, err)
	endpoint := &runtimeUnitEndpoint{response: GetStateResponse{
		LeaderID: 1,
		Revision: state.Revision,
		Checksum: state.Checksum,
		Payload:  payload,
	}}
	dir := t.TempDir()
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:       2,
		StateDir:     dir,
		ClusterID:    state.ClusterID,
		Role:         RuntimeRoleMirror,
		Voters:       []Voter{{NodeID: 1, Addr: "n1"}},
		SyncPeers:    runtimeUnitPeerPicker{ids: []uint64{1}, endpoints: map[uint64]Endpoint{1: endpoint}},
		TickInterval: time.Hour,
	})
	require.NoError(t, err)
	require.NoError(t, runtime.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, runtime.Stop(context.Background())) })

	visible, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.Equal(t, state.Revision, visible.Revision)
	require.Equal(t, state.Checksum, visible.Checksum)
	require.Equal(t, uint64(1), runtime.LeaderID())
	require.Len(t, endpoint.requests, 1)
	require.Equal(t, state.ClusterID, endpoint.requests[0].ClusterID)
	require.FileExists(t, filepath.Join(dir, "cluster-state.json"))

	select {
	case event := <-runtime.Watch():
		require.Equal(t, state.Revision, event.State.Revision)
	default:
		t.Fatal("mirror start did not publish its installed state")
	}
	response, err := runtime.GetState(context.Background(), GetStateRequest{ClusterID: state.ClusterID})
	require.NoError(t, err)
	require.True(t, response.NotReady, "mirrors must not serve authoritative snapshots")

	require.NoError(t, runtime.Stop(context.Background()))
	require.Nil(t, runtime.refreshCancel)
}

func TestRuntimeStartRejectsInvalidAndUnavailableRolesBeforeBackgroundWork(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	dir := filepath.Join(t.TempDir(), "canceled")
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID: 1, StateDir: dir, ClusterID: "wk-runtime-start", Voters: []Voter{{NodeID: 1, Addr: "n1"}},
	})
	require.NoError(t, err)
	require.ErrorIs(t, runtime.Start(canceled), context.Canceled)
	_, statErr := os.Stat(dir)
	require.ErrorIs(t, statErr, os.ErrNotExist)

	runtime.cfg.Role = RuntimeRole("observer")
	require.ErrorContains(t, runtime.Start(context.Background()), "invalid runtime role")
	require.Nil(t, runtime.refreshCancel)

	runtime.cfg.Role = RuntimeRoleMirror
	runtime.cfg.SyncPeers = nil
	require.ErrorContains(t, runtime.Start(context.Background()), "mirror sync peers required")
	require.Nil(t, runtime.refreshCancel)

	statePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(statePath, []byte("file"), 0o600))
	runtime.cfg.StateDir = statePath
	require.Error(t, runtime.Start(context.Background()))
}

func TestRuntimeSyncTickPublishesOnlyAChangedValidatedSnapshot(t *testing.T) {
	current := runtimeContractState(t, 7)
	updated := runtimeContractState(t, 8)

	unchangedSync := &runtimeUnitServerSync{state: current}
	unchangedServer, err := server.New(server.Config{SyncClient: unchangedSync})
	require.NoError(t, err)
	runtime := &Runtime{server: unchangedServer, state: current.Clone(), watch: make(chan StateEvent, 1)}
	require.NoError(t, runtime.syncTick(context.Background()))
	require.Equal(t, 1, unchangedSync.calls)
	require.Empty(t, runtime.watch)

	changedSync := &runtimeUnitServerSync{state: updated}
	changedServer, err := server.New(server.Config{SyncClient: changedSync})
	require.NoError(t, err)
	runtime.server = changedServer
	require.NoError(t, runtime.syncTick(context.Background()))
	require.Equal(t, uint64(8), runtime.state.Revision)
	require.Equal(t, uint64(8), (<-runtime.watch).State.Revision)

	wantErr := errors.New("leader unavailable")
	failedServer, err := server.New(server.Config{SyncClient: &runtimeUnitServerSync{err: wantErr}})
	require.NoError(t, err)
	runtime.server = failedServer
	require.ErrorIs(t, runtime.syncTick(context.Background()), wantErr)

	invalidServer, err := server.New(server.Config{SyncClient: &runtimeUnitServerSync{state: ClusterState{Revision: 9}}})
	require.NoError(t, err)
	runtime.server = invalidServer
	require.Error(t, runtime.syncTick(context.Background()))

	runtime.server = nil
	require.NoError(t, runtime.syncTick(context.Background()))
}

func TestRuntimeStateSyncServerServesOnlyReadyVoterState(t *testing.T) {
	state := runtimeContractState(t, 9)
	sm, err := fsm.New(statefile.New(filepath.Join(t.TempDir(), "cluster-state.json")))
	require.NoError(t, err)
	require.NoError(t, sm.Restore(context.Background(), state))
	runtime := &Runtime{
		cfg:   RuntimeConfig{NodeID: 1, ClusterID: state.ClusterID},
		state: state.Clone(),
		sm:    sm,
	}
	runtime.syncServer = runtime.newStateSyncServer()

	response, err := runtime.GetState(context.Background(), GetStateRequest{ClusterID: state.ClusterID})
	require.NoError(t, err)
	require.False(t, response.NotReady)
	require.Equal(t, state.Revision, response.Revision)
	require.NotEmpty(t, response.Payload)

	runtime.sm = nil
	response, err = runtime.GetState(context.Background(), GetStateRequest{ClusterID: state.ClusterID})
	require.NoError(t, err)
	require.True(t, response.NotReady)

	var transport noopRaftTransport
	transport.Send(nil)
}

func TestRuntimeRefreshLoopHasIdempotentStartAndSynchronousStop(t *testing.T) {
	runtime := &Runtime{cfg: RuntimeConfig{TickInterval: time.Hour}}
	runtime.stopRefreshLoop()
	runtime.startRefreshLoop()
	firstCancel := runtime.refreshCancel
	require.NotNil(t, firstCancel)
	runtime.startRefreshLoop()
	require.NotNil(t, runtime.refreshCancel)
	runtime.stopRefreshLoop()
	require.Nil(t, runtime.refreshCancel)
}

func TestRuntimeControlAndBootstrapTicksAvoidUnprovenMutations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sm, err := fsm.New(statefile.New(filepath.Join(dir, "cluster-state.json")))
	require.NoError(t, err)
	state := runtimeContractState(t, 7)
	require.NoError(t, sm.Restore(ctx, state))
	runtime := &Runtime{
		cfg: RuntimeConfig{
			NodeID: 1, ClusterID: state.ClusterID, InitialSlotCount: 1,
			Voters: []Voter{{NodeID: 1, Addr: "n1"}}, TickInterval: time.Hour,
		},
		sm: sm, server: &server.Server{}, state: runtimeContractState(t, 6), watch: make(chan StateEvent, 1),
	}
	require.NoError(t, runtime.controlTick(ctx))
	require.Equal(t, state.Revision, runtime.state.Revision)
	require.NoError(t, runtime.bootstrapIfNeeded(ctx))

	runtime.cfg.InitialSlotCount = 2
	runtime.raft = &controllerraft.Service{}
	require.NoError(t, runtime.controlTick(ctx), "a follower must not run bootstrap planning")
	require.False(t, runtime.isLocalLeader())

	emptySM, err := fsm.New(statefile.New(filepath.Join(t.TempDir(), "cluster-state.json")))
	require.NoError(t, err)
	runtime.sm = emptySM
	runtime.cfg.AllowBootstrap = false
	require.ErrorContains(t, runtime.bootstrapIfNeeded(ctx), "bootstrap disabled")
	require.NoError(t, runtime.controlTick(ctx))

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, runtime.waitLocalLeader(canceled), context.Canceled)
}

func TestRuntimeBootstrapCommandCapturesClusterTopologyAndClock(t *testing.T) {
	now := time.Date(2026, 9, 2, 11, 12, 13, 0, time.FixedZone("east", 8*60*60))
	runtime := &Runtime{cfg: RuntimeConfig{
		ClusterID: "wk-bootstrap", InitialSlotCount: 8, HashSlotCount: 256, ReplicaCount: 3,
		Voters: []Voter{{NodeID: 2, Addr: "n2"}, {NodeID: 1, Addr: "n1"}},
		Now:    func() time.Time { return now },
	}}
	command := runtime.initCommand()
	require.Equal(t, now.UTC(), command.IssuedAt)
	require.Equal(t, "wk-bootstrap", command.Init.ClusterID)
	require.Equal(t, uint32(8), command.Init.Config.SlotCount)
	require.Equal(t, uint16(256), command.Init.Config.HashSlotCount)
	require.Equal(t, uint16(3), command.Init.Config.ReplicaCount)
	require.Equal(t, []ControllerVoter{
		{NodeID: 2, Addr: "n2", Role: ControllerRoleVoter},
		{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter},
	}, command.Init.Controllers)
	require.Equal(t, []controllerraft.Peer{{NodeID: 2, Addr: "n2"}, {NodeID: 1, Addr: "n1"}}, runtime.raftPeers())
}
