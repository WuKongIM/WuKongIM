package raft

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestServiceBootstrapsOnlyOnSmallestDerivedPeer(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	counts := env.captureBootstrapCalls(t)

	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	require.Equal(t, map[uint64]int{}, counts.snapshot())

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1, 2, 3})

	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())
}

func TestServiceRestartUsesPersistedMembershipInsteadOfConfigDrift(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	counts := env.captureBootstrapCalls(t)

	env.startNode(t, 1, nil)
	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	env.waitForLeader(t, []uint64{1, 2, 3})

	state := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(state.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())

	env.restartNode(t, 1, []Peer{})
	env.waitForLeader(t, []uint64{1, 2, 3})

	restarted := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(restarted.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())
}

func TestServiceRestartTrustsPersistedMembershipWhenConfigOmitsLocalNode(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	counts := env.captureBootstrapCalls(t)

	env.startNode(t, 1, nil)
	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	env.waitForLeader(t, []uint64{1, 2, 3})

	state := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(state.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())

	env.restartNode(t, 1, []Peer{
		{NodeID: 2, Addr: env.addrOf(2)},
		{NodeID: 3, Addr: env.addrOf(3)},
	})
	env.waitForLeader(t, []uint64{1, 2, 3})

	restarted := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(restarted.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())
}

func TestServiceStartReturnsBootstrapFailureWithoutDeadlock(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	sentinel := errors.New("bootstrap failed")
	original := rawNodeBootstrap
	rawNodeBootstrap = func(_ uint64, _ *raft.RawNode, _ []raft.Peer) error {
		return sentinel
	}
	t.Cleanup(func() {
		rawNodeBootstrap = original
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- env.startNodeErr(t, 1, nil)
	}()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, sentinel)
	case <-time.After(2 * time.Second):
		t.Fatal("Start hung on bootstrap failure")
	}
}

func TestServiceProposeReturnsRunLoopErrorAfterExit(t *testing.T) {
	sentinel := errors.New("run loop failed")
	doneCh := make(chan struct{})
	close(doneCh)

	service := &Service{
		started:   true,
		stopCh:    make(chan struct{}),
		doneCh:    doneCh,
		proposeCh: make(chan proposalRequest),
		err:       sentinel,
	}

	err := service.Propose(context.Background(), slotcontroller.Command{})
	require.ErrorIs(t, err, sentinel)
}

func TestServiceHandleMessageDropsMisroutedRaftMessages(t *testing.T) {
	service := &Service{
		cfg:     Config{NodeID: 1},
		started: true,
		stopCh:  make(chan struct{}),
		stepCh:  make(chan raftpb.Message, 4),
	}

	misrouted := []raftpb.Message{
		{From: 1, To: 2, Type: raftpb.MsgHeartbeat},
		{From: 2, To: 2, Type: raftpb.MsgHeartbeat},
		{From: 1, To: 1, Type: raftpb.MsgHeartbeat},
	}
	for _, msg := range misrouted {
		service.handleMessage(mustMarshalRaftMessage(t, msg))
		select {
		case got := <-service.stepCh:
			t.Fatalf("handleMessage stepped misrouted message %+v", got)
		default:
		}
	}

	valid := raftpb.Message{From: 2, To: 1, Type: raftpb.MsgHeartbeat}
	service.handleMessage(mustMarshalRaftMessage(t, valid))
	select {
	case got := <-service.stepCh:
		require.Equal(t, valid, got)
	default:
		t.Fatal("handleMessage dropped valid inbound message")
	}
}

func TestControllerRaftMessageTypeAvoidsClusterTransportTypes(t *testing.T) {
	const (
		clusterRaftMessageType            uint8 = 1
		clusterObservationHintMessageType uint8 = 2
	)

	require.NotContains(t, []uint8{
		clusterRaftMessageType,
		clusterObservationHintMessageType,
	}, msgTypeControllerRaft)
}

func TestRaftTransportSkipsLocalMessages(t *testing.T) {
	rt := &raftTransport{localNodeID: 1}

	err := rt.Send(context.Background(), []raftpb.Message{
		{From: 1, To: 1, Type: raftpb.MsgHeartbeat},
		{From: 2, To: 2, Type: raftpb.MsgHeartbeat},
		{From: 2, To: 1, Type: raftpb.MsgHeartbeat},
	})

	require.NoError(t, err)
}

func TestServiceProposeAppliesMigrationCommand(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	ctx := context.Background()

	require.NoError(t, node.meta.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindStartMigration,
		Migration: &slotcontroller.MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
		},
	}))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindAdvanceMigration,
		Migration: &slotcontroller.MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    uint8(hashslot.PhaseDelta),
		},
	}))

	table, err := node.meta.LoadHashSlotTable(ctx)
	require.NoError(t, err)

	migration := table.GetMigration(3)
	require.NotNil(t, migration)
	require.Equal(t, uint16(3), migration.HashSlot)
	require.Equal(t, multiraft.SlotID(1), migration.Source)
	require.Equal(t, multiraft.SlotID(2), migration.Target)
	require.Equal(t, hashslot.PhaseDelta, migration.Phase)
}

func TestServiceProposeAppliesAddSlotCommand(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	ctx := context.Background()

	require.NoError(t, node.meta.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindAddSlot,
		AddSlot: &slotcontroller.AddSlotRequest{
			NewSlotID: 3,
			Peers:     []uint64{1},
		},
	}))

	assignment, err := node.meta.GetAssignment(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint32(3), assignment.SlotID)
	require.Equal(t, []uint64{1}, assignment.DesiredPeers)

	task, err := node.meta.GetTask(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, controllermeta.TaskKindBootstrap, task.Kind)
	require.Equal(t, controllermeta.TaskStepAddLearner, task.Step)
	require.Equal(t, uint64(1), task.TargetNode)

	table, err := node.meta.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, table.ActiveMigrations())
}

func TestServiceProposeAppliesRemoveSlotCommand(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	ctx := context.Background()

	require.NoError(t, node.meta.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 3)))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindRemoveSlot,
		RemoveSlot: &slotcontroller.RemoveSlotRequest{
			SlotID: 3,
		},
	}))

	table, err := node.meta.LoadHashSlotTable(ctx)
	require.NoError(t, err)

	active := table.ActiveMigrations()
	require.NotEmpty(t, active)
	for _, migration := range active {
		require.Equal(t, multiraft.SlotID(3), migration.Source)
		require.NotEqual(t, multiraft.SlotID(3), migration.Target)
	}
}

func TestControllerRaftServicePublishesCommittedCommandHook(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	var (
		mu        sync.Mutex
		committed []slotcontroller.Command
	)

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.OnCommittedCommand = func(cmd slotcontroller.Command) {
			mu.Lock()
			committed = append(committed, cmd)
			mu.Unlock()
		}
	})
	env.waitForLeader(t, []uint64{1})

	require.NoError(t, env.nodes[1].service.Propose(context.Background(), slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorMarkNodeDraining,
			NodeID: 2,
		},
	}))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(committed) == 1
	}, 2*time.Second, 20*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, slotcontroller.CommandKindOperatorRequest, committed[0].Kind)
	require.NotNil(t, committed[0].Op)
	require.Equal(t, uint64(2), committed[0].Op.NodeID)
}

func TestControllerRaftServicePublishesLeaderChangeHook(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	type leaderChange struct {
		from uint64
		to   uint64
	}

	var (
		mu      sync.Mutex
		changes []leaderChange
	)

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.OnLeaderChange = func(from, to uint64) {
			mu.Lock()
			changes = append(changes, leaderChange{from: from, to: to})
			mu.Unlock()
		}
	})
	env.waitForLeader(t, []uint64{1})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) >= 1
	}, 2*time.Second, 20*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, leaderChange{from: 0, to: 1}, changes[len(changes)-1])
}

func TestEncodeDecodeCommandNodeJoinRoundTrip(t *testing.T) {
	joinedAt := time.Unix(100, 0)
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoin,
		NodeJoin: &slotcontroller.NodeJoinRequest{
			NodeID:         7,
			Name:           "worker-7",
			Addr:           "127.0.0.1:7007",
			CapacityWeight: 5,
			JoinedAt:       joinedAt,
		},
	}

	data, err := encodeCommand(cmd)
	require.NoError(t, err)
	decoded, err := decodeCommand(data)
	require.NoError(t, err)

	require.Equal(t, cmd.Kind, decoded.Kind)
	require.NotNil(t, decoded.NodeJoin)
	require.Equal(t, *cmd.NodeJoin, *decoded.NodeJoin)
}

func TestEncodeDecodeCommandNodeJoinActivateRoundTrip(t *testing.T) {
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoinActivate,
		NodeJoinActivate: &slotcontroller.NodeJoinActivateRequest{
			NodeID:      7,
			ActivatedAt: time.Unix(200, 0),
		},
	}

	data, err := encodeCommand(cmd)
	require.NoError(t, err)
	decoded, err := decodeCommand(data)
	require.NoError(t, err)

	require.Equal(t, cmd.Kind, decoded.Kind)
	require.NotNil(t, decoded.NodeJoinActivate)
	require.Equal(t, *cmd.NodeJoinActivate, *decoded.NodeJoinActivate)
}

func TestEncodeDecodeCommandAssignmentPreservesPreferredLeader(t *testing.T) {
	cooldown := time.Unix(1710000200, 123)
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindAssignmentTaskUpdate,
		Assignment: &controllermeta.SlotAssignment{
			SlotID:                      7,
			DesiredPeers:                []uint64{1, 2, 3},
			PreferredLeader:             2,
			LeaderTransferCooldownUntil: cooldown,
			ConfigEpoch:                 4,
			BalanceVersion:              5,
		},
		Task: &controllermeta.ReconcileTask{
			SlotID:     7,
			Kind:       controllermeta.TaskKindRebalance,
			Step:       controllermeta.TaskStepTransferLeader,
			TargetNode: 2,
			Status:     controllermeta.TaskStatusPending,
		},
	}

	data, err := encodeCommand(cmd)
	require.NoError(t, err)
	decoded, err := decodeCommand(data)
	require.NoError(t, err)

	require.Equal(t, cmd.Kind, decoded.Kind)
	require.NotNil(t, decoded.Assignment)
	require.Equal(t, cmd.Assignment.SlotID, decoded.Assignment.SlotID)
	require.Equal(t, cmd.Assignment.DesiredPeers, decoded.Assignment.DesiredPeers)
	require.Equal(t, cmd.Assignment.PreferredLeader, decoded.Assignment.PreferredLeader)
	require.Equal(t, cmd.Assignment.LeaderTransferCooldownUntil, decoded.Assignment.LeaderTransferCooldownUntil)
	require.Equal(t, cmd.Assignment.ConfigEpoch, decoded.Assignment.ConfigEpoch)
	require.Equal(t, cmd.Assignment.BalanceVersion, decoded.Assignment.BalanceVersion)
	require.NotNil(t, decoded.Task)
	require.Equal(t, cmd.Task.Kind, decoded.Task.Kind)
}

func TestEncodeDecodeCommandRoundTripsNodeOnboardingJobUpdate(t *testing.T) {
	expected := controllermeta.OnboardingJobStatusPlanned
	job, assignment, task := sampleRaftOnboardingUpdate()
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &slotcontroller.NodeOnboardingJobUpdate{
			Job:            &job,
			ExpectedStatus: &expected,
			Assignment:     &assignment,
			Task:           &task,
		},
	}

	data, err := encodeCommand(cmd)
	require.NoError(t, err)
	decoded, err := decodeCommand(data)
	require.NoError(t, err)

	require.Equal(t, cmd.Kind, decoded.Kind)
	require.NotNil(t, decoded.NodeOnboarding)
	require.NotNil(t, decoded.NodeOnboarding.Job)
	require.Equal(t, job.JobID, decoded.NodeOnboarding.Job.JobID)
	require.Equal(t, job.Plan.BlockedReasons[0].Scope, decoded.NodeOnboarding.Job.Plan.BlockedReasons[0].Scope)
	require.Equal(t, job.Moves[0].DesiredPeersAfter, decoded.NodeOnboarding.Job.Moves[0].DesiredPeersAfter)
	require.Equal(t, expected, *decoded.NodeOnboarding.ExpectedStatus)
	require.Equal(t, assignment, *decoded.NodeOnboarding.Assignment)
	require.Equal(t, task.Kind, decoded.NodeOnboarding.Task.Kind)
}

func TestFailInflightProposalsOnLeaderLossFailsQueuedAndIndexed(t *testing.T) {
	queuedResp := make(chan error, 1)
	indexedResp := make(chan error, 1)
	queue := []trackedProposal{{resp: queuedResp}}
	byIndex := map[uint64]trackedProposal{
		7: {resp: indexedResp},
	}

	failInflightProposalsOnLeaderLoss(raft.StateFollower, &queue, byIndex)

	require.Empty(t, queue)
	require.Empty(t, byIndex)
	require.ErrorIs(t, <-queuedResp, ErrNotLeader)
	require.ErrorIs(t, <-indexedResp, ErrNotLeader)
}

func TestFailInflightProposalsOnLeaderLossLeavesLeaderRequestsIntact(t *testing.T) {
	resp := make(chan error, 1)
	queue := []trackedProposal{{resp: resp}}
	byIndex := map[uint64]trackedProposal{}

	failInflightProposalsOnLeaderLoss(raft.StateLeader, &queue, byIndex)

	require.Len(t, queue, 1)
	require.Empty(t, byIndex)
	select {
	case err := <-resp:
		t.Fatalf("unexpected inflight failure: %v", err)
	default:
	}
}

func sampleRaftOnboardingUpdate() (controllermeta.NodeOnboardingJob, controllermeta.SlotAssignment, controllermeta.ReconcileTask) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	task := controllermeta.ReconcileTask{
		SlotID:     2,
		Kind:       controllermeta.TaskKindRebalance,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 1,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
	}
	job := controllermeta.NodeOnboardingJob{
		JobID:           "onboard-20260426-000001",
		TargetNodeID:    4,
		Status:          controllermeta.OnboardingJobStatusRunning,
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Second),
		StartedAt:       now.Add(time.Minute),
		PlanVersion:     1,
		PlanFingerprint: "fingerprint",
		Plan: controllermeta.NodeOnboardingPlan{
			TargetNodeID: 4,
			Summary: controllermeta.NodeOnboardingPlanSummary{
				PlannedTargetSlotCount: 1,
				PlannedLeaderGain:      1,
			},
			Moves: []controllermeta.NodeOnboardingPlanMove{{
				SlotID:                 2,
				SourceNodeID:           1,
				TargetNodeID:           4,
				Reason:                 "replica_balance",
				DesiredPeersBefore:     []uint64{1, 2, 3},
				DesiredPeersAfter:      []uint64{2, 3, 4},
				CurrentLeaderID:        1,
				LeaderTransferRequired: true,
			}},
			BlockedReasons: []controllermeta.NodeOnboardingBlockedReason{{
				Code:    "slot_task_running",
				Scope:   "slot",
				SlotID:  2,
				Message: "slot has running reconcile task",
			}},
		},
		Moves: []controllermeta.NodeOnboardingMove{{
			SlotID:                 2,
			SourceNodeID:           1,
			TargetNodeID:           4,
			Status:                 controllermeta.OnboardingMoveStatusRunning,
			TaskKind:               controllermeta.TaskKindRebalance,
			TaskSlotID:             2,
			StartedAt:              now.Add(time.Minute),
			DesiredPeersBefore:     []uint64{1, 2, 3},
			DesiredPeersAfter:      []uint64{2, 3, 4},
			LeaderBefore:           1,
			LeaderTransferRequired: true,
		}},
		CurrentMoveIndex: 0,
		ResultCounts: controllermeta.OnboardingResultCounts{
			Running: 1,
		},
		CurrentTask: &task,
	}
	assignment := controllermeta.SlotAssignment{
		SlotID:                      2,
		DesiredPeers:                []uint64{2, 3, 4},
		PreferredLeader:             3,
		LeaderTransferCooldownUntil: now.Add(5 * time.Minute),
		ConfigEpoch:                 4,
		BalanceVersion:              8,
	}
	return job, assignment, task
}

type testEnv struct {
	root     string
	addrs    map[uint64]string
	allPeers []Peer
	nodes    map[uint64]*testNode
}

type testNode struct {
	id       uint64
	dir      string
	addr     string
	server   *transport.Server
	rpcMux   *transport.RPCMux
	pool     *transport.Pool
	logDB    *raftstorage.DB
	meta     *controllermeta.Store
	sm       *slotcontroller.StateMachine
	service  *Service
	allPeers []Peer
}

type bootstrapCounts struct {
	mu     sync.Mutex
	counts map[uint64]int
}

func newTestEnv(t *testing.T, nodeIDs []uint64) *testEnv {
	t.Helper()

	root := t.TempDir()
	addrs := reserveNodeAddrs(t, len(nodeIDs))
	peers := make([]Peer, 0, len(nodeIDs))
	nodes := make(map[uint64]*testNode, len(nodeIDs))
	addrMap := make(map[uint64]string, len(nodeIDs))

	for i, nodeID := range nodeIDs {
		addrMap[nodeID] = addrs[i]
		peers = append(peers, Peer{
			NodeID: nodeID,
			Addr:   addrs[i],
		})
	}

	for _, nodeID := range nodeIDs {
		nodes[nodeID] = &testNode{
			id:       nodeID,
			dir:      filepath.Join(root, fmt.Sprintf("n%d", nodeID)),
			addr:     addrMap[nodeID],
			allPeers: append([]Peer(nil), peers...),
		}
	}

	return &testEnv{
		root:     root,
		addrs:    addrMap,
		allPeers: peers,
		nodes:    nodes,
	}
}

func (e *testEnv) startNode(t *testing.T, nodeID uint64, peers []Peer) {
	t.Helper()
	require.NoError(t, e.startNodeErr(t, nodeID, peers))
}

func (e *testEnv) startNodeWithConfig(t *testing.T, nodeID uint64, peers []Peer, mutate func(*Config)) {
	t.Helper()
	require.NoError(t, e.startNodeErrWithConfig(t, nodeID, peers, mutate))
}

func (e *testEnv) startNodeErr(t *testing.T, nodeID uint64, peers []Peer) error {
	return e.startNodeErrWithConfig(t, nodeID, peers, nil)
}

func (e *testEnv) startNodeErrWithConfig(t *testing.T, nodeID uint64, peers []Peer, mutate func(*Config)) error {
	t.Helper()
	node := e.nodes[nodeID]
	require.NotNil(t, node)
	require.Nil(t, node.service)

	if peers == nil {
		peers = node.allPeers
	}
	require.NoError(t, os.MkdirAll(node.dir, 0o755))

	node.server = transport.NewServer()
	node.rpcMux = transport.NewRPCMux()
	node.server.HandleRPCMux(node.rpcMux)
	require.NoError(t, node.server.Start(node.addr))

	discoveryPeers := make(testDiscovery, len(node.allPeers))
	for _, peer := range node.allPeers {
		discoveryPeers[peer.NodeID] = peer.Addr
	}
	node.pool = transport.NewPool(discoveryPeers, 2, 5*time.Second)

	var err error
	node.logDB, err = raftstorage.Open(filepath.Join(node.dir, "controller-raft"))
	require.NoError(t, err)
	node.meta, err = controllermeta.Open(filepath.Join(node.dir, "controller-meta"))
	require.NoError(t, err)
	node.sm = slotcontroller.NewStateMachine(node.meta, slotcontroller.StateMachineConfig{})

	cfg := Config{
		NodeID:         nodeID,
		Peers:          append([]Peer(nil), peers...),
		AllowBootstrap: true,
		LogDB:          node.logDB,
		StateMachine:   node.sm,
		Server:         node.server,
		RPCMux:         node.rpcMux,
		Pool:           node.pool,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	node.service = &Service{cfg: cfg}
	if err := node.service.Start(context.Background()); err != nil {
		e.stopNode(nodeID)
		return err
	}
	return nil
}

func (e *testEnv) restartNode(t *testing.T, nodeID uint64, peers []Peer) {
	t.Helper()
	e.stopNode(nodeID)
	e.startNode(t, nodeID, peers)
}

type testDiscovery map[uint64]string

func (d testDiscovery) Resolve(nodeID uint64) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", fmt.Errorf("node %d not found", nodeID)
	}
	return addr, nil
}

func (e *testEnv) stopAll() {
	for _, nodeID := range sortedPeersFromMap(e.nodes) {
		e.stopNode(nodeID)
	}
}

func (e *testEnv) stopNode(nodeID uint64) {
	node := e.nodes[nodeID]
	if node == nil {
		return
	}
	if node.service != nil {
		_ = node.service.Stop()
		node.service = nil
	}
	if node.pool != nil {
		node.pool.Close()
		node.pool = nil
	}
	if node.server != nil {
		node.server.Stop()
		node.server = nil
	}
	if node.logDB != nil {
		_ = node.logDB.Close()
		node.logDB = nil
	}
	if node.meta != nil {
		_ = node.meta.Close()
		node.meta = nil
	}
	node.rpcMux = nil
	node.sm = nil
}

func (e *testEnv) waitForLeader(t *testing.T, nodeIDs []uint64) uint64 {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var leader uint64
		allAgree := true
		for _, nodeID := range nodeIDs {
			node := e.nodes[nodeID]
			if node == nil || node.service == nil {
				allAgree = false
				break
			}
			got := node.service.LeaderID()
			if got == 0 {
				allAgree = false
				break
			}
			if leader == 0 {
				leader = got
				continue
			}
			if leader != got {
				allAgree = false
				break
			}
		}
		if allAgree && leader != 0 {
			return leader
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("leader was not observed for nodes %v", nodeIDs)
	return 0
}

func (e *testEnv) mustInitialState(t *testing.T, nodeID uint64) multiraft.BootstrapState {
	t.Helper()
	node := e.nodes[nodeID]
	require.NotNil(t, node)
	require.NotNil(t, node.logDB)

	state, err := node.logDB.ForController().InitialState(context.Background())
	require.NoError(t, err)
	return state
}

func (e *testEnv) addrOf(nodeID uint64) string {
	return e.addrs[nodeID]
}

func (e *testEnv) captureBootstrapCalls(t *testing.T) *bootstrapCounts {
	t.Helper()

	counts := &bootstrapCounts{counts: make(map[uint64]int)}
	original := rawNodeBootstrap
	rawNodeBootstrap = func(nodeID uint64, rawNode *raft.RawNode, peers []raft.Peer) error {
		counts.record(nodeID)
		return original(nodeID, rawNode, peers)
	}
	t.Cleanup(func() {
		rawNodeBootstrap = original
	})
	return counts
}

func (c *bootstrapCounts) record(nodeID uint64) {
	c.mu.Lock()
	c.counts[nodeID]++
	c.mu.Unlock()
}

func (c *bootstrapCounts) snapshot() map[uint64]int {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[uint64]int, len(c.counts))
	for nodeID, count := range c.counts {
		out[nodeID] = count
	}
	return out
}

func mustMarshalRaftMessage(t *testing.T, msg raftpb.Message) []byte {
	t.Helper()
	body, err := msg.Marshal()
	require.NoError(t, err)
	return body
}

func sortedPeers(peers []uint64) []uint64 {
	out := append([]uint64(nil), peers...)
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}

func sortedPeersFromMap(nodes map[uint64]*testNode) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for nodeID := range nodes {
		out = append(out, nodeID)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}

func reserveNodeAddrs(t *testing.T, n int) []string {
	t.Helper()

	listeners := make([]net.Listener, 0, n)
	addrs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners = append(listeners, ln)
		addrs = append(addrs, ln.Addr().String())
	}
	for _, ln := range listeners {
		require.NoError(t, ln.Close())
	}
	return addrs
}
