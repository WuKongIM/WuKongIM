package presence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

type recoveryOwnersFunc func(context.Context, RouteTarget, []string) ([]Route, error)

func (f recoveryOwnersFunc) RecoverRoutes(ctx context.Context, target RouteTarget, uids []string) ([]Route, error) {
	return f(ctx, target, uids)
}

func TestAuthorityRecoveryRequiresCompleteOwnersAndRejectsOldCompletion(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}
	directory.BecomeAuthority(target)
	unavailable := true
	advance := false
	calls := 0
	recovery := NewAuthorityRecovery(directory, recoveryOwnersFunc(func(ctx context.Context, tgt RouteTarget, uids []string) ([]Route, error) {
		calls++
		if len(uids) > authority.RecoveryBatchSize {
			t.Fatal("unbounded recovery page")
		}
		if unavailable {
			return nil, authority.ErrRouteNotReady
		}
		if advance {
			next := tgt
			next.LeaderTerm++
			directory.BecomeAuthority(next)
		}
		return nil, nil
	}))
	if _, err := recovery.Endpoints(context.Background(), target, []string{"offline"}); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("partial owners=%v", err)
	}
	if _, err := directory.EndpointsByUID(target, "offline"); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("partial proof became offline: %v", err)
	}
	unavailable = false
	for i := 0; i < 2; i++ {
		if rows, err := recovery.Endpoints(context.Background(), target, []string{"offline"}); err != nil || len(rows) != 0 {
			t.Fatalf("confirmed offline=%+v err=%v", rows, err)
		}
	}
	if calls != 2 {
		t.Fatalf("cached offline was requeried: calls=%d", calls)
	}
	advance = true
	if _, err := recovery.Endpoints(context.Background(), target, []string{"next"}); !errors.Is(err, authority.ErrNotLeader) {
		t.Fatalf("old completion=%v", err)
	}
	next := target
	next.LeaderTerm++
	if _, err := directory.EndpointsByUID(next, "next"); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("old completion made new authority ready: %v", err)
	}
}

func TestAuthorityRecoveryCancellationDoesNotQueryOwner(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}
	directory.BecomeAuthority(target)
	recovery := NewAuthorityRecovery(directory, recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
		t.Fatal("canceled recovery called owner")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recovery.Endpoints(ctx, target, []string{"bob"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup=%v", err)
	}
}

type recoveryBatchOwnersFunc func(context.Context, []EndpointLookupGroup) []EndpointLookupResult

func (f recoveryBatchOwnersFunc) RecoverRoutesByTargets(ctx context.Context, g []EndpointLookupGroup) []EndpointLookupResult {
	return f(ctx, g)
}
func (f recoveryBatchOwnersFunc) RecoverRoutes(ctx context.Context, t RouteTarget, u []string) ([]Route, error) {
	r := f(ctx, []EndpointLookupGroup{{Target: t, UIDs: u}})[0]
	return r.Routes, r.Err
}

func TestAuthorityRecoveryBatchesColdTargetsAndPreservesSiblingResults(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	groups := make([]EndpointLookupGroup, 256)
	for i := range groups {
		target := RouteTarget{HashSlot: uint16(i), SlotID: uint32(i % 12), LeaderNodeID: 1, LeaderTerm: 4}
		directory.BecomeAuthority(target)
		groups[i] = EndpointLookupGroup{Target: target, UIDs: []string{fmt.Sprintf("%d-a", i), fmt.Sprintf("%d-b", i)}}
	}
	calls := 0
	owners := recoveryBatchOwnersFunc(func(_ context.Context, page []EndpointLookupGroup) []EndpointLookupResult {
		calls++
		if !ValidOwnerRecoveryPage(page) || len(page) != 256 {
			t.Fatalf("page bounds=%d", len(page))
		}
		results := make([]EndpointLookupResult, len(page))
		for i, group := range page {
			if group.Target.HashSlot == 3 {
				results[i].Err = authority.ErrNotLeader
				continue
			}
			for j, uid := range group.UIDs {
				results[i].Routes = append(results[i].Routes, Route{UID: uid, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: uint64(i*2 + j + 1)})
			}
		}
		return results
	})
	results := NewAuthorityRecovery(directory, owners).EndpointsByTargets(context.Background(), groups)
	if calls != 1 {
		t.Fatalf("owner rounds=%d", calls)
	}
	for i, result := range results {
		if i == 3 {
			if !errors.Is(result.Err, authority.ErrNotLeader) {
				t.Fatalf("stale group=%+v", result)
			}
			if _, err := directory.EndpointsByUIDs(groups[i].Target, groups[i].UIDs); !errors.Is(err, authority.ErrRouteNotReady) {
				t.Fatalf("failed group gained proof: %v", err)
			}
			continue
		}
		if result.Err != nil || len(result.Routes) != 2 {
			t.Fatalf("group %d=%+v", i, result)
		}
		for j, route := range result.Routes {
			if route.UID != groups[i].UIDs[j] {
				t.Fatalf("alignment=%+v", result)
			}
		}
	}
}

func TestAuthorityRecoveryPagesLargeTargetWithoutDiscardingEarlierResults(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 4}
	directory.BecomeAuthority(target)
	uids := make([]string, 1025)
	for i := range uids {
		uids[i] = fmt.Sprintf("uid-%d", i)
	}
	sizes := []int{}
	owners := recoveryBatchOwnersFunc(func(_ context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
		if !ValidOwnerRecoveryPage(groups) || len(groups) != 1 {
			t.Fatal("unbounded page")
		}
		sizes = append(sizes, len(groups[0].UIDs))
		results := make([]EndpointLookupResult, 1)
		for _, uid := range groups[0].UIDs {
			results[0].Routes = append(results[0].Routes, Route{UID: uid, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 1})
		}
		return results
	})
	rows, err := NewAuthorityRecovery(directory, owners).Endpoints(context.Background(), target, uids)
	if err != nil || len(rows) != len(uids) || !reflect.DeepEqual(sizes, []int{512, 512, 1}) {
		t.Fatalf("rows=%d pages=%v err=%v", len(rows), sizes, err)
	}
	for i, row := range rows {
		if row.UID != uids[i] {
			t.Fatalf("result %d=%s", i, row.UID)
		}
	}
}
