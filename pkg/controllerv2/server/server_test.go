package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/planner"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
)

func TestServerTickPlannerProposesBootstrapCommand(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 30, 0, 0, time.UTC)
	initial := testServerState()
	proposer := &fakeProposer{leaderID: 1}
	srv, err := New(Config{
		InitialState: initial,
		StateSource:  &fakeStateSource{state: initial},
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

func TestServerTickPlannerCommandRequiresStateSource(t *testing.T) {
	proposer := &fakeProposer{leaderID: 1}
	srv, err := New(Config{
		InitialState: testServerState(),
		Proposer:     proposer,
	})
	require.NoError(t, err)

	err = srv.TickPlanner(context.Background())

	require.ErrorIs(t, err, ErrStateSourceRequired)
	require.Empty(t, proposer.commands)
}

func TestServerTickPlannerRefreshesAuthoritativeStateAfterSuccessfulPropose(t *testing.T) {
	initial := testServerState()
	initial.Config.SlotCount = 1
	source := &fakeStateSource{state: initial}
	proposer := &fakeProposer{
		leaderID: 1,
		onPropose: func(cmd command.Command) {
			applied := initial.Clone()
			applied.Revision++
			applied.Slots = []state.SlotAssignment{*cmd.Assignment}
			applied.Tasks = []state.ReconcileTask{*cmd.Task}
			source.set(applied)
		},
	}
	srv, err := New(Config{
		InitialState: initial,
		StateSource:  source,
		Proposer:     proposer,
	})
	require.NoError(t, err)

	require.NoError(t, srv.TickPlanner(context.Background()))
	require.NoError(t, srv.TickPlanner(context.Background()))

	require.Len(t, proposer.commands, 1)
	require.Equal(t, uint64(4), srv.LocalState().Revision)
	require.Len(t, srv.LocalState().Slots, 1)
}

func TestServerTickPlannerSerializesConcurrentTicks(t *testing.T) {
	pl := newBlockingPlanner()
	srv, err := New(Config{
		InitialState: testServerState(),
		Planner:      pl,
		Proposer:     &fakeProposer{},
	})
	require.NoError(t, err)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- srv.TickPlanner(context.Background())
	}()
	require.Equal(t, 1, pl.waitEnter(t))

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- srv.TickPlanner(context.Background())
	}()
	select {
	case seq := <-pl.entered:
		pl.releaseOne()
		pl.releaseOne()
		t.Fatalf("planner entered concurrently on call %d", seq)
	case <-time.After(50 * time.Millisecond):
	}

	pl.releaseOne()
	require.NoError(t, <-firstDone)
	require.Equal(t, 2, pl.waitEnter(t))
	pl.releaseOne()
	require.NoError(t, <-secondDone)
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
	leaderID  uint64
	commands  []command.Command
	err       error
	onPropose func(command.Command)
}

func (p *fakeProposer) Propose(ctx context.Context, cmd command.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.commands = append(p.commands, cmd)
	if p.onPropose != nil {
		p.onPropose(cmd)
	}
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

type blockingPlanner struct {
	calls   atomic.Int32
	entered chan int
	release chan struct{}
}

func newBlockingPlanner() *blockingPlanner {
	return &blockingPlanner{
		entered: make(chan int, 2),
		release: make(chan struct{}),
	}
}

func (p *blockingPlanner) Next(ctx context.Context, view planner.View) (planner.Decision, error) {
	_ = view
	call := int(p.calls.Add(1))
	p.entered <- call
	select {
	case <-p.release:
		return planner.Decision{Kind: planner.DecisionKindNone}, nil
	case <-ctx.Done():
		return planner.Decision{}, ctx.Err()
	}
}

func (p *blockingPlanner) waitEnter(t *testing.T) int {
	t.Helper()
	select {
	case call := <-p.entered:
		return call
	case <-time.After(time.Second):
		t.Fatal("planner did not enter")
		return 0
	}
}

func (p *blockingPlanner) releaseOne() {
	p.release <- struct{}{}
}

type fakeStateSource struct {
	state state.ClusterState
}

func (s *fakeStateSource) Snapshot(ctx context.Context) state.ClusterState {
	_ = ctx
	return s.state.Clone()
}

func (s *fakeStateSource) set(st state.ClusterState) {
	s.state = st.Clone()
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
