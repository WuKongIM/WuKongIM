package controllerv2

import (
	"context"
	"fmt"

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
	if err := r.proposeTaskCommand(ctx, command.Command{
		Kind:             command.KindUpsertSlotAssignmentAndTask,
		ExpectedRevision: &expectedRevision,
		Assignment:       &assignment,
		Task:             &task,
	}); err != nil {
		return SlotLeaderTransferResult{}, err
	}
	return SlotLeaderTransferResult{Created: true, Task: (*ReconcileTask)(&task)}, nil
}
