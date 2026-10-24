//go:build integration

package presence_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type flightOwnersFunc func(context.Context, []presence.EndpointLookupGroup) []presence.EndpointLookupResult

func (f flightOwnersFunc) RecoverRoutesByTargets(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	return f(ctx, groups)
}
func (f flightOwnersFunc) RecoverRoutes(ctx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	r := f(ctx, []presence.EndpointLookupGroup{{Target: target, UIDs: uids}})[0]
	return r.Routes, r.Err
}

// waitObservedContext exposes only the public context cancellation boundary. A
// follower with no owner I/O must reach a cancellation-aware wait before the
// test releases its leader; no private registry/lane state is inspected.
type waitObservedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type flightCall struct {
	done    chan []presence.EndpointLookupResult
	waiting chan struct{}
	cancel  context.CancelFunc
	joined  bool
	result  []presence.EndpointLookupResult
}

func startFlightCall(t *testing.T, r *presence.AuthorityRecovery, groups []presence.EndpointLookupGroup) *flightCall {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	observed := &waitObservedContext{Context: ctx, waiting: make(chan struct{})}
	c := &flightCall{done: make(chan []presence.EndpointLookupResult, 1), waiting: observed.waiting, cancel: cancel}
	go func() { c.done <- r.EndpointsByTargets(observed, groups) }()
	t.Cleanup(func() {
		cancel()
		if !c.joined {
			select {
			case c.result = <-c.done:
				c.joined = true
			case <-time.After(time.Second):
				t.Error("flight lookup cleanup did not join")
			}
		}
	})
	return c
}

func (c *flightCall) await(t *testing.T) []presence.EndpointLookupResult {
	t.Helper()
	if !c.joined {
		select {
		case c.result = <-c.done:
			c.joined = true
		case <-time.After(time.Second):
			t.Fatal("flight lookup did not finish within watchdog")
		}
	}
	return c.result
}

func awaitFlightGate(t *testing.T, gate <-chan struct{}) {
	t.Helper()
	select {
	case <-gate:
	case <-time.After(time.Second):
		t.Fatal("controlled public boundary was not reached")
	}
}

func flightTarget(hash uint16) presence.RouteTarget {
	return presence.RouteTarget{HashSlot: hash, SlotID: uint32(hash%12 + 1), LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1}
}

func flightGroups(target presence.RouteTarget, uids ...string) []presence.EndpointLookupGroup {
	return []presence.EndpointLookupGroup{{Target: target, UIDs: uids}}
}

func completeFlightRoutes(groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	results := make([]presence.EndpointLookupResult, len(groups))
	for i, g := range groups {
		for _, uid := range g.UIDs {
			if uid != "" {
				results[i].Routes = append(results[i].Routes, presence.Route{UID: uid, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 1})
			}
		}
	}
	return results
}

func flightError(groups []presence.EndpointLookupGroup, err error) []presence.EndpointLookupResult {
	results := make([]presence.EndpointLookupResult, len(groups))
	for i := range results {
		results[i].Err = err
	}
	return results
}

func requireFlightError(t *testing.T, results []presence.EndpointLookupResult, want error) {
	t.Helper()
	if len(results) != 1 || !errors.Is(results[0].Err, want) {
		var got error
		if len(results) == 1 {
			got = results[0].Err
		}
		t.Fatalf("groups=%d err=%v, want %v", len(results), got, want)
	}
}

func TestAuthorityRecoveryUIDFlightPartialOverlapPublishesBeforeJoin(t *testing.T) {
	d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(0)
	d.BecomeAuthority(target)
	aEntered, bEntered := make(chan struct{}), make(chan struct{})
	aRelease, bRelease := make(chan struct{}), make(chan struct{})
	var aOnce, bOnce sync.Once
	var reads atomic.Int32
	owners := flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		reads.Add(1)
		var gate <-chan struct{}
		for _, uid := range groups[0].UIDs {
			if uid == "u1" {
				aOnce.Do(func() { close(aEntered) })
				gate = aRelease
			} else if uid == "u2" {
				bOnce.Do(func() { close(bEntered) })
				gate = bRelease
			}
		}
		select {
		case <-gate:
			return completeFlightRoutes(groups)
		case <-ctx.Done():
			return flightError(groups, ctx.Err())
		}
	})
	r := presence.NewAuthorityRecovery(d, owners)
	a := startFlightCall(t, r, flightGroups(target, "u1"))
	t.Cleanup(func() {
		aOnce.Do(func() { close(aEntered) })
		select {
		case <-aRelease:
		default:
			close(aRelease)
		}
		select {
		case <-bRelease:
		default:
			close(bRelease)
		}
	})
	awaitFlightGate(t, aEntered)
	b := startFlightCall(t, r, flightGroups(target, "u1", "u2"))
	awaitFlightGate(t, bEntered)
	c := startFlightCall(t, r, flightGroups(target, "u2"))
	awaitFlightGate(t, c.waiting)
	close(bRelease)
	// C must be signaled by B's owned work before B can join A. A remains held.
	requireColdRoutes(t, c.await(t), flightGroups(target, "u2"))
	if got, err := d.EndpointsByUIDs(target, []string{"u2"}); err != nil || len(got) != 1 {
		t.Fatalf("new sibling not published before join: routes=%v err=%v", got, err)
	}
	close(aRelease)
	requireColdRoutes(t, a.await(t), flightGroups(target, "u1"))
	requireColdRoutes(t, b.await(t), flightGroups(target, "u1", "u2"))
	if reads.Load() != 2 {
		t.Fatalf("owner reads=%d, want two distinct owned subsets", reads.Load())
	}
}

func TestAuthorityRecoveryUIDFlightLeaderCancellationKeepsFollowerOwned(t *testing.T) {
	d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(0)
	d.BecomeAuthority(target)
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var once sync.Once
	var reads atomic.Int32
	r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		if reads.Add(1) > 1 {
			return completeFlightRoutes(groups)
		}
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return completeFlightRoutes(groups)
		case <-ctx.Done():
			return flightError(groups, ctx.Err())
		}
	}))
	a := startFlightCall(t, r, flightGroups(target, "same"))
	awaitFlightGate(t, entered)
	b := startFlightCall(t, r, flightGroups(target, "same"))
	awaitFlightGate(t, b.waiting)
	a.cancel()
	requireFlightError(t, a.await(t), context.Canceled)
	select {
	case <-b.done:
		t.Fatal("creator cancellation canceled a live follower's owned work")
	default:
	}
	release <- struct{}{}
	if result := b.await(t); len(result) != 1 || result[0].Err != nil || len(result[0].Routes) != 1 {
		t.Fatal("live follower did not reread successful owned completion")
	}
	if reads.Load() != 1 {
		t.Fatalf("follower retried owner: reads=%d", reads.Load())
	}
	if _, err := d.EndpointsByUIDs(target, []string{"same"}); err != nil {
		t.Fatalf("live follower's complete work did not install proof: %v", err)
	}
}

func TestAuthorityRecoveryUIDFlightDistributedIdentity(t *testing.T) {
	changes := []struct {
		name   string
		change func(*presence.RouteTarget)
	}{
		{"hash", func(t *presence.RouteTarget) { t.HashSlot++ }}, {"slot", func(t *presence.RouteTarget) { t.SlotID++ }},
		{"leader", func(t *presence.RouteTarget) { t.LeaderNodeID++ }}, {"term", func(t *presence.RouteTarget) { t.LeaderTerm++ }},
		{"config", func(t *presence.RouteTarget) { t.ConfigEpoch++ }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			old := flightTarget(0)
			d.BecomeAuthority(old)
			entered, release := make(chan struct{}), make(chan struct{})
			var reads atomic.Int32
			r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				if reads.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return flightError(groups, ctx.Err())
					}
				}
				return completeFlightRoutes(groups)
			}))
			a := startFlightCall(t, r, flightGroups(old, "same"))
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			awaitFlightGate(t, entered)
			next := old
			change.change(&next)
			d.BecomeAuthority(next)
			b := startFlightCall(t, r, flightGroups(next, "same"))
			requireColdRoutes(t, b.await(t), flightGroups(next, "same"))
			close(release)
			if next.HashSlot == old.HashSlot {
				requireFlightError(t, a.await(t), authority.ErrNotLeader)
			} else {
				requireColdRoutes(t, a.await(t), flightGroups(old, "same"))
			}
			if reads.Load() != 2 {
				t.Fatalf("distributed identities shared owner proof: reads=%d", reads.Load())
			}
		})
	}
}

func TestAuthorityRecoveryUIDFlightLocalMetadataCoalesces(t *testing.T) {
	r, owners, a, groups := startPausedColdPage(t, 1)
	other := groups[0]
	other.Target.AuthorityEpoch = 99
	other.Target.RouteRevision = 99
	b := startFlightCall(t, r, []presence.EndpointLookupGroup{other})
	awaitFlightGate(t, b.waiting)
	owners.unblock()
	requireColdRoutes(t, a.await(t), groups)
	requireColdRoutes(t, b.await(t), []presence.EndpointLookupGroup{other})
	if owners.blockedCalls.Load() != 1 {
		t.Fatalf("local observations split one distributed identity")
	}
}

func TestAuthorityRecoveryUIDFlightOwnsUIDBacking(t *testing.T) {
	d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(0)
	d.BecomeAuthority(target)
	backing := strings.Repeat("x", 1<<20)
	uid := backing[:8]
	var borrowed atomic.Bool
	r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		borrowed.Store(unsafe.StringData(groups[0].UIDs[0]) == unsafe.StringData(uid))
		return completeFlightRoutes(groups)
	}))
	requireColdRoutes(t, r.EndpointsByTargets(context.Background(), flightGroups(target, uid)), flightGroups(target, uid))
	if borrowed.Load() {
		t.Fatal("owner request retained caller UID backing instead of an owned exact-byte copy")
	}
}

func TestAuthorityRecoveryUIDFlightQuotaRechecksProofAndDoesNotReservePartialKeys(t *testing.T) {
	d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	entered := make(chan int, 32)
	releases := make([]chan struct{}, 16)
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	var active, peak, reads atomic.Int32
	r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		n := active.Add(1)
		defer active.Add(-1)
		reads.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		i := int(groups[0].Target.HashSlot)
		entered <- i
		if i < 16 {
			select {
			case <-releases[i]:
			case <-ctx.Done():
				return flightError(groups, ctx.Err())
			}
		}
		return completeFlightRoutes(groups)
	}))
	as := make([]*flightCall, 16)
	for i := 0; i < 17; i++ {
		d.BecomeAuthority(flightTarget(uint16(i)))
	}
	for i := range as {
		as[i] = startFlightCall(t, r, flightGroups(flightTarget(uint16(i)), fmt.Sprint(i)))
		select {
		case got := <-entered:
			if got != i {
				t.Fatalf("owner=%d", got)
			}
		case <-time.After(time.Second):
			t.Fatal("owner did not enter")
		}
	}
	t.Cleanup(func() {
		for _, release := range releases {
			select {
			case <-release:
			default:
				close(release)
			}
		}
	})
	canceled := startFlightCall(t, r, flightGroups(flightTarget(16), "canceled-1", "canceled-2"))
	awaitFlightGate(t, canceled.waiting)
	canceled.cancel()
	requireFlightError(t, canceled.await(t), context.Canceled)
	b := startFlightCall(t, r, flightGroups(flightTarget(16), "new-1", "new-2"))
	awaitFlightGate(t, b.waiting)
	if peak.Load() != 16 {
		t.Fatalf("leader cohort exceeded sixteen: peak=%d", peak.Load())
	}
	follower := startFlightCall(t, r, flightGroups(flightTarget(0), "0"))
	awaitFlightGate(t, follower.waiting)
	follower.cancel()
	requireFlightError(t, follower.await(t), context.Canceled)
	if err := d.InstallRecoveredRoutes(flightTarget(16), []string{"new-1", "new-2"}, completeFlightRoutes(flightGroups(flightTarget(16), "new-1", "new-2"))[0].Routes); err != nil {
		t.Fatal(err)
	}
	close(releases[0])
	requireColdRoutes(t, b.await(t), flightGroups(flightTarget(16), "new-1", "new-2"))
	for i := 1; i < 16; i++ {
		close(releases[i])
	}
	for i, a := range as {
		requireColdRoutes(t, a.await(t), flightGroups(flightTarget(uint16(i)), fmt.Sprint(i)))
	}
	if reads.Load() != 16 {
		t.Fatalf("quota wake requeried installed proof: reads=%d", reads.Load())
	}
}

func TestAuthorityRecoveryUIDFlightPackingBounds(t *testing.T) {
	for _, n := range []int{0, 511, 512, 513, 1025} {
		t.Run(fmt.Sprintf("uids-%d", n), func(t *testing.T) {
			d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			target := flightTarget(0)
			d.BecomeAuthority(target)
			uids := make([]string, n)
			for i := range uids {
				uids[i] = fmt.Sprint(i)
			}
			sizes := []int{}
			r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(_ context.Context, g []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				total := 0
				for _, group := range g {
					total += len(group.UIDs)
				}
				sizes = append(sizes, total)
				if total > 512 || len(g) > 256 {
					t.Error("owner page exceeded bounds")
				}
				return completeFlightRoutes(g)
			}))
			got := r.EndpointsByTargets(context.Background(), flightGroups(target, uids...))
			if len(got) != 1 || got[0].Err != nil || len(got[0].Routes) != n {
				t.Fatalf("packing groups=%d routes=%d", len(got), len(got[0].Routes))
			}
			want := (n + 511) / 512
			if len(sizes) != want {
				t.Fatalf("owner rounds=%v, want %d", sizes, want)
			}
		})
	}
	for _, n := range []int{255, 256, 257} {
		t.Run(fmt.Sprintf("targets-%d", n), func(t *testing.T) {
			d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			groups := make([]presence.EndpointLookupGroup, n)
			for i := range groups {
				target := flightTarget(uint16(i % 256))
				d.BecomeAuthority(target)
				groups[i] = flightGroups(target, fmt.Sprint(i))[0]
			}
			rounds := 0
			r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(_ context.Context, g []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				rounds++
				total := 0
				for _, group := range g {
					total += len(group.UIDs)
				}
				if total > 512 || len(g) > 256 {
					t.Error("owner page exceeded bounds")
				}
				return completeFlightRoutes(g)
			}))
			requireColdRoutes(t, r.EndpointsByTargets(context.Background(), groups), groups)
			if rounds != (n+255)/256 {
				t.Fatalf("owner rounds=%d", rounds)
			}
		})
	}
}

func TestAuthorityRecoveryUIDFlightPageBytesAndLegalStoredUIDs(t *testing.T) {
	for _, size := range []int{4096, 65535} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			target := flightTarget(0)
			d.BecomeAuthority(target)
			uids := make([]string, 512)
			for i := range uids {
				uids[i] = fmt.Sprintf("%04d", i) + strings.Repeat("x", size-4)
			}
			var rounds int
			r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				rounds++
				if len(groups) != 1 || len(groups[0].UIDs) != 512 {
					t.Errorf("legal full page changed packing")
				}
				return completeFlightRoutes(groups)
			}))
			requireColdRoutes(t, r.EndpointsByTargets(context.Background(), flightGroups(target, uids...)), flightGroups(target, uids...))
			if rounds != 1 {
				t.Fatalf("legal full page required %d owner rounds", rounds)
			}
		})
	}
	for _, size := range []int{32 << 20, (32 << 20) + 1} {
		t.Run(fmt.Sprintf("byte-bound-%d", size), func(t *testing.T) {
			d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			target := flightTarget(0)
			d.BecomeAuthority(target)
			var rounds int
			uid := strings.Repeat("x", size)
			r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(_ context.Context, g []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				rounds++
				return completeFlightRoutes(g)
			}))
			got := r.EndpointsByTargets(context.Background(), flightGroups(target, uid))
			if size > 32<<20 {
				requireFlightError(t, got, authority.ErrRouteNotReady)
				if rounds != 0 {
					t.Fatal("oversized page called owner")
				}
			} else {
				requireColdRoutes(t, got, flightGroups(target, uid))
				if rounds != 1 {
					t.Fatal("boundary-equal page rejected")
				}
			}
		})
	}
}

// evictingDirectory models another legitimate public Directory writer advancing
// the real FIFO immediately after installation, before a late waiter rereads it.
type evictingDirectory struct {
	*authority.Directory
	target presence.RouteTarget
}

func (d *evictingDirectory) InstallRecoveredRoutes(target presence.RouteTarget, uids []string, routes []presence.Route) error {
	if err := d.Directory.InstallRecoveredRoutes(target, uids, routes); err != nil {
		return err
	}
	for i := 0; i < 1024; i++ {
		if err := d.Directory.InstallRecoveredRoutes(target, []string{fmt.Sprintf("evict-%d", i)}, nil); err != nil {
			return err
		}
	}
	return nil
}

func TestAuthorityRecoveryUIDFlightLateWaiterMustRereadEvictedProof(t *testing.T) {
	d := &evictingDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true}), target: flightTarget(0)}
	d.BecomeAuthority(d.target)
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(ctx context.Context, g []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		if reads.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return completeFlightRoutes(g)
		case <-ctx.Done():
			return flightError(g, ctx.Err())
		}
	}))
	a := startFlightCall(t, r, flightGroups(d.target, "old"))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	awaitFlightGate(t, entered)
	b := startFlightCall(t, r, flightGroups(d.target, "old"))
	awaitFlightGate(t, b.waiting)
	close(release)
	requireFlightError(t, a.await(t), authority.ErrRouteNotReady)
	requireFlightError(t, b.await(t), authority.ErrRouteNotReady)
	if reads.Load() != 1 {
		t.Fatalf("late follower silently retried owner: reads=%d", reads.Load())
	}
}

func TestAuthorityRecoveryUIDFlightMixedGroupsDuplicatesAndTombstone(t *testing.T) {
	d := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(0)
	bad := flightTarget(1)
	d.BecomeAuthority(target)
	d.BecomeAuthority(bad)
	route := completeFlightRoutes(flightGroups(target, "u"))[0].Routes[0]
	if err := d.UnregisterRoute(target, route.Identity(), 2); err != nil {
		t.Fatal(err)
	}
	r := presence.NewAuthorityRecovery(d, flightOwnersFunc(func(_ context.Context, g []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		results := completeFlightRoutes(g)
		for i := range results {
			if g[i].Target == bad {
				results[i].Err = authority.ErrRouteNotReady
			}
		}
		return results
	}))
	groups := append(flightGroups(target, "u", "u", "v"), flightGroups(bad, "failed")...)
	got := r.EndpointsByTargets(context.Background(), groups)
	if len(got) != 2 || got[0].Err != nil || len(got[0].Routes) != 1 || got[0].Routes[0].UID != "v" || !errors.Is(got[1].Err, authority.ErrRouteNotReady) {
		t.Fatalf("mixed results lost alignment/tombstone: %v", got)
	}
	if _, err := d.EndpointsByUIDs(bad, []string{"failed"}); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("failed sibling gained negative proof: %v", err)
	}
	got = r.EndpointsByTargets(context.Background(), flightGroups(target, "v", "v"))
	if got[0].Err != nil || len(got[0].Routes) != 2 {
		t.Fatalf("duplicate multiplicity lost: %v", got)
	}
}
