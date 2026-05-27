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
	mu       sync.RWMutex
	services map[uint64]*Service
}

func newMemoryRaftTransport() *memoryRaftTransport {
	return &memoryRaftTransport{services: make(map[uint64]*Service)}
}

func (t *memoryRaftTransport) register(nodeID uint64, service *Service) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.services[nodeID] = service
}

func (t *memoryRaftTransport) Send(batch []raftpb.Message) {
	for _, msg := range batch {
		t.mu.RLock()
		service := t.services[msg.To]
		t.mu.RUnlock()
		if service == nil {
			continue
		}
		// The test transport must not let one slow peer stall the sender's Raft loop.
		ctx, cancel := context.WithTimeout(context.Background(), testRaftStepTimeout)
		err := service.Step(ctx, msg)
		cancel()
		if err != nil && !errors.Is(err, ErrNotStarted) && !errors.Is(err, ErrStopped) && !errors.Is(err, context.DeadlineExceeded) {
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
