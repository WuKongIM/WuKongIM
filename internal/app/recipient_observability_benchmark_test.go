package app

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	infracluster "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// recipientAuthorityObservabilityBenchmarkNode returns one aligned immutable
// authority snapshot. It keeps the benchmark focused on the production
// resolver conversion, distinct-target accounting, and optional metrics cost.
type recipientAuthorityObservabilityBenchmarkNode struct {
	results []pkgcluster.RouteAuthorityResult
}

func (n *recipientAuthorityObservabilityBenchmarkNode) RouteKey(key string) (pkgcluster.Route, error) {
	index, err := strconv.Atoi(key)
	if err != nil || index < 0 || index >= len(n.results) {
		return pkgcluster.Route{}, fmt.Errorf("benchmark route key %q is invalid", key)
	}
	authority := n.results[index].Authority
	return pkgcluster.Route{
		HashSlot: authority.HashSlot, SlotID: authority.SlotID, Leader: authority.LeaderNodeID,
		LeaderTerm: authority.LeaderTerm, ConfigEpoch: authority.ConfigEpoch,
		Revision: authority.RouteRevision, AuthorityEpoch: authority.AuthorityEpoch,
	}, n.results[index].Err
}

func (n *recipientAuthorityObservabilityBenchmarkNode) RouteAuthoritiesPartial([]string) ([]pkgcluster.RouteAuthorityResult, error) {
	return n.results, nil
}

// BenchmarkRecipientAuthorityResolveObservabilityCloudMedium compares the real
// 512-item authority resolver with metrics disabled and enabled. It complements
// the cross-commit production benchmark, which intentionally remains
// byte-identical and starts from already grouped exact targets.
func BenchmarkRecipientAuthorityResolveObservabilityCloudMedium(b *testing.B) {
	const items = productionRecipientBenchmarkRecipients
	const targets = productionRecipientBenchmarkTargets
	uids := make([]string, items)
	results := make([]pkgcluster.RouteAuthorityResult, items)
	for i := range uids {
		uids[i] = strconv.Itoa(i)
		hashSlot := uint16(i % targets)
		results[i].Authority = pkgcluster.RouteAuthority{
			HashSlot: hashSlot, SlotID: uint32(hashSlot%10 + 1), LeaderNodeID: 1,
			LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1,
		}
	}
	node := &recipientAuthorityObservabilityBenchmarkNode{results: results}
	for _, enabled := range []bool{false, true} {
		name := "metrics-disabled"
		resolver := channelAppendRecipientResolver{node: node}
		if enabled {
			name = "metrics-enabled"
			resolver.observer = deliveryMetricsObserver{metrics: obsmetrics.New(1, "benchmark")}
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(items, "items/op")
			b.ReportMetric(targets, "targets/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resolved, err := resolver.ResolveRecipientAuthorities(context.Background(), uids)
				if err != nil || len(resolved) != items {
					b.Fatalf("ResolveRecipientAuthorities() len/error = %d/%v", len(resolved), err)
				}
			}
		})
	}
}

// BenchmarkPresenceEndpointLookupObservabilityCloudMedium compares the real
// exact-target local-bulk directory path with its optional aggregate metrics.
func BenchmarkPresenceEndpointLookupObservabilityCloudMedium(b *testing.B) {
	const items = productionRecipientBenchmarkRecipients
	const targets = productionRecipientBenchmarkTargets
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: 1, ShardCount: 32})
	node := &productionRecipientBenchmarkPresenceNode{
		routes: make(map[string]pkgcluster.Route), byHashSlot: make(map[uint16]pkgcluster.Route),
		authorityEvents: make(chan pkgcluster.RouteAuthorityEvent),
	}
	groups := make([]presenceusecase.EndpointLookupGroup, targets)
	for i := range groups {
		target := authoritypresence.RouteTarget{
			HashSlot: uint16(i), SlotID: uint32(i%10 + 1), LeaderNodeID: 1,
			LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1,
		}
		directory.BecomeAuthority(target)
		groups[i].Target = target
	}
	for i := 0; i < items; i++ {
		groups[i%targets].UIDs = append(groups[i%targets].UIDs, strconv.Itoa(i))
	}

	for _, enabled := range []bool{false, true} {
		name := "metrics-disabled"
		client := infracluster.NewPresenceAuthorityClient(node, presenceDirectoryAuthority{directory: directory})
		if enabled {
			name = "metrics-enabled"
			client.SetEndpointLookupObserver(presenceMetricsObserver{metrics: obsmetrics.New(1, "benchmark")})
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(items, "items/op")
			b.ReportMetric(targets, "target-groups/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resolved := client.EndpointsByTargets(context.Background(), groups)
				if len(resolved) != targets {
					b.Fatalf("EndpointsByTargets() len = %d, want %d", len(resolved), targets)
				}
				for targetIndex, result := range resolved {
					if result.Err != nil {
						b.Fatalf("EndpointsByTargets() target %d error = %v", targetIndex, result.Err)
					}
				}
			}
		})
	}
}

var _ recipientAuthorityRouteNode = (*recipientAuthorityObservabilityBenchmarkNode)(nil)
var _ recipientAuthorityPartialBatchNode = (*recipientAuthorityObservabilityBenchmarkNode)(nil)
var _ channelappend.RecipientAuthorityResolver = channelAppendRecipientResolver{}
