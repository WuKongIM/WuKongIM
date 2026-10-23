package presence

import (
	"context"
	"unsafe"
)

// RecoveryResultUsage separates the retained aggregate from the currently
// decoded owner page. These are structural byte estimates, not heap/RSS bounds.
type RecoveryResultUsage struct {
	ResultRows, ResultDecodedBytes, PageRows, PageDecodedBytes uint64
}

// RecoveryCallBatchOwnersObserved optionally reports synchronous ownership
// snapshots until return. Observers cannot retain payloads or change outcomes.
type RecoveryCallBatchOwnersObserved interface {
	RecoverRouteCallsObserved(context.Context, []RecoveryCall, func(RecoveryResultUsage)) [][]EndpointLookupResult
}

// MeasureRecoveryResults counts visible slice capacity and every live route's
// UID, device and listener bytes. Aliases are charged per reference. Hidden
// backing prefixes, allocator rounding, error objects, maps and wire buffers
// are excluded; this census must not be presented as an actual heap bound.
func MeasureRecoveryResults(results [][]EndpointLookupResult) RecoveryResultUsage {
	u := RecoveryResultUsage{ResultDecodedBytes: uint64(cap(results)) * uint64(unsafe.Sizeof([]EndpointLookupResult{}))}
	for _, groups := range results {
		u.ResultDecodedBytes += uint64(cap(groups)) * uint64(unsafe.Sizeof(EndpointLookupResult{}))
		for _, g := range groups {
			u.ResultRows += uint64(len(g.Routes))
			u.ResultDecodedBytes += recoveryRouteBytes(g.Routes)
		}
	}
	return u
}

// MeasureRecoveryPage uses the same structural accounting for an owner page,
// separately from the accumulator which may hold references to its strings.
func MeasureRecoveryPage(page OwnerRouteCallPage) RecoveryResultUsage {
	u := RecoveryResultUsage{PageDecodedBytes: uint64(cap(page.Results)) * uint64(unsafe.Sizeof([]OwnerRouteResult{}))}
	for _, groups := range page.Results {
		u.PageDecodedBytes += uint64(cap(groups)) * uint64(unsafe.Sizeof(OwnerRouteResult{}))
		for _, g := range groups {
			u.PageRows += uint64(len(g.Snapshot.Routes))
			u.PageDecodedBytes += recoveryRouteBytes(g.Snapshot.Routes)
		}
	}
	return u
}

func recoveryRouteBytes(routes []Route) uint64 {
	n := uint64(cap(routes)) * uint64(unsafe.Sizeof(Route{}))
	for _, route := range routes {
		n += uint64(len(route.UID)) + uint64(len(route.DeviceID)) + uint64(len(route.Listener))
	}
	return n
}
