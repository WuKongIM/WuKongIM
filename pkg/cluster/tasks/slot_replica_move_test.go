package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slots"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestSlotReplicaMoveExecutorOpensTargetLearnerBeforeDesiredPeersChange(t *testing.T) {
	opener := &fakeLearnerOpener{}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 4, Learners: opener, MoveWriter: writer})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepOpenLearner, 0, moveStatus()))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(opener.opened) != 1 || opener.opened[0].SlotID != 1 {
		t.Fatalf("opened = %#v, want target learner slot 1", opener.opened)
	}
	if !sameUint64Set(opener.opened[0].DesiredPeers, []uint64{2, 3, 4}) {
		t.Fatalf("opened peers = %#v, want target peers", opener.opened[0].DesiredPeers)
	}
	if writer.phaseCalls != 1 || writer.phase.NextStep != control.TaskStepAddLearner || writer.phase.ExpectedPhaseIndex != 0 {
		t.Fatalf("phase = %#v, want add-learner phase after local open", writer.phase)
	}
	if writer.commitCalls != 0 {
		t.Fatalf("commit calls=%d, want no commit from learner opener", writer.commitCalls)
	}
}

func TestSlotReplicaMoveExecutorSkipsFailedTask(t *testing.T) {
	runtime := &fakeSlotReplicaMoveRuntime{status: moveStatus()}
	writer := &fakeSlotReplicaMoveWriter{}
	snapshot := slotReplicaMoveSnapshot(control.TaskStepAddLearner, 0, moveStatus())
	snapshot.Tasks[0].Status = control.TaskStatusFailed
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.statusCalls != 0 || len(runtime.changes) != 0 {
		t.Fatalf("runtime statusCalls=%d changes=%#v, want failed task skipped", runtime.statusCalls, runtime.changes)
	}
	if writer.phaseCalls != 0 || writer.commitCalls != 0 || len(writer.failed) != 0 {
		t.Fatalf("writer phase=%d commit=%d failed=%#v, want no writes for failed task", writer.phaseCalls, writer.commitCalls, writer.failed)
	}
}

func TestSlotReplicaMoveExecutorAddsLearnerBeforeDesiredPeersChange(t *testing.T) {
	initial := moveStatus()
	afterAdd := initial
	afterAdd.CurrentLearners = []multiraft.NodeID{4}
	afterAdd.ConfigAppliedIndex = 33
	runtime := &fakeSlotReplicaMoveRuntime{statuses: []multiraft.Status{initial, afterAdd}}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer, PollInterval: -1})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepAddLearner, 0, initial))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := runtime.changes[0]; got.Type != multiraft.AddLearner || got.NodeID != 4 {
		t.Fatalf("first change = %#v, want AddLearner target 4", got)
	}
	if writer.phaseCalls != 1 {
		t.Fatalf("phase calls = %d, want 1", writer.phaseCalls)
	}
	if writer.phase.ExpectedPhaseIndex != 0 || writer.phase.NextStep != control.TaskStepPromoteLearner {
		t.Fatalf("phase = %#v, want promote phase fence", writer.phase)
	}
	if writer.phase.ObservedConfigIndex != 33 ||
		!sameUint64Set(writer.phase.ObservedVoters, []uint64{1, 2, 3}) ||
		!sameUint64Set(writer.phase.ObservedLearners, []uint64{4}) {
		t.Fatalf("phase observed index/voters = %#v", writer.phase)
	}
	if writer.commitCalls != 0 {
		t.Fatalf("commit calls = %d, want none before desired peers change", writer.commitCalls)
	}
}

func TestSlotReplicaMoveExecutorResumesFromPersistedPromotePhase(t *testing.T) {
	status := moveStatus()
	status.CurrentLearners = []multiraft.NodeID{4}
	status.CommitIndex = 44
	status.ConfigAppliedIndex = 33
	status.Progress = map[multiraft.NodeID]multiraft.PeerProgress{4: {Match: 44}}
	afterPromote := status
	afterPromote.CurrentVoters = []multiraft.NodeID{1, 2, 3, 4}
	afterPromote.CurrentLearners = nil
	afterPromote.ConfigAppliedIndex = 44
	runtime := &fakeSlotReplicaMoveRuntime{statuses: []multiraft.Status{status, afterPromote}}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer, PollInterval: -1})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepPromoteLearner, 1, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := runtime.changes[0]; got.Type != multiraft.PromoteLearner || got.NodeID != 4 {
		t.Fatalf("change = %#v, want PromoteLearner target 4", got)
	}
	if writer.phase.ExpectedPhaseIndex != 1 || writer.phase.NextStep != control.TaskStepRemoveVoter {
		t.Fatalf("phase = %#v, want remove-voter phase fence", writer.phase)
	}
	if writer.phase.ObservedConfigIndex != 44 || !sameUint64Set(writer.phase.ObservedVoters, []uint64{1, 2, 3, 4}) {
		t.Fatalf("phase observed = %#v, want promoted voter set", writer.phase)
	}
}

func TestSlotReplicaMoveExecutorDefersPromotionUntilLearnerCatchesUp(t *testing.T) {
	status := moveStatus()
	status.CurrentLearners = []multiraft.NodeID{4}
	status.CommitIndex = 44
	status.Progress = map[multiraft.NodeID]multiraft.PeerProgress{4: {Match: 40}}
	runtime := &fakeSlotReplicaMoveRuntime{status: status}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer, PollMax: 1, PollInterval: -1})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepPromoteLearner, 1, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.failed) != 0 {
		t.Fatalf("failed = %#v, want learner catch-up to remain pending", writer.failed)
	}
	if len(runtime.changes) != 0 || writer.phaseCalls != 0 {
		t.Fatalf("changes=%#v phaseCalls=%d, want no promotion before catch-up", runtime.changes, writer.phaseCalls)
	}
}

func TestSlotReplicaMoveExecutorTransfersLeadershipBeforeRemovingSourceVoter(t *testing.T) {
	status := moveStatus()
	status.CurrentVoters = []multiraft.NodeID{1, 2, 3, 4}
	status.LeaderID = 1
	afterTransfer := status
	afterTransfer.LeaderID = 2
	runtime := &fakeSlotReplicaMoveRuntime{statuses: []multiraft.Status{status, afterTransfer}}
	writer := &fakeSlotReplicaMoveWriter{}
	observer := &recordingSlotReplicaMoveObserver{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer, Observer: observer, PollInterval: -1})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepRemoveVoter, 2, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.transferCalls != 1 || runtime.transferSlot != 1 || runtime.transferTarget == 1 {
		t.Fatalf("transfer slot=%d target=%d calls=%d, want one transfer away from source", runtime.transferSlot, runtime.transferTarget, runtime.transferCalls)
	}
	if len(runtime.changes) != 0 {
		t.Fatalf("changes=%#v, want no removal while source remains leader", runtime.changes)
	}
	if writer.phaseCalls != 1 || writer.phase.NextStep != control.TaskStepRemoveVoter || writer.phase.ExpectedPhaseIndex != 2 {
		t.Fatalf("phase = %#v calls=%d, want remove_voter wakeup phase", writer.phase, writer.phaseCalls)
	}
	if writer.phase.ObservedConfigIndex != afterTransfer.ConfigAppliedIndex || writer.phase.ObservedVoters == nil {
		t.Fatalf("phase observed = %#v, want transferred status observation", writer.phase)
	}
	if len(observer.phases) != 1 {
		t.Fatalf("phases = %#v, want one observation", observer.phases)
	}
	if observer.phases[0].step != "transfer_leader" || observer.phases[0].result != "ok" {
		t.Fatalf("phase = %#v, want transfer_leader ok", observer.phases[0])
	}
}

func TestSlotReplicaMoveExecutorDefersRemoveVoterWhenSourceLeadershipTransferStillPending(t *testing.T) {
	status := moveStatus()
	status.CurrentVoters = []multiraft.NodeID{1, 2, 3, 4}
	status.LeaderID = 1
	runtime := &fakeSlotReplicaMoveRuntime{status: status}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer, PollMax: 1, PollInterval: -1})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepRemoveVoter, 2, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.transferCalls != 1 {
		t.Fatalf("transfer calls=%d, want one transfer attempt", runtime.transferCalls)
	}
	if writer.phaseCalls != 0 {
		t.Fatalf("phase = %#v, want no wakeup phase before leader changes", writer.phase)
	}
	if len(writer.failed) != 0 {
		t.Fatalf("failed = %#v, want leadership transfer to remain pending", writer.failed)
	}
}

func TestSlotReplicaMoveExecutorRemovesSourceVoterAfterLeadershipMoves(t *testing.T) {
	status := moveStatus()
	status.CurrentVoters = []multiraft.NodeID{1, 2, 3, 4}
	status.LeaderID = 2
	status.ConfigAppliedIndex = 66
	afterRemove := status
	afterRemove.CurrentVoters = []multiraft.NodeID{2, 3, 4}
	afterRemove.ConfigAppliedIndex = 77
	runtime := &fakeSlotReplicaMoveRuntime{statuses: []multiraft.Status{status, afterRemove}}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 2, Runtime: runtime, MoveWriter: writer, PollInterval: -1})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepRemoveVoter, 2, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := runtime.changes[0]; got.Type != multiraft.RemoveVoter || got.NodeID != 1 {
		t.Fatalf("change = %#v, want RemoveVoter source 1", got)
	}
	if writer.phase.ExpectedPhaseIndex != 2 || writer.phase.NextStep != control.TaskStepCommitAssignment {
		t.Fatalf("phase = %#v, want commit phase fence", writer.phase)
	}
	if writer.phase.ObservedConfigIndex != 77 || !sameUint64Set(writer.phase.ObservedVoters, []uint64{2, 3, 4}) {
		t.Fatalf("phase observed = %#v, want target voters", writer.phase)
	}
}

func TestSlotReplicaMoveExecutorObservesPhaseOutcome(t *testing.T) {
	status := moveStatus()
	status.CurrentVoters = []multiraft.NodeID{1, 2, 3, 4}
	status.LeaderID = 2
	afterRemove := status
	afterRemove.CurrentVoters = []multiraft.NodeID{2, 3, 4}
	afterRemove.ConfigAppliedIndex = 77
	runtime := &fakeSlotReplicaMoveRuntime{statuses: []multiraft.Status{status, afterRemove}}
	writer := &fakeSlotReplicaMoveWriter{}
	observer := &recordingSlotReplicaMoveObserver{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{
		LocalNode:    2,
		Runtime:      runtime,
		MoveWriter:   writer,
		Observer:     observer,
		PollInterval: -1,
	})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepRemoveVoter, 2, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(observer.phases) != 1 {
		t.Fatalf("phases = %#v, want one observation", observer.phases)
	}
	if observer.phases[0].step != "remove_voter" || observer.phases[0].result != "ok" {
		t.Fatalf("phase = %#v, want remove_voter ok", observer.phases[0])
	}
}

func TestSlotReplicaMoveExecutorObservesDeferredPhaseOutcome(t *testing.T) {
	status := moveStatus()
	status.CurrentLearners = []multiraft.NodeID{4}
	status.CommitIndex = 44
	status.Progress = map[multiraft.NodeID]multiraft.PeerProgress{4: {Match: 40}}
	runtime := &fakeSlotReplicaMoveRuntime{status: status}
	writer := &fakeSlotReplicaMoveWriter{}
	observer := &recordingSlotReplicaMoveObserver{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{
		LocalNode:    1,
		Runtime:      runtime,
		MoveWriter:   writer,
		Observer:     observer,
		PollMax:      1,
		PollInterval: -1,
	})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepPromoteLearner, 1, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(observer.phases) != 1 {
		t.Fatalf("phases = %#v, want one observation", observer.phases)
	}
	if observer.phases[0].step != "promote_learner" || observer.phases[0].result != "deferred" {
		t.Fatalf("phase = %#v, want promote_learner deferred", observer.phases[0])
	}
}

func TestSlotReplicaMoveExecutorDoesNotAdvanceRemoveVoterWithoutCommittableFence(t *testing.T) {
	tests := []struct {
		name   string
		status multiraft.Status
	}{
		{
			name: "source absent but voters differ",
			status: multiraft.Status{
				SlotID:             1,
				NodeID:             2,
				LeaderID:           2,
				CurrentVoters:      []multiraft.NodeID{2, 4},
				ConfigAppliedIndex: 77,
				Role:               multiraft.RoleLeader,
			},
		},
		{
			name: "source absent but config index missing",
			status: multiraft.Status{
				SlotID:        1,
				NodeID:        2,
				LeaderID:      2,
				CurrentVoters: []multiraft.NodeID{2, 3, 4},
				Role:          multiraft.RoleLeader,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &fakeSlotReplicaMoveRuntime{status: tt.status}
			writer := &fakeSlotReplicaMoveWriter{}
			executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 2, Runtime: runtime, MoveWriter: writer, PollMax: 1, PollInterval: -1})

			err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepRemoveVoter, 2, tt.status))

			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if writer.phaseCalls != 0 {
				t.Fatalf("phase = %#v, want no commit_assignment phase without committable fence", writer.phase)
			}
			if len(writer.failed) != 1 || writer.failed[0].TaskID != "slot-1-replica-move-1-to-4-r9" {
				t.Fatalf("failed = %#v, want fenced failure", writer.failed)
			}
		})
	}
}

func TestSlotReplicaMoveExecutorCommitsAfterTargetVotersObserved(t *testing.T) {
	status := moveStatus()
	status.LeaderID = 2
	status.CurrentVoters = []multiraft.NodeID{2, 3, 4}
	status.ConfigAppliedIndex = 77
	runtime := &fakeSlotReplicaMoveRuntime{status: status}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 2, Runtime: runtime, MoveWriter: writer})

	err := executor.Reconcile(context.Background(), slotReplicaMoveSnapshot(control.TaskStepCommitAssignment, 3, status))

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if writer.commitCalls != 1 {
		t.Fatalf("commit calls = %d, want 1", writer.commitCalls)
	}
	if writer.commit.TaskID != "slot-1-replica-move-1-to-4-r9" || writer.commit.ConfigEpoch != 7 || writer.commit.Attempt != 0 {
		t.Fatalf("commit = %#v, want fenced move commit", writer.commit)
	}
	if len(runtime.changes) != 0 || writer.phaseCalls != 0 {
		t.Fatalf("changes=%#v phase calls=%d, want no config change at commit", runtime.changes, writer.phaseCalls)
	}
}

func slotReplicaMoveSnapshot(step control.TaskStep, phaseIndex uint32, status multiraft.Status) control.Snapshot {
	_ = status
	return control.Snapshot{
		Revision:  9,
		HashSlots: control.HashSlotTable{Revision: 9, Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		Slots: []control.SlotAssignment{{
			SlotID:          1,
			DesiredPeers:    []uint64{1, 2, 3},
			ConfigEpoch:     7,
			PreferredLeader: 1,
		}},
		Tasks: []control.ReconcileTask{{
			TaskID:           "slot-1-replica-move-1-to-4-r9",
			SlotID:           1,
			Kind:             control.TaskKindSlotReplicaMove,
			Step:             step,
			SourceNode:       1,
			TargetNode:       4,
			TargetPeers:      []uint64{2, 3, 4},
			CompletionPolicy: control.TaskCompletionPolicySingleObserver,
			ConfigEpoch:      7,
			Status:           control.TaskStatusPending,
			PhaseIndex:       phaseIndex,
		}},
	}
}

func moveStatus() multiraft.Status {
	return multiraft.Status{
		SlotID:             1,
		NodeID:             1,
		LeaderID:           1,
		CurrentVoters:      []multiraft.NodeID{1, 2, 3},
		ConfigAppliedIndex: 7,
		CommitIndex:        7,
		Role:               multiraft.RoleLeader,
	}
}

type fakeSlotReplicaMoveRuntime struct {
	statuses       []multiraft.Status
	status         multiraft.Status
	statusErr      error
	statusCalls    int
	changeErr      error
	transferErr    error
	changes        []multiraft.ConfigChange
	transferCalls  int
	transferSlot   multiraft.SlotID
	transferTarget multiraft.NodeID
}

func (f *fakeSlotReplicaMoveRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	if f.statusErr != nil {
		return multiraft.Status{}, f.statusErr
	}
	if len(f.statuses) > 0 {
		idx := f.statusCalls
		if idx >= len(f.statuses) {
			idx = len(f.statuses) - 1
		}
		f.statusCalls++
		return f.statuses[idx], nil
	}
	f.statusCalls++
	return f.status, nil
}

func (f *fakeSlotReplicaMoveRuntime) ChangeConfig(_ context.Context, _ multiraft.SlotID, change multiraft.ConfigChange) (multiraft.Future, error) {
	f.changes = append(f.changes, change)
	return immediateFuture{}, f.changeErr
}

func (f *fakeSlotReplicaMoveRuntime) TransferLeadership(_ context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	f.transferCalls++
	f.transferSlot = slotID
	f.transferTarget = target
	return f.transferErr
}

type fakeLearnerOpener struct {
	opened []slots.Assignment
	err    error
}

func (f *fakeLearnerOpener) OpenLearner(_ context.Context, assignment slots.Assignment) error {
	f.opened = append(f.opened, assignment)
	return f.err
}

type fakeSlotReplicaMoveWriter struct {
	phaseCalls  int
	phase       control.SlotReplicaMovePhaseAdvance
	phaseErr    error
	commitCalls int
	commit      control.SlotReplicaMoveCommit
	commitErr   error
	failed      []controller.TaskResult
	failErr     error
}

type slotReplicaMovePhaseObservation struct {
	step   string
	result string
	d      time.Duration
}

type recordingSlotReplicaMoveObserver struct {
	phases []slotReplicaMovePhaseObservation
}

func (o *recordingSlotReplicaMoveObserver) ObserveSlotReplicaMovePhase(step, result string, d time.Duration) {
	o.phases = append(o.phases, slotReplicaMovePhaseObservation{step: step, result: result, d: d})
}

func (w *fakeSlotReplicaMoveWriter) AdvanceSlotReplicaMovePhase(_ context.Context, phase control.SlotReplicaMovePhaseAdvance) error {
	w.phaseCalls++
	w.phase = phase
	return w.phaseErr
}

func (w *fakeSlotReplicaMoveWriter) CommitSlotReplicaMove(_ context.Context, commit control.SlotReplicaMoveCommit) error {
	w.commitCalls++
	w.commit = commit
	return w.commitErr
}

func (w *fakeSlotReplicaMoveWriter) FailTask(_ context.Context, result controller.TaskResult) error {
	w.failed = append(w.failed, result)
	return w.failErr
}

type immediateFuture struct{}

func (immediateFuture) Wait(context.Context) (multiraft.Result, error) {
	return multiraft.Result{Index: 1}, nil
}
