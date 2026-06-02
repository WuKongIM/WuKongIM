package cluster

import (
	"context"
	"fmt"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// DeliveryRouteNode exposes clusterv2 hash-slot routing for delivery fanout partitioning.
type DeliveryRouteNode interface {
	Snapshot() clusterv2.Snapshot
	RouteHashSlot(uint16) (clusterv2.Route, error)
}

// DeliveryPartitioner builds delivery fanout partitions from the cluster UID route table.
type DeliveryPartitioner struct {
	// node provides the current cluster route table.
	node DeliveryRouteNode
}

// NewDeliveryPartitioner creates a route-table-backed delivery partitioner.
func NewDeliveryPartitioner(node DeliveryRouteNode) *DeliveryPartitioner {
	return &DeliveryPartitioner{node: node}
}

// Partitions returns contiguous hash-slot ranges grouped by authority leader node.
func (p *DeliveryPartitioner) Partitions(ctx context.Context) ([]runtimedelivery.Partition, error) {
	if p == nil || p.node == nil {
		return nil, runtimedelivery.ErrRouteNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := p.node.Snapshot()
	if !snapshot.RoutesReady || snapshot.HashSlotCount == 0 {
		return nil, fmt.Errorf("%w: routes=%t hashSlotCount=%d", runtimedelivery.ErrRouteNotReady, snapshot.RoutesReady, snapshot.HashSlotCount)
	}

	partitions := make([]runtimedelivery.Partition, 0, snapshot.HashSlotCount)
	var current runtimedelivery.Partition
	flush := func() {
		if current.ID != 0 {
			partitions = append(partitions, current)
		}
	}
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		route, err := p.node.RouteHashSlot(hashSlot)
		if err != nil {
			return nil, mapDeliveryRouteError(err)
		}
		if route.Leader == 0 {
			return nil, fmt.Errorf("%w: hash slot %d leader is unknown", runtimedelivery.ErrRouteNotReady, hashSlot)
		}
		if current.ID == 0 {
			current = runtimedelivery.Partition{
				ID:            1,
				LeaderNodeID:  route.Leader,
				HashSlotStart: hashSlot,
				HashSlotEnd:   hashSlot,
			}
			continue
		}
		if current.LeaderNodeID == route.Leader && current.HashSlotEnd+1 == hashSlot {
			current.HashSlotEnd = hashSlot
			continue
		}
		flush()
		current = runtimedelivery.Partition{
			ID:            uint32(len(partitions) + 1),
			LeaderNodeID:  route.Leader,
			HashSlotStart: hashSlot,
			HashSlotEnd:   hashSlot,
		}
	}
	flush()
	return partitions, nil
}

func mapDeliveryRouteError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", runtimedelivery.ErrRouteNotReady, err)
}
