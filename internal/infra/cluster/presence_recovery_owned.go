package cluster

import (
	"context"
	"errors"
	"strings"
	"time"
	"unsafe"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// SupportsBoundedRecoveryResults requires the local dependency's allocation
// contract as well as its method set. Unknown embedding readers keep the legacy
// original-call credit guard; remote new/old wire decoders reserve before decode.
func (r *PresenceRecoveryOwners) SupportsBoundedRecoveryResults() bool {
	if r == nil || r.node == nil || r.remote == nil || r.local == nil {
		return false
	}
	capability, ok := r.local.(interface{ SupportsBoundedRecoveryResults() bool })
	if !ok || !capability.SupportsBoundedRecoveryResults() {
		return false
	}
	_, ok = r.local.(presence.OwnerRouteCallPageReaderBounded)
	return ok
}

// RecoverRouteCallsBounded keeps each caller's all-owner proof under one joint
// nonwaiting result budget. Input selection remains under the existing fixed
// call/group/UID bounds; only decoded output, scratch and retained copies use
// this additional ledger. No page publishes a Directory proof independently.
func (r *PresenceRecoveryOwners) RecoverRouteCallsBounded(ctx context.Context, calls []presence.RecoveryCall, budget *presence.RecoveryResultBudget, observe func(presence.RecoveryResultUsage)) (owned presence.OwnedRecoveryResults, err error) {
	if !r.SupportsBoundedRecoveryResults() || !presence.ValidOwnerRecoveryCalls(calls, 0) {
		return owned, authority.ErrRouteNotReady
	}
	bytes := uint64(len(calls)) * uint64(unsafe.Sizeof([]presence.EndpointLookupResult{}))
	for _, call := range calls {
		bytes += uint64(len(call.Groups)) * uint64(unsafe.Sizeof(presence.EndpointLookupResult{}))
	}
	owned.Lease, err = budget.TryReserve(0, bytes)
	if err != nil {
		return owned, err
	}
	owned.Results = make([][]presence.EndpointLookupResult, len(calls))
	for i, call := range calls {
		owned.Results[i] = make([]presence.EndpointLookupResult, len(call.Groups))
	}
	defer func() {
		if err != nil {
			owned.Release()
		}
		if observe != nil {
			observe(presence.MeasureRecoveryResults(owned.Results))
		}
	}()
	var observePage func(*presence.OwnerRouteCallPage)
	if observe != nil {
		observePage = func(page *presence.OwnerRouteCallPage) {
			usage := presence.MeasureRecoveryResults(owned.Results)
			if page != nil {
				p := presence.MeasureRecoveryPage(*page)
				usage.PageRows, usage.PageDecodedBytes = p.PageRows, p.PageDecodedBytes
			}
			observe(usage)
		}
		observePage(nil)
	}
	admission := rpctrace.Begin(ctx, rpctrace.StageRecoveryOwners)
	select {
	case r.admission <- struct{}{}:
		admission.End(rpctrace.ResultOK)
	case <-ctx.Done():
		admission.End(rpctrace.ResultFor(ctx.Err()))
		return owned, ctx.Err()
	}
	defer func() { <-r.admission }()
	if err := ctx.Err(); err != nil {
		return owned, err
	}
	active, proofs := r.activeRecoveryCalls(calls, owned.Results)
	if len(active) == 0 {
		return owned, nil
	}
	before, err := r.node.LocalControllerSnapshot(ctx)
	if err != nil {
		return owned, authority.ErrRouteNotReady
	}
	nodes := recoveryOwnerNodes(before)
	if len(nodes) == 0 || len(nodes) > 256 {
		return owned, authority.ErrRouteNotReady
	}
	for _, node := range nodes {
		readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		span := rpctrace.Begin(readCtx, rpctrace.StageRecoveryOwner)
		next, readErr := r.readOwnedRecoveryCallPages(readCtx, node.NodeID, active, proofs, budget, func(page presence.OwnerRouteCallPage) error {
			for i, reads := range page.Results {
				proof := &proofs[page.Start+i]
				if !proof.failed {
					if err := mergeOwnedRecoveryCall(owned.Results[proof.index], proof, reads, owned.Lease); err != nil {
						return err
					}
				}
			}
			return nil
		}, observePage)
		span.End(rpctrace.ResultFor(readErr))
		cancel()
		if err := ctx.Err(); err != nil {
			return owned, err
		}
		if readErr == nil {
			continue
		}
		if !recoveryCallUnavailable(readErr) {
			if errors.Is(readErr, presence.ErrRecoveryResultBudget) {
				return owned, readErr
			}
			return owned, authority.ErrRouteNotReady
		}
		if next == 0 && recoveryUnavailableMember(node, readErr) {
			continue
		}
		for i := next; i < len(proofs); i++ {
			proofs[i].failed = true
			failOwnedRecoveryCall(owned.Results[proofs[i].index], owned.Lease, authority.ErrRouteNotReady)
		}
		if observePage != nil {
			observePage(nil)
		}
	}
	after, err := r.node.LocalControllerSnapshot(ctx)
	if err != nil || !sameRecoveryOwners(nodes, recoveryOwnerNodes(after)) {
		return owned, authority.ErrRouteNotReady
	}
	for _, proof := range proofs {
		for _, j := range proof.groups {
			result := &owned.Results[proof.index][j]
			if result.Err == nil {
				if err := ValidatePresenceTarget(r.node, calls[proof.index].Groups[j].Target); err != nil {
					dropOwnedRecoveryRoutes(result, owned.Lease)
					*result = presence.EndpointLookupResult{Err: err}
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return owned, err
	}
	return owned, nil
}

func (r *PresenceRecoveryOwners) readOwnedRecoveryCallPages(ctx context.Context, owner uint64, calls []presence.RecoveryCall, proofs []recoveryCallProof, budget *presence.RecoveryResultBudget, consume func(presence.OwnerRouteCallPage) error, observe func(*presence.OwnerRouteCallPage)) (int, error) {
	if observe != nil {
		defer func() { observe(nil) }()
	}
	local := r.local.(presence.OwnerRouteCallPageReaderBounded)
	legacy, start := false, 0
	var boot uint64
	for start < len(calls) {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		var owned presence.OwnedOwnerRouteCallPage
		var err error
		switch {
		case legacy:
			owned, err = r.remote.ReadOwnerRoutesByTargetsBounded(ctx, owner, calls[start].Groups, budget)
			if err == nil {
				owned.Page.Start, owned.Page.Next = start, start+1
			}
		case owner == r.node.NodeID():
			owned, err = local.ReadOwnerRouteCallsBounded(ctx, calls, start, budget)
		default:
			owned, err = r.remote.ReadOwnerRouteCallsBounded(ctx, owner, calls, start, budget)
			if start == 0 && errors.Is(err, presence.ErrOwnerRouteCallsUnsupported) {
				owned.Release()
				legacy = true
				continue
			}
		}
		page := owned.Page
		if observe != nil {
			observe(&page)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		if err == nil && !validRecoveryCallPage(page, start, owner, &boot, calls, proofs) {
			err = authority.ErrStaleRoute
		}
		if err == nil {
			err = consume(page)
		}
		next := page.Next
		if observe != nil {
			observe(&page)
		}
		page = presence.OwnerRouteCallPage{}
		owned.Release()
		if observe != nil {
			observe(nil)
		}
		if err != nil {
			return start, err
		}
		start = next
	}
	return start, nil
}

func retainedRouteStringBytes(route presence.Route) uint64 {
	return uint64(len(route.UID)) + uint64(len(route.DeviceID)) + uint64(len(route.Listener))
}

func dropOwnedRecoveryRoutes(result *presence.EndpointLookupResult, lease *presence.RecoveryResultLease) {
	rows := result.Routes
	bytes := uint64(cap(rows)) * uint64(unsafe.Sizeof(presence.Route{}))
	for _, route := range rows {
		bytes += retainedRouteStringBytes(route)
	}
	n := cap(rows)
	clear(rows[:n])
	result.Routes = nil
	rows = nil
	lease.Reduce(uint64(n), bytes)
}

func failOwnedRecoveryCall(results []presence.EndpointLookupResult, lease *presence.RecoveryResultLease, err error) {
	for i := range results {
		dropOwnedRecoveryRoutes(&results[i], lease)
		results[i] = presence.EndpointLookupResult{Err: err}
	}
}

// mergeOwnedRecoveryCall charges old+new backing while copying and keeps page
// strings independently owned until its caller releases that whole page.
func mergeOwnedRecoveryCall(results []presence.EndpointLookupResult, proof *recoveryCallProof, reads []presence.OwnerRouteResult, lease *presence.RecoveryResultLease) error {
	for _, read := range reads {
		if read.Err == nil {
			if len(read.Snapshot.Routes) > 4096-proof.rows {
				proof.failed = true
				failOwnedRecoveryCall(results, lease, authority.ErrRouteNotReady)
				return nil
			}
			proof.rows += len(read.Snapshot.Routes)
		}
	}
	for j, read := range reads {
		result := &results[proof.groups[j]]
		if result.Err != nil {
			continue
		}
		if read.Err != nil {
			err := read.Err
			if recoveryCallUnavailable(err) {
				err = authority.ErrRouteNotReady
			}
			dropOwnedRecoveryRoutes(result, lease)
			*result = presence.EndpointLookupResult{Err: err}
			continue
		}
		rows := read.Snapshot.Routes
		if len(rows) == 0 {
			continue
		}
		old := result.Routes
		newLen, newCap := len(old)+len(rows), cap(old)
		allocationRows, allocationBytes := uint64(0), uint64(0)
		if newLen > newCap {
			newCap = max(newLen, min(4096, newCap*2))
			allocationRows = uint64(newCap)
			allocationBytes = uint64(newCap) * uint64(unsafe.Sizeof(presence.Route{}))
		}
		for _, route := range rows {
			allocationBytes += retainedRouteStringBytes(route)
		}
		if err := lease.TryGrow(allocationRows, allocationBytes); err != nil {
			return err
		}
		oldLen := len(old)
		if allocationRows != 0 {
			grown := make([]presence.Route, newLen, newCap)
			copy(grown, old)
			oldCap := cap(old)
			clear(old[:oldCap])
			result.Routes = nil
			old = nil
			lease.Reduce(uint64(oldCap), uint64(oldCap)*uint64(unsafe.Sizeof(presence.Route{})))
			result.Routes = grown
		} else {
			result.Routes = old[:newLen]
		}
		for i, route := range rows {
			route.UID = strings.Clone(route.UID)
			route.DeviceID = strings.Clone(route.DeviceID)
			route.Listener = strings.Clone(route.Listener)
			result.Routes[oldLen+i] = route
		}
	}
	return nil
}
