package cluster

import (
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func slotMappingTestNode(t testing.TB) *Node {
	t.Helper()
	snapshot := routeAuthoritySnapshot(1)
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	n := &Node{router: routing.NewRouter()}
	if err := n.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 2}})
	n.started.Store(true)
	return n
}

func TestSlotMappingPreservesForegroundAndLeaderChecks(t *testing.T) {
	n := slotMappingTestNode(t)
	assertEquivalent := func() {
		t.Helper()
		for i := 0; i < 256; i++ {
			key := fmt.Sprintf("channel-%d", i)
			route, err := n.RouteKey(key)
			want := multiraft.SlotID(0)
			if err == nil {
				want = multiraft.SlotID(route.SlotID)
			}
			if got := n.SlotForKey(key); got != want {
				t.Fatalf("key=%s slot=%d want=%d routeErr=%v", key, got, want, err)
			}
		}
	}
	assertEquivalent()
	// Mapping changes are observed immediately from the same foreground router,
	// including the window before diagnostic authority epochs are published.
	snapshot := routeAuthoritySnapshot(2)
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	snapshot.HashSlots.Ranges[0].SlotID = 2
	snapshot.Slots[0].SlotID = 2
	if err := n.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 2, Leader: 1, LeaderTerm: 3}})
	assertEquivalent()
	if n.SlotForKey("channel-key") != 2 {
		t.Fatal("Slot mapping did not observe replacement table")
	}
	n.maintenance.Store(true)
	assertEquivalent()
	n.maintenance.Store(false)
	n.stopping.Store(true)
	assertEquivalent()
	n.stopping.Store(false)
	n.started.Store(false)
	assertEquivalent()
	n.started.Store(true)
	n.router = routing.NewRouter()
	if err := n.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	assertEquivalent()
	if n.SlotForKey("channel-key") != 0 {
		t.Fatal("Slot mapping accepted a table without an observed Leader")
	}
	n.router = routing.NewRouter()
	assertEquivalent()
	n.router = nil
	assertEquivalent()
	n = nil
	assertEquivalent()
}

func TestSlotMappingDoesNotAllocatePeerCopies(t *testing.T) {
	n := slotMappingTestNode(t)
	var got multiraft.SlotID
	allocs := testing.AllocsPerRun(100, func() { got = n.SlotForKey("channel-key") })
	if got != 1 || allocs != 0 {
		t.Fatalf("slot=%d allocations=%v, want slot=1 allocations=0", got, allocs)
	}
}

// BenchmarkSlotMapping measures the scalar lookup used repeatedly by bounded
// Slot metadata batches, without starting cluster processes or retaining routes.
func BenchmarkSlotMapping(b *testing.B) {
	n := slotMappingTestNode(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n.SlotForKey("cohort-0-channel-099") != 1 {
			b.Fatal("unexpected Slot")
		}
	}
}
