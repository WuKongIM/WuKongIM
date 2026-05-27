package fsm

import (
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

const (
	// ReasonNoChange marks an idempotent command that did not change logical state.
	ReasonNoChange = "no_change"
	// ReasonAlreadyApplied marks an entry at or before the durable applied index.
	ReasonAlreadyApplied = "already_applied"
	// ReasonStaleBootstrapObsolete marks a bootstrap command superseded by newer slot state.
	ReasonStaleBootstrapObsolete = "stale_bootstrap_obsolete"
	// ReasonStaleBootstrapMissingSlot marks a stale bootstrap command for a still-missing slot.
	ReasonStaleBootstrapMissingSlot = "stale_bootstrap_missing_slot"
	// ReasonExpectedRevisionMismatch marks a failed compare-and-set guard.
	ReasonExpectedRevisionMismatch = "expected_revision_mismatch"
	// ReasonInvalidState marks a command that would violate cluster-state invariants.
	ReasonInvalidState = "invalid_state"
	// ReasonInvalidCommand marks a command missing required payload fields.
	ReasonInvalidCommand = "invalid_command"
	// ReasonInvalidTaskResult marks a task result missing required identifiers.
	ReasonInvalidTaskResult = "invalid_task_result"
	// ReasonTaskMissing marks an obsolete task result for an absent active task.
	ReasonTaskMissing = "task_missing"
	// ReasonTaskSlotMismatch marks a task result that targets the wrong slot.
	ReasonTaskSlotMismatch = "task_slot_mismatch"
	// ReasonInitConflict marks an init command that does not match existing state.
	ReasonInitConflict = "init_conflict"
	// MaxTaskLastErrorBytes bounds the durable LastError field for failed tasks.
	MaxTaskLastErrorBytes = 1024
)

func (sm *StateMachine) applyMutation(next *state.ClusterState, raftIndex uint64, cmd command.Command) ApplyResult {
	currentRevision := next.Revision
	switch cmd.Kind {
	case command.KindInitClusterState:
		return sm.applyInit(next, raftIndex, cmd)
	case command.KindUpsertSlotAssignmentAndTask:
		if cmd.Assignment == nil || cmd.Task == nil {
			return reject(ReasonInvalidCommand)
		}
		if cmd.Task.SlotID != cmd.Assignment.SlotID {
			return reject(ReasonTaskSlotMismatch)
		}
		if stale, handled := handleBootstrapRevisionMismatch(next, cmd); handled {
			return stale
		}
	case command.KindFailTask:
		if stale, handled := handleFailTaskRevisionMismatch(next, cmd); handled {
			return stale
		}
	default:
		if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != currentRevision {
			if isNonBootstrapIdempotent(*next, cmd) {
				return noop(ReasonNoChange)
			}
			return reject(ReasonExpectedRevisionMismatch)
		}
	}

	switch cmd.Kind {
	case command.KindUpsertNode:
		return sm.applyUpsertNode(next, cmd)
	case command.KindUpdateControllerVoters:
		return sm.applyUpdateControllerVoters(next, cmd)
	case command.KindReplaceHashSlotTable:
		return sm.applyReplaceHashSlotTable(next, cmd)
	case command.KindUpsertSlotAssignmentAndTask:
		return sm.applyUpsertSlotAssignmentAndTask(next, cmd)
	case command.KindCompleteTask:
		return sm.applyCompleteTask(next, cmd)
	case command.KindFailTask:
		return sm.applyFailTask(next, cmd)
	default:
		return reject(ReasonInvalidCommand)
	}
}
