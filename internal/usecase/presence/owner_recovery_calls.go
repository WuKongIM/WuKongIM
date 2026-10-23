package presence

import (
	"context"
	"errors"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// ErrOwnerRouteCallsUnsupported is positive evidence of an unsupported call-page
// operation. Timeouts, malformed replies and generic failures never imply it.
var ErrOwnerRouteCallsUnsupported = errors.New("presence: owner route calls unsupported")

// RecoveryCall preserves one original caller's independent result boundary.
type RecoveryCall struct {
	Groups []EndpointLookupGroup
}

// RecoveryCallBatchOwners reads independent calls without sharing their route cap.
type RecoveryCallBatchOwners interface {
	RecoverRouteCalls(context.Context, []RecoveryCall) [][]EndpointLookupResult
}

// OwnerRouteCallPage contains complete calls in the half-open Start/Next range.
// Continuations never split a call or publish a partial offline proof.
// Only a successfully returned page carries proof; an errored unreturned prefix
// must not be treated as completed calls.
type OwnerRouteCallPage struct {
	Start   int
	Next    int
	Results [][]OwnerRouteResult
}

// OwnerRouteCallPageReader limits each page to 4096 routes while retaining each
// original call's separate 4096-route availability.
type OwnerRouteCallPageReader interface {
	ReadOwnerRouteCalls(context.Context, []RecoveryCall, int) (OwnerRouteCallPage, error)
}

// ValidOwnerRecoveryCalls checks the complete batch before any owner work.
func ValidOwnerRecoveryCalls(calls []RecoveryCall, start int) bool {
	if len(calls) == 0 || len(calls) > 16 || start < 0 || start >= len(calls) {
		return false
	}
	groups, uids := 0, 0
	for _, call := range calls {
		if !ValidOwnerRecoveryPage(call.Groups) {
			return false
		}
		groups += len(call.Groups)
		for _, group := range call.Groups {
			uids += len(group.UIDs)
		}
		if groups > 256 || uids > authority.RecoveryBatchSize {
			return false
		}
	}
	return uids > 0
}

// ReadOwnerRouteCalls returns complete original calls up to the page route cap.
// A following call that does not fit is reread under fresh fences on its page.
func (r *OwnerRecovery) ReadOwnerRouteCalls(ctx context.Context, calls []RecoveryCall, start int) (OwnerRouteCallPage, error) {
	page := OwnerRouteCallPage{Start: start, Next: start}
	if !ValidOwnerRecoveryCalls(calls, start) || r == nil || r.validate == nil {
		return page, authority.ErrRouteNotReady
	}
	total := 0
	for i := start; i < len(calls); i++ {
		if err := ctx.Err(); err != nil {
			return OwnerRouteCallPage{}, err
		}
		results := r.ReadOwnerRoutesByTargets(ctx, calls[i].Groups)
		count := 0
		for _, result := range results {
			count += len(result.Snapshot.Routes)
		}
		if total+count > 4096 {
			break
		}
		page.Results = append(page.Results, results)
		page.Next = i + 1
		total += count
		// A full page cannot admit another call. Preserve its final fence/context
		// checks without spending this page's budget on the next caller.
		if total == 4096 {
			break
		}
	}
	for i, results := range page.Results {
		for j := range results {
			if results[j].Err == nil {
				if err := r.validate(calls[start+i].Groups[j].Target); err != nil {
					results[j] = OwnerRouteResult{Err: err}
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return OwnerRouteCallPage{}, err
	}
	return page, nil
}
