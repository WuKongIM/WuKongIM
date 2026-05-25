package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	"github.com/stretchr/testify/require"
)

func TestServerReturnsNotModifiedForSameRevisionAndChecksum(t *testing.T) {
	st := testSyncState(1, "wk-sync")
	payload, checksum := encodeSyncState(t, st)
	srv := NewServer(ServerConfig{
		NodeID:    1,
		ClusterID: "wk-sync",
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (state.ClusterState, error) { return st, nil },
	})

	resp, err := srv.GetState(context.Background(), GetStateRequest{
		ClusterID:     "wk-sync",
		LocalRevision: st.Revision,
		LocalChecksum: checksum,
	})

	require.NoError(t, err)
	require.True(t, resp.NotModified)
	require.Equal(t, st.Revision, resp.Revision)
	require.Equal(t, checksum, resp.Checksum)
	require.Empty(t, resp.Payload)
	require.NotEmpty(t, payload)
}

func TestServerReturnsPayloadForSameRevisionDifferentChecksum(t *testing.T) {
	st := testSyncState(1, "wk-sync")
	expectedPayload, checksum := encodeSyncState(t, st)
	srv := NewServer(ServerConfig{
		NodeID:    1,
		ClusterID: "wk-sync",
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (state.ClusterState, error) { return st, nil },
	})

	resp, err := srv.GetState(context.Background(), GetStateRequest{
		ClusterID:     "wk-sync",
		LocalRevision: st.Revision,
		LocalChecksum: "crc32c:00000000",
	})

	require.NoError(t, err)
	require.False(t, resp.NotModified)
	require.Equal(t, st.Revision, resp.Revision)
	require.Equal(t, checksum, resp.Checksum)
	require.JSONEq(t, string(expectedPayload), string(resp.Payload))
}

func TestServerReturnsNotReadyWhenLeaderUnknown(t *testing.T) {
	st := testSyncState(1, "wk-sync")
	srv := NewServer(ServerConfig{
		NodeID:    1,
		ClusterID: "wk-sync",
		LeaderID:  func() uint64 { return 0 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (state.ClusterState, error) { return st, nil },
	})

	resp, err := srv.GetState(context.Background(), GetStateRequest{ClusterID: "wk-sync"})

	require.NoError(t, err)
	require.True(t, resp.NotReady)
	require.Empty(t, resp.Payload)
}

func TestClientInstallsNewerLeaderState(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	leader := testSyncState(2, "wk-sync")
	picker := fakePeerPicker{endpoints: map[uint64]Endpoint{
		1: endpointWithPayload(t, leader),
	}, ids: []uint64{1}}
	client := NewClient(ClientConfig{ClusterID: "wk-sync", Store: store, Peers: picker, LeaderID: 1})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), got.Revision)
	snapshot, ok := client.LocalState()
	require.True(t, ok)
	require.Equal(t, uint64(2), snapshot.Revision)
}

func TestClientInstallsLeaderStateWhenLocalMissing(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	leader := testSyncState(1, "wk-sync")
	picker := fakePeerPicker{endpoints: map[uint64]Endpoint{
		1: endpointWithPayload(t, leader),
	}, ids: []uint64{1}}
	client := NewClient(ClientConfig{ClusterID: "wk-sync", Store: store, Peers: picker, LeaderID: 1})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, leader.Revision, got.Revision)
}

func TestClientRepairsSameRevisionDifferentChecksum(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "wk-sync")
	local.Nodes[2].Status = state.NodeStatusSuspect
	require.NoError(t, store.Save(ctx, local))
	leader := testSyncState(2, "wk-sync")
	picker := fakePeerPicker{endpoints: map[uint64]Endpoint{
		1: endpointWithPayload(t, leader),
	}, ids: []uint64{1}}
	client := NewClient(ClientConfig{ClusterID: "wk-sync", Store: store, Peers: picker, LeaderID: 1})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), got.Revision)
	require.Equal(t, state.NodeStatusAlive, got.Nodes[2].Status)
}

func TestClientRejectsWrongClusterID(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	foreign := testSyncState(2, "other-cluster")
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers:     fakePeerPicker{endpoints: map[uint64]Endpoint{1: endpointWithPayload(t, foreign)}, ids: []uint64{1}},
		LeaderID:  1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), ErrClusterIDMismatch)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
	require.Equal(t, local.ClusterID, got.ClusterID)
}

func TestClientRejectsBadChecksum(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	leader := testSyncState(2, "wk-sync")
	payload, checksum := encodeSyncState(t, leader)
	badPayload := []byte(strings.Replace(string(payload), `"revision":2`, `"revision":3`, 1))
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			Revision: leader.Revision,
			Checksum: checksum,
			Payload:  badPayload,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), state.ErrChecksumMismatch)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientRejectsMissingRevisionHeader(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	leader := testSyncState(2, "wk-sync")
	payload, checksum := encodeSyncState(t, leader)
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			Checksum: checksum,
			Payload:  payload,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), ErrHeaderMismatch)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientRejectsMissingChecksumHeader(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	leader := testSyncState(2, "wk-sync")
	payload, _ := encodeSyncState(t, leader)
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			Revision: leader.Revision,
			Payload:  payload,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), ErrHeaderMismatch)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientContinuesAfterStalePayloadAndInstallsNextPeer(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	stale := testSyncState(1, "wk-sync")
	fresh := testSyncState(3, "wk-sync")
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{
			1: endpointWithPayload(t, stale),
			2: endpointWithPayload(t, fresh),
		}, ids: []uint64{1, 2}},
		LeaderID: 1,
	})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, fresh.Revision, got.Revision)
}

func TestClientRejectsLocalClusterIDMismatchBeforePeerProbe(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "other-cluster")
	require.NoError(t, store.Save(ctx, local))
	peerState := testSyncState(3, "wk-sync")
	peer := endpointWithPayload(t, peerState).(*fakeEndpoint)
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers:     fakePeerPicker{endpoints: map[uint64]Endpoint{1: peer}, ids: []uint64{1}},
		LeaderID:  1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), ErrClusterIDMismatch)
	require.Empty(t, peer.requests)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, "other-cluster", got.ClusterID)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientRejectsLowerRevisionBeforeSave(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	lower := testSyncState(1, "wk-sync")
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers:     fakePeerPicker{endpoints: map[uint64]Endpoint{1: endpointWithPayload(t, lower)}, ids: []uint64{1}},
		LeaderID:  1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), ErrStalePayload)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientTreatsSameRevisionSameChecksumPayloadAsNotModified(t *testing.T) {
	ctx := context.Background()
	saveErr := errors.New("unexpected save")
	path := filepath.Join(t.TempDir(), "cluster-state.json")
	store := statefile.New(path)
	guardedStore := statefile.New(path, statefile.WithAfterTempWriteHook(func() error {
		return saveErr
	}))
	local := testSyncState(2, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	payload, checksum := encodeSyncState(t, local)
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     guardedStore,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			Revision: local.Revision,
			Checksum: checksum,
			Payload:  payload,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
	require.Equal(t, checksum, got.Checksum)
}

func TestClientRejectsNotModifiedMissingHeaders(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			NotModified: true,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.ErrorIs(t, client.SyncOnce(ctx), ErrHeaderMismatch)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientContinuesAfterNotModifiedMismatchedHeaders(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	fresh := testSyncState(3, "wk-sync")
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{
			1: &fakeEndpoint{resp: GetStateResponse{
				NotModified: true,
				Revision:    local.Revision,
				Checksum:    "crc32c:00000000",
			}},
			2: endpointWithPayload(t, fresh),
		}, ids: []uint64{1, 2}},
		LeaderID: 1,
	})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, fresh.Revision, got.Revision)
}

func TestClientConcurrentSnapshotAccessIsRaceFree(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	fresh := testSyncState(2, "wk-sync")
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers:     fakePeerPicker{endpoints: map[uint64]Endpoint{1: endpointWithPayload(t, fresh)}, ids: []uint64{1}},
		LeaderID:  1,
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = client.LocalState()
				_ = client.LeaderID()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.SyncOnce(ctx)
		}()
	}
	wg.Wait()
}

func TestClientConcurrentSyncDoesNotPersistRevisionRegression(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))

	ep := newOrderedConcurrentEndpoint(t, testSyncState(2, "wk-sync"), testSyncState(3, "wk-sync"))
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers:     fakePeerPicker{endpoints: map[uint64]Endpoint{1: ep}, ids: []uint64{1}},
		LeaderID:  1,
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- client.SyncOnce(ctx)
	}()
	<-ep.firstStarted

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- client.SyncOnce(ctx)
	}()

	select {
	case err := <-secondDone:
		require.NoError(t, err)
		// Without operation serialization, rev3 can install before the first call resumes.
		close(ep.releaseFirst)
	case <-time.After(50 * time.Millisecond):
		// With operation serialization, release the first call so the second can run after it.
		close(ep.releaseFirst)
		require.NoError(t, <-secondDone)
	}
	require.NoError(t, <-firstDone)

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), got.Revision)
}

func TestClientKeepsMemorySnapshotWhenLocalCorruptAndLeaderUnavailable(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	memory := testSyncState(2, "wk-sync")
	require.NoError(t, os.WriteFile(store.Path(), []byte("not-json"), 0o600))
	client := NewClient(ClientConfig{
		ClusterID:    "wk-sync",
		Store:        store,
		Peers:        fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{err: errors.New("offline")}}, ids: []uint64{1}},
		LeaderID:     1,
		InitialState: &memory,
	})

	require.Error(t, client.SyncOnce(ctx))

	snapshot, ok := client.LocalState()
	require.True(t, ok)
	require.Equal(t, memory.Revision, snapshot.Revision)
	_, err := store.Load(ctx)
	require.Error(t, err)
}

func TestClientPreservesLocalStateOnStaleLeader(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(3, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			StaleLeader: true,
			LeaderID:    1,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

func TestClientRetriesAdvertisedLeader(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(1, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	leader := testSyncState(2, "wk-sync")
	picker := fakePeerPicker{endpoints: map[uint64]Endpoint{
		1: &fakeEndpoint{resp: GetStateResponse{NotLeader: true, LeaderID: 2}},
		2: endpointWithPayload(t, leader),
	}, ids: []uint64{1, 2}}
	client := NewClient(ClientConfig{ClusterID: "wk-sync", Store: store, Peers: picker, LeaderID: 1})

	require.NoError(t, client.SyncOnce(ctx))

	require.Equal(t, []GetStateRequest{
		{ClusterID: "wk-sync", LocalRevision: 1, LocalChecksum: mustChecksum(t, local)},
	}, picker.endpoints[1].(*fakeEndpoint).requests)
	require.Equal(t, uint64(2), client.LeaderID())
	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, leader.Revision, got.Revision)
}

func TestClientPreservesLocalStateOnNotReady(t *testing.T) {
	ctx := context.Background()
	store := newSyncStore(t)
	local := testSyncState(2, "wk-sync")
	require.NoError(t, store.Save(ctx, local))
	client := NewClient(ClientConfig{
		ClusterID: "wk-sync",
		Store:     store,
		Peers: fakePeerPicker{endpoints: map[uint64]Endpoint{1: &fakeEndpoint{resp: GetStateResponse{
			NotReady: true,
			LeaderID: 1,
		}}}, ids: []uint64{1}},
		LeaderID: 1,
	})

	require.NoError(t, client.SyncOnce(ctx))

	got, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, local.Revision, got.Revision)
}

type fakePeerPicker struct {
	endpoints map[uint64]Endpoint
	ids       []uint64
}

func (p fakePeerPicker) Endpoint(nodeID uint64) (Endpoint, bool) {
	ep, ok := p.endpoints[nodeID]
	return ep, ok
}

func (p fakePeerPicker) PeerIDs() []uint64 {
	return append([]uint64(nil), p.ids...)
}

type fakeEndpoint struct {
	mu       sync.Mutex
	resp     GetStateResponse
	err      error
	requests []GetStateRequest
}

func (e *fakeEndpoint) GetState(_ context.Context, req GetStateRequest) (GetStateResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests = append(e.requests, req)
	return e.resp, e.err
}

func endpointWithPayload(t *testing.T, st state.ClusterState) Endpoint {
	t.Helper()
	payload, checksum := encodeSyncState(t, st)
	return &fakeEndpoint{resp: GetStateResponse{Revision: st.Revision, Checksum: checksum, Payload: payload}}
}

type orderedConcurrentEndpoint struct {
	t            *testing.T
	mu           sync.Mutex
	calls        int
	first        GetStateResponse
	second       GetStateResponse
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func newOrderedConcurrentEndpoint(t *testing.T, first, second state.ClusterState) *orderedConcurrentEndpoint {
	t.Helper()
	firstPayload, firstChecksum := encodeSyncState(t, first)
	secondPayload, secondChecksum := encodeSyncState(t, second)
	return &orderedConcurrentEndpoint{
		t:            t,
		first:        GetStateResponse{Revision: first.Revision, Checksum: firstChecksum, Payload: firstPayload},
		second:       GetStateResponse{Revision: second.Revision, Checksum: secondChecksum, Payload: secondPayload},
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (e *orderedConcurrentEndpoint) GetState(ctx context.Context, req GetStateRequest) (GetStateResponse, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	e.mu.Unlock()

	switch call {
	case 1:
		close(e.firstStarted)
		select {
		case <-e.releaseFirst:
			return e.first, nil
		case <-ctx.Done():
			return GetStateResponse{}, ctx.Err()
		}
	case 2:
		return e.second, nil
	default:
		e.t.Fatalf("unexpected GetState call %d with request %+v", call, req)
		return GetStateResponse{}, nil
	}
}

func newSyncStore(t *testing.T) *statefile.Store {
	t.Helper()
	return statefile.New(filepath.Join(t.TempDir(), "cluster-state.json"))
}

func encodeSyncState(t *testing.T, st state.ClusterState) ([]byte, string) {
	t.Helper()
	payload, err := state.Encode(st)
	require.NoError(t, err)
	decoded, err := state.Decode(payload)
	require.NoError(t, err)
	return payload, decoded.Checksum
}

func mustChecksum(t *testing.T, st state.ClusterState) string {
	t.Helper()
	_, checksum := encodeSyncState(t, st)
	return checksum
}

func testSyncState(revision uint64, clusterID string) state.ClusterState {
	table, err := state.BuildInitialHashSlotTable(1, 16)
	if err != nil {
		panic(err)
	}
	return state.ClusterState{
		SchemaVersion:    state.CurrentSchemaVersion,
		ClusterID:        clusterID,
		Revision:         revision,
		AppliedRaftIndex: revision,
		UpdatedAt:        time.Date(2026, 5, 24, 10, int(revision), 0, 0, time.UTC),
		Config: state.ClusterConfig{
			SlotCount:     1,
			HashSlotCount: 16,
			ReplicaCount:  3,
		},
		Controllers: []state.ControllerVoter{
			{NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter},
			{NodeID: 2, Addr: "n2", Role: state.ControllerRoleVoter},
		},
		Nodes: []state.Node{
			{NodeID: 1, Name: "n1", Addr: "n1", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100},
			{NodeID: 2, Name: "n2", Addr: "n2", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100},
			{NodeID: 3, Name: "n3", Addr: "n3", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100},
		},
		Slots:     []state.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: table,
		Tasks: []state.ReconcileTask{
			{TaskID: "slot-1-bootstrap-1", SlotID: 1, Kind: state.TaskKindBootstrap, Step: state.TaskStepCreateSlot, TargetNode: 1, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, Status: state.TaskStatusPending},
		},
	}
}
