package clusterv2

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestLocalControllerLogEntriesUsesControlFacade(t *testing.T) {
	controller := &controllerLogReaderStub{
		StaticController: control.NewStaticController(nodeControlSnapshot()),
		page: control.ControllerLogEntries{
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []control.ControllerLogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				CreatedAtMS:  1781754611123,
				DataSize:     12,
				DecodeStatus: "ok",
				DecodedType:  "init_cluster_state",
				Decoded:      map[string]any{"command": "init_cluster_state"},
			}},
		},
	}
	node, err := New(validNodeConfig(t), withController(controller))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.started.Store(true)

	got, err := node.LocalControllerLogEntries(context.Background(), LogEntriesOptions{Limit: 2, Cursor: 5})
	if err != nil {
		t.Fatalf("LocalControllerLogEntries() error = %v", err)
	}

	if controller.opts.Limit != 2 || controller.opts.Cursor != 5 {
		t.Fatalf("controller opts = %#v, want limit 2 cursor 5", controller.opts)
	}
	if got.NodeID != 1 || got.NextCursor != 3 || len(got.Items) != 1 || got.Items[0].DecodedType != "init_cluster_state" || got.Items[0].CreatedAtMS != 1781754611123 {
		t.Fatalf("controller logs = %#v, want mapped page", got)
	}
}

func TestLocalControllerRaftOperationsUseControlFacade(t *testing.T) {
	controller := &controllerRaftOpsStub{
		StaticController: control.NewStaticController(nodeControlSnapshot()),
		status:           control.ControllerRaftStatus{NodeID: 1, Role: "leader", LeaderID: 1, Term: 3, CommitIndex: 8, AppliedIndex: 7},
		result:           control.ControllerRaftCompactionResult{NodeID: 1, AppliedIndex: 7, Compacted: true, AfterSnapshotIndex: 7},
	}
	node, err := New(validNodeConfig(t), withController(controller))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.started.Store(true)

	status, err := node.LocalControllerRaftStatus(context.Background())
	if err != nil {
		t.Fatalf("LocalControllerRaftStatus() error = %v", err)
	}
	result, err := node.LocalCompactControllerRaftLog(context.Background())
	if err != nil {
		t.Fatalf("LocalCompactControllerRaftLog() error = %v", err)
	}

	if !controller.statusCalled || !controller.compactCalled {
		t.Fatalf("controller calls status=%v compact=%v, want both", controller.statusCalled, controller.compactCalled)
	}
	if status.NodeID != 1 || status.Role != "leader" || status.AppliedIndex != 7 {
		t.Fatalf("controller raft status = %#v, want local status", status)
	}
	if !result.Compacted || result.AfterSnapshotIndex != 7 {
		t.Fatalf("controller raft compaction = %#v, want local result", result)
	}
}

func TestLocalSlotLogEntriesReadsDefaultSlotRaftDB(t *testing.T) {
	ctx := context.Background()
	db, err := raftlog.Open(t.TempDir(), raftlog.Options{})
	if err != nil {
		t.Fatalf("raftlog.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	storage := db.ForSlot(9)
	hs := raftpb.HardState{Term: 2, Vote: 2, Commit: 3}
	createdAtMS := int64(1781754611123)
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal},
		{Index: 2, Term: 2, Type: raftpb.EntryNormal, Data: multiraftPayloadWithCreatedAt(7, createdAtMS, metafsm.EncodeNoopCommand())},
		{Index: 3, Term: 2, Type: raftpb.EntryConfChange, Data: mustSlotLogConfChangeData(t, 2)},
	}
	if err := storage.Save(ctx, multiraft.PersistentState{HardState: &hs, Entries: entries}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := storage.MarkApplied(ctx, 2); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	runtime, err := multiraft.New(multiraft.Options{
		NodeID:       2,
		TickInterval: time.Hour,
		Workers:      1,
		Transport:    noopSlotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  defaultSlotElectionTick,
			HeartbeatTick: defaultSlotHeartbeatTick,
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.OpenSlot(ctx, multiraft.SlotOptions{ID: 9, Storage: storage, StateMachine: noopSlotStateMachine{}}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	node, err := New(Config{NodeID: 2, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.defaultSlotRaftDB = db
	node.defaultSlotRuntime = runtime
	node.started.Store(true)

	got, err := node.LocalSlotLogEntries(ctx, 9, LogEntriesOptions{Limit: 2})
	if err != nil {
		t.Fatalf("LocalSlotLogEntries() error = %v", err)
	}

	if got.NodeID != 2 || got.SlotID != 9 || got.FirstIndex != 1 || got.LastIndex != 3 || got.NextCursor != 2 {
		t.Fatalf("slot page metadata = %#v, want node 2 slot 9 range 1-3 cursor 2", got)
	}
	if got.CommitIndex != 3 || got.AppliedIndex != 2 {
		t.Fatalf("slot page watermarks = commit %d applied %d, want 3/2", got.CommitIndex, got.AppliedIndex)
	}
	if len(got.Items) != 2 || got.Items[0].Type != "conf_change" || got.Items[1].DecodedType != "noop" {
		t.Fatalf("slot entries = %#v, want conf change then noop", got.Items)
	}
	if got.Items[1].Decoded["hash_slot"] != uint64(7) {
		t.Fatalf("decoded hash_slot = %#v, want 7", got.Items[1].Decoded)
	}
	if got.Items[1].CreatedAtMS != createdAtMS {
		t.Fatalf("slot entry created_at_ms = %d, want %d", got.Items[1].CreatedAtMS, createdAtMS)
	}
}

func TestLocalCompactSlotRaftLogUsesDefaultSlotRuntime(t *testing.T) {
	ctx := context.Background()
	db, err := raftlog.Open(t.TempDir(), raftlog.Options{})
	if err != nil {
		t.Fatalf("raftlog.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	storage := db.ForSlot(9)
	hs := raftpb.HardState{Term: 2, Vote: 2, Commit: 3}
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal},
		{Index: 2, Term: 2, Type: raftpb.EntryNormal, Data: multiraftPayloadWithCreatedAt(7, 1781754611123, metafsm.EncodeNoopCommand())},
		{Index: 3, Term: 2, Type: raftpb.EntryNormal, Data: multiraftPayloadWithCreatedAt(7, 1781754611124, metafsm.EncodeNoopCommand())},
	}
	if err := storage.Save(ctx, multiraft.PersistentState{HardState: &hs, Entries: entries}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := storage.MarkApplied(ctx, 2); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	runtime, err := multiraft.New(multiraft.Options{
		NodeID:       2,
		TickInterval: time.Hour,
		Workers:      1,
		Transport:    noopSlotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  defaultSlotElectionTick,
			HeartbeatTick: defaultSlotHeartbeatTick,
			LogCompaction: multiraft.LogCompactionConfig{Enabled: true, TriggerEntries: 1, CheckInterval: time.Nanosecond},
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.OpenSlot(ctx, multiraft.SlotOptions{ID: 9, Storage: storage, StateMachine: noopSlotStateMachine{}}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	node, err := New(Config{NodeID: 2, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.defaultSlotRuntime = runtime
	node.started.Store(true)

	got, err := node.LocalCompactSlotRaftLog(ctx, 9)
	if err != nil {
		t.Fatalf("LocalCompactSlotRaftLog() error = %v", err)
	}

	if got.NodeID != 2 || got.SlotID != 9 || got.AppliedIndex != 2 || got.AfterSnapshotIndex != 2 || !got.Compacted {
		t.Fatalf("compaction = %#v, want node 2 slot 9 compacted through applied 2", got)
	}
}

type controllerLogReaderStub struct {
	*control.StaticController
	opts control.ControllerLogEntriesOptions
	page control.ControllerLogEntries
}

func (c *controllerLogReaderStub) ControllerLogEntries(_ context.Context, opts control.ControllerLogEntriesOptions) (control.ControllerLogEntries, error) {
	c.opts = opts
	return c.page, nil
}

type controllerRaftOpsStub struct {
	*control.StaticController
	status        control.ControllerRaftStatus
	result        control.ControllerRaftCompactionResult
	statusCalled  bool
	compactCalled bool
}

func (c *controllerRaftOpsStub) ControllerRaftStatus(context.Context) (control.ControllerRaftStatus, error) {
	c.statusCalled = true
	return c.status, nil
}

func (c *controllerRaftOpsStub) CompactControllerRaftLog(context.Context) (control.ControllerRaftCompactionResult, error) {
	c.compactCalled = true
	return c.result, nil
}

type noopSlotStateMachine struct{}

func (noopSlotStateMachine) Apply(context.Context, multiraft.Command) ([]byte, error) {
	return nil, nil
}

func (noopSlotStateMachine) Restore(context.Context, multiraft.Snapshot) error {
	return nil
}

func (noopSlotStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}

func mustSlotLogConfChangeData(t *testing.T, nodeID uint64) []byte {
	t.Helper()
	data, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: nodeID}).Marshal()
	if err != nil {
		t.Fatalf("ConfChange.Marshal() error = %v", err)
	}
	return data
}
