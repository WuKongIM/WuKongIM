package presence_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func ownedRecoveryFixture(t *testing.T, counts ...int) (*presence.OwnerRecovery, []presence.RecoveryCall) {
	t.Helper()
	registry := online.NewRegistry(online.RegistryOptions{})
	var session uint64
	calls := make([]presence.RecoveryCall, len(counts))
	for i, count := range counts {
		uid := fmt.Sprintf("member-%d", i)
		calls[i] = presence.RecoveryCall{Groups: []presence.EndpointLookupGroup{{Target: presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}, UIDs: []string{uid}}}}
		for range count {
			session++
			row := online.OwnerRoute{UID: uid, DeviceID: "device", Listener: "tcp", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 11, SessionID: session, OwnerSeq: 1}
			if err := registry.RegisterPending(online.LocalSession{Route: row}); err != nil {
				t.Fatal(err)
			}
			if err := registry.MarkActive(session); err != nil {
				t.Fatal(err)
			}
		}
	}
	return presence.NewOwnerRecovery(registry, 1, 11, func(presence.RouteTarget) error { return nil }), calls
}

func TestOwnedOwnerRecoveryPageKeepsCompleteCallsAndReturnsAllCredits(t *testing.T) {
	owner, calls := ownedRecoveryFixture(t, 2500, 2500)
	reader, ok := any(owner).(presence.OwnerRouteCallPageReaderBounded)
	if !ok {
		t.Fatal("owner cannot return complete pages under controlled result ownership")
	}
	budget := presence.NewRecoveryResultBudget(20_000, 8<<20)
	for start := range calls {
		owned, err := reader.ReadOwnerRouteCallsBounded(context.Background(), calls, start, budget)
		if err != nil {
			t.Fatal(err)
		}
		if owned.Page.Start != start || owned.Page.Next != start+1 || len(owned.Page.Results) != 1 || len(owned.Page.Results[0]) != 1 || len(owned.Page.Results[0][0].Snapshot.Routes) != 2500 {
			t.Fatalf("complete-call boundary changed: start=%d next=%d results=%d", owned.Page.Start, owned.Page.Next, len(owned.Page.Results))
		}
		if got := budget.Stats(); got.Rows != 2500 || got.Bytes == 0 || got.HighWaterRows > 20_000 {
			t.Fatalf("transient scratch/lookahead escaped or credits were returned early: %+v", got)
		}
		owned.Release()
		owned.Release()
		if len(owned.Page.Results) != 0 || budget.Stats().Rows != 0 || budget.Stats().Bytes != 0 {
			t.Fatal("released owner proof retained references or credits")
		}
	}
}

func TestOwnedOwnerRecoveryPageFailsClosedWhenScratchAndOutputCannotCoexist(t *testing.T) {
	owner, calls := ownedRecoveryFixture(t, 1)
	reader, ok := any(owner).(presence.OwnerRouteCallPageReaderBounded)
	if !ok {
		t.Fatal("owner lacks controlled result ownership")
	}
	budget := presence.NewRecoveryResultBudget(16, 1<<20)
	owned, err := reader.ReadOwnerRouteCallsBounded(context.Background(), calls, 0, budget)
	if !errors.Is(err, presence.ErrRecoveryResultBudget) || len(owned.Page.Results) != 0 {
		t.Fatalf("saturated owned page became partial proof: err=%v", err)
	}
	if got := budget.Stats(); got.Rows != 0 || got.Bytes != 0 || got.HighWaterRows > 16 || got.Rejected == 0 {
		t.Fatalf("failed local read leaked reservation: %+v", got)
	}
}
