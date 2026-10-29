package presence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	p "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Before implementation: reject an uncommitted/stale Begin before touching the
// registry; changed claim during Begin must not return success; a current End
// does not erase the gate needed for exact, prepaid all-owner UID rechecks.
func TestOwnerGenerationValidatesBeforeBegin(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	c := p.GenerationClaim{Target: p.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}, OwnerSetEpoch: 2, ServingBoot: 22, Generation: 1, AppliedRaftIndex: 100}
	owner := p.NewOwnerGeneration(r, 1, 11, func(context.Context, p.GenerationClaim) error { return authority.ErrStaleGeneration })
	if _, err := owner.BeginSlot(context.Background(), c); !errors.Is(err, authority.ErrStaleGeneration) {
		t.Fatalf("invalid Begin: %v", err)
	}
	// A failed validator must not consume this claim in the real registry.
	valid := p.NewOwnerGeneration(r, 1, 11, func(context.Context, p.GenerationClaim) error { return nil })
	cut, err := valid.BeginSlot(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = valid.AbortSlot(context.Background(), c, cut) })
	budget := p.NewRecoveryResultBudget(4096, 1<<20)
	page, err := valid.ReadSlotPage(context.Background(), p.SlotPageRequest{Claim: c, Owner: cut, Sequence: 1, MaxRows: 10, MaxBytes: 1024}, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Release()
	if !page.Page.Exhausted || len(page.Page.Routes) != 0 {
		t.Fatalf("wrong empty page: %+v", page.Page)
	}
	end := p.SlotEndRequest{Claim: c, Owner: cut, Sequence: 1}
	if _, err := valid.EndSlot(context.Background(), end); err != nil {
		t.Fatal(err)
	}
	read, err := valid.ReadGenerationUIDs(context.Background(), c, cut, []string{"offline"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	read.Release()
	page.Release()
	if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("credits retained: %+v", stats)
	}
}

// A freshness proof may require a control quorum only at Begin. Repeated pages,
// End and suspect reads must check the applied gate without repeating that I/O.
func TestOwnerGenerationPagesUseCurrentGateWithoutBeginProbe(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	beginChecks, currentChecks := 0, 0
	c := p.GenerationClaim{Target: p.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}, OwnerSetEpoch: 2, ServingBoot: 22, Generation: 1, AppliedRaftIndex: 100}
	owner := p.NewOwnerGenerationWithChecks(r, 1, 11, func(context.Context, p.GenerationClaim) error { beginChecks++; return nil }, func(context.Context, p.GenerationClaim) error { currentChecks++; return nil })
	ctx := context.Background()
	cut, err := owner.BeginSlot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	budget := p.NewRecoveryResultBudget(4096, 1<<20)
	page, err := owner.ReadSlotPage(ctx, p.SlotPageRequest{Claim: c, Owner: cut, Sequence: 1, MaxRows: 10, MaxBytes: 1024}, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Release()
	if _, err = owner.EndSlot(ctx, p.SlotEndRequest{Claim: c, Owner: cut, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	read, err := owner.ReadGenerationUIDs(ctx, c, cut, []string{"offline"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	read.Release()
	if beginChecks != 1 || currentChecks < 5 {
		t.Fatalf("unexpected probe boundary: begin=%d current=%d", beginChecks, currentChecks)
	}
}
