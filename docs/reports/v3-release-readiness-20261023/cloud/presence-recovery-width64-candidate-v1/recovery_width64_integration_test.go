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

// width64Lookup has a longer fixed watchdog than the small legacy fixtures so
// a real 128-MiB owned-byte boundary never relies on the three-second helper.
func width64Lookup(t *testing.T, recovery *presence.AuthorityRecovery, groups []presence.EndpointLookupGroup) *flightCall {
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
				t.Error("width64 lookup cleanup did not join")
			}
		}
	})
	return call
}

func width64Await(t *testing.T, call *flightCall) []presence.EndpointLookupResult {
	t.Helper()
	if !call.joined {
		select {
		case call.result = <-call.done:
			call.joined = true
		case <-time.After(5 * time.Second):
			t.Fatal("width64 lookup did not finish within watchdog")
		}
	}
	return call.result
}

func width64Entry(t *testing.T, entered <-chan uint16, want uint16, watchdog time.Duration) {
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

func TestAuthorityRecoveryWidth64AdmitsCohortsAndFullKeyBudget(t *testing.T) {
	for _, budget := range []struct{ pageSize, cohorts int }{{1, 64}, {512, 16}} {
		t.Run(fmt.Sprint(budget.pageSize), func(t *testing.T) {
			pageSize, cohorts := budget.pageSize, budget.cohorts
			directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			entered := make(chan uint16, 65)
			releases := make([]chan struct{}, cohorts)
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
				if int(index) < cohorts {
					select {
					case <-releases[index]:
					case <-ctx.Done():
						return flightError(groups, ctx.Err())
					}
				}
				return completeFlightRoutes(groups)
			}))
			calls := make([]*flightCall, cohorts)
			groups := make([][]presence.EndpointLookupGroup, cohorts)
			for i := 0; i <= cohorts; i++ {
				directory.BecomeAuthority(flightTarget(uint16(i)))
			}
			for i := range calls {
				uids := make([]string, pageSize)
				for j := range uids {
					uids[j] = fmt.Sprintf("width64-%02d-%03d", i, j)
				}
				groups[i] = flightGroups(flightTarget(uint16(i)), uids...)
				calls[i] = width64Lookup(t, recovery, groups[i])
				width64Entry(t, entered, uint16(i), 2*time.Second)
			}
			blocked := width64Lookup(t, recovery, flightGroups(flightTarget(uint16(cohorts)), "not-reserved-a", "not-reserved-b"))
			awaitFlightGate(t, blocked.waiting)
			if got := reads.Load(); got != int32(cohorts) {
				t.Fatalf("next round entered despite full width/key quota: reads=%d", got)
			}
			blocked.cancel()
			requireFlightError(t, width64Await(t, blocked), context.Canceled)
			follower := width64Lookup(t, recovery, groups[0])
			awaitFlightGate(t, follower.waiting)
			follower.cancel()
			requireFlightError(t, width64Await(t, follower), context.Canceled)
			close(releases[0])
			requireColdRoutes(t, width64Await(t, calls[0]), groups[0])
			retryGroups := flightGroups(flightTarget(uint16(cohorts)), "not-reserved-a", "not-reserved-b")
			retry := width64Lookup(t, recovery, retryGroups)
			width64Entry(t, entered, uint16(cohorts), 2*time.Second)
			requireColdRoutes(t, width64Await(t, retry), retryGroups)
			for i := 1; i < cohorts; i++ {
				close(releases[i])
			}
			for i, call := range calls {
				requireColdRoutes(t, width64Await(t, call), groups[i])
			}
			if got := reads.Load(); got != int32(cohorts+1) {
				t.Fatalf("unexpected owner reads=%d after canceled follower and waiter", got)
			}
		})
	}
}

// This integration fixture allocates over 128 MiB for caller input alone and
// another 128 MiB for owned clones. The operator must admit memory headroom
// before execution; the owned-byte ledger is not a process RSS guarantee.
func TestAuthorityRecoveryWidth64Keeps128MiBByteBudgetWithFreeCohorts(t *testing.T) {
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
	// 4*512*65535 = 128 MiB - 2048, independently of the 64-cohort limit.
	held := make([]*flightCall, 4)
	for i := range held {
		held[i] = width64Lookup(t, recovery, pages[i])
		width64Entry(t, entered, uint16(100+i), 10*time.Second)
	}
	fillTarget := flightTarget(104)
	directory.BecomeAuthority(fillTarget)
	fill := width64Lookup(t, recovery, flightGroups(fillTarget, strings.Repeat("y", 2048)))
	width64Entry(t, entered, 104, 10*time.Second)
	byteTarget := flightTarget(105)
	directory.BecomeAuthority(byteTarget)
	oneByte := width64Lookup(t, recovery, flightGroups(byteTarget, "z"))
	awaitFlightGate(t, oneByte.waiting)
	if got := reads.Load(); got != 5 {
		t.Fatalf("byte quota admitted one extra byte with free cohorts; possible 512-MiB derived regression: reads=%d", got)
	}
	oneByte.cancel()
	requireFlightError(t, width64Await(t, oneByte), context.Canceled)
	held[0].cancel()
	requireFlightError(t, width64Await(t, held[0]), context.Canceled)
	afterRelease := width64Lookup(t, recovery, flightGroups(byteTarget, "z"))
	width64Entry(t, entered, 105, 10*time.Second)
	afterRelease.cancel()
	requireFlightError(t, width64Await(t, afterRelease), context.Canceled)
	fill.cancel()
	requireFlightError(t, width64Await(t, fill), context.Canceled)
	for i := 1; i < len(held); i++ {
		held[i].cancel()
		requireFlightError(t, width64Await(t, held[i]), context.Canceled)
	}
}
