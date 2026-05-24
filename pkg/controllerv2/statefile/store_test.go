package statefile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster-state.json")
	store := New(path)
	require.Equal(t, path, store.Path())

	want := testClusterState()
	require.NoError(t, store.Save(context.Background(), want))

	got, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, want.ClusterID, got.ClusterID)
	require.Equal(t, want.Revision, got.Revision)
	require.Equal(t, want.AppliedRaftIndex, got.AppliedRaftIndex)
	require.NotEmpty(t, got.Checksum)
	require.NoError(t, got.Validate())
}

func TestStoreLoadRejectsChecksumMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster-state.json")
	encoded, err := state.Encode(testClusterState())
	require.NoError(t, err)
	tampered := strings.Replace(string(encoded), `"revision":1`, `"revision":2`, 1)
	require.NoError(t, os.WriteFile(path, []byte(tampered), 0o600))

	_, err = New(path).Load(context.Background())
	require.ErrorIs(t, err, state.ErrChecksumMismatch)
}

func TestStoreLoadIgnoresLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster-state.json")
	store := New(path)
	want := testClusterState()
	require.NoError(t, store.Save(context.Background(), want))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cluster-state.json.tmp"), []byte("not-json"), 0o600))

	got, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, want.Revision, got.Revision)
}

func TestStoreSaveFailureKeepsPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster-state.json")
	ctx := context.Background()
	previous := testClusterState()
	require.NoError(t, New(path).Save(ctx, previous))

	boom := errors.New("boom after temp write")
	next := testClusterState()
	next.Revision = 2
	next.AppliedRaftIndex = 2
	next.UpdatedAt = next.UpdatedAt.Add(time.Minute)
	err := New(path, WithAfterTempWriteHook(func() error { return boom })).Save(ctx, next)
	require.ErrorIs(t, err, boom)

	got, err := New(path).Load(ctx)
	require.NoError(t, err)
	require.Equal(t, previous.Revision, got.Revision)
	require.Equal(t, previous.AppliedRaftIndex, got.AppliedRaftIndex)
}

func testClusterState() state.ClusterState {
	table, err := state.BuildInitialHashSlotTable(1, 16)
	if err != nil {
		panic(err)
	}
	return state.ClusterState{
		SchemaVersion:    state.CurrentSchemaVersion,
		ClusterID:        "wk-statefile-test",
		Revision:         1,
		AppliedRaftIndex: 1,
		UpdatedAt:        time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
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
