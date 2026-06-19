package controllerv2

import (
	"context"
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// SlotLeaderTransferRequest describes one Controller-backed Slot leader transfer intent.
type SlotLeaderTransferRequest struct {
	// SlotID is the physical slot whose Raft leader should move.
	SlotID uint32
	// SourceNode is the observed current Slot Raft leader.
	SourceNode uint64
	// TargetNode is the desired new Slot Raft leader.
	TargetNode uint64
	// TargetPeers is the desired Slot Raft replica set for this epoch.
	TargetPeers []uint64
	// ConfigEpoch fences the request to the caller's observed assignment epoch.
	ConfigEpoch uint64
	// StateRevision is the ControllerV2 cluster-state revision observed by the caller.
	StateRevision uint64
}

// SlotLeaderTransferResult is returned after a leader-transfer intent is accepted.
type SlotLeaderTransferResult struct {
	// Created reports whether a durable task was proposed.
	Created bool
	// Task is the deterministic task written by the proposal.
	Task *ReconcileTask
}

// RequestSlotLeaderTransfer proposes a ControllerV2 task for a Slot Raft leader transfer.
func (r *Runtime) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	expectedRevision := req.StateRevision
	taskID := fmt.Sprintf("slot-%d-leader-transfer-%d-r%d", req.SlotID, req.ConfigEpoch, req.StateRevision)
	assignment := state.SlotAssignment{
		SlotID:          req.SlotID,
		DesiredPeers:    append([]uint64(nil), req.TargetPeers...),
		ConfigEpoch:     req.ConfigEpoch,
		PreferredLeader: req.TargetNode,
	}
	task := state.ReconcileTask{
		TaskID:           taskID,
		SlotID:           req.SlotID,
		Kind:             state.TaskKindLeaderTransfer,
		Step:             state.TaskStepTransferLeader,
		SourceNode:       req.SourceNode,
		TargetNode:       req.TargetNode,
		TargetPeers:      append([]uint64(nil), req.TargetPeers...),
		CompletionPolicy: state.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      req.ConfigEpoch,
		Status:           state.TaskStatusPending,
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return SlotLeaderTransferResult{}, err
	}
	if existing, ok := equivalentActiveLeaderTransferTask(st, assignment, task); ok {
		return SlotLeaderTransferResult{Created: false, Task: (*ReconcileTask)(&existing)}, nil
	}
	if err := r.proposeTaskCommand(ctx, command.Command{
		Kind:             command.KindUpsertSlotAssignmentAndTask,
		ExpectedRevision: &expectedRevision,
		Assignment:       &assignment,
		Task:             &task,
	}); err != nil {
		return SlotLeaderTransferResult{}, err
	}
	st, err = r.LocalState(ctx)
	if err != nil {
		return SlotLeaderTransferResult{}, err
	}
	if existing, ok := equivalentActiveLeaderTransferTask(st, assignment, task); ok && existing.TaskID != task.TaskID {
		return SlotLeaderTransferResult{Created: false, Task: (*ReconcileTask)(&existing)}, nil
	}
	return SlotLeaderTransferResult{Created: true, Task: (*ReconcileTask)(&task)}, nil
}

func equivalentActiveLeaderTransferTask(st state.ClusterState, assignment state.SlotAssignment, task state.ReconcileTask) (state.ReconcileTask, bool) {
	foundAssignment := false
	for _, existingAssignment := range st.Slots {
		if existingAssignment.SlotID != assignment.SlotID {
			continue
		}
		if !equivalentLeaderTransferAssignment(existingAssignment, assignment) {
			return state.ReconcileTask{}, false
		}
		foundAssignment = true
		break
	}
	if !foundAssignment {
		return state.ReconcileTask{}, false
	}
	for _, existing := range st.Tasks {
		if existing.SlotID == task.SlotID && equivalentLeaderTransferTask(existing, task) {
			return existing, true
		}
	}
	return state.ReconcileTask{}, false
}

func equivalentLeaderTransferAssignment(a, b state.SlotAssignment) bool {
	return a.SlotID == b.SlotID &&
		a.ConfigEpoch == b.ConfigEpoch &&
		a.PreferredLeader == b.PreferredLeader &&
		sameUint64Set(a.DesiredPeers, b.DesiredPeers)
}

func equivalentLeaderTransferTask(a, b state.ReconcileTask) bool {
	return a.SlotID == b.SlotID &&
		a.Kind == state.TaskKindLeaderTransfer &&
		b.Kind == state.TaskKindLeaderTransfer &&
		a.Step == b.Step &&
		a.SourceNode == b.SourceNode &&
		a.TargetNode == b.TargetNode &&
		a.ConfigEpoch == b.ConfigEpoch &&
		a.CompletionPolicy == b.CompletionPolicy &&
		a.Status == state.TaskStatusPending &&
		b.Status == state.TaskStatusPending &&
		sameUint64Set(a.TargetPeers, b.TargetPeers)
}

func sameUint64Set(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]uint64(nil), a...)
	right := append([]uint64(nil), b...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
