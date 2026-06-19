package tasks

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// SlotManager ensures the local Slot runtime for a control assignment.
type SlotManager interface {
	Ensure(context.Context, slots.Assignment) error
}

// StatusReader reads local Slot Multi-Raft status.
type StatusReader interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
}

// Writer submits fenced ControllerV2 task progress and results.
type Writer interface {
	CompleteTask(context.Context, cv2.TaskResult) error
	FailTask(context.Context, cv2.TaskResult) error
	ReportTaskProgress(context.Context, cv2.TaskProgress) error
}

// BootstrapExecutorConfig wires a bootstrap task executor.
type BootstrapExecutorConfig struct {
	// LocalNode is this node's stable cluster identity.
	LocalNode uint64
	// Slots ensures local Slot runtime state.
	Slots SlotManager
	// Status observes local Slot Multi-Raft status after convergence.
	Status StatusReader
	// Writer persists task progress through the Controller facade.
	Writer Writer
}

// BootstrapExecutor reports participant progress for bootstrap tasks.
type BootstrapExecutor struct {
	cfg BootstrapExecutorConfig
}

// NewBootstrapExecutor creates a BootstrapExecutor.
func NewBootstrapExecutor(cfg BootstrapExecutorConfig) *BootstrapExecutor {
	return &BootstrapExecutor{cfg: cfg}
}

// Reconcile reports local bootstrap progress and completes converged tasks.
func (e *BootstrapExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if e == nil || e.cfg.LocalNode == 0 || e.cfg.Writer == nil {
		return nil
	}
	for _, task := range snapshot.Tasks {
		if task.Kind != control.TaskKindBootstrap || task.CompletionPolicy != control.TaskCompletionPolicyAllTargetPeers {
			continue
		}
		assignment, ok := findSlot(snapshot.Slots, task.SlotID)
		if !ok {
			continue
		}
		if e.cfg.Slots != nil && containsNode(task.TargetPeers, e.cfg.LocalNode) && !participantDone(task, e.cfg.LocalNode) {
			if err := e.ensureLocal(ctx, snapshot.HashSlots, assignment); err != nil {
				return e.cfg.Writer.ReportTaskProgress(ctx, failedProgress(task, e.cfg.LocalNode, err.Error()))
			}
			if err := e.cfg.Writer.ReportTaskProgress(ctx, doneProgress(task, e.cfg.LocalNode)); err != nil {
				return err
			}
		}
		if allParticipantsDone(task) && e.observedConverged(task) {
			if err := e.cfg.Writer.CompleteTask(ctx, cv2.TaskResult{
				TaskID:      task.TaskID,
				SlotID:      task.SlotID,
				TaskKind:    cv2.TaskKind(task.Kind),
				ConfigEpoch: task.ConfigEpoch,
				Attempt:     task.Attempt,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *BootstrapExecutor) ensureLocal(ctx context.Context, table control.HashSlotTable, assignment control.SlotAssignment) error {
	return e.cfg.Slots.Ensure(ctx, slots.Assignment{
		SlotID:          assignment.SlotID,
		DesiredPeers:    append([]uint64(nil), assignment.DesiredPeers...),
		PreferredLeader: assignment.PreferredLeader,
		HashSlots:       hashSlotsForSlot(table, assignment.SlotID),
	})
}

func (e *BootstrapExecutor) observedConverged(task control.ReconcileTask) bool {
	if e == nil || e.cfg.Status == nil {
		return false
	}
	status, err := e.cfg.Status.Status(multiraft.SlotID(task.SlotID))
	if err != nil || status.LeaderID == 0 {
		return false
	}
	voters := make([]uint64, 0, len(status.CurrentVoters))
	for _, voter := range status.CurrentVoters {
		voters = append(voters, uint64(voter))
	}
	quorum := len(task.TargetPeers)/2 + 1
	if len(voters) < quorum {
		return false
	}
	sort.Slice(voters, func(i, j int) bool { return voters[i] < voters[j] })
	targets := append([]uint64(nil), task.TargetPeers...)
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	if len(voters) != len(targets) {
		return false
	}
	for i := range voters {
		if voters[i] != targets[i] {
			return false
		}
	}
	return true
}

func findSlot(slots []control.SlotAssignment, slotID uint32) (control.SlotAssignment, bool) {
	for _, slot := range slots {
		if slot.SlotID == slotID {
			return slot, true
		}
	}
	return control.SlotAssignment{}, false
}

func participantDone(task control.ReconcileTask, nodeID uint64) bool {
	for _, progress := range task.ParticipantProgress {
		if progress.NodeID == nodeID {
			return progress.Status == control.TaskParticipantStatusDone
		}
	}
	return false
}

func allParticipantsDone(task control.ReconcileTask) bool {
	if len(task.ParticipantProgress) == 0 {
		return false
	}
	for _, progress := range task.ParticipantProgress {
		if progress.Status != control.TaskParticipantStatusDone {
			return false
		}
	}
	return true
}

func doneProgress(task control.ReconcileTask, nodeID uint64) cv2.TaskProgress {
	progress := baseProgress(task, nodeID)
	progress.Status = cv2.TaskParticipantStatusDone
	return progress
}

func failedProgress(task control.ReconcileTask, nodeID uint64, err string) cv2.TaskProgress {
	progress := baseProgress(task, nodeID)
	progress.Status = cv2.TaskParticipantStatusFailed
	progress.Err = err
	return progress
}

func baseProgress(task control.ReconcileTask, nodeID uint64) cv2.TaskProgress {
	progress := cv2.TaskProgress{
		TaskID:            task.TaskID,
		SlotID:            task.SlotID,
		TaskKind:          cv2.TaskKind(task.Kind),
		ConfigEpoch:       task.ConfigEpoch,
		TaskAttempt:       task.Attempt,
		ParticipantNodeID: nodeID,
	}
	for _, item := range task.ParticipantProgress {
		if item.NodeID == nodeID {
			progress.ParticipantAttempt = item.Attempt
			break
		}
	}
	return progress
}

func containsNode(peers []uint64, nodeID uint64) bool {
	for _, peerID := range peers {
		if peerID == nodeID {
			return true
		}
	}
	return false
}

func hashSlotsForSlot(table control.HashSlotTable, slotID uint32) []uint16 {
	out := make([]uint16, 0)
	for _, r := range table.Ranges {
		if r.SlotID != slotID {
			continue
		}
		for hashSlot := r.From; hashSlot <= r.To; hashSlot++ {
			out = append(out, hashSlot)
			if hashSlot == r.To {
				break
			}
		}
	}
	return out
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
