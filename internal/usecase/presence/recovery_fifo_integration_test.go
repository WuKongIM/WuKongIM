//go:build integration

package presence_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type fifoTraceOwners struct {
	entered chan []presence.RecoveryCall
	release chan struct{}
}

func TestRecoveryFIFOAdmitsOldestCallAtEachSingleCreditTurn(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	releases := make(map[string]chan struct{})
	for i := 0; i < 16; i++ {
		releases[fmt.Sprintf("order-seed-%d", i)] = make(chan struct{})
	}
	for i := 0; i < 3; i++ {
		releases[fmt.Sprintf("order-queued-%d", i)] = make(chan struct{})
	}
	entered := make(chan []presence.RecoveryCall, 32)
	owners := recoveryCallsFunc(func(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
		entered <- calls
		out := make([][]presence.EndpointLookupResult, len(calls))
		for i, c := range calls {
			select {
			case <-releases[c.Groups[0].UIDs[0]]:
				out[i] = completeFlightRoutes(c.Groups)
			case <-ctx.Done():
				out[i] = flightError(c.Groups, ctx.Err())
			}
		}
		return out
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	for i := 0; i < 16; i++ {
		startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("order-seed-%d", i)))
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("initial owner failed")
		}
	}
	queued := make([]*flightCall, 3)
	for i := range queued {
		queued[i] = startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("order-queued-%d", i)))
		awaitFlightGate(t, queued[i].waiting)
	}
	close(releases["order-seed-0"])
	for i, call := range queued {
		select {
		case calls := <-entered:
			if len(calls) != 1 || calls[0].Groups[0].UIDs[0] != fmt.Sprintf("order-queued-%d", i) {
				t.Fatal("later caller overtook an older FIFO entry")
			}
		case <-time.After(time.Second):
			t.Fatal("FIFO head made no progress")
		}
		close(releases[fmt.Sprintf("order-queued-%d", i)])
		if got := call.await(t); got[0].Err != nil {
			t.Fatal("FIFO head failed after provider completion")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recovery.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

type canceledScanContext struct {
	context.Context
	waiting  chan struct{}
	seen     chan struct{}
	gate     <-chan struct{}
	calls    *atomic.Int64
	waitOnce sync.Once
	seenOnce sync.Once
}

func (c *canceledScanContext) Done() <-chan struct{} {
	c.waitOnce.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
func (c *canceledScanContext) Err() error {
	err := c.Context.Err()
	if err != nil {
		c.seenOnce.Do(func() { c.seen <- struct{}{} })
		<-c.gate
		c.calls.Add(1)
	}
	return err
}

func TestRecoveryCancellationScansEachAffectedExecutionOnce(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	owners := flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		close(entered)
		<-release
		return completeFlightRoutes(groups)
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	uids := make([]string, 128)
	for i := range uids {
		uids[i] = fmt.Sprintf("scan-%d", i)
	}
	const members = 8
	seen, gate := make(chan struct{}, members), make(chan struct{})
	var calls atomic.Int64
	cancels := make([]context.CancelFunc, members)
	done := make([]chan []presence.EndpointLookupResult, members)
	for i := 0; i < members; i++ {
		base, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel
		ctx := &canceledScanContext{Context: base, waiting: make(chan struct{}), seen: seen, gate: gate, calls: &calls}
		done[i] = make(chan []presence.EndpointLookupResult, 1)
		go func(i int) { done[i] <- recovery.EndpointsByTargets(ctx, flightGroups(target, uids...)) }(i)
		if i == 0 {
			awaitFlightGate(t, entered)
		} else {
			awaitFlightGate(t, ctx.waiting)
		}
	}
	for _, cancel := range cancels {
		cancel()
	}
	for range members {
		awaitFlightGate(t, seen)
	}
	close(gate)
	for _, result := range done {
		select {
		case got := <-result:
			requireFlightError(t, got, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("cancel join stalled under shared admission lock")
		}
	}
	if got := calls.Load(); got > 5000 {
		t.Fatalf("duplicate UID references amplified cancellation scans: %d context checks", got)
	}
}

func TestRecoveryCanceledBatchMemberDoesNotPublishWithoutLiveFollower(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	seedRelease, batchRelease := make(chan struct{}), make(chan struct{})
	var seedOnce, batchOnce sync.Once
	unblockSeeds := func() { seedOnce.Do(func() { close(seedRelease) }) }
	unblockBatch := func() { batchOnce.Do(func() { close(batchRelease) }) }
	t.Cleanup(unblockSeeds)
	t.Cleanup(unblockBatch)
	type batchStart struct {
		ctx   context.Context
		calls []presence.RecoveryCall
	}
	entered := make(chan []presence.RecoveryCall, 32)
	batchCtx := make(chan batchStart, 32)
	owners := recoveryCallsFunc(func(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
		entered <- calls
		gate := batchRelease
		if strings.HasPrefix(calls[0].Groups[0].UIDs[0], "seed-") {
			gate = seedRelease
		} else {
			batchCtx <- batchStart{ctx, calls}
		}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		out := make([][]presence.EndpointLookupResult, len(calls))
		for i, c := range calls {
			out[i] = completeFlightRoutes(c.Groups)
		}
		return out
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	for i := 0; i < 16; i++ {
		startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("seed-%d", i)))
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("initial owner failed")
		}
	}
	queued := make([]*flightCall, 16)
	for i := range queued {
		queued[i] = startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("member-%d", i)))
		awaitFlightGate(t, queued[i].waiting)
	}
	unblockSeeds()
	var selected batchStart
	started := 0
	for len(selected.calls) < 2 && started < len(queued) {
		select {
		case batch := <-batchCtx:
			started += len(batch.calls)
			if len(batch.calls) > 1 {
				selected = batch
			}
		case <-time.After(time.Second):
			t.Fatal("queued multi-call execution did not start")
		}
	}
	if len(selected.calls) < 2 {
		t.Fatal("controlled queued burst produced no multi-call execution")
	}
	index := 0
	if _, err := fmt.Sscanf(selected.calls[0].Groups[0].UIDs[0], "member-%d", &index); err != nil {
		t.Fatal(err)
	}
	queued[index].cancel()
	requireFlightError(t, queued[index].await(t), context.Canceled)
	if selected.ctx.Err() != nil {
		t.Fatal("one canceled member canceled other batch members")
	}
	unblockBatch()
	for i, call := range queued {
		if i != index {
			if got := call.await(t); got[0].Err != nil {
				t.Fatal("canceled sibling damaged independent call")
			}
		}
	}
	if _, err := directory.EndpointsByUIDs(target, []string{fmt.Sprintf("member-%d", index)}); !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatal("canceled member without a live follower published a proof")
	}
	if err := recovery.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type queuedOwnershipMarker struct{ padding [64]byte }
type queuedOwnershipKey struct{}

func TestRecoveryCanceledQueueReleasesCallerReferencesWithAllCreditsHeld(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	owners := &fifoTraceOwners{entered: make(chan []presence.RecoveryCall, 32), release: make(chan struct{}, 16)}
	t.Cleanup(func() { close(owners.release) })
	recovery := presence.NewAuthorityRecovery(directory, owners)
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	for i := 0; i < 16; i++ {
		width16Lookup(t, recovery, flightGroups(target, fmt.Sprintf("seed-%d", i)))
		select {
		case <-owners.entered:
		case <-time.After(time.Second):
			t.Fatal("held owner failed")
		}
	}
	const count = 2000
	released := make(chan struct{}, count)
	for i := 0; i < count; i++ {
		func() {
			marker := &queuedOwnershipMarker{}
			runtime.SetFinalizer(marker, func(*queuedOwnershipMarker) { released <- struct{}{} })
			base, cancel := context.WithCancel(context.WithValue(context.Background(), queuedOwnershipKey{}, marker))
			ctx := &waitObservedContext{Context: base, waiting: make(chan struct{})}
			done := make(chan []presence.EndpointLookupResult, 1)
			go func() { done <- recovery.EndpointsByTargets(ctx, flightGroups(target, "cancelled-queue")) }()
			awaitFlightGate(t, ctx.waiting)
			cancel()
			select {
			case result := <-done:
				requireFlightError(t, result, context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("queued cancellation did not return")
			}
		}()
	}
	runtime.GC()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for i := 0; i < count-1; i++ {
		select {
		case <-released:
		case <-deadline.C:
			t.Fatal("completed canceled entries retained caller contexts while all result credits remained held")
		}
	}
	runtime.KeepAlive(recovery)
}

type recoveryCallsFunc func(context.Context, []presence.RecoveryCall) [][]presence.EndpointLookupResult

func TestRecoveryPartialCancellationDoesNotHideInvalidOwnerRoutes(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			target := flightTarget(1)
			directory.BecomeAuthority(target)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			owners := flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				close(entered)
				<-release
				results := completeFlightRoutes(groups)
				bad := presence.Route{UID: "dropped", OwnerNodeID: 2, OwnerBootID: 22, SessionID: 1}
				if foreign {
					bad.UID = "not-requested"
				} else {
					bad.SessionID = 0
				}
				results[0].Routes = append(results[0].Routes, bad)
				return results
			})
			recovery := presence.NewAuthorityRecovery(directory, owners)
			creator := startFlightCall(t, recovery, flightGroups(target, "kept", "dropped"))
			awaitFlightGate(t, entered)
			follower := startFlightCall(t, recovery, flightGroups(target, "kept"))
			awaitFlightGate(t, follower.waiting)
			creator.cancel()
			requireFlightError(t, creator.await(t), context.Canceled)
			unblock()
			requireFlightError(t, follower.await(t), authority.ErrStaleRoute)
			if err := recovery.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecoveryLateCallerCannotResurrectCanceledActiveFlight(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var reads atomic.Int32
	owners := flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		if reads.Add(1) == 1 {
			close(entered)
		}
		<-release
		return completeFlightRoutes(groups)
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	first := startFlightCall(t, recovery, flightGroups(target, "late-flight"))
	awaitFlightGate(t, entered)
	first.cancel()
	requireFlightError(t, first.await(t), context.Canceled)
	base, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx := &waitObservedContext{Context: base, waiting: make(chan struct{})}
	done := make(chan []presence.EndpointLookupResult, 1)
	panicked := make(chan struct{}, 1)
	go func() {
		defer func() {
			if recover() != nil {
				panicked <- struct{}{}
			}
		}()
		done <- recovery.EndpointsByTargets(ctx, flightGroups(target, "late-flight"))
	}()
	select {
	case <-ctx.waiting:
	case <-panicked:
		t.Fatal("late caller panicked while subscribing to canceled but still-owned flight")
	case <-time.After(time.Second):
		t.Fatal("late subscriber did not reach cancellation-aware wait")
	}
	unblock()
	select {
	case got := <-done:
		requireFlightError(t, got, authority.ErrRouteNotReady)
	case <-time.After(time.Second):
		t.Fatal("late subscriber failed to join actual provider completion")
	}
	if reads.Load() != 1 {
		t.Fatal("late caller added an implicit retry")
	}
	if err := recovery.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f recoveryCallsFunc) RecoverRoutes(context.Context, presence.RouteTarget, []string) ([]presence.Route, error) {
	return nil, authority.ErrRouteNotReady
}
func (f recoveryCallsFunc) RecoverRouteCalls(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
	return f(ctx, calls)
}

func TestRecoveryResultCreditsRemainOwnedAfterAllCallersCancel(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	entered := make(chan []presence.RecoveryCall, 32)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	owners := recoveryCallsFunc(func(_ context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
		entered <- calls
		<-release // The remote handler has not joined merely because callers left.
		out := make([][]presence.EndpointLookupResult, len(calls))
		for i, c := range calls {
			out[i] = completeFlightRoutes(c.Groups)
		}
		return out
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	for i := 0; i < 17; i++ {
		directory.BecomeAuthority(flightTarget(uint16(i)))
	}
	var calls []*flightCall
	for i := 0; i < 16; i++ {
		calls = append(calls, startFlightCall(t, recovery, flightGroups(flightTarget(uint16(i)), fmt.Sprintf("credit-%d", i))))
		select {
		case batch := <-entered:
			if len(batch) != 1 {
				t.Fatal("unexpected initial batch")
			}
		case <-time.After(time.Second):
			t.Fatal("initial owner did not enter")
		}
	}
	for _, call := range calls {
		call.cancel()
		requireFlightError(t, call.await(t), context.Canceled)
	}
	later := startFlightCall(t, recovery, flightGroups(flightTarget(16), "still-owned"))
	awaitFlightGate(t, later.waiting)
	select {
	case <-entered:
		t.Fatal("cancellation returned result credits before provider completion")
	default:
	}
	unblock()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("real completion failed to return credits")
	}
	if got := later.await(t); len(got) != 1 || got[0].Err != nil {
		t.Fatal("released credits did not permit FIFO progress")
	}
	if err := recovery.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryTotalCallerBudgetIncludesWaitingAndReclaimsCanceledEntry(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	owners := &fifoTraceOwners{entered: make(chan []presence.RecoveryCall, 32), release: make(chan struct{}, 16)}
	t.Cleanup(func() { close(owners.release) })
	recovery := presence.NewAuthorityRecovery(directory, owners)
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	for i := 0; i < 16; i++ {
		call := startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("seed-%d", i)))
		select {
		case <-owners.entered:
		case <-call.done:
			call.joined = true
			t.Fatal("initial owner failed")
		}
	}
	queued := make([]*flightCall, 496)
	for i := range queued {
		queued[i] = startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("entry-%d", i)))
		awaitFlightGate(t, queued[i].waiting)
	}
	_, err := recovery.Endpoints(context.Background(), target, []string{"entry-over-limit"})
	if !errors.Is(err, authority.ErrRouteNotReady) {
		t.Fatal("513th caller was not fail closed")
	}
	queued[0].cancel()
	requireFlightError(t, queued[0].await(t), context.Canceled)
	replacement := startFlightCall(t, recovery, flightGroups(target, "entry-replacement"))
	awaitFlightGate(t, replacement.waiting)
	replacement.cancel()
	requireFlightError(t, replacement.await(t), context.Canceled)
}

func TestRecoveryLaterFollowerDoesNotInheritCreatorDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			target := flightTarget(1)
			directory.BecomeAuthority(target)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			owners := flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				close(entered)
				select {
				case <-ctx.Done():
					return flightError(groups, ctx.Err())
				case <-release:
					return completeFlightRoutes(groups)
				}
			})
			recovery := presence.NewAuthorityRecovery(directory, owners)
			creatorCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer stop()
			creatorDone := make(chan []presence.EndpointLookupResult, 1)
			go func() {
				creatorDone <- recovery.EndpointsByTargets(creatorCtx, flightGroups(target, "deadline-flight"))
			}()
			awaitFlightGate(t, entered)
			followerCtx := context.Background()
			if deadline {
				var cancel context.CancelFunc
				followerCtx, cancel = context.WithTimeout(followerCtx, time.Second)
				defer cancel()
			}
			observed := &waitObservedContext{Context: followerCtx, waiting: make(chan struct{})}
			followerDone := make(chan []presence.EndpointLookupResult, 1)
			go func() { followerDone <- recovery.EndpointsByTargets(observed, flightGroups(target, "deadline-flight")) }()
			awaitFlightGate(t, observed.waiting)
			select {
			case result := <-creatorDone:
				if !errors.Is(result[0].Err, context.DeadlineExceeded) {
					t.Fatal("creator deadline changed")
				}
			case <-time.After(time.Second):
				t.Fatal("creator deadline failed")
			}
			unblock()
			select {
			case result := <-followerDone:
				if len(result) != 1 || result[0].Err != nil || len(result[0].Routes) != 1 {
					t.Fatalf("live follower inherited expired creator execution: %v", result[0].Err)
				}
			case <-time.After(time.Second):
				t.Fatal("live follower did not finish")
			}
			if err := recovery.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (o *fifoTraceOwners) RecoverRoutes(context.Context, presence.RouteTarget, []string) ([]presence.Route, error) {
	return nil, authority.ErrRouteNotReady
}

func (o *fifoTraceOwners) RecoverRouteCalls(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
	o.entered <- calls
	if strings.HasPrefix(calls[0].Groups[0].UIDs[0], "seed-") {
		select {
		case <-o.release:
		case <-ctx.Done():
			out := make([][]presence.EndpointLookupResult, len(calls))
			for i, call := range calls {
				out[i] = flightError(call.Groups, ctx.Err())
			}
			return out
		}
	}
	out := make([][]presence.EndpointLookupResult, len(calls))
	for i, call := range calls {
		out[i] = completeFlightRoutes(call.Groups)
	}
	return out
}

func TestRecoveryFIFOCoalescesQueuedCallsWithoutWaitingToFill(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	owners := &fifoTraceOwners{entered: make(chan []presence.RecoveryCall, 64), release: make(chan struct{}, 16)}
	t.Cleanup(func() { close(owners.release) })
	recovery := presence.NewAuthorityRecovery(directory, owners)
	seeds := make([]*flightCall, 16)
	for i := 0; i < 48; i++ {
		directory.BecomeAuthority(flightTarget(uint16(i)))
	}
	for i := range seeds {
		seeds[i] = startFlightCall(t, recovery, flightGroups(flightTarget(uint16(i)), fmt.Sprintf("seed-%d", i)))
		select {
		case calls := <-owners.entered:
			if len(calls) != 1 {
				t.Fatal("sequential held owner must start without waiting to fill")
			}
		case <-seeds[i].done:
			seeds[i].joined = true
			t.Fatal("recovery did not use the bounded call-batch owner port")
		}
	}
	queued := make([]*flightCall, 32)
	for i := range queued {
		queued[i] = startFlightCall(t, recovery, flightGroups(flightTarget(uint16(i+16)), fmt.Sprintf("queued-%02d-a", i), fmt.Sprintf("queued-%02d-b", i)))
		awaitFlightGate(t, queued[i].waiting)
	}
	for range seeds {
		owners.release <- struct{}{}
	}
	seen, batches := 0, 0
	for seen < len(queued) {
		select {
		case calls := <-owners.entered:
			if len(calls) < 1 || len(calls) > 16 {
				t.Fatal("unbounded call batch")
			}
			seen += len(calls)
			batches++
			t.Logf("actual queued batch calls=%d", len(calls))
		case <-time.After(time.Second):
			t.Fatal("FIFO queue failed to make progress")
		}
	}
	t.Logf("actual queued original calls=%d provider batches=%d", seen, batches)
	if batches >= len(queued) {
		t.Fatal("ready queued calls were never coalesced")
	}
	for _, call := range queued {
		if results := call.await(t); len(results) != 1 || results[0].Err != nil || len(results[0].Routes) != 2 {
			t.Fatal("coalescing changed a caller's independent result")
		}
	}
}

func TestRecoveryCloseRetainsActiveResultOwnershipUntilProviderReturns(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	entered, release := make(chan struct{}), make(chan struct{})
	owners := flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		close(entered)
		<-release // Deliberately ignores cancellation; Close must join real work.
		return completeFlightRoutes(groups)
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	call := startFlightCall(t, recovery, flightGroups(target, "close-owner"))
	awaitFlightGate(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recovery.Close(ctx); err != context.Canceled {
		t.Fatalf("Close erased unfinished owner: %v", err)
	}
	requireFlightError(t, call.await(t), authority.ErrRouteNotReady)
	if _, err := recovery.Endpoints(context.Background(), target, []string{"late"}); !errors.Is(err, authority.ErrRouteNotReady) || presence.ActivationFailurePhase(err) != presence.ActivationPhaseRecoveryAdmission {
		t.Fatalf("closed scheduler lost admission failure: %v", err)
	}
	close(release)
	join, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := recovery.Close(join); err != nil {
		t.Fatalf("repeated Close failed to join real completion: %v", err)
	}
	if _, err := directory.EndpointsByUIDs(target, []string{"close-owner"}); err != authority.ErrRouteNotReady {
		t.Fatal("closed work installed a proof")
	}
}
