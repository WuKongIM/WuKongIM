package cluster

import (
	"context"
	"errors"
	"strconv"
	"testing"

	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

const (
	recipientAuthorityBenchmarkItems   = 512
	recipientAuthorityBenchmarkTargets = 221
)

// recipientAuthorityObservabilityBenchmarkNode returns one aligned immutable
// authority snapshot. It keeps the benchmark focused on the production
// adapter conversion, distinct-target accounting, and optional metrics cost.
type recipientAuthorityObservabilityBenchmarkNode struct {
	results []pkgcluster.RouteAuthorityResult
}

func (n *recipientAuthorityObservabilityBenchmarkNode) RouteKey(string) (pkgcluster.Route, error) {
	return pkgcluster.Route{}, errors.New("unexpected single recipient authority lookup")
}

func (n *recipientAuthorityObservabilityBenchmarkNode) RouteAuthoritiesPartial([]string) ([]pkgcluster.RouteAuthorityResult, error) {
	return n.results, nil
}

type recipientAuthorityMetricsObserver struct {
	metrics *obsmetrics.Registry
}

func (o recipientAuthorityMetricsObserver) ObserveRecipientAuthorityResolve(event RecipientAuthorityResolveObservation) {
	o.metrics.Delivery.ObserveRecipientAuthorityResolve(event.Result, event.Items, event.Targets, event.Duration)
}

// BenchmarkRecipientAuthorityResolveObservabilityCloudMedium compares the real
// 512-item authority adapter with metrics disabled and enabled. It complements
// the cross-commit production benchmark, which starts from grouped targets.
func BenchmarkRecipientAuthorityResolveObservabilityCloudMedium(b *testing.B) {
	uids := make([]string, recipientAuthorityBenchmarkItems)
	results := make([]pkgcluster.RouteAuthorityResult, recipientAuthorityBenchmarkItems)
	for i := range uids {
		uids[i] = strconv.Itoa(i)
		hashSlot := uint16(i % recipientAuthorityBenchmarkTargets)
		results[i].Authority = pkgcluster.RouteAuthority{
			HashSlot: hashSlot, SlotID: uint32(hashSlot%10 + 1), LeaderNodeID: 1,
			LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1,
		}
	}
	node := &recipientAuthorityObservabilityBenchmarkNode{results: results}
	for _, enabled := range []bool{false, true} {
		name := "metrics-disabled"
		var observer RecipientAuthorityResolveObserver
		if enabled {
			name = "metrics-enabled"
			observer = recipientAuthorityMetricsObserver{metrics: obsmetrics.New(1, "benchmark")}
		}
		resolver := NewRecipientAuthorityResolver(node, observer)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(recipientAuthorityBenchmarkItems, "items/op")
			b.ReportMetric(recipientAuthorityBenchmarkTargets, "targets/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resolved, err := resolver.ResolveRecipientAuthorities(context.Background(), uids)
				if err != nil || len(resolved) != recipientAuthorityBenchmarkItems {
					b.Fatalf("ResolveRecipientAuthorities() len/error = %d/%v", len(resolved), err)
				}
			}
		})
	}
}

func TestRecipientAuthorityResolveObservabilityCloudMediumAllocations(t *testing.T) {
	uids := make([]string, recipientAuthorityBenchmarkItems)
	results := make([]pkgcluster.RouteAuthorityResult, recipientAuthorityBenchmarkItems)
	for i := range results {
		hashSlot := uint16(i % recipientAuthorityBenchmarkTargets)
		results[i].Authority = pkgcluster.RouteAuthority{
			HashSlot: hashSlot, SlotID: uint32(hashSlot%10 + 1), LeaderNodeID: 1,
			LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1,
		}
	}
	node := &recipientAuthorityObservabilityBenchmarkNode{results: results}
	disabled := NewRecipientAuthorityResolver(node, nil)
	enabled := NewRecipientAuthorityResolver(node, recipientAuthorityMetricsObserver{metrics: obsmetrics.New(1, "allocation-test")})
	if _, err := enabled.ResolveRecipientAuthorities(context.Background(), uids); err != nil {
		t.Fatalf("warm metrics-enabled resolver: %v", err)
	}
	measure := func(resolver *RecipientAuthorityResolver) float64 {
		return testing.AllocsPerRun(100, func() {
			resolved, err := resolver.ResolveRecipientAuthorities(context.Background(), uids)
			if err != nil || len(resolved) != recipientAuthorityBenchmarkItems {
				t.Fatalf("ResolveRecipientAuthorities() len/error = %d/%v", len(resolved), err)
			}
		})
	}
	disabledAllocs := measure(disabled)
	enabledAllocs := measure(enabled)
	if enabledAllocs > disabledAllocs {
		t.Fatalf("metrics-enabled allocations = %.2f, want no more than disabled %.2f", enabledAllocs, disabledAllocs)
	}
}
