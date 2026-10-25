//go:build integration

package presence_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestRecoveryCapacityCensusKeepsCanceledProviderOwnership(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	entered, release := make(chan struct{}, 16), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	owners := recoveryCallsFunc(func(_ context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
		entered <- struct{}{}
		<-release // Cancellation cannot release real provider ownership.
		out := make([][]presence.EndpointLookupResult, len(calls))
		for i, call := range calls {
			out[i] = completeFlightRoutes(call.Groups)
		}
		return out
	})
	r := presence.NewAuthorityRecovery(directory, owners)
	if s := r.Stats(); s != (presence.RecoveryStats{}) {
		t.Fatalf("initial census=%+v", s)
	}
	seeds := make([]*flightCall, 16)
	for i := range seeds {
		seeds[i] = startFlightCall(t, r, flightGroups(target, fmt.Sprintf("seed-%02d", i)))
		awaitFlightGate(t, entered)
	}
	queued := make([]*flightCall, 2)
	for i := range queued {
		queued[i] = startFlightCall(t, r, flightGroups(target, fmt.Sprintf("queued-%d", i)))
		awaitFlightGate(t, queued[i].waiting)
	}
	s := r.Stats()
	if s.QueuedCalls != 2 || s.ActiveBatches != 16 || s.ResultCalls != 16 || s.Members != 18 || s.FencedUIDs != 18 || s.OwnedUIDBytes == 0 || !s.ResultLimited || !s.ExecutionLimited {
		t.Fatalf("paused capacity census=%+v", s)
	}
	if s.BatchesTotal != 16 || s.BatchCallsTotal != 16 || s.BatchUIDsTotal != 16 || s.BatchCallsHighWater != 1 {
		t.Fatalf("dispatch census=%+v", s)
	}
	queued[0].cancel()
	requireFlightError(t, queued[0].await(t), context.Canceled)
	if got := r.Stats(); got.QueuedCalls != 1 || got.Members != 17 || got.FencedUIDs != 17 || got.OwnedUIDBytes >= s.OwnedUIDBytes || got.ResultCalls != 16 {
		t.Fatalf("queued cancel census=%+v", got)
	}
	seeds[0].cancel()
	requireFlightError(t, seeds[0].await(t), context.Canceled)
	if got := r.Stats(); got.ActiveBatches != 16 || got.ResultCalls != 16 || got.FencedUIDs != 17 {
		t.Fatalf("early active ownership release=%+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Close(ctx); err != context.Canceled {
		t.Fatalf("incomplete Close=%v", err)
	}
	for _, call := range seeds[1:] {
		requireFlightError(t, call.await(t), authority.ErrRouteNotReady)
	}
	requireFlightError(t, queued[1].await(t), authority.ErrRouteNotReady)
	if got := r.Stats(); got.QueuedCalls != 0 || got.ActiveBatches != 16 || got.ResultCalls != 16 || got.Members != 16 || got.FencedUIDs != 16 {
		t.Fatalf("Close lost provider census=%+v", got)
	}
	unblock()
	join, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := r.Close(join); err != nil {
		t.Fatal(err)
	}
	if got := r.Stats(); got.QueuedCalls != 0 || got.ActiveBatches != 0 || got.ResultCalls != 0 || got.Members != 0 || got.FencedUIDs != 0 || got.OwnedUIDBytes != 0 || got.ResultRows != 0 || got.ResultDecodedBytes != 0 {
		t.Fatalf("drained census=%+v", got)
	}
}

type censusOwners struct {
	recoveryCallsFunc
	observed func(context.Context, []presence.RecoveryCall, func(presence.RecoveryResultUsage)) [][]presence.EndpointLookupResult
}

func (o censusOwners) RecoverRouteCallsObserved(ctx context.Context, calls []presence.RecoveryCall, observe func(presence.RecoveryResultUsage)) [][]presence.EndpointLookupResult {
	return o.observed(ctx, calls, observe)
}

func TestRecoveryCapacityCensusRetainsPartialOwnerResultsUntilRealReturn(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	read := func(_ context.Context, calls []presence.RecoveryCall, observe func(presence.RecoveryResultUsage)) [][]presence.EndpointLookupResult {
		if observe != nil {
			observe(presence.RecoveryResultUsage{ResultRows: 3, ResultDecodedBytes: 500, PageRows: 2, PageDecodedBytes: 300})
		}
		close(entered)
		<-release
		return [][]presence.EndpointLookupResult{completeFlightRoutes(calls[0].Groups)}
	}
	owners := censusOwners{observed: read, recoveryCallsFunc: func(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
		return read(ctx, calls, nil)
	}}
	r := presence.NewAuthorityRecoveryWithOptions(directory, owners, presence.RecoveryOptions{ResultCensus: true})
	call := startFlightCall(t, r, flightGroups(target, "one"))
	awaitFlightGate(t, entered)
	s := r.Stats()
	if !s.ResultCensusEnabled || s.ResultRows != 3 || s.ResultDecodedBytes != 500 || s.PageRows != 2 || s.PageDecodedBytes != 300 || s.DecodedBytesHighWater != 800 {
		t.Fatalf("partial owner footprint absent: %+v", s)
	}
	call.cancel()
	requireFlightError(t, call.await(t), context.Canceled)
	if got := r.Stats(); got.ResultRows != 3 || got.ResultDecodedBytes != 500 || got.PageRows != 2 || got.PageDecodedBytes != 300 {
		t.Fatalf("caller cancellation released real response: %+v", got)
	}
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.Stats(); got.ResultRows != 0 || got.ResultDecodedBytes != 0 || got.PageRows != 0 || got.PageDecodedBytes != 0 || got.DecodedBytesHighWater != 800 {
		t.Fatalf("real return did not retire footprint: %+v", got)
	}
}

func TestRecoveryCapacityCensusDisabledAvoidsResultObservation(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	observed := false
	read := func(_ context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
		return [][]presence.EndpointLookupResult{completeFlightRoutes(calls[0].Groups)}
	}
	owners := censusOwners{recoveryCallsFunc: read, observed: func(ctx context.Context, calls []presence.RecoveryCall, _ func(presence.RecoveryResultUsage)) [][]presence.EndpointLookupResult {
		observed = true
		return read(ctx, calls)
	}}
	r := presence.NewAuthorityRecovery(directory, owners)
	if _, err := r.Endpoints(context.Background(), target, []string{"off"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := r.Stats()
	if observed || s.ResultCensusEnabled || s.ResultRowsHighWater != 0 || s.DecodedBytesHighWater != 0 || s.BatchesTotal != 1 {
		t.Fatalf("disabled census still scanned provider results: observed=%v stats=%+v", observed, s)
	}
}
