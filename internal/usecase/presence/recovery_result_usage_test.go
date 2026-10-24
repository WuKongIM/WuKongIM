package presence_test

import (
	"strconv"
	"testing"

	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestRecoveryDecodedOwnershipCountsCapacityAndAllRouteStrings(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("release targets use 64-bit Go layouts")
	}
	routes := make([]presence.Route, 1, 2)
	routes[0] = presence.Route{UID: "user", DeviceID: "device", Listener: "tcp"}
	results := [][]presence.EndpointLookupResult{{{Routes: routes}}}
	usage := presence.MeasureRecoveryResults(results)
	// One outer slice header (24), one result (40), two route slots (208),
	// and all three referenced string lengths (4+6+3). Unused capacity has no strings.
	if usage.ResultRows != 1 || usage.ResultDecodedBytes != 285 || usage.PageDecodedBytes != 0 {
		t.Fatalf("decoded ownership=%+v", usage)
	}
	page := presence.OwnerRouteCallPage{Results: [][]presence.OwnerRouteResult{{{Snapshot: presence.OwnerRouteSnapshot{Routes: routes}}}}}
	usage = presence.MeasureRecoveryPage(page)
	// Owner result adds the 16-byte owner identity to its 40-byte route/error shape.
	if usage.PageRows != 1 || usage.PageDecodedBytes != 301 || usage.ResultDecodedBytes != 0 {
		t.Fatalf("decoded page ownership=%+v", usage)
	}
}
