package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
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

func TestServiceStartRestoresSnapshotAndReplaysPostSnapshotEntries(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	t.Cleanup(env.stopAll)

	node := env.nodes[1]
	require.NoError(t, os.MkdirAll(node.dir, 0o755))

	snapStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "snapshot-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapStore.Close()) })
	require.NoError(t, snapStore.UpsertNode(context.Background(), controllermeta.ClusterNode{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, JoinedAt: time.Unix(1, 0), LastHeartbeatAt: time.Unix(1, 0), CapacityWeight: 1}))
	snapData, err := snapStore.ExportSnapshot(context.Background())
	require.NoError(t, err)

	entryData, err := encodeCommand(slotcontroller.Command{
		Kind:     slotcontroller.CommandKindNodeJoin,
		NodeJoin: &slotcontroller.NodeJoinRequest{NodeID: 3, Addr: "127.0.0.1:7003", JoinedAt: time.Unix(2, 0), CapacityWeight: 1},
	})
	require.NoError(t, err)

	logDB, err := raftstorage.Open(filepath.Join(node.dir, "controller-raft"))
	require.NoError(t, err)
	snap := raftpb.Snapshot{Data: snapData, Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 2}
	require.NoError(t, logDB.ForController().Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
		Entries:   []raftpb.Entry{{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: entryData}},
	}))
	require.NoError(t, logDB.ForController().MarkApplied(context.Background(), 2))
	require.NoError(t, logDB.Close())

	env.startNode(t, 1, nil)
	require.Eventually(t, func() bool {
		_, err := env.nodes[1].meta.GetNode(context.Background(), 2)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		_, err := env.nodes[1].meta.GetNode(context.Background(), 3)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestServiceIncomingReadySnapshotRestoresControllerMeta(t *testing.T) {
	ctx := context.Background()

	sourceStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "source-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sourceStore.Close()) })
	require.NoError(t, sourceStore.UpsertNode(ctx, controllermeta.ClusterNode{
		NodeID:          9,
		Addr:            "127.0.0.1:7009",
		Status:          controllermeta.NodeStatusAlive,
		JoinedAt:        time.Unix(9, 0),
		LastHeartbeatAt: time.Unix(9, 0),
		CapacityWeight:  1,
	}))
	snapData, err := sourceStore.ExportSnapshot(ctx)
	require.NoError(t, err)

	targetStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "target-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, targetStore.Close()) })
	targetSM := slotcontroller.NewStateMachine(targetStore, slotcontroller.StateMachineConfig{})

	logDB, err := raftstorage.Open(filepath.Join(t.TempDir(), "controller-raft"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logDB.Close()) })
	storage := logDB.ForController()
	storageView := newStorageAdapter(storage)
	_, _, _, err = storageView.load(ctx)
	require.NoError(t, err)

	service := NewService(Config{NodeID: 1, StateMachine: targetSM})
	snap := raftpb.Snapshot{
		Data: snapData,
		Metadata: raftpb.SnapshotMetadata{
			Index:     5,
			Term:      2,
			ConfState: raftpb.ConfState{Voters: []uint64{1, 2}},
		},
	}

	entryData, err := encodeCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoin,
		NodeJoin: &slotcontroller.NodeJoinRequest{
			NodeID:         10,
			Addr:           "127.0.0.1:7010",
			JoinedAt:       time.Unix(10, 0),
			CapacityWeight: 1,
		},
	})
	require.NoError(t, err)
	entry := raftpb.Entry{Index: 6, Term: 2, Type: raftpb.EntryNormal, Data: entryData}
	ready := raft.Ready{Snapshot: snap, CommittedEntries: []raftpb.Entry{entry}}
	require.NoError(t, storageView.persistReady(ctx, ready))

	latestConfState := raftpb.ConfState{Voters: []uint64{7}}
	applied, err := service.applyReadyState(ctx, nil, ready, storageView, &latestConfState, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(6), applied)

	_, err = targetStore.GetNode(ctx, 9)
	require.NoError(t, err)
	_, err = targetStore.GetNode(ctx, 10)
	require.NoError(t, err)

	state, err := storage.InitialState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(6), state.AppliedIndex)
	require.Equal(t, []uint64{1, 2}, latestConfState.Voters)
}

func TestServiceStatusBeforeStartAndAfterStartReportsNodeAndRole(t *testing.T) {
	service := NewService(Config{NodeID: 1})
	before := service.Status()
	require.Equal(t, uint64(1), before.NodeID)
	require.Equal(t, RoleUnknown, before.Role)
	require.True(t, before.Compaction.Enabled)

	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()
	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	require.Eventually(t, func() bool {
		st := node.service.Status()
		return st.Role == RoleLeader && st.LeaderID == 1 && st.Term > 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestServiceStopDuringStartupPreventsLateStart(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	t.Cleanup(env.stopAll)

	entered := make(chan struct{})
	release := make(chan struct{})
	original := currentRawNodeBootstrap()
	setRawNodeBootstrapForTest(func(_ uint64, _ *raft.RawNode, _ []raft.Peer) error {
		close(entered)
		<-release
		return nil
	})
	t.Cleanup(func() {
		setRawNodeBootstrapForTest(original)
		closeIfOpen(release)
	})

	startErr := make(chan error, 1)
	go func() {
		startErr <- env.startNodeErr(t, 1, nil)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("bootstrap hook was not reached")
	}
	service := env.nodes[1].service

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- service.Stop()
	}()
	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.stopRequested
	}, 2*time.Second, 10*time.Millisecond)

	close(release)

	require.ErrorIs(t, <-startErr, ErrStopped)
	require.NoError(t, <-stopDone)
	require.False(t, service.Status().Role == RoleLeader)
	require.Equal(t, uint64(0), service.LeaderID())
}

func TestServiceConcurrentStartWaitsForStartupFailure(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	t.Cleanup(env.stopAll)
	waitCh := observeLifecycleWaits(t)

	sentinel := errors.New("bootstrap failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	original := currentRawNodeBootstrap()
	setRawNodeBootstrapForTest(func(_ uint64, _ *raft.RawNode, _ []raft.Peer) error {
		close(entered)
		<-release
		return sentinel
	})
	t.Cleanup(func() {
		setRawNodeBootstrapForTest(original)
		closeIfOpen(release)
	})

	firstErr := make(chan error, 1)
	go func() {
		firstErr <- env.startNodeErr(t, 1, nil)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("bootstrap hook was not reached")
	}

	secondErr := make(chan error, 1)
	go func() {
		secondErr <- env.nodes[1].service.Start(context.Background())
	}()
	requireLifecycleWait(t, waitCh, "start")

	close(release)
	require.ErrorIs(t, <-firstErr, sentinel)
	require.ErrorIs(t, <-secondErr, sentinel)
}

func TestServiceStartWaitsForStopToFinish(t *testing.T) {
	service := NewService(Config{NodeID: 1})
	waitCh := observeLifecycleWaits(t)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	service.mu.Lock()
	service.started = true
	service.stopCh = stopCh
	service.doneCh = doneCh
	service.mu.Unlock()

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- service.Stop()
	}()

	select {
	case <-stopCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not begin stopping the service")
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- service.Start(context.Background())
	}()
	requireLifecycleWait(t, waitCh, "stop")

	close(doneCh)
	require.NoError(t, <-stopDone)
	require.ErrorIs(t, <-startDone, ErrInvalidConfig)
}

func TestServiceRunErrorClearsStartedLifecycle(t *testing.T) {
	service := NewService(Config{NodeID: 1})
	sentinel := errors.New("run failed")
	service.mu.Lock()
	service.started = true
	service.stopCh = make(chan struct{})
	service.doneCh = make(chan struct{})
	service.stepCh = make(chan raftpb.Message)
	service.proposeCh = make(chan proposalRequest)
	service.mu.Unlock()

	service.setError(sentinel)

	service.mu.Lock()
	started := service.started
	stepCh := service.stepCh
	proposeCh := service.proposeCh
	err := service.err
	service.mu.Unlock()
	require.False(t, started)
	require.Nil(t, stepCh)
	require.Nil(t, proposeCh)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, RoleUnknown, service.Status().Role)
	require.Equal(t, uint64(0), service.LeaderID())
}

func TestServiceStatusAfterStopClearsVolatileRaftState(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})
	require.Equal(t, RoleLeader, env.nodes[1].service.Status().Role)
	sentinel := errors.New("preserve diagnostic")
	env.nodes[1].service.recordCompactionFailure(sentinel, time.Unix(100, 0))
	env.nodes[1].service.recordRestoreFailure(raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{Index: 9, Term: 3},
	}, sentinel)

	require.NoError(t, env.nodes[1].service.Stop())
	st := env.nodes[1].service.Status()
	require.Equal(t, RoleUnknown, st.Role)
	require.Equal(t, uint64(0), st.LeaderID)
	require.Equal(t, uint64(0), st.Term)
	require.Empty(t, st.Peers)
	require.True(t, st.Compaction.Degraded)
	require.Equal(t, sentinel.Error(), st.Compaction.LastError)
	require.True(t, st.Restore.Failed)
	require.Equal(t, uint64(9), st.Restore.LastSnapshotIndex)
}

func TestServiceStatusRecordsStartupSnapshotRestore(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	t.Cleanup(env.stopAll)

	node := env.nodes[1]
	require.NoError(t, os.MkdirAll(node.dir, 0o755))

	snapStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "snapshot-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapStore.Close()) })
	require.NoError(t, snapStore.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          2,
		Addr:            "127.0.0.1:7002",
		Status:          controllermeta.NodeStatusAlive,
		JoinedAt:        time.Unix(1, 0),
		LastHeartbeatAt: time.Unix(1, 0),
		CapacityWeight:  1,
	}))
	snapData, err := snapStore.ExportSnapshot(context.Background())
	require.NoError(t, err)

	logDB, err := raftstorage.Open(filepath.Join(node.dir, "controller-raft"))
	require.NoError(t, err)
	snap := raftpb.Snapshot{Data: snapData, Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	require.NoError(t, logDB.ForController().Save(context.Background(), multiraft.PersistentState{Snapshot: &snap}))
	require.NoError(t, logDB.Close())

	env.startNode(t, 1, nil)
	st := env.nodes[1].service.Status()
	require.Equal(t, uint64(5), st.Restore.LastSnapshotIndex)
	require.Equal(t, uint64(2), st.Restore.LastSnapshotTerm)
	require.False(t, st.Restore.LastRestoredAt.IsZero())
	require.False(t, st.Restore.Failed)
	require.Empty(t, st.Restore.LastError)
}

func TestServiceStatusRecordsStartupSnapshotRestoreFailure(t *testing.T) {
	ctx := context.Background()
	meta, err := controllermeta.Open(filepath.Join(t.TempDir(), "meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, meta.Close()) })
	logDB, err := raftstorage.Open(filepath.Join(t.TempDir(), "raft"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logDB.Close()) })

	snap := raftpb.Snapshot{Data: []byte("not-a-controller-snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 7, Term: 3, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	require.NoError(t, logDB.ForController().Save(ctx, multiraft.PersistentState{Snapshot: &snap}))

	pool := transport.NewPool(testDiscovery{1: "127.0.0.1:1"}, 1, time.Second)
	t.Cleanup(pool.Close)
	service := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "127.0.0.1:1"}},
		AllowBootstrap: true,
		LogDB:          logDB,
		StateMachine:   slotcontroller.NewStateMachine(meta, slotcontroller.StateMachineConfig{}),
		Server:         transport.NewServer(),
		RPCMux:         transport.NewRPCMux(),
		Pool:           pool,
	})

	require.Error(t, service.Start(ctx))
	st := service.Status()
	require.True(t, st.Restore.Failed)
	require.Equal(t, uint64(7), st.Restore.LastSnapshotIndex)
	require.Equal(t, uint64(3), st.Restore.LastSnapshotTerm)
	require.NotEmpty(t, st.Restore.LastError)
	require.False(t, st.Restore.LastErrorAt.IsZero())
}

func TestServiceStatusRecordsReadySnapshotRestore(t *testing.T) {
	ctx := context.Background()

	sourceStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "source-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sourceStore.Close()) })
	require.NoError(t, sourceStore.UpsertNode(ctx, controllermeta.ClusterNode{NodeID: 9, Addr: "127.0.0.1:7009", Status: controllermeta.NodeStatusAlive, JoinedAt: time.Unix(9, 0), LastHeartbeatAt: time.Unix(9, 0), CapacityWeight: 1}))
	snapData, err := sourceStore.ExportSnapshot(ctx)
	require.NoError(t, err)

	targetStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "target-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, targetStore.Close()) })
	service := NewService(Config{NodeID: 1, StateMachine: slotcontroller.NewStateMachine(targetStore, slotcontroller.StateMachineConfig{})})
	latestConfState := raftpb.ConfState{Voters: []uint64{7}}
	snap := raftpb.Snapshot{Data: snapData, Metadata: raftpb.SnapshotMetadata{Index: 11, Term: 4, ConfState: raftpb.ConfState{Voters: []uint64{1, 2}}}}

	applied, err := service.restoreReadySnapshot(ctx, snap, &latestConfState)
	require.NoError(t, err)
	require.Equal(t, uint64(11), applied)
	st := service.Status()
	require.Equal(t, uint64(11), st.Restore.LastSnapshotIndex)
	require.Equal(t, uint64(4), st.Restore.LastSnapshotTerm)
	require.False(t, st.Restore.LastRestoredAt.IsZero())
	require.False(t, st.Restore.Failed)
	require.Empty(t, st.Restore.LastError)
}

func TestServiceStatusRecordsReadySnapshotRestoreFailure(t *testing.T) {
	ctx := context.Background()
	targetStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "target-meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, targetStore.Close()) })
	service := NewService(Config{NodeID: 1, StateMachine: slotcontroller.NewStateMachine(targetStore, slotcontroller.StateMachineConfig{})})
	latestConfState := raftpb.ConfState{Voters: []uint64{7}}
	snap := raftpb.Snapshot{Data: []byte("not-a-controller-snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 12, Term: 5, ConfState: raftpb.ConfState{Voters: []uint64{1, 2}}}}

	_, err = service.restoreReadySnapshot(ctx, snap, &latestConfState)
	require.Error(t, err)
	st := service.Status()
	require.True(t, st.Restore.Failed)
	require.Equal(t, uint64(12), st.Restore.LastSnapshotIndex)
	require.Equal(t, uint64(5), st.Restore.LastSnapshotTerm)
	require.NotEmpty(t, st.Restore.LastError)
	require.False(t, st.Restore.LastErrorAt.IsZero())
}

func TestServiceStartReturnsBootstrapFailureWithoutDeadlock(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	sentinel := errors.New("bootstrap failed")
	original := currentRawNodeBootstrap()
	setRawNodeBootstrapForTest(func(_ uint64, _ *raft.RawNode, _ []raft.Peer) error {
		return sentinel
	})
	t.Cleanup(func() {
		setRawNodeBootstrapForTest(original)
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

func TestServiceCompactsControllerLogAfterAppliedThreshold(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))

	snap := waitForControllerSnapshotIndex(t, node.logDB.ForController(), 2)
	first, err := node.logDB.ForController().FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, snap.Metadata.Index+1, first)
}

func TestServiceControllerLogCompactionCanBeDisabled(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        false,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))

	require.Never(t, func() bool {
		snap, err := node.logDB.ForController().Snapshot(context.Background())
		return err == nil && snap.Metadata.Index > 0
	}, 300*time.Millisecond, 10*time.Millisecond)

	first, err := node.logDB.ForController().FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), first)

	last, err := node.logDB.ForController().LastIndex(context.Background())
	require.NoError(t, err)
	entries, err := node.logDB.ForController().Entries(context.Background(), first, last+1, 0)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}

func TestServiceCompactionFailureDoesNotFailProposalLoop(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	sentinel := errors.New("compact once")
	var hookCalls atomic.Uint32
	setCompactControllerLogHookForTest(func() error {
		hookCalls.Add(1)
		return sentinel
	})
	t.Cleanup(func() {
		setCompactControllerLogHookForTest(nil)
	})

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
	require.Eventually(t, func() bool {
		return hookCalls.Load() > 0
	}, 2*time.Second, 10*time.Millisecond)

	setCompactControllerLogHookForTest(nil)
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(4)))

	waitForControllerSnapshotIndex(t, node.logDB.ForController(), 4)
}

func TestServiceStatusRecordsCompactionFailureAndClearsOnLaterSuccess(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	sentinel := errors.New("compact once")
	setCompactControllerLogHookForTest(func() error { return sentinel })
	t.Cleanup(func() { setCompactControllerLogHookForTest(nil) })

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.Eventually(t, func() bool {
		st := node.service.Status()
		return st.Compaction.Degraded &&
			st.Compaction.LastError == sentinel.Error() &&
			!st.Compaction.LastErrorAt.IsZero()
	}, 2*time.Second, 10*time.Millisecond)

	setCompactControllerLogHookForTest(nil)
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
	require.Eventually(t, func() bool {
		st := node.service.Status()
		return !st.Compaction.Degraded &&
			st.Compaction.LastError == "" &&
			st.Compaction.LastSnapshotIndex >= 3 &&
			!st.Compaction.LastSnapshotAt.IsZero()
	}, 2*time.Second, 10*time.Millisecond)
}

func TestServiceManualCompactionForcesSnapshotBelowAutomaticThreshold(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1000,
			CheckInterval:  time.Hour,
		}
	})
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	store := node.logDB.ForController()
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.Eventually(t, func() bool {
		state, err := store.InitialState(context.Background())
		return err == nil && state.AppliedIndex >= 2
	}, 2*time.Second, 10*time.Millisecond)

	firstBefore, err := store.FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), firstBefore)

	result, err := node.service.CompactLog(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.NodeID)
	require.True(t, result.Compacted)
	require.Empty(t, result.SkippedReason)
	require.Zero(t, result.BeforeSnapshotIndex)
	require.GreaterOrEqual(t, result.AppliedIndex, uint64(2))
	require.Equal(t, result.AppliedIndex, result.AfterSnapshotIndex)

	snap := waitForControllerSnapshotIndex(t, store, result.AppliedIndex)
	require.Equal(t, result.AfterSnapshotIndex, snap.Metadata.Index)
	firstAfter, err := store.FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, result.AfterSnapshotIndex+1, firstAfter)
}

func TestServiceManualCompactionSkipsWhenDisabled(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        false,
			EnabledSet:     true,
			TriggerEntries: 1000,
			CheckInterval:  time.Hour,
		}
	})
	env.waitForLeader(t, []uint64{1})

	result, err := env.nodes[1].service.CompactLog(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.NodeID)
	require.False(t, result.Compacted)
	require.Equal(t, LogCompactionSkippedDisabled, result.SkippedReason)
}

func TestServiceRestartRestoresSnapshotCreatedByCompaction(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	compactionCfg := func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 2,
			CheckInterval:  time.Nanosecond,
		}
	}
	env.startNodeWithConfig(t, 1, nil, compactionCfg)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	store := node.logDB.ForController()
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	snap2 := waitForControllerSnapshotIndex(t, store, 2)
	require.Equal(t, uint64(2), snap2.Metadata.Index)

	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
	require.Eventually(t, func() bool {
		state, err := store.InitialState(context.Background())
		return err == nil && state.AppliedIndex >= 3
	}, 5*time.Second, 10*time.Millisecond)

	snapAfterNode3, err := store.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(2), snapAfterNode3.Metadata.Index)

	first, err := store.FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(3), first)
	last, err := store.LastIndex(context.Background())
	require.NoError(t, err)
	entries, err := store.Entries(context.Background(), first, last+1, 0)
	require.NoError(t, err)
	require.Contains(t, entryIndexes(entries), uint64(3))

	env.stopNode(1)
	require.NoError(t, os.RemoveAll(filepath.Join(node.dir, "controller-meta")))
	env.startNodeWithConfig(t, 1, nil, compactionCfg)

	require.Eventually(t, func() bool {
		_, err := env.nodes[1].meta.GetNode(context.Background(), 2)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		_, err := env.nodes[1].meta.GetNode(context.Background(), 3)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestServiceLaggingFollowerRestoresControllerSnapshot(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	compactionCfg := func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	}
	env.startNodeWithConfig(t, 1, nil, compactionCfg)
	env.startNodeWithConfig(t, 2, nil, compactionCfg)
	env.startNodeWithConfig(t, 3, nil, compactionCfg)
	leaderID := env.waitForLeader(t, []uint64{1, 2, 3})
	laggingID := firstNonLeader([]uint64{1, 2, 3}, leaderID)
	laggingStore := env.nodes[laggingID].logDB.ForController()
	laggingLastBeforeStop, err := laggingStore.LastIndex(context.Background())
	require.NoError(t, err)
	laggingSnapBeforeStop, err := laggingStore.Snapshot(context.Background())
	require.NoError(t, err)

	env.stopNode(laggingID)
	activeIDs := removeNodeID([]uint64{1, 2, 3}, laggingID)
	require.Equal(t, leaderID, env.waitForLeader(t, activeIDs))

	leader := env.nodes[leaderID]
	for nodeID := uint64(10); nodeID < 20; nodeID++ {
		require.NoError(t, leader.service.Propose(context.Background(), nodeJoinCommand(nodeID)))
	}
	leaderStore := leader.logDB.ForController()
	var leaderSnap raftpb.Snapshot
	require.Eventually(t, func() bool {
		gotSnap, err := leaderStore.Snapshot(context.Background())
		if err != nil {
			return false
		}
		first, err := leaderStore.FirstIndex(context.Background())
		if err != nil {
			return false
		}
		if gotSnap.Metadata.Index <= laggingLastBeforeStop {
			return false
		}
		if first <= laggingLastBeforeStop+1 {
			return false
		}
		leaderSnap = gotSnap
		return true
	}, 5*time.Second, 10*time.Millisecond)

	env.startNodeWithConfig(t, laggingID, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        false,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	for nodeID := uint64(10); nodeID < 20; nodeID++ {
		waitForControllerNode(t, env.nodes[laggingID].meta, nodeID)
	}
	followerSnap := waitForControllerSnapshotIndex(t, env.nodes[laggingID].logDB.ForController(), leaderSnap.Metadata.Index)
	require.Greater(t, followerSnap.Metadata.Index, laggingSnapBeforeStop.Metadata.Index)
	require.GreaterOrEqual(t, followerSnap.Metadata.Index, leaderSnap.Metadata.Index)
}

func TestServiceLeaderStatusIncludesPeerProgress(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	leaderID := env.waitForLeader(t, []uint64{1, 2, 3})

	leader := env.nodes[leaderID]
	require.Eventually(t, func() bool {
		st := leader.service.Status()
		if st.Role != RoleLeader || st.LeaderID != leaderID || len(st.Peers) != 2 {
			return false
		}
		seen := map[uint64]bool{}
		for _, peer := range st.Peers {
			if peer.NodeID == leaderID || peer.NodeID == 0 || peer.Next == 0 || peer.State == "" {
				return false
			}
			seen[peer.NodeID] = true
		}
		return seen[firstNonLeader([]uint64{1, 2, 3}, leaderID)]
	}, 2*time.Second, 10*time.Millisecond)
}

func TestServiceCompactionSnapshotUsesConfChangeV2State(t *testing.T) {
	ctx := context.Background()

	store, err := controllermeta.Open(filepath.Join(t.TempDir(), "meta"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	sm := slotcontroller.NewStateMachine(store, slotcontroller.StateMachineConfig{})

	logDB, err := raftstorage.Open(filepath.Join(t.TempDir(), "raft"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logDB.Close()) })
	memory := newLoadedMemoryStorage(raft.NewMemoryStorage(), raftpb.ConfState{Voters: []uint64{1}})
	storageView := &storageAdapter{
		storage: logDB.ForController(),
		memory:  memory,
	}

	rawNode, err := raft.NewRawNode(&raft.Config{
		ID:                       1,
		ElectionTick:             10,
		HeartbeatTick:            1,
		Storage:                  memory,
		MaxSizePerMsg:            math.MaxUint64,
		MaxCommittedSizePerReady: math.MaxUint64,
		MaxInflightMsgs:          256,
	})
	require.NoError(t, err)

	cc := raftpb.ConfChangeV2{
		Changes: []raftpb.ConfChangeSingle{
			{Type: raftpb.ConfChangeAddNode, NodeID: 2},
		},
	}
	data, err := cc.Marshal()
	require.NoError(t, err)
	entry := raftpb.Entry{Index: 1, Term: 1, Type: raftpb.EntryConfChangeV2, Data: data}
	require.NoError(t, memory.Append([]raftpb.Entry{entry}))

	service := &Service{cfg: Config{StateMachine: sm}}
	latestConfState := raftpb.ConfState{Voters: []uint64{1}}
	applied, err := service.applyReadyState(ctx, rawNode, raft.Ready{CommittedEntries: []raftpb.Entry{entry}}, storageView, &latestConfState, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), applied)
	require.Equal(t, []uint64{1, 2}, sortedPeers(latestConfState.Voters))

	require.NoError(t, service.compactControllerLog(ctx, storageView, applied, latestConfState))
	snap, err := logDB.ForController().Snapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2}, sortedPeers(snap.Metadata.ConfState.Voters))
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
			NewSlotID:       3,
			Peers:           []uint64{1},
			PreferredLeader: 1,
		},
	}))

	assignment, err := node.meta.GetAssignment(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint32(3), assignment.SlotID)
	require.Equal(t, []uint64{1}, assignment.DesiredPeers)
	require.Equal(t, uint64(1), assignment.PreferredLeader)

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

func TestEncodeDecodeCommandAddSlotPreservesPreferredLeader(t *testing.T) {
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindAddSlot,
		AddSlot: &slotcontroller.AddSlotRequest{
			NewSlotID:       4,
			Peers:           []uint64{1, 2, 3},
			PreferredLeader: 2,
		},
	}

	data, err := encodeCommand(cmd)
	require.NoError(t, err)
	decoded, err := decodeCommand(data)
	require.NoError(t, err)

	require.Equal(t, cmd.Kind, decoded.Kind)
	require.NotNil(t, decoded.AddSlot)
	require.Equal(t, cmd.AddSlot.NewSlotID, decoded.AddSlot.NewSlotID)
	require.Equal(t, cmd.AddSlot.Peers, decoded.AddSlot.Peers)
	require.Equal(t, cmd.AddSlot.PreferredLeader, decoded.AddSlot.PreferredLeader)
}

func TestEncodeCommandWritesBinaryEnvelope(t *testing.T) {
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindAddSlot,
		AddSlot: &slotcontroller.AddSlotRequest{
			NewSlotID:       4,
			Peers:           []uint64{1, 2, 3},
			PreferredLeader: 2,
		},
	}

	data, err := encodeCommand(cmd)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	require.NotEqual(t, byte('{'), data[0])

	decoded, err := decodeCommand(data)
	require.NoError(t, err)
	require.Equal(t, cmd.Kind, decoded.Kind)
	require.Equal(t, cmd.AddSlot, decoded.AddSlot)
}

func TestEncodeCommandBinaryAllocationsStayBounded(t *testing.T) {
	cmd := sampleRaftOnboardingCommand()

	allocs := testing.AllocsPerRun(1000, func() {
		data, err := encodeCommand(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 || data[0] == '{' {
			t.Fatalf("encodeCommand() wrote non-binary payload: %q", data)
		}
	})

	require.LessOrEqual(t, allocs, float64(4), "encoding should avoid envelope clone allocations")
}

func TestDecodeCommandBinaryAllocationsStayBounded(t *testing.T) {
	cmd := sampleRaftOnboardingCommand()
	data, err := encodeCommand(cmd)
	require.NoError(t, err)

	allocs := testing.AllocsPerRun(1000, func() {
		decoded, err := decodeCommand(data)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.NodeOnboarding == nil || decoded.NodeOnboarding.Job == nil {
			t.Fatalf("decodeCommand() lost onboarding payload: %+v", decoded)
		}
	})

	require.LessOrEqual(t, allocs, float64(24), "decoding should avoid the intermediate envelope clone path")
}

func TestDecodeCommandAcceptsLegacyJSONEnvelope(t *testing.T) {
	legacy, err := json.Marshal(commandEnvelope{
		Kind: slotcontroller.CommandKindAddSlot,
		AddSlot: &slotcontroller.AddSlotRequest{
			NewSlotID:       4,
			Peers:           []uint64{1, 2, 3},
			PreferredLeader: 2,
		},
	})
	require.NoError(t, err)
	require.Equal(t, byte('{'), legacy[0])

	decoded, err := decodeCommand(legacy)
	require.NoError(t, err)
	require.Equal(t, slotcontroller.CommandKindAddSlot, decoded.Kind)
	require.Equal(t, &slotcontroller.AddSlotRequest{
		NewSlotID:       4,
		Peers:           []uint64{1, 2, 3},
		PreferredLeader: 2,
	}, decoded.AddSlot)
}

func TestEncodeDecodeCommandBinaryRoundTripsRepresentativePayloads(t *testing.T) {
	now := time.Date(2026, 5, 4, 10, 30, 0, 123, time.UTC)
	expectedStatus := controllermeta.NodeStatusSuspect
	expectedOnboardingStatus := controllermeta.OnboardingJobStatusPlanned
	job, assignment, task := sampleRaftOnboardingUpdate()

	cases := []struct {
		name string
		cmd  slotcontroller.Command
	}{
		{
			name: "agent report",
			cmd: slotcontroller.Command{
				Kind: slotcontroller.CommandKindNodeHeartbeat,
				Report: &slotcontroller.AgentReport{
					NodeID:               2,
					Addr:                 "127.0.0.1:12002",
					ObservedAt:           now,
					CapacityWeight:       3,
					HashSlotTableVersion: 9,
					Runtime: &controllermeta.SlotRuntimeView{
						SlotID:              7,
						CurrentPeers:        []uint64{1, 2, 3},
						CurrentVoters:       []uint64{1, 2},
						LeaderID:            1,
						HealthyVoters:       2,
						HasQuorum:           true,
						ObservedConfigEpoch: 8,
						LastReportAt:        now.Add(time.Second),
					},
				},
			},
		},
		{
			name: "task advance",
			cmd: slotcontroller.Command{
				Kind:    slotcontroller.CommandKindTaskResult,
				Advance: &slotcontroller.TaskAdvance{SlotID: 7, Attempt: 2, Now: now, Err: errors.New("catchup failed")},
			},
		},
		{
			name: "node status update",
			cmd: slotcontroller.Command{
				Kind: slotcontroller.CommandKindNodeStatusUpdate,
				NodeStatusUpdate: &slotcontroller.NodeStatusUpdate{Transitions: []slotcontroller.NodeStatusTransition{{
					NodeID:         2,
					NewStatus:      controllermeta.NodeStatusDead,
					ExpectedStatus: &expectedStatus,
					EvaluatedAt:    now,
					Addr:           "127.0.0.1:12002",
					CapacityWeight: 4,
				}}},
			},
		},
		{
			name: "node onboarding",
			cmd: slotcontroller.Command{
				Kind: slotcontroller.CommandKindNodeOnboardingJobUpdate,
				NodeOnboarding: &slotcontroller.NodeOnboardingJobUpdate{
					Job:            &job,
					ExpectedStatus: &expectedOnboardingStatus,
					Assignment:     &assignment,
					Task:           &task,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := encodeCommand(tc.cmd)
			require.NoError(t, err)
			require.NotEmpty(t, data)
			require.NotEqual(t, byte('{'), data[0])

			decoded, err := decodeCommand(data)
			require.NoError(t, err)
			require.Equal(t, tc.cmd.Kind, decoded.Kind)
			requireCommandPayloadEqual(t, tc.cmd, decoded)
		})
	}
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

func requireCommandPayloadEqual(t *testing.T, want, got slotcontroller.Command) {
	t.Helper()

	require.Equal(t, want.Report, got.Report)
	require.Equal(t, want.Op, got.Op)
	require.Equal(t, want.Assignment, got.Assignment)
	require.Equal(t, want.Task, got.Task)
	require.Equal(t, want.Migration, got.Migration)
	require.Equal(t, want.AddSlot, got.AddSlot)
	require.Equal(t, want.RemoveSlot, got.RemoveSlot)
	require.Equal(t, want.NodeStatusUpdate, got.NodeStatusUpdate)
	require.Equal(t, want.NodeJoin, got.NodeJoin)
	require.Equal(t, want.NodeJoinActivate, got.NodeJoinActivate)
	require.Equal(t, want.NodeOnboarding, got.NodeOnboarding)
	if want.Advance == nil {
		require.Nil(t, got.Advance)
		return
	}
	require.NotNil(t, got.Advance)
	require.Equal(t, want.Advance.SlotID, got.Advance.SlotID)
	require.Equal(t, want.Advance.Attempt, got.Advance.Attempt)
	require.Equal(t, want.Advance.Now, got.Advance.Now)
	if want.Advance.Err == nil {
		require.NoError(t, got.Advance.Err)
		return
	}
	require.Error(t, got.Advance.Err)
	require.Equal(t, want.Advance.Err.Error(), got.Advance.Err.Error())
}

func sampleRaftOnboardingCommand() slotcontroller.Command {
	expected := controllermeta.OnboardingJobStatusPlanned
	job, assignment, task := sampleRaftOnboardingUpdate()
	return slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &slotcontroller.NodeOnboardingJobUpdate{
			Job:            &job,
			ExpectedStatus: &expected,
			Assignment:     &assignment,
			Task:           &task,
		},
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
	node.service = NewService(cfg)
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

func waitForControllerSnapshotIndex(t *testing.T, store multiraft.Storage, min uint64) raftpb.Snapshot {
	t.Helper()
	var snap raftpb.Snapshot
	require.Eventually(t, func() bool {
		got, err := store.Snapshot(context.Background())
		if err != nil {
			return false
		}
		if got.Metadata.Index < min {
			return false
		}
		snap = got
		return true
	}, 5*time.Second, 10*time.Millisecond)
	return snap
}

func waitForControllerNode(t *testing.T, store *controllermeta.Store, nodeID uint64) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := store.GetNode(context.Background(), nodeID)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
}

func firstNonLeader(nodeIDs []uint64, leaderID uint64) uint64 {
	for _, nodeID := range nodeIDs {
		if nodeID != leaderID {
			return nodeID
		}
	}
	return 0
}

func removeNodeID(nodeIDs []uint64, removed uint64) []uint64 {
	out := make([]uint64, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID != removed {
			out = append(out, nodeID)
		}
	}
	return out
}

func closeIfOpen(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}

func observeLifecycleWaits(t *testing.T) <-chan string {
	t.Helper()
	waitCh := make(chan string, 8)
	setLifecycleWaitHookForTest(func(reason string) {
		waitCh <- reason
	})
	t.Cleanup(func() {
		setLifecycleWaitHookForTest(nil)
	})
	return waitCh
}

func requireLifecycleWait(t *testing.T, waitCh <-chan string, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-waitCh:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for lifecycle wait %q", want)
		}
	}
}

func (e *testEnv) addrOf(nodeID uint64) string {
	return e.addrs[nodeID]
}

func (e *testEnv) captureBootstrapCalls(t *testing.T) *bootstrapCounts {
	t.Helper()

	counts := &bootstrapCounts{counts: make(map[uint64]int)}
	original := currentRawNodeBootstrap()
	setRawNodeBootstrapForTest(func(nodeID uint64, rawNode *raft.RawNode, peers []raft.Peer) error {
		counts.record(nodeID)
		return original(nodeID, rawNode, peers)
	})
	t.Cleanup(func() {
		setRawNodeBootstrapForTest(original)
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

func entryIndexes(entries []raftpb.Entry) []uint64 {
	out := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Index)
	}
	return out
}

func nodeJoinCommand(nodeID uint64) slotcontroller.Command {
	return slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoin,
		NodeJoin: &slotcontroller.NodeJoinRequest{
			NodeID:         nodeID,
			Addr:           fmt.Sprintf("127.0.0.1:%d", 7000+nodeID),
			JoinedAt:       time.Unix(int64(nodeID), 0),
			CapacityWeight: 1,
		},
	}
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
