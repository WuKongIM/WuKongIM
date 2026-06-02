package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestDeliveryPartitionerGroupsContiguousHashSlotsByLeader(t *testing.T) {
	node := &fakeDeliveryRouteNode{
		snapshot: clusterv2.Snapshot{RoutesReady: true, HashSlotCount: 5},
		routes: map[uint16]clusterv2.Route{
			0: {HashSlot: 0, Leader: 1},
			1: {HashSlot: 1, Leader: 1},
			2: {HashSlot: 2, Leader: 2},
			3: {HashSlot: 3, Leader: 2},
			4: {HashSlot: 4, Leader: 1},
		},
	}
	partitioner := NewDeliveryPartitioner(node)

	partitions, err := partitioner.Partitions(context.Background())
	if err != nil {
		t.Fatalf("Partitions() error = %v", err)
	}

	want := []runtimedelivery.Partition{
		{ID: 1, LeaderNodeID: 1, HashSlotStart: 0, HashSlotEnd: 1},
		{ID: 2, LeaderNodeID: 2, HashSlotStart: 2, HashSlotEnd: 3},
		{ID: 3, LeaderNodeID: 1, HashSlotStart: 4, HashSlotEnd: 4},
	}
	if !reflect.DeepEqual(partitions, want) {
		t.Fatalf("partitions = %#v, want %#v", partitions, want)
	}
}

func TestDeliveryPartitionerReturnsRouteNotReadyForUnreadySnapshot(t *testing.T) {
	partitioner := NewDeliveryPartitioner(&fakeDeliveryRouteNode{
		snapshot: clusterv2.Snapshot{RoutesReady: false, HashSlotCount: 0},
	})

	_, err := partitioner.Partitions(context.Background())
	if !errors.Is(err, runtimedelivery.ErrRouteNotReady) {
		t.Fatalf("Partitions() error = %v, want ErrRouteNotReady", err)
	}
}

type fakeDeliveryRouteNode struct {
	snapshot clusterv2.Snapshot
	routes   map[uint16]clusterv2.Route
	err      error
}

func (f *fakeDeliveryRouteNode) Snapshot() clusterv2.Snapshot {
	return f.snapshot
}

func (f *fakeDeliveryRouteNode) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	if f.err != nil {
		return clusterv2.Route{}, f.err
	}
	route, ok := f.routes[hashSlot]
	if !ok {
		return clusterv2.Route{}, clusterv2.ErrRouteNotReady
	}
	return route, nil
}
