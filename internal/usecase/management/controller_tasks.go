package management

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultControllerTaskLimit is the manager default active task page size.
	DefaultControllerTaskLimit = 100
	// MaxControllerTaskLimit is the largest accepted active task page size.
	MaxControllerTaskLimit = 500
)

// ErrControllerTaskNotFound reports that an active Controller task is absent.
var ErrControllerTaskNotFound = errors.New("internal/usecase/management: controller task not found")

// ListControllerTasksRequest contains manager filters for active Controller tasks.
type ListControllerTasksRequest struct {
	// Kind limits results to one reconcile workflow kind.
	Kind string
	// Status limits results to one task status.
	Status string
	// SlotID limits results to one physical Slot.
	SlotID uint32
	// NodeID limits results to tasks related to this node.
	NodeID uint64
	// Limit bounds the returned item count.
	Limit int
}

// ListControllerTasksResponse is a bounded manager page of active Controller tasks.
type ListControllerTasksResponse struct {
	// Total is the number of matching active tasks before the limit is applied.
	Total int
	// Items contains sorted active Controller task rows.
	Items []ControllerTask
}

// ControllerTask is the manager-facing active Controller task DTO.
type ControllerTask struct {
	// TaskID is the durable task identity.
	TaskID string
	// SlotID is the affected physical Slot.
	SlotID uint32
	// Kind is the reconcile workflow kind.
	Kind string
	// Step is the current workflow step.
	Step string
	// Status is the task state.
	Status string
	// SourceNode is the optional source node for move-like tasks.
	SourceNode uint64
	// TargetNode is the primary task target when set.
	TargetNode uint64
	// TargetPeers are the peers expected to participate.
	TargetPeers []uint64
	// CompletionPolicy describes how participant progress gates completion.
	CompletionPolicy string
	// ConfigEpoch ties the task to a Slot assignment epoch.
	ConfigEpoch uint64
	// Attempt is the global task attempt.
	Attempt uint32
	// LastError is the latest task-level error.
	LastError string
	// PhaseIndex is the externally observed Slot Raft phase index for this task.
	PhaseIndex uint32
	// ObservedConfigIndex is the Slot Raft applied index that proved the current phase.
	ObservedConfigIndex uint64
	// ObservedVoters is the Slot Raft voter set observed for the current phase.
	ObservedVoters []uint64
	// ObservedLearners is the Slot Raft learner set observed for the current phase.
	ObservedLearners []uint64
	// Participants contains per-node task progress.
	Participants []ControllerTaskParticipant
}

// ControllerTaskParticipant is one node's Controller task progress summary.
type ControllerTaskParticipant struct {
	// NodeID is the participant node identity.
	NodeID uint64
	// Attempt is the participant-local attempt.
	Attempt uint32
	// Status is the participant progress state.
	Status string
	// LastError is the latest participant-level error.
	LastError string
}

// ListControllerTasks returns active Controller tasks from the control snapshot.
func (a *App) ListControllerTasks(ctx context.Context, req ListControllerTasksRequest) (ListControllerTasksResponse, error) {
	limit, err := normalizeControllerTaskLimit(req.Limit)
	if err != nil {
		return ListControllerTasksResponse{}, err
	}
	if a == nil || a.cluster == nil {
		return ListControllerTasksResponse{}, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ListControllerTasksResponse{}, err
	}

	items := make([]ControllerTask, 0, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if !controllerTaskMatches(task, req) {
			continue
		}
		items = append(items, controllerTaskFromControl(task))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SlotID != items[j].SlotID {
			return items[i].SlotID < items[j].SlotID
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].TaskID < items[j].TaskID
	})

	total := len(items)
	if total > limit {
		items = items[:limit]
	}
	return ListControllerTasksResponse{Total: total, Items: items}, nil
}

// ControllerTask returns one active Controller task by exact task ID.
func (a *App) ControllerTask(ctx context.Context, taskID string) (ControllerTask, error) {
	if strings.TrimSpace(taskID) == "" {
		return ControllerTask{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return ControllerTask{}, ErrControllerTaskNotFound
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ControllerTask{}, err
	}
	for _, task := range snapshot.Tasks {
		if task.TaskID == taskID {
			return controllerTaskFromControl(task), nil
		}
	}
	return ControllerTask{}, ErrControllerTaskNotFound
}

func normalizeControllerTaskLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxControllerTaskLimit {
		return 0, metadb.ErrInvalidArgument
	}
	if limit == 0 {
		return DefaultControllerTaskLimit, nil
	}
	return limit, nil
}

func controllerTaskMatches(task control.ReconcileTask, req ListControllerTasksRequest) bool {
	if req.Kind != "" && string(task.Kind) != req.Kind {
		return false
	}
	if req.Status != "" && string(task.Status) != req.Status {
		return false
	}
	if req.SlotID != 0 && task.SlotID != req.SlotID {
		return false
	}
	if req.NodeID != 0 && !controllerTaskHasNode(task, req.NodeID) {
		return false
	}
	return true
}

func controllerTaskHasNode(task control.ReconcileTask, nodeID uint64) bool {
	if task.SourceNode == nodeID || task.TargetNode == nodeID {
		return true
	}
	if containsUint64(task.TargetPeers, nodeID) {
		return true
	}
	for _, participant := range task.ParticipantProgress {
		if participant.NodeID == nodeID {
			return true
		}
	}
	return false
}

func controllerTaskFromControl(task control.ReconcileTask) ControllerTask {
	slotTask := slotTaskFromControl(task)
	participants := make([]ControllerTaskParticipant, 0, len(slotTask.Participants))
	for _, participant := range slotTask.Participants {
		participants = append(participants, ControllerTaskParticipant{
			NodeID:    participant.NodeID,
			Attempt:   participant.Attempt,
			Status:    participant.Status,
			LastError: participant.LastError,
		})
	}
	return ControllerTask{
		TaskID:              slotTask.TaskID,
		SlotID:              task.SlotID,
		Kind:                slotTask.Kind,
		Step:                slotTask.Step,
		Status:              slotTask.Status,
		SourceNode:          slotTask.SourceNode,
		TargetNode:          slotTask.TargetNode,
		TargetPeers:         append([]uint64(nil), slotTask.TargetPeers...),
		CompletionPolicy:    slotTask.CompletionPolicy,
		ConfigEpoch:         slotTask.ConfigEpoch,
		Attempt:             slotTask.Attempt,
		LastError:           slotTask.LastError,
		PhaseIndex:          slotTask.PhaseIndex,
		ObservedConfigIndex: slotTask.ObservedConfigIndex,
		ObservedVoters:      append([]uint64(nil), slotTask.ObservedVoters...),
		ObservedLearners:    append([]uint64(nil), slotTask.ObservedLearners...),
		Participants:        participants,
	}
}
