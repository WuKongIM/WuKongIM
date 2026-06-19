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

func (sm *StateMachine) applyCompleteTask(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.TaskResult == nil || cmd.TaskResult.TaskID == "" {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, cmd.TaskResult.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	if guard := taskResultGuard(next.Tasks[idx], cmd.TaskResult); guard != (ApplyResult{}) {
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
	if guard := taskResultGuard(next.Tasks[idx], cmd.TaskResult); guard != (ApplyResult{}) {
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
	if guard := taskProgressGuard(next.Tasks[idx], cmd.TaskProgress); guard != (ApplyResult{}) {
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
	if cmd.TaskProgress.ParticipantAttempt == current.Attempt && current.Status == state.TaskParticipantStatusFailed && cmd.TaskProgress.Status == state.TaskParticipantStatusDone {
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
