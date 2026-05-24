package fsm

import (
	"reflect"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

const (
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
	if cmd.TaskResult.SlotID != 0 && cmd.TaskResult.SlotID != next.Tasks[idx].SlotID {
		return reject(ReasonTaskSlotMismatch)
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
	if cmd.TaskResult.SlotID != 0 && cmd.TaskResult.SlotID != next.Tasks[idx].SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	before := next.Clone()
	next.Tasks[idx].Status = state.TaskStatusFailed
	next.Tasks[idx].Attempt++
	next.Tasks[idx].LastError = truncateUTF8(cmd.TaskResult.Err, MaxTaskLastErrorBytes)
	next.Normalize()
	return validateChanged(next, before, cmd)
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

func handleBootstrapRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool) {
	if cmd.ExpectedRevision == nil || *cmd.ExpectedRevision == current.Revision {
		return ApplyResult{}, false
	}
	if cmd.Assignment == nil || cmd.Task == nil || cmd.Task.Kind != state.TaskKindBootstrap {
		return reject(ReasonExpectedRevisionMismatch), true
	}
	slotID := cmd.Assignment.SlotID
	if existing, ok := findAssignment(current.Slots, slotID); ok {
		if equivalentAssignment(existing, *cmd.Assignment) {
			return noop(ReasonStaleBootstrapObsolete), true
		}
		return noop(ReasonStaleBootstrapObsolete), true
	}
	if existing, ok := findTaskBySlot(current.Tasks, slotID); ok {
		if equivalentTask(existing, *cmd.Task) {
			return noop(ReasonStaleBootstrapObsolete), true
		}
		return noop(ReasonStaleBootstrapObsolete), true
	}
	return reject(ReasonStaleBootstrapMissingSlot), true
}

func handleFailTaskRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool) {
	if cmd.ExpectedRevision == nil || *cmd.ExpectedRevision == current.Revision {
		return ApplyResult{}, false
	}
	if cmd.TaskResult == nil || cmd.TaskResult.TaskID == "" {
		return reject(ReasonInvalidTaskResult), true
	}
	if findTaskByID(current.Tasks, cmd.TaskResult.TaskID) < 0 {
		return noop(ReasonTaskMissing), true
	}
	return reject(ReasonExpectedRevisionMismatch), true
}

func isNonBootstrapIdempotent(current state.ClusterState, cmd command.Command) bool {
	switch cmd.Kind {
	case command.KindUpsertNode:
		if cmd.Node == nil {
			return false
		}
		idx := findNode(current.Nodes, cmd.Node.NodeID)
		return idx >= 0 && equivalentNode(current.Nodes[idx], *cmd.Node)
	case command.KindUpdateControllerVoters:
		candidate := current.Clone()
		candidate.Controllers = append([]state.ControllerVoter(nil), cmd.Controllers...)
		candidate.Normalize()
		return reflect.DeepEqual(current.Controllers, candidate.Controllers)
	case command.KindReplaceHashSlotTable:
		return cmd.HashSlots != nil && reflect.DeepEqual(current.HashSlots, cloneHashSlotTable(*cmd.HashSlots))
	case command.KindCompleteTask:
		return cmd.TaskResult != nil && cmd.TaskResult.TaskID != "" && findTaskByID(current.Tasks, cmd.TaskResult.TaskID) < 0
	default:
		return false
	}
}

func validateChanged(next *state.ClusterState, before state.ClusterState, cmd command.Command) ApplyResult {
	next.Revision++
	next.UpdatedAt = nextUpdatedAt(before.UpdatedAt, cmd.IssuedAt)
	if err := next.Validate(); err != nil {
		*next = before
		return reject(ReasonInvalidState)
	}
	return changed()
}

// nextUpdatedAt is deterministic for replay: non-zero command timestamps are
// normalized to UTC, while zero timestamps preserve the previous durable value.
func nextUpdatedAt(previous time.Time, issuedAt time.Time) time.Time {
	if issuedAt.IsZero() {
		return previous.UTC()
	}
	return issuedAt.UTC()
}

// commandIssuedAt uses zero time for zero-timestamp init commands because no
// previous durable state exists yet.
func commandIssuedAt(issuedAt time.Time) time.Time {
	if issuedAt.IsZero() {
		return time.Time{}
	}
	return issuedAt.UTC()
}

func changed() ApplyResult {
	return ApplyResult{Changed: true}
}

func noop(reason string) ApplyResult {
	return ApplyResult{Noop: true, Reason: reason}
}

func reject(reason string) ApplyResult {
	return ApplyResult{Rejected: true, Reason: reason}
}

func upsertNode(st *state.ClusterState, node state.Node) {
	idx := findNode(st.Nodes, node.NodeID)
	if idx >= 0 {
		st.Nodes[idx] = node
		return
	}
	st.Nodes = append(st.Nodes, node)
}

func upsertAssignment(st *state.ClusterState, assignment state.SlotAssignment) {
	for i := range st.Slots {
		if st.Slots[i].SlotID == assignment.SlotID {
			st.Slots[i] = assignment
			return
		}
	}
	st.Slots = append(st.Slots, assignment)
}

func upsertTask(st *state.ClusterState, task state.ReconcileTask) {
	for i := range st.Tasks {
		if st.Tasks[i].TaskID == task.TaskID {
			st.Tasks[i] = task
			return
		}
	}
	st.Tasks = append(st.Tasks, task)
}

func equivalentInit(current state.ClusterState, initial state.ClusterState) bool {
	current = current.Clone()
	initial = initial.Clone()
	current.Revision = 1
	initial.Revision = 1
	current.AppliedRaftIndex = 0
	initial.AppliedRaftIndex = 0
	current.UpdatedAt = time.Time{}
	initial.UpdatedAt = time.Time{}
	current.Checksum = ""
	initial.Checksum = ""
	current.Normalize()
	initial.Normalize()
	return reflect.DeepEqual(current.ClusterID, initial.ClusterID) &&
		reflect.DeepEqual(current.Config, initial.Config) &&
		reflect.DeepEqual(current.Controllers, initial.Controllers) &&
		reflect.DeepEqual(current.Nodes, initial.Nodes) &&
		reflect.DeepEqual(current.HashSlots, initial.HashSlots) &&
		len(current.Slots) == 0 && len(current.Tasks) == 0
}

func equivalentNode(a, b state.Node) bool {
	left := cloneNode(a)
	right := cloneNode(b)
	sort.Slice(left.Roles, func(i, j int) bool { return left.Roles[i] < left.Roles[j] })
	sort.Slice(right.Roles, func(i, j int) bool { return right.Roles[i] < right.Roles[j] })
	if left.CapacityWeight == 0 {
		left.CapacityWeight = 1
	}
	if right.CapacityWeight == 0 {
		right.CapacityWeight = 1
	}
	return reflect.DeepEqual(left, right)
}

func equivalentAssignment(a, b state.SlotAssignment) bool {
	left := cloneAssignment(a)
	right := cloneAssignment(b)
	sort.Slice(left.DesiredPeers, func(i, j int) bool { return left.DesiredPeers[i] < left.DesiredPeers[j] })
	sort.Slice(right.DesiredPeers, func(i, j int) bool { return right.DesiredPeers[i] < right.DesiredPeers[j] })
	return reflect.DeepEqual(left, right)
}

func equivalentTask(a, b state.ReconcileTask) bool {
	left := cloneTask(a)
	right := cloneTask(b)
	sort.Slice(left.TargetPeers, func(i, j int) bool { return left.TargetPeers[i] < left.TargetPeers[j] })
	sort.Slice(right.TargetPeers, func(i, j int) bool { return right.TargetPeers[i] < right.TargetPeers[j] })
	return reflect.DeepEqual(left, right)
}

func findNode(nodes []state.Node, nodeID uint64) int {
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			return i
		}
	}
	return -1
}

func findAssignment(assignments []state.SlotAssignment, slotID uint32) (state.SlotAssignment, bool) {
	for _, assignment := range assignments {
		if assignment.SlotID == slotID {
			return assignment, true
		}
	}
	return state.SlotAssignment{}, false
}

func findTaskBySlot(tasks []state.ReconcileTask, slotID uint32) (state.ReconcileTask, bool) {
	for _, task := range tasks {
		if task.SlotID == slotID {
			return task, true
		}
	}
	return state.ReconcileTask{}, false
}

func findTaskByID(tasks []state.ReconcileTask, taskID string) int {
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			return i
		}
	}
	return -1
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(s)) <= maxBytes {
		return s
	}
	trimmed := string([]byte(s)[:maxBytes])
	for !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

func cloneNodes(nodes []state.Node) []state.Node {
	out := make([]state.Node, len(nodes))
	for i := range nodes {
		out[i] = cloneNode(nodes[i])
	}
	return out
}

func cloneNode(node state.Node) state.Node {
	node.Roles = append([]state.NodeRole(nil), node.Roles...)
	return node
}

func cloneAssignment(assignment state.SlotAssignment) state.SlotAssignment {
	assignment.DesiredPeers = append([]uint64(nil), assignment.DesiredPeers...)
	return assignment
}

func cloneTask(task state.ReconcileTask) state.ReconcileTask {
	task.TargetPeers = append([]uint64(nil), task.TargetPeers...)
	return task
}

func cloneHashSlotTable(table state.HashSlotTable) state.HashSlotTable {
	table.Ranges = append([]state.HashSlotRange(nil), table.Ranges...)
	return table
}
