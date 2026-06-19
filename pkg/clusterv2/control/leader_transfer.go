package control

import "fmt"

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
	// Created reports whether a durable task was accepted.
	Created bool
	// Task is the deterministic task created by the request.
	Task *ReconcileTask
}

func leaderTransferTaskFromRequest(req SlotLeaderTransferRequest) ReconcileTask {
	return ReconcileTask{
		TaskID:           fmt.Sprintf("slot-%d-leader-transfer-%d-r%d", req.SlotID, req.ConfigEpoch, req.StateRevision),
		SlotID:           req.SlotID,
		Kind:             TaskKindLeaderTransfer,
		Step:             TaskStepTransferLeader,
		SourceNode:       req.SourceNode,
		TargetNode:       req.TargetNode,
		TargetPeers:      append([]uint64(nil), req.TargetPeers...),
		CompletionPolicy: TaskCompletionPolicySingleObserver,
		ConfigEpoch:      req.ConfigEpoch,
		Status:           TaskStatusPending,
	}
}
