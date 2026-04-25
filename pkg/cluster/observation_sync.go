package cluster

import (
	"reflect"
	"sort"
	"sync"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// observationRevisions tracks the latest controller-side revision per read model.
type observationRevisions struct {
	Assignments uint64
	Tasks       uint64
	Nodes       uint64
	Runtime     uint64
}

// observationDeltaRequest asks the leader for changes newer than the supplied revisions.
type observationDeltaRequest struct {
	LeaderID         uint64
	LeaderGeneration uint64
	Revisions        observationRevisions
	RequestedSlots   []uint32
	ForceFullSync    bool
}

// observationDeltaResponse carries either an incremental delta or a full snapshot.
type observationDeltaResponse struct {
	LeaderID         uint64
	LeaderGeneration uint64
	Revisions        observationRevisions
	FullSync         bool

	Assignments  []controllermeta.SlotAssignment
	Tasks        []controllermeta.ReconcileTask
	Nodes        []controllermeta.ClusterNode
	RuntimeViews []controllermeta.SlotRuntimeView

	DeletedTasks        []uint32
	DeletedRuntimeSlots []uint32
}

// observationAppliedState is the follower-side cache view updated by delta apply helpers.
type observationAppliedState struct {
	LeaderID         uint64
	LeaderGeneration uint64
	Assignments      map[uint32]controllermeta.SlotAssignment
	Tasks            map[uint32]controllermeta.ReconcileTask
	Nodes            map[uint64]controllermeta.ClusterNode
	RuntimeViews     map[uint32]controllermeta.SlotRuntimeView
	Revisions        observationRevisions
}

// observationSyncState holds the leader-local latest state plus per-entity change revisions.
type observationSyncState struct {
	mu           sync.RWMutex
	revisions    observationRevisions
	historyFloor observationRevisions

	assignments  map[uint32]controllermeta.SlotAssignment
	tasks        map[uint32]controllermeta.ReconcileTask
	nodes        map[uint64]controllermeta.ClusterNode
	runtimeViews map[uint32]controllermeta.SlotRuntimeView

	assignmentRevisionBySlot map[uint32]uint64
	taskRevisionBySlot       map[uint32]uint64
	nodeRevisionByID         map[uint64]uint64
	runtimeRevisionBySlot    map[uint32]uint64

	deletedTaskRevisionBySlot    map[uint32]uint64
	deletedRuntimeRevisionBySlot map[uint32]uint64
}

// newObservationSyncState creates an empty revision tracker for leader-local control snapshots.
func newObservationSyncState() *observationSyncState {
	return &observationSyncState{
		assignments:                  make(map[uint32]controllermeta.SlotAssignment),
		tasks:                        make(map[uint32]controllermeta.ReconcileTask),
		nodes:                        make(map[uint64]controllermeta.ClusterNode),
		runtimeViews:                 make(map[uint32]controllermeta.SlotRuntimeView),
		assignmentRevisionBySlot:     make(map[uint32]uint64),
		taskRevisionBySlot:           make(map[uint32]uint64),
		nodeRevisionByID:             make(map[uint64]uint64),
		runtimeRevisionBySlot:        make(map[uint32]uint64),
		deletedTaskRevisionBySlot:    make(map[uint32]uint64),
		deletedRuntimeRevisionBySlot: make(map[uint32]uint64),
	}
}

// reset clears all revision-tracked observation state for a new leader generation.
func (s *observationSyncState) reset() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.revisions = observationRevisions{}
	s.historyFloor = observationRevisions{}
	s.assignments = make(map[uint32]controllermeta.SlotAssignment)
	s.tasks = make(map[uint32]controllermeta.ReconcileTask)
	s.nodes = make(map[uint64]controllermeta.ClusterNode)
	s.runtimeViews = make(map[uint32]controllermeta.SlotRuntimeView)
	s.assignmentRevisionBySlot = make(map[uint32]uint64)
	s.taskRevisionBySlot = make(map[uint32]uint64)
	s.nodeRevisionByID = make(map[uint64]uint64)
	s.runtimeRevisionBySlot = make(map[uint32]uint64)
	s.deletedTaskRevisionBySlot = make(map[uint32]uint64)
	s.deletedRuntimeRevisionBySlot = make(map[uint32]uint64)
}

// currentRevisions returns a snapshot of the latest known revisions.
func (s *observationSyncState) currentRevisions() observationRevisions {
	if s == nil {
		return observationRevisions{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revisions
}

// replaceMetadataSnapshot updates the latest nodes, assignments, and tasks from one leader snapshot.
func (s *observationSyncState) replaceMetadataSnapshot(snapshot controllerMetadataSnapshot) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	nextAssignments := make(map[uint32]controllermeta.SlotAssignment, len(snapshot.Assignments))
	for _, assignment := range snapshot.Assignments {
		if assignment.SlotID == 0 {
			continue
		}
		nextAssignments[assignment.SlotID] = cloneSlotAssignment(assignment)
	}
	s.replaceAssignmentsLocked(nextAssignments)

	nextTasks := make(map[uint32]controllermeta.ReconcileTask, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if task.SlotID == 0 {
			continue
		}
		nextTasks[task.SlotID] = task
	}
	s.replaceTasksLocked(nextTasks)

	nextNodes := make(map[uint64]controllermeta.ClusterNode, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID == 0 {
			continue
		}
		nextNodes[node.NodeID] = node
	}
	s.replaceNodesLocked(nextNodes)
}

// replaceRuntimeViews updates the latest aggregated runtime view snapshot from the leader.
func (s *observationSyncState) replaceRuntimeViews(views []controllermeta.SlotRuntimeView) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[uint32]controllermeta.SlotRuntimeView, len(views))
	for _, view := range views {
		if view.SlotID == 0 {
			continue
		}
		next[view.SlotID] = cloneRuntimeView(view)
	}
	s.replaceRuntimeViewsLocked(next)
}

// buildDelta returns the latest incremental changes newer than the supplied revisions.
func (s *observationSyncState) buildDelta(req observationDeltaRequest) observationDeltaResponse {
	if s == nil {
		return observationDeltaResponse{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if req.ForceFullSync || s.requiresFullSyncLocked(req.Revisions) {
		return s.fullSyncLocked()
	}

	wantSlots := requestedSlotSet(req.RequestedSlots)
	resp := observationDeltaResponse{
		Revisions: s.revisions,
	}
	for slotID, revision := range s.assignmentRevisionBySlot {
		if revision <= req.Revisions.Assignments || !slotRequested(wantSlots, slotID) {
			continue
		}
		resp.Assignments = append(resp.Assignments, cloneSlotAssignment(s.assignments[slotID]))
	}
	for slotID, revision := range s.taskRevisionBySlot {
		if revision <= req.Revisions.Tasks || !slotRequested(wantSlots, slotID) {
			continue
		}
		resp.Tasks = append(resp.Tasks, s.tasks[slotID])
	}
	for slotID, revision := range s.deletedTaskRevisionBySlot {
		if revision <= req.Revisions.Tasks || !slotRequested(wantSlots, slotID) {
			continue
		}
		resp.DeletedTasks = append(resp.DeletedTasks, slotID)
	}
	for nodeID, revision := range s.nodeRevisionByID {
		if revision <= req.Revisions.Nodes {
			continue
		}
		resp.Nodes = append(resp.Nodes, s.nodes[nodeID])
	}
	for slotID, revision := range s.runtimeRevisionBySlot {
		if revision <= req.Revisions.Runtime || !slotRequested(wantSlots, slotID) {
			continue
		}
		resp.RuntimeViews = append(resp.RuntimeViews, cloneRuntimeView(s.runtimeViews[slotID]))
	}
	for slotID, revision := range s.deletedRuntimeRevisionBySlot {
		if revision <= req.Revisions.Runtime || !slotRequested(wantSlots, slotID) {
			continue
		}
		resp.DeletedRuntimeSlots = append(resp.DeletedRuntimeSlots, slotID)
	}
	sortObservationDelta(&resp)
	return resp
}

// applyObservationDelta merges a delta or full snapshot into the follower-side caches.
func applyObservationDelta(state *observationAppliedState, delta observationDeltaResponse) {
	if state == nil {
		return
	}

	if delta.FullSync {
		state.Assignments = make(map[uint32]controllermeta.SlotAssignment, len(delta.Assignments))
		state.Tasks = make(map[uint32]controllermeta.ReconcileTask, len(delta.Tasks))
		state.Nodes = make(map[uint64]controllermeta.ClusterNode, len(delta.Nodes))
		state.RuntimeViews = make(map[uint32]controllermeta.SlotRuntimeView, len(delta.RuntimeViews))
	}
	if state.Assignments == nil {
		state.Assignments = make(map[uint32]controllermeta.SlotAssignment)
	}
	if state.Tasks == nil {
		state.Tasks = make(map[uint32]controllermeta.ReconcileTask)
	}
	if state.Nodes == nil {
		state.Nodes = make(map[uint64]controllermeta.ClusterNode)
	}
	if state.RuntimeViews == nil {
		state.RuntimeViews = make(map[uint32]controllermeta.SlotRuntimeView)
	}

	for _, assignment := range delta.Assignments {
		if assignment.SlotID == 0 {
			continue
		}
		state.Assignments[assignment.SlotID] = cloneSlotAssignment(assignment)
	}
	for _, task := range delta.Tasks {
		if task.SlotID == 0 {
			continue
		}
		state.Tasks[task.SlotID] = task
	}
	for _, node := range delta.Nodes {
		if node.NodeID == 0 {
			continue
		}
		state.Nodes[node.NodeID] = node
	}
	for _, view := range delta.RuntimeViews {
		if view.SlotID == 0 {
			continue
		}
		state.RuntimeViews[view.SlotID] = cloneRuntimeView(view)
	}
	for _, slotID := range delta.DeletedTasks {
		delete(state.Tasks, slotID)
	}
	for _, slotID := range delta.DeletedRuntimeSlots {
		delete(state.RuntimeViews, slotID)
	}
	state.LeaderID = delta.LeaderID
	state.LeaderGeneration = delta.LeaderGeneration
	state.Revisions = delta.Revisions
}

func (s *observationSyncState) replaceAssignmentsLocked(next map[uint32]controllermeta.SlotAssignment) {
	for slotID, assignment := range next {
		current, ok := s.assignments[slotID]
		if ok && reflect.DeepEqual(current, assignment) {
			continue
		}
		s.revisions.Assignments++
		s.assignments[slotID] = cloneSlotAssignment(assignment)
		s.assignmentRevisionBySlot[slotID] = s.revisions.Assignments
	}
	for slotID := range s.assignments {
		if _, ok := next[slotID]; ok {
			continue
		}
		s.revisions.Assignments++
		delete(s.assignments, slotID)
		delete(s.assignmentRevisionBySlot, slotID)
	}
}

func (s *observationSyncState) replaceTasksLocked(next map[uint32]controllermeta.ReconcileTask) {
	for slotID, task := range next {
		current, ok := s.tasks[slotID]
		if ok && current == task {
			continue
		}
		s.revisions.Tasks++
		s.tasks[slotID] = task
		s.taskRevisionBySlot[slotID] = s.revisions.Tasks
		delete(s.deletedTaskRevisionBySlot, slotID)
	}
	for slotID := range s.tasks {
		if _, ok := next[slotID]; ok {
			continue
		}
		s.revisions.Tasks++
		delete(s.tasks, slotID)
		delete(s.taskRevisionBySlot, slotID)
		s.deletedTaskRevisionBySlot[slotID] = s.revisions.Tasks
	}
}

func (s *observationSyncState) replaceNodesLocked(next map[uint64]controllermeta.ClusterNode) {
	for nodeID, node := range next {
		current, ok := s.nodes[nodeID]
		if ok && current == node {
			continue
		}
		s.revisions.Nodes++
		s.nodes[nodeID] = node
		s.nodeRevisionByID[nodeID] = s.revisions.Nodes
	}
	for nodeID := range s.nodes {
		if _, ok := next[nodeID]; ok {
			continue
		}
		s.revisions.Nodes++
		delete(s.nodes, nodeID)
		delete(s.nodeRevisionByID, nodeID)
	}
}

func (s *observationSyncState) replaceRuntimeViewsLocked(next map[uint32]controllermeta.SlotRuntimeView) {
	for slotID, view := range next {
		current, ok := s.runtimeViews[slotID]
		if ok && runtimeViewEquivalent(current, view) {
			continue
		}
		s.revisions.Runtime++
		s.runtimeViews[slotID] = cloneRuntimeView(view)
		s.runtimeRevisionBySlot[slotID] = s.revisions.Runtime
		delete(s.deletedRuntimeRevisionBySlot, slotID)
	}
	for slotID := range s.runtimeViews {
		if _, ok := next[slotID]; ok {
			continue
		}
		s.revisions.Runtime++
		delete(s.runtimeViews, slotID)
		delete(s.runtimeRevisionBySlot, slotID)
		s.deletedRuntimeRevisionBySlot[slotID] = s.revisions.Runtime
	}
}

func (s *observationSyncState) requiresFullSyncLocked(revisions observationRevisions) bool {
	return revisions.Assignments < s.historyFloor.Assignments ||
		revisions.Tasks < s.historyFloor.Tasks ||
		revisions.Nodes < s.historyFloor.Nodes ||
		revisions.Runtime < s.historyFloor.Runtime
}

func (s *observationSyncState) fullSyncLocked() observationDeltaResponse {
	resp := observationDeltaResponse{
		Revisions:    s.revisions,
		FullSync:     true,
		Assignments:  make([]controllermeta.SlotAssignment, 0, len(s.assignments)),
		Tasks:        make([]controllermeta.ReconcileTask, 0, len(s.tasks)),
		Nodes:        make([]controllermeta.ClusterNode, 0, len(s.nodes)),
		RuntimeViews: make([]controllermeta.SlotRuntimeView, 0, len(s.runtimeViews)),
	}
	for _, assignment := range s.assignments {
		resp.Assignments = append(resp.Assignments, cloneSlotAssignment(assignment))
	}
	for _, task := range s.tasks {
		resp.Tasks = append(resp.Tasks, task)
	}
	for _, node := range s.nodes {
		resp.Nodes = append(resp.Nodes, node)
	}
	for _, view := range s.runtimeViews {
		resp.RuntimeViews = append(resp.RuntimeViews, cloneRuntimeView(view))
	}
	sortObservationDelta(&resp)
	return resp
}

func requestedSlotSet(slotIDs []uint32) map[uint32]struct{} {
	if len(slotIDs) == 0 {
		return nil
	}
	out := make(map[uint32]struct{}, len(slotIDs))
	for _, slotID := range slotIDs {
		if slotID == 0 {
			continue
		}
		out[slotID] = struct{}{}
	}
	return out
}

func slotRequested(slots map[uint32]struct{}, slotID uint32) bool {
	if len(slots) == 0 {
		return true
	}
	_, ok := slots[slotID]
	return ok
}

func reconcileScopeFromObservationDelta(delta observationDeltaResponse) []uint32 {
	if len(delta.Nodes) > 0 {
		return nil
	}
	return affectedSlotsFromObservationDelta(delta)
}

func sortObservationDelta(resp *observationDeltaResponse) {
	if resp == nil {
		return
	}
	sort.Slice(resp.Assignments, func(i, j int) bool { return resp.Assignments[i].SlotID < resp.Assignments[j].SlotID })
	sort.Slice(resp.Tasks, func(i, j int) bool { return resp.Tasks[i].SlotID < resp.Tasks[j].SlotID })
	sort.Slice(resp.Nodes, func(i, j int) bool { return resp.Nodes[i].NodeID < resp.Nodes[j].NodeID })
	sort.Slice(resp.RuntimeViews, func(i, j int) bool { return resp.RuntimeViews[i].SlotID < resp.RuntimeViews[j].SlotID })
	sort.Slice(resp.DeletedTasks, func(i, j int) bool { return resp.DeletedTasks[i] < resp.DeletedTasks[j] })
	sort.Slice(resp.DeletedRuntimeSlots, func(i, j int) bool { return resp.DeletedRuntimeSlots[i] < resp.DeletedRuntimeSlots[j] })
}

type observationNodeStatusChange struct {
	nodeID uint64
	from   controllermeta.NodeStatus
	to     controllermeta.NodeStatus
}

func cloneObservationNodesByID(nodes map[uint64]controllermeta.ClusterNode) map[uint64]controllermeta.ClusterNode {
	if len(nodes) == 0 {
		return nil
	}
	cloned := make(map[uint64]controllermeta.ClusterNode, len(nodes))
	for nodeID, node := range nodes {
		cloned[nodeID] = node
	}
	return cloned
}

func diffObservationNodeStatuses(before, after map[uint64]controllermeta.ClusterNode) []observationNodeStatusChange {
	if len(after) == 0 {
		return nil
	}
	changes := make([]observationNodeStatusChange, 0, len(after))
	for nodeID, node := range after {
		prev, ok := before[nodeID]
		if ok && prev.Status == node.Status {
			continue
		}
		from := controllermeta.NodeStatusUnknown
		if ok {
			from = prev.Status
		}
		changes = append(changes, observationNodeStatusChange{
			nodeID: nodeID,
			from:   from,
			to:     node.Status,
		})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].nodeID < changes[j].nodeID })
	return changes
}
