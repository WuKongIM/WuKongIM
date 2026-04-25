package meta

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	"github.com/cockroachdb/pebble/v2"
)

type Store struct {
	db *pebble.DB
	mu sync.RWMutex
}

func Open(path string) (*Store, error) {
	db, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.db == nil {
		s.mu.Unlock()
		return nil
	}
	db := s.db
	s.db = nil
	s.mu.Unlock()

	return db.Close()
}

func (s *Store) SaveHashSlotTable(ctx context.Context, table *hashslot.HashSlotTable) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if table == nil {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeValueLocked(hashSlotTableKey(), table.Encode())
}

func (s *Store) UpsertAssignmentsAndSaveHashSlotTable(ctx context.Context, assignments []SlotAssignment, table *hashslot.HashSlotTable) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if table == nil {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	writes := make([]batchWrite, 0, len(assignments)+1)
	for _, assignment := range assignments {
		if assignment.SlotID == 0 {
			return ErrInvalidArgument
		}
		assignment = normalizeGroupAssignment(assignment)
		if err := validateRequiredPeerSet(assignment.DesiredPeers, ErrInvalidArgument); err != nil {
			return err
		}
		writes = append(writes, batchWrite{
			key:   encodeGroupKey(recordPrefixAssignment, assignment.SlotID),
			value: encodeGroupAssignment(assignment),
		})
	}
	writes = append(writes, batchWrite{
		key:   hashSlotTableKey(),
		value: table.Encode(),
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBatchLocked(writes)
}

func (s *Store) UpsertAssignmentTaskAndSaveHashSlotTable(ctx context.Context, assignment SlotAssignment, task ReconcileTask, table *hashslot.HashSlotTable) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if table == nil {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	if assignment.SlotID == 0 || task.SlotID == 0 || assignment.SlotID != task.SlotID {
		return ErrInvalidArgument
	}

	assignment = normalizeGroupAssignment(assignment)
	if err := validateRequiredPeerSet(assignment.DesiredPeers, ErrInvalidArgument); err != nil {
		return err
	}
	task = normalizeReconcileTask(task)
	if !validTaskKind(task.Kind) || !validTaskStep(task.Step) || !validTaskStatus(task.Status) {
		return ErrInvalidArgument
	}
	if err := validateReconcileTaskState(task, ErrInvalidArgument); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBatchLocked([]batchWrite{
		{
			key:   encodeGroupKey(recordPrefixAssignment, assignment.SlotID),
			value: encodeGroupAssignment(assignment),
		},
		{
			key:   encodeGroupKey(recordPrefixTask, task.SlotID),
			value: encodeReconcileTask(task),
		},
		{
			key:   hashSlotTableKey(),
			value: table.Encode(),
		},
	})
}

func (s *Store) DeleteAssignmentTaskAndSaveHashSlotTable(ctx context.Context, slotID uint32, table *hashslot.HashSlotTable) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if table == nil || slotID == 0 {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBatchLocked([]batchWrite{
		{
			key:    encodeGroupKey(recordPrefixAssignment, slotID),
			delete: true,
		},
		{
			key:    encodeGroupKey(recordPrefixTask, slotID),
			delete: true,
		},
		{
			key:   hashSlotTableKey(),
			value: table.Encode(),
		},
	})
}

func (s *Store) LoadHashSlotTable(ctx context.Context) (*hashslot.HashSlotTable, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	value, err := s.getValueLocked(hashSlotTableKey())
	if err != nil {
		return nil, err
	}
	return hashslot.DecodeHashSlotTable(value)
}

func (s *Store) GetNode(ctx context.Context, nodeID uint64) (ClusterNode, error) {
	if err := s.ensureOpen(); err != nil {
		return ClusterNode{}, err
	}
	if nodeID == 0 {
		return ClusterNode{}, ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return ClusterNode{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	value, err := s.getValueLocked(encodeNodeKey(nodeID))
	if err != nil {
		return ClusterNode{}, err
	}
	return decodeClusterNode(encodeNodeKey(nodeID), value)
}

func (s *Store) DeleteNode(ctx context.Context, nodeID uint64) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if nodeID == 0 {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteValueLocked(encodeNodeKey(nodeID))
}

func (s *Store) ListNodes(ctx context.Context) ([]ClusterNode, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listNodesLocked(ctx)
}

func (s *Store) UpsertNode(ctx context.Context, node ClusterNode) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	if node.NodeID == 0 || node.Addr == "" || node.CapacityWeight < 0 || !validNodeStatus(node.Status) {
		return ErrInvalidArgument
	}
	node = normalizeClusterNode(node)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeValueLocked(encodeNodeKey(node.NodeID), encodeClusterNode(node))
}

func (s *Store) UpsertNodeAndDeleteRepairTasks(ctx context.Context, node ClusterNode) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	if node.NodeID == 0 || node.Addr == "" || node.CapacityWeight < 0 || !validNodeStatus(node.Status) {
		return ErrInvalidArgument
	}
	node = normalizeClusterNode(node)

	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, err := s.listTasksLocked(ctx)
	if err != nil {
		return err
	}
	assignments, err := s.listAssignmentsLocked(ctx)
	if err != nil {
		return err
	}
	assignmentsByGroup := make(map[uint32]SlotAssignment, len(assignments))
	for _, assignment := range assignments {
		assignmentsByGroup[assignment.SlotID] = assignment
	}

	writes := []batchWrite{{
		key:   encodeNodeKey(node.NodeID),
		value: encodeClusterNode(node),
	}}
	for _, task := range tasks {
		if task.Kind != TaskKindRepair || task.SourceNode != node.NodeID {
			continue
		}
		if assignment, ok := assignmentsByGroup[task.SlotID]; ok {
			if restored, changed := restoreRepairAssignment(assignment, task); changed {
				assignmentsByGroup[task.SlotID] = restored
				writes = append(writes, batchWrite{
					key:   encodeGroupKey(recordPrefixAssignment, restored.SlotID),
					value: encodeGroupAssignment(restored),
				})
			}
		}
		writes = append(writes, batchWrite{
			key:    encodeGroupKey(recordPrefixTask, task.SlotID),
			delete: true,
		})
	}
	return s.writeBatchLocked(writes)
}

func restoreRepairAssignment(assignment SlotAssignment, task ReconcileTask) (SlotAssignment, bool) {
	if assignment.SlotID == 0 || assignment.SlotID != task.SlotID {
		return SlotAssignment{}, false
	}
	if task.SourceNode == 0 || task.TargetNode == 0 {
		return SlotAssignment{}, false
	}
	if containsUint64(assignment.DesiredPeers, task.SourceNode) || !containsUint64(assignment.DesiredPeers, task.TargetNode) {
		return SlotAssignment{}, false
	}

	restored := assignment
	peers := make([]uint64, 0, len(assignment.DesiredPeers))
	for _, peer := range assignment.DesiredPeers {
		if peer == task.TargetNode {
			continue
		}
		peers = append(peers, peer)
	}
	peers = append(peers, task.SourceNode)
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })

	restored.DesiredPeers = peers
	restored.ConfigEpoch++
	return normalizeGroupAssignment(restored), true
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Store) GetAssignment(ctx context.Context, slotID uint32) (SlotAssignment, error) {
	if err := s.ensureOpen(); err != nil {
		return SlotAssignment{}, err
	}
	if slotID == 0 {
		return SlotAssignment{}, ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return SlotAssignment{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := encodeGroupKey(recordPrefixAssignment, slotID)
	value, err := s.getValueLocked(key)
	if err != nil {
		return SlotAssignment{}, err
	}
	return decodeGroupAssignment(key, value)
}

func (s *Store) DeleteAssignment(ctx context.Context, slotID uint32) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if slotID == 0 {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteValueLocked(encodeGroupKey(recordPrefixAssignment, slotID))
}

func (s *Store) ListAssignments(ctx context.Context) ([]SlotAssignment, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listAssignmentsLocked(ctx)
}

func (s *Store) UpsertAssignment(ctx context.Context, assignment SlotAssignment) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	if assignment.SlotID == 0 {
		return ErrInvalidArgument
	}
	assignment = normalizeGroupAssignment(assignment)
	if err := validateRequiredPeerSet(assignment.DesiredPeers, ErrInvalidArgument); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeBatchLocked([]batchWrite{
		{
			key:   encodeGroupKey(recordPrefixAssignment, assignment.SlotID),
			value: encodeGroupAssignment(assignment),
		},
	})
}

func (s *Store) GetRuntimeView(ctx context.Context, slotID uint32) (SlotRuntimeView, error) {
	if err := s.ensureOpen(); err != nil {
		return SlotRuntimeView{}, err
	}
	if slotID == 0 {
		return SlotRuntimeView{}, ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return SlotRuntimeView{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := encodeGroupKey(recordPrefixRuntimeView, slotID)
	value, err := s.getValueLocked(key)
	if err != nil {
		return SlotRuntimeView{}, err
	}
	return decodeGroupRuntimeView(key, value)
}

func (s *Store) GetControllerMembership(ctx context.Context) (ControllerMembership, error) {
	if err := s.ensureOpen(); err != nil {
		return ControllerMembership{}, err
	}
	if err := s.checkContext(ctx); err != nil {
		return ControllerMembership{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	value, err := s.getValueLocked(membershipKey())
	if err != nil {
		return ControllerMembership{}, err
	}
	return decodeControllerMembership(value)
}

func (s *Store) DeleteControllerMembership(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteValueLocked(membershipKey())
}

func (s *Store) ListRuntimeViews(ctx context.Context) ([]SlotRuntimeView, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listRuntimeViewsLocked(ctx)
}

func (s *Store) DeleteRuntimeView(ctx context.Context, slotID uint32) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if slotID == 0 {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteValueLocked(encodeGroupKey(recordPrefixRuntimeView, slotID))
}

func (s *Store) UpsertControllerMembership(ctx context.Context, membership ControllerMembership) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	membership.Peers = normalizeUint64Set(membership.Peers)
	if err := validateRequiredPeerSet(membership.Peers, ErrInvalidArgument); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeValueLocked(membershipKey(), encodeControllerMembership(membership))
}

func (s *Store) UpsertRuntimeView(ctx context.Context, view SlotRuntimeView) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	if view.SlotID == 0 {
		return ErrInvalidArgument
	}
	view = normalizeGroupRuntimeView(view)
	if err := validateRuntimeViewState(view, ErrInvalidArgument); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeValueLocked(
		encodeGroupKey(recordPrefixRuntimeView, view.SlotID),
		encodeGroupRuntimeView(view),
	)
}

func (s *Store) GetTask(ctx context.Context, slotID uint32) (ReconcileTask, error) {
	if err := s.ensureOpen(); err != nil {
		return ReconcileTask{}, err
	}
	if slotID == 0 {
		return ReconcileTask{}, ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return ReconcileTask{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := encodeGroupKey(recordPrefixTask, slotID)
	value, err := s.getValueLocked(key)
	if err != nil {
		return ReconcileTask{}, err
	}
	return decodeReconcileTask(key, value)
}

func (s *Store) DeleteTask(ctx context.Context, slotID uint32) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if slotID == 0 {
		return ErrInvalidArgument
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteValueLocked(encodeGroupKey(recordPrefixTask, slotID))
}

func (s *Store) ListTasks(ctx context.Context) ([]ReconcileTask, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listTasksLocked(ctx)
}

func (s *Store) UpsertTask(ctx context.Context, task ReconcileTask) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	task = normalizeReconcileTask(task)
	if task.SlotID == 0 || !validTaskKind(task.Kind) || !validTaskStep(task.Step) || !validTaskStatus(task.Status) || validateReconcileTaskState(task, ErrInvalidArgument) != nil {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeBatchLocked([]batchWrite{
		{
			key:   encodeGroupKey(recordPrefixTask, task.SlotID),
			value: encodeReconcileTask(task),
		},
	})
}

func (s *Store) UpsertAssignmentTask(ctx context.Context, assignment SlotAssignment, task ReconcileTask) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	if assignment.SlotID == 0 || task.SlotID == 0 || assignment.SlotID != task.SlotID {
		return ErrInvalidArgument
	}
	assignment = normalizeGroupAssignment(assignment)
	if err := validateRequiredPeerSet(assignment.DesiredPeers, ErrInvalidArgument); err != nil {
		return err
	}
	task = normalizeReconcileTask(task)
	if !validTaskKind(task.Kind) || !validTaskStep(task.Step) || !validTaskStatus(task.Status) {
		return ErrInvalidArgument
	}
	if err := validateReconcileTaskState(task, ErrInvalidArgument); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writeBatchLocked([]batchWrite{
		{
			key:   encodeGroupKey(recordPrefixAssignment, assignment.SlotID),
			value: encodeGroupAssignment(assignment),
		},
		{
			key:   encodeGroupKey(recordPrefixTask, task.SlotID),
			value: encodeReconcileTask(task),
		},
	})
}

func (s *Store) listNodesLocked(ctx context.Context) ([]ClusterNode, error) {
	return listRecords(ctx, s.db, recordPrefixNode, decodeClusterNode)
}

func (s *Store) listAssignmentsLocked(ctx context.Context) ([]SlotAssignment, error) {
	return listRecords(ctx, s.db, recordPrefixAssignment, decodeGroupAssignment)
}

func (s *Store) listRuntimeViewsLocked(ctx context.Context) ([]SlotRuntimeView, error) {
	return listRecords(ctx, s.db, recordPrefixRuntimeView, decodeGroupRuntimeView)
}

func (s *Store) listTasksLocked(ctx context.Context) ([]ReconcileTask, error) {
	return listRecords(ctx, s.db, recordPrefixTask, decodeReconcileTask)
}

func listRecords[T any](ctx context.Context, db *pebble.DB, prefix byte, decode func(key, value []byte) (T, error)) ([]T, error) {
	if db == nil {
		return nil, ErrClosed
	}
	lowerBound, upperBound := prefixBounds(prefix)
	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: upperBound,
	})
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, 16)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := checkContext(ctx); err != nil {
			iter.Close()
			return nil, err
		}

		value, err := iter.ValueAndErr()
		if err != nil {
			iter.Close()
			return nil, err
		}
		record, err := decode(iter.Key(), value)
		if err != nil {
			iter.Close()
			return nil, err
		}
		out = append(out, record)
	}
	if err := iter.Error(); err != nil {
		iter.Close()
		return nil, err
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) getValueLocked(key []byte) ([]byte, error) {
	if err := s.ensureOpenLocked(); err != nil {
		return nil, err
	}
	value, closer, err := s.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer closer.Close()

	return append([]byte(nil), value...), nil
}

func (s *Store) deleteValueLocked(key []byte) error {
	if err := s.ensureOpenLocked(); err != nil {
		return err
	}
	_, closer, err := s.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	closer.Close()

	batch := s.db.NewBatch()
	defer batch.Close()

	if err := batch.Delete(key, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) writeValueLocked(key, value []byte) error {
	return s.writeBatchLocked([]batchWrite{{key: key, value: value}})
}

type batchWrite struct {
	key    []byte
	value  []byte
	delete bool
}

func (s *Store) writeBatchLocked(writes []batchWrite) error {
	if err := s.ensureOpenLocked(); err != nil {
		return err
	}
	batch := s.db.NewBatch()
	defer batch.Close()

	for _, write := range writes {
		if write.delete {
			if err := batch.Delete(write.key, nil); err != nil {
				return err
			}
			continue
		}
		if err := batch.Set(write.key, write.value, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) checkContext(ctx context.Context) error {
	return checkContext(ctx)
}

func (s *Store) ensureOpenLocked() error {
	if s == nil || s.db == nil {
		return ErrClosed
	}
	return nil
}

func (s *Store) ensureOpen() error {
	if s == nil {
		return ErrClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ensureOpenLocked()
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
