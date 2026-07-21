package fsm

import (
	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
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
	// ReasonTaskKindMismatch marks a task result for the wrong active task kind.
	ReasonTaskKindMismatch = "task_kind_mismatch"
	// ReasonTaskEpochMismatch marks a task result for the wrong config epoch.
	ReasonTaskEpochMismatch = "task_epoch_mismatch"
	// ReasonTaskAttemptMismatch marks an obsolete task result for an older global attempt.
	ReasonTaskAttemptMismatch = "task_attempt_mismatch"
	// ReasonTaskParticipantUnexpected marks a progress report from a non-participant.
	ReasonTaskParticipantUnexpected = "task_participant_unexpected"
	// ReasonTaskParticipantAttemptStale marks an obsolete participant progress report.
	ReasonTaskParticipantAttemptStale = "task_participant_attempt_stale"
	// ReasonTaskPhaseMismatch marks an obsolete Slot replica move phase report.
	ReasonTaskPhaseMismatch = "task_phase_mismatch"
	// ReasonTaskStepMismatch marks a task command for the wrong workflow step.
	ReasonTaskStepMismatch = "task_step_mismatch"
	// ReasonTaskObservedVotersMismatch marks a commit whose observed voters do not match target peers.
	ReasonTaskObservedVotersMismatch = "task_observed_voters_mismatch"
	// ReasonTaskObservedLearnersMismatch marks a phase whose observed learners do not prove the next step.
	ReasonTaskObservedLearnersMismatch = "task_observed_learners_mismatch"
	// ReasonTaskObservedConfigMissing marks a commit without a durable Slot Raft config observation.
	ReasonTaskObservedConfigMissing = "task_observed_config_missing"
	// ReasonControllerVoterProofMissing marks a promotion without live Controller Raft voter evidence.
	ReasonControllerVoterProofMissing = "controller_voter_proof_missing"
	// ReasonControllerVoterSetMismatch marks a promotion fenced to a stale Controller voter set.
	ReasonControllerVoterSetMismatch = "controller_voter_set_mismatch"
	// ReasonInitConflict marks an init command that does not match existing state.
	ReasonInitConflict = "init_conflict"
	// MaxTaskLastErrorBytes bounds the durable LastError field for failed tasks.
	MaxTaskLastErrorBytes = 1024
)

func (sm *StateMachine) applyMutation(next *state.ClusterState, raftIndex uint64, raftTerm uint64, cmd command.Command) ApplyResult {
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
		if cmd.Task.Kind == state.TaskKindBootstrap {
			if stale, handled := handleBootstrapRevisionMismatch(next, cmd); handled {
				return stale
			}
		} else if cmd.Task.Kind == state.TaskKindLeaderTransfer {
			if stale, handled := handleLeaderTransferRevisionMismatch(next, cmd); handled {
				return stale
			}
		} else if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != currentRevision {
			return reject(ReasonExpectedRevisionMismatch)
		}
	case command.KindUpsertSlotReplicaMoveTask:
		if cmd.Task == nil || cmd.Task.Kind != state.TaskKindSlotReplicaMove {
			return reject(ReasonInvalidCommand)
		}
		if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != currentRevision {
			return reject(ReasonExpectedRevisionMismatch)
		}
	case command.KindFailTask:
		if stale, handled := handleFailTaskRevisionMismatch(next, cmd); handled {
			return stale
		}
	case command.KindReportTaskProgress:
		if stale, handled := handleTaskProgressRevisionMismatch(next, cmd); handled {
			return stale
		}
	case command.KindReportNodeHealth:
		if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != currentRevision {
			return reject(ReasonExpectedRevisionMismatch)
		}
	default:
		if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != currentRevision {
			if isNonBootstrapIdempotent(*next, cmd) {
				return noop(ReasonNoChange)
			}
			return reject(ReasonExpectedRevisionMismatch)
		}
	}

	beforeTasks := cloneTasks(next.Tasks)
	var result ApplyResult
	switch cmd.Kind {
	case command.KindUpsertNode:
		result = sm.applyUpsertNode(next, cmd)
	case command.KindUpdateControllerVoters:
		result = sm.applyUpdateControllerVoters(next, cmd)
	case command.KindPromoteControllerVoter:
		result = sm.applyPromoteControllerVoter(next, cmd)
	case command.KindReplaceHashSlotTable:
		result = sm.applyReplaceHashSlotTable(next, cmd)
	case command.KindReplaceBackupCoordinationState:
		result = sm.applyReplaceBackupCoordinationState(next, cmd)
	case command.KindReplaceRestoreCoordinationState:
		result = sm.applyReplaceRestoreCoordinationState(next, cmd)
	case command.KindUpsertSlotAssignmentAndTask:
		result = sm.applyUpsertSlotAssignmentAndTask(next, cmd)
	case command.KindUpsertSlotReplicaMoveTask:
		result = sm.applyUpsertSlotReplicaMoveTask(next, cmd)
	case command.KindAdvanceSlotReplicaMovePhase:
		result = sm.applyAdvanceSlotReplicaMovePhase(next, cmd)
	case command.KindCommitSlotReplicaMove:
		result = sm.applyCommitSlotReplicaMove(next, cmd)
	case command.KindCompleteTask:
		result = sm.applyCompleteTask(next, cmd)
	case command.KindFailTask:
		result = sm.applyFailTask(next, cmd)
	case command.KindReportTaskProgress:
		result = sm.applyReportTaskProgress(next, cmd)
	case command.KindReportNodeHealth:
		result = sm.applyReportNodeHealth(next, raftIndex, cmd)
	default:
		result = reject(ReasonInvalidCommand)
	}
	if result.Changed {
		transitions := taskTransitionsForCommand(raftIndex, raftTerm, cmd, beforeTasks, next.Tasks)
		if len(transitions) > 0 {
			result.TaskTransitions = transitions
		}
	}
	return result
}
