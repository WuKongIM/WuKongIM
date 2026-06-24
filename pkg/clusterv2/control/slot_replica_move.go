package control

import "fmt"

// SlotReplicaMoveRequest describes one Controller-backed Slot replica move intent.
type SlotReplicaMoveRequest struct {
	// SlotID is the physical Slot whose replica set should change.
	SlotID uint32 `json:"slot_id"`
	// SourceNode is the current desired peer that will be removed.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the active data node that will replace SourceNode.
	TargetNode uint64 `json:"target_node"`
	// TargetPeers is the desired peer set after replacing SourceNode with TargetNode.
	TargetPeers []uint64 `json:"target_peers"`
	// ConfigEpoch fences the request to the current Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// StateRevision is the ControllerV2 cluster-state revision observed by the caller.
	StateRevision uint64 `json:"state_revision"`
}

// SlotReplicaMoveResult is returned after a replica-move intent is accepted.
type SlotReplicaMoveResult struct {
	// Created reports whether a durable task was accepted.
	Created bool `json:"created"`
	// Task is the deterministic task created by the request.
	Task *ReconcileTask `json:"task,omitempty"`
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
