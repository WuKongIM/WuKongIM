package online

// ActiveRoutesByUIDsInto fills prepaid caller-owned storage without allocating
// another route backing array. Strings borrow immutable registry values; callers
// that retain them independently must clone under their own ownership budget.
// Saturation clears every written reference and returns no partial proof.
func (r *Registry) ActiveRoutesByUIDsInto(uids []string, storage []OwnerRoute) ([]OwnerRoute, bool) {
	if r == nil || cap(storage) == 0 || len(uids) > 512 {
		return nil, false
	}
	routes := storage[:0]
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.RLock()
		for _, uid := range uids {
			for id := range shard.activeByUID[uid] {
				session, ok := shard.bySession[id]
				if !ok || session.State != RouteStateActive {
					continue
				}
				if len(routes) == cap(routes) {
					shard.mu.RUnlock()
					clear(routes)
					return nil, false
				}
				routes = append(routes, session.Route)
			}
		}
		shard.mu.RUnlock()
	}
	return routes, true
}

// ActiveRoutesByUIDs reads only indexed active routes for a bounded UID batch.
// The complete flag is false on output saturation; partial rows cannot prove offline.
func (r *Registry) ActiveRoutesByUIDs(uids []string, limit int) ([]OwnerRoute, bool) {
	if r == nil || limit <= 0 || len(uids) > 512 {
		return nil, false
	}
	var routes []OwnerRoute
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.RLock()
		for _, uid := range uids {
			for id := range shard.activeByUID[uid] {
				session, ok := shard.bySession[id]
				if !ok || session.State != RouteStateActive {
					continue
				}
				if len(routes) == limit {
					shard.mu.RUnlock()
					return nil, false
				}
				routes = append(routes, session.Route)
			}
		}
		shard.mu.RUnlock()
	}
	return routes, true
}

func (s *registryShard) removeActiveUID(session LocalSession) {
	uid := session.Route.UID
	ids := s.activeByUID[uid]
	delete(ids, session.Route.SessionID)
	if len(ids) == 0 {
		delete(s.activeByUID, uid)
	}
}
