package management

import (
	"context"
	"errors"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// SlotDetail is the manager-facing slot detail DTO.
type SlotDetail struct {
	Slot
	// Task contains the current reconcile task summary when one exists.
	Task *Task
}

// GetSlot returns one manager slot detail DTO with an optional task summary.
func (a *App) GetSlot(ctx context.Context, slotID uint32) (SlotDetail, error) {
	if a == nil || a.cluster == nil {
		return SlotDetail{}, nil
	}

	assignments, err := a.cluster.ListSlotAssignmentsStrict(ctx)
	if err != nil {
		return SlotDetail{}, err
	}
	views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
	if err != nil {
		return SlotDetail{}, err
	}

	slot, ok := managerSlotByID(slotID, assignments, views)
	if !ok {
		return SlotDetail{}, controllermeta.ErrNotFound
	}

	detail := SlotDetail{Slot: slot}
	task, err := a.cluster.GetReconcileTaskStrict(ctx, slotID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			return detail, nil
		}
		return SlotDetail{}, err
	}

	taskDTO := managerTask(task)
	detail.Task = &taskDTO
	return detail, nil
}

func managerSlotByID(slotID uint32, assignments []controllermeta.SlotAssignment, views []controllermeta.SlotRuntimeView) (Slot, bool) {
	assignmentBySlot := slotAssignmentsBySlot(assignments)
	viewBySlot := runtimeViewsBySlot(views)

	assignment, hasAssignment := assignmentBySlot[slotID]
	view, hasView := viewBySlot[slotID]

	if hasAssignment {
		return slotFromAssignmentView(assignment, view, hasView), true
	}
	if hasView {
		return slotFromRuntimeView(slotID, view), true
	}
	return Slot{}, false
}

func slotAssignmentsBySlot(assignments []controllermeta.SlotAssignment) map[uint32]controllermeta.SlotAssignment {
	assignmentBySlot := make(map[uint32]controllermeta.SlotAssignment, len(assignments))
	for _, assignment := range assignments {
		assignmentBySlot[assignment.SlotID] = assignment
	}
	return assignmentBySlot
}

func slotFromRuntimeView(slotID uint32, view controllermeta.SlotRuntimeView) Slot {
	return Slot{
		SlotID: slotID,
		State: SlotState{
			Quorum: managerSlotQuorumState(true, view.HasQuorum),
			Sync:   "unreported",
		},
		Runtime: SlotRuntime{
			CurrentPeers:        append([]uint64(nil), view.CurrentPeers...),
			LeaderID:            view.LeaderID,
			HealthyVoters:       view.HealthyVoters,
			HasQuorum:           view.HasQuorum,
			ObservedConfigEpoch: view.ObservedConfigEpoch,
			LastReportAt:        view.LastReportAt,
		},
	}
}

func slotWithoutObservation(slotID uint32) Slot {
	return Slot{
		SlotID: slotID,
		State: SlotState{
			Quorum: "unknown",
			Sync:   "unreported",
		},
	}
}
