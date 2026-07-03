package controller

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// SlotReplicaMoveRequest describes a staged physical Slot replica move.
type SlotReplicaMoveRequest struct {
	// SlotID is the physical Slot whose replica set should change.
	SlotID uint32
	// SourceNode is the current desired peer that will be removed.
	SourceNode uint64
	// TargetNode is the active data node that will replace SourceNode.
	TargetNode uint64
	// TargetPeers is the desired peer set after replacing SourceNode with TargetNode.
	TargetPeers []uint64
	// ConfigEpoch fences the request to the current Slot assignment epoch.
	ConfigEpoch uint64
	// StateRevision is the ControllerV2 cluster-state revision observed by the caller.
	StateRevision uint64
}

// SlotReplicaMoveResult is returned after a move task intent is accepted.
type SlotReplicaMoveResult struct {
	// Created reports whether a durable task was proposed.
	Created bool
	// Task is the deterministic staged task written by the proposal.
	Task *ReconcileTask
}

// RequestSlotReplicaMove creates a staged move task without changing DesiredPeers.
func (r *Runtime) RequestSlotReplicaMove(ctx context.Context, req SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return SlotReplicaMoveResult{}, err
	}
	assignment, ok := findSlotReplicaMoveAssignment(st, req.SlotID)
	if !ok {
		return SlotReplicaMoveResult{}, fmt.Errorf("controllerv2: slot %d assignment not found", req.SlotID)
	}
	if err := validateSlotReplicaMoveRequest(st, assignment, req); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	taskID := fmt.Sprintf("slot-%d-replica-move-%d-to-%d-r%d", req.SlotID, req.SourceNode, req.TargetNode, req.StateRevision)
	task := state.ReconcileTask{
		TaskID:           taskID,
		SlotID:           req.SlotID,
		Kind:             state.TaskKindSlotReplicaMove,
		Step:             state.TaskStepOpenLearner,
		SourceNode:       req.SourceNode,
		TargetNode:       req.TargetNode,
		TargetPeers:      append([]uint64(nil), req.TargetPeers...),
		CompletionPolicy: state.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      req.ConfigEpoch,
		Status:           state.TaskStatusPending,
	}
	expectedRevision := req.StateRevision
	if err := r.proposeTaskCommand(ctx, command.Command{
		Kind:             command.KindUpsertSlotReplicaMoveTask,
		ExpectedRevision: &expectedRevision,
		Task:             &task,
	}); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	return SlotReplicaMoveResult{Created: true, Task: (*ReconcileTask)(&task)}, nil
}

func validateSlotReplicaMoveRequest(st state.ClusterState, assignment state.SlotAssignment, req SlotReplicaMoveRequest) error {
	if req.SlotID == 0 || req.SourceNode == 0 || req.TargetNode == 0 || req.ConfigEpoch == 0 || req.StateRevision == 0 {
		return fmt.Errorf("controllerv2: slot replica move requires slot, source, target, config epoch, and state revision")
	}
	if assignment.ConfigEpoch != req.ConfigEpoch {
		return fmt.Errorf("controllerv2: slot %d config epoch %d does not match request %d", req.SlotID, assignment.ConfigEpoch, req.ConfigEpoch)
	}
	if !containsSlotReplicaMovePeer(assignment.DesiredPeers, req.SourceNode) {
		return fmt.Errorf("controllerv2: source node %d is not a desired peer for slot %d", req.SourceNode, req.SlotID)
	}
	if containsSlotReplicaMovePeer(assignment.DesiredPeers, req.TargetNode) {
		return fmt.Errorf("controllerv2: target node %d is already a desired peer for slot %d", req.TargetNode, req.SlotID)
	}
	if !activeDataNodeForSlotReplicaMove(st.Nodes, req.TargetNode) {
		return fmt.Errorf("controllerv2: target node %d is not an active data node", req.TargetNode)
	}
	if !sameUint64Set(req.TargetPeers, replaceSlotReplicaMovePeer(assignment.DesiredPeers, req.SourceNode, req.TargetNode)) {
		return fmt.Errorf("controllerv2: target peers must replace source node %d with target node %d", req.SourceNode, req.TargetNode)
	}
	for _, task := range st.Tasks {
		if task.SlotID == req.SlotID {
			return fmt.Errorf("%w: slot %d task %s", ErrSlotActiveTaskConflict, req.SlotID, task.TaskID)
		}
	}
	return nil
}

func findSlotReplicaMoveAssignment(st state.ClusterState, slotID uint32) (state.SlotAssignment, bool) {
	for _, assignment := range st.Slots {
		if assignment.SlotID == slotID {
			return assignment, true
		}
	}
	return state.SlotAssignment{}, false
}

func activeDataNodeForSlotReplicaMove(nodes []state.Node, nodeID uint64) bool {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node.JoinState == state.NodeJoinStateActive && node.HasRole(state.NodeRoleData)
		}
	}
	return false
}

func replaceSlotReplicaMovePeer(peers []uint64, source uint64, target uint64) []uint64 {
	out := append([]uint64(nil), peers...)
	for i, peer := range out {
		if peer == source {
			out[i] = target
			break
		}
	}
	return out
}

func containsSlotReplicaMovePeer(peers []uint64, want uint64) bool {
	for _, peer := range peers {
		if peer == want {
			return true
		}
	}
	return false
}
