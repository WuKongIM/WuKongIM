package tasks

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestLeaderTransferExecutorLeaderCallsTransfer(t *testing.T) {
	runtime := &fakeLeaderTransferRuntime{
		statuses: []multiraft.Status{
			leaderTransferStatus(1),
			leaderTransferStatus(2),
		},
	}
	writer := &recordingWriter{}
	executor := NewLeaderTransferExecutor(LeaderTransferExecutorConfig{
		LocalNode:    1,
		Runtime:      runtime,
		Writer:       writer,
		PollMax:      2,
		PollInterval: -1,
	})

	err := executor.Reconcile(context.Background(), leaderTransferSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.transferCalls != 1 || runtime.transferSlot != 1 || runtime.transferTarget != 2 {
		t.Fatalf("transfer calls=%d slot=%d target=%d, want one call slot 1 target 2", runtime.transferCalls, runtime.transferSlot, runtime.transferTarget)
	}
	if runtime.expectCalls != 1 || runtime.expectSlot != 1 || runtime.expectTarget != 2 {
		t.Fatalf("expect calls=%d slot=%d target=%d, want one expected transfer slot 1 target 2", runtime.expectCalls, runtime.expectSlot, runtime.expectTarget)
	}
	if len(writer.completed) != 1 || writer.completed[0].TaskID != "slot-1-leader-transfer-7-r1" {
		t.Fatalf("completed = %#v, want leader transfer task completed", writer.completed)
	}
}

func TestLeaderTransferExecutorCompletesOnLegalNonTargetLeader(t *testing.T) {
	runtime := &fakeLeaderTransferRuntime{statuses: []multiraft.Status{leaderTransferStatus(3)}}
	writer := &recordingWriter{}
	executor := NewLeaderTransferExecutor(LeaderTransferExecutorConfig{
		LocalNode:    1,
		Runtime:      runtime,
		Writer:       writer,
		PollMax:      1,
		PollInterval: -1,
	})

	err := executor.Reconcile(context.Background(), leaderTransferSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.transferCalls != 0 {
		t.Fatalf("transfer calls = %d, want 0 when legal non-source leader already exists", runtime.transferCalls)
	}
	if runtime.expectCalls != 0 {
		t.Fatalf("expect calls = %d, want 0 when legal leader already exists", runtime.expectCalls)
	}
	if len(writer.completed) != 1 || writer.completed[0].TaskID != "slot-1-leader-transfer-7-r1" {
		t.Fatalf("completed = %#v, want leader transfer task completed", writer.completed)
	}
}

func TestLeaderTransferExecutorNonLeaderExpectsTransferWithoutTransferring(t *testing.T) {
	runtime := &fakeLeaderTransferRuntime{statuses: []multiraft.Status{leaderTransferStatus(1)}}
	writer := &recordingWriter{}
	executor := NewLeaderTransferExecutor(LeaderTransferExecutorConfig{
		LocalNode:    2,
		Runtime:      runtime,
		Writer:       writer,
		PollMax:      1,
		PollInterval: -1,
	})

	err := executor.Reconcile(context.Background(), leaderTransferSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.transferCalls != 0 {
		t.Fatalf("transfer calls = %d, want 0 from non-leader executor", runtime.transferCalls)
	}
	if runtime.expectCalls != 1 || runtime.expectSlot != 1 || runtime.expectTarget != 2 {
		t.Fatalf("expect calls=%d slot=%d target=%d, want one expected transfer slot 1 target 2", runtime.expectCalls, runtime.expectSlot, runtime.expectTarget)
	}
	if len(writer.completed) != 0 || len(writer.failed) != 0 {
		t.Fatalf("writer completed=%#v failed=%#v, want no task writes", writer.completed, writer.failed)
	}
}

func TestLeaderTransferExecutorFailsOnTimeout(t *testing.T) {
	runtime := &fakeLeaderTransferRuntime{
		statuses: []multiraft.Status{
			leaderTransferStatus(1),
			leaderTransferStatus(1),
			leaderTransferStatus(1),
		},
	}
	writer := &recordingWriter{}
	executor := NewLeaderTransferExecutor(LeaderTransferExecutorConfig{
		LocalNode:    1,
		Runtime:      runtime,
		Writer:       writer,
		PollMax:      2,
		PollInterval: -1,
	})

	err := executor.Reconcile(context.Background(), leaderTransferSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.transferCalls != 1 {
		t.Fatalf("transfer calls = %d, want 1", runtime.transferCalls)
	}
	if runtime.expectCalls != 1 {
		t.Fatalf("expect calls = %d, want 1", runtime.expectCalls)
	}
	if len(writer.failed) != 1 || writer.failed[0].TaskID != "slot-1-leader-transfer-7-r1" || writer.failed[0].Err != "leader transfer timed out" {
		t.Fatalf("failed = %#v, want timed out leader transfer task", writer.failed)
	}
}

func leaderTransferSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 1,
		Slots: []control.SlotAssignment{{
			SlotID:          1,
			DesiredPeers:    []uint64{1, 2, 3},
			ConfigEpoch:     7,
			PreferredLeader: 2,
		}},
		Tasks: []control.ReconcileTask{{
			TaskID:           "slot-1-leader-transfer-7-r1",
			SlotID:           1,
			Kind:             control.TaskKindLeaderTransfer,
			Step:             control.TaskStepTransferLeader,
			SourceNode:       1,
			TargetNode:       2,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: control.TaskCompletionPolicySingleObserver,
			ConfigEpoch:      7,
			Status:           control.TaskStatusPending,
		}},
	}
}

func leaderTransferStatus(leader multiraft.NodeID) multiraft.Status {
	return multiraft.Status{
		SlotID:        1,
		NodeID:        1,
		LeaderID:      leader,
		CurrentVoters: []multiraft.NodeID{1, 2, 3},
	}
}

type fakeLeaderTransferRuntime struct {
	statuses       []multiraft.Status
	err            error
	transferErr    error
	statusCalls    int
	transferCalls  int
	transferSlot   multiraft.SlotID
	transferTarget multiraft.NodeID
	expectCalls    int
	expectSlot     multiraft.SlotID
	expectTarget   multiraft.NodeID
}

func (f *fakeLeaderTransferRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	if f.err != nil {
		return multiraft.Status{}, f.err
	}
	if len(f.statuses) == 0 {
		return multiraft.Status{}, nil
	}
	idx := f.statusCalls
	if idx >= len(f.statuses) {
		idx = len(f.statuses) - 1
	}
	f.statusCalls++
	return f.statuses[idx], nil
}

func (f *fakeLeaderTransferRuntime) TransferLeadership(_ context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	f.transferCalls++
	f.transferSlot = slotID
	f.transferTarget = target
	return f.transferErr
}

func (f *fakeLeaderTransferRuntime) ExpectLeaderTransfer(_ context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	f.expectCalls++
	f.expectSlot = slotID
	f.expectTarget = target
	return nil
}
