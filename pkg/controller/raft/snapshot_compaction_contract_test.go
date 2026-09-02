package raft

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"go.etcd.io/raft/v3/raftpb"
)

type snapshotStateMachineStub struct {
	configStateMachineStub
	state state.ClusterState
}

func (s snapshotStateMachineStub) Snapshot(context.Context) state.ClusterState { return s.state }

func TestSnapshotCompactionPersistsMaterializedStateAndRetentionBoundary(t *testing.T) {
	ctx := context.Background()
	store, err := raftstore.Open(ctx, raftstore.Config{
		Dir: filepath.Join(t.TempDir(), "controller-raft"), NodeID: 1, SegmentSize: 1 << 20,
	})
	if err != nil {
		t.Fatalf("open raft store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry := raftpb.Entry{Index: 1, Term: 2, Type: raftpb.EntryNormal}
	if err := store.SaveReady(ctx, raftpb.HardState{Term: 2, Vote: 1, Commit: 1}, []raftpb.Entry{entry}, raftpb.Snapshot{}); err != nil {
		t.Fatalf("seed raft store: %v", err)
	}

	table, err := state.BuildInitialHashSlotTable(1, 1)
	if err != nil {
		t.Fatalf("build hash-slot table: %v", err)
	}
	materialized := state.ClusterState{
		SchemaVersion: state.CurrentSchemaVersion,
		ClusterID:     "snapshot-contract",
		Revision:      3,
		UpdatedAt:     time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC),
		Config:        state.ClusterConfig{SlotCount: 1, HashSlotCount: 1, ReplicaCount: 1},
		Controllers: []state.ControllerVoter{{
			NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter,
		}},
		Nodes: []state.Node{{
			NodeID: 1, Addr: "n1", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData},
			JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 1,
		}},
		Slots:     []state.SlotAssignment{},
		HashSlots: table,
		Tasks:     []state.ReconcileTask{},
	}
	service := &Service{cfg: Config{
		NodeID: 1, StateMachine: snapshotStateMachineStub{state: materialized},
		SnapshotCount: 1, SnapshotCatchUpEntries: 0, SnapshotMinInterval: time.Minute,
	}}

	result, err := service.compactLogAt(nil, store, 1, LogCompactionTriggerManual)
	if err != nil {
		t.Fatalf("compactLogAt(): %v", err)
	}
	if !result.Compacted || result.AppliedIndex != 1 || result.BeforeSnapshotIndex != 0 || result.AfterSnapshotIndex != 1 || result.SkippedReason != "" {
		t.Fatalf("compaction result = %+v", result)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.Metadata.Index != 1 || snapshot.Metadata.Term != 2 {
		t.Fatalf("snapshot metadata = %+v", snapshot.Metadata)
	}
	decoded, err := state.Decode(snapshot.Data)
	if err != nil {
		t.Fatalf("decode snapshot state: %v", err)
	}
	if decoded.Revision != 3 || decoded.AppliedRaftIndex != 1 || decoded.ClusterID != "snapshot-contract" {
		t.Fatalf("snapshot state = %+v", decoded)
	}
	if first, err := store.FirstIndex(); err != nil || first != 2 {
		t.Fatalf("first index after compaction = (%d, %v), want 2", first, err)
	}
	status := service.Status().Compaction
	if !status.Compacted || status.LastTrigger != LogCompactionTriggerManual || status.LastSuccessAt.IsZero() || status.LastError != "" {
		t.Fatalf("compaction status = %+v", status)
	}
}

func TestSnapshotCompactionSkipsOnlyForDocumentedSafetyReasons(t *testing.T) {
	ctx := context.Background()
	store, err := raftstore.Open(ctx, raftstore.Config{
		Dir: filepath.Join(t.TempDir(), "controller-raft"), NodeID: 4, SegmentSize: 1 << 20,
	})
	if err != nil {
		t.Fatalf("open raft store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := &Service{cfg: Config{
		NodeID: 4, StateMachine: configStateMachineStub{}, SnapshotCount: 2,
		SnapshotCatchUpEntries: 1, SnapshotMinInterval: time.Hour,
	}}

	result, err := service.compactLogAt(ctx, store, 0, LogCompactionTriggerManual)
	if err != nil || result.SkippedReason != LogCompactionSkipNoAppliedIndex {
		t.Fatalf("zero applied compaction = (%+v, %v)", result, err)
	}
	result, err = service.compactLogAt(ctx, store, 1, LogCompactionTriggerManual)
	if err != nil || result.SkippedReason != LogCompactionSkipNoMaterializedState {
		t.Fatalf("unmaterialized compaction = (%+v, %v)", result, err)
	}
	if err := service.maybeSnapshot(ctx, store, 1); err != nil {
		t.Fatalf("below-threshold maybeSnapshot(): %v", err)
	}
	if err := service.maybeSnapshot(ctx, store, 2); err != nil {
		t.Fatalf("eligible maybeSnapshot(): %v", err)
	}
	firstAttempt := service.lastSnapshot
	if firstAttempt.IsZero() {
		t.Fatal("eligible automatic compaction did not record its attempt time")
	}
	if err := service.maybeSnapshot(ctx, store, 2); err != nil {
		t.Fatalf("rate-limited maybeSnapshot(): %v", err)
	}
	if !service.lastSnapshot.Equal(firstAttempt) {
		t.Fatalf("rate-limited attempt changed timestamp: before %v after %v", firstAttempt, service.lastSnapshot)
	}
	service.cfg.SnapshotCount = 0
	if err := service.maybeSnapshot(ctx, store, 100); err != nil {
		t.Fatalf("disabled maybeSnapshot(): %v", err)
	}

	notStarted := &Service{cfg: Config{NodeID: 9, SnapshotCount: 10, SnapshotMinInterval: time.Minute}}
	result, err = notStarted.compactLogNow(ctx, nil, LogCompactionTriggerManual)
	if !errors.Is(err, ErrNotStarted) || result.NodeID != 9 || result.SkippedReason != LogCompactionSkipNotStarted || result.Error != ErrNotStarted.Error() {
		t.Fatalf("not-started compaction = (%+v, %v)", result, err)
	}
	status := notStarted.Status().Compaction
	if status.LastTrigger != LogCompactionTriggerManual || status.LastError != ErrNotStarted.Error() || status.LastErrorAt.IsZero() {
		t.Fatalf("not-started compaction status = %+v", status)
	}
	(*Service)(nil).recordCompactionStatus(LogCompactionTriggerManual, LogCompactionResult{}, nil)
}

func TestSnapshotCompactionRejectsInvalidMaterializedState(t *testing.T) {
	ctx := context.Background()
	store, err := raftstore.Open(ctx, raftstore.Config{
		Dir: filepath.Join(t.TempDir(), "controller-raft"), NodeID: 1, SegmentSize: 1 << 20,
	})
	if err != nil {
		t.Fatalf("open raft store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := &Service{cfg: Config{
		NodeID: 1,
		StateMachine: snapshotStateMachineStub{state: state.ClusterState{
			SchemaVersion: state.CurrentSchemaVersion, Revision: 1,
		}},
		SnapshotCount: 1, SnapshotMinInterval: time.Minute,
	}}

	result, err := service.compactLogAt(ctx, store, 1, LogCompactionTriggerManual)
	if err == nil || result.Compacted || result.Error == "" {
		t.Fatalf("invalid state compaction = (%+v, %v)", result, err)
	}
	status := service.Status().Compaction
	if status.LastError == "" || status.LastErrorAt.IsZero() || status.Compacted {
		t.Fatalf("invalid state compaction status = %+v", status)
	}
}
