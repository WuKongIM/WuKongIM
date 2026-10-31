//go:build integration

package presence_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	p "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Failure inventory: independent slow owner Begin checks must not exhaust an
// earlier owner's budget before its first page. The original one-second owner
// and two-second physical deadlines, complete owner proof, cancellation join
// and staging credits remain mandatory. A slow single owner must still fail.
// The external owner port injects latency; real registries/cuts/pages/Directory
// and the public AuthorityRecovery Endpoints/Close paths determine the outcome.
type slotBeginFixture struct {
	*slotFairFixture
	owners        map[uint64]*p.OwnerGeneration
	ids           []uint64
	beginDelay    time.Duration
	pageDelay     time.Duration
	gate          <-chan struct{}
	entered       chan uint64
	late          bool
	failOwner     uint64
	failure       error
	failGate      <-chan struct{}
	canceled      chan struct{}
	claimGate     <-chan struct{}
	claimEntered  chan uint16
	controlActive atomic.Int32
	controlPeak   atomic.Int32
	active        atomic.Int32
	peak          atomic.Int32
	begun         atomic.Int32
	aborted       atomic.Int32
}

func (f *slotBeginFixture) ClaimSlotGeneration(ctx context.Context, target p.RouteTarget) (p.GenerationClaim, []uint64, error) {
	defer f.enterControl()()
	if f.claimEntered != nil {
		f.claimEntered <- target.HashSlot
	}
	if f.claimGate != nil && target.HashSlot < 44 {
		<-f.claimGate
		ctx = context.Background()
	}
	c, _, err := f.slotFairFixture.ClaimSlotGeneration(ctx, target)
	return c, append([]uint64(nil), f.ids...), err
}

func (f *slotBeginFixture) enterControl() func() {
	n := f.controlActive.Add(1)
	for old := f.controlPeak.Load(); n > old; old = f.controlPeak.Load() {
		if f.controlPeak.CompareAndSwap(old, n) {
			break
		}
	}
	return func() { f.controlActive.Add(-1) }
}

func (f *slotBeginFixture) BeginSlot(ctx context.Context, c p.GenerationClaim, id uint64) (p.OwnerCut, error) {
	defer f.enterControl()()
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for old := f.peak.Load(); n > old; old = f.peak.Load() {
		if f.peak.CompareAndSwap(old, n) {
			break
		}
	}
	if f.entered != nil {
		f.entered <- id
	}
	if id == f.failOwner && f.failGate != nil {
		select {
		case <-f.failGate:
			return p.OwnerCut{}, f.failure
		case <-ctx.Done():
			return p.OwnerCut{}, ctx.Err()
		}
	}
	if f.gate != nil {
		if f.late {
			select {
			case <-f.gate:
			case <-ctx.Done():
				if f.canceled != nil {
					f.canceled <- struct{}{}
				}
				<-f.gate
			}
		} else {
			select {
			case <-f.gate:
			case <-ctx.Done():
				return p.OwnerCut{}, ctx.Err()
			}
		}
	}
	if err := waitSlotBegin(ctx, f.beginDelay); err != nil {
		return p.OwnerCut{}, err
	}
	if id == f.failOwner {
		return p.OwnerCut{}, f.failure
	}
	if f.late {
		// An external provider can return a valid cut after ignoring cancellation.
		// Actual ownership must be joined and that cut must still be aborted.
		ctx = context.Background()
	}
	cut, err := f.owners[id].BeginSlot(ctx, c)
	if err == nil {
		f.begun.Add(1)
	}
	return cut, err
}

func waitSlotBegin(ctx context.Context, delay time.Duration) error {
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *slotBeginFixture) ReadSlotPage(ctx context.Context, req p.SlotPageRequest, budget *p.RecoveryResultBudget) (*p.OwnedSlotPage, error) {
	if err := waitSlotBegin(ctx, f.pageDelay); err != nil {
		return nil, err
	}
	return f.owners[req.Owner.NodeID].ReadSlotPage(ctx, req, budget)
}

func (f *slotBeginFixture) EndSlot(ctx context.Context, req p.SlotEndRequest) (p.SlotEnd, error) {
	return f.owners[req.Owner.NodeID].EndSlot(ctx, req)
}

func (f *slotBeginFixture) AbortSlot(ctx context.Context, c p.GenerationClaim, cut p.OwnerCut) error {
	err := f.owners[cut.NodeID].AbortSlot(ctx, c, cut)
	if err == nil {
		f.aborted.Add(1)
	}
	return err
}

func (f *slotBeginFixture) ReadGenerationUIDs(ctx context.Context, c p.GenerationClaim, cut p.OwnerCut, uids []string, budget *p.RecoveryResultBudget) (*p.OwnedSlotRoutes, error) {
	return f.owners[cut.NodeID].ReadGenerationUIDs(ctx, c, cut, uids, budget)
}

func newSlotBeginRecovery(t *testing.T, owners int) (*slotBeginFixture, *p.SlotRecovery, *p.AuthorityRecovery, p.RouteTarget) {
	t.Helper()
	d := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := p.RouteTarget{HashSlot: 15, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}
	d.BecomeAuthority(target)
	f := &slotBeginFixture{slotFairFixture: &slotFairFixture{claims: make(map[uint16]p.GenerationClaim), directory: d}, owners: make(map[uint64]*p.OwnerGeneration)}
	for id := uint64(1); id <= uint64(owners); id++ {
		f.ids = append(f.ids, id)
		f.owners[id] = p.NewOwnerGeneration(online.NewRegistry(online.RegistryOptions{}), id, 10+id, f.ValidateSlotGeneration)
	}
	slots := p.NewSlotRecovery(d, f, f)
	recovery := p.NewAuthorityRecovery(slots, slots)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := recovery.Close(ctx); err != nil {
			t.Error(err)
		}
		if err := slots.Close(ctx); err != nil {
			t.Error(err)
		}
		if s := slots.RecoveryResultBudget().Stats(); s.Rows != 0 || s.Bytes != 0 {
			t.Errorf("owner Begin retained result credits: %+v", s)
		}
	})
	return f, slots, recovery, target
}

func TestPresenceIndependentOwnerBeginsFitOriginalBudgets(t *testing.T) {
	f, _, recovery, target := newSlotBeginRecovery(t, 3)
	f.beginDelay = 375 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := recovery.Endpoints(ctx, target, []string{"offline"}); err != nil {
		t.Fatalf("independent owner checks aged an earlier owner's original budget: %v", err)
	}
	if f.begun.Load() != 3 {
		t.Fatal("success did not include every obligated owner")
	}
}

func TestPresenceOwnerBudgetStillSpansBeginAndPages(t *testing.T) {
	f, _, recovery, target := newSlotBeginRecovery(t, 1)
	f.beginDelay, f.pageDelay = 700*time.Millisecond, 400*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := recovery.Endpoints(ctx, target, []string{"offline"}); err == nil {
		t.Fatal("one owner exceeded its unchanged one-second budget")
	}
	if _, err := f.directory.EndpointsByUID(target, "offline"); err == nil {
		t.Fatal("failed owner was published as a complete offline proof")
	}
}

func TestPresenceOwnerBeginCancellationJoinsLateCutsWithinFixedWidth(t *testing.T) {
	f, slots, recovery, target := newSlotBeginRecovery(t, 8)
	gate := make(chan struct{})
	f.gate, f.late, f.entered = gate, true, make(chan uint64, 8)
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := recovery.Endpoints(ctx, target, []string{"offline"}); done <- err }()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("caller not joined")
		}
	})
	for i := 0; i < 4; i++ {
		select {
		case <-f.entered:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("only %d independent owners admitted before the fixed width", i)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation: %v", err)
		}
		// Keep one buffered outcome for the unconditional cleanup join.
		done <- err
	case <-time.After(time.Second):
		t.Fatal("caller cancellation waited for a late provider")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer closeCancel()
	if err := recovery.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close lost late provider ownership: %v", err)
	}
	if s := slots.RecoveryResultBudget().Stats(); s.Bytes == 0 {
		t.Fatal("staging credits released while Begin providers still held ownership")
	}
	if f.peak.Load() > 4 || f.begun.Load() != 0 {
		t.Fatal("unbounded provider admission or premature gate release")
	}
	release()
	joined, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := recovery.Close(joined); err != nil {
		t.Fatal(err)
	}
	if err := slots.Close(joined); err != nil {
		t.Fatal(err)
	}
	if f.active.Load() != 0 || f.begun.Load() != 4 || f.aborted.Load() != 4 {
		t.Fatalf("late cuts not joined/aborted: active=%d begun=%d aborted=%d", f.active.Load(), f.begun.Load(), f.aborted.Load())
	}
	if _, err := f.directory.EndpointsByUID(target, "offline"); err == nil {
		t.Fatal("canceled incomplete generation published")
	}
}

func TestPresenceOwnerBeginsShareNodeWideControlCapacity(t *testing.T) {
	f, slots, recovery, target := newSlotBeginRecovery(t, 8)
	gate := make(chan struct{})
	f.gate, f.late, f.entered = gate, true, make(chan uint64, 128)
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	ctx, cancel := context.WithCancel(context.Background())
	var callers sync.WaitGroup
	t.Cleanup(func() { cancel(); release(); callers.Wait() })
	for i := 0; i < 16; i++ {
		q := target
		q.HashSlot = uint16(32 + i)
		f.directory.BecomeAuthority(q)
		callers.Add(1)
		go func() { defer callers.Done(); _, _ = recovery.Endpoints(ctx, q, []string{"offline"}) }()
	}
	for i := 0; i < 16; i++ {
		select {
		case <-f.entered:
		case <-time.After(time.Second):
			t.Fatalf("only %d node-wide control providers entered", i)
		}
	}
	select {
	case <-f.entered:
		t.Fatal("more than sixteen actual control providers entered across scans")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	release()
	callers.Wait()
	joined, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := recovery.Close(joined); err != nil {
		t.Fatal(err)
	}
	if err := slots.Close(joined); err != nil {
		t.Fatal(err)
	}
	if f.peak.Load() > 16 || f.active.Load() != 0 {
		t.Fatalf("node-wide provider ownership exceeded bound or did not join: peak=%d active=%d", f.peak.Load(), f.active.Load())
	}
}

func TestPresenceOwnerBeginFailureRetainsLateSiblingCleanup(t *testing.T) {
	f, slots, recovery, target := newSlotBeginRecovery(t, 8)
	gate, fail := make(chan struct{}), make(chan struct{})
	f.gate, f.failGate, f.late, f.entered = gate, fail, true, make(chan uint64, 8)
	f.failOwner, f.failure = 1, errors.New("owner begin failed")
	f.canceled = make(chan struct{}, 3)
	var once, failOnce sync.Once
	release := func() { once.Do(func() { close(gate) }); failOnce.Do(func() { close(fail) }) }
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := recovery.Endpoints(ctx, target, []string{"offline"}); done <- err }()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("failed caller not joined")
		}
	})
	for i := 0; i < 4; i++ {
		select {
		case <-f.entered:
		case <-time.After(time.Second):
			t.Fatal("independent siblings never entered")
		}
	}
	failOnce.Do(func() { close(fail) })
	for i := 0; i < 3; i++ {
		select {
		case <-f.canceled:
		case <-time.After(time.Second):
			t.Fatal("failed provider did not cancel an admitted sibling")
		}
	}
	select {
	case <-f.entered:
		t.Fatal("queued owner started while every admitted sibling remained held")
	default:
	}
	if s := slots.RecoveryResultBudget().Stats(); s.Bytes == 0 {
		t.Fatal("failed round discarded live sibling ownership")
	}
	release()
	err := <-done
	done <- err
	if err == nil {
		t.Fatal("failed owner yielded a complete proof")
	}
	joined, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := recovery.Close(joined); err != nil {
		t.Fatal(err)
	}
	if err := slots.Close(joined); err != nil {
		t.Fatal(err)
	}
	if f.begun.Load() != 3 || f.aborted.Load() != 3 || f.active.Load() != 0 {
		t.Fatalf("late siblings were lost: begun=%d aborted=%d active=%d", f.begun.Load(), f.aborted.Load(), f.active.Load())
	}
	if _, err := f.directory.EndpointsByUID(target, "offline"); err == nil {
		t.Fatal("partial proof published")
	}
}

func TestPresenceClaimsAndBeginsShareActualControlCapacity(t *testing.T) {
	f, slots, recovery, target := newSlotBeginRecovery(t, 8)
	claims, begins := make(chan struct{}), make(chan struct{})
	f.claimGate, f.gate, f.late = claims, begins, true
	f.claimEntered, f.entered = make(chan uint16, 16), make(chan uint64, 64)
	var once sync.Once
	release := func() { once.Do(func() { close(claims); close(begins) }) }
	ctx, cancel := context.WithCancel(context.Background())
	var callers sync.WaitGroup
	t.Cleanup(func() { cancel(); release(); callers.Wait() })
	start := func(hash uint16) {
		q := target
		q.HashSlot = hash
		f.directory.BecomeAuthority(q)
		uids := make([]string, 257) // Prevent unrelated calls merging into one owner batch.
		for i := range uids {
			uids[i] = fmt.Sprintf("offline-%d", i)
		}
		callers.Add(1)
		go func() { defer callers.Done(); _, _ = recovery.Endpoints(ctx, q, uids) }()
	}
	for i := uint16(32); i < 44; i++ {
		start(i)
	}
	for i := 0; i < 12; i++ {
		select {
		case <-f.claimEntered:
		case <-time.After(time.Second):
			t.Fatal("held Claims did not enter")
		}
	}
	for i := uint16(44); i < 48; i++ {
		start(i)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-f.entered:
		case <-time.After(time.Second):
			t.Fatal("Begins did not use remaining control capacity")
		}
	}
	select {
	case <-f.entered:
		t.Fatal("Claim and Begin exceeded combined sixteen-provider capacity")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	closeCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if err := recovery.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mixed late providers lost cleanup ownership: %v", err)
	}
	if slots.RecoveryResultBudget().Stats().Bytes == 0 {
		t.Fatal("mixed late providers lost staging credits")
	}
	release()
	callers.Wait()
	joined, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	if err := recovery.Close(joined); err != nil {
		t.Fatal(err)
	}
	if err := slots.Close(joined); err != nil {
		t.Fatal(err)
	}
	if f.controlPeak.Load() > 16 || f.controlActive.Load() != 0 {
		t.Fatalf("shared control cap/join: peak=%d active=%d", f.controlPeak.Load(), f.controlActive.Load())
	}
}
