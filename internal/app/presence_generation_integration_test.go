//go:build integration

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

type generationWiringCluster struct {
	*recoveryWiringCluster
	mu                sync.Mutex
	claim             controller.PresenceGeneration
	route             clusterpkg.Route
	enabled           bool
	wiredBeforeEnable bool
}

func (n *generationWiringCluster) RouteKey(string) (clusterpkg.Route, error) { return n.route, nil }
func (n *generationWiringCluster) RouteHashSlot(uint16) (clusterpkg.Route, error) {
	return n.route, nil
}
func (n *generationWiringCluster) RouteKeysPartial(keys []string) ([]clusterpkg.RouteKeyResult, error) {
	out := make([]clusterpkg.RouteKeyResult, len(keys))
	for i := range out {
		out[i].Route = n.route
	}
	return out, nil
}
func (*generationWiringCluster) PresenceBootID() uint64 { return 11 }
func (n *generationWiringCluster) EnablePresenceProtocol(v uint32) error {
	n.enabled = v == 1
	n.wiredBeforeEnable = n.registeredHandlers[accessnode.PresenceAuthorityRPCServiceID] != nil && n.registeredHandlers[accessnode.PresenceOwnerRPCServiceID] != nil
	return nil
}
func (*generationWiringCluster) EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error) {
	return controller.PresenceOwnerSet{Epoch: 7, Owners: []controller.PresenceOwner{{NodeID: 1, CapabilityGeneration: 11}}}, nil
}
func (n *generationWiringCluster) GetPresenceGeneration(context.Context, uint16) (controller.PresenceGenerationSnapshot, error) {
	return n.GetLocalPresenceGeneration(7)
}
func (n *generationWiringCluster) GetLocalPresenceGeneration(uint16) (controller.PresenceGenerationSnapshot, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	owners, _ := n.EnsurePresenceOwnerSet(context.Background())
	return controller.PresenceGenerationSnapshot{Generation: n.claim, OwnerSet: owners}, nil
}
func (n *generationWiringCluster) ClaimPresenceGeneration(_ context.Context, r controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.enabled || r.ExpectedGeneration != n.claim.Generation {
		return controller.PresenceGeneration{}, controller.ErrPresenceGenerationConflict
	}
	g := n.claim.Generation + 100
	n.claim = controller.PresenceGeneration{Fence: r.Fence, OwnerSetEpoch: r.OwnerSetEpoch, ServingBoot: r.ServingBoot, Generation: g, PreviousGeneration: r.ExpectedGeneration, AppliedRaftIndex: g}
	return n.claim, nil
}
func (n *generationWiringCluster) CheckPresenceGeneration(c controller.PresenceGeneration) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.enabled || c != n.claim {
		return controller.ErrPresenceGenerationConflict
	}
	return nil
}
func (n *generationWiringCluster) CheckPresenceOwnerGeneration(c controller.PresenceGeneration) error {
	return n.CheckPresenceGeneration(c)
}
func (n *generationWiringCluster) ValidatePresenceGeneration(_ context.Context, c controller.PresenceGeneration) error {
	return n.CheckPresenceGeneration(c)
}

func TestPresenceFullCompositionBindsBootReceiptsAndJoinedRecovery(t *testing.T) {
	base := newFakePresenceCluster(1, nil)
	targetRoute := clusterpkg.Route{HashSlot: 7, SlotID: 11, Leader: 1, LeaderTerm: 23, ConfigEpoch: 29, Revision: 17, AuthorityEpoch: 19}
	node := &generationWiringCluster{recoveryWiringCluster: &recoveryWiringCluster{base}, route: targetRoute}
	a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(node))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if a.presenceRecovery != nil {
			if err := a.presenceRecovery.Close(cleanup); err != nil {
				t.Error(err)
			}
		}
	})
	if !node.enabled || !node.wiredBeforeEnable {
		t.Fatal("protocol capability advertised before complete wiring")
	}
	if a.presenceAuthorityClient == nil || !a.presenceAuthorityClient.RequiresActivationReceipt() {
		t.Fatal("production client did not require receipt")
	}
	target := presence.RouteTarget{HashSlot: 7, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 23, ConfigEpoch: 29, RouteRevision: 17, AuthorityEpoch: 19}
	a.presenceDirectory.BecomeAuthority(target)
	for _, id := range []uint64{20, 10} {
		err = a.presence.Activate(ctx, presence.ActivateCommand{UID: "full-user", SessionID: id, DeviceID: "device", ConnectedUnix: 10})
		if err != nil {
			t.Fatalf("full Activate %d: %v", id, err)
		}
		session, ok := a.online.LocalSession(id)
		if !ok || session.Route.OwnerBootID != 11 || session.Route.OwnerSeq == 0 {
			t.Fatalf("owner boot/sequence not bound: %+v", session)
		}
	}
	first, _ := a.online.LocalSession(20)
	second, _ := a.online.LocalSession(10)
	if second.Route.OwnerSeq <= first.Route.OwnerSeq {
		t.Fatal("OwnerSeq fell back to reused/non-monotonic SessionID")
	}
	rows, err := a.presenceDirectory.EndpointsByUID(target, "full-user")
	if err != nil || len(rows) == 0 {
		t.Fatalf("active owner missing from Full directory: %+v %v", rows, err)
	}
	if err = a.presenceRecovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.presenceDirectory.RegisterRoute(target, authority.Route{UID: "legacy"}); err == nil {
		t.Fatal("Full slot accepted legacy activation")
	}
}
