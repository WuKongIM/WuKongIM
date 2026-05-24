package raft

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestNewServiceValidatesConfig(t *testing.T) {
	service, err := NewService(Config{})
	require.Nil(t, service)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestConcurrentStartStatusStepRaceShape(t *testing.T) {
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		Storage:        errorInitialStateStorage{err: errors.New("startup fails")},
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   time.Millisecond,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = service.Start(context.Background())
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = service.Status()
				_ = service.Step(context.Background(), raftpb.Message{To: 1})
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentStartSingleFlightAndStopWaitsForStartup(t *testing.T) {
	dir := t.TempDir()
	base := openControllerStorage(t, dir)
	storage := newBlockingInitialStateStorage(base)
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		Storage:        storage,
		StateMachine:   newTestStateMachine(t, filepath.Join(dir, "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   5 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Stop() })
	t.Cleanup(storage.release)

	startResults := make(chan error, 2)
	go func() { startResults <- service.Start(context.Background()) }()
	<-storage.entered
	go func() { startResults <- service.Start(context.Background()) }()

	require.Never(t, func() bool {
		return storage.initialStateCalls.Load() > 1
	}, 100*time.Millisecond, 10*time.Millisecond)

	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop() }()
	require.Never(t, func() bool {
		select {
		case <-stopDone:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond)

	storage.release()
	for i := 0; i < 2; i++ {
		err := <-startResults
		if err != nil {
			require.ErrorIs(t, err, ErrStopped)
		}
	}
	require.NoError(t, <-stopDone)
}

func TestStartDuringActiveStopReturnsStopped(t *testing.T) {
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		Storage:        raftlog.NewMemory(),
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   5 * time.Millisecond,
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

func TestSendErrorDoesNotDegradeOrStopService(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.transport.failNextSends(errors.New("transient send failure"), 1)
	cluster.start(t)
	require.Equal(t, int32(1), cluster.transport.failedSendCount.Load())

	cluster.propose(t, testInitCommand("wk-send-error-nonfatal", cluster.peers))
	cluster.waitForRevision(t, 1)
	for _, node := range cluster.nodes {
		status := node.service.Status()
		require.False(t, status.Degraded, status.ErrorReason)
	}
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

func TestThreeControllerVotersCommitStateFile(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)

	cluster.propose(t, testInitCommand("wk-raft-state-file", cluster.peers))
	cluster.waitForRevision(t, 1)

	expectedRevision := uint64(1)
	cluster.propose(t, testUpsertNodeCommand(expectedRevision, 2, "node-2-renamed"))
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

func TestMarkAppliedFailureDegradesServiceAndStopsApplyingLaterEntries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-mark-applied-fail", peers)
	upsertCmd := testUpsertNodeCommand(1, 1, "should-not-apply")
	seedControllerLog(t, base, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 1)

	store := statefile.New(filepath.Join(dir, "cluster-state.json"))
	sm, err := fsm.New(store)
	require.NoError(t, err)
	_, err = sm.Apply(ctx, 2, initCmd)
	require.NoError(t, err)

	boom := errors.New("mark applied boom")
	storage := &markAppliedFailStorage{Storage: base, failIndex: 2, err: boom}
	transport := newMemoryRaftTransport()
	service, err := NewService(Config{
		NodeID:       1,
		Peers:        peers,
		Storage:      storage,
		StateMachine: sm,
		Transport:    transport,
		TickInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
	transport.register(1, service)
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() { require.NoError(t, service.Stop()) })

	require.Eventually(t, func() bool {
		status := service.Status()
		return status.Degraded && status.ErrorReason != ""
	}, time.Second, 10*time.Millisecond)
	require.True(t, storage.failed())

	snap := sm.Snapshot(ctx)
	require.Equal(t, uint64(1), snap.Revision)
	require.Equal(t, uint64(2), snap.AppliedRaftIndex)
	require.Equal(t, "n1", findTestNode(t, snap, 1).Name)
}

func TestRestartAfterStateFileSaveBeforeMarkAppliedReplaysIdempotently(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	boom := errors.New("mark applied once")
	failStorage := &markAppliedFailStorage{Storage: cluster.nodes[0].storage, failIndex: ^uint64(0), err: boom}
	cluster.nodes[0].storage = failStorage
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          cluster.peers,
		AllowBootstrap: true,
		Storage:        failStorage,
		StateMachine:   cluster.nodes[0].stateMachine,
		Transport:      cluster.transport,
		TickInterval:   5 * time.Millisecond,
	})
	require.NoError(t, err)
	cluster.nodes[0].service = service
	cluster.transport.register(1, cluster.nodes[0].service)
	require.NoError(t, cluster.nodes[0].service.Start(context.Background()))
	t.Cleanup(cluster.stop)
	cluster.waitForLeader(t)
	var nextIndex uint64
	require.Eventually(t, func() bool {
		boot, err := failStorage.Storage.InitialState(context.Background())
		if err != nil {
			return false
		}
		last, err := failStorage.Storage.LastIndex(context.Background())
		if err != nil || last == 0 || boot.AppliedIndex < last {
			return false
		}
		nextIndex = last + 1
		return true
	}, time.Second, 10*time.Millisecond)

	failStorage.failIndex = nextIndex
	cmd := testInitCommand("wk-replay-after-mark-fail", cluster.peers)
	err = cluster.nodes[0].service.Propose(context.Background(), cmd)
	require.ErrorIs(t, err, boom)
	require.True(t, failStorage.failed())
	before := cluster.nodes[0].stateMachine.Snapshot(context.Background())
	require.Equal(t, uint64(1), before.Revision)
	require.NotZero(t, before.AppliedRaftIndex)

	require.NoError(t, cluster.nodes[0].service.Stop())
	healthyStorage := failStorage.Storage
	cluster.nodes[0].storage = healthyStorage
	cluster.nodes[0].stateMachine = newTestStateMachine(t, cluster.nodes[0].statePath)
	service, err = NewService(Config{
		NodeID:         1,
		Peers:          cluster.peers,
		AllowBootstrap: true,
		Storage:        healthyStorage,
		StateMachine:   cluster.nodes[0].stateMachine,
		Transport:      cluster.transport,
		TickInterval:   5 * time.Millisecond,
	})
	require.NoError(t, err)
	cluster.nodes[0].service = service
	cluster.transport.register(1, cluster.nodes[0].service)
	require.NoError(t, cluster.nodes[0].service.Start(context.Background()))
	cluster.waitForLeader(t)

	require.Eventually(t, func() bool {
		boot, err := healthyStorage.InitialState(context.Background())
		if err != nil {
			return false
		}
		return boot.AppliedIndex >= before.AppliedRaftIndex
	}, time.Second, 10*time.Millisecond)
	after := cluster.nodes[0].stateMachine.Snapshot(context.Background())
	require.Equal(t, before.ClusterID, after.ClusterID)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Config, after.Config)
	require.Equal(t, before.Controllers, after.Controllers)
	require.Equal(t, before.Nodes, after.Nodes)
	require.Equal(t, before.Slots, after.Slots)
	require.Equal(t, before.HashSlots, after.HashSlots)
	require.Equal(t, before.Tasks, after.Tasks)
	require.GreaterOrEqual(t, after.AppliedRaftIndex, before.AppliedRaftIndex)
}

func TestStartupReplaysWhenStateBehindRaftlog(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-state-behind", peers)
	upsertCmd := testUpsertNodeCommand(1, 1, "node-1-replayed")
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 3)

	statePath := filepath.Join(dir, "cluster-state.json")
	sm := newTestStateMachine(t, statePath)
	_, err := sm.Apply(ctx, 2, initCmd)
	require.NoError(t, err)

	service := startSingleService(t, 1, peers, storage, statePath, false)
	t.Cleanup(func() { require.NoError(t, service.Stop()) })

	snap := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(2), snap.Revision)
	require.Equal(t, uint64(3), snap.AppliedRaftIndex)
	require.Equal(t, "node-1-replayed", findTestNode(t, snap, 1).Name)
}

func TestStartupReplaysStateBehindNonCommandEntriesToRaftApplied(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}}
	initCmd := testInitCommand("wk-state-behind-non-command", peers)
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		{Type: raftpb.EntryNormal, Term: 1, Index: 3},
		testConfChangeEntry(t, 4, 2),
	}, 4, 4)

	statePath := filepath.Join(dir, "cluster-state.json")
	sm := newTestStateMachine(t, statePath)
	_, err := sm.Apply(ctx, 2, initCmd)
	require.NoError(t, err)

	service := startSingleService(t, 1, peers, storage, statePath, false)
	t.Cleanup(func() { require.NoError(t, service.Stop()) })

	snap := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(1), snap.Revision)
	require.Equal(t, uint64(4), snap.AppliedRaftIndex)
	require.Equal(t, "n1", findTestNode(t, snap, 1).Name)
}

func TestStartupRebuildsMissingStateFromCompleteLog(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-rebuild-missing", peers)
	upsertCmd := testUpsertNodeCommand(1, 1, "node-1-rebuilt")
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 3)

	statePath := filepath.Join(dir, "cluster-state.json")
	require.NoFileExists(t, statePath)
	service := startSingleService(t, 1, peers, storage, statePath, false)
	t.Cleanup(func() { require.NoError(t, service.Stop()) })

	snap := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(2), snap.Revision)
	require.Equal(t, uint64(3), snap.AppliedRaftIndex)
	require.Equal(t, "node-1-rebuilt", findTestNode(t, snap, 1).Name)
	persisted, err := statefile.New(statePath).Load(ctx)
	require.NoError(t, err)
	require.Equal(t, snap, persisted)
}

func TestStartupAllowsMissingStateWithOnlyPreInitCommittedEntries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	rejected := testUpsertNodeCommand(1, 1, "pre-init-rejected")
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		{Type: raftpb.EntryNormal, Term: 1, Index: 2},
		testCommandEntry(t, 3, rejected),
	}, 3, 0)

	statePath := filepath.Join(dir, "cluster-state.json")
	require.NoFileExists(t, statePath)
	service := startSingleService(t, 1, peers, storage, statePath, true)
	t.Cleanup(func() { require.NoError(t, service.Stop()) })
	require.Zero(t, service.cfg.StateMachine.Snapshot(ctx).Revision)
	boot, err := storage.InitialState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), boot.AppliedIndex)

	require.Eventually(t, func() bool {
		return service.Status().Role == RoleLeader
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, service.Propose(ctx, testInitCommand("wk-pre-init-history", peers)))

	snap := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(1), snap.Revision)
	require.Equal(t, "wk-pre-init-history", snap.ClusterID)
	require.Greater(t, snap.AppliedRaftIndex, uint64(3))
	persisted, err := statefile.New(statePath).Load(ctx)
	require.NoError(t, err)
	require.Equal(t, snap, persisted)
}

func TestStartupRebuildAfterPartialPrefixMarkAppliedUsesCommitTarget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-partial-rebuild", peers)
	upsertCmd := testUpsertNodeCommand(1, 1, "rebuilt-after-prefix")
	seedControllerLog(t, base, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 0)
	statePath := filepath.Join(dir, "cluster-state.json")

	partialStorage := &tailUnavailableStorage{Storage: base, maxIndex: 1}
	partial, err := newSingleService(1, peers, partialStorage, statePath, false)
	require.NoError(t, err)
	require.Error(t, partial.Start(ctx))
	boot, err := base.InitialState(ctx)
	require.NoError(t, err)
	require.Zero(t, boot.AppliedIndex)
	require.NoFileExists(t, statePath)

	recovered := startSingleService(t, 1, peers, base, statePath, false)
	t.Cleanup(func() { require.NoError(t, recovered.Stop()) })
	snap := recovered.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(2), snap.Revision)
	require.Equal(t, uint64(3), snap.AppliedRaftIndex)
	require.Equal(t, "rebuilt-after-prefix", findTestNode(t, snap, 1).Name)
	boot, err = base.InitialState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), boot.AppliedIndex)
}

func TestStartupRebuildsCorruptStateWithWarmMemoryDurably(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-warm-corrupt-rebuild", peers)
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
	}, 2, 0)

	statePath := filepath.Join(dir, "cluster-state.json")
	sm := newTestStateMachine(t, statePath)
	_, err := sm.Apply(ctx, 2, initCmd)
	require.NoError(t, err)
	require.Equal(t, uint64(1), sm.Snapshot(ctx).Revision)
	require.NoError(t, os.WriteFile(statePath, []byte(`{"corrupt":true}`), 0o600))

	transport := newMemoryRaftTransport()
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: true,
		Storage:        storage,
		StateMachine:   sm,
		Transport:      transport,
		TickInterval:   5 * time.Millisecond,
	})
	require.NoError(t, err)
	transport.register(1, service)
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() { require.NoError(t, service.Stop()) })

	persisted, err := statefile.New(statePath).Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), persisted.Revision)
	require.Equal(t, uint64(2), persisted.AppliedRaftIndex)
	require.Equal(t, persisted, sm.Snapshot(ctx))
}

func TestStartupFailsWhenMissingStateHasCompactedPostInitTail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	seedControllerSnapshot(t, storage, 5, []uint64{1})
	hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 6}
	require.NoError(t, storage.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   []raftpb.Entry{testCommandEntry(t, 6, testUpsertNodeCommand(1, 1, "post-init-tail"))},
	}))

	statePath := filepath.Join(dir, "cluster-state.json")
	require.NoFileExists(t, statePath)
	service, err := newSingleService(1, peers, storage, statePath, false)
	require.NoError(t, err)
	require.Error(t, service.Start(ctx))
	status := service.Status()
	require.True(t, status.Degraded)
	require.NotEmpty(t, status.ErrorReason)
	require.Zero(t, service.cfg.StateMachine.Snapshot(ctx).Revision)
}

func TestStartupAllowsRejectedInitBeforeAnyValidState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	invalidInit := testInitCommand("wk-invalid-init", peers)
	invalidInit.Init.Config.SlotCount = 0
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, invalidInit),
		{Type: raftpb.EntryNormal, Term: 1, Index: 3},
	}, 3, 0)

	statePath := filepath.Join(dir, "cluster-state.json")
	service := startSingleService(t, 1, peers, storage, statePath, true)
	t.Cleanup(func() { require.NoError(t, service.Stop()) })
	require.Zero(t, service.cfg.StateMachine.Snapshot(ctx).Revision)
	boot, err := storage.InitialState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), boot.AppliedIndex)

	require.Eventually(t, func() bool {
		return service.Status().Role == RoleLeader
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, service.Propose(ctx, testInitCommand("wk-after-rejected-init", peers)))

	snap := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(1), snap.Revision)
	require.Equal(t, "wk-after-rejected-init", snap.ClusterID)
	require.Greater(t, snap.AppliedRaftIndex, uint64(3))
}

func TestStartupFailsDegradedWhenStateMissingAndLogCompacted(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	seedControllerSnapshot(t, storage, 5, []uint64{1})

	statePath := filepath.Join(dir, "cluster-state.json")
	require.NoFileExists(t, statePath)
	service, err := newSingleService(1, []Peer{{NodeID: 1, Addr: "n1"}}, storage, statePath, false)
	require.NoError(t, err)
	require.Error(t, service.Start(ctx))
	status := service.Status()
	require.True(t, status.Degraded)
	require.NotEmpty(t, status.ErrorReason)
}

func TestStartupFailsDegradedWhenRequiredEntriesUnavailable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCmd := testInitCommand("wk-missing-required", peers)
	upsertCmd := testUpsertNodeCommand(1, 1, "node-1-hidden")
	seedControllerLog(t, base, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 2, initCmd),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 3)

	statePath := filepath.Join(dir, "cluster-state.json")
	sm := newTestStateMachine(t, statePath)
	_, err := sm.Apply(ctx, 2, initCmd)
	require.NoError(t, err)

	storage := &missingEntryStorage{Storage: base, missing: map[uint64]bool{3: true}}
	service, err := newSingleService(1, peers, storage, statePath, false)
	require.NoError(t, err)
	require.Error(t, service.Start(ctx))
	status := service.Status()
	require.True(t, status.Degraded)
	require.NotEmpty(t, status.ErrorReason)
}

func TestStartupRejectsCorruptStateWithoutCompleteHistory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage := openControllerStorage(t, dir)
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	upsertCmd := testUpsertNodeCommand(1, 1, "node-1-gap")
	seedControllerLog(t, storage, []raftpb.Entry{
		testConfChangeEntry(t, 1, 1),
		testCommandEntry(t, 3, upsertCmd),
	}, 3, 3)

	statePath := filepath.Join(dir, "cluster-state.json")
	require.NoError(t, os.WriteFile(statePath, []byte(`{"corrupt":true}`), 0o600))
	service, err := newSingleService(1, peers, storage, statePath, false)
	require.NoError(t, err)
	require.Error(t, service.Start(ctx))
	status := service.Status()
	require.True(t, status.Degraded)
	require.NotEmpty(t, status.ErrorReason)
}

type testRaftCluster struct {
	peers     []Peer
	nodes     []*testRaftNode
	transport *memoryRaftTransport
}

type testRaftNode struct {
	id           uint64
	dir          string
	statePath    string
	db           *raftlog.DB
	storage      multiraft.Storage
	stateMachine *fsm.StateMachine
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
		db, err := raftlog.Open(filepath.Join(dir, "raft"), raftlog.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		statePath := filepath.Join(dir, "cluster-state.json")
		sm := newTestStateMachine(t, statePath)
		storage := db.ForController()
		service, err := NewService(Config{
			NodeID:         id,
			Peers:          peers,
			AllowBootstrap: true,
			Storage:        storage,
			StateMachine:   sm,
			Transport:      transport,
			TickInterval:   5 * time.Millisecond,
		})
		require.NoError(t, err)
		node := &testRaftNode{
			id:           id,
			dir:          dir,
			statePath:    statePath,
			db:           db,
			storage:      storage,
			stateMachine: sm,
			service:      service,
		}
		cluster.nodes = append(cluster.nodes, node)
		transport.register(id, service)
	}
	t.Cleanup(cluster.stop)
	return cluster
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
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
	mu               sync.RWMutex
	services         map[uint64]*Service
	sendErr          error
	sendErrRemaining int
	failedSendCount  atomic.Int32
}

func newMemoryRaftTransport() *memoryRaftTransport {
	return &memoryRaftTransport{services: make(map[uint64]*Service)}
}

func (t *memoryRaftTransport) register(nodeID uint64, service *Service) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.services[nodeID] = service
}

func (t *memoryRaftTransport) failNextSends(err error, count int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendErr = err
	t.sendErrRemaining = count
}

func (t *memoryRaftTransport) Send(ctx context.Context, batch []raftpb.Message) error {
	for _, msg := range batch {
		t.mu.RLock()
		service := t.services[msg.To]
		t.mu.RUnlock()
		if service == nil {
			continue
		}
		err := service.Step(ctx, msg)
		if err != nil && !errors.Is(err, ErrNotStarted) && !errors.Is(err, ErrStopped) {
			return err
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sendErrRemaining > 0 {
		t.sendErrRemaining--
		t.failedSendCount.Add(1)
		return t.sendErr
	}
	return nil
}

type markAppliedFailStorage struct {
	multiraft.Storage
	mu        sync.Mutex
	failIndex uint64
	err       error
	didFail   bool
}

func (s *markAppliedFailStorage) MarkApplied(ctx context.Context, index uint64) error {
	s.mu.Lock()
	if !s.didFail && (s.failIndex == 0 || s.failIndex == index) {
		s.didFail = true
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.Storage.MarkApplied(ctx, index)
}

func (s *markAppliedFailStorage) failed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.didFail
}

type missingEntryStorage struct {
	multiraft.Storage
	missing map[uint64]bool
}

func (s *missingEntryStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	entries, err := s.Storage.Entries(ctx, lo, hi, maxSize)
	if err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if s.missing[entry.Index] {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered, nil
}

type errorInitialStateStorage struct {
	multiraft.Storage
	err error
}

func (s errorInitialStateStorage) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	_ = ctx
	return multiraft.BootstrapState{}, s.err
}

type tailUnavailableStorage struct {
	multiraft.Storage
	maxIndex uint64
}

func (s *tailUnavailableStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	if hi > s.maxIndex+1 {
		hi = s.maxIndex + 1
	}
	return s.Storage.Entries(ctx, lo, hi, maxSize)
}

type blockingInitialStateStorage struct {
	multiraft.Storage
	entered           chan struct{}
	releaseCh         chan struct{}
	releaseOnce       sync.Once
	initialStateCalls atomic.Int32
}

func newBlockingInitialStateStorage(storage multiraft.Storage) *blockingInitialStateStorage {
	return &blockingInitialStateStorage{
		Storage:   storage,
		entered:   make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
}

func (s *blockingInitialStateStorage) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	call := s.initialStateCalls.Add(1)
	if call == 1 {
		close(s.entered)
		select {
		case <-s.releaseCh:
		case <-ctx.Done():
			return multiraft.BootstrapState{}, ctx.Err()
		}
	}
	return s.Storage.InitialState(ctx)
}

func (s *blockingInitialStateStorage) release() {
	s.releaseOnce.Do(func() { close(s.releaseCh) })
}

func openControllerStorage(t *testing.T, dir string) multiraft.Storage {
	t.Helper()
	db, err := raftlog.Open(filepath.Join(dir, "raft"), raftlog.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db.ForController()
}

func newTestStateMachine(t *testing.T, path string) *fsm.StateMachine {
	t.Helper()
	sm, err := fsm.New(statefile.New(path))
	require.NoError(t, err)
	return sm
}

func newSingleService(id uint64, peers []Peer, storage multiraft.Storage, statePath string, allowBootstrap bool) (*Service, error) {
	sm, err := fsm.New(statefile.New(statePath))
	if err != nil {
		return nil, err
	}
	transport := newMemoryRaftTransport()
	service, err := NewService(Config{
		NodeID:         id,
		Peers:          peers,
		AllowBootstrap: allowBootstrap,
		Storage:        storage,
		StateMachine:   sm,
		Transport:      transport,
		TickInterval:   5 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	transport.register(id, service)
	return service, nil
}

func startSingleService(t *testing.T, id uint64, peers []Peer, storage multiraft.Storage, statePath string, allowBootstrap bool) *Service {
	t.Helper()
	service, err := newSingleService(id, peers, storage, statePath, allowBootstrap)
	require.NoError(t, err)
	require.NoError(t, service.Start(context.Background()))
	return service
}

func seedControllerLog(t *testing.T, storage multiraft.Storage, entries []raftpb.Entry, commit uint64, applied uint64) {
	t.Helper()
	hs := raftpb.HardState{Term: 1, Vote: 1, Commit: commit}
	require.NoError(t, storage.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Entries:   entries,
	}))
	if applied > 0 {
		require.NoError(t, storage.MarkApplied(context.Background(), applied))
	}
}

func seedControllerSnapshot(t *testing.T, storage multiraft.Storage, index uint64, voters []uint64) {
	t.Helper()
	hs := raftpb.HardState{Term: 1, Vote: voters[0], Commit: index}
	snap := raftpb.Snapshot{
		Data: []byte("compacted-controller-history"),
		Metadata: raftpb.SnapshotMetadata{
			Index:     index,
			Term:      1,
			ConfState: raftpb.ConfState{Voters: append([]uint64(nil), voters...)},
		},
	}
	require.NoError(t, storage.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
	}))
	require.NoError(t, storage.MarkApplied(context.Background(), index))
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
		controllers = append(controllers, state.ControllerVoter{
			NodeID: peer.NodeID,
			Addr:   peer.Addr,
			Role:   state.ControllerRoleVoter,
		})
		nodes = append(nodes, state.Node{
			NodeID:         peer.NodeID,
			Name:           fmt.Sprintf("n%d", peer.NodeID),
			Addr:           peer.Addr,
			Roles:          []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData},
			JoinState:      state.NodeJoinStateActive,
			Status:         state.NodeStatusAlive,
			CapacityWeight: 10,
		})
	}
	replicaCount := uint16(len(peers))
	if replicaCount == 0 {
		replicaCount = 1
	}
	return command.Command{
		Kind:     command.KindInitClusterState,
		IssuedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		Init: &command.InitClusterState{
			ClusterID: clusterID,
			Config: state.ClusterConfig{
				SlotCount:             4,
				HashSlotCount:         16,
				ReplicaCount:          replicaCount,
				DefaultCapacityWeight: 10,
			},
			Controllers: controllers,
			Nodes:       nodes,
		},
	}
}

func testUpsertNodeCommand(expectedRevision uint64, nodeID uint64, name string) command.Command {
	node := state.Node{
		NodeID:         nodeID,
		Name:           name,
		Addr:           fmt.Sprintf("n%d", nodeID),
		Roles:          []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData},
		JoinState:      state.NodeJoinStateActive,
		Status:         state.NodeStatusAlive,
		CapacityWeight: 10,
	}
	return command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         time.Date(2026, 5, 24, 10, 1, 0, 0, time.UTC),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}
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
