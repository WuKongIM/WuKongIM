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
	bySession  map[uint64]LocalSession
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
		reg.shards[i].bySession = make(map[uint64]LocalSession)
		reg.shards[i].dirtyIDs = make(map[uint64]struct{})
	}
	return reg
}

// RegisterPending indexes a newly accepted session before it is route-active.
func (r *Registry) RegisterPending(session LocalSession) error {
	route := session.Route
	if route.UID == "" || route.SessionID == 0 {
		return ErrInvalidConnection
	}
	if route.LastActivityUnix == 0 {
		route.LastActivityUnix = route.ConnectedUnix
	}
	session.Route = route
	session.State = RouteStatePending

	shard := r.sessionShard(route.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	delete(shard.dirtyIDs, route.SessionID)
	shard.bySession[route.SessionID] = session
	return nil
}

// MarkActive promotes a pending session into the active route index.
func (r *Registry) MarkActive(sessionID uint64) error {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	session, ok := shard.bySession[sessionID]
	if !ok {
		return ErrConnectionNotFound
	}
	if session.State == RouteStateActive {
		return nil
	}
	session.State = RouteStateActive
	shard.bySession[sessionID] = session
	return nil
}

// MarkClosingAndUnregister removes a session from local indexes and returns a closing copy.
func (r *Registry) MarkClosingAndUnregister(sessionID uint64) (OwnerRoute, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	session, ok := shard.bySession[sessionID]
	if !ok {
		return OwnerRoute{}, false
	}
	delete(shard.bySession, sessionID)
	delete(shard.dirtyIDs, sessionID)
	session.State = RouteStateClosing
	return session.Route, true
}

// Route returns a copy of the registered route projection by session ID.
func (r *Registry) Route(sessionID uint64) (OwnerRoute, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	session, ok := shard.bySession[sessionID]
	if !ok {
		return OwnerRoute{}, false
	}
	return session.Route, true
}

// LocalSession returns the concrete local session record by session ID.
func (r *Registry) LocalSession(sessionID uint64) (LocalSession, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	session, ok := shard.bySession[sessionID]
	return session, ok
}

// LocalSessionsByUID returns copies of local sessions currently indexed for uid.
func (r *Registry) LocalSessionsByUID(uid string) []LocalSession {
	if r == nil || uid == "" {
		return nil
	}
	var out []LocalSession
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.RLock()
		for _, session := range shard.bySession {
			if session.Route.UID == uid {
				out = append(out, session)
			}
		}
		shard.mu.RUnlock()
	}
	return out
}

// MarkTouched records owner-observed client activity on an active route.
func (r *Registry) MarkTouched(sessionID uint64, activityUnix int64) (OwnerRoute, bool) {
	shard := r.sessionShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	session, ok := shard.bySession[sessionID]
	if !ok || session.State != RouteStateActive {
		return OwnerRoute{}, false
	}
	if activityUnix > session.Route.LastActivityUnix {
		session.Route.LastActivityUnix = activityUnix
	}
	shard.bySession[sessionID] = session
	shard.markDirtyLocked(sessionID)
	return session.Route, true
}

// DrainTouched returns up to limit dirty active routes and clears their dirty markers.
func (r *Registry) DrainTouched(limit int) []OwnerRoute {
	if limit <= 0 {
		return nil
	}
	batch := make([]OwnerRoute, 0, limit)
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
func (r *Registry) RequeueTouched(routes []OwnerRoute) {
	for _, drained := range routes {
		shard := r.sessionShard(drained.SessionID)
		shard.mu.Lock()
		current, ok := shard.bySession[drained.SessionID]
		if ok && current.State == RouteStateActive && sameRoute(current.Route, drained) {
			if drained.LastActivityUnix > current.Route.LastActivityUnix {
				current.Route.LastActivityUnix = drained.LastActivityUnix
				shard.bySession[current.Route.SessionID] = current
			}
			shard.markDirtyLocked(current.Route.SessionID)
		}
		shard.mu.Unlock()
	}
}

// Snapshot returns aggregate owner-local route counts without exposing sessions.
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	var snap Snapshot
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.RLock()
		for _, session := range shard.bySession {
			switch session.State {
			case RouteStatePending:
				snap.Pending++
			case RouteStateActive:
				snap.Active++
			}
		}
		snap.TouchedDirty += len(shard.dirtyIDs)
		shard.mu.RUnlock()
	}
	return snap
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

func (s *registryShard) drainDirtyLocked(batch []OwnerRoute, remaining int) []OwnerRoute {
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
			session, ok := s.bySession[sessionID]
			if ok && session.State == RouteStateActive {
				batch = append(batch, session.Route)
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

func sameRoute(a, b OwnerRoute) bool {
	return a.UID == b.UID &&
		a.SessionID == b.SessionID &&
		a.OwnerNodeID == b.OwnerNodeID &&
		a.OwnerBootID == b.OwnerBootID &&
		a.OwnerSeq == b.OwnerSeq
}
