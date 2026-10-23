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
			awaitFlightGate(t, blocked.waiting)
			if got := reads.Load(); got != 16 {
				t.Fatalf("17th round entered despite full width/key quota: reads=%d", got)
			}
			blocked.cancel()
			requireFlightError(t, width16Await(t, blocked), context.Canceled)
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

// This integration fixture allocates over 128 MiB for caller input alone and
// another 128 MiB for owned clones. The operator must admit memory headroom
// before execution; the owned-byte ledger is not a process RSS guarantee.
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
	pages := make([][]presence.EndpointLookupGroup, 4)
	for page := range pages {
		target := flightTarget(uint16(100 + page))
		directory.BecomeAuthority(target)
		uids := make([]string, 512)
		for index := range uids {
			uids[index] = fmt.Sprintf("%04x", page*512+index) + strings.Repeat("x", 65531)
		}
		pages[page] = flightGroups(target, uids...)
	}
	// 4*512*65535 = 128 MiB - 2048, independently of the 16-cohort limit.
	held := make([]*flightCall, 4)
	for i := range held {
		held[i] = width16Lookup(t, recovery, pages[i])
		width16Entry(t, entered, uint16(100+i), 10*time.Second)
	}
	fillTarget := flightTarget(104)
	directory.BecomeAuthority(fillTarget)
	fill := width16Lookup(t, recovery, flightGroups(fillTarget, strings.Repeat("y", 2048)))
	width16Entry(t, entered, 104, 10*time.Second)
	byteTarget := flightTarget(105)
	directory.BecomeAuthority(byteTarget)
	oneByte := width16Lookup(t, recovery, flightGroups(byteTarget, "z"))
	awaitFlightGate(t, oneByte.waiting)
	if got := reads.Load(); got != 5 {
		t.Fatalf("byte quota admitted one extra byte with free cohorts; possible 512-MiB derived regression: reads=%d", got)
	}
	oneByte.cancel()
	requireFlightError(t, width16Await(t, oneByte), context.Canceled)
	held[0].cancel()
	requireFlightError(t, width16Await(t, held[0]), context.Canceled)
	afterRelease := width16Lookup(t, recovery, flightGroups(byteTarget, "z"))
	width16Entry(t, entered, 105, 10*time.Second)
	afterRelease.cancel()
	requireFlightError(t, width16Await(t, afterRelease), context.Canceled)
	fill.cancel()
	requireFlightError(t, width16Await(t, fill), context.Canceled)
	for i := 1; i < len(held); i++ {
		held[i].cancel()
		requireFlightError(t, width16Await(t, held[i]), context.Canceled)
	}
}
