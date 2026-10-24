package presence_test

import (
	"errors"
	"math"
	"sync"
	"testing"

	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestRecoveryResultBudgetReservesAllDimensionsBeforeOwnership(t *testing.T) {
	b := presence.NewRecoveryResultBudget(3, 4096)
	a, err := b.TryReserve(2, 100)
	if err != nil {
		t.Fatal(err)
	}
	before := b.Stats()
	if before.Rows != 2 || before.Bytes < 100 {
		t.Fatalf("unaccounted ownership: %+v", before)
	}
	for _, request := range [][2]uint64{{2, 1}, {1, 4096}, {math.MaxUint64, 1}, {1, math.MaxUint64}} {
		lease, err := b.TryReserve(request[0], request[1])
		if !errors.Is(err, presence.ErrRecoveryResultBudget) || lease != nil {
			t.Fatalf("accepted over-limit ownership: %v %v", lease, err)
		}
		got := b.Stats()
		if got.Rows != before.Rows || got.Bytes != before.Bytes {
			t.Fatalf("partial reservation: %+v", got)
		}
	}
	if err := a.TryGrow(2, 1); !errors.Is(err, presence.ErrRecoveryResultBudget) {
		t.Fatalf("grow escaped row limit: %v", err)
	}
	if err := a.TryGrow(1, 200); err != nil {
		t.Fatal(err)
	}
	if a.Reduce(4, 0) || a.Reduce(0, 301) {
		t.Fatal("underflow accepted")
	}
	if !a.Reduce(1, 200) {
		t.Fatal("dropped old backing not returned")
	}
	if got := b.Stats(); got.Rows != before.Rows || got.Bytes != before.Bytes {
		t.Fatalf("wrong remaining ownership: %+v", got)
	}
	a.Release()
	a.Release()
	if got := b.Stats(); got.Rows != 0 || got.Bytes != 0 || got.HighWaterRows != 3 || got.Rejected != 5 {
		t.Fatalf("bad terminal ledger: %+v", got)
	}
	if err := a.TryGrow(1, 1); !errors.Is(err, presence.ErrRecoveryResultBudget) {
		t.Fatal("released lease resurrected")
	}
	if a.Reduce(0, 0) {
		t.Fatal("released lease reduced")
	}
}

func TestRecoveryResultBudgetConcurrentOwnersCannotOversubscribe(t *testing.T) {
	b := presence.NewRecoveryResultBudget(8, 1<<20)
	var wg sync.WaitGroup
	held := make(chan *presence.RecoveryResultLease, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l, err := b.TryReserve(1, 100); err == nil {
				held <- l
			}
		}()
	}
	wg.Wait()
	close(held)
	if got := b.Stats(); got.Rows != 8 {
		t.Fatalf("unexpected admitted ownership: %+v", got)
	}
	for l := range held {
		wg.Add(2)
		go func() { defer wg.Done(); l.Release() }()
		go func() { defer wg.Done(); l.Release() }()
	}
	wg.Wait()
	if got := b.Stats(); got.Rows != 0 || got.Bytes != 0 {
		t.Fatalf("leaked ownership: %+v", got)
	}
}

func TestRecoveryOwnedResultsDropReferencesBeforeReturningCredits(t *testing.T) {
	b := presence.NewRecoveryResultBudget(2, 4096)
	l, err := b.TryReserve(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	groups := []presence.EndpointLookupResult{{Routes: []presence.Route{{UID: "held"}}}}
	results := [][]presence.EndpointLookupResult{groups}
	owned := presence.OwnedRecoveryResults{Results: results, Lease: l}
	owned.Release()
	if owned.Results != nil || owned.Lease != nil || results[0] != nil {
		t.Fatal("results retained after credits returned")
	}
	l, err = b.TryReserve(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	pageResults := [][]presence.OwnerRouteResult{{{}}}
	page := presence.OwnedOwnerRouteCallPage{Page: presence.OwnerRouteCallPage{Results: pageResults}, Lease: l}
	page.Release()
	if page.Page.Results != nil || page.Lease != nil || pageResults[0] != nil {
		t.Fatal("page retained after credits returned")
	}
	if got := b.Stats(); got.Rows != 0 || got.Bytes != 0 {
		t.Fatalf("credits retained: %+v", got)
	}
}
