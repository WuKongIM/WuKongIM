package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestRequestSlotLeaderTransferCreatesControllerTask(t *testing.T) {
	generatedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	writer := &fakeSlotLeaderTransferWriter{
		result: control.SlotLeaderTransferResult{
			Created: true,
			Task: &control.ReconcileTask{
				TaskID:           "slot-1-leader-transfer-7-r9",
				SlotID:           1,
				Kind:             control.TaskKindLeaderTransfer,
				Step:             control.TaskStepTransferLeader,
				SourceNode:       1,
				TargetNode:       2,
				TargetPeers:      []uint64{1, 2, 3},
				CompletionPolicy: control.TaskCompletionPolicySingleObserver,
				ConfigEpoch:      7,
				Status:           control.TaskStatusPending,
			},
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: leaderTransferSnapshot()},
		SlotRuntimeStatus: fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
		LeaderTransfer: writer,
		Now:            func() time.Time { return generatedAt },
	})

	got, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2})

	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}
	if !got.Created || got.Message != SlotLeaderTransferMessageCreated || !got.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("response = %#v, want created response at generated time", got)
	}
	if got.SlotID != 1 || got.TargetNode != 2 || got.PreferredLeader != 1 || got.ActualLeader != 1 {
		t.Fatalf("response identity = %#v, want slot 1 target 2 preferred/actual leader 1", got)
	}
	if got.Task == nil || got.Task.TaskID != "slot-1-leader-transfer-7-r9" || got.Task.Kind != "leader_transfer" {
		t.Fatalf("task = %#v, want mapped leader transfer task", got.Task)
	}
	if len(writer.requests) != 1 {
		t.Fatalf("writer requests = %d, want 1", len(writer.requests))
	}
	req := writer.requests[0]
	if req.SlotID != 1 || req.SourceNode != 1 || req.TargetNode != 2 || !sameUint64Slice(req.TargetPeers, []uint64{1, 2, 3}) || req.ConfigEpoch != 7 || req.StateRevision != 9 {
		t.Fatalf("writer request = %#v, want slot/source/target/peers/epoch/revision", req)
	}
}

func TestRequestSlotLeaderTransferAlreadyLeaderIsNoop(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: leaderTransferSnapshot()},
		SlotRuntimeStatus: fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
		LeaderTransfer: writer,
	})

	got, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2})

	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}
	if got.Created || got.Message != SlotLeaderTransferMessageAlreadyLeader || got.Task != nil {
		t.Fatalf("response = %#v, want already leader no-op", got)
	}
	if len(writer.requests) != 0 {
		t.Fatalf("writer requests = %#v, want no call for already leader", writer.requests)
	}
}

func TestRequestSlotLeaderTransferRejectsInvalidTargetAndUnavailablePorts(t *testing.T) {
	if _, err := New(Options{}).RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("target 0 error = %v, want invalid argument", err)
	}
	if _, err := New(Options{}).RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2}); !errors.Is(err, ErrSlotLeaderTransferUnavailable) {
		t.Fatalf("missing cluster error = %v, want %v", err, ErrSlotLeaderTransferUnavailable)
	}
	if _, err := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: leaderTransferSnapshot()}}).RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2}); !errors.Is(err, ErrSlotRuntimeStatusUnavailable) {
		t.Fatalf("missing runtime error = %v, want %v", err, ErrSlotRuntimeStatusUnavailable)
	}
}

func TestRequestSlotLeaderTransferReusesExistingTaskAndRejectsConflicts(t *testing.T) {
	existing := control.ReconcileTask{
		TaskID:           "existing-transfer",
		SlotID:           1,
		Kind:             control.TaskKindLeaderTransfer,
		Step:             control.TaskStepTransferLeader,
		SourceNode:       1,
		TargetNode:       2,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: control.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      7,
		Status:           control.TaskStatusPending,
	}
	snapshot := leaderTransferSnapshot()
	snapshot.Tasks = []control.ReconcileTask{existing}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2})

	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer(existing) error = %v", err)
	}
	if got.Created || got.Message != SlotLeaderTransferMessageExistingTask || got.Task == nil || got.Task.TaskID != "existing-transfer" {
		t.Fatalf("response = %#v, want existing task no-op", got)
	}

	snapshot.Tasks[0].TargetNode = 3
	app = New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})
	if _, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2}); !errors.Is(err, ErrSlotLeaderTransferConflict) {
		t.Fatalf("RequestSlotLeaderTransfer(conflict) error = %v, want %v", err, ErrSlotLeaderTransferConflict)
	}
}

func leaderTransferSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 9,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "n2", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "n3", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{{
			SlotID:          1,
			DesiredPeers:    []uint64{1, 2, 3},
			ConfigEpoch:     7,
			PreferredLeader: 1,
		}},
	}
}

type fakeSlotLeaderTransferWriter struct {
	requests []control.SlotLeaderTransferRequest
	result   control.SlotLeaderTransferResult
	err      error
}

func (f *fakeSlotLeaderTransferWriter) RequestSlotLeaderTransfer(_ context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return control.SlotLeaderTransferResult{}, f.err
	}
	return f.result, nil
}

type fakeSlotRuntimeStatusReader struct {
	statuses map[uint32]SlotRuntimeStatus
	err      error
}

func (f fakeSlotRuntimeStatusReader) SlotRuntimeStatus(_ context.Context, slotID uint32) (SlotRuntimeStatus, error) {
	if f.err != nil {
		return SlotRuntimeStatus{}, f.err
	}
	return f.statuses[slotID], nil
}
