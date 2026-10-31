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
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

const architectureUID = "architecture-user"

// This fixture crosses public interfaces with real state owners. Its authority
// adapter only delays a completed response, as a transport may; it never
// manufactures Register, Commit, or owner-recovery results.
type architectureFixture struct {
	directory   *authority.Directory
	registry    *online.Registry
	recovery    *presence.AuthorityRecovery
	slots       *presence.SlotRecovery
	owner       *presence.OwnerGeneration
	claim       presence.GenerationClaim
	cut         presence.OwnerCut
	generation  uint64
	ownerEpoch  uint64
	ownerNode   uint64
	ownerBegins atomic.Uint64
	ownerPages  atomic.Uint64
	app         *presence.App
	targetMu    sync.RWMutex
	target      presence.RouteTarget
	seq         atomic.Uint64
	phase       string
	entered     chan struct{}
	release     chan struct{}
	released    sync.Once
	paused      sync.Once
	// Dropping this notification models a lost best-effort unregister transport.
	dropUnregister bool
}

type architectureOwnerReader struct{ reader *presence.OwnerRecovery }

type architectureOwnerReadFunc func(context.Context, presence.RouteTarget, []string) ([]presence.Route, error)

func (fn architectureOwnerReadFunc) RecoverRoutes(ctx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	return fn(ctx, target, uids)
}

func (r architectureOwnerReader) RecoverRoutes(ctx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	result, err := r.reader.ReadOwnerRoutes(ctx, target, uids)
	return result.Routes, err
}

func newArchitectureFixture(t *testing.T, phase string) *architectureFixture {
	t.Helper()
	f := &architectureFixture{
		directory:  authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true}),
		registry:   online.NewRegistry(online.RegistryOptions{}),
		target:     presence.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9},
		phase:      phase,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
		ownerEpoch: 2,
		ownerNode:  1,
	}
	f.directory.BecomeAuthority(f.currentTarget())
	f.owner = presence.NewOwnerGeneration(f.registry, 1, 11, f.ValidateSlotGeneration)
	f.slots = presence.NewSlotRecovery(f.directory, f, f)
	f.recovery = presence.NewAuthorityRecovery(f.slots, f.slots)
	f.app = presence.New(presence.Options{
		Local: f.registry, Authority: f, OwnerActions: f, OwnerNodeID: 1, OwnerBootID: 11,
		HashSlot: func(string) (uint16, error) { return 37, nil },
		OwnerSeq: func(string) uint64 { return f.seq.Add(1) },
	})
	t.Cleanup(func() {
		f.unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := f.recovery.Close(ctx); err != nil {
			t.Errorf("recovery cleanup did not join: %v", err)
		}
		if err := f.slots.Close(ctx); err != nil {
			t.Errorf("slot recovery cleanup: %v", err)
		}
		stats := f.recovery.Stats()
		if stats.QueuedCalls != 0 || stats.ActiveBatches != 0 || stats.ResultCalls != 0 || stats.Members != 0 || stats.FencedUIDs != 0 || stats.OwnedUIDBytes != 0 || stats.OwnedResultRows != 0 || stats.OwnedResultBytes != 0 {
			t.Errorf("recovery retained ownership after Close: %+v", stats)
		}
		for _, session := range f.registry.LocalSessionsByUID(architectureUID) {
			f.registry.MarkClosingAndUnregister(session.Route.SessionID)
		}
		f.directory.LoseAuthority(37)
		if got := f.registry.Snapshot(); got.Active != 0 || got.Pending != 0 || got.TouchedDirty != 0 {
			t.Errorf("registry retained sessions after cleanup: %+v", got)
		}
	})
	return f
}

func (f *architectureFixture) currentTarget() presence.RouteTarget {
	f.targetMu.RLock()
	defer f.targetMu.RUnlock()
	return f.target
}

func (f *architectureFixture) unblock() { f.released.Do(func() { close(f.release) }) }

func (f *architectureFixture) pauseResponse(ctx context.Context, phase string) error {
	if f.phase != phase {
		return nil
	}
	f.paused.Do(func() { close(f.entered) })
	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *architectureFixture) RegisterRoute(ctx context.Context, route presence.Route) (presence.RegisterResult, error) {
	target := f.currentTarget()
	result, err := f.slots.RegisterRoute(ctx, target, route)
	if err == nil && route.SessionID == 2 && result.PendingToken == "" {
		err = f.pauseResponse(ctx, "register")
	}
	return result, err
}

// New public mutation entrypoints must reject an old claim even when its target
// is unchanged, and an old operation must not discard a Full activation receipt.
func TestPresenceArchitectureFullEntryRejectsStaleMutationAndOldPeer(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	old, _, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil {
		t.Fatal(err)
	}
	f.directory.LoseAuthority(37)
	f.directory.BecomeAuthority(f.currentTarget())
	if _, err := f.recovery.Endpoints(ctx, f.currentTarget(), []string{architectureUID}); err != nil {
		t.Fatal(err)
	}
	route := presence.Route{UID: architectureUID, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: 1, SessionID: 2, LastSeenUnix: 200}
	if err := f.slots.TouchRoutes(ctx, old, []presence.Route{route}); err == nil {
		t.Fatal("old generation touch accepted")
	}
	if err := f.slots.UnregisterRoute(ctx, old, route.Identity(), route.OwnerSeq); err == nil {
		t.Fatal("old generation mutation accepted")
	}
	if _, err := f.directory.RegisterRoute(old.Target, route); err == nil {
		t.Fatal("old registration discarded Full receipt")
	}
	if err := f.directory.TouchRoutes(old.Target, []presence.Route{route}); err == nil {
		t.Fatal("old touch recreated Full row")
	}
}

func (f *architectureFixture) CommitRoute(ctx context.Context, token presence.PendingRouteToken) error {
	_, err := f.CommitRouteWithReceipt(ctx, token)
	return err
}

func (f *architectureFixture) RequiresActivationReceipt() bool { return true }
func (f *architectureFixture) CommitRouteWithReceipt(ctx context.Context, token presence.PendingRouteToken) (presence.ActivationReceipt, error) {
	claim, _, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil {
		return presence.ActivationReceipt{}, err
	}
	receipt, err := f.slots.CommitRouteWithReceipt(ctx, claim, token)
	if err != nil {
		return receipt, err
	}
	return receipt, f.pauseResponse(ctx, "commit")
}

func (f *architectureFixture) AbortRoute(_ context.Context, token presence.PendingRouteToken) error {
	claim, _, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil {
		return err
	}
	return f.directory.AbortRouteWithClaim(claim, token)
}

func (f *architectureFixture) EnqueueUnregister(_ context.Context, identity presence.RouteIdentity, seq uint64) {
	if f.dropUnregister {
		return
	}
	_ = f.directory.UnregisterRoute(f.currentTarget(), identity, seq)
}

func (f *architectureFixture) EndpointsByUID(ctx context.Context, uid string) ([]presence.Route, error) {
	return f.recovery.Endpoints(ctx, f.currentTarget(), []string{uid})
}

func (f *architectureFixture) ApplyRouteAction(ctx context.Context, action presence.RouteAction) error {
	return f.app.Deactivate(ctx, presence.DeactivateCommand{UID: action.UID, SessionID: action.SessionID})
}

func (f *architectureFixture) ClaimSlotGeneration(ctx context.Context, target presence.RouteTarget) (presence.GenerationClaim, []uint64, error) {
	if err := ctx.Err(); err != nil {
		return presence.GenerationClaim{}, nil, err
	}
	f.targetMu.Lock()
	defer f.targetMu.Unlock()
	if target != f.target {
		return presence.GenerationClaim{}, nil, authority.ErrNotLeader
	}
	f.generation++
	f.claim = presence.GenerationClaim{Target: target, OwnerSetEpoch: f.ownerEpoch, ServingBoot: 22, Generation: f.generation, PreviousGeneration: f.generation - 1, AppliedRaftIndex: 100 + f.generation}
	return f.claim, []uint64{f.ownerNode}, nil
}
func (f *architectureFixture) ValidateSlotGeneration(ctx context.Context, claim presence.GenerationClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.targetMu.RLock()
	defer f.targetMu.RUnlock()
	if !claim.Equal(f.claim) || claim.Target != f.target {
		return authority.ErrStaleGeneration
	}
	return nil
}
func (f *architectureFixture) BeginSlot(ctx context.Context, claim presence.GenerationClaim, node uint64) (presence.OwnerCut, error) {
	f.ownerBegins.Add(1)
	if node != f.ownerNode {
		return presence.OwnerCut{}, authority.ErrStaleGeneration
	}
	cut, err := f.owner.BeginSlot(ctx, claim)
	if err == nil {
		f.targetMu.Lock()
		f.cut = cut
		f.targetMu.Unlock()
	}
	return cut, err
}
func (f *architectureFixture) ReadSlotPage(ctx context.Context, req presence.SlotPageRequest, b *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	f.ownerPages.Add(1)
	return f.owner.ReadSlotPage(ctx, req, b)
}
func (f *architectureFixture) EndSlot(ctx context.Context, req presence.SlotEndRequest) (presence.SlotEnd, error) {
	return f.owner.EndSlot(ctx, req)
}
func (f *architectureFixture) AbortSlot(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut) error {
	return f.owner.AbortSlot(ctx, claim, cut)
}
func (f *architectureFixture) ReadGenerationUIDs(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut, uids []string, b *presence.RecoveryResultBudget) (*presence.OwnedSlotRoutes, error) {
	return f.owner.ReadGenerationUIDs(ctx, claim, cut, uids, b)
}

func architectureCommand(session uint64) presence.ActivateCommand {
	return presence.ActivateCommand{UID: architectureUID, DeviceID: "device", DeviceFlag: 1, DeviceLevel: 1, SessionID: session, ConnectedUnix: 100}
}

func startArchitectureActivation(t *testing.T, f *architectureFixture) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- f.app.Activate(ctx, architectureCommand(2))
	}()
	t.Cleanup(func() {
		f.unblock()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Activate goroutine did not join")
		}
	})
	return result
}

func waitArchitectureEvent(t *testing.T, event <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not reach its explicit barrier", name)
	}
}

func requireArchitectureActivationVisibleOrRejected(t *testing.T, f *architectureFixture, result <-chan error) {
	t.Helper()
	var activationErr error
	select {
	case activationErr = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("Activate did not finish after response release")
	}
	if errors.Is(activationErr, context.Canceled) || errors.Is(activationErr, context.DeadlineExceeded) {
		t.Fatalf("test timeout/cancellation is not a safe generation rejection: %v", activationErr)
	}
	session, exists := f.registry.LocalSession(2)
	if activationErr != nil {
		if exists && session.State == online.RouteStateActive {
			t.Fatalf("rejected Activate left an active owner: %v", activationErr)
		}
		return
	}
	if !exists || session.State != online.RouteStateActive {
		t.Fatal("successful Activate did not create an active owner session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	routes, err := f.app.EndpointsByUID(ctx, architectureUID)
	if err != nil {
		// An unproven current generation may fail closed; it must not claim offline.
		if errors.Is(err, authority.ErrRouteNotReady) || errors.Is(err, authority.ErrNotLeader) {
			return
		}
		t.Fatalf("unexpected lookup failure after Activate: %v", err)
	}
	for _, route := range routes {
		if route.OwnerNodeID == 1 && route.OwnerBootID == 11 && route.SessionID == 2 {
			return
		}
	}
	t.Fatalf("successful Activate is invisible to a successful current-authority read: active_session=%d routes=%+v", session.Route.SessionID, routes)
}

func TestPresenceArchitectureLateRegisterAcrossAuthorityRebuild(t *testing.T) {
	runArchitectureLateSuccess(t, "register", func(target presence.RouteTarget) presence.RouteTarget { return target })
}

func TestPresenceArchitectureLateCommitAcrossAuthorityRebuild(t *testing.T) {
	runArchitectureLateSuccess(t, "commit", func(target presence.RouteTarget) presence.RouteTarget { return target })
}

func TestPresenceArchitectureLateSuccessAcrossMigration(t *testing.T) {
	for _, phase := range []string{"register", "commit"} {
		t.Run(phase, func(t *testing.T) {
			for _, move := range []struct {
				name string
				next func(presence.RouteTarget) presence.RouteTarget
			}{
				{"leader_term", func(target presence.RouteTarget) presence.RouteTarget { target.LeaderTerm++; return target }},
				{"placement_epoch", func(target presence.RouteTarget) presence.RouteTarget {
					target.SlotID++
					target.ConfigEpoch++
					return target
				}},
			} {
				t.Run(move.name, func(t *testing.T) { runArchitectureLateSuccess(t, phase, move.next) })
			}
		})
	}
}

func runArchitectureLateSuccess(t *testing.T, phase string, next func(presence.RouteTarget) presence.RouteTarget) {
	t.Helper()
	f := newArchitectureFixture(t, phase)
	if phase == "commit" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := f.app.Activate(ctx, architectureCommand(1)); err != nil {
			t.Fatalf("activate real conflicting session: %v", err)
		}
	}
	result := startArchitectureActivation(t, f)
	waitArchitectureEvent(t, f.entered, "successful "+phase+" response")
	if session, ok := f.registry.LocalSession(2); !ok || session.State != online.RouteStatePending {
		t.Fatal("response barrier did not leave the real owner session pending")
	}
	before := f.currentTarget()
	committed, err := f.directory.EndpointsByUID(before, architectureUID)
	if err != nil || len(committed) != 1 || committed[0].SessionID != 2 {
		t.Fatalf("barrier must follow actual authority success: routes=%+v err=%v", committed, err)
	}
	if phase == "commit" {
		if _, exists := f.registry.LocalSession(1); exists {
			t.Fatal("real conflict action did not remove the previous owner session")
		}
	}
	target := next(before)
	f.directory.LoseAuthority(before.HashSlot)
	f.targetMu.Lock()
	f.target = target
	f.targetMu.Unlock()
	f.directory.BecomeAuthority(target)
	if target != before {
		if _, err := f.directory.RecoveryUIDs(before, nil); !errors.Is(err, authority.ErrNotLeader) {
			t.Fatalf("old target survived the actual authority change: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	routes, err := f.recovery.Endpoints(ctx, target, []string{architectureUID})
	if err != nil || len(routes) != 0 {
		t.Fatalf("new authority recovery while owner pending: routes=%+v err=%v", routes, err)
	}
	f.unblock()
	requireArchitectureActivationVisibleOrRejected(t, f, result)
}

func TestPresenceArchitectureTTLWithNewOwnerActivityDoesNotProveOffline(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	if route, ok := f.registry.MarkTouched(2, 200); !ok || route.LastActivityUnix != 200 {
		t.Fatal("owner did not record the newer actual activity")
	}
	// Do not flush the dirty touch. The authority still only knows activity100.
	if expired := f.directory.ExpireRoutesDetailed(time.Unix(201, 0), 50*time.Second); expired.Expired != 1 {
		t.Fatalf("the actual old authority route did not expire: %+v", expired)
	}
	routes, err := f.app.EndpointsByUID(ctx, architectureUID)
	if errors.Is(err, authority.ErrRouteNotReady) {
		return // An explicit unresolved state is safe; successful absence is not.
	}
	if err != nil || len(routes) != 1 || routes[0].SessionID != 2 || routes[0].LastSeenUnix != 200 {
		t.Fatalf("newer owner activity lost behind expired authority proof: routes=%+v err=%v", routes, err)
	}
}

func TestPresenceArchitectureColdPendingLoginDoesNotWaitForItself(t *testing.T) {
	f := newArchitectureFixture(t, "")
	result := startArchitectureActivation(t, f)
	requireArchitectureActivationVisibleOrRejected(t, f, result)
	if got := f.registry.Snapshot(); got.Active != 1 || got.Pending != 0 {
		t.Fatalf("healthy cold activation must finish, not reject or wait on its pending session: %+v", got)
	}
}

func TestPresenceArchitectureClosePendingBeforeResponseCannotActivate(t *testing.T) {
	f := newArchitectureFixture(t, "register")
	result := startArchitectureActivation(t, f)
	waitArchitectureEvent(t, f.entered, "successful Register response")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.app.Deactivate(ctx, presence.DeactivateCommand{UID: architectureUID, SessionID: 2}); err != nil {
		t.Fatal(err)
	}
	f.unblock()
	select {
	case err := <-result:
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("closed pending session was not rejected at local activation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed pending activation did not join")
	}
	if _, exists := f.registry.LocalSession(2); exists {
		t.Fatal("closed pending session remains in owner registry")
	}
	routes, err := f.app.EndpointsByUID(ctx, architectureUID)
	if err != nil || len(routes) != 0 {
		t.Fatalf("closed pending route remains visible: routes=%+v err=%v", routes, err)
	}
}

func TestPresenceArchitectureForwardedTouchPreservesActualActivity(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.registry.MarkTouched(2, 200); !ok {
		t.Fatal("active owner touch rejected")
	}
	owner := presence.NewOwnerRecovery(f.registry, 1, 11, func(target presence.RouteTarget) error {
		_, err := f.directory.RecoveryUIDs(target, nil)
		return err
	})
	snapshot, err := owner.ReadOwnerRoutes(ctx, f.currentTarget(), []string{architectureUID})
	if err != nil || len(snapshot.Routes) != 1 {
		t.Fatalf("read actual newer owner route: %+v %v", snapshot, err)
	}
	claim, _, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.slots.TouchRoutes(ctx, claim, snapshot.Routes); err != nil {
		t.Fatal(err)
	}
	if expired := f.directory.ExpireRoutesDetailed(time.Unix(201, 0), 50*time.Second); expired.Expired != 0 {
		t.Fatalf("forwarded new activity unexpectedly expired: %+v", expired)
	}
	routes, err := f.app.EndpointsByUID(ctx, architectureUID)
	if err != nil || len(routes) != 1 || routes[0].LastSeenUnix != 200 {
		t.Fatalf("current owner activity not retained: %+v %v", routes, err)
	}
}

func TestPresenceArchitectureLostUnregisterDoesNotRenewTTL(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	f.dropUnregister = true
	if err := f.app.Deactivate(ctx, presence.DeactivateCommand{UID: architectureUID, SessionID: 2}); err != nil {
		t.Fatal(err)
	}
	if _, exists := f.registry.LocalSession(2); exists {
		t.Fatal("actual Close did not remove the owner session")
	}
	routes, err := f.directory.EndpointsByUID(f.currentTarget(), architectureUID)
	if err != nil || len(routes) != 1 || routes[0].LastSeenUnix != 100 {
		t.Fatalf("lost notification must leave only the original stale positive: %+v %v", routes, err)
	}
	if got := f.directory.ExpireRoutesDetailed(time.Unix(151, 0), 50*time.Second); got.Expired != 1 {
		t.Fatalf("original LastSeen+TTL did not remove the stale route: %+v", got)
	}
	routes, err = f.app.EndpointsByUID(ctx, architectureUID)
	if len(routes) != 0 || (err != nil && !errors.Is(err, authority.ErrRouteNotReady)) {
		t.Fatalf("expired stale route was revived: %+v %v", routes, err)
	}
}

func TestPresenceArchitectureResultCapacityRefusalRetainsNoPartialCredit(t *testing.T) {
	budget := presence.NewRecoveryResultBudget(2, 1024)
	lease, err := budget.TryReserve(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Release)
	before := budget.Stats()
	if err := lease.TryGrow(2, 1); !errors.Is(err, presence.ErrRecoveryResultBudget) {
		t.Fatalf("row-capacity overflow accepted: %v", err)
	}
	if err := lease.TryGrow(0, 1024); !errors.Is(err, presence.ErrRecoveryResultBudget) {
		t.Fatalf("byte-capacity overflow accepted: %v", err)
	}
	after := budget.Stats()
	if after.Rows != before.Rows || after.Bytes != before.Bytes || after.HighWaterRows != before.HighWaterRows || after.HighWaterBytes != before.HighWaterBytes || after.Rejected != before.Rejected+2 {
		t.Fatalf("refusal retained a partial reservation: before=%+v after=%+v", before, after)
	}
	lease.Release()
	lease.Release()
	if final := budget.Stats(); final.Rows != 0 || final.Bytes != 0 {
		t.Fatalf("release did not return all credits exactly once: %+v", final)
	}
}

func TestPresenceArchitectureCanceledRecoveryKeepsOwnershipUntilReturn(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	f.directory.LoseAuthority(37)
	f.directory.BecomeAuthority(f.currentTarget())
	owner := presence.NewOwnerRecovery(f.registry, 1, 11, func(target presence.RouteTarget) error {
		_, err := f.directory.RecoveryUIDs(target, nil)
		return err
	})
	entered, providerCanceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	// A real owner read has completed, but its transport has not returned yet.
	// Even cancellation cannot release the response's actual owner prematurely.
	f.recovery = presence.NewAuthorityRecovery(f.directory, architectureOwnerReadFunc(func(readCtx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
		snapshot, err := owner.ReadOwnerRoutes(readCtx, target, uids)
		close(entered)
		<-readCtx.Done()
		close(providerCanceled)
		<-release
		return snapshot.Routes, err
	}))
	callCtx, callCancel := context.WithCancel(context.Background())
	result, joined := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(joined)
		_, err := f.app.EndpointsByUID(callCtx, architectureUID)
		result <- err
	}()
	t.Cleanup(func() {
		callCancel()
		unblock()
		waitArchitectureEvent(t, joined, "canceled lookup join")
	})
	waitArchitectureEvent(t, entered, "real owner read")
	callCancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller did not keep its original context: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller remained blocked on provider return")
	}
	waitArchitectureEvent(t, providerCanceled, "all-member execution cancellation")
	stats := f.recovery.Stats()
	if stats.ActiveBatches != 1 || stats.ResultCalls != 1 || stats.OwnedUIDBytes == 0 {
		t.Fatalf("caller cancellation released real provider ownership: %+v", stats)
	}
	closedCtx, stopWaiting := context.WithCancel(context.Background())
	stopWaiting()
	if err := f.recovery.Close(closedCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close claimed join before real provider returned: %v", err)
	}
	unblock()
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer joinCancel()
	if err := f.recovery.Close(joinCtx); err != nil {
		t.Fatalf("second Close failed to join actual ownership: %v", err)
	}
	if _, err := f.directory.EndpointsByUID(f.currentTarget(), architectureUID); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("late unowned response published a completed proof: %v", err)
	}
}

func TestPresenceArchitecturePartialOwnerFailureCannotPublishProof(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	f.directory.LoseAuthority(37)
	f.directory.BecomeAuthority(f.currentTarget())
	owner := presence.NewOwnerRecovery(f.registry, 1, 11, func(target presence.RouteTarget) error {
		_, err := f.directory.RecoveryUIDs(target, nil)
		return err
	})
	f.recovery = presence.NewAuthorityRecovery(f.directory, architectureOwnerReadFunc(func(readCtx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
		snapshot, err := owner.ReadOwnerRoutes(readCtx, target, uids)
		if err != nil {
			return snapshot.Routes, err
		}
		// The first real owner replied; another required owner did not.
		return snapshot.Routes, authority.ErrRouteNotReady
	}))
	if _, err := f.app.EndpointsByUID(ctx, architectureUID); err == nil {
		t.Fatal("incomplete owner round was returned as success")
	}
	if _, err := f.directory.EndpointsByUID(f.currentTarget(), architectureUID); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatalf("partial owner response installed a completed proof: %v", err)
	}
}

// The complete-slot certificate must remove offline working-set churn without
// making UID count an unbounded persistent cache or changing caller limits.
func TestPresenceArchitectureCompleteSlotDoesNotRecoverOfflineWorkingSet(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for start := 0; start < 3072; start += 512 {
		uids := make([]string, 512)
		for i := range uids {
			uids[i] = fmt.Sprintf("offline-%d", start+i)
		}
		rows, err := f.recovery.Endpoints(ctx, f.currentTarget(), uids)
		if err != nil || len(rows) != 0 {
			t.Fatalf("offline slot lookup: %d %v %v", start, rows, err)
		}
	}
	if f.ownerBegins.Load() != 1 || f.ownerPages.Load() != 1 {
		t.Fatalf("offline UID working set caused repeat recovery: Begin=%d pages=%d", f.ownerBegins.Load(), f.ownerPages.Load())
	}
}

// A completed vector from a previous owner boot must not permanently bypass
// shared reconstruction. This simulates owner-process replacement while the
// authority and physical placement remain unchanged; the source supplies a new
// committed membership/generation only when reconstruction is requested.
func TestPresenceArchitectureOwnerRestartRebuildsCompleteSlot(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	old, complete, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil || !complete {
		t.Fatalf("original generation was not complete: %+v %t %v", old, complete, err)
	}
	// Real process death removes local sessions without an unregister delivery.
	oldApp := f.app
	oldRegistry := f.registry
	f.registry.MarkClosingAndUnregister(2)
	f.registry = online.NewRegistry(online.RegistryOptions{})
	f.owner = presence.NewOwnerGeneration(f.registry, 1, 12, f.ValidateSlotGeneration)
	f.targetMu.Lock()
	f.ownerEpoch++
	f.targetMu.Unlock()
	f.app = presence.New(presence.Options{
		Local: f.registry, Authority: f, OwnerActions: f, OwnerNodeID: 1, OwnerBootID: 12,
		HashSlot: func(string) (uint16, error) { return 37, nil },
		OwnerSeq: func(string) uint64 { return f.seq.Add(1) },
	})
	if err := f.app.Activate(ctx, architectureCommand(3)); err != nil {
		t.Fatalf("new owner boot remained behind old Complete vector: %v", err)
	}
	claim, complete, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil || !complete || claim.Generation <= old.Generation || claim.OwnerSetEpoch <= old.OwnerSetEpoch {
		t.Fatalf("new owner did not rebuild committed completeness: %+v %t %v", claim, complete, err)
	}
	rows, err := f.app.EndpointsByUID(ctx, architectureUID)
	if err != nil || len(rows) != 1 || rows[0].SessionID != 3 || rows[0].OwnerBootID != 12 {
		t.Fatalf("new activation not visible or old boot retained: %+v %v", rows, err)
	}
	if f.ownerBegins.Load() != 2 {
		t.Fatalf("boot repair should share one new reconstruction, got %d Begins", f.ownerBegins.Load())
	}
	// Boot IDs have equality, not ordering. A delayed old request may cause one
	// conservative invalidation, but after authenticated reconstruction it cannot
	// revoke the new certificate again or promote its obsolete local registry.
	if err := oldApp.Activate(ctx, architectureCommand(4)); err == nil {
		t.Fatal("delayed old boot activated against the new actual owner")
	}
	latest, complete, err := f.directory.CurrentGeneration(f.currentTarget())
	if err != nil || !complete || latest.Generation != claim.Generation+1 || f.ownerBegins.Load() != 3 {
		t.Fatalf("retry revoked fresh authenticated certificate or rebuilt repeatedly: claim=%+v complete=%t err=%v Begins=%d", latest, complete, err, f.ownerBegins.Load())
	}
	if s := oldRegistry.Snapshot(); s.Pending != 0 || s.Active != 0 {
		t.Fatalf("old boot retained a session: %+v", s)
	}
	rows, err = f.app.EndpointsByUID(ctx, architectureUID)
	if err != nil || len(rows) != 1 || rows[0].SessionID != 3 || rows[0].OwnerBootID != 12 {
		t.Fatalf("late obsolete activation damaged current routes: %+v %v", rows, err)
	}
}

// Full-slot scans may span many pages, but the original grouped lookup still
// has one 4096-route result boundary; siblings retain their aligned outcomes.
func TestPresenceArchitectureGroupedResultRetainsOriginalRouteBound(t *testing.T) {
	f := newArchitectureFixture(t, "")
	uids := make([]string, 500)
	for i := range uids {
		uids[i] = fmt.Sprintf("result-%d", i)
	}
	for n := 1; n <= 5000; n++ {
		// Keep all 5000 routes and stay within the original 512-UID call bound.
		// Ten devices per UID avoids making this result-bound test depend on the
		// unrelated quadratic conflict scan for thousands of devices of one UID.
		uid := uids[(n-1)/10]
		route := online.OwnerRoute{UID: uid, HashSlot: 37, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: uint64(n), SessionID: uint64(n), DeviceID: fmt.Sprintf("device-%d", n), LastActivityUnix: 100}
		if err := f.registry.RegisterPending(online.LocalSession{Route: route}); err != nil {
			t.Fatal(err)
		}
		if err := f.registry.MarkActive(uint64(n)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for n := 1; n <= 5000; n++ {
			f.registry.MarkClosingAndUnregister(uint64(n))
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	groups := []presence.EndpointLookupGroup{
		{Target: f.currentTarget(), UIDs: uids[:250]},
		{Target: f.currentTarget(), UIDs: uids[250:]},
	}
	results := f.recovery.EndpointsByTargets(ctx, groups)
	if len(results) != 2 || results[0].Err != nil || len(results[0].Routes) != 2500 {
		t.Fatalf("first aligned group lost valid routes: %+v", results)
	}
	if results[1].Err == nil || len(results[1].Routes) != 0 {
		t.Fatalf("Full steps bypassed original 4096-route call bound: second_count=%d second_error=%v", len(results[1].Routes), results[1].Err)
	}
	// Independent callers retain their own original result allowance.
	rows, err := f.recovery.Endpoints(ctx, f.currentTarget(), uids[250:])
	if err != nil || len(rows) != 2500 {
		t.Fatalf("independent call lost its own result allowance: %d %v", len(rows), err)
	}
}

type architectureBlockingSlots struct {
	*architectureFixture
	entered, release chan struct{}
	once             sync.Once
}

func (b *architectureBlockingSlots) ReadSlotPage(ctx context.Context, req presence.SlotPageRequest, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	page, err := b.architectureFixture.ReadSlotPage(ctx, req, budget)
	b.once.Do(func() { close(b.entered) })
	<-b.release // Actual transport ownership lasts until return, even on cancel.
	return page, err
}

func TestPresenceArchitectureSlotCancelJoinsActualPageOwnership(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	b := &architectureBlockingSlots{architectureFixture: f, entered: make(chan struct{}), release: make(chan struct{})}
	f.slots = presence.NewSlotRecovery(f.directory, f, b)
	f.recovery = presence.NewAuthorityRecovery(f.slots, f.slots)
	callCtx, callCancel := context.WithCancel(ctx)
	defer callCancel()
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		_, err := f.recovery.Endpoints(callCtx, f.currentTarget(), []string{"offline"})
		done <- err
	}()
	var once sync.Once
	release := func() { once.Do(func() { close(b.release) }) }
	t.Cleanup(func() { callCancel(); release(); waitArchitectureEvent(t, joined, "lookup join") })
	waitArchitectureEvent(t, b.entered, "physical slot page")
	callCancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller did not return original cancel: %v", err)
	}
	// Full page turns now belong to the outer execution too. Neither owner may
	// claim join while the transport explicitly retains its actual page reply.
	for name, closeOwner := range map[string]func(context.Context) error{"outer": f.recovery.Close, "slot": f.slots.Close} {
		closedCtx, closedCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		err := closeOwner(closedCtx)
		closedCancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s Close claimed physical completion before return: %v", name, err)
		}
	}
	if s := f.slots.RecoveryResultBudget().Stats(); s.Bytes == 0 {
		t.Fatal("page credit released while transport still owned reply")
	}
	release()
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if s := f.slots.RecoveryResultBudget().Stats(); s.Bytes != 0 || s.Rows != 0 {
		t.Fatalf("actual join retained credits: %+v", s)
	}
	if _, err := f.directory.EndpointsByUID(f.currentTarget(), "offline"); err == nil {
		t.Fatal("canceled page published Complete")
	}
}

// Capacity refusal is exercised through the real owner page and shared result
// ledger. It must fail before partial completion, not merely reject a counter.
func TestPresenceArchitectureSlotPageCapacityFailureRemainsUnknown(t *testing.T) {
	f := newArchitectureFixture(t, "")
	for id := uint64(101); id < 103; id++ {
		err := f.registry.RegisterPending(online.LocalSession{Route: online.OwnerRoute{UID: architectureUID, HashSlot: 37, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: id, SessionID: id, DeviceID: fmt.Sprintf("d%d", id), DeviceFlag: 1, DeviceLevel: 0, ConnectedUnix: 100, LastActivityUnix: 100}})
		if err != nil {
			t.Fatal(err)
		}
		if err = f.registry.MarkActive(id); err != nil {
			t.Fatal(err)
		}
	}
	pressure, err := f.slots.RecoveryResultBudget().TryReserve(presence.RecoveryResultMaxRows-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pressure.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err = f.recovery.Endpoints(ctx, f.currentTarget(), []string{architectureUID}); err == nil {
		t.Fatal("capacity refused page published proof")
	}
	if _, err = f.directory.EndpointsByUID(f.currentTarget(), architectureUID); err == nil {
		t.Fatal("partial rows escaped as complete")
	}
	pressure.Release()
	if err = f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.slots.RecoveryResultBudget().Stats(); got.Bytes != 0 || got.Rows != 0 || got.Rejected == 0 {
		t.Fatalf("capacity/cleanup accounting: %+v", got)
	}
}

func TestPresenceArchitectureClosedSlotRecoveryRejectsWarmCalls(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.recovery.Endpoints(ctx, f.currentTarget(), []string{"offline"}); err != nil {
		t.Fatal(err)
	}
	if err := f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.slots.RecoverRoutes(ctx, f.currentTarget(), []string{"offline"}); err == nil {
		t.Fatal("closed recovery accepted a warm call")
	}
}

type architectureBlockingRecheck struct {
	*architectureFixture
	entered, release chan struct{}
}

func (b *architectureBlockingRecheck) ReadGenerationUIDs(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut, uids []string, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotRoutes, error) {
	rows, err := b.architectureFixture.ReadGenerationUIDs(ctx, claim, cut, uids, budget)
	close(b.entered)
	<-b.release
	return rows, err
}

func TestPresenceArchitectureCloseWaitsForActualSuspectRecheck(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.app.Activate(ctx, architectureCommand(2)); err != nil {
		t.Fatal(err)
	}
	f.directory.ExpireRoutes(time.Unix(151, 0), 50*time.Second)
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	b := &architectureBlockingRecheck{architectureFixture: f, entered: make(chan struct{}), release: make(chan struct{})}
	f.slots = presence.NewSlotRecovery(f.directory, f, b)
	f.recovery = presence.NewAuthorityRecovery(f.slots, f.slots)
	callCtx, callCancel := context.WithCancel(ctx)
	defer callCancel()
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		_, err := f.recovery.Endpoints(callCtx, f.currentTarget(), []string{architectureUID})
		done <- err
	}()
	var once sync.Once
	release := func() { once.Do(func() { close(b.release) }) }
	t.Cleanup(func() { callCancel(); release(); waitArchitectureEvent(t, joined, "recheck lookup join") })
	waitArchitectureEvent(t, b.entered, "actual suspect recheck")
	callCancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancel=%v", err)
	}
	closedCtx, closedCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer closedCancel()
	if err := f.slots.Close(closedCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close claimed recheck completion before actual return: %v", err)
	}
	release()
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if s := f.slots.RecoveryResultBudget().Stats(); s.Rows != 0 || s.Bytes != 0 {
		t.Fatalf("recheck join retained credits: %+v", s)
	}
}

type architectureDeadlineSource struct {
	*architectureFixture
	chain    time.Time
	hasChain bool
}

func (s *architectureDeadlineSource) ClaimSlotGeneration(ctx context.Context, target presence.RouteTarget) (presence.GenerationClaim, []uint64, error) {
	s.chain, s.hasChain = ctx.Deadline()
	return s.architectureFixture.ClaimSlotGeneration(ctx, target)
}

type architectureDeadlineOwners struct {
	*architectureFixture
	deadlines []time.Time
	missing   bool
}

func (o *architectureDeadlineOwners) record(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	if !ok {
		o.missing = true
	}
	o.deadlines = append(o.deadlines, deadline)
}
func (o *architectureDeadlineOwners) BeginSlot(ctx context.Context, claim presence.GenerationClaim, id uint64) (presence.OwnerCut, error) {
	o.record(ctx)
	return o.architectureFixture.BeginSlot(ctx, claim, id)
}
func (o *architectureDeadlineOwners) ReadSlotPage(ctx context.Context, req presence.SlotPageRequest, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	o.record(ctx)
	return o.architectureFixture.ReadSlotPage(ctx, req, budget)
}
func (o *architectureDeadlineOwners) EndSlot(ctx context.Context, req presence.SlotEndRequest) (presence.SlotEnd, error) {
	o.record(ctx)
	return o.architectureFixture.EndSlot(ctx, req)
}

// Before deadline implementation: a live follower must not turn the physical
// task into an unbounded background root, and continuation pages cannot reset
// their owner's original one-second allowance.
func TestPresenceArchitectureSlotDeadlinesAreFixedAcrossPages(t *testing.T) {
	f := newArchitectureFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.slots.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s := &architectureDeadlineSource{architectureFixture: f}
	o := &architectureDeadlineOwners{architectureFixture: f}
	f.slots = presence.NewSlotRecovery(f.directory, s, o)
	f.recovery = presence.NewAuthorityRecovery(f.slots, f.slots)
	started := time.Now()
	if _, err := f.recovery.Endpoints(ctx, f.currentTarget(), []string{"offline"}); err != nil {
		t.Fatal(err)
	}
	if !s.hasChain || s.chain.After(started.Add(2100*time.Millisecond)) {
		t.Fatalf("physical chain lacks original 2s bound: present=%v deadline=%v", s.hasChain, s.chain)
	}
	if o.missing || len(o.deadlines) != 3 {
		t.Fatalf("owner operations lack deadlines: missing=%v count=%d", o.missing, len(o.deadlines))
	}
	first := o.deadlines[0]
	if first.After(started.Add(1100*time.Millisecond)) || first.After(s.chain) {
		t.Fatal("owner deadline exceeds original one-second/chain bound")
	}
	for _, deadline := range o.deadlines[1:] {
		if !deadline.Equal(first) {
			t.Fatal("continuation reset the owner deadline")
		}
	}
}
