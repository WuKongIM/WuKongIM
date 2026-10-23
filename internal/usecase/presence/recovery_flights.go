package presence

import (
	"context"
	"errors"
	"strings"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

const (
	// Sixteen leaders match the production node-wide owner-read admission. Keys
	// and bytes bound only registry-owned UID clones, not caller/Directory/RPC heap.
	recoveryMaxCohorts = 16
	recoveryMaxKeys    = recoveryMaxCohorts * authority.RecoveryBatchSize
	// A full page of 512 maximum-length stored UIDs (65535 bytes each) fits.
	// This is a new internal admission budget, not an RPC string-size contract.
	recoveryPageUIDBytes = 32 << 20
	// The byte budget is independent of cohort width.
	recoveryMaxUIDBytes = 128 << 20
)

// recoveryUIDKey uses the distributed authority identity. Local routing revision
// and AuthorityEpoch must not split one proof; original targets still flow to
// Directory and owners unchanged for their independent validation.
type recoveryUIDKey struct {
	hashSlot                              uint16
	slotID                                uint32
	leaderNodeID, leaderTerm, configEpoch uint64
	uid                                   string
}

func recoveryKey(target RouteTarget, uid string) recoveryUIDKey {
	return recoveryUIDKey{target.HashSlot, target.SlotID, target.LeaderNodeID, target.LeaderTerm, target.ConfigEpoch, uid}
}

// recoveryUIDFlight publishes status before done closes. It never caches routes
// or an offline proof; every successful follower rereads the current Directory.
type recoveryUIDFlight struct {
	done <-chan struct{}
	err  error
}

type recoveryCohort struct {
	done     chan struct{}
	keys     []recoveryUIDKey
	groups   []EndpointLookupGroup
	uidBytes int
}

func (r *AuthorityRecovery) recoverPage(ctx context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	results := make([]EndpointLookupResult, len(groups))
	if r == nil || r.directory == nil || r.owners == nil {
		for i := range results {
			results[i].Err = authority.ErrRouteNotReady
		}
		return results
	}
	results, cohort, waits := r.admitRecoveryPage(ctx, groups)
	if cohort != nil {
		// Publish/release owned work before joining older flights. In particular,
		// A={u1}, B={u1,u2} must make u2 available before B waits for A's u1.
		r.executeRecoveryCohort(ctx, cohort)
	}
	for i, group := range groups {
		if !errors.Is(results[i].Err, authority.ErrRouteNotReady) {
			continue
		}
		var err error
		for _, flight := range waits[i] {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-flight.done:
				err = flight.err
			}
			if err != nil {
				break
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			results[i] = EndpointLookupResult{Err: err}
			continue
		}
		results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(group.Target, group.UIDs)
	}
	return results
}

// admitRecoveryPage reserves a whole new subset or none. Quota waiting retains
// no partial keys, UID copies or cohort slot, and rechecks proofs after waking.
func (r *AuthorityRecovery) admitRecoveryPage(ctx context.Context, groups []EndpointLookupGroup) ([]EndpointLookupResult, *recoveryCohort, [][]*recoveryUIDFlight) {
	for {
		results := make([]EndpointLookupResult, len(groups))
		missing := make([]EndpointLookupGroup, 0, len(groups))
		indexes := make([]int, 0, len(groups))
		waits := make([][]*recoveryUIDFlight, len(groups))
		for i, group := range groups {
			if err := ctx.Err(); err != nil {
				results[i].Err = err
				continue
			}
			results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(group.Target, group.UIDs)
			if !errors.Is(results[i].Err, authority.ErrRouteNotReady) {
				continue
			}
			uids, err := r.directory.RecoveryUIDs(group.Target, group.UIDs)
			if err != nil {
				results[i].Err = err
				continue
			}
			if len(uids) == 0 {
				continue
			}
			missing = append(missing, EndpointLookupGroup{Target: group.Target, UIDs: uids})
			indexes = append(indexes, i)
		}
		if len(missing) == 0 {
			return results, nil, waits
		}

		r.mu.Lock()
		newKeys := make(map[recoveryUIDKey]recoveryUIDKey)
		uidBytes := 0
		oversized := false
		for _, group := range missing {
			for _, uid := range group.UIDs {
				key := recoveryKey(group.Target, uid)
				if _, exists := r.inFlight[key]; exists {
					continue
				}
				if _, exists := newKeys[key]; exists {
					continue
				}
				// Subtraction makes accounting overflow-safe before any UID copy.
				if len(uid) > recoveryPageUIDBytes-uidBytes {
					oversized = true
					break
				}
				uidBytes += len(uid)
				newKeys[key] = key
			}
			if oversized {
				break
			}
		}
		if oversized {
			r.mu.Unlock()
			return results, nil, waits
		}
		if len(newKeys) != 0 && (r.cohorts >= recoveryMaxCohorts || len(newKeys) > recoveryMaxKeys-len(r.inFlight) || uidBytes > recoveryMaxUIDBytes-r.ownedUIDBytes) {
			changed := r.changed
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				for _, i := range indexes {
					results[i] = EndpointLookupResult{Err: ctx.Err()}
				}
				return results, nil, waits
			case <-changed:
				continue
			}
		}
		var cohort *recoveryCohort
		if len(newKeys) != 0 {
			cohort = &recoveryCohort{done: make(chan struct{}), uidBytes: uidBytes, keys: make([]recoveryUIDKey, 0, len(newKeys))}
			for key := range newKeys {
				owned := key
				owned.uid = strings.Clone(key.uid)
				newKeys[key] = owned
				cohort.keys = append(cohort.keys, owned)
				r.inFlight[owned] = &recoveryUIDFlight{done: cohort.done}
			}
			r.cohorts++
			r.ownedUIDBytes += uidBytes
		}
		for j, group := range missing {
			ownedGroup := EndpointLookupGroup{Target: group.Target}
			for _, uid := range group.UIDs {
				key := recoveryKey(group.Target, uid)
				waits[indexes[j]] = append(waits[indexes[j]], r.inFlight[key])
				if owned, ok := newKeys[key]; ok {
					ownedGroup.UIDs = append(ownedGroup.UIDs, owned.uid)
					delete(newKeys, key)
				}
			}
			if len(ownedGroup.UIDs) != 0 {
				cohort.groups = append(cohort.groups, ownedGroup)
			}
		}
		r.mu.Unlock()
		return results, cohort, waits
	}
}

// executeRecoveryCohort runs in the caller, with its original deadline. A
// canceled leader cannot publish incomplete proof or strand live followers.
func (r *AuthorityRecovery) executeRecoveryCohort(ctx context.Context, cohort *recoveryCohort) {
	outcomes := make(map[recoveryUIDKey]error, len(cohort.keys))
	for _, key := range cohort.keys {
		outcomes[key] = authority.ErrRouteNotReady
	}
	defer r.completeRecoveryCohort(cohort, outcomes)
	set := func(group EndpointLookupGroup, err error) {
		for _, uid := range group.UIDs {
			outcomes[recoveryKey(group.Target, uid)] = err
		}
	}
	active := make([]EndpointLookupGroup, 0, len(cohort.groups))
	for _, group := range cohort.groups {
		uids, err := r.directory.RecoveryUIDs(group.Target, group.UIDs)
		if err != nil {
			set(group, err)
			continue
		}
		set(group, nil)
		if len(uids) != 0 {
			active = append(active, EndpointLookupGroup{Target: group.Target, UIDs: uids})
		}
	}
	if len(active) == 0 {
		return
	}
	recovered := make([]EndpointLookupResult, len(active))
	if batch, ok := r.owners.(RecoveryBatchOwners); ok {
		recovered = batch.RecoverRoutesByTargets(ctx, active)
	} else {
		// Limited compatibility ports keep their existing single-target path.
		for i, group := range active {
			recovered[i].Routes, recovered[i].Err = r.owners.RecoverRoutes(ctx, group.Target, group.UIDs)
		}
	}
	for i, group := range active {
		if err := ctx.Err(); err != nil {
			set(group, err)
			continue
		}
		if len(recovered) != len(active) {
			set(group, authority.ErrRouteNotReady)
			continue
		}
		if recovered[i].Err != nil {
			set(group, recovered[i].Err)
			continue
		}
		set(group, r.directory.InstallRecoveredRoutes(group.Target, group.UIDs, recovered[i].Routes))
	}
}

// completeRecoveryCohort releases owned resources and signals followers before
// its leader can wait on older flights. A live follower never inherits someone
// else's context cancellation as its own; existing outer retries stay unchanged.
func (r *AuthorityRecovery) completeRecoveryCohort(cohort *recoveryCohort, outcomes map[recoveryUIDKey]error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range cohort.keys {
		err := outcomes[key]
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = authority.ErrRouteNotReady
		}
		r.inFlight[key].err = err
		delete(r.inFlight, key)
	}
	r.ownedUIDBytes -= cohort.uidBytes
	r.cohorts--
	close(cohort.done)
	close(r.changed)
	r.changed = make(chan struct{})
}
