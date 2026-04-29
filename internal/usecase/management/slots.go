package management

import (
	"context"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// Slot is the manager-facing slot DTO.
type Slot struct {
	// SlotID is the physical slot identifier.
	SlotID uint32
	// State contains lightweight derived health summaries for list rendering.
	State SlotState
	// Assignment contains the desired slot placement view.
	Assignment SlotAssignment
	// Runtime contains the observed runtime view.
	Runtime SlotRuntime
	// NodeLog contains the selected node's local Raft log watermark when requested.
	NodeLog *SlotNodeLogStatus
}

// ListSlotsOptions contains optional filters for manager slot inventory reads.
type ListSlotsOptions struct {
	// NodeID limits the list to slots assigned to or observed on this node.
	NodeID uint64
}

// SlotState contains derived manager slot state fields.
type SlotState struct {
	// Quorum summarizes whether the slot currently has quorum.
	Quorum string
	// Sync summarizes whether runtime peers/config match the desired assignment.
	Sync string
	// LeaderMatch reports whether the preferred leader is currently leading.
	LeaderMatch bool
	// LeaderTransferPending reports whether a leader transfer task is current.
	LeaderTransferPending bool
}

// SlotAssignment contains the manager-facing desired slot placement view.
type SlotAssignment struct {
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64
	// PreferredLeader is the controller preferred leader; zero means unset.
	PreferredLeader uint64
	// ConfigEpoch is the desired slot config epoch.
	ConfigEpoch uint64
	// BalanceVersion is the desired slot balance generation.
	BalanceVersion uint64
}

// SlotRuntime contains the manager-facing observed slot runtime view.
type SlotRuntime struct {
	// CurrentPeers is the currently observed slot peer set.
	CurrentPeers []uint64
	// CurrentVoters is the currently observed slot voter set.
	CurrentVoters []uint64
	// LeaderID is the currently observed slot leader.
	LeaderID uint64
	// HealthyVoters is the observed healthy voter count.
	HealthyVoters uint32
	// HasQuorum reports whether the slot currently has quorum.
	HasQuorum bool
	// ObservedConfigEpoch is the currently observed runtime config epoch.
	ObservedConfigEpoch uint64
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time
}

// SlotNodeLogStatus is one node's local Raft log watermark for a slot.
type SlotNodeLogStatus struct {
	// NodeID is the node that reported the local log watermark.
	NodeID uint64
	// LeaderID is the slot Raft leader known by the reporting node.
	LeaderID uint64
	// CommitIndex is the highest committed Raft log index known by the reporting node.
	CommitIndex uint64
	// AppliedIndex is the highest Raft log index applied by the reporting node.
	AppliedIndex uint64
}

// ListSlots returns manager slot DTOs ordered by slot ID.
func (a *App) ListSlots(ctx context.Context, opts ListSlotsOptions) ([]Slot, error) {
	if a == nil || a.cluster == nil {
		return nil, nil
	}

	assignments, err := a.cluster.ListSlotAssignmentsStrict(ctx)
	if err != nil {
		return nil, err
	}
	views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := a.cluster.ListTasksStrict(ctx)
	if err != nil {
		return nil, err
	}

	viewsBySlot := runtimeViewsBySlot(views)
	tasksBySlot := reconcileTasksBySlot(tasks)

	slots := make([]Slot, 0, len(assignments))
	for _, assignment := range assignments {
		view, ok := viewsBySlot[assignment.SlotID]
		slot := slotFromAssignmentView(assignment, view, ok)
		if opts.NodeID != 0 && !slotMatchesNode(slot, opts.NodeID) {
			continue
		}
		if opts.NodeID != 0 {
			logStatus, err := a.cluster.SlotLogStatusOnNode(ctx, opts.NodeID, slot.SlotID)
			if err != nil {
				return nil, err
			}
			slot.NodeLog = &SlotNodeLogStatus{
				NodeID:       opts.NodeID,
				LeaderID:     uint64(logStatus.LeaderID),
				CommitIndex:  logStatus.CommitIndex,
				AppliedIndex: logStatus.AppliedIndex,
			}
		}
		applySlotTaskState(&slot, tasksBySlot[assignment.SlotID])
		slots = append(slots, slot)
	}

	sort.Slice(slots, func(i, j int) bool {
		return slots[i].SlotID < slots[j].SlotID
	})
	return slots, nil
}

func slotMatchesNode(slot Slot, nodeID uint64) bool {
	return containsUint64(slot.Assignment.DesiredPeers, nodeID) || containsUint64(slot.Runtime.CurrentPeers, nodeID)
}

func runtimeViewsBySlot(views []controllermeta.SlotRuntimeView) map[uint32]controllermeta.SlotRuntimeView {
	viewsBySlot := make(map[uint32]controllermeta.SlotRuntimeView, len(views))
	for _, view := range views {
		viewsBySlot[view.SlotID] = view
	}
	return viewsBySlot
}

func reconcileTasksBySlot(tasks []controllermeta.ReconcileTask) map[uint32]controllermeta.ReconcileTask {
	tasksBySlot := make(map[uint32]controllermeta.ReconcileTask, len(tasks))
	for _, task := range tasks {
		tasksBySlot[task.SlotID] = task
	}
	return tasksBySlot
}

func slotFromAssignmentView(assignment controllermeta.SlotAssignment, view controllermeta.SlotRuntimeView, hasView bool) Slot {
	slot := Slot{
		SlotID: assignment.SlotID,
		Assignment: SlotAssignment{
			DesiredPeers:    append([]uint64(nil), assignment.DesiredPeers...),
			PreferredLeader: assignment.PreferredLeader,
			ConfigEpoch:     assignment.ConfigEpoch,
			BalanceVersion:  assignment.BalanceVersion,
		},
	}
	if hasView {
		slot.Runtime = SlotRuntime{
			CurrentPeers:        append([]uint64(nil), view.CurrentPeers...),
			CurrentVoters:       append([]uint64(nil), view.CurrentVoters...),
			LeaderID:            view.LeaderID,
			HealthyVoters:       view.HealthyVoters,
			HasQuorum:           view.HasQuorum,
			ObservedConfigEpoch: view.ObservedConfigEpoch,
			LastReportAt:        view.LastReportAt,
		}
	}
	slot.State = SlotState{
		Quorum:      managerSlotQuorumState(hasView, slot.Runtime.HasQuorum),
		Sync:        managerSlotSyncState(assignment, view, hasView),
		LeaderMatch: assignment.PreferredLeader != 0 && assignment.PreferredLeader == view.LeaderID,
	}
	return slot
}

func applySlotTaskState(slot *Slot, task controllermeta.ReconcileTask) {
	if slot == nil {
		return
	}
	slot.State.LeaderTransferPending = task.Kind == controllermeta.TaskKindLeaderTransfer
}

func managerSlotQuorumState(hasView, hasQuorum bool) string {
	if !hasView {
		return "unknown"
	}
	if hasQuorum {
		return "ready"
	}
	return "lost"
}

func managerSlotSyncState(assignment controllermeta.SlotAssignment, view controllermeta.SlotRuntimeView, hasView bool) string {
	if !hasView {
		return "unreported"
	}
	if !sameUint64Set(assignment.DesiredPeers, view.CurrentPeers) {
		return "peer_mismatch"
	}
	if view.ObservedConfigEpoch < assignment.ConfigEpoch {
		return "epoch_lag"
	}
	return "matched"
}

func sameUint64Set(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]uint64(nil), left...)
	rightCopy := append([]uint64(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}
