package fsm

import (
	"reflect"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

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
