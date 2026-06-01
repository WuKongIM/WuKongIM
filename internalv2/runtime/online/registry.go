package online

import (
	"sync"
	"sync/atomic"
)

const defaultShardCount = 32

// Registry stores owner-local gateway session routes in sharded indexes.
type Registry struct {
	shards         []registryShard
	nextDrainShard atomic.Uint64
}

type registryShard struct {
	mu         sync.RWMutex
	bySession  map[uint64]OnlineConn
	dirtyIDs   map[uint64]struct{}
	dirtyOrder []uint64
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
		reg.shards[i].dirtyIDs = make(map[uint64]struct{})
	}
	return reg
}

// RegisterPending indexes a newly accepted session before it is route-active.
func (r *Registry) RegisterPending(conn OnlineConn) error {
	if conn.UID == "" || conn.SessionID == 0 {
		return ErrInvalidConnection
	}
	if conn.LastActivityUnix == 0 {
		conn.LastActivityUnix = conn.ConnectedUnix
	}
	conn.State = RouteStatePending

	shard := r.sessionShard(conn.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	delete(shard.dirtyIDs, conn.SessionID)
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
	delete(shard.bySession, sessionID)
	delete(shard.dirtyIDs, sessionID)
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

// MarkTouched records owner-observed client activity on an active route.
func (r *Registry) MarkTouched(sessionID uint64, activityUnix int64) (OnlineConn, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	conn, ok := shard.bySession[sessionID]
	if !ok || conn.State != RouteStateActive {
		return OnlineConn{}, false
	}
	if activityUnix > conn.LastActivityUnix {
		conn.LastActivityUnix = activityUnix
	}
	shard.bySession[sessionID] = conn
	shard.markDirtyLocked(sessionID)
	return conn, true
}

// DrainTouched returns up to limit dirty active routes and clears their dirty markers.
func (r *Registry) DrainTouched(limit int) []OnlineConn {
	if limit <= 0 {
		return nil
	}
	batch := make([]OnlineConn, 0, limit)
	shardCount := len(r.shards)
	start := int(r.nextDrainShard.Add(1)-1) % shardCount
	for offset := 0; offset < shardCount; offset++ {
		if len(batch) == limit {
			break
		}
		shard := &r.shards[(start+offset)%shardCount]
		shard.mu.Lock()
		batch = shard.drainDirtyLocked(batch, limit-len(batch))
		shard.mu.Unlock()
	}
	return batch
}

// RequeueTouched marks still-current active routes dirty after a failed authority touch batch.
func (r *Registry) RequeueTouched(conns []OnlineConn) {
	for _, drained := range conns {
		shard := r.sessionShard(drained.SessionID)
		shard.mu.Lock()
		current, ok := shard.bySession[drained.SessionID]
		if ok && current.State == RouteStateActive && sameRoute(current, drained) {
			if drained.LastActivityUnix > current.LastActivityUnix {
				current.LastActivityUnix = drained.LastActivityUnix
				shard.bySession[current.SessionID] = current
			}
			shard.markDirtyLocked(current.SessionID)
		}
		shard.mu.Unlock()
	}
}

func (r *Registry) sessionShard(sessionID uint64) *registryShard {
	return &r.shards[sessionID%uint64(len(r.shards))]
}

func (s *registryShard) markDirtyLocked(sessionID uint64) {
	if _, ok := s.dirtyIDs[sessionID]; ok {
		return
	}
	s.dirtyIDs[sessionID] = struct{}{}
	s.dirtyOrder = append(s.dirtyOrder, sessionID)
}

func (s *registryShard) drainDirtyLocked(batch []OnlineConn, remaining int) []OnlineConn {
	if remaining <= 0 || len(s.dirtyOrder) == 0 {
		return batch
	}
	write := 0
	for _, sessionID := range s.dirtyOrder {
		if _, dirty := s.dirtyIDs[sessionID]; !dirty {
			continue
		}
		if remaining > 0 {
			delete(s.dirtyIDs, sessionID)
			conn, ok := s.bySession[sessionID]
			if ok && conn.State == RouteStateActive {
				batch = append(batch, conn)
				remaining--
			}
			continue
		}
		s.dirtyOrder[write] = sessionID
		write++
	}
	for i := write; i < len(s.dirtyOrder); i++ {
		s.dirtyOrder[i] = 0
	}
	s.dirtyOrder = s.dirtyOrder[:write]
	return batch
}

func sameRoute(a, b OnlineConn) bool {
	return a.UID == b.UID &&
		a.SessionID == b.SessionID &&
		a.OwnerNodeID == b.OwnerNodeID &&
		a.OwnerBootID == b.OwnerBootID &&
		a.OwnerSeq == b.OwnerSeq
}
