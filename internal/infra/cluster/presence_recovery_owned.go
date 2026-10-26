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
	diagnostic := newRecoveryProviderObservation(len(calls))
	if !r.SupportsBoundedRecoveryResults() || !presence.ValidOwnerRecoveryCalls(calls, 0) {
		return owned, diagnostic.failure(presence.RecoveryProviderInvalidInput, authority.ErrRouteNotReady)
	}
	bytes := uint64(len(calls)) * uint64(unsafe.Sizeof([]presence.EndpointLookupResult{}))
	for _, call := range calls {
		bytes += uint64(len(call.Groups)) * uint64(unsafe.Sizeof(presence.EndpointLookupResult{}))
	}
	diagnostic.begin(presence.RecoveryProviderReserveStage)
	owned.Lease, err = budget.TryReserve(0, bytes)
	diagnostic.end()
	if err != nil {
		return owned, diagnostic.failure(presence.RecoveryProviderResultBudget, err)
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
	diagnostic.begin(presence.RecoveryProviderAdmissionStage)
	select {
	case r.admission <- struct{}{}:
		admission.End(rpctrace.ResultOK)
		diagnostic.end()
	case <-ctx.Done():
		admission.End(rpctrace.ResultFor(ctx.Err()))
		diagnostic.end()
		return owned, diagnostic.failure(presence.RecoveryProviderAdmission, ctx.Err())
	}
	defer func() { <-r.admission }()
	if err := ctx.Err(); err != nil {
		return owned, diagnostic.failure(presence.RecoveryProviderContext, err)
	}
	diagnostic.begin(presence.RecoveryProviderTargetBeforeStage)
	active, proofs := r.activeRecoveryCalls(calls, owned.Results)
	diagnostic.end()
	for _, results := range owned.Results {
		for i := range results {
			if results[i].Err != nil {
				results[i].Err = diagnostic.failure(presence.RecoveryProviderTargetBefore, results[i].Err)
			}
		}
	}
	if len(active) == 0 {
		return owned, nil
	}
	diagnostic.begin(presence.RecoveryProviderSnapshotBeforeStage)
	before, err := r.node.LocalControllerSnapshot(ctx)
	diagnostic.end()
	if err != nil {
		return owned, diagnostic.failure(presence.RecoveryProviderSnapshotBefore, authority.ErrRouteNotReady)
	}
	nodes := recoveryOwnerNodes(before)
	diagnostic.data.OwnersPlanned = uint64(len(nodes))
	if len(nodes) == 0 || len(nodes) > 256 {
		return owned, diagnostic.failure(presence.RecoveryProviderOwnerRoster, authority.ErrRouteNotReady)
	}
	for _, node := range nodes {
		readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		diagnostic.owner(node.NodeID)
		span := rpctrace.Begin(readCtx, rpctrace.StageRecoveryOwner)
		next, readErr := r.readOwnedRecoveryCallPages(readCtx, node.NodeID, active, proofs, budget, func(page presence.OwnerRouteCallPage) error {
			for i, reads := range page.Results {
				proof := &proofs[page.Start+i]
				if !proof.failed {
					if err := mergeOwnedRecoveryCallObserved(owned.Results[proof.index], proof, reads, owned.Lease, &diagnostic); err != nil {
						return diagnostic.failure(presence.RecoveryProviderResultBudget, err)
					}
				}
			}
			return nil
		}, observePage, &diagnostic)
		span.End(rpctrace.ResultFor(readErr))
		diagnostic.ownerEnd()
		diagnostic.ownerError(readErr)
		cancel()
		if err := ctx.Err(); err != nil {
			return owned, diagnostic.failure(presence.RecoveryProviderContext, err)
		}
		if readErr == nil {
			diagnostic.data.OwnersCompleted++
			continue
		}
		if !recoveryCallUnavailable(readErr) {
			if errors.Is(readErr, presence.ErrRecoveryResultBudget) {
				return owned, diagnostic.failure(presence.RecoveryProviderResultBudget, readErr)
			}
			return owned, diagnostic.failure(presence.RecoveryProviderOwnerPage, authority.ErrRouteNotReady)
		}
		if next == 0 && recoveryUnavailableMember(node, readErr) {
			continue
		}
		for i := next; i < len(proofs); i++ {
			proofs[i].failed = true
			failOwnedRecoveryCall(owned.Results[proofs[i].index], owned.Lease, diagnostic.failure(presence.RecoveryProviderOwnerUnavailable, authority.ErrRouteNotReady))
		}
		if observePage != nil {
			observePage(nil)
		}
	}
	diagnostic.begin(presence.RecoveryProviderSnapshotAfterStage)
	after, err := r.node.LocalControllerSnapshot(ctx)
	diagnostic.end()
	if err != nil {
		return owned, diagnostic.failure(presence.RecoveryProviderSnapshotAfter, authority.ErrRouteNotReady)
	}
	if !sameRecoveryOwners(nodes, recoveryOwnerNodes(after)) {
		return owned, diagnostic.failure(presence.RecoveryProviderMembersChanged, authority.ErrRouteNotReady)
	}
	diagnostic.begin(presence.RecoveryProviderTargetAfterStage)
	for _, proof := range proofs {
		for _, j := range proof.groups {
			result := &owned.Results[proof.index][j]
			if result.Err == nil {
				if err := ValidatePresenceTarget(r.node, calls[proof.index].Groups[j].Target); err != nil {
					dropOwnedRecoveryRoutes(result, owned.Lease)
					*result = presence.EndpointLookupResult{Err: diagnostic.failure(presence.RecoveryProviderTargetAfter, err)}
				}
			}
		}
	}
	diagnostic.end()
	if err := ctx.Err(); err != nil {
		return owned, diagnostic.failure(presence.RecoveryProviderContext, err)
	}
	return owned, nil
}

func (r *PresenceRecoveryOwners) readOwnedRecoveryCallPages(ctx context.Context, owner uint64, calls []presence.RecoveryCall, proofs []recoveryCallProof, budget *presence.RecoveryResultBudget, consume func(presence.OwnerRouteCallPage) error, observe func(*presence.OwnerRouteCallPage), diagnostic *recoveryProviderObservation) (int, error) {
	if observe != nil {
		defer func() { observe(nil) }()
	}
	local := r.local.(presence.OwnerRouteCallPageReaderBounded)
	legacy, start := false, 0
	var boot uint64
	for start < len(calls) {
		diagnostic.page(start)
		if err := ctx.Err(); err != nil {
			return start, err
		}
		var owned presence.OwnedOwnerRouteCallPage
		var err error
		var capture *rpctrace.TerminalCapture
		switch {
		case legacy:
			mode := presence.RecoveryProviderOwnerRemoteTargets
			if len(calls[start].Groups) == 1 {
				mode = presence.RecoveryProviderOwnerRemoteSingle
			}
			diagnostic.dispatch(ctx, mode)
			callCtx, terminal := rpctrace.WithTerminalCapture(ctx)
			capture = terminal
			owned, err = r.remote.ReadOwnerRoutesByTargetsBounded(callCtx, owner, calls[start].Groups, budget)
			if err == nil {
				owned.Page.Start, owned.Page.Next = start, start+1
			}
		case owner == r.node.NodeID():
			diagnostic.dispatch(ctx, presence.RecoveryProviderOwnerLocal)
			owned, err = local.ReadOwnerRouteCallsBounded(ctx, calls, start, budget)
		default:
			diagnostic.dispatch(ctx, presence.RecoveryProviderOwnerRemoteCalls)
			callCtx, terminal := rpctrace.WithTerminalCapture(ctx)
			capture = terminal
			owned, err = r.remote.ReadOwnerRouteCallsBounded(callCtx, owner, calls, start, budget)
			if start == 0 && errors.Is(err, presence.ErrOwnerRouteCallsUnsupported) {
				owned.Release()
				legacy = true
				continue
			}
		}
		diagnostic.data.OwnerRPCTrace = capture.Snapshot()
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
			diagnostic.data.PageNext = uint64(page.Next)
			diagnostic.data.Observed |= presence.RecoveryProviderObservedPageNext
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
	return mergeOwnedRecoveryCallObserved(results, proof, reads, lease, nil)
}

func mergeOwnedRecoveryCallObserved(results []presence.EndpointLookupResult, proof *recoveryCallProof, reads []presence.OwnerRouteResult, lease *presence.RecoveryResultLease, diagnostic *recoveryProviderObservation) error {
	for _, read := range reads {
		if read.Err == nil {
			if len(read.Snapshot.Routes) > 4096-proof.rows {
				proof.failed = true
				err := authority.ErrRouteNotReady
				if diagnostic != nil {
					err = diagnostic.failure(presence.RecoveryProviderCallLimit, err)
				}
				failOwnedRecoveryCall(results, lease, err)
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
			if diagnostic != nil {
				diagnostic.ownerError(err)
			}
			if recoveryCallUnavailable(err) {
				err = authority.ErrRouteNotReady
			}
			if diagnostic != nil {
				err = diagnostic.failure(presence.RecoveryProviderOwnerResult, err)
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
