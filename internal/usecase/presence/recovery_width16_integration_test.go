//go:build integration

package presence_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// width16Lookup has a longer fixed watchdog than the small legacy fixtures so
// a real 128-MiB owned-byte boundary never relies on the three-second helper.
func width16Lookup(t *testing.T, recovery *presence.AuthorityRecovery, groups []presence.EndpointLookupGroup) *flightCall {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	observed := &waitObservedContext{Context: ctx, waiting: make(chan struct{})}
	call := &flightCall{done: make(chan []presence.EndpointLookupResult, 1), waiting: observed.waiting, cancel: cancel}
	go func() { call.done <- recovery.EndpointsByTargets(observed, groups) }()
	t.Cleanup(func() {
		cancel()
		if !call.joined {
			select {
			case call.result = <-call.done:
				call.joined = true
			case <-time.After(5 * time.Second):
				t.Error("width16 lookup cleanup did not join")
			}
		}
	})
	return call
}

func width16Await(t *testing.T, call *flightCall) []presence.EndpointLookupResult {
	t.Helper()
	if !call.joined {
		select {
		case call.result = <-call.done:
			call.joined = true
		case <-time.After(5 * time.Second):
			t.Fatal("width16 lookup did not finish within watchdog")
		}
	}
	return call.result
}

func width16Entry(t *testing.T, entered <-chan uint16, want uint16, watchdog time.Duration) {
	t.Helper()
	select {
	case got := <-entered:
		if got != want {
			t.Fatalf("owner entry=%d, want=%d", got, want)
		}
	case <-time.After(watchdog):
		t.Fatalf("owner round %d did not enter", want)
	}
}

func TestAuthorityRecoveryWidth16AdmitsCohortsAndFullKeyBudget(t *testing.T) {
	for _, pageSize := range []int{1, 512} {
		t.Run(fmt.Sprint(pageSize), func(t *testing.T) {
			directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			entered := make(chan uint16, 32)
			releases := make([]chan struct{}, 16)
			for i := range releases {
				releases[i] = make(chan struct{})
			}
			var reads atomic.Int32
			recovery := presence.NewAuthorityRecovery(directory, flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				reads.Add(1)
				if len(groups) != 1 || len(groups[0].UIDs) > 512 {
					t.Error("owner round exceeded the unchanged page contract")
					return flightError(groups, authority.ErrRouteNotReady)
				}
				index := groups[0].Target.HashSlot
				entered <- index
				if index < 16 {
					select {
					case <-releases[index]:
					case <-ctx.Done():
						return flightError(groups, ctx.Err())
					}
				}
				return completeFlightRoutes(groups)
			}))
			calls := make([]*flightCall, 16)
			groups := make([][]presence.EndpointLookupGroup, 16)
			for i := 0; i <= 16; i++ {
				directory.BecomeAuthority(flightTarget(uint16(i)))
			}
			for i := range calls {
				uids := make([]string, pageSize)
				for j := range uids {
					uids[j] = fmt.Sprintf("width16-%02d-%03d", i, j)
				}
				groups[i] = flightGroups(flightTarget(uint16(i)), uids...)
				calls[i] = width16Lookup(t, recovery, groups[i])
				width16Entry(t, entered, uint16(i), 2*time.Second)
			}
			blocked := width16Lookup(t, recovery, flightGroups(flightTarget(16), "not-reserved-a", "not-reserved-b"))
			if pageSize == 512 {
				// Waiting callers now own bounded indices/keys. The full 8192-key
				// ledger fails closed instead of borrowing an unbounded waiter.
				requireFlightError(t, width16Await(t, blocked), authority.ErrRouteNotReady)
			} else {
				awaitFlightGate(t, blocked.waiting)
				blocked.cancel()
				requireFlightError(t, width16Await(t, blocked), context.Canceled)
			}
			if got := reads.Load(); got != 16 {
				t.Fatalf("17th round entered despite full width/key quota: reads=%d", got)
			}
			follower := width16Lookup(t, recovery, groups[0])
			awaitFlightGate(t, follower.waiting)
			follower.cancel()
			requireFlightError(t, width16Await(t, follower), context.Canceled)
			close(releases[0])
			requireColdRoutes(t, width16Await(t, calls[0]), groups[0])
			retryGroups := flightGroups(flightTarget(16), "not-reserved-a", "not-reserved-b")
			retry := width16Lookup(t, recovery, retryGroups)
			width16Entry(t, entered, 16, 2*time.Second)
			requireColdRoutes(t, width16Await(t, retry), retryGroups)
			for i := 1; i < 16; i++ {
				close(releases[i])
			}
			for i, call := range calls {
				requireColdRoutes(t, width16Await(t, call), groups[i])
			}
			if got := reads.Load(); got != 17 {
				t.Fatalf("unexpected owner reads=%d after canceled follower and waiter", got)
			}
		})
	}
}

// This integration fixture retains three maximum-length pages and their owned
// clones. A fourth page exceeds the unchanged 128-MiB UID-plus-metadata ledger
// while cohort, caller and key capacities are still free; this is not an RSS cap.
func TestAuthorityRecoveryWidth16Keeps128MiBByteBudgetWithFreeCohorts(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	entered := make(chan uint16, 16)
	var reads atomic.Int32
	recovery := presence.NewAuthorityRecovery(directory, flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		reads.Add(1)
		entered <- groups[0].Target.HashSlot
		<-ctx.Done()
		return flightError(groups, ctx.Err())
	}))
	held := make([]*flightCall, 3)
	for page := 0; page < 4; page++ {
		target := flightTarget(uint16(100 + page))
		directory.BecomeAuthority(target)
		uids := make([]string, 512)
		for index := range uids {
			uids[index] = fmt.Sprintf("%04x", page*512+index) + strings.Repeat("x", 65531)
		}
		groups := flightGroups(target, uids...)
		call := width16Lookup(t, recovery, groups)
		if page < 3 {
			held[page] = call
			width16Entry(t, entered, uint16(100+page), 10*time.Second)
		} else {
			requireFlightError(t, width16Await(t, call), authority.ErrRouteNotReady)
		}
	}
	if reads.Load() != 3 {
		t.Fatal("metadata-inclusive byte limit admitted a fourth full page")
	}
	for _, call := range held {
		call.cancel()
		requireFlightError(t, width16Await(t, call), context.Canceled)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recovery.Close(ctx); err != nil {
		t.Fatalf("byte-budget fixture failed to join: %v", err)
	}
}
