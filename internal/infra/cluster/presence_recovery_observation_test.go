package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestRecoveryProviderSuccessAllocationBaseline(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	provider := NewPresenceRecoveryOwners(node, ownedRecoveryReader(t, 1, calls))
	budget := presence.NewRecoveryResultBudget(100, 1<<20)
	allocations := testing.AllocsPerRun(100, func() {
		owned, err := provider.RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
		if err != nil {
			panic(err)
		}
		owned.Release()
	})
	t.Logf("successful public provider allocations: %.0f", allocations)
}

func TestRecoveryProviderFailureCapsuleDistinguishesSnapshotAndOwner(t *testing.T) {
	for _, failure := range []string{"snapshot_before", "snapshot_after", "members_changed", "owner_timeout", "owner_page"} {
		t.Run(failure, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			local := ownedRecoveryReader(t, 1, calls)
			snapshots := 0
			node.controller = func(context.Context) (control.Snapshot, error) {
				snapshots++
				if failure == "snapshot_before" || failure == "snapshot_after" && snapshots == 2 {
					return control.Snapshot{}, errors.New("snapshot private sentinel")
				}
				result := node.snapshot
				if failure == "members_changed" && snapshots == 2 {
					result.Nodes = append([]control.Node(nil), result.Nodes...)
					result.Nodes[1].Addr = "changed"
				}
				return result, nil
			}
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(context.Context, []byte) ([]byte, error) {
				if failure == "owner_timeout" {
					return nil, transport.ErrTimeout
				}
				return nil, errors.New("malformed/private owner sentinel")
			}}}
			if failure != "owner_timeout" && failure != "owner_page" {
				remote := ownedRecoveryReader(t, 2, calls)
				node.rpcByNode[2] = presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: remote})}
			}
			owned, err := NewPresenceRecoveryOwners(node, local).RecoverRouteCallsBounded(context.Background(), calls, presence.NewRecoveryResultBudget(100, 1<<20), nil)
			defer owned.Release()
			if err == nil {
				err = owned.Results[0][0].Err
			}
			if !errors.Is(err, authority.ErrRouteNotReady) {
				t.Fatalf("failure family=%v", err)
			}
			got, ok := presence.RecoveryProviderFailure(err)
			wantReason := failure
			if failure == "owner_timeout" {
				wantReason = "owner_unavailable"
			}
			if !ok || got.Reason.String() != wantReason {
				t.Fatalf("closed failure evidence=%+v/%t, want %s", got, ok, wantReason)
			}
			if got.Calls != 2 || got.FailureElapsed < 0 {
				t.Fatalf("shared physical round=%+v", got)
			}
			if failure == "snapshot_before" && (got.Observed&presence.RecoveryProviderOwnerReadStage.Mask() != 0 || got.OwnerConfiguredBudget != 0) {
				t.Fatal("unreached owner read invented timing")
			}
			if failure == "owner_timeout" && (got.OwnerNodeID != 2 || got.OwnersPlanned != 2 || got.OwnersCompleted != 1 || got.OwnerConfiguredBudget != 250*time.Millisecond || got.OwnerEffectiveBudget <= 0 || got.OwnerEffectiveBudget > got.OwnerConfiguredBudget || got.OwnerMode != presence.RecoveryProviderOwnerRemoteCalls) {
				t.Fatalf("owner evidence=%+v", got)
			}
		})
	}
}

func TestRecoveryProviderScopeWithoutDispatchKeepsEffectiveBudgetUnknown(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node.controller = func(context.Context) (control.Snapshot, error) { cancel(); return node.snapshot, nil }
	owned, err := NewPresenceRecoveryOwners(node, ownedRecoveryReader(t, 1, calls)).RecoverRouteCallsBounded(ctx, calls, presence.NewRecoveryResultBudget(100, 1<<20), nil)
	defer owned.Release()
	data, ok := presence.RecoveryProviderFailure(err)
	if !ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("context evidence=%+v/%t err=%v", data, ok, err)
	}
	if data.OwnerConfiguredBudget != 250*time.Millisecond || data.OwnerEffectiveBudget != 0 || data.OwnerMode != presence.RecoveryProviderOwnerNone || data.Observed&presence.RecoveryProviderObservedOwnerDispatch != 0 {
		t.Fatalf("scope entry invented port dispatch: %+v", data)
	}
}

// observationPages changes only the injected page port, retaining the real
// registry capability for public adapter boundary tests with synthetic pages.
type observationPages struct {
	*presence.OwnerRecovery
	page func(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error)
}

func (p observationPages) ReadOwnerRouteCalls(ctx context.Context, calls []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
	return p.page(ctx, calls, start)
}
func (p observationPages) ReadOwnerRouteCallsBounded(ctx context.Context, calls []presence.RecoveryCall, start int, _ *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error) {
	page, err := p.page(ctx, calls, start)
	return presence.OwnedOwnerRouteCallPage{Page: page}, err
}

func TestRecoveryProviderFailureRetainsPrefixAndFreezesEarlierOwner(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2, 3)
	remote := observationPages{OwnerRecovery: ownedRecoveryReader(t, 2, calls), page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		if start == 1 {
			return presence.OwnerRouteCallPage{}, transport.ErrBusy
		}
		return recoveryCallsPage(2, 22, c, start, start+1, 0), nil
	}}
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: remote})}, 3: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: ownedRecoveryReader(t, 3, calls)})}}
	owned, err := NewPresenceRecoveryOwners(node, ownedRecoveryReader(t, 1, calls)).RecoverRouteCallsBounded(context.Background(), calls, presence.NewRecoveryResultBudget(100, 1<<20), nil)
	defer owned.Release()
	if err != nil || owned.Results[0][0].Err != nil || len(owned.Results[0][0].Routes) != 2 {
		t.Fatal("completed prefix proof changed")
	}
	data, ok := presence.RecoveryProviderFailure(owned.Results[1][0].Err)
	if !ok || data.Reason != presence.RecoveryProviderOwnerUnavailable || data.OwnerNodeID != 2 || data.PageStart != 1 || data.OwnersPlanned != 3 || data.OwnersCompleted != 1 || data.OwnerError != presence.RecoveryProviderOwnerBusy || data.OwnerElapsed <= 0 || data.Stages[presence.RecoveryProviderOwnerReadStage] <= 0 {
		t.Fatalf("earlier owner failure was overwritten: %+v/%t", data, ok)
	}
	if data.Observed&presence.RecoveryProviderSnapshotAfterStage.Mask() != 0 {
		t.Fatal("later work mutated captured mask")
	}
}

func TestRecoveryProviderFailureDoesNotPublishMalformedContinuation(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	local := observationPages{OwnerRecovery: ownedRecoveryReader(t, 1, calls), page: func(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error) {
		return presence.OwnerRouteCallPage{Next: 17}, nil
	}}
	owned, err := NewPresenceRecoveryOwners(node, local).RecoverRouteCallsBounded(context.Background(), calls, presence.NewRecoveryResultBudget(100, 1<<20), nil)
	defer owned.Release()
	data, ok := presence.RecoveryProviderFailure(err)
	if !ok || data.Reason != presence.RecoveryProviderOwnerPage || data.Observed&presence.RecoveryProviderObservedPageNext != 0 || data.PageNext != 0 || data.OwnerMode != presence.RecoveryProviderOwnerLocal {
		t.Fatalf("malformed page hid failure or invented validated next: %+v/%t", data, ok)
	}
}

func TestRecoveryProviderNewPageDoesNotBorrowPreviousPortEntry(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := observationPages{OwnerRecovery: ownedRecoveryReader(t, 1, calls), page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		if start != 0 {
			t.Fatal("canceled current page invoked its port")
		}
		return recoveryCallsPage(1, 11, c, start, start+1, 1), nil
	}}
	pageObservations := 0
	owned, err := NewPresenceRecoveryOwners(node, local).RecoverRouteCallsBounded(ctx, calls, presence.NewRecoveryResultBudget(100, 1<<20), func(usage presence.RecoveryResultUsage) {
		if usage.PageRows == 1 {
			pageObservations++
			if pageObservations == 2 {
				cancel()
			}
		}
	})
	defer owned.Release()
	data, ok := presence.RecoveryProviderFailure(err)
	if !ok || !errors.Is(err, context.Canceled) || data.PageStart != 1 || data.OwnerElapsed <= 0 || data.Observed&presence.RecoveryProviderObservedOwnerElapsed == 0 || data.Observed&presence.RecoveryProviderObservedOwnerDispatch != 0 || data.OwnerEffectiveBudget != 0 || data.OwnerMode != presence.RecoveryProviderOwnerNone {
		t.Fatalf("current page borrowed a previous port entry: %+v/%t", data, ok)
	}
}

func TestRecoveryProviderFailureSeparatesOwnerKindsAndParentBudget(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind presence.RecoveryProviderOwnerError
	}{{transport.ErrTimeout, presence.RecoveryProviderOwnerTimeout}, {transport.ErrBusy, presence.RecoveryProviderOwnerBusy}, {transport.ErrDialFailed, presence.RecoveryProviderOwnerDial}, {transport.ErrNodeNotFound, presence.RecoveryProviderOwnerMissing}, {context.Canceled, presence.RecoveryProviderOwnerCanceled}} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(context.Context, []byte) ([]byte, error) { return nil, tc.err }}}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			owned, err := NewPresenceRecoveryOwners(node, ownedRecoveryReader(t, 1, calls)).RecoverRouteCallsBounded(ctx, calls, presence.NewRecoveryResultBudget(100, 1<<20), nil)
			defer owned.Release()
			if err == nil {
				err = owned.Results[0][0].Err
			}
			data, ok := presence.RecoveryProviderFailure(err)
			if !ok || data.OwnerError != tc.kind || data.Observed&presence.RecoveryProviderObservedOwnerError == 0 || data.OwnerConfiguredBudget != 250*time.Millisecond || data.OwnerEffectiveBudget <= 0 || data.OwnerEffectiveBudget > 100*time.Millisecond {
				t.Fatalf("owner kind/budget collapsed: %+v/%t", data, ok)
			}
		})
	}
}

func TestRecoveryProviderFailureLimitDoesNotInheritPreviousGroupKind(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2)
	local := observationPages{OwnerRecovery: ownedRecoveryReader(t, 1, calls), page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		p := recoveryCallsPage(1, 11, c, start, len(c), 0)
		p.Results[1] = recoveryCallsPage(1, 11, c, 1, 2, 4096).Results[0]
		return p, nil
	}}
	remote := observationPages{OwnerRecovery: ownedRecoveryReader(t, 2, calls), page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		p := recoveryCallsPage(2, 22, c, start, len(c), 1)
		p.Results[0][0].Err = context.DeadlineExceeded
		p.Results[0][0].Snapshot.Routes = nil
		return p, nil
	}}
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: remote})}}
	owned, err := NewPresenceRecoveryOwners(node, local).RecoverRouteCallsBounded(context.Background(), calls, presence.NewRecoveryResultBudget(20000, 32<<20), nil)
	defer owned.Release()
	if err != nil {
		t.Fatal(err)
	}
	data, ok := presence.RecoveryProviderFailure(owned.Results[1][0].Err)
	if !ok || data.Reason != presence.RecoveryProviderCallLimit || data.OwnerError != presence.RecoveryProviderOwnerNoError || data.Observed&presence.RecoveryProviderObservedOwnerError != 0 {
		t.Fatalf("limit inherited transport cause: %+v/%t", data, ok)
	}
}
