//go:build integration

package presence_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
)

// proofWorkingSetOwners supplies complete healthy owner evidence. Odd fixture
// indexes are online; even indexes are confirmed offline. Calls retain only
// anonymous fixture indexes, independently of the real Directory's proofs.
type proofWorkingSetOwners struct {
	mu      sync.Mutex
	indexes map[string]int
	calls   [][]int
	hook    func(context.Context, []int) error
}

func (o *proofWorkingSetOwners) RecoverRoutes(ctx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	indexes := make([]int, len(uids))
	var routes []presence.Route
	for i, uid := range uids {
		index, ok := o.indexes[uid]
		if !ok || hashslot.HashSlotForKey(uid, 256) != target.HashSlot {
			return nil, fmt.Errorf("owner fixture received an unknown or misrouted UID")
		}
		indexes[i] = index
		if index%2 == 1 {
			routes = append(routes, proofWorkingSetRoute(uid, index))
		}
	}
	o.mu.Lock()
	o.calls = append(o.calls, indexes)
	hook := o.hook
	var observed []int
	if hook != nil {
		observed = append([]int(nil), indexes...)
	}
	o.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, observed); err != nil {
			return nil, err
		}
	}
	return routes, nil
}

func (o *proofWorkingSetOwners) setHook(hook func(context.Context, []int) error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.hook = hook
}

func (o *proofWorkingSetOwners) observedCalls() [][]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	calls := make([][]int, len(o.calls))
	for i := range calls {
		calls[i] = append([]int(nil), o.calls[i]...)
	}
	return calls
}

func proofWorkingSetRoute(uid string, index int) presence.Route {
	return presence.Route{UID: uid, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1,
		SessionID: uint64(index + 1), ConnectedUnix: 100, LastSeenUnix: 100}
}

// proofWorkingSetUIDs uses the actual public 256-slot hash function, rather
// than putting arbitrary UIDs into a target they would never reach in service.
func proofWorkingSetUIDs(t *testing.T, target presence.RouteTarget, count int) []string {
	t.Helper()
	uids := make([]string, 0, count)
	for candidate := 0; candidate < 1<<20 && len(uids) < count; candidate++ {
		uid := fmt.Sprintf("proof-working-set-%06d", candidate)
		if hashslot.HashSlotForKey(uid, 256) == target.HashSlot {
			uids = append(uids, uid)
		}
	}
	if len(uids) != count {
		t.Fatalf("bounded UID selection found %d same-slot UIDs, want %d", len(uids), count)
	}
	return uids
}

func newProofWorkingSet(t *testing.T, count int) (*authority.Directory, *presence.AuthorityRecovery, *proofWorkingSetOwners, presence.RouteTarget, []string) {
	t.Helper()
	target := presence.RouteTarget{HashSlot: 37, SlotID: 7, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2, RouteRevision: 1}
	uids := proofWorkingSetUIDs(t, target, count)
	owners := &proofWorkingSetOwners{indexes: make(map[string]int, len(uids))}
	for i, uid := range uids {
		owners.indexes[uid] = i
	}
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	directory.BecomeAuthority(target)
	recovery := presence.NewAuthorityRecovery(directory, owners)
	closeProofWorkingSet(t, recovery)
	return directory, recovery, owners, target, uids
}

func closeProofWorkingSet(t *testing.T, recovery *presence.AuthorityRecovery) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := recovery.Close(ctx); err != nil {
			t.Errorf("recovery ownership did not join: %v", err)
		}
	})
}

// This wrapper deliberately overrides the existing public readback contract.
// Embedding a built-in Directory must not bypass its current failure result.
type proofWorkingSetReadbackFault struct {
	*authority.Directory
	mu  sync.Mutex
	err error
}

func (d *proofWorkingSetReadbackFault) EndpointsByUIDs(target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	d.mu.Lock()
	err := d.err
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return d.Directory.EndpointsByUIDs(target, uids)
}

func requireProofWorkingSetRoutes(t *testing.T, got []presence.Route, uids []string, owners *proofWorkingSetOwners) {
	t.Helper()
	var want []presence.Route
	for _, uid := range uids {
		index := owners.indexes[uid]
		if index%2 == 1 {
			want = append(want, proofWorkingSetRoute(uid, index))
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete positive/offline result changed: got %d positive routes, want %d with exact identities and order", len(got), len(want))
	}
}

func proofWorkingSetCallSizes(calls [][]int) []int {
	sizes := make([]int, len(calls))
	for i, call := range calls {
		sizes[i] = len(call)
	}
	return sizes
}

// This reproduces a proof-retention mechanism through public APIs. It does not
// establish that this mechanism caused either expired sample in the R13 run.
func TestAuthorityRecoveryProofWorkingSet(t *testing.T) {
	t.Run("1025_uid_page_remains_readable_after_recovery", func(t *testing.T) {
		directory, recovery, owners, target, uids := newProofWorkingSet(t, 1025)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows, err := recovery.Endpoints(ctx, target, uids)
		if err != nil {
			t.Fatalf("healthy first scan failed: %v", err)
		}
		requireProofWorkingSetRoutes(t, rows, uids, owners)
		if sizes := proofWorkingSetCallSizes(owners.observedCalls()); !reflect.DeepEqual(sizes, []int{512, 512, 1}) {
			t.Fatalf("first-scan owner page sizes=%v, want [512 512 1]", sizes)
		}

		// A bounded cache may require a new owner proof. Completing that proof
		// must not make another UID in this same requested page unreadable.
		rows, err = recovery.Endpoints(ctx, target, uids[:512])
		calls := owners.observedCalls()
		t.Logf("owner_calls=%d owner_page_sizes=%v last_requested_fixture_indexes=%v", len(calls), proofWorkingSetCallSizes(calls), calls[len(calls)-1])
		if sizes := proofWorkingSetCallSizes(calls); !reflect.DeepEqual(sizes, []int{512, 512, 1, 1}) {
			t.Fatalf("repeated-scan owner page sizes=%v, want [512 512 1 1]", sizes)
		}
		if _, ordinaryErr := directory.EndpointsByUIDs(target, uids[:512]); !errors.Is(ordinaryErr, authority.ErrRouteNotReady) {
			t.Fatalf("ordinary Directory lookup unexpectedly retained all evicted proofs: %v", ordinaryErr)
		}
		if err != nil {
			t.Fatalf("healthy repeated page lost complete proof: phase=%s err=%v", presence.ActivationFailurePhase(err), err)
		}
		requireProofWorkingSetRoutes(t, rows, uids[:512], owners)
	})

	for _, scenario := range []string{"current_route_after_owner_change", "same_fence_authority_replacement", "revision_only_authority_update"} {
		t.Run(scenario, func(t *testing.T) {
			directory, recovery, owners, target, uids := newProofWorkingSet(t, 1025)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := recovery.Endpoints(ctx, target, uids); err != nil {
				t.Fatal(err)
			}
			if sizes := proofWorkingSetCallSizes(owners.observedCalls()); !reflect.DeepEqual(sizes, []int{512, 512, 1}) {
				t.Fatalf("initial owner pages=%v", sizes)
			}
			entered, release := make(chan []int, 1), make(chan struct{})
			var releaseOnce sync.Once
			owners.setHook(func(ctx context.Context, indexes []int) error {
				select {
				case entered <- indexes:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			type result struct {
				rows []presence.Route
				err  error
			}
			done := make(chan result, 1)
			go func() { rows, err := recovery.Endpoints(ctx, target, uids[:512]); done <- result{rows, err} }()
			joined := false
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				if !joined {
					joinCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
					defer stop()
					select {
					case <-done:
					case <-joinCtx.Done():
						t.Error("blocked readback caller did not join")
					}
				}
			})
			select {
			case indexes := <-entered:
				if !reflect.DeepEqual(indexes, []int{0}) {
					t.Fatalf("missing owner subset=%v, want anonymous index [0]", indexes)
				}
			case <-ctx.Done():
				t.Fatal("missing owner did not reach the controlled boundary")
			}
			current := proofWorkingSetRoute(uids[1], 1)
			switch scenario {
			case "current_route_after_owner_change":
				if err := directory.UnregisterRoute(target, current.Identity(), 2); err != nil {
					t.Fatal(err)
				}
				current.OwnerBootID, current.SessionID, current.OwnerSeq = 23, 9001, 3
				if registered, err := directory.RegisterRoute(target, current); err != nil || registered.PendingToken != "" {
					t.Fatalf("current owner registration failed: %v", err)
				}
			case "same_fence_authority_replacement":
				directory.LoseAuthority(target.HashSlot)
				directory.BecomeAuthority(target)
			case "revision_only_authority_update":
				next := target
				next.RouteRevision++
				directory.BecomeAuthority(next)
			}
			releaseOnce.Do(func() { close(release) })
			var got result
			select {
			case got = <-done:
				joined = true
			case <-ctx.Done():
				t.Fatal("controlled readback did not return")
			}
			if scenario == "same_fence_authority_replacement" {
				if !errors.Is(got.err, authority.ErrRouteNotReady) || len(got.rows) != 0 {
					t.Fatalf("replaced authority fabricated ready/offline result: rows=%d err=%v", len(got.rows), got.err)
				}
				return
			}
			if got.err != nil {
				t.Fatalf("current-generation readback failed: %v", got.err)
			}
			if scenario == "current_route_after_owner_change" {
				if len(got.rows) == 0 || got.rows[0] != current {
					t.Fatal("readback returned a captured old route instead of the current owner")
				}
				requireProofWorkingSetRoutes(t, got.rows[1:], uids[2:512], owners)
			} else {
				requireProofWorkingSetRoutes(t, got.rows, uids[:512], owners)
			}
		})
	}

	t.Run("embedding_wrapper_retains_readback_failure", func(t *testing.T) {
		directory, _, owners, target, uids := newProofWorkingSet(t, 2)
		wrapper := &proofWorkingSetReadbackFault{Directory: directory}
		sentinel := fmt.Errorf("controlled readback failure: %w", authority.ErrRouteNotReady)
		owners.setHook(func(context.Context, []int) error {
			wrapper.mu.Lock()
			defer wrapper.mu.Unlock()
			wrapper.err = sentinel
			return nil
		})
		recovery := presence.NewAuthorityRecovery(wrapper, owners)
		closeProofWorkingSet(t, recovery)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if rows, err := recovery.Endpoints(ctx, target, uids); !errors.Is(err, sentinel) || !errors.Is(err, authority.ErrRouteNotReady) || len(rows) != 0 {
			t.Fatalf("embedding wrapper failure bypassed: rows=%d err=%v", len(rows), err)
		}
	})

	t.Run("built_in_typed_nil_fails_closed", func(t *testing.T) {
		defer func() {
			if recover() != nil {
				t.Error("typed-nil built-in Directory panicked instead of returning not-ready")
			}
		}()
		var directory *authority.Directory
		recovery := presence.NewAuthorityRecovery(directory, &proofWorkingSetOwners{})
		closeProofWorkingSet(t, recovery)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if rows, err := recovery.Endpoints(ctx, presence.RouteTarget{HashSlot: 37, SlotID: 7, LeaderNodeID: 1, LeaderTerm: 3}, []string{"typed-nil"}); !errors.Is(err, authority.ErrRouteNotReady) || len(rows) != 0 {
			t.Fatalf("typed-nil admission did not fail closed: rows=%d err=%v", len(rows), err)
		}
	})

	t.Run("1024_uid_warm_page_does_not_query_owners", func(t *testing.T) {
		_, recovery, owners, target, uids := newProofWorkingSet(t, 1024)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows, err := recovery.Endpoints(ctx, target, uids)
		if err != nil {
			t.Fatalf("healthy first scan failed: %v", err)
		}
		requireProofWorkingSetRoutes(t, rows, uids, owners)
		before := owners.observedCalls()
		if sizes := proofWorkingSetCallSizes(before); !reflect.DeepEqual(sizes, []int{512, 512}) {
			t.Fatalf("first-scan owner page sizes=%v, want [512 512]", sizes)
		}
		rows, err = recovery.Endpoints(ctx, target, uids[:512])
		if err != nil {
			t.Fatalf("warm repeated page failed: %v", err)
		}
		requireProofWorkingSetRoutes(t, rows, uids[:512], owners)
		if after := owners.observedCalls(); len(after) != len(before) {
			t.Fatalf("warm page queried healthy owners again: before=%d after=%d", len(before), len(after))
		}
		t.Logf("owner_calls=%d owner_page_sizes=%v", len(before), proofWorkingSetCallSizes(before))
	})

	t.Run("authority_advance_requires_fresh_proof", func(t *testing.T) {
		directory, recovery, owners, target, uids := newProofWorkingSet(t, 2)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows, err := recovery.Endpoints(ctx, target, uids)
		if err != nil {
			t.Fatalf("initial authority proof failed: %v", err)
		}
		requireProofWorkingSetRoutes(t, rows, uids, owners)
		next := target
		next.LeaderTerm++
		directory.BecomeAuthority(next)
		if _, err := directory.EndpointsByUIDs(next, uids); !errors.Is(err, authority.ErrRouteNotReady) {
			t.Fatalf("new authority reused old proof: %v", err)
		}
		if _, err := recovery.Endpoints(ctx, target, uids); !errors.Is(err, authority.ErrNotLeader) {
			t.Fatalf("stale authority did not retain its fence error: %v", err)
		}
		if calls := owners.observedCalls(); len(calls) != 1 {
			t.Fatalf("stale authority called owners: calls=%d", len(calls))
		}
		rows, err = recovery.Endpoints(ctx, next, uids)
		if err != nil {
			t.Fatalf("new authority could not reconstruct current proof: %v", err)
		}
		requireProofWorkingSetRoutes(t, rows, uids, owners)
		if calls := owners.observedCalls(); len(calls) != 2 {
			t.Fatalf("new authority did not query current owners independently: calls=%d", len(calls))
		}
	})

	t.Run("stale_owner_cannot_resurrect_tombstoned_route", func(t *testing.T) {
		directory, recovery, owners, target, uids := newProofWorkingSet(t, 2)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stale := proofWorkingSetRoute(uids[1], 1)
		if err := directory.UnregisterRoute(target, stale.Identity(), stale.OwnerSeq+1); err != nil {
			t.Fatal(err)
		}
		for attempt := 0; attempt < 2; attempt++ {
			rows, err := recovery.Endpoints(ctx, target, uids[1:])
			if err != nil || len(rows) != 0 {
				t.Fatalf("tombstoned owner route was resurrected: attempt=%d rows=%d err=%v", attempt, len(rows), err)
			}
		}
		if calls := owners.observedCalls(); len(calls) != 1 {
			t.Fatalf("completed tombstone proof queried owners again: calls=%d", len(calls))
		}
	})
}
