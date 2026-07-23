package cluster

import (
	"context"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

const (
	cloudMediumPresenceBenchmarkGroups = 256
	cloudMediumPresenceBenchmarkItems  = 19_650
)

var cloudMediumPresenceBenchmarkResultSink []presence.EndpointLookupResult

// benchmarkTargetBatchPresenceAuthority isolates client-side exact-target
// leader grouping; the production local directory cost has separate coverage.
type benchmarkTargetBatchPresenceAuthority struct {
	*fakePresenceAuthority
	results []presence.EndpointLookupResult
}

func (a *benchmarkTargetBatchPresenceAuthority) EndpointsByTargets(
	_ context.Context,
	_ []presence.EndpointLookupGroup,
) []presence.EndpointLookupResult {
	return a.results
}

func newCloudMediumPresenceBenchmark() (*PresenceAuthorityClient, []presence.EndpointLookupGroup) {
	groups := make([]presence.EndpointLookupGroup, cloudMediumPresenceBenchmarkGroups)
	for groupIndex := range groups {
		groups[groupIndex].Target = testEndpointLookupTarget(uint16(groupIndex), 1)
	}
	for itemIndex := 0; itemIndex < cloudMediumPresenceBenchmarkItems; itemIndex++ {
		groupIndex := itemIndex % len(groups)
		groups[groupIndex].UIDs = append(groups[groupIndex].UIDs, strconv.Itoa(itemIndex))
	}
	local := &benchmarkTargetBatchPresenceAuthority{
		fakePresenceAuthority: &fakePresenceAuthority{},
		results:               make([]presence.EndpointLookupResult, len(groups)),
	}
	return NewPresenceAuthorityClient(&fakePresenceCluster{nodeID: 1}, local), groups
}

func TestPresenceAuthorityClientSingleLeaderCloudMediumAllocations(t *testing.T) {
	client, groups := newCloudMediumPresenceBenchmark()
	allocations := testing.AllocsPerRun(20, func() {
		cloudMediumPresenceBenchmarkResultSink = client.EndpointsByTargets(context.Background(), groups)
	})
	if got := len(cloudMediumPresenceBenchmarkResultSink); got != len(groups) {
		t.Fatalf("EndpointsByTargets() len = %d, want %d", got, len(groups))
	}
	if allocations > 2 {
		t.Fatalf("EndpointsByTargets() allocations = %.2f, want at most two aligned result allocations", allocations)
	}
}

// BenchmarkPresenceAuthorityClientSingleLeaderCloudMedium measures the normal
// 256-physical-hash-slot / one-local-leader target grouping overhead for one
// Cloud Medium-shaped recipient feedback slice.
func BenchmarkPresenceAuthorityClientSingleLeaderCloudMedium(b *testing.B) {
	client, groups := newCloudMediumPresenceBenchmark()

	b.ReportAllocs()
	b.ReportMetric(cloudMediumPresenceBenchmarkGroups, "target-groups/op")
	b.ReportMetric(cloudMediumPresenceBenchmarkItems, "items/op")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloudMediumPresenceBenchmarkResultSink = client.EndpointsByTargets(context.Background(), groups)
		if len(cloudMediumPresenceBenchmarkResultSink) != len(groups) {
			b.Fatalf("EndpointsByTargets() len = %d, want %d", len(cloudMediumPresenceBenchmarkResultSink), len(groups))
		}
	}
}
