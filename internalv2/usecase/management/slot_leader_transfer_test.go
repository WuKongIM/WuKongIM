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
	statusReader := &fakeSlotRuntimeStatusReader{
		statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
		},
	}
	app := New(Options{
		Cluster:           fakeNodeSnapshotReader{snapshot: leaderTransferSnapshot()},
		SlotRuntimeStatus: statusReader,
		LeaderTransfer:    writer,
		Now:               func() time.Time { return generatedAt },
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
	if !sameUint64Slice(statusReader.candidates, []uint64{1, 2, 3}) {
		t.Fatalf("runtime candidates = %v, want desired peers [1 2 3]", statusReader.candidates)
	}
}

func TestRequestSlotLeaderTransferAlreadyLeaderIsNoop(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: leaderTransferSnapshot()},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
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

func TestRequestSlotLeaderTransferMapsWriterNoopToExistingTask(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{
		result: control.SlotLeaderTransferResult{
			Created: false,
			Task: &control.ReconcileTask{
				TaskID:           "slot-1-leader-transfer-7-r8",
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
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
		LeaderTransfer: writer,
	})

	got, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2})

	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}
	if got.Created || got.Message != SlotLeaderTransferMessageExistingTask || got.Task == nil || got.Task.TaskID != "slot-1-leader-transfer-7-r8" {
		t.Fatalf("response = %#v, want existing task no-op from writer", got)
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

func TestRequestSlotLeaderTransferRejectsInvalidSnapshotTargets(t *testing.T) {
	tests := []struct {
		name     string
		snapshot control.Snapshot
		target   uint64
		wantErr  error
	}{
		{
			name: "slot absent",
			snapshot: func() control.Snapshot {
				snapshot := leaderTransferSnapshot()
				snapshot.Slots = nil
				return snapshot
			}(),
			target:  2,
			wantErr: ErrSlotLeaderTransferSlotNotFound,
		},
		{
			name: "target outside desired peers",
			snapshot: func() control.Snapshot {
				snapshot := leaderTransferSnapshot()
				snapshot.Slots[0].DesiredPeers = []uint64{1, 3}
				return snapshot
			}(),
			target:  2,
			wantErr: metadb.ErrInvalidArgument,
		},
		{
			name: "target node absent",
			snapshot: func() control.Snapshot {
				snapshot := leaderTransferSnapshot()
				snapshot.Nodes = []control.Node{snapshot.Nodes[0], snapshot.Nodes[2]}
				return snapshot
			}(),
			target:  2,
			wantErr: metadb.ErrInvalidArgument,
		},
		{
			name: "target node leaving",
			snapshot: func() control.Snapshot {
				snapshot := leaderTransferSnapshot()
				snapshot.Nodes[1].JoinState = control.NodeJoinStateLeaving
				return snapshot
			}(),
			target:  2,
			wantErr: metadb.ErrInvalidArgument,
		},
		{
			name: "target node lacks data role",
			snapshot: func() control.Snapshot {
				snapshot := leaderTransferSnapshot()
				snapshot.Nodes[1].Roles = []control.Role{control.RoleController}
				return snapshot
			}(),
			target:  2,
			wantErr: metadb.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &fakeSlotLeaderTransferWriter{}
			app := New(Options{
				Cluster: fakeNodeSnapshotReader{snapshot: tt.snapshot},
				SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
					statuses: map[uint32]SlotRuntimeStatus{
						1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
					},
				},
				LeaderTransfer: writer,
			})

			_, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: tt.target})

			if err != tt.wantErr {
				t.Fatalf("RequestSlotLeaderTransfer() error = %v, want %v", err, tt.wantErr)
			}
			if len(writer.requests) != 0 {
				t.Fatalf("writer requests = %#v, want no call for invalid snapshot target", writer.requests)
			}
		})
	}
}

func TestRequestSlotLeaderTransferRejectsJoiningTarget(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{}
	snapshot := leaderTransferSnapshot()
	snapshot.Nodes[1].JoinState = control.NodeJoinStateJoining
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
		LeaderTransfer: writer,
	})

	_, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{
		SlotID:     1,
		TargetNode: 2,
	})
	if err == nil {
		t.Fatal("RequestSlotLeaderTransfer() error = nil, want joining target rejection")
	}
	if len(writer.requests) != 0 {
		t.Fatalf("writer requests = %#v, want none", writer.requests)
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
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
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
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})
	if _, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2}); !errors.Is(err, ErrSlotLeaderTransferConflict) {
		t.Fatalf("RequestSlotLeaderTransfer(conflict) error = %v, want %v", err, ErrSlotLeaderTransferConflict)
	}
}

func TestRequestSlotLeaderTransferRejectsUnsafeRuntimeStateAsConflict(t *testing.T) {
	tests := []struct {
		name     string
		snapshot control.Snapshot
		status   SlotRuntimeStatus
	}{
		{
			name:     "leader zero",
			snapshot: leaderTransferSnapshot(),
			status:   SlotRuntimeStatus{SlotID: 1, CurrentVoters: []uint64{1, 2, 3}},
		},
		{
			name:     "leader not voter",
			snapshot: leaderTransferSnapshot(),
			status:   SlotRuntimeStatus{SlotID: 1, LeaderID: 4, CurrentVoters: []uint64{1, 2, 3}},
		},
		{
			name:     "target not voter",
			snapshot: leaderTransferSnapshot(),
			status:   SlotRuntimeStatus{SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 3}},
		},
		{
			name: "voters cannot form quorum",
			snapshot: func() control.Snapshot {
				snapshot := leaderTransferSnapshot()
				snapshot.Slots[0].DesiredPeers = []uint64{1, 2, 3, 4, 5}
				return snapshot
			}(),
			status: SlotRuntimeStatus{SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &fakeSlotLeaderTransferWriter{}
			app := New(Options{
				Cluster: fakeNodeSnapshotReader{snapshot: tt.snapshot},
				SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
					statuses: map[uint32]SlotRuntimeStatus{1: tt.status},
				},
				LeaderTransfer: writer,
			})

			_, err := app.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2})

			if !errors.Is(err, ErrSlotLeaderTransferConflict) {
				t.Fatalf("RequestSlotLeaderTransfer() error = %v, want %v", err, ErrSlotLeaderTransferConflict)
			}
			if len(writer.requests) != 0 {
				t.Fatalf("writer requests = %#v, want no call for unsafe runtime state", writer.requests)
			}
		})
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
	statuses   map[uint32]SlotRuntimeStatus
	candidates []uint64
	err        error
}

func (f *fakeSlotRuntimeStatusReader) SlotRuntimeStatus(_ context.Context, slotID uint32, candidates []uint64) (SlotRuntimeStatus, error) {
	if f.err != nil {
		return SlotRuntimeStatus{}, f.err
	}
	f.candidates = append([]uint64(nil), candidates...)
	return f.statuses[slotID], nil
}
