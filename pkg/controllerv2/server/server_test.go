package server

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
)

func TestServerTickPlannerProposesBootstrapCommand(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 30, 0, 0, time.UTC)
	proposer := &fakeProposer{leaderID: 1}
	srv, err := New(Config{
		InitialState: testServerState(),
		Proposer:     proposer,
		Now:          func() time.Time { return now },
	})
	require.NoError(t, err)

	err = srv.TickPlanner(context.Background())

	require.NoError(t, err)
	require.Len(t, proposer.commands, 1)
	proposed := proposer.commands[0]
	require.Equal(t, command.KindUpsertSlotAssignmentAndTask, proposed.Kind)
	require.Equal(t, now, proposed.IssuedAt)
	require.NotNil(t, proposed.ExpectedRevision)
	require.Equal(t, uint64(3), *proposed.ExpectedRevision)
	require.NotNil(t, proposed.Assignment)
	require.Equal(t, uint32(1), proposed.Assignment.SlotID)
	require.Equal(t, []uint64{1, 2}, proposed.Assignment.DesiredPeers)
	require.Equal(t, uint64(1), proposed.Assignment.PreferredLeader)
	require.NotNil(t, proposed.Task)
	require.Equal(t, "slot-1-bootstrap-1", proposed.Task.TaskID)
	require.Equal(t, proposed.Assignment.DesiredPeers, proposed.Task.TargetPeers)
}

func TestServerLocalStateReturnsSnapshotCopy(t *testing.T) {
	initial := testServerState()
	initial.Slots = []state.SlotAssignment{{SlotID: 2, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 1}}
	initial.HashSlots = state.HashSlotTable{
		Version:   state.CurrentHashSlotTableVersion,
		SlotCount: 4,
		Ranges:    []state.HashSlotRange{{From: 0, To: 3, SlotID: 2}},
	}
	initial.Tasks = []state.ReconcileTask{{
		TaskID:      "slot-2-bootstrap-1",
		SlotID:      2,
		Kind:        state.TaskKindBootstrap,
		Step:        state.TaskStepCreateSlot,
		TargetNode:  1,
		TargetPeers: []uint64{1, 2},
		ConfigEpoch: 1,
		Status:      state.TaskStatusPending,
	}}
	srv, err := New(Config{InitialState: initial, Proposer: &fakeProposer{}})
	require.NoError(t, err)

	initial.Nodes[0].Roles[0] = "mutated"
	got := srv.LocalState()
	got.Nodes[0].Name = "mutated"
	got.Nodes[0].Roles[0] = "mutated"
	got.Slots[0].DesiredPeers[0] = 99
	got.HashSlots.Ranges[0].SlotID = 99
	got.Tasks[0].TargetPeers[0] = 99

	again := srv.LocalState()
	require.Equal(t, "node-1", again.Nodes[0].Name)
	require.Equal(t, state.NodeRoleData, again.Nodes[0].Roles[0])
	require.Equal(t, []uint64{1, 2}, again.Slots[0].DesiredPeers)
	require.Equal(t, uint32(2), again.HashSlots.Ranges[0].SlotID)
	require.Equal(t, []uint64{1, 2}, again.Tasks[0].TargetPeers)
}

func TestServerSyncOnceDelegatesToSyncClient(t *testing.T) {
	ctx := context.WithValue(context.Background(), fakeContextKey{}, "sync")
	next := testServerState()
	next.Revision = 4
	next.Nodes[0].Name = "synced"
	client := &fakeSyncClient{state: next}
	srv, err := New(Config{
		InitialState: testServerState(),
		Proposer:     &fakeProposer{},
		SyncClient:   client,
	})
	require.NoError(t, err)

	err = srv.SyncOnce(ctx)

	require.NoError(t, err)
	require.Equal(t, 1, client.calls)
	require.Equal(t, "sync", client.contextValue)
	require.Equal(t, uint64(4), srv.LocalState().Revision)
	require.Equal(t, "synced", srv.LocalState().Nodes[0].Name)
	next.Nodes[0].Name = "mutated"
	require.Equal(t, "synced", srv.LocalState().Nodes[0].Name)
}

type fakeProposer struct {
	leaderID uint64
	commands []command.Command
	err      error
}

func (p *fakeProposer) Propose(ctx context.Context, cmd command.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.commands = append(p.commands, cmd)
	return p.err
}

func (p *fakeProposer) LeaderID() uint64 {
	return p.leaderID
}

type fakeSyncClient struct {
	state        state.ClusterState
	err          error
	calls        int
	contextValue any
}

func (c *fakeSyncClient) SyncOnce(ctx context.Context) (state.ClusterState, error) {
	c.calls++
	c.contextValue = ctx.Value(fakeContextKey{})
	return c.state, c.err
}

type fakeContextKey struct{}

func testServerState() state.ClusterState {
	return state.ClusterState{
		SchemaVersion: state.CurrentSchemaVersion,
		ClusterID:     "wk-server-test",
		Revision:      3,
		UpdatedAt:     time.Date(2026, 5, 24, 8, 0, 0, 0, time.UTC),
		Config: state.ClusterConfig{
			SlotCount:             2,
			HashSlotCount:         4,
			ReplicaCount:          2,
			DefaultCapacityWeight: 10,
		},
		Controllers: []state.ControllerVoter{
			{NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter},
		},
		Nodes: []state.Node{
			{NodeID: 1, Name: "node-1", Addr: "n1", Roles: []state.NodeRole{state.NodeRoleData, state.NodeRoleControllerVoter}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
			{NodeID: 2, Name: "node-2", Addr: "n2", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
		},
	}
}
