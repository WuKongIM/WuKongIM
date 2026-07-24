package app

import (
	"context"
	"strconv"
	"testing"

	infracluster "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

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
		client := infracluster.NewPresenceAuthorityClient(node, infracluster.NewPresenceDirectoryAuthority(directory))
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
