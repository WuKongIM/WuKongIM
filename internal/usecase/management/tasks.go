package management

import (
	"context"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// Task is the manager-facing reconcile task DTO.
type Task struct {
	// SlotID is the physical slot identifier associated with the task.
	SlotID uint32
	// Kind is the stringified task kind.
	Kind string
	// Step is the stringified task step.
	Step string
	// Status is the stringified task status.
	Status string
	// SourceNode is the task source node when applicable.
	SourceNode uint64
	// TargetNode is the task target node when applicable.
	TargetNode uint64
	// Attempt is the current task attempt count.
	Attempt uint32
	// NextRunAt is the next retry schedule when the task is retrying.
	NextRunAt *time.Time
	// LastError is the last recorded task error message.
	LastError string
}

// TaskDetail is the manager-facing reconcile task detail DTO.
type TaskDetail struct {
	Task
	// Slot contains lightweight slot context for the task.
	Slot Slot
}

// ListTasks returns manager task DTOs ordered by slot ID.
func (a *App) ListTasks(ctx context.Context) ([]Task, error) {
	if a == nil || a.cluster == nil {
		return nil, nil
	}

	tasks, err := a.cluster.ListTasksStrict(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, managerTask(task))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SlotID < out[j].SlotID
	})
	return out, nil
}

// GetTask returns one manager task detail DTO with slot context.
func (a *App) GetTask(ctx context.Context, slotID uint32) (TaskDetail, error) {
	if a == nil || a.cluster == nil {
		return TaskDetail{}, nil
	}

	task, err := a.cluster.GetReconcileTaskStrict(ctx, slotID)
	if err != nil {
		return TaskDetail{}, err
	}
	assignments, err := a.cluster.ListSlotAssignmentsStrict(ctx)
	if err != nil {
		return TaskDetail{}, err
	}
	views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
	if err != nil {
		return TaskDetail{}, err
	}

	detail := TaskDetail{Task: managerTask(task)}
	if slot, ok := managerSlotByID(task.SlotID, assignments, views); ok {
		detail.Slot = slot
		return detail, nil
	}
	detail.Slot = slotWithoutObservation(task.SlotID)
	return detail, nil
}

func managerTask(task controllermeta.ReconcileTask) Task {
	return Task{
		SlotID:     task.SlotID,
		Kind:       managerTaskKind(task.Kind),
		Step:       managerTaskStep(task.Step),
		Status:     managerTaskStatus(task.Status),
		SourceNode: task.SourceNode,
		TargetNode: task.TargetNode,
		Attempt:    task.Attempt,
		NextRunAt:  managerTaskNextRunAt(task),
		LastError:  task.LastError,
	}
}

func managerTaskKind(kind controllermeta.TaskKind) string {
	switch kind {
	case controllermeta.TaskKindBootstrap:
		return "bootstrap"
	case controllermeta.TaskKindRepair:
		return "repair"
	case controllermeta.TaskKindRebalance:
		return "rebalance"
	default:
		return "unknown"
	}
}

func managerTaskStep(step controllermeta.TaskStep) string {
	switch step {
	case controllermeta.TaskStepAddLearner:
		return "add_learner"
	case controllermeta.TaskStepCatchUp:
		return "catch_up"
	case controllermeta.TaskStepPromote:
		return "promote"
	case controllermeta.TaskStepTransferLeader:
		return "transfer_leader"
	case controllermeta.TaskStepRemoveOld:
		return "remove_old"
	default:
		return "unknown"
	}
}

func managerTaskStatus(status controllermeta.TaskStatus) string {
	switch status {
	case controllermeta.TaskStatusPending:
		return "pending"
	case controllermeta.TaskStatusRetrying:
		return "retrying"
	case controllermeta.TaskStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func managerTaskNextRunAt(task controllermeta.ReconcileTask) *time.Time {
	if task.Status != controllermeta.TaskStatusRetrying || task.NextRunAt.IsZero() {
		return nil
	}
	nextRunAt := task.NextRunAt
	return &nextRunAt
}
