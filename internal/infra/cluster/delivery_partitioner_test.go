package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestDeliveryPartitionerGroupsContiguousHashSlotsByLeader(t *testing.T) {
	node := &fakeDeliveryRouteNode{
		snapshot: cluster.Snapshot{StateRevision: 7, RoutesReady: true, HashSlotCount: 5},
		routes: map[uint16]cluster.Route{
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

func TestDeliveryPartitionerCachesPartitionsForSameRevision(t *testing.T) {
	node := &fakeDeliveryRouteNode{
		snapshot: cluster.Snapshot{StateRevision: 8, RoutesReady: true, HashSlotCount: 2},
		routes: map[uint16]cluster.Route{
			0: {HashSlot: 0, Leader: 1},
			1: {HashSlot: 1, Leader: 2},
		},
	}
	partitioner := NewDeliveryPartitioner(node)

	first, err := partitioner.Partitions(context.Background())
	if err != nil {
		t.Fatalf("first Partitions() error = %v", err)
	}
	if node.routeCalls != 2 {
		t.Fatalf("route calls after first read = %d, want 2", node.routeCalls)
	}

	node.routes = nil
	second, err := partitioner.Partitions(context.Background())
	if err != nil {
		t.Fatalf("second Partitions() error = %v", err)
	}
	if node.routeCalls != 2 {
		t.Fatalf("route calls after cache hit = %d, want 2", node.routeCalls)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("cached partitions = %#v, want %#v", second, first)
	}
	second[0].LeaderNodeID = 99
	third, err := partitioner.Partitions(context.Background())
	if err != nil {
		t.Fatalf("third Partitions() error = %v", err)
	}
	if third[0].LeaderNodeID == 99 {
		t.Fatalf("cached partitions share memory with caller: %#v", third)
	}
}

func TestDeliveryPartitionerUsesLastValidPartitionsWhenSnapshotTemporarilyUnready(t *testing.T) {
	node := &fakeDeliveryRouteNode{
		snapshot: cluster.Snapshot{StateRevision: 9, RoutesReady: true, HashSlotCount: 2},
		routes: map[uint16]cluster.Route{
			0: {HashSlot: 0, Leader: 1},
			1: {HashSlot: 1, Leader: 1},
		},
	}
	partitioner := NewDeliveryPartitioner(node)
	want, err := partitioner.Partitions(context.Background())
	if err != nil {
		t.Fatalf("first Partitions() error = %v", err)
	}

	node.snapshot = cluster.Snapshot{StateRevision: 10, RoutesReady: false, HashSlotCount: 0}
	node.err = cluster.ErrRouteNotReady
	got, err := partitioner.Partitions(context.Background())
	if err != nil {
		t.Fatalf("Partitions() with cached fallback error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback partitions = %#v, want %#v", got, want)
	}
}

func TestDeliveryPartitionerReturnsRouteNotReadyForUnreadySnapshot(t *testing.T) {
	partitioner := NewDeliveryPartitioner(&fakeDeliveryRouteNode{
		snapshot: cluster.Snapshot{RoutesReady: false, HashSlotCount: 0},
	})

	_, err := partitioner.Partitions(context.Background())
	if !errors.Is(err, runtimedelivery.ErrRouteNotReady) {
		t.Fatalf("Partitions() error = %v, want ErrRouteNotReady", err)
	}
}

type fakeDeliveryRouteNode struct {
	snapshot   cluster.Snapshot
	routes     map[uint16]cluster.Route
	err        error
	routeCalls int
}

func (f *fakeDeliveryRouteNode) Snapshot() cluster.Snapshot {
	return f.snapshot
}

func (f *fakeDeliveryRouteNode) RouteHashSlot(hashSlot uint16) (cluster.Route, error) {
	f.routeCalls++
	if f.err != nil {
		return cluster.Route{}, f.err
	}
	route, ok := f.routes[hashSlot]
	if !ok {
		return cluster.Route{}, cluster.ErrRouteNotReady
	}
	return route, nil
}
