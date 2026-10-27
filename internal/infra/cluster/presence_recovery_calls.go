package cluster

import (
	"context"
	"errors"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// recoveryCallProof maps filtered targets back to one original caller. Its row
// budget spans every owner, independently of the other calls in the batch.
type recoveryCallProof struct {
	index   int
	groups  []int
	allowed []map[string]struct{}
	rows    int
	failed  bool
}

// RecoverRouteCalls shares the existing owner-round gate while preserving each
// original call's route budget and all-owner proof. Pages are consumed one at a
// time; the caller owns admission for retained original-call result credits.
func (r *PresenceRecoveryOwners) RecoverRouteCalls(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
	return r.RecoverRouteCallsObserved(ctx, calls, nil)
}

// RecoverRouteCallsObserved reports aggregate and transient decoded ownership
// at page boundaries, including partial proof held while another owner waits.
// The callback borrows only scalar counts; it does not alter proof or admission.
func (r *PresenceRecoveryOwners) RecoverRouteCallsObserved(ctx context.Context, calls []presence.RecoveryCall, observe func(presence.RecoveryResultUsage)) [][]presence.EndpointLookupResult {
	results := make([][]presence.EndpointLookupResult, len(calls))
	for i, call := range calls {
		results[i] = make([]presence.EndpointLookupResult, len(call.Groups))
	}
	var observePage func(*presence.OwnerRouteCallPage)
	if observe != nil {
		observePage = func(page *presence.OwnerRouteCallPage) {
			usage := presence.MeasureRecoveryResults(results)
			if page != nil {
				pageUsage := presence.MeasureRecoveryPage(*page)
				usage.PageRows, usage.PageDecodedBytes = pageUsage.PageRows, pageUsage.PageDecodedBytes
			}
			observe(usage)
		}
		observePage(nil)
		// Return transfers the final result slice; malformed/fence failures have
		// already removed their routes, and no owner page remains retained.
		defer func() { observePage(nil) }()
	}
	fail := func(err error) [][]presence.EndpointLookupResult {
		for _, call := range results {
			failRecoveryCall(call, err)
		}
		if observePage != nil {
			observePage(nil)
		}
		return results
	}
	if r == nil || r.node == nil || r.local == nil || !presence.ValidOwnerRecoveryCalls(calls, 0) {
		return fail(authority.ErrRouteNotReady)
	}
	readChainCtx, cancelReadChain := context.WithTimeout(ctx, recoveryReadChainBudget)
	defer cancelReadChain()
	admission := rpctrace.Begin(readChainCtx, rpctrace.StageRecoveryOwners)
	select {
	case r.admission <- struct{}{}:
		admission.End(rpctrace.ResultOK)
	case <-readChainCtx.Done():
		admission.End(rpctrace.ResultFor(readChainCtx.Err()))
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		return fail(authority.ErrRouteNotReady)
	}
	defer func() { <-r.admission }()
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	active, proofs := r.activeRecoveryCalls(calls, results)
	if len(active) == 0 {
		return results
	}
	before, err := r.node.LocalControllerSnapshot(readChainCtx)
	if err != nil {
		return fail(authority.ErrRouteNotReady)
	}
	nodes := recoveryOwnerNodes(before)
	if len(nodes) == 0 || len(nodes) > 256 {
		return fail(authority.ErrRouteNotReady)
	}
	failUnproven := func(start int) {
		for i := start; i < len(proofs); i++ {
			if proofs[i].failed {
				continue
			}
			proofs[i].failed = true
			failRecoveryCall(results[proofs[i].index], authority.ErrRouteNotReady)
		}
	}
	for ownerIndex, node := range nodes {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if readChainCtx.Err() != nil {
			failUnproven(0)
			break
		}
		readCtx, cancel := context.WithTimeout(readChainCtx, recoveryOwnerReadBudget)
		span := rpctrace.Begin(readCtx, rpctrace.StageRecoveryOwner)
		next, readErr := r.readRecoveryCallPages(readCtx, node.NodeID, active, proofs, func(page presence.OwnerRouteCallPage) {
			for i, reads := range page.Results {
				proof := &proofs[page.Start+i]
				if !proof.failed {
					mergeRecoveryCall(results[proof.index], proof, reads)
				}
			}
		}, observePage)
		span.End(rpctrace.ResultFor(readErr))
		cancel()
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if readErr == nil {
			if readChainCtx.Err() != nil && ownerIndex < len(nodes)-1 {
				failUnproven(0)
				break
			}
			if allRecoveryCallsFailed(proofs) {
				break
			}
			continue
		}
		if !recoveryCallUnavailable(readErr) {
			// Malformed pages and changing owner identities invalidate this round,
			// including proof collected before the contradiction was observed.
			return fail(authority.ErrRouteNotReady)
		}
		if readChainCtx.Err() != nil {
			if ownerIndex < len(nodes)-1 {
				next = 0
			}
			failUnproven(next)
			break
		}
		if next == 0 && recoveryUnavailableMember(node, readErr) {
			continue
		}
		// Earlier complete calls retain this owner's proof. Unfinished calls
		// cannot infer absence, retry, or consume a fresh owner deadline.
		for i := next; i < len(proofs); i++ {
			proofs[i].failed = true
			failRecoveryCall(results[proofs[i].index], authority.ErrRouteNotReady)
		}
		if observePage != nil {
			observePage(nil)
		}
		if allRecoveryCallsFailed(proofs) {
			break
		}
	}
	cancelReadChain()
	after, err := r.node.LocalControllerSnapshot(ctx)
	if err != nil || !sameRecoveryOwners(nodes, recoveryOwnerNodes(after)) {
		return fail(authority.ErrRouteNotReady)
	}
	for _, proof := range proofs {
		for _, j := range proof.groups {
			if results[proof.index][j].Err == nil {
				if err := ValidatePresenceTarget(r.node, calls[proof.index].Groups[j].Target); err != nil {
					results[proof.index][j] = presence.EndpointLookupResult{Err: err}
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return results
}

func failRecoveryCall(results []presence.EndpointLookupResult, err error) {
	for i := range results {
		results[i] = presence.EndpointLookupResult{Err: err}
	}
}

func (r *PresenceRecoveryOwners) activeRecoveryCalls(calls []presence.RecoveryCall, results [][]presence.EndpointLookupResult) ([]presence.RecoveryCall, []recoveryCallProof) {
	active := make([]presence.RecoveryCall, 0, len(calls))
	proofs := make([]recoveryCallProof, 0, len(calls))
	for i, call := range calls {
		var filtered presence.RecoveryCall
		proof := recoveryCallProof{index: i}
		for j, group := range call.Groups {
			if err := ValidatePresenceTarget(r.node, group.Target); err != nil {
				results[i][j].Err = err
				continue
			}
			filtered.Groups = append(filtered.Groups, group)
			proof.groups = append(proof.groups, j)
			allowed := make(map[string]struct{}, len(group.UIDs))
			for _, uid := range group.UIDs {
				allowed[uid] = struct{}{}
			}
			proof.allowed = append(proof.allowed, allowed)
		}
		if len(filtered.Groups) != 0 {
			active = append(active, filtered)
			proofs = append(proofs, proof)
		}
	}
	return active, proofs
}

// readRecoveryCallPages owns one owner's fixed deadline and boot identity. Only
// a first-page unsupported remote operation permits the legacy per-call path.
func (r *PresenceRecoveryOwners) readRecoveryCallPages(ctx context.Context, owner uint64, calls []presence.RecoveryCall, proofs []recoveryCallProof, consume func(presence.OwnerRouteCallPage), observePage func(*presence.OwnerRouteCallPage)) (int, error) {
	if observePage != nil {
		defer func() { observePage(nil) }()
	}
	local, hasLocalPages := r.local.(presence.OwnerRouteCallPageReader)
	legacy := owner == r.node.NodeID() && !hasLocalPages
	start := 0
	var boot uint64
	for start < len(calls) {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		var page presence.OwnerRouteCallPage
		var err error
		switch {
		case legacy:
			var rows []presence.OwnerRouteResult
			rows, err = r.readOwnerPage(ctx, owner, calls[start].Groups)
			page = presence.OwnerRouteCallPage{Start: start, Next: start + 1, Results: [][]presence.OwnerRouteResult{rows}}
		case owner == r.node.NodeID():
			page, err = local.ReadOwnerRouteCalls(ctx, calls, start)
		default:
			page, err = r.remote.ReadOwnerRouteCalls(ctx, owner, calls, start)
			if start == 0 && errors.Is(err, presence.ErrOwnerRouteCallsUnsupported) {
				legacy = true
				continue
			}
		}
		if observePage != nil {
			observePage(&page)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return start, ctxErr
		}
		if err != nil {
			return start, err
		}
		if !validRecoveryCallPage(page, start, owner, &boot, calls, proofs) {
			return start, authority.ErrStaleRoute
		}
		consume(page)
		start = page.Next
		if observePage != nil {
			observePage(&page)
		}
		page = presence.OwnerRouteCallPage{}
		if observePage != nil {
			observePage(nil)
		}
	}
	return start, nil
}

// validRecoveryCallPage checks complete-call boundaries and all contextual
// identities before any page is retained, including empty successful snapshots.
func validRecoveryCallPage(page presence.OwnerRouteCallPage, start int, owner uint64, boot *uint64, calls []presence.RecoveryCall, proofs []recoveryCallProof) bool {
	if page.Start != start || page.Next <= start || page.Next > len(calls) || len(page.Results) != page.Next-start {
		return false
	}
	total := 0
	for i, reads := range page.Results {
		if len(reads) != len(calls[start+i].Groups) {
			return false
		}
		for j, read := range reads {
			snapshot := read.Snapshot
			if len(snapshot.Routes) > 4096-total {
				return false
			}
			total += len(snapshot.Routes)
			if read.Err != nil {
				if len(snapshot.Routes) != 0 {
					return false
				}
				continue
			}
			if snapshot.OwnerNodeID != owner || snapshot.OwnerBootID == 0 || (*boot != 0 && snapshot.OwnerBootID != *boot) {
				return false
			}
			*boot = snapshot.OwnerBootID
			for _, route := range snapshot.Routes {
				if _, ok := proofs[start+i].allowed[j][route.UID]; !ok || route.OwnerNodeID != owner || route.OwnerBootID != *boot || route.SessionID == 0 {
					return false
				}
			}
		}
	}
	return true
}

func mergeRecoveryCall(results []presence.EndpointLookupResult, proof *recoveryCallProof, reads []presence.OwnerRouteResult) {
	for _, read := range reads {
		if read.Err == nil {
			if len(read.Snapshot.Routes) > 4096-proof.rows {
				proof.failed = true
				failRecoveryCall(results, authority.ErrRouteNotReady)
				return
			}
			proof.rows += len(read.Snapshot.Routes)
		}
	}
	for j, read := range reads {
		i := proof.groups[j]
		if results[i].Err != nil {
			continue
		}
		if read.Err != nil {
			err := read.Err
			if recoveryCallUnavailable(err) {
				err = authority.ErrRouteNotReady
			}
			results[i] = presence.EndpointLookupResult{Err: err}
			continue
		}
		results[i].Routes = append(results[i].Routes, read.Snapshot.Routes...)
	}
}

func recoveryCallUnavailable(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrTimeout) || errors.Is(err, transport.ErrBusy) || errors.Is(err, transport.ErrDialFailed) || errors.Is(err, transport.ErrNodeNotFound)
}

func recoveryUnavailableMember(node control.Node, err error) bool {
	return (node.Health.Freshness == control.NodeHealthStale || node.Health.Status == control.NodeDown) &&
		(errors.Is(err, context.DeadlineExceeded) || errors.Is(err, transport.ErrTimeout) || errors.Is(err, transport.ErrDialFailed) || errors.Is(err, transport.ErrNodeNotFound))
}
