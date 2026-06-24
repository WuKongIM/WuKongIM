package control

import "fmt"

// SlotReplicaMoveRequest describes one Controller-backed Slot replica move intent.
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

// SlotReplicaMoveResult is returned after a replica-move intent is accepted.
type SlotReplicaMoveResult struct {
	// Created reports whether a durable task was accepted.
	Created bool
	// Task is the deterministic task created by the request.
	Task *ReconcileTask
}

func slotReplicaMoveTaskFromRequest(req SlotReplicaMoveRequest) ReconcileTask {
	return ReconcileTask{
		TaskID:           fmt.Sprintf("slot-%d-replica-move-%d-to-%d-r%d", req.SlotID, req.SourceNode, req.TargetNode, req.StateRevision),
		SlotID:           req.SlotID,
		Kind:             TaskKindSlotReplicaMove,
		Step:             TaskStepOpenLearner,
		SourceNode:       req.SourceNode,
		TargetNode:       req.TargetNode,
		TargetPeers:      append([]uint64(nil), req.TargetPeers...),
		CompletionPolicy: TaskCompletionPolicySingleObserver,
		ConfigEpoch:      req.ConfigEpoch,
		Status:           TaskStatusPending,
	}
}
