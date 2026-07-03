package management

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListControllerTasksFiltersSortsAndLimits(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		controllerTaskFixture("slot-2-leader-transfer-9-r1", 2, control.TaskKindLeaderTransfer, control.TaskStatusPending, 2, 3, []uint64{2, 3}),
		controllerTaskFixture("slot-1-bootstrap-7", 1, control.TaskKindBootstrap, control.TaskStatusRunning, 0, 1, []uint64{1, 2, 3}),
		controllerTaskFixture("slot-1-leader-transfer-7-r9", 1, control.TaskKindLeaderTransfer, control.TaskStatusPending, 1, 2, []uint64{1, 2, 3}),
	}}}})

	resp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{
		Kind:   "leader_transfer",
		Status: "pending",
		NodeID: 2,
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("ListControllerTasks() error = %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("Total = %d, want 2", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("Items len = %d, want 1: %#v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].TaskID != "slot-1-leader-transfer-7-r9" {
		t.Fatalf("first task = %#v, want sorted slot 1 leader-transfer task", resp.Items[0])
	}
}

func TestListControllerTasksFiltersBySlotAndRelatedNode(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		{
			TaskID:      "source",
			SlotID:      1,
			Kind:        control.TaskKindLeaderTransfer,
			Step:        control.TaskStepTransferLeader,
			SourceNode:  7,
			TargetNode:  2,
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
		{
			TaskID:      "target",
			SlotID:      1,
			Kind:        control.TaskKindLeaderTransfer,
			Step:        control.TaskStepTransferLeader,
			TargetNode:  7,
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
		{
			TaskID:      "peer",
			SlotID:      1,
			Kind:        control.TaskKindBootstrap,
			Step:        control.TaskStepCreateSlot,
			TargetPeers: []uint64{1, 7, 9},
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
		{
			TaskID: "participant",
			SlotID: 2,
			Kind:   control.TaskKindBootstrap,
			Step:   control.TaskStepCreateSlot,
			ParticipantProgress: []control.TaskParticipantProgress{
				{NodeID: 7, Attempt: 1, Status: control.TaskParticipantStatusFailed, LastError: "open failed"},
			},
			ConfigEpoch: 1,
			Status:      control.TaskStatusFailed,
			LastError:   "barrier failed",
		},
		{
			TaskID:      "other",
			SlotID:      1,
			Kind:        control.TaskKindBootstrap,
			Step:        control.TaskStepCreateSlot,
			TargetPeers: []uint64{1, 2, 3},
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
	}}}})

	nodeResp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{NodeID: 7})
	if err != nil {
		t.Fatalf("ListControllerTasks(node) error = %v", err)
	}
	if nodeResp.Total != 4 {
		t.Fatalf("node Total = %d, want 4: %#v", nodeResp.Total, nodeResp.Items)
	}

	slotResp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{SlotID: 2, NodeID: 7})
	if err != nil {
		t.Fatalf("ListControllerTasks(slot,node) error = %v", err)
	}
	if slotResp.Total != 1 || slotResp.Items[0].TaskID != "participant" {
		t.Fatalf("slot/node response = %#v, want participant", slotResp)
	}
	if slotResp.Items[0].Participants[0].LastError != "open failed" {
		t.Fatalf("participant = %#v, want failed participant detail", slotResp.Items[0].Participants)
	}
}

func TestListControllerTasksLimitBounds(t *testing.T) {
	tasks := make([]control.ReconcileTask, 0, DefaultControllerTaskLimit+1)
	for i := 0; i < DefaultControllerTaskLimit+1; i++ {
		slotID := uint32(i + 1)
		tasks = append(tasks, controllerTaskFixture(fmt.Sprintf("task-%03d", i), slotID, control.TaskKindBootstrap, control.TaskStatusPending, 0, 1, []uint64{1}))
	}
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: tasks}}})

	resp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{})
	if err != nil {
		t.Fatalf("ListControllerTasks(default limit) error = %v", err)
	}
	if resp.Total != DefaultControllerTaskLimit+1 || len(resp.Items) != DefaultControllerTaskLimit {
		t.Fatalf("default limit response = total %d len %d, want total %d len %d", resp.Total, len(resp.Items), DefaultControllerTaskLimit+1, DefaultControllerTaskLimit)
	}

	_, err = app.ListControllerTasks(context.Background(), ListControllerTasksRequest{Limit: -1})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ListControllerTasks(negative limit) error = %v, want %v", err, metadb.ErrInvalidArgument)
	}

	resp, err = app.ListControllerTasks(context.Background(), ListControllerTasksRequest{Limit: MaxControllerTaskLimit})
	if err != nil {
		t.Fatalf("ListControllerTasks(max limit) error = %v", err)
	}
	if resp.Total != DefaultControllerTaskLimit+1 || len(resp.Items) != DefaultControllerTaskLimit+1 {
		t.Fatalf("max limit response = total %d len %d, want total %d len %d", resp.Total, len(resp.Items), DefaultControllerTaskLimit+1, DefaultControllerTaskLimit+1)
	}

	_, err = app.ListControllerTasks(context.Background(), ListControllerTasksRequest{Limit: MaxControllerTaskLimit + 1})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ListControllerTasks(over max) error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

func TestControllerTaskReturnsDetailAndClonesSlices(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             control.TaskKindBootstrap,
		Step:             control.TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []control.TaskParticipantProgress{
			{NodeID: 1, Attempt: 2, Status: control.TaskParticipantStatusDone},
		},
		ConfigEpoch:         7,
		Attempt:             2,
		Status:              control.TaskStatusRunning,
		PhaseIndex:          3,
		ObservedConfigIndex: 55,
		ObservedVoters:      []uint64{1, 2, 3},
		ObservedLearners:    []uint64{4},
	}}}}})

	first, err := app.ControllerTask(context.Background(), "slot-1-bootstrap-1")
	if err != nil {
		t.Fatalf("ControllerTask() error = %v", err)
	}
	first.TargetPeers[0] = 99
	first.Participants[0].NodeID = 99
	first.ObservedVoters[0] = 99
	first.ObservedLearners[0] = 99

	second, err := app.ControllerTask(context.Background(), "slot-1-bootstrap-1")
	if err != nil {
		t.Fatalf("ControllerTask(second) error = %v", err)
	}
	if !sameUint64Slice(second.TargetPeers, []uint64{1, 2, 3}) {
		t.Fatalf("TargetPeers = %#v, want cloned [1 2 3]", second.TargetPeers)
	}
	var participants []ControllerTaskParticipant = second.Participants
	if len(participants) != 1 {
		t.Fatalf("Participants len = %d, want 1", len(participants))
	}
	if second.Participants[0].NodeID != 1 {
		t.Fatalf("Participants = %#v, want cloned node 1", second.Participants)
	}
	if second.PhaseIndex != 3 || second.ObservedConfigIndex != 55 {
		t.Fatalf("proof indexes = phase %d config %d, want phase 3 config 55", second.PhaseIndex, second.ObservedConfigIndex)
	}
	if !sameUint64Slice(second.ObservedVoters, []uint64{1, 2, 3}) {
		t.Fatalf("ObservedVoters = %#v, want cloned [1 2 3]", second.ObservedVoters)
	}
	if !sameUint64Slice(second.ObservedLearners, []uint64{4}) {
		t.Fatalf("ObservedLearners = %#v, want cloned [4]", second.ObservedLearners)
	}
	if second.Kind != "bootstrap" || second.Step != "create_slot" || second.Status != "running" || second.ConfigEpoch != 7 || second.Attempt != 2 {
		t.Fatalf("detail = %#v, want bootstrap running detail", second)
	}
}

func TestControllerTaskFromControlPreservesProofFieldsAndClonesSlices(t *testing.T) {
	task := control.ReconcileTask{
		TaskID:              "slot-9-replica-move-7",
		SlotID:              9,
		Kind:                control.TaskKindSlotReplicaMove,
		Step:                control.TaskStepPromoteLearner,
		TargetPeers:         []uint64{1, 2, 3},
		PhaseIndex:          4,
		ObservedConfigIndex: 88,
		ObservedVoters:      []uint64{1, 2, 3},
		ObservedLearners:    []uint64{4},
		Status:              control.TaskStatusRunning,
	}

	first := controllerTaskFromControl(task)
	if first.PhaseIndex != 4 ||
		first.ObservedConfigIndex != 88 ||
		!sameUint64Slice(first.ObservedVoters, []uint64{1, 2, 3}) ||
		!sameUint64Slice(first.ObservedLearners, []uint64{4}) {
		t.Fatalf("first proof = %#v, want copied proof fields", first)
	}
	first.ObservedVoters[0] = 99
	first.ObservedLearners[0] = 99

	second := controllerTaskFromControl(task)
	if !sameUint64Slice(second.ObservedVoters, []uint64{1, 2, 3}) {
		t.Fatalf("ObservedVoters = %#v, want cloned [1 2 3]", second.ObservedVoters)
	}
	if !sameUint64Slice(second.ObservedLearners, []uint64{4}) {
		t.Fatalf("ObservedLearners = %#v, want cloned [4]", second.ObservedLearners)
	}
}

func TestControllerTaskUsesExactTaskIDMatch(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		controllerTaskFixture(" task-with-space ", 1, control.TaskKindBootstrap, control.TaskStatusPending, 0, 1, []uint64{1}),
	}}}})

	got, err := app.ControllerTask(context.Background(), " task-with-space ")
	if err != nil {
		t.Fatalf("ControllerTask(exact spaces) error = %v", err)
	}
	if got.TaskID != " task-with-space " {
		t.Fatalf("TaskID = %q, want exact task ID with spaces", got.TaskID)
	}

	_, err = app.ControllerTask(context.Background(), "task-with-space")
	if !errors.Is(err, ErrControllerTaskNotFound) {
		t.Fatalf("ControllerTask(trimmed lookup) error = %v, want %v", err, ErrControllerTaskNotFound)
	}
}

func TestControllerTaskNotFoundAndSnapshotError(t *testing.T) {
	notFoundApp := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		controllerTaskFixture("existing", 1, control.TaskKindBootstrap, control.TaskStatusPending, 0, 1, []uint64{1}),
	}}}})
	_, err := notFoundApp.ControllerTask(context.Background(), "   ")
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ControllerTask(empty) error = %v, want %v", err, metadb.ErrInvalidArgument)
	}

	_, err = notFoundApp.ControllerTask(context.Background(), "missing")
	if !errors.Is(err, ErrControllerTaskNotFound) {
		t.Fatalf("ControllerTask(missing) error = %v, want %v", err, ErrControllerTaskNotFound)
	}

	wantErr := errors.New("control snapshot unavailable")
	errApp := New(Options{Cluster: fakeNodeSnapshotReader{err: wantErr}})
	_, err = errApp.ListControllerTasks(context.Background(), ListControllerTasksRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListControllerTasks(snapshot error) error = %v, want %v", err, wantErr)
	}
	_, err = errApp.ControllerTask(context.Background(), "existing")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ControllerTask(snapshot error) error = %v, want %v", err, wantErr)
	}
}

func controllerTaskFixture(taskID string, slotID uint32, kind control.TaskKind, status control.TaskStatus, sourceNode uint64, targetNode uint64, targetPeers []uint64) control.ReconcileTask {
	step := control.TaskStepCreateSlot
	policy := control.TaskCompletionPolicyAllTargetPeers
	if kind == control.TaskKindLeaderTransfer {
		step = control.TaskStepTransferLeader
		policy = control.TaskCompletionPolicySingleObserver
	}
	return control.ReconcileTask{
		TaskID:           taskID,
		SlotID:           slotID,
		Kind:             kind,
		Step:             step,
		SourceNode:       sourceNode,
		TargetNode:       targetNode,
		TargetPeers:      append([]uint64(nil), targetPeers...),
		CompletionPolicy: policy,
		ConfigEpoch:      1,
		Status:           status,
	}
}
