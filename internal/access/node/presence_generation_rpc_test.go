package node

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type generationLoopback struct {
	adapter *Adapter
	calls   int
}

func (n *generationLoopback) CallRPC(ctx context.Context, _ uint64, service uint8, body []byte) ([]byte, error) {
	n.calls++
	if service == PresenceOwnerRPCServiceID {
		return n.adapter.HandlePresenceOwnerRPC(ctx, body)
	}
	return n.adapter.HandlePresenceAuthorityRPC(ctx, body)
}

func TestGenerationRPCPagesRoundTripAndReleaseOwnedReferences(t *testing.T) {
	claim := wireGeneration()
	registry := online.NewRegistry(online.RegistryOptions{})
	route := wireRoute()
	if err := registry.RegisterPending(online.LocalSession{Route: online.OwnerRoute{UID: route.UID, HashSlot: 37, OwnerNodeID: 7, OwnerBootID: 17, OwnerSeq: 8, SessionID: 88, DeviceID: "d", LastActivityUnix: 20}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive(88); err != nil {
		t.Fatal(err)
	}
	owner := presence.NewOwnerGeneration(registry, 7, 17, func(_ context.Context, c presence.GenerationClaim) error {
		if !claim.Equal(c) {
			return authority.ErrStaleGeneration
		}
		return nil
	})
	network := &generationLoopback{adapter: New(Options{GenerationOwner: owner})}
	client := NewClient(network)
	cut, err := client.BeginPresenceSlot(context.Background(), 7, claim)
	if err != nil {
		t.Fatal(err)
	}
	budget := presence.NewRecoveryResultBudget(4096, 1<<20)
	req := presence.SlotPageRequest{Claim: claim, Owner: cut, Sequence: 1, MaxRows: 4096, MaxBytes: 1 << 20}
	page, err := client.ReadPresenceSlotPage(context.Background(), req, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Page.Routes) != 1 || page.Page.Routes[0].LastSeenUnix != 20 {
		t.Fatal("wire activity not preserved")
	}
	end := presence.SlotEndRequest{Claim: claim, Owner: cut, Sequence: page.Page.Sequence, LastSessionID: page.Page.NextSessionID, RowsTotal: page.Page.RowsTotal}
	if _, err = client.EndPresenceSlot(context.Background(), end); err != nil {
		t.Fatal(err)
	}
	page.Release()
	if stats := registry.SlotPageBudget(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("owner wire/cache leaked: %+v", stats)
	}
	if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("decode leaked: %+v", stats)
	}
	rows, err := client.ReadPresenceGenerationUIDs(context.Background(), claim, cut, []string{"user", "offline"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Routes) != 1 {
		t.Fatalf("UID recheck %+v", rows.Routes)
	}
	rows.Release()
	before := network.calls
	foreign := req
	foreign.Owner.BootID++
	if _, err = client.ReadPresenceSlotPage(context.Background(), foreign, budget); err == nil {
		t.Fatal("foreign boot accepted")
	}
	if network.calls != before+1 {
		t.Fatal("failed full request retried or downgraded")
	}
	missing := NewClient(&generationLoopback{adapter: New(Options{})})
	if _, err = missing.BeginPresenceSlot(context.Background(), 7, claim); err == nil {
		t.Fatal("missing generation owner returned successful empty")
	}
}

func TestGenerationRPCCommitCarriesFinalClaimAndReceipt(t *testing.T) {
	claim, cut := wireGeneration(), wireCut()
	route := wireRoute()
	port := &generationAuthorityFixture{claim: claim, receipt: presence.ActivationReceipt{Claim: claim, Owner: cut, Identity: route.Identity(), OwnerSeq: route.OwnerSeq}}
	network := &generationLoopback{adapter: New(Options{GenerationAuthority: port})}
	client := NewClient(network)
	result, err := client.RegisterPresenceGeneration(context.Background(), claim.Target, route)
	if err != nil || result.PendingClaim == nil || !result.PendingClaim.Equal(claim) {
		t.Fatalf("pending response %+v %v", result, err)
	}
	receipt, err := client.CommitPresenceGeneration(context.Background(), claim, string(result.PendingToken))
	if err != nil || !receipt.Claim.Equal(claim) {
		t.Fatalf("commit response %+v %v", receipt, err)
	}
	stale := claim
	stale.Generation--
	if _, err = client.CommitPresenceGeneration(context.Background(), stale, "pending"); !errors.Is(err, authority.ErrStaleGeneration) {
		t.Fatalf("stale claim became success: %v", err)
	}
	if err = client.AbortPresenceGeneration(context.Background(), claim, "pending"); err != nil {
		t.Fatal(err)
	}
}

type generationAuthorityFixture struct {
	claim   presence.GenerationClaim
	receipt presence.ActivationReceipt
}

func (f *generationAuthorityFixture) RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error) {
	c := f.claim
	return presence.RegisterResult{PendingToken: "pending", PendingClaim: &c}, nil
}
func (f *generationAuthorityFixture) CommitRouteWithReceipt(_ context.Context, c presence.GenerationClaim, _ presence.PendingRouteToken) (presence.ActivationReceipt, error) {
	if !c.Equal(f.claim) {
		return presence.ActivationReceipt{}, authority.ErrStaleGeneration
	}
	return f.receipt, nil
}
func (f *generationAuthorityFixture) AbortRouteWithClaim(context.Context, presence.GenerationClaim, presence.PendingRouteToken) error {
	return nil
}
func (f *generationAuthorityFixture) TouchRoutes(context.Context, presence.GenerationClaim, []presence.Route) error {
	return nil
}
func (f *generationAuthorityFixture) UnregisterRoute(context.Context, presence.GenerationClaim, presence.RouteIdentity, uint64) error {
	return nil
}
