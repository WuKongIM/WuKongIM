package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	infracluster "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

const (
	recipientObservabilityBenchmarkItems   = 512
	recipientObservabilityBenchmarkTargets = 221
)

type recipientObservabilityPresenceNode struct {
	routes          map[string]pkgcluster.Route
	byHashSlot      map[uint16]pkgcluster.Route
	authorityEvents chan pkgcluster.RouteAuthorityEvent
}

func (*recipientObservabilityPresenceNode) NodeID() uint64 { return 1 }

func (n *recipientObservabilityPresenceNode) RouteKey(uid string) (pkgcluster.Route, error) {
	route, ok := n.routes[uid]
	if !ok {
		return pkgcluster.Route{}, fmt.Errorf("benchmark route for uid %q not found", uid)
	}
	return route, nil
}

func (n *recipientObservabilityPresenceNode) RouteKeysPartial(uids []string) ([]pkgcluster.RouteKeyResult, error) {
	results := make([]pkgcluster.RouteKeyResult, len(uids))
	for i, uid := range uids {
		results[i].Route, results[i].Err = n.RouteKey(uid)
	}
	return results, nil
}

func (n *recipientObservabilityPresenceNode) RouteHashSlot(hashSlot uint16) (pkgcluster.Route, error) {
	route, ok := n.byHashSlot[hashSlot]
	if !ok {
		return pkgcluster.Route{}, fmt.Errorf("benchmark route for hash slot %d not found", hashSlot)
	}
	return route, nil
}

func (*recipientObservabilityPresenceNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, errors.New("unexpected remote presence RPC")
}

func (*recipientObservabilityPresenceNode) RegisterRPC(uint8, pkgcluster.NodeRPCHandler) {}

func (n *recipientObservabilityPresenceNode) WatchRouteAuthorities() <-chan pkgcluster.RouteAuthorityEvent {
	return n.authorityEvents
}

// BenchmarkPresenceEndpointLookupObservabilityCloudMedium compares the real
// exact-target local-bulk directory path with its optional aggregate metrics.
func BenchmarkPresenceEndpointLookupObservabilityCloudMedium(b *testing.B) {
	const items = recipientObservabilityBenchmarkItems
	const targets = recipientObservabilityBenchmarkTargets
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: 1, ShardCount: 32})
	node := &recipientObservabilityPresenceNode{
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
