package cluster

import (
	"reflect"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

func TestObservationCacheUpsertNodeReportKeepsNewestObservation(t *testing.T) {
	cache := newObservationCache()
	older := slotcontroller.AgentReport{
		NodeID:               1,
		Addr:                 "old",
		ObservedAt:           time.Unix(10, 0),
		CapacityWeight:       1,
		HashSlotTableVersion: 2,
	}
	newer := slotcontroller.AgentReport{
		NodeID:               1,
		Addr:                 "new",
		ObservedAt:           time.Unix(20, 0),
		CapacityWeight:       3,
		HashSlotTableVersion: 5,
	}

	cache.applyNodeReport(older)
	cache.applyNodeReport(newer)
	cache.applyNodeReport(older)

	snapshot := cache.snapshot()
	if len(snapshot.Nodes) != 1 {
		t.Fatalf("snapshot.Nodes len = %d, want 1", len(snapshot.Nodes))
	}
	got := snapshot.Nodes[0]
	if got.NodeID != newer.NodeID {
		t.Fatalf("snapshot.Nodes[0].NodeID = %d, want %d", got.NodeID, newer.NodeID)
	}
	if got.Addr != newer.Addr {
		t.Fatalf("snapshot.Nodes[0].Addr = %q, want %q", got.Addr, newer.Addr)
	}
	if !got.ObservedAt.Equal(newer.ObservedAt) {
		t.Fatalf("snapshot.Nodes[0].ObservedAt = %v, want %v", got.ObservedAt, newer.ObservedAt)
	}
	if got.CapacityWeight != newer.CapacityWeight {
		t.Fatalf("snapshot.Nodes[0].CapacityWeight = %d, want %d", got.CapacityWeight, newer.CapacityWeight)
	}
	if got.HashSlotTableVersion != newer.HashSlotTableVersion {
		t.Fatalf("snapshot.Nodes[0].HashSlotTableVersion = %d, want %d", got.HashSlotTableVersion, newer.HashSlotTableVersion)
	}
}

func TestObservationCacheApplyRuntimeReportFullSyncReplacesNodeViews(t *testing.T) {
	cache := newObservationCache()
	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(10, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{
			{
				SlotID:              1,
				CurrentPeers:        []uint64{1, 2, 3},
				LeaderID:            1,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 1,
				LastReportAt:        time.Unix(10, 0),
			},
			{
				SlotID:              2,
				CurrentPeers:        []uint64{1, 2, 3},
				LeaderID:            2,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 1,
				LastReportAt:        time.Unix(10, 0),
			},
		},
	})
	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(10, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              9,
			CurrentPeers:        []uint64{2, 3, 4},
			LeaderID:            2,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 1,
			LastReportAt:        time.Unix(10, 0),
		}},
	})

	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(20, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              2,
			CurrentPeers:        []uint64{1, 3},
			LeaderID:            3,
			HealthyVoters:       2,
			HasQuorum:           true,
			ObservedConfigEpoch: 2,
			LastReportAt:        time.Unix(20, 0),
		}},
	})

	snapshot := cache.snapshot()
	if got, want := len(snapshot.RuntimeViews), 2; got != want {
		t.Fatalf("snapshot.RuntimeViews len = %d, want %d", got, want)
	}
	if got, want := snapshot.RuntimeViews[0].SlotID, uint32(2); got != want {
		t.Fatalf("snapshot.RuntimeViews[0].SlotID = %d, want %d", got, want)
	}
	if got, want := snapshot.RuntimeViews[1].SlotID, uint32(9); got != want {
		t.Fatalf("snapshot.RuntimeViews[1].SlotID = %d, want %d", got, want)
	}
	if got, want := snapshot.RuntimeViews[0].CurrentPeers, []uint64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot.RuntimeViews[0].CurrentPeers = %v, want %v", got, want)
	}
}

func TestObservationCacheApplyRuntimeReportIncrementalUpsertsAndDeletes(t *testing.T) {
	cache := newObservationCache()
	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(30, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              9,
			CurrentPeers:        []uint64{2, 3, 4},
			LeaderID:            3,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 1,
			LastReportAt:        time.Unix(30, 0),
		}},
	})
	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:      2,
		ObservedAt:  time.Unix(40, 0),
		ClosedSlots: []uint32{9},
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              7,
			CurrentPeers:        []uint64{2, 4},
			LeaderID:            2,
			HealthyVoters:       2,
			HasQuorum:           true,
			ObservedConfigEpoch: 2,
			LastReportAt:        time.Unix(40, 0),
		}},
	})

	snapshot := cache.snapshot()
	if got, want := len(snapshot.RuntimeViews), 1; got != want {
		t.Fatalf("snapshot.RuntimeViews len = %d, want %d", got, want)
	}
	if got, want := snapshot.RuntimeViews[0].SlotID, uint32(7); got != want {
		t.Fatalf("snapshot.RuntimeViews[0].SlotID = %d, want %d", got, want)
	}
	if got, want := snapshot.RuntimeViews[0].CurrentPeers, []uint64{2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot.RuntimeViews[0].CurrentPeers = %v, want %v", got, want)
	}
}

func TestObservationCacheEvictsStaleRuntimeViews(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newObservationCache()
	cache.now = func() time.Time { return now }
	cache.runtimeViewTTL = 30 * time.Second

	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(100, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:       1,
			CurrentPeers: []uint64{1},
			LeaderID:     1,
			HasQuorum:    true,
			LastReportAt: time.Unix(100, 0),
		}},
	})
	now = time.Unix(120, 0)
	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(120, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:       2,
			CurrentPeers: []uint64{2},
			LeaderID:     2,
			HasQuorum:    true,
			LastReportAt: time.Unix(120, 0),
		}},
	})

	now = time.Unix(131, 0)
	snapshot := cache.snapshot()
	if got, want := len(snapshot.RuntimeViews), 1; got != want {
		t.Fatalf("snapshot.RuntimeViews len = %d, want %d", got, want)
	}
	if got, want := snapshot.RuntimeViews[0].SlotID, uint32(2); got != want {
		t.Fatalf("snapshot.RuntimeViews[0].SlotID = %d, want %d", got, want)
	}
}

func TestObservationCacheSnapshotReturnsStableCopies(t *testing.T) {
	cache := newObservationCache()
	cache.applyNodeReport(slotcontroller.AgentReport{
		NodeID:               2,
		Addr:                 "node-2",
		ObservedAt:           time.Unix(30, 0),
		CapacityWeight:       4,
		HashSlotTableVersion: 11,
	})
	cache.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(40, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              9,
			CurrentPeers:        []uint64{2, 3, 4},
			LeaderID:            3,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 12,
			LastReportAt:        time.Unix(40, 0),
		}},
	})

	snapshot := cache.snapshot()
	snapshot.Nodes[0].Addr = "mutated"
	snapshot.RuntimeViews[0].CurrentPeers[0] = 99

	next := cache.snapshot()
	if got, want := next.Nodes[0].Addr, "node-2"; got != want {
		t.Fatalf("snapshot node addr = %q, want %q", got, want)
	}
	if got, want := next.RuntimeViews[0].CurrentPeers, []uint64{2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot runtime peers = %v, want %v", got, want)
	}
}
