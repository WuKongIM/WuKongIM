package fsm

import (
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// Command handlers mutate only the in-memory candidate state. ApplyBatch owns durable save and publication.
func (sm *StateMachine) applyInit(next *state.ClusterState, raftIndex uint64, cmd command.Command) ApplyResult {
	if cmd.Init == nil {
		return reject(ReasonInvalidCommand)
	}
	initial, err := initialStateFromCommand(cmd, raftIndex)
	if err != nil {
		return reject(ReasonInvalidState)
	}
	if next.Revision == 0 {
		*next = initial
		return changed()
	}
	if equivalentInit(*next, initial) {
		return noop(ReasonNoChange)
	}
	return reject(ReasonInitConflict)
}

func (sm *StateMachine) applyUpsertNode(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.Node == nil {
		return reject(ReasonInvalidCommand)
	}
	before := next.Clone()
	node := cloneNode(*cmd.Node)
	upsertNode(next, node)
	next.Normalize()
	if reflect.DeepEqual(before.Nodes, next.Nodes) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyUpdateControllerVoters(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 {
		return reject(ReasonInvalidCommand)
	}
	before := next.Clone()
	next.Controllers = append([]state.ControllerVoter(nil), cmd.Controllers...)
	next.Normalize()
	if reflect.DeepEqual(before.Controllers, next.Controllers) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyReplaceHashSlotTable(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.HashSlots == nil {
		return reject(ReasonInvalidCommand)
	}
	before := next.Clone()
	next.HashSlots = cloneHashSlotTable(*cmd.HashSlots)
	next.Normalize()
	if reflect.DeepEqual(before.HashSlots, next.HashSlots) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyUpsertSlotAssignmentAndTask(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.Assignment == nil || cmd.Task == nil {
		return reject(ReasonInvalidCommand)
	}
	if cmd.Task.SlotID != cmd.Assignment.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	before := next.Clone()
	upsertAssignment(next, cloneAssignment(*cmd.Assignment))
	upsertTask(next, cloneTask(*cmd.Task))
	next.Normalize()
	if reflect.DeepEqual(before.Slots, next.Slots) && reflect.DeepEqual(before.Tasks, next.Tasks) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyUpsertSlotReplicaMoveTask(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.Task == nil || cmd.Task.Kind != state.TaskKindSlotReplicaMove {
		return reject(ReasonInvalidCommand)
	}
	before := next.Clone()
	upsertTask(next, cloneTask(*cmd.Task))
	next.Normalize()
	if reflect.DeepEqual(before.Tasks, next.Tasks) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyAdvanceSlotReplicaMovePhase(next *state.ClusterState, cmd command.Command) ApplyResult {
	phase := cmd.SlotReplicaMovePhase
	if next.Revision == 0 || phase == nil || phase.TaskID == "" || phase.SlotID == 0 || phase.ConfigEpoch == 0 || phase.NextStep == "" {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, phase.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	task := next.Tasks[idx]
	if task.SlotID != phase.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	if task.Kind != state.TaskKindSlotReplicaMove {
		return noop(ReasonTaskKindMismatch)
	}
	if task.ConfigEpoch != phase.ConfigEpoch {
		return noop(ReasonTaskEpochMismatch)
	}
	if task.Attempt != phase.Attempt {
		return noop(ReasonTaskAttemptMismatch)
	}
	if task.PhaseIndex != phase.ExpectedPhaseIndex {
		return reject(ReasonTaskPhaseMismatch)
	}
	if reason := validateSlotReplicaMovePhaseAdvance(task, phase); reason != "" {
		return reject(reason)
	}
	before := next.Clone()
	next.Tasks[idx].Step = phase.NextStep
	next.Tasks[idx].PhaseIndex++
	next.Tasks[idx].ObservedConfigIndex = phase.ObservedConfigIndex
	next.Tasks[idx].ObservedVoters = append([]uint64(nil), phase.ObservedVoters...)
	next.Tasks[idx].ObservedLearners = append([]uint64(nil), phase.ObservedLearners...)
	next.Normalize()
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyCommitSlotReplicaMove(next *state.ClusterState, cmd command.Command) ApplyResult {
	commit := cmd.SlotReplicaMoveCommit
	if next.Revision == 0 || commit == nil || commit.TaskID == "" || commit.SlotID == 0 || commit.ConfigEpoch == 0 {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, commit.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	task := next.Tasks[idx]
	if task.SlotID != commit.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	if task.Kind != state.TaskKindSlotReplicaMove {
		return noop(ReasonTaskKindMismatch)
	}
	if task.ConfigEpoch != commit.ConfigEpoch {
		return noop(ReasonTaskEpochMismatch)
	}
	if task.Attempt != commit.Attempt {
		return noop(ReasonTaskAttemptMismatch)
	}
	if task.Step != state.TaskStepCommitAssignment {
		return reject(ReasonTaskStepMismatch)
	}
	if commit.ObservedConfigIndex == 0 {
		return reject(ReasonTaskObservedConfigMissing)
	}
	if !sameUint64Set(commit.ObservedVoters, task.TargetPeers) {
		return reject(ReasonTaskObservedVotersMismatch)
	}
	if task.ObservedConfigIndex == 0 {
		return reject(ReasonTaskObservedConfigMissing)
	}
	if !sameUint64Set(task.ObservedVoters, task.TargetPeers) {
		return reject(ReasonTaskObservedVotersMismatch)
	}
	assignmentIdx := -1
	for i := range next.Slots {
		if next.Slots[i].SlotID == task.SlotID {
			assignmentIdx = i
			break
		}
	}
	if assignmentIdx < 0 {
		return reject(ReasonInvalidState)
	}
	assignment := next.Slots[assignmentIdx]
	if assignment.ConfigEpoch != task.ConfigEpoch {
		return reject(ReasonTaskEpochMismatch)
	}
	if !containsUint64(assignment.DesiredPeers, task.SourceNode) || containsUint64(assignment.DesiredPeers, task.TargetNode) {
		return reject(ReasonInvalidState)
	}
	if !sameUint64Set(task.TargetPeers, replacePeer(assignment.DesiredPeers, task.SourceNode, task.TargetNode)) {
		return reject(ReasonInvalidState)
	}
	before := next.Clone()
	next.Slots[assignmentIdx].DesiredPeers = append([]uint64(nil), task.TargetPeers...)
	next.Slots[assignmentIdx].ConfigEpoch++
	if !containsUint64(next.Slots[assignmentIdx].DesiredPeers, next.Slots[assignmentIdx].PreferredLeader) {
		next.Slots[assignmentIdx].PreferredLeader = task.TargetNode
	}
	next.Tasks = append(next.Tasks[:idx], next.Tasks[idx+1:]...)
	next.Normalize()
	return validateChanged(next, before, cmd)
}

func validateSlotReplicaMovePhaseAdvance(task state.ReconcileTask, phase *command.SlotReplicaMovePhaseAdvance) string {
	switch task.Step {
	case state.TaskStepOpenLearner:
		if phase.NextStep != state.TaskStepAddLearner {
			return ReasonTaskStepMismatch
		}
		return ""
	case state.TaskStepAddLearner:
		if phase.NextStep != state.TaskStepPromoteLearner && phase.NextStep != state.TaskStepRemoveVoter {
			return ReasonTaskStepMismatch
		}
		if phase.ObservedConfigIndex == 0 {
			return ReasonTaskObservedConfigMissing
		}
		if phase.NextStep == state.TaskStepPromoteLearner {
			if !sameUint64Set(phase.ObservedVoters, sourcePeersForSlotReplicaMove(task)) {
				return ReasonTaskObservedVotersMismatch
			}
			if !containsUint64(phase.ObservedLearners, task.TargetNode) {
				return ReasonTaskObservedLearnersMismatch
			}
			return ""
		}
		if !containsUint64(phase.ObservedVoters, task.TargetNode) {
			return ReasonTaskObservedVotersMismatch
		}
		return ""
	case state.TaskStepPromoteLearner:
		if phase.NextStep != state.TaskStepRemoveVoter {
			return ReasonTaskStepMismatch
		}
		if phase.ObservedConfigIndex == 0 {
			return ReasonTaskObservedConfigMissing
		}
		if !containsUint64(phase.ObservedVoters, task.TargetNode) {
			return ReasonTaskObservedVotersMismatch
		}
		return ""
	case state.TaskStepRemoveVoter:
		if phase.NextStep != state.TaskStepRemoveVoter && phase.NextStep != state.TaskStepCommitAssignment {
			return ReasonTaskStepMismatch
		}
		if phase.ObservedConfigIndex == 0 {
			return ReasonTaskObservedConfigMissing
		}
		if phase.NextStep == state.TaskStepCommitAssignment {
			if !sameUint64Set(phase.ObservedVoters, task.TargetPeers) {
				return ReasonTaskObservedVotersMismatch
			}
			return ""
		}
		if !containsUint64(phase.ObservedVoters, task.SourceNode) || !containsUint64(phase.ObservedVoters, task.TargetNode) {
			return ReasonTaskObservedVotersMismatch
		}
		return ""
	default:
		return ReasonTaskStepMismatch
	}
}

func sourcePeersForSlotReplicaMove(task state.ReconcileTask) []uint64 {
	return replacePeer(task.TargetPeers, task.TargetNode, task.SourceNode)
}

func (sm *StateMachine) applyCompleteTask(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.TaskResult == nil || cmd.TaskResult.TaskID == "" {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, cmd.TaskResult.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	if guard := taskResultGuard(next.Tasks[idx], cmd.TaskResult); hasApplyOutcome(guard) {
		return guard
	}
	before := next.Clone()
	next.Tasks = append(next.Tasks[:idx], next.Tasks[idx+1:]...)
	next.Normalize()
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyFailTask(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.TaskResult == nil || cmd.TaskResult.TaskID == "" {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, cmd.TaskResult.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	if guard := taskResultGuard(next.Tasks[idx], cmd.TaskResult); hasApplyOutcome(guard) {
		return guard
	}
	before := next.Clone()
	next.Tasks[idx].Status = state.TaskStatusFailed
	next.Tasks[idx].Attempt++
	next.Tasks[idx].LastError = truncateUTF8(cmd.TaskResult.Err, MaxTaskLastErrorBytes)
	if next.Tasks[idx].CompletionPolicy == state.TaskCompletionPolicyAllTargetPeers {
		next.Tasks[idx].ParticipantProgress = nil
		for _, peerID := range next.Tasks[idx].TargetPeers {
			next.Tasks[idx].ParticipantProgress = append(next.Tasks[idx].ParticipantProgress, state.TaskParticipantProgress{NodeID: peerID, Status: state.TaskParticipantStatusPending})
		}
	}
	next.Normalize()
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyReportTaskProgress(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.TaskProgress == nil || cmd.TaskProgress.TaskID == "" {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, cmd.TaskProgress.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	if guard := taskProgressGuard(next.Tasks[idx], cmd.TaskProgress); hasApplyOutcome(guard) {
		return guard
	}
	participantIdx := findParticipant(next.Tasks[idx].ParticipantProgress, cmd.TaskProgress.ParticipantNodeID)
	if participantIdx < 0 {
		return reject(ReasonTaskParticipantUnexpected)
	}
	current := next.Tasks[idx].ParticipantProgress[participantIdx]
	if cmd.TaskProgress.ParticipantAttempt < current.Attempt {
		return noop(ReasonTaskParticipantAttemptStale)
	}
	before := next.Clone()
	next.Tasks[idx].ParticipantProgress[participantIdx].Status = cmd.TaskProgress.Status
	next.Tasks[idx].ParticipantProgress[participantIdx].Attempt = cmd.TaskProgress.ParticipantAttempt
	next.Tasks[idx].ParticipantProgress[participantIdx].LastError = ""
	if cmd.TaskProgress.Status == state.TaskParticipantStatusFailed {
		next.Tasks[idx].Status = state.TaskStatusFailed
		next.Tasks[idx].ParticipantProgress[participantIdx].Attempt++
		next.Tasks[idx].ParticipantProgress[participantIdx].LastError = truncateUTF8(cmd.TaskProgress.Err, MaxTaskLastErrorBytes)
	}
	next.Normalize()
	if reflect.DeepEqual(before.Tasks, next.Tasks) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}

func (sm *StateMachine) applyReportNodeHealth(next *state.ClusterState, raftIndex uint64, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.NodeHealth == nil {
		return reject(ReasonInvalidCommand)
	}
	before := next.Clone()
	report := *cmd.NodeHealth
	report.AppliedRaftIndex = raftIndex
	upsertNodeHealthReport(next, report)
	next.Normalize()
	if err := next.Validate(); err != nil {
		*next = before
		return reject(ReasonInvalidState)
	}
	if reflect.DeepEqual(before.NodeHealthReports, next.NodeHealthReports) {
		return noop(ReasonNoChange)
	}
	return ApplyResult{Updated: true}
}

func taskResultGuard(task state.ReconcileTask, result *command.TaskResult) ApplyResult {
	if result == nil || result.TaskID == "" || result.SlotID == 0 || result.TaskKind == "" || result.ConfigEpoch == 0 {
		return reject(ReasonInvalidTaskResult)
	}
	if result.SlotID != task.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	if result.TaskKind != task.Kind {
		return noop(ReasonTaskKindMismatch)
	}
	if result.ConfigEpoch != task.ConfigEpoch {
		return noop(ReasonTaskEpochMismatch)
	}
	if result.Attempt != task.Attempt {
		return noop(ReasonTaskAttemptMismatch)
	}
	return ApplyResult{}
}

func taskProgressGuard(task state.ReconcileTask, progress *command.TaskProgress) ApplyResult {
	if progress == nil || progress.TaskID == "" || progress.SlotID == 0 || progress.TaskKind == "" || progress.ConfigEpoch == 0 || progress.ParticipantNodeID == 0 {
		return reject(ReasonInvalidTaskResult)
	}
	if progress.SlotID != task.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	if progress.TaskKind != task.Kind {
		return noop(ReasonTaskKindMismatch)
	}
	if progress.ConfigEpoch != task.ConfigEpoch {
		return noop(ReasonTaskEpochMismatch)
	}
	if progress.TaskAttempt != task.Attempt {
		return noop(ReasonTaskAttemptMismatch)
	}
	switch progress.Status {
	case state.TaskParticipantStatusPending, state.TaskParticipantStatusDone, state.TaskParticipantStatusFailed:
	default:
		return reject(ReasonInvalidTaskResult)
	}
	return ApplyResult{}
}

func initialStateFromCommand(cmd command.Command, raftIndex uint64) (state.ClusterState, error) {
	table, err := state.BuildInitialHashSlotTable(cmd.Init.Config.SlotCount, cmd.Init.Config.HashSlotCount)
	if err != nil {
		return state.ClusterState{}, err
	}
	st := state.ClusterState{
		SchemaVersion:    state.CurrentSchemaVersion,
		ClusterID:        cmd.Init.ClusterID,
		Revision:         1,
		AppliedRaftIndex: raftIndex,
		UpdatedAt:        commandIssuedAt(cmd.IssuedAt),
		Config:           cmd.Init.Config,
		Controllers:      append([]state.ControllerVoter(nil), cmd.Init.Controllers...),
		Nodes:            cloneNodes(cmd.Init.Nodes),
		Slots:            []state.SlotAssignment{},
		HashSlots:        table,
		Tasks:            []state.ReconcileTask{},
	}
	st.Normalize()
	if err := st.Validate(); err != nil {
		return state.ClusterState{}, err
	}
	return st, nil
}
