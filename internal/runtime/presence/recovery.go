package presence

// RecoveryBatchSize bounds one owner query and atomic reconstruction page.
const RecoveryBatchSize = 512

// A bounded FIFO proof cache avoids unbounded memory for offline recipient UIDs.
// Eviction requires another owner query; it never turns unknown into offline.
const recoveryCacheSize = 1024

func (s *authoritySlot) recoveryReady(uids []string) bool {
	for _, uid := range uids {
		if uid != "" {
			if _, ok := s.recovered[uid]; !ok {
				return false
			}
		}
	}
	return true
}

// RecoveryUIDs returns only UIDs lacking an exact current-authority owner proof.
func (d *Directory) RecoveryUIDs(target RouteTarget, uids []string) ([]string, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return nil, err
	}
	if len(uids) > RecoveryBatchSize {
		return nil, ErrRouteNotReady
	}
	return slot.missingRecoveryUIDsLocked(uids, nil), nil
}

// RecoveryUIDsWithReadback captures one bounded request's known proof under the
// same lock as missing selection. The caller must keep uids immutable and discard
// the readback when that synchronous lookup ends. No route snapshot is retained.
// After successful recovery, FIFO eviction of initially known proofs cannot
// invalidate this request. Initially missing UIDs still need current proof: a
// shared flight's nil status alone cannot prove installation in a new instance.
func (d *Directory) RecoveryUIDsWithReadback(target RouteTarget, uids []string) ([]string, func() ([]Route, error), error) {
	if d == nil || len(d.shards) == 0 || len(uids) > RecoveryBatchSize {
		return nil, nil, ErrRouteNotReady
	}
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return nil, nil, err
	}
	var missingPositions [(RecoveryBatchSize + 63) / 64]uint64
	missing := slot.missingRecoveryUIDsLocked(uids, &missingPositions)
	generation := slot.recoveryGeneration
	readback := func() ([]Route, error) {
		shard.mu.RLock()
		defer shard.mu.RUnlock()
		current, err := d.validateTargetLocked(shard, target)
		if err != nil {
			return nil, err
		}
		if current.recoveryGeneration != generation {
			return nil, ErrRouteNotReady
		}
		if d.requireRecovery {
			for i, uid := range uids {
				if missingPositions[i/64]&(uint64(1)<<uint(i%64)) != 0 {
					if _, ok := current.recovered[uid]; !ok {
						return nil, ErrRouteNotReady
					}
				}
			}
		}
		return current.endpointsByUIDsLocked(uids), nil
	}
	return missing, readback, nil
}

func (s *authoritySlot) missingRecoveryUIDsLocked(uids []string, positions *[(RecoveryBatchSize + 63) / 64]uint64) []string {
	var missing []string
	seen := make(map[string]struct{}, len(uids))
	for i, uid := range uids {
		if uid == "" {
			continue
		}
		if _, ok := s.recovered[uid]; ok {
			continue
		}
		if positions != nil {
			positions[i/64] |= uint64(1) << uint(i%64)
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		missing = append(missing, uid)
	}
	return missing
}

// InstallRecoveredRoutes publishes one complete owner-query page under the
// original authority fence. Concurrent unregister tombstones and newer owner
// sequences still win; missing proof is never published by partial owner reads.
func (d *Directory) InstallRecoveredRoutes(target RouteTarget, uids []string, routes []Route) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	if len(uids) > RecoveryBatchSize || len(routes) > 4096 {
		return ErrRouteNotReady
	}
	allowed := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		allowed[uid] = struct{}{}
	}
	for _, route := range routes {
		if _, ok := allowed[route.UID]; !ok || route.OwnerNodeID == 0 || route.OwnerBootID == 0 || route.SessionID == 0 {
			return ErrStaleRoute
		}
	}
	for _, route := range routes {
		slot.touchLocked(route)
	}
	if slot.recovered == nil {
		slot.recovered = make(map[string]struct{})
	}
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		if _, ok := slot.recovered[uid]; ok {
			continue
		}
		if len(slot.recoveryOrder) < recoveryCacheSize {
			slot.recoveryOrder = append(slot.recoveryOrder, uid)
		} else {
			delete(slot.recovered, slot.recoveryOrder[slot.recoveryNext])
			slot.recoveryOrder[slot.recoveryNext] = uid
			slot.recoveryNext = (slot.recoveryNext + 1) % recoveryCacheSize
		}
		slot.recovered[uid] = struct{}{}
	}
	return nil
}
