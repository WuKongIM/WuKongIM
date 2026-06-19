package management

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// SlotLeaderTransferMessageCreated is returned when a new transfer task is accepted.
	SlotLeaderTransferMessageCreated = "leader transfer task created"
	// SlotLeaderTransferMessageAlreadyLeader is returned when the target already leads the Slot.
	SlotLeaderTransferMessageAlreadyLeader = "already leader"
	// SlotLeaderTransferMessageExistingTask is returned when the same transfer task already exists.
	SlotLeaderTransferMessageExistingTask = "leader transfer task already exists"
)

var (
	// ErrSlotLeaderTransferUnavailable reports that the control writer is unavailable.
	ErrSlotLeaderTransferUnavailable = errors.New("internalv2/usecase/management: slot leader transfer unavailable")
	// ErrSlotRuntimeStatusUnavailable reports that Slot runtime status is unavailable.
	ErrSlotRuntimeStatusUnavailable = errors.New("internalv2/usecase/management: slot runtime status unavailable")
	// ErrSlotLeaderTransferSlotNotFound reports that the requested Slot is not assigned.
	ErrSlotLeaderTransferSlotNotFound = errors.New("internalv2/usecase/management: slot leader transfer slot not found")
	// ErrSlotLeaderTransferConflict reports that a different active task already owns the Slot.
	ErrSlotLeaderTransferConflict = errors.New("internalv2/usecase/management: slot leader transfer conflict")
)

// SlotLeaderTransferWriter submits Controller-backed Slot leader-transfer intents.
type SlotLeaderTransferWriter interface {
	// RequestSlotLeaderTransfer submits a validated Slot leader-transfer request.
	RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error)
}

// SlotRuntimeStatusReader reads live Slot Raft status for transfer validation.
type SlotRuntimeStatusReader interface {
	// SlotRuntimeStatus returns the currently observed leader and voter set for a Slot.
	SlotRuntimeStatus(context.Context, uint32, []uint64) (SlotRuntimeStatus, error)
}

// SlotRuntimeStatus is the live Slot Raft status needed before creating a transfer task.
type SlotRuntimeStatus struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// LeaderID is the currently observed Slot Raft leader.
	LeaderID uint64
	// CurrentVoters is the currently observed Slot Raft voter set.
	CurrentVoters []uint64
}

// SlotLeaderTransferRequest is the manager-facing transfer intent.
type SlotLeaderTransferRequest struct {
	// SlotID is the physical Slot whose leader should move.
	SlotID uint32
	// TargetNode is the desired new Slot Raft leader.
	TargetNode uint64
}

// SlotLeaderTransferResponse is returned after evaluating or submitting a transfer intent.
type SlotLeaderTransferResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt time.Time
	// SlotID is the physical Slot whose leader transfer was requested.
	SlotID uint32
	// TargetNode is the desired new Slot Raft leader.
	TargetNode uint64
	// PreferredLeader is the controller preferred leader from the assignment.
	PreferredLeader uint64
	// ActualLeader is the observed Slot Raft leader before the request.
	ActualLeader uint64
	// Created reports whether a new durable task was created.
	Created bool
	// Task contains the active or created Slot task when available.
	Task *SlotTask
	// Message is a stable operator-facing result summary.
	Message string
}

// RequestSlotLeaderTransfer validates and submits a Slot leader transfer intent.
func (a *App) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferResponse{}, err
	}
	if req.SlotID == 0 || req.TargetNode == 0 {
		return SlotLeaderTransferResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return SlotLeaderTransferResponse{}, ErrSlotLeaderTransferUnavailable
	}
	if a.slotRuntimeStatus == nil {
		return SlotLeaderTransferResponse{}, ErrSlotRuntimeStatusUnavailable
	}

	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return SlotLeaderTransferResponse{}, err
	}
	assignment, ok := findSlotAssignment(snapshot.Slots, req.SlotID)
	if !ok {
		return SlotLeaderTransferResponse{}, ErrSlotLeaderTransferSlotNotFound
	}
	runtime, err := a.slotRuntimeStatus.SlotRuntimeStatus(ctx, req.SlotID, assignment.DesiredPeers)
	if err != nil {
		return SlotLeaderTransferResponse{}, err
	}
	response := SlotLeaderTransferResponse{
		GeneratedAt:     a.now(),
		SlotID:          req.SlotID,
		TargetNode:      req.TargetNode,
		PreferredLeader: assignment.PreferredLeader,
		ActualLeader:    runtime.LeaderID,
	}

	if task, ok := findActiveSlotTask(snapshot.Tasks, req.SlotID); ok {
		if task.Kind == control.TaskKindLeaderTransfer && task.TargetNode == req.TargetNode {
			response.Task = slotTaskFromControl(task)
			response.Message = SlotLeaderTransferMessageExistingTask
			return response, nil
		}
		return SlotLeaderTransferResponse{}, ErrSlotLeaderTransferConflict
	}

	if err := validateSlotLeaderTransferSnapshot(snapshot, assignment, req.TargetNode); err != nil {
		return SlotLeaderTransferResponse{}, err
	}
	if err := validateSlotLeaderTransferRuntime(runtime, assignment, req.TargetNode); err != nil {
		return SlotLeaderTransferResponse{}, err
	}
	if runtime.LeaderID == req.TargetNode {
		response.Message = SlotLeaderTransferMessageAlreadyLeader
		return response, nil
	}
	if a.leaderTransfer == nil {
		return SlotLeaderTransferResponse{}, ErrSlotLeaderTransferUnavailable
	}

	result, err := a.leaderTransfer.RequestSlotLeaderTransfer(ctx, control.SlotLeaderTransferRequest{
		SlotID:        req.SlotID,
		SourceNode:    runtime.LeaderID,
		TargetNode:    req.TargetNode,
		TargetPeers:   append([]uint64(nil), assignment.DesiredPeers...),
		ConfigEpoch:   assignment.ConfigEpoch,
		StateRevision: snapshot.Revision,
	})
	if err != nil {
		return SlotLeaderTransferResponse{}, err
	}
	response.Created = result.Created
	response.Task = slotTaskFromControlPtr(result.Task)
	if result.Created {
		response.Message = SlotLeaderTransferMessageCreated
	} else {
		response.Message = SlotLeaderTransferMessageExistingTask
	}
	return response, nil
}

func findSlotAssignment(items []control.SlotAssignment, slotID uint32) (control.SlotAssignment, bool) {
	for _, item := range items {
		if item.SlotID == slotID {
			return item, true
		}
	}
	return control.SlotAssignment{}, false
}

func findActiveSlotTask(items []control.ReconcileTask, slotID uint32) (control.ReconcileTask, bool) {
	for _, item := range items {
		if item.SlotID == slotID {
			return item, true
		}
	}
	return control.ReconcileTask{}, false
}

func validateSlotLeaderTransferSnapshot(snapshot control.Snapshot, assignment control.SlotAssignment, targetNode uint64) error {
	if len(assignment.DesiredPeers) < 2 || !containsUint64(assignment.DesiredPeers, targetNode) {
		return metadb.ErrInvalidArgument
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID != targetNode {
			continue
		}
		if node.Status == control.NodeAlive && hasRole(node.Roles, control.RoleData) {
			return nil
		}
		return metadb.ErrInvalidArgument
	}
	return metadb.ErrInvalidArgument
}

func validateSlotLeaderTransferRuntime(runtime SlotRuntimeStatus, assignment control.SlotAssignment, targetNode uint64) error {
	if runtime.LeaderID == 0 || !containsUint64(runtime.CurrentVoters, runtime.LeaderID) || !containsUint64(runtime.CurrentVoters, targetNode) {
		return ErrSlotLeaderTransferConflict
	}
	if len(runtime.CurrentVoters) < int(quorumSize(len(assignment.DesiredPeers))) {
		return ErrSlotLeaderTransferConflict
	}
	return nil
}

func slotTaskFromControlPtr(task *control.ReconcileTask) *SlotTask {
	if task == nil {
		return nil
	}
	return slotTaskFromControl(*task)
}
