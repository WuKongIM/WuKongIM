package tasks

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slots"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestBootstrapExecutorReportsParticipantDoneAfterEnsure(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 2, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), bootstrapSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if manager.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", manager.ensureCalls)
	}
	if len(writer.progress) != 1 || writer.progress[0].ParticipantNodeID != 2 || writer.progress[0].Status != controller.TaskParticipantStatusDone {
		t.Fatalf("progress = %#v, want node 2 done", writer.progress)
	}
}

func TestBootstrapExecutorCompletesAfterAllParticipantsDoneAndLegalLeaderObserved(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 1 || writer.completed[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("completed = %#v", writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWithoutQuorumObservation(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{err: multiraft.ErrSlotNotFound}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 0 {
		t.Fatalf("completed = %#v, want none", writer.completed)
	}
}

func TestBootstrapExecutorRetriesFailedParticipantWithAdvancedAttempt(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		if snapshot.Tasks[0].ParticipantProgress[i].NodeID == 2 {
			snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusFailed
			snapshot.Tasks[0].ParticipantProgress[i].Attempt = 1
			snapshot.Tasks[0].ParticipantProgress[i].LastError = "open failed"
		}
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 2, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if manager.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", manager.ensureCalls)
	}
	if len(writer.progress) != 1 {
		t.Fatalf("progress = %#v, want one retry report", writer.progress)
	}
	got := writer.progress[0]
	if got.ParticipantNodeID != 2 || got.ParticipantAttempt != 1 || got.Status != controller.TaskParticipantStatusDone {
		t.Fatalf("progress[0] = %#v, want node 2 done at participant attempt 1", got)
	}
}

func TestBootstrapExecutorSkipsLocalProgressForNonTargetNode(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 9, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), bootstrapSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if manager.ensureCalls != 0 {
		t.Fatalf("ensure calls = %d, want 0 for non-target node", manager.ensureCalls)
	}
	if len(writer.progress) != 0 || len(writer.completed) != 0 {
		t.Fatalf("writer progress=%#v completed=%#v, want no task writes", writer.progress, writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWhenObservedVotersDifferFromTargetPeers(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 4}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 0 {
		t.Fatalf("completed = %#v, want none for mismatched voters", writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWithoutObservedLeader(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 0, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Runtime: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 0 {
		t.Fatalf("completed = %#v, want none without observed leader", writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWithLeaderOutsideTargetVoters(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 9, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Runtime: status, Writer: writer})

	if err := executor.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 0 {
		t.Fatalf("completed = %#v, want none for leader outside target voters", writer.completed)
	}
}

func TestBootstrapExecutorAcceptsRaftLeaderDifferentFromPreferredTarget(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	runtime := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 2, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 2, Slots: manager, Runtime: runtime, Writer: writer})

	if err := executor.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.expectCalls != 0 || runtime.transferCalls != 0 {
		t.Fatalf("runtime expect=%d transfer=%d, want no bootstrap leader transfer", runtime.expectCalls, runtime.transferCalls)
	}
	if len(writer.completed) != 1 || writer.completed[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("completed = %#v, want Raft-leader convergence completion", writer.completed)
	}
}

func TestBootstrapExecutorFollowerAcceptsRaftLeaderDifferentFromPreferredTarget(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	runtime := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 2, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 3, Slots: manager, Runtime: runtime, Writer: writer})

	if err := executor.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.expectCalls != 0 || runtime.transferCalls != 0 {
		t.Fatalf("runtime expect=%d transfer=%d, want no bootstrap leader transfer", runtime.expectCalls, runtime.transferCalls)
	}
	if len(writer.completed) != 1 {
		t.Fatalf("completed = %#v, want task completion under legal Raft leader", writer.completed)
	}
}

func bootstrapSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "n2", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "n3", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}}},
		Tasks: []control.ReconcileTask{{
			TaskID:           "slot-1-bootstrap-1",
			SlotID:           1,
			Kind:             control.TaskKindBootstrap,
			Step:             control.TaskStepCreateSlot,
			TargetNode:       1,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
			ParticipantProgress: []control.TaskParticipantProgress{
				{NodeID: 1, Status: control.TaskParticipantStatusPending},
				{NodeID: 2, Status: control.TaskParticipantStatusPending},
				{NodeID: 3, Status: control.TaskParticipantStatusPending},
			},
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		}},
	}
}

type fakeSlotManager struct {
	ensureCalls int
	last        slots.Assignment
	err         error
}

func (f *fakeSlotManager) Ensure(ctx context.Context, assignment slots.Assignment) error {
	f.ensureCalls++
	f.last = assignment
	return f.err
}

type fakeStatusReader struct {
	status        multiraft.Status
	err           error
	expectErr     error
	transferErr   error
	expectCalls   int
	transferCalls int
	lastTarget    multiraft.NodeID
}

func (f *fakeStatusReader) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	return f.status, f.err
}

func (f *fakeStatusReader) ExpectLeaderTransfer(_ context.Context, _ multiraft.SlotID, target multiraft.NodeID) error {
	f.expectCalls++
	f.lastTarget = target
	return f.expectErr
}

func (f *fakeStatusReader) TransferLeadership(_ context.Context, _ multiraft.SlotID, target multiraft.NodeID) error {
	f.transferCalls++
	f.lastTarget = target
	return f.transferErr
}

type recordingWriter struct {
	completed []controller.TaskResult
	failed    []controller.TaskResult
	progress  []controller.TaskProgress
}

func (w *recordingWriter) CompleteTask(ctx context.Context, result controller.TaskResult) error {
	w.completed = append(w.completed, result)
	return nil
}

func (w *recordingWriter) FailTask(ctx context.Context, result controller.TaskResult) error {
	w.failed = append(w.failed, result)
	return nil
}

func (w *recordingWriter) ReportTaskProgress(ctx context.Context, progress controller.TaskProgress) error {
	w.progress = append(w.progress, progress)
	return nil
}
