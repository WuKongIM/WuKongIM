package online

import (
	"sort"
	"sync"
)

const defaultShardCount = 32

// Registry stores owner-local gateway session routes in sharded indexes.
type Registry struct {
	shards []registryShard
}

type registryShard struct {
	mu         sync.RWMutex
	bySession  map[uint64]OnlineConn
	byHashSlot map[uint16]*routeBucket
}

type routeBucket struct {
	activeIDs map[uint64]struct{}
	order     []uint64
}

// NewRegistry creates an owner-local online registry.
func NewRegistry(opts RegistryOptions) *Registry {
	shardCount := opts.ShardCount
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	reg := &Registry{
		shards: make([]registryShard, shardCount),
	}
	for i := range reg.shards {
		reg.shards[i].bySession = make(map[uint64]OnlineConn)
		reg.shards[i].byHashSlot = make(map[uint16]*routeBucket)
	}
	return reg
}

// RegisterPending indexes a newly accepted session before it is route-active.
func (r *Registry) RegisterPending(conn OnlineConn) error {
	if conn.UID == "" || conn.SessionID == 0 {
		return ErrInvalidConnection
	}
	conn.State = RouteStatePending

	shard := r.sessionShard(conn.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if existing, ok := shard.bySession[conn.SessionID]; ok && existing.State == RouteStateActive {
		shard.removeActiveLocked(existing)
	}
	shard.bySession[conn.SessionID] = conn
	return nil
}

// MarkActive promotes a pending session into the active route index.
func (r *Registry) MarkActive(sessionID uint64) error {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	conn, ok := shard.bySession[sessionID]
	if !ok {
		return ErrConnectionNotFound
	}
	if conn.State == RouteStateActive {
		return nil
	}
	conn.State = RouteStateActive
	shard.bySession[sessionID] = conn
	shard.insertActiveLocked(conn)
	return nil
}

// MarkClosingAndUnregister removes a session from local indexes and returns a closing copy.
func (r *Registry) MarkClosingAndUnregister(sessionID uint64) (OnlineConn, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	conn, ok := shard.bySession[sessionID]
	if !ok {
		return OnlineConn{}, false
	}
	if conn.State == RouteStateActive {
		shard.removeActiveLocked(conn)
	}
	delete(shard.bySession, sessionID)
	conn.State = RouteStateClosing
	return conn, true
}

// Connection returns a copy of the registered session route by session ID.
func (r *Registry) Connection(sessionID uint64) (OnlineConn, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	conn, ok := shard.bySession[sessionID]
	return conn, ok
}

// VisitActiveByHashSlot visits at most limit active routes for one hash slot.
func (r *Registry) VisitActiveByHashSlot(hashSlot uint16, cursor RouteCursor, limit int, fn func(OnlineConn) bool) (RouteCursor, bool) {
	if limit <= 0 || fn == nil {
		return cursor, false
	}

	results, more := r.collectActiveByHashSlot(hashSlot, cursor, limit)
	for i, conn := range results {
		if !fn(conn) {
			return RouteCursor{LastSessionID: conn.SessionID}, more || i+1 < len(results)
		}
		cursor.LastSessionID = conn.SessionID
	}
	return cursor, more
}

func (r *Registry) collectActiveByHashSlot(hashSlot uint16, cursor RouteCursor, limit int) ([]OnlineConn, bool) {
	for i := range r.shards {
		r.shards[i].mu.RLock()
	}
	defer func() {
		for i := len(r.shards) - 1; i >= 0; i-- {
			r.shards[i].mu.RUnlock()
		}
	}()

	positions := make([]int, len(r.shards))
	for i := range r.shards {
		bucket := r.shards[i].byHashSlot[hashSlot]
		if bucket == nil {
			continue
		}
		positions[i] = sort.Search(len(bucket.order), func(pos int) bool {
			return bucket.order[pos] > cursor.LastSessionID
		})
	}

	results := make([]OnlineConn, 0, limit)
	for len(results) <= limit {
		shardIndex, conn, ok := r.nextActiveLocked(hashSlot, positions)
		if !ok {
			return results, false
		}
		positions[shardIndex]++
		if len(results) == limit {
			return results, true
		}
		results = append(results, conn)
	}
	return results, false
}

func (r *Registry) nextActiveLocked(hashSlot uint16, positions []int) (int, OnlineConn, bool) {
	var selected OnlineConn
	selectedShard := -1
	for i := range r.shards {
		shard := &r.shards[i]
		bucket := shard.byHashSlot[hashSlot]
		if bucket == nil {
			continue
		}
		for positions[i] < len(bucket.order) {
			sessionID := bucket.order[positions[i]]
			if _, ok := bucket.activeIDs[sessionID]; !ok {
				positions[i]++
				continue
			}
			conn, ok := shard.bySession[sessionID]
			if !ok || conn.State != RouteStateActive || conn.HashSlot != hashSlot {
				positions[i]++
				continue
			}
			if selectedShard == -1 || conn.SessionID < selected.SessionID {
				selected = conn
				selectedShard = i
			}
			break
		}
	}
	if selectedShard == -1 {
		return 0, OnlineConn{}, false
	}
	return selectedShard, selected, true
}

func (r *Registry) sessionShard(sessionID uint64) *registryShard {
	return &r.shards[sessionID%uint64(len(r.shards))]
}

func (s *registryShard) insertActiveLocked(conn OnlineConn) {
	bucket := s.bucketLocked(conn.HashSlot)
	if _, ok := bucket.activeIDs[conn.SessionID]; ok {
		return
	}
	bucket.activeIDs[conn.SessionID] = struct{}{}
	index := sort.Search(len(bucket.order), func(i int) bool {
		return bucket.order[i] >= conn.SessionID
	})
	if index < len(bucket.order) && bucket.order[index] == conn.SessionID {
		return
	}
	bucket.order = append(bucket.order, 0)
	copy(bucket.order[index+1:], bucket.order[index:])
	bucket.order[index] = conn.SessionID
}

func (s *registryShard) removeActiveLocked(conn OnlineConn) {
	bucket := s.byHashSlot[conn.HashSlot]
	if bucket == nil {
		return
	}
	delete(bucket.activeIDs, conn.SessionID)
	index := sort.Search(len(bucket.order), func(i int) bool {
		return bucket.order[i] >= conn.SessionID
	})
	if index < len(bucket.order) && bucket.order[index] == conn.SessionID {
		copy(bucket.order[index:], bucket.order[index+1:])
		bucket.order[len(bucket.order)-1] = 0
		bucket.order = bucket.order[:len(bucket.order)-1]
	}
	if len(bucket.order) == 0 {
		delete(s.byHashSlot, conn.HashSlot)
	}
}

func (s *registryShard) bucketLocked(hashSlot uint16) *routeBucket {
	bucket := s.byHashSlot[hashSlot]
	if bucket != nil {
		return bucket
	}
	bucket = &routeBucket{
		activeIDs: make(map[uint64]struct{}),
	}
	s.byHashSlot[hashSlot] = bucket
	return bucket
}
