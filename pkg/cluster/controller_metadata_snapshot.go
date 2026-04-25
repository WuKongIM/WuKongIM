package cluster

import (
	"context"
	"sort"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// controllerMetadataSnapshot is a leader-local, read-only cache of controller metadata.
//
// Pebble-backed controller meta is the source of truth. This snapshot exists only as an internal
// fast path; callers must fall back to store reads when the snapshot is not ready or is dirty.
type controllerMetadataSnapshot struct {
	// Nodes is the full list of controller-known cluster nodes.
	Nodes []controllermeta.ClusterNode
	// NodesByID indexes Nodes by node ID.
	NodesByID map[uint64]controllermeta.ClusterNode
	// Assignments is the full list of slot assignments.
	Assignments []controllermeta.SlotAssignment
	// AssignmentsBySlot indexes Assignments by slot ID.
	AssignmentsBySlot map[uint32]controllermeta.SlotAssignment
	// Tasks is the full list of reconcile tasks.
	Tasks []controllermeta.ReconcileTask
	// TasksBySlot indexes Tasks by slot ID.
	TasksBySlot map[uint32]controllermeta.ReconcileTask

	// LeaderID is the controller leader this snapshot is associated with.
	LeaderID multiraft.NodeID
	// Generation increments on each local leader acquisition.
	Generation uint64
	// Ready means the snapshot is fully loaded and can be used by fast paths (if not Dirty).
	Ready bool
	// Dirty means the snapshot may be stale and callers must fall back to store reads.
	Dirty bool
}

func cloneUint64s(src []uint64) []uint64 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]uint64, len(src))
	copy(dst, src)
	return dst
}

func cloneSlotAssignment(src controllermeta.SlotAssignment) controllermeta.SlotAssignment {
	out := src
	out.DesiredPeers = cloneUint64s(src.DesiredPeers)
	return out
}

func (s controllerMetadataSnapshot) clone() controllerMetadataSnapshot {
	out := controllerMetadataSnapshot{
		LeaderID:   s.LeaderID,
		Generation: s.Generation,
		Ready:      s.Ready,
		Dirty:      s.Dirty,
	}
	if len(s.Nodes) > 0 {
		out.Nodes = append([]controllermeta.ClusterNode(nil), s.Nodes...)
	}
	if len(s.Assignments) > 0 {
		out.Assignments = make([]controllermeta.SlotAssignment, len(s.Assignments))
		for i := range s.Assignments {
			out.Assignments[i] = cloneSlotAssignment(s.Assignments[i])
		}
	}
	if len(s.Tasks) > 0 {
		out.Tasks = append([]controllermeta.ReconcileTask(nil), s.Tasks...)
	}
	if len(s.NodesByID) > 0 {
		out.NodesByID = make(map[uint64]controllermeta.ClusterNode, len(s.NodesByID))
		for k, v := range s.NodesByID {
			out.NodesByID[k] = v
		}
	}
	if len(s.AssignmentsBySlot) > 0 {
		out.AssignmentsBySlot = make(map[uint32]controllermeta.SlotAssignment, len(s.AssignmentsBySlot))
		for k, v := range s.AssignmentsBySlot {
			out.AssignmentsBySlot[k] = cloneSlotAssignment(v)
		}
	}
	if len(s.TasksBySlot) > 0 {
		out.TasksBySlot = make(map[uint32]controllermeta.ReconcileTask, len(s.TasksBySlot))
		for k, v := range s.TasksBySlot {
			out.TasksBySlot[k] = v
		}
	}
	return out
}

// controllerMetadataSnapshotState owns the leader-local controller metadata snapshot lifecycle.
//
// It tracks readiness/dirty state and coalesces reload requests so that hot-path readers can use
// a clean snapshot when available and fall back to Pebble-backed reads otherwise.
type controllerMetadataSnapshotState struct {
	mu              sync.RWMutex
	reloadMu        sync.Mutex
	snapshot        controllerMetadataSnapshot
	dirtySeq        uint64
	reloadPending   bool
	reloadScheduled bool

	// loadFn can be overridden by tests to deterministically block reloads.
	loadFn func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error)
	// onLoaded observes snapshots that were successfully installed for the active leader generation.
	onLoaded func(controllerMetadataSnapshot)
}

func (s *controllerMetadataSnapshotState) snapshotIfReadyClean() (controllerMetadataSnapshot, bool) {
	if s == nil {
		return controllerMetadataSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.snapshot.Ready || s.snapshot.Dirty {
		return controllerMetadataSnapshot{}, false
	}
	return s.snapshot.clone(), true
}

func (s *controllerMetadataSnapshotState) markDirtyOnly() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirtySeq++
	s.snapshot.Dirty = true
}

func (s *controllerMetadataSnapshotState) markDirtyAndEnqueueReload(ctx context.Context, store *controllermeta.Store, local multiraft.NodeID, timeout time.Duration) {
	if s == nil {
		return
	}

	shouldStart := false
	s.mu.Lock()
	s.dirtySeq++
	s.snapshot.Dirty = true
	if s.snapshot.LeaderID == local && s.snapshot.Generation > 0 {
		s.reloadPending = true
		if !s.reloadScheduled {
			s.reloadScheduled = true
			shouldStart = true
		}
	}
	s.mu.Unlock()

	if !shouldStart {
		return
	}

	go s.reloadWorker(ctx, store, local, timeout)
}

func (s *controllerMetadataSnapshotState) invalidate(newLeader multiraft.NodeID) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = controllerMetadataSnapshot{
		LeaderID:   newLeader,
		Generation: s.snapshot.Generation,
		Ready:      false,
		Dirty:      false,
	}
	s.reloadPending = false
	s.reloadScheduled = false
}

func (s *controllerMetadataSnapshotState) onLocalLeaderAcquiredAsync(ctx context.Context, store *controllermeta.Store, local multiraft.NodeID, timeout time.Duration) {
	if s == nil {
		return
	}

	shouldStart := false
	s.mu.Lock()
	s.snapshot = controllerMetadataSnapshot{
		LeaderID:   local,
		Generation: s.snapshot.Generation + 1,
		Ready:      false,
		Dirty:      false,
	}
	s.reloadPending = false
	s.reloadScheduled = false
	s.reloadPending = true
	s.reloadScheduled = true
	shouldStart = true
	s.mu.Unlock()

	if shouldStart {
		go s.reloadWorker(ctx, store, local, timeout)
	}
}

func (s *controllerMetadataSnapshotState) reloadIfLeader(ctx context.Context, store *controllermeta.Store, leader multiraft.NodeID) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	generation := s.snapshot.Generation
	currentLeader := s.snapshot.LeaderID
	s.mu.RUnlock()
	if currentLeader != leader || leader == 0 || generation == 0 {
		return nil
	}
	return s.reload(ctx, store, leader, generation)
}

func (s *controllerMetadataSnapshotState) reloadWorker(ctx context.Context, store *controllermeta.Store, local multiraft.NodeID, timeout time.Duration) {
	if s == nil {
		return
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	for {
		s.mu.Lock()
		if !s.reloadPending || s.snapshot.LeaderID != local || s.snapshot.Generation == 0 {
			s.reloadPending = false
			s.reloadScheduled = false
			s.mu.Unlock()
			return
		}
		s.reloadPending = false
		leader := s.snapshot.LeaderID
		generation := s.snapshot.Generation
		s.mu.Unlock()

		attemptCtx := ctx
		if attemptCtx == nil {
			attemptCtx = context.Background()
		}
		reloadCtx, cancel := context.WithTimeout(attemptCtx, timeout)
		err := s.reload(reloadCtx, store, leader, generation)
		cancel()
		if err != nil {
			// Leave the snapshot dirty; callers will fall back. Retry is triggered by future dirties.
			s.mu.Lock()
			if s.reloadPending && s.snapshot.LeaderID == local && s.snapshot.Generation == generation {
				s.mu.Unlock()
				continue
			}
			s.reloadScheduled = false
			s.mu.Unlock()
			return
		}

		s.mu.Lock()
		if s.snapshot.LeaderID != local || s.snapshot.Generation != generation {
			s.reloadPending = false
			s.reloadScheduled = false
			s.mu.Unlock()
			return
		}
		if s.snapshot.Dirty {
			s.reloadPending = true
			s.mu.Unlock()
			continue
		}
		if s.reloadPending {
			s.mu.Unlock()
			continue
		}
		s.reloadScheduled = false
		s.mu.Unlock()
		return
	}
}

func (s *controllerMetadataSnapshotState) reload(ctx context.Context, store *controllermeta.Store, leader multiraft.NodeID, generation uint64) error {
	if s == nil || store == nil {
		return nil
	}
	// Coalesce concurrent reload attempts.
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	s.mu.RLock()
	startDirtySeq := s.dirtySeq
	testLoadFn := s.loadFn
	s.mu.RUnlock()

	loadFn := loadControllerMetadataSnapshot
	if testLoadFn != nil {
		loadFn = testLoadFn
	}
	loaded, err := loadFn(ctx, store)
	if err != nil {
		return err
	}
	loaded.LeaderID = leader
	loaded.Generation = generation
	loaded.Ready = true

	s.mu.Lock()
	loaded.Dirty = s.dirtySeq != startDirtySeq
	if s.snapshot.LeaderID != leader || s.snapshot.Generation != generation {
		s.mu.Unlock()
		return nil
	}
	s.snapshot = loaded
	onLoaded := s.onLoaded
	installed := loaded.clone()
	s.mu.Unlock()
	if onLoaded != nil {
		onLoaded(installed)
	}
	return nil
}

func loadControllerMetadataSnapshot(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
	rawNodes, err := store.ListNodes(ctx)
	if err != nil {
		return controllerMetadataSnapshot{}, err
	}
	assignments, err := store.ListAssignments(ctx)
	if err != nil {
		return controllerMetadataSnapshot{}, err
	}
	rawTasks, err := store.ListTasks(ctx)
	if err != nil {
		return controllerMetadataSnapshot{}, err
	}

	nodes := make([]controllermeta.ClusterNode, 0, len(rawNodes))
	for _, node := range rawNodes {
		if node.NodeID == 0 {
			continue
		}
		nodes = append(nodes, node)
	}
	tasks := make([]controllermeta.ReconcileTask, 0, len(rawTasks))
	for _, task := range rawTasks {
		if task.SlotID == 0 {
			continue
		}
		tasks = append(tasks, task)
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].SlotID < tasks[j].SlotID })

	snapshot := controllerMetadataSnapshot{
		Nodes:             nodes,
		NodesByID:         make(map[uint64]controllermeta.ClusterNode, len(nodes)),
		Assignments:       make([]controllermeta.SlotAssignment, 0, len(assignments)),
		AssignmentsBySlot: make(map[uint32]controllermeta.SlotAssignment, len(assignments)),
		Tasks:             tasks,
		TasksBySlot:       make(map[uint32]controllermeta.ReconcileTask, len(tasks)),
	}
	for _, node := range nodes {
		snapshot.NodesByID[node.NodeID] = node
	}
	for _, assignment := range assignments {
		if assignment.SlotID == 0 {
			continue
		}
		assignment = cloneSlotAssignment(assignment)
		snapshot.Assignments = append(snapshot.Assignments, assignment)
		snapshot.AssignmentsBySlot[assignment.SlotID] = assignment
	}
	for _, task := range tasks {
		snapshot.TasksBySlot[task.SlotID] = task
	}
	return snapshot, nil
}
