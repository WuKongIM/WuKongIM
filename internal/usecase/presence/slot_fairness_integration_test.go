//go:build integration

package presence_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	p "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Failure matrix before the global page-yield implementation:
//   - Sixteen multipage slots may occupy actual I/O, but after page return a cold
//     seventeenth slot must precede their continuation work in the original FIFO.
//   - No caller/input credit is released merely to make room; 257 UIDs per call
//     prevents batching while all 17 calls stay below 8192 keys/512 callers.
//   - Cancellation and partial-page budget refusal must retire queued continuations
//     without publishing Complete, retrying pages, or waiting while holding credit.
//
// The last two use existing real cancellation/capacity integration cases plus
// the scheduler's original negative controls; this test isolates FIFO ordering.
type slotFairFixture struct {
	mu                        sync.RWMutex
	claims                    map[uint16]p.GenerationClaim
	directory                 *authority.Directory
	owner                     *p.OwnerGeneration
	first                     chan uint16
	releaseFirst, releaseRest chan struct{}
}

func (f *slotFairFixture) ClaimSlotGeneration(ctx context.Context, target p.RouteTarget) (p.GenerationClaim, []uint64, error) {
	if err := ctx.Err(); err != nil {
		return p.GenerationClaim{}, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old := f.claims[target.HashSlot]
	claim := p.GenerationClaim{Target: target, OwnerSetEpoch: 2, ServingBoot: 22, Generation: old.Generation + 1, PreviousGeneration: old.Generation, AppliedRaftIndex: 100 + old.Generation + 1}
	f.claims[target.HashSlot] = claim
	return claim, []uint64{1}, nil
}
func (f *slotFairFixture) ValidateSlotGeneration(ctx context.Context, c p.GenerationClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !c.Equal(f.claims[c.Target.HashSlot]) {
		return authority.ErrStaleGeneration
	}
	return nil
}
func (f *slotFairFixture) BeginSlot(ctx context.Context, c p.GenerationClaim, node uint64) (p.OwnerCut, error) {
	return f.owner.BeginSlot(ctx, c)
}
func (f *slotFairFixture) ReadSlotPage(ctx context.Context, req p.SlotPageRequest, b *p.RecoveryResultBudget) (*p.OwnedSlotPage, error) {
	page, err := f.owner.ReadSlotPage(ctx, req, b)
	if err != nil {
		return page, err
	}
	if req.Claim.Target.HashSlot < 16 {
		gate := f.releaseRest
		if req.Sequence == 1 {
			f.first <- req.Claim.Target.HashSlot
			gate = f.releaseFirst
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return page, ctx.Err()
		}
	}
	return page, nil
}
func (f *slotFairFixture) EndSlot(ctx context.Context, req p.SlotEndRequest) (p.SlotEnd, error) {
	return f.owner.EndSlot(ctx, req)
}
func (f *slotFairFixture) AbortSlot(ctx context.Context, c p.GenerationClaim, cut p.OwnerCut) error {
	return f.owner.AbortSlot(ctx, c, cut)
}
func (f *slotFairFixture) ReadGenerationUIDs(ctx context.Context, c p.GenerationClaim, cut p.OwnerCut, uids []string, b *p.RecoveryResultBudget) (*p.OwnedSlotRoutes, error) {
	return f.owner.ReadGenerationUIDs(ctx, c, cut, uids, b)
}

func TestPresenceArchitectureGlobalPageYieldServesColdSeventeenthSlot(t *testing.T) {
	d := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	registry := online.NewRegistry(online.RegistryOptions{})
	f := &slotFairFixture{claims: make(map[uint16]p.GenerationClaim), directory: d, first: make(chan uint16, 16), releaseFirst: make(chan struct{}), releaseRest: make(chan struct{})}
	f.owner = p.NewOwnerGeneration(registry, 1, 11, f.ValidateSlotGeneration)
	queries := make([][]string, 17)
	targets := make([]p.RouteTarget, 17)
	for slot := 0; slot < 17; slot++ {
		targets[slot] = p.RouteTarget{HashSlot: uint16(slot), SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}
		d.BecomeAuthority(targets[slot])
		queries[slot] = make([]string, 257)
		for i := range queries[slot] {
			queries[slot][i] = fmt.Sprintf("offline-%d-%d", slot, i)
		}
		if slot == 16 {
			continue
		}
		for i := 1; i <= 4097; i++ {
			id := uint64(slot*4097 + i)
			route := online.OwnerRoute{UID: fmt.Sprintf("active-%d-%d", slot, i), HashSlot: uint16(slot), OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: id, SessionID: id, DeviceID: "device", LastActivityUnix: 100, ConnectedUnix: 100}
			if err := registry.RegisterPending(online.LocalSession{Route: route}); err != nil {
				t.Fatal(err)
			}
			if err := registry.MarkActive(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	slots := p.NewSlotRecovery(d, f, f)
	recovery := p.NewAuthorityRecovery(slots, slots)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	var wg sync.WaitGroup
	var firstOnce, restOnce sync.Once
	unblockFirst := func() { firstOnce.Do(func() { close(f.releaseFirst) }) }
	unblockRest := func() { restOnce.Do(func() { close(f.releaseRest) }) }
	t.Cleanup(func() {
		cancel()
		unblockFirst()
		unblockRest()
		joined := make(chan struct{})
		go func() { wg.Wait(); close(joined) }()
		waitArchitectureEvent(t, joined, "all fair lookup owners")
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if err := recovery.Close(cleanup); err != nil {
			t.Error(err)
		}
		if err := slots.Close(cleanup); err != nil {
			t.Error(err)
		}
		if s := slots.RecoveryResultBudget().Stats(); s.Rows != 0 || s.Bytes != 0 {
			t.Errorf("fairness cleanup retained result credits: %+v", s)
		}
	})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, _ = recovery.Endpoints(ctx, targets[i], queries[i]) }(i)
	}
	for i := 0; i < 16; i++ {
		select {
		case <-f.first:
		case <-ctx.Done():
			t.Fatalf("only %d dense slots reached first page: %v", i, ctx.Err())
		}
	}
	coldCtx, coldCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer coldCancel()
	coldDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := recovery.Endpoints(coldCtx, targets[16], queries[16])
		coldDone <- err
	}()
	for recovery.Stats().QueuedCalls == 0 {
		if err := coldCtx.Err(); err != nil {
			t.Fatalf("cold caller never entered original FIFO: %v", err)
		}
		runtime.Gosched()
	}
	unblockFirst()
	if err := <-coldDone; err != nil {
		t.Fatalf("cold slot was held behind dense continuation pages: %v", err)
	}
	// Continuation pages remain blocked here: success therefore required global
	// FIFO page yielding, not eventual completion of any dense slot.
}
