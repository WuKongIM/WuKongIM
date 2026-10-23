package presence

import (
	"context"
	"errors"
	"strings"
	"unsafe"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// ActiveRouteRegistryInto projects into prepaid caller storage. Route strings
// borrow the registry only until the caller clones its retained result.
type ActiveRouteRegistryInto interface {
	ActiveRoutesByUIDsInto([]string, []OwnerRoute) ([]OwnerRoute, bool)
}

// SupportsBoundedRecoveryResults opts in only with the controlled registry
// projection port; legacy embedding registries retain their conservative path.
func (r *OwnerRecovery) SupportsBoundedRecoveryResults() bool {
	if r == nil {
		return false
	}
	_, ok := r.local.(ActiveRouteRegistryInto)
	return ok
}

// ownerRecoveryScratch is reused by every group/call in one physical page.
// Growth drops the already-cleared old buffer before reserving its replacement;
// each bounded reread stays under the caller's original owner deadline/fences.
type ownerRecoveryScratch struct {
	rows  []OwnerRoute
	lease *RecoveryResultLease
}

func (s *ownerRecoveryScratch) release() {
	n := cap(s.rows)
	clear(s.rows[:n])
	s.rows = nil
	if n != 0 {
		s.lease.Reduce(uint64(n), uint64(n)*uint64(unsafe.Sizeof(OwnerRoute{})))
	}
}

func (s *ownerRecoveryScratch) grow(n int) error {
	s.release()
	if err := s.lease.TryGrow(uint64(n), uint64(n)*uint64(unsafe.Sizeof(OwnerRoute{}))); err != nil {
		return err
	}
	s.rows = make([]OwnerRoute, n)
	return nil
}

func (r *OwnerRecovery) readOwnedProjection(ctx context.Context, group EndpointLookupGroup, scratch *ownerRecoveryScratch) ([]OwnerRoute, error) {
	registry := r.local.(ActiveRouteRegistryInto)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := r.validate(group.Target); err != nil {
			return nil, err
		}
		if len(scratch.rows) == 0 {
			if err := scratch.grow(16); err != nil {
				return nil, err
			}
		}
		rows, complete := registry.ActiveRoutesByUIDsInto(group.UIDs, scratch.rows[:0])
		if err := r.validate(group.Target); err != nil {
			clear(scratch.rows)
			return nil, err
		}
		if complete {
			return rows, nil
		}
		clear(scratch.rows)
		if cap(scratch.rows) == 4096 {
			return nil, authority.ErrRouteNotReady
		}
		if err := scratch.grow(min(4096, cap(scratch.rows)*4)); err != nil {
			return nil, err
		}
	}
}

func ownedRouteStringBytes(route Route) uint64 {
	return uint64(len(route.UID)) + uint64(len(route.DeviceID)) + uint64(len(route.Listener))
}

// dropOwnedOwnerRoutes clears borrowed headers before returning owned copies'
// backing/string credits. The caller retains its fixed result metadata.
func dropOwnedOwnerRoutes(result *OwnerRouteResult, lease *RecoveryResultLease) {
	rows := result.Snapshot.Routes
	bytes := uint64(cap(rows)) * uint64(unsafe.Sizeof(Route{}))
	for _, row := range rows {
		bytes += ownedRouteStringBytes(row)
	}
	n := cap(rows)
	clear(rows[:n])
	result.Snapshot.Routes = nil
	rows = nil
	lease.Reduce(uint64(n), bytes)
}

// dropOwnedOwnerCall consumes the caller's slice header before returning the
// backing-array credit, so no caller reference outlives the reservation.
func dropOwnedOwnerCall(results *[]OwnerRouteResult, lease *RecoveryResultLease) {
	rows := *results
	for i := range rows {
		dropOwnedOwnerRoutes(&rows[i], lease)
	}
	n := cap(rows)
	clear(rows[:n])
	*results = nil
	rows = nil
	lease.Reduce(0, uint64(n)*uint64(unsafe.Sizeof(OwnerRouteResult{})))
}

func (r *OwnerRecovery) readOwnedCall(ctx context.Context, call RecoveryCall, scratch *ownerRecoveryScratch) ([]OwnerRouteResult, int, error) {
	lease := scratch.lease
	if err := lease.TryGrow(0, uint64(len(call.Groups))*uint64(unsafe.Sizeof(OwnerRouteResult{}))); err != nil {
		return nil, 0, err
	}
	results := make([]OwnerRouteResult, len(call.Groups))
	total := 0
	for i, group := range call.Groups {
		rows, err := r.readOwnedProjection(ctx, group, scratch)
		if errors.Is(err, ErrRecoveryResultBudget) {
			dropOwnedOwnerCall(&results, lease)
			return nil, 0, err
		}
		if err != nil {
			results[i].Err = err
			continue
		}
		stringBytes := uint64(0)
		for _, row := range rows {
			if row.OwnerNodeID != r.nodeID || row.OwnerBootID != r.bootID || row.HashSlot != group.Target.HashSlot {
				err = authority.ErrStaleRoute
				break
			}
			stringBytes += uint64(len(row.UID)) + uint64(len(row.DeviceID)) + uint64(len(row.Listener))
		}
		if err != nil {
			clear(scratch.rows)
			results[i].Err = err
			continue
		}
		if err := lease.TryGrow(uint64(len(rows)), uint64(len(rows))*uint64(unsafe.Sizeof(Route{}))+stringBytes); err != nil {
			clear(scratch.rows)
			dropOwnedOwnerCall(&results, lease)
			return nil, 0, err
		}
		routes := make([]Route, len(rows))
		for j, row := range rows {
			routes[j] = routeFromOwnerRoute(row)
			routes[j].UID = strings.Clone(row.UID)
			routes[j].DeviceID = strings.Clone(row.DeviceID)
			routes[j].Listener = strings.Clone(row.Listener)
		}
		clear(scratch.rows)
		results[i].Snapshot = OwnerRouteSnapshot{OwnerNodeID: r.nodeID, OwnerBootID: r.bootID, Routes: routes}
		total += len(routes)
		if total > 4096 {
			dropOwnedOwnerRoutes(&results[i], lease)
			results[i] = OwnerRouteResult{Err: authority.ErrRouteNotReady}
		}
	}
	// As with the original multi-target port, a later group read can overlap a
	// route movement. Each earlier successful sibling revalidates before return.
	count := 0
	for i, group := range call.Groups {
		if results[i].Err == nil {
			if err := r.validate(group.Target); err != nil {
				dropOwnedOwnerRoutes(&results[i], lease)
				results[i] = OwnerRouteResult{Err: err}
			}
		}
		count += len(results[i].Snapshot.Routes)
	}
	return results, count, nil
}

// ReadOwnerRouteCallsBounded preserves the complete-call page and independent
// 4096-row semantics while reserving each temporary/result allocation first.
// A lookahead call coexists with the earlier page until its fit is known.
func (r *OwnerRecovery) ReadOwnerRouteCallsBounded(ctx context.Context, calls []RecoveryCall, start int, budget *RecoveryResultBudget) (owned OwnedOwnerRouteCallPage, err error) {
	if !ValidOwnerRecoveryCalls(calls, start) || !r.SupportsBoundedRecoveryResults() || r.validate == nil || r.nodeID == 0 || r.bootID == 0 {
		return owned, authority.ErrRouteNotReady
	}
	if err := ctx.Err(); err != nil {
		return owned, err
	}
	lease, err := budget.TryReserve(0, uint64(len(calls)-start)*uint64(unsafe.Sizeof([]OwnerRouteResult{})))
	if err != nil {
		return owned, err
	}
	owned.Lease = lease
	owned.Page = OwnerRouteCallPage{Start: start, Next: start, Results: make([][]OwnerRouteResult, 0, len(calls)-start)}
	scratch := ownerRecoveryScratch{lease: lease}
	defer func() {
		scratch.release()
		if err != nil {
			owned.Release()
		}
	}()
	total := 0
	for i := start; i < len(calls); i++ {
		if err = ctx.Err(); err != nil {
			return owned, err
		}
		var results []OwnerRouteResult
		var count int
		results, count, err = r.readOwnedCall(ctx, calls[i], &scratch)
		if err != nil {
			return owned, err
		}
		if total+count > 4096 {
			dropOwnedOwnerCall(&results, lease)
			break
		}
		owned.Page.Results = append(owned.Page.Results, results)
		owned.Page.Next = i + 1
		total += count
		if total == 4096 {
			break
		}
	}
	for i, results := range owned.Page.Results {
		for j := range results {
			if results[j].Err == nil {
				if fenceErr := r.validate(calls[start+i].Groups[j].Target); fenceErr != nil {
					dropOwnedOwnerRoutes(&results[j], lease)
					results[j] = OwnerRouteResult{Err: fenceErr}
				}
			}
		}
	}
	err = ctx.Err()
	return owned, err
}
