package raft

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	testRaftTickInterval = 20 * time.Millisecond
	testRaftStepTimeout  = 10 * time.Millisecond
)

func TestNewServiceValidatesConfig(t *testing.T) {
	service, err := NewService(Config{})
	require.Nil(t, service)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestNewServiceRequiresRaftDir(t *testing.T) {
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
	})
	require.Nil(t, service)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "raft dir")
}

func TestStartDuringActiveStopReturnsStopped(t *testing.T) {
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		RaftDir:        filepath.Join(t.TempDir(), "controller-raft"),
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   testRaftTickInterval,
	})
	require.NoError(t, err)

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	service.mu.Lock()
	service.started = true
	service.stopCh = stopCh
	service.doneCh = doneCh
	service.stepCh = make(chan raftpb.Message)
	service.proposal = make(chan proposalRequest)
	service.mu.Unlock()

	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop() }()
	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.stopping
	}, time.Second, 10*time.Millisecond)
	require.ErrorIs(t, service.Start(context.Background()), ErrStopped)
	close(doneCh)
	require.NoError(t, <-stopDone)
	select {
	case <-stopCh:
	default:
		t.Fatal("stop channel was not closed")
	}
}

func TestThreeControllerVotersCommitStateFile(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-state-file", cluster.peers))
	cluster.waitForRevision(t, 1)

	cluster.propose(t, testUpsertNodeCommand(1, 2, "node-2-renamed"))
	states := cluster.waitForRevision(t, 2)
	for _, st := range states {
		require.Equal(t, "node-2-renamed", findTestNode(t, st, 2).Name)
		require.NotZero(t, st.AppliedRaftIndex)
		require.NotEmpty(t, st.Checksum)
	}
}

func TestThreeControllerVotersCommitInitClusterStateToIdenticalChecksum(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-identical", cluster.peers))
	states := cluster.waitForRevision(t, 1)

	checksum := states[0].Checksum
	require.NotEmpty(t, checksum)
	for _, st := range states {
		require.Equal(t, checksum, st.Checksum)
		require.Equal(t, states[0], st)
	}
}

func TestControllerRaftAddsLearnerThenPromotesVoter(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-membership", cluster.peers))
	cluster.waitForRevision(t, 1)

	target := cluster.addNode(t, 4)
	require.NoError(t, target.service.Start(context.Background()))
	leader := cluster.waitForLeader(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	learner, err := leader.service.AddLearner(ctx, 4)
	cancel()
	require.NoError(t, err)
	require.Contains(t, learner.ConfState.Learners, uint64(4))

	require.Eventually(t, func() bool {
		st := target.service.Status()
		return containsUint64ForRaftTest(st.Learners, 4)
	}, 5*time.Second, 10*time.Millisecond)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	voter, err := leader.service.PromoteLearner(ctx, 4)
	cancel()
	require.NoError(t, err)
	require.Contains(t, voter.ConfState.Voters, uint64(4))

	require.Eventually(t, func() bool {
		st := target.service.Status()
		return containsUint64ForRaftTest(st.Voters, 4) && !containsUint64ForRaftTest(st.Learners, 4)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestControllerRaftMembershipStatusReportsVotersAndLearners(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-status-confstate", cluster.peers))
	cluster.waitForRevision(t, 1)

	status := cluster.nodes[0].service.Status()
	require.Equal(t, []uint64{1}, status.Voters)
	require.Empty(t, status.Learners)
}

func TestControllerRaftConcurrentMembershipChangeRejectsPending(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-membership-pending", cluster.peers))
	cluster.waitForRevision(t, 1)

	target := cluster.addNode(t, 4)
	require.NoError(t, target.service.Start(context.Background()))
	leader := cluster.waitForLeader(t)

	held := make(chan raftpb.Message, 32)
	firstAppendHeld := make(chan struct{})
	var firstAppendOnce sync.Once
	cluster.transport.setInterceptor(func(msg raftpb.Message) bool {
		if msg.From == leader.id && msg.Type == raftpb.MsgApp && len(msg.Entries) > 0 {
			firstAppendOnce.Do(func() { close(firstAppendHeld) })
			select {
			case held <- msg:
			default:
			}
			return true
		}
		return false
	})
	defer cluster.transport.setInterceptor(nil)

	firstDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := leader.service.AddLearner(ctx, 4)
		firstDone <- err
	}()

	select {
	case <-firstAppendHeld:
	case err := <-firstDone:
		require.NoError(t, err)
		t.Fatal("first membership change completed before the append was held")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first membership append")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := leader.service.AddLearner(ctx, 5)
	cancel()
	require.ErrorIs(t, err, ErrMembershipChangePending)

	cluster.transport.setInterceptor(nil)
	drainHeldMessages(cluster.transport, held)
	select {
	case err := <-firstDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first membership change")
	}

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = leader.service.ProbePropose(ctx)
	cancel()
	require.NoError(t, err)
}

func containsUint64ForRaftTest(items []uint64, item uint64) bool {
	for _, existing := range items {
		if existing == item {
			return true
		}
	}
	return false
}

func TestThreeControllerVotersRejectFollowerProposal(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	leader := cluster.waitForLeader(t)
	var follower *testRaftNode
	for _, node := range cluster.nodes {
		if node.id != leader.id {
			follower = node
			break
		}
	}
	require.NotNil(t, follower)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := follower.service.Propose(ctx, testInitCommand("wk-raft-follower-reject", cluster.peers))
	require.ErrorIs(t, err, ErrNotLeader)
}

func TestProbeProposeSingleNodeSucceedsWithoutStateMutation(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-probe-single", cluster.peers))
	cluster.waitForRevision(t, 1)

	before := cluster.nodes[0].stateMachine.Snapshot(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, cluster.waitForLeader(t).service.ProbePropose(ctx))
	after := cluster.nodes[0].stateMachine.Snapshot(context.Background())

	require.Equal(t, before, after)
}

func TestProbeProposeFollowerReturnsNotLeader(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	leader := cluster.waitForLeader(t)
	var follower *testRaftNode
	for _, node := range cluster.nodes {
		if node.id != leader.id {
			follower = node
			break
		}
	}
	require.NotNil(t, follower)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.ErrorIs(t, follower.service.ProbePropose(ctx), ErrNotLeader)
}

func TestProbeProposeFindsLeaderInThreeVoters(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)

	require.Eventually(t, func() bool {
		for _, node := range cluster.nodes {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err := node.service.ProbePropose(ctx)
			cancel()
			if err == nil {
				return true
			}
			if !errors.Is(err, ErrNotLeader) {
				t.Logf("ProbePropose(node=%d) error: %v", node.id, err)
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
}

func TestProbeProposeLifecycleErrors(t *testing.T) {
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: true,
		RaftDir:        filepath.Join(t.TempDir(), "controller-raft"),
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   testRaftTickInterval,
	})
	require.NoError(t, err)

	require.ErrorIs(t, service.ProbePropose(context.Background()), ErrNotStarted)
	require.NoError(t, service.Start(context.Background()))
	require.NoError(t, service.Stop())
	require.ErrorIs(t, service.ProbePropose(context.Background()), ErrStopped)
}

func TestServiceCompactLogReturnsSkippedWhenNotStarted(t *testing.T) {
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: true,
		RaftDir:        filepath.Join(t.TempDir(), "controller-raft"),
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   testRaftTickInterval,
	})
	require.NoError(t, err)

	result, err := service.CompactLog(context.Background())

	require.ErrorIs(t, err, ErrNotStarted)
	require.False(t, result.Compacted)
	require.Equal(t, LogCompactionSkipNotStarted, result.SkippedReason)
	st := service.Status()
	require.Equal(t, uint64(1), st.NodeID)
	require.Equal(t, RoleUnknown, st.Role)
	require.Equal(t, LogCompactionSkipNotStarted, st.Compaction.SkippedReason)
}

func TestServiceCompactLogForcesSnapshotBelowAutomaticThreshold(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	node := cluster.nodes[0]
	node.service.cfg.SnapshotCount = 1000
	node.service.cfg.SnapshotCatchUpEntries = 1
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-manual-compact", cluster.peers))
	states := cluster.waitForRevision(t, 1)
	applied := states[0].AppliedRaftIndex
	before, err := node.service.store.Snapshot()
	require.NoError(t, err)

	result, err := node.service.CompactLog(context.Background())

	require.NoError(t, err)
	require.True(t, result.Compacted)
	require.Empty(t, result.SkippedReason)
	require.Equal(t, applied, result.AppliedIndex)
	require.Equal(t, before.Metadata.Index, result.BeforeSnapshotIndex)
	require.GreaterOrEqual(t, result.AfterSnapshotIndex, result.AppliedIndex)
	after, err := node.service.store.Snapshot()
	require.NoError(t, err)
	require.Equal(t, result.AfterSnapshotIndex, after.Metadata.Index)
	status := node.service.Status()
	require.True(t, status.Compaction.Compacted)
	require.Equal(t, result.AfterSnapshotIndex, status.Compaction.AfterSnapshotIndex)
}

func TestServiceStatusReportsCompactionPolicyAndLogWatermarks(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	node := cluster.nodes[0]
	node.service.cfg.SnapshotCount = 1000
	node.service.cfg.SnapshotCatchUpEntries = 1
	node.service.cfg.SnapshotMinInterval = 25 * time.Millisecond
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-status-watermarks", cluster.peers))
	cluster.waitForRevision(t, 1)
	_, err := node.service.CompactLog(context.Background())
	require.NoError(t, err)
	first, err := node.service.store.FirstIndex()
	require.NoError(t, err)
	last, err := node.service.store.LastIndex()
	require.NoError(t, err)
	snap, err := node.service.store.Snapshot()
	require.NoError(t, err)

	status := node.service.Status()

	require.Equal(t, first, status.FirstIndex)
	require.Equal(t, last, status.LastIndex)
	require.Equal(t, snap.Metadata.Index, status.SnapshotIndex)
	require.Equal(t, snap.Metadata.Term, status.SnapshotTerm)
	require.True(t, status.Compaction.Enabled)
	require.Equal(t, uint64(1000), status.Compaction.TriggerEntries)
	require.Equal(t, 25*time.Millisecond, status.Compaction.CheckInterval)
	require.Equal(t, snap.Metadata.Index, status.Compaction.AfterSnapshotIndex)
}

func TestSemanticRejectReturnsProposalErrorAndServiceKeepsRunning(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-semantic-reject", cluster.peers))
	cluster.waitForRevision(t, 1)

	badRevision := uint64(0)
	node := findTestNode(t, cluster.nodes[0].stateMachine.Snapshot(context.Background()), 1)
	node.Name = "bad-revision"
	err := cluster.waitForLeader(t).service.Propose(context.Background(), command.Command{
		Kind:             command.KindUpsertNode,
		ExpectedRevision: &badRevision,
		Node:             &node,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), fsm.ReasonExpectedRevisionMismatch)
	require.False(t, cluster.nodes[0].service.Status().Degraded)

	cluster.propose(t, testUpsertNodeCommand(1, 1, "good-revision"))
	states := cluster.waitForRevision(t, 2)
	require.Equal(t, "good-revision", findTestNode(t, states[0], 1).Name)
}

func TestServiceProposeResultReportsChangedAndStaleNoop(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-propose-result", cluster.peers))
	cluster.waitForRevision(t, 1)
	leader := cluster.waitForLeader(t)

	changed, err := leader.service.ProposeResult(context.Background(), testUpsertNodeCommand(1, 1, "node-1-result"))
	require.NoError(t, err)
	require.True(t, changed.Changed)
	require.False(t, changed.Noop)
	require.False(t, changed.Rejected)
	require.Equal(t, uint64(2), changed.Revision)
	require.NotZero(t, changed.AppliedRaftIndex)

	noop, err := leader.service.ProposeResult(context.Background(), testUpsertNodeCommand(1, 1, "node-1-result"))
	require.NoError(t, err)
	require.False(t, noop.Changed)
	require.True(t, noop.Noop)
	require.False(t, noop.Rejected)
	require.Equal(t, fsm.ReasonNoChange, noop.Reason)
	require.Equal(t, changed.Revision, noop.Revision)
	require.Greater(t, noop.AppliedRaftIndex, changed.AppliedRaftIndex)

	rejected, err := leader.service.ProposeResult(context.Background(), testUpsertNodeCommand(1, 1, "node-1-rejected"))
	require.ErrorIs(t, err, ErrProposalRejected)
	require.True(t, rejected.Rejected)
	require.Equal(t, fsm.ReasonExpectedRevisionMismatch, rejected.Reason)
	require.Equal(t, changed.Revision, rejected.Revision)
	require.Greater(t, rejected.AppliedRaftIndex, noop.AppliedRaftIndex)
}

func TestStartupReplaysWhenStateBehindWAL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "controller-raft")
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-state-behind", peers)
	upsertCmd := testUpsertNodeCommand(1, 1, "node-1-replayed")
	seedControllerRaftStore(t, raftDir, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 2)

	statePath := filepath.Join(dir, "cluster-state.json")
	sm := newTestStateMachine(t, statePath)
	_, err := sm.Apply(ctx, 2, initCmd)
	require.NoError(t, err)

	service := startSingleService(t, 1, peers, raftDir, statePath, false)
	t.Cleanup(func() { require.NoError(t, service.Stop()) })
	snap := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(2), snap.Revision)
	require.Equal(t, uint64(3), snap.AppliedRaftIndex)
	require.Equal(t, "node-1-replayed", findTestNode(t, snap, 1).Name)
}

func TestSlowApplyDoesNotBlockStep(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	blocker := newBlockingBatchStateMachine(cluster.nodes[0].stateMachine)
	cluster.nodes[0].stateMachine = blocker
	cluster.rebuildService(t, cluster.nodes[0])
	cluster.start(t)
	leader := cluster.waitForLeader(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- leader.service.Propose(context.Background(), testInitCommand("wk-slow-apply", cluster.peers))
	}()
	<-blocker.entered

	stepCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.NoError(t, leader.service.Step(stepCtx, raftpb.Message{To: leader.id, Type: raftpb.MsgHeartbeat}))

	blocker.release()
	require.NoError(t, <-resultCh)
}

func TestStepObservesQueueDepthAndEnqueueLatency(t *testing.T) {
	observer := &stepObserver{}
	service := &Service{
		cfg:     Config{NodeID: 1, Observer: observer},
		started: true,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		stepCh:  make(chan raftpb.Message, 2),
	}

	require.NoError(t, service.Step(context.Background(), raftpb.Message{To: 1, Type: raftpb.MsgHeartbeat}))

	require.Equal(t, 1, observer.depth)
	require.Equal(t, 2, observer.capacity)
	require.Equal(t, []string{"ok"}, observer.results)
}

func TestUpdateStatusObservesApplyState(t *testing.T) {
	observer := &stepObserver{}
	cluster := newRaftTestCluster(t, []uint64{1})
	node := cluster.nodes[0]
	node.service.cfg.Observer = observer
	cluster.start(t)
	leader := cluster.waitForLeader(t)

	require.NoError(t, leader.service.Propose(context.Background(), testInitCommand("wk-apply-state", cluster.peers)))
	require.Eventually(t, func() bool {
		observer.mu.Lock()
		defer observer.mu.Unlock()
		return len(observer.applyStates) > 0
	}, 2*time.Second, 10*time.Millisecond)

	observer.mu.Lock()
	defer observer.mu.Unlock()
	last := observer.applyStates[len(observer.applyStates)-1]
	require.GreaterOrEqual(t, last.commit, last.applied)
	require.Greater(t, last.commit, uint64(0))
}

func TestMemoryRaftTransportSendDoesNotBlockWhenPeerStepQueueFull(t *testing.T) {
	transport := newMemoryRaftTransport()
	stopCh := make(chan struct{})
	stopClosed := false
	closeStop := func() {
		if !stopClosed {
			close(stopCh)
			stopClosed = true
		}
	}
	defer closeStop()

	service := &Service{
		cfg:     Config{NodeID: 1},
		started: true,
		stopCh:  stopCh,
		doneCh:  make(chan struct{}),
		stepCh:  make(chan raftpb.Message),
	}
	transport.register(1, service)

	done := make(chan struct{})
	go func() {
		transport.Send([]raftpb.Message{{To: 1, From: 2, Type: raftpb.MsgHeartbeat}})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		closeStop()
		<-done
		t.Fatal("memory raft transport blocked behind a full peer step queue")
	}
}

type testRaftCluster struct {
	peers     []Peer
	nodes     []*testRaftNode
	transport *memoryRaftTransport
}

type testRaftNode struct {
	id           uint64
	dir          string
	raftDir      string
	statePath    string
	stateMachine stateMachine
	service      *Service
}

func newRaftTestCluster(t *testing.T, ids []uint64) *testRaftCluster {
	t.Helper()
	transport := newMemoryRaftTransport()
	peers := make([]Peer, 0, len(ids))
	for _, id := range ids {
		peers = append(peers, Peer{NodeID: id, Addr: fmt.Sprintf("n%d", id)})
	}
	cluster := &testRaftCluster{peers: peers, transport: transport}
	for _, id := range ids {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "cluster-state.json")
		node := &testRaftNode{id: id, dir: dir, raftDir: filepath.Join(dir, "controller-raft"), statePath: statePath, stateMachine: newTestStateMachine(t, statePath)}
		cluster.rebuildService(t, node)
		cluster.nodes = append(cluster.nodes, node)
	}
	t.Cleanup(cluster.stop)
	return cluster
}

func (c *testRaftCluster) addNode(t *testing.T, id uint64) *testRaftNode {
	t.Helper()
	c.peers = append(c.peers, Peer{NodeID: id, Addr: fmt.Sprintf("n%d", id)})
	dir := t.TempDir()
	statePath := filepath.Join(dir, "cluster-state.json")
	node := &testRaftNode{
		id:           id,
		dir:          dir,
		raftDir:      filepath.Join(dir, "controller-raft"),
		statePath:    statePath,
		stateMachine: newTestStateMachine(t, statePath),
	}
	c.rebuildService(t, node)
	c.nodes = append(c.nodes, node)
	return node
}

func (c *testRaftCluster) rebuildService(t *testing.T, node *testRaftNode) {
	t.Helper()
	service, err := NewService(Config{
		NodeID:         node.id,
		Peers:          c.peers,
		AllowBootstrap: true,
		RaftDir:        node.raftDir,
		StateMachine:   node.stateMachine,
		Transport:      c.transport,
		TickInterval:   testRaftTickInterval,
	})
	require.NoError(t, err)
	node.service = service
	c.transport.register(node.id, service)
}

func (c *testRaftCluster) start(t *testing.T) {
	t.Helper()
	for _, node := range c.nodes {
		require.NoError(t, node.service.Start(context.Background()))
	}
	c.waitForLeader(t)
}

func (c *testRaftCluster) stop() {
	for _, node := range c.nodes {
		if node.service != nil {
			_ = node.service.Stop()
		}
	}
}

func (c *testRaftCluster) waitForLeader(t *testing.T) *testRaftNode {
	t.Helper()
	var leader *testRaftNode
	require.Eventually(t, func() bool {
		leader = nil
		for _, node := range c.nodes {
			if node.service.Status().Role == RoleLeader {
				if leader != nil {
					return false
				}
				leader = node
			}
		}
		return leader != nil
	}, 5*time.Second, 10*time.Millisecond)
	return leader
}

func (c *testRaftCluster) propose(t *testing.T, cmd command.Command) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		leader := c.waitForLeader(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := leader.service.Propose(ctx, cmd)
		cancel()
		if err == nil {
			return
		}
		lastErr = err
		if !errors.Is(err, ErrNotLeader) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, lastErr)
}

func (c *testRaftCluster) waitForRevision(t *testing.T, revision uint64) []state.ClusterState {
	t.Helper()
	var states []state.ClusterState
	require.Eventually(t, func() bool {
		states = states[:0]
		for _, node := range c.nodes {
			st := node.stateMachine.Snapshot(context.Background())
			if st.Revision != revision || st.Checksum == "" {
				return false
			}
			persisted, err := statefile.New(node.statePath).Load(context.Background())
			if err != nil || persisted.Revision != revision {
				return false
			}
			states = append(states, st)
		}
		return len(states) == len(c.nodes)
	}, 5*time.Second, 10*time.Millisecond)
	out := make([]state.ClusterState, len(states))
	copy(out, states)
	return out
}

type memoryRaftTransport struct {
	mu          sync.RWMutex
	services    map[uint64]*Service
	interceptor func(raftpb.Message) bool
}

type stepObserver struct {
	mu       sync.Mutex
	depth    int
	capacity int
	results  []string

	applyStates []applyStateSample
}

type applyStateSample struct {
	commit  uint64
	applied uint64
}

func (o *stepObserver) SetStepQueueDepth(depth int, capacity int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.depth = depth
	o.capacity = capacity
}

func (o *stepObserver) ObserveStepEnqueue(result string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.results = append(o.results, result)
}

func (o *stepObserver) SetApplyState(commitIndex, appliedIndex uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.applyStates = append(o.applyStates, applyStateSample{commit: commitIndex, applied: appliedIndex})
}

func newMemoryRaftTransport() *memoryRaftTransport {
	return &memoryRaftTransport{services: make(map[uint64]*Service)}
}

func (t *memoryRaftTransport) register(nodeID uint64, service *Service) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.services[nodeID] = service
}

func (t *memoryRaftTransport) setInterceptor(interceptor func(raftpb.Message) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.interceptor = interceptor
}

func (t *memoryRaftTransport) Send(batch []raftpb.Message) {
	for _, msg := range batch {
		t.mu.RLock()
		interceptor := t.interceptor
		t.mu.RUnlock()
		if interceptor != nil && interceptor(msg) {
			continue
		}
		t.deliver(msg)
	}
}

func (t *memoryRaftTransport) deliver(msg raftpb.Message) {
	t.mu.RLock()
	service := t.services[msg.To]
	t.mu.RUnlock()
	if service == nil {
		return
	}
	// The test transport must not let one slow peer stall the sender's Raft loop.
	ctx, cancel := context.WithTimeout(context.Background(), testRaftStepTimeout)
	err := service.Step(ctx, msg)
	cancel()
	if err != nil && !errors.Is(err, ErrNotStarted) && !errors.Is(err, ErrStopped) && !errors.Is(err, context.DeadlineExceeded) {
		return
	}
}

func drainHeldMessages(transport *memoryRaftTransport, held <-chan raftpb.Message) {
	for {
		select {
		case msg := <-held:
			transport.deliver(msg)
		default:
			return
		}
	}
}

type blockingBatchStateMachine struct {
	stateMachine
	entered   chan struct{}
	releaseCh chan struct{}
	once      sync.Once
}

func newBlockingBatchStateMachine(base stateMachine) *blockingBatchStateMachine {
	return &blockingBatchStateMachine{stateMachine: base, entered: make(chan struct{}), releaseCh: make(chan struct{})}
}

func (s *blockingBatchStateMachine) ApplyBatch(ctx context.Context, entries []fsm.AppliedCommand) (fsm.BatchApplyResult, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.releaseCh:
	case <-ctx.Done():
		return fsm.BatchApplyResult{}, ctx.Err()
	}
	return s.stateMachine.ApplyBatch(ctx, entries)
}

func (s *blockingBatchStateMachine) release() { close(s.releaseCh) }

func newTestStateMachine(t *testing.T, path string) *fsm.StateMachine {
	t.Helper()
	sm, err := fsm.New(statefile.New(path))
	require.NoError(t, err)
	return sm
}

func startSingleService(t *testing.T, id uint64, peers []Peer, raftDir string, statePath string, allowBootstrap bool) *Service {
	t.Helper()
	sm := newTestStateMachine(t, statePath)
	transport := newMemoryRaftTransport()
	service, err := NewService(Config{NodeID: id, Peers: peers, AllowBootstrap: allowBootstrap, RaftDir: raftDir, StateMachine: sm, Transport: transport, TickInterval: testRaftTickInterval})
	require.NoError(t, err)
	transport.register(id, service)
	require.NoError(t, service.Start(context.Background()))
	return service
}

func seedControllerRaftStore(t *testing.T, dir string, entries []raftpb.Entry, commit uint64, applied uint64) {
	t.Helper()
	store, err := raftstore.Open(context.Background(), raftstore.Config{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
	require.NoError(t, err)
	hs := raftpb.HardState{Term: 1, Vote: 1, Commit: commit}
	require.NoError(t, store.SaveReady(context.Background(), hs, entries, raftpb.Snapshot{}))
	if applied > 0 {
		require.NoError(t, store.MarkAppliedBatch(context.Background(), applied))
	}
	require.NoError(t, store.Close())
}

func testConfChangeEntry(t *testing.T, index uint64, nodeID uint64) raftpb.Entry {
	t.Helper()
	cc := raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: nodeID}
	data, err := cc.Marshal()
	require.NoError(t, err)
	return raftpb.Entry{Type: raftpb.EntryConfChange, Term: 1, Index: index, Data: data}
}

func testCommandEntry(t *testing.T, index uint64, cmd command.Command) raftpb.Entry {
	t.Helper()
	data, err := command.Encode(cmd)
	require.NoError(t, err)
	return raftpb.Entry{Type: raftpb.EntryNormal, Term: 1, Index: index, Data: data}
}

func testInitCommand(clusterID string, peers []Peer) command.Command {
	controllers := make([]state.ControllerVoter, 0, len(peers))
	nodes := make([]state.Node, 0, len(peers))
	for _, peer := range peers {
		controllers = append(controllers, state.ControllerVoter{NodeID: peer.NodeID, Addr: peer.Addr, Role: state.ControllerRoleVoter})
		nodes = append(nodes, state.Node{NodeID: peer.NodeID, Name: fmt.Sprintf("n%d", peer.NodeID), Addr: peer.Addr, Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10})
	}
	return command.Command{Kind: command.KindInitClusterState, Init: &command.InitClusterState{ClusterID: clusterID, Config: state.ClusterConfig{SlotCount: 4, HashSlotCount: 16, ReplicaCount: 3, DefaultCapacityWeight: 10}, Controllers: controllers, Nodes: nodes}}
}

func testUpsertNodeCommand(expectedRevision uint64, nodeID uint64, name string) command.Command {
	node := state.Node{NodeID: nodeID, Name: name, Addr: fmt.Sprintf("n%d", nodeID), Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10}
	return command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &expectedRevision, Node: &node}
}

func findTestNode(t *testing.T, st state.ClusterState, nodeID uint64) state.Node {
	t.Helper()
	for _, node := range st.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	t.Fatalf("node %d not found", nodeID)
	return state.Node{}
}
