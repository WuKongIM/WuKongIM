package cluster

import (
	"context"
	"errors"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func ownedRecoveryReader(t *testing.T, owner uint64, calls []presence.RecoveryCall) *presence.OwnerRecovery {
	t.Helper()
	registry := online.NewRegistry(online.RegistryOptions{})
	var session uint64
	for _, call := range calls {
		for _, group := range call.Groups {
			for _, uid := range group.UIDs {
				session++
				route := online.OwnerRoute{UID: uid, DeviceID: "device", Listener: "tcp", HashSlot: group.Target.HashSlot, OwnerNodeID: owner, OwnerBootID: owner * 11, SessionID: session, OwnerSeq: 1}
				if err := registry.RegisterPending(online.LocalSession{Route: route}); err != nil {
					t.Fatal(err)
				}
				if err := registry.MarkActive(session); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return presence.NewOwnerRecovery(registry, owner, owner*11, func(presence.RouteTarget) error { return nil })
}

func TestOwnedRecoveryProviderRetainsAllOwnerProofUntilRelease(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2)
	local := ownedRecoveryReader(t, 1, calls)
	remote := accessnode.New(accessnode.Options{OwnerRoutes: ownedRecoveryReader(t, 2, calls)})
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: remote}}
	provider, ok := any(NewPresenceRecoveryOwners(node, local)).(presence.RecoveryCallBatchOwnersBounded)
	if !ok || !provider.SupportsBoundedRecoveryResults() {
		t.Fatal("production owner adapter cannot bound page and retained accumulator ownership")
	}
	budget := presence.NewRecoveryResultBudget(100, 1<<20)
	owned, err := provider.RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned.Results) != 2 {
		t.Fatalf("calls=%d", len(owned.Results))
	}
	for _, call := range owned.Results {
		if len(call) != 1 || call[0].Err != nil || len(call[0].Routes) != 2 {
			t.Fatal("bounded proof lost an owner or changed original-call alignment")
		}
	}
	if usage := budget.Stats(); usage.Rows < 4 || usage.Rows > 8 || usage.HighWaterRows <= usage.Rows || usage.HighWaterRows > 100 {
		t.Fatalf("page/accumulator peak was not covered: %+v", usage)
	}
	owned.Release()
	owned.Release()
	if len(owned.Results) != 0 || budget.Stats().Rows != 0 || budget.Stats().Bytes != 0 {
		t.Fatal("released proof still owns result references or credits")
	}
}

func TestOwnedRecoveryProviderRejectsMalformedLaterOwnerWithoutPartialProof(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2)
	local := ownedRecoveryReader(t, 1, calls)
	remote := accessnode.New(accessnode.Options{OwnerRoutes: ownedRecoveryReader(t, 2, calls)})
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(ctx context.Context, body []byte) ([]byte, error) {
		reply, err := remote.HandlePresenceOwnerRPC(ctx, body)
		return append(reply, 0), err
	}}}
	provider, ok := any(NewPresenceRecoveryOwners(node, local)).(presence.RecoveryCallBatchOwnersBounded)
	if !ok {
		t.Fatal("production adapter lacks owned results")
	}
	budget := presence.NewRecoveryResultBudget(100, 1<<20)
	owned, err := provider.RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
	if err == nil || len(owned.Results) != 0 || budget.Stats().Rows != 0 || budget.Stats().Bytes != 0 {
		t.Fatal("later malformed owner retained or published an earlier partial proof")
	}
}

func TestOwnedRecoveryProviderBoundsLegacyProtocolAndKeepsLegacyProviderGuard(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2)
	local := ownedRecoveryReader(t, 1, calls)
	remote := accessnode.New(accessnode.Options{OwnerRoutes: ownedRecoveryReader(t, 2, calls)})
	attempts := 0
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(ctx context.Context, body []byte) ([]byte, error) {
		attempts++
		if len(body) > 5 && body[5] == 11 {
			return nil, transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "internal/access/node: unknown presence op id 11"}
		}
		return remote.HandlePresenceOwnerRPC(ctx, body)
	}}}
	provider, ok := any(NewPresenceRecoveryOwners(node, local)).(presence.RecoveryCallBatchOwnersBounded)
	if !ok {
		t.Fatal("production adapter lacks owned results")
	}
	budget := presence.NewRecoveryResultBudget(100, 1<<20)
	owned, err := provider.RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
	if err != nil || len(owned.Results) != 2 || len(owned.Results[0][0].Routes) != 2 || attempts != 3 {
		t.Fatalf("bounded protocol fallback changed proof or replay boundary: err=%v attempts=%d", err, attempts)
	}
	owned.Release()
	if budget.Stats().Bytes != 0 || budget.Stats().Rows != 0 {
		t.Fatal("legacy wire path leaked owned credit")
	}
	legacy := recoveryCallReader{page: func(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error) {
		return presence.OwnerRouteCallPage{}, errors.New("not expected")
	}}
	fallback, ok := any(NewPresenceRecoveryOwners(node, legacy)).(presence.RecoveryCallBatchOwnersBounded)
	if ok && fallback.SupportsBoundedRecoveryResults() {
		t.Fatal("unknown local allocator bypassed original-call result credits")
	}
}
