//go:build integration

package presence_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type prepaidRecoveryOwners struct {
	entered   chan []presence.RecoveryCall
	seeds     map[string]chan struct{}
	batchGate chan struct{}
	enabled   bool
}

func (o *prepaidRecoveryOwners) SupportsBoundedRecoveryResults() bool { return o.enabled }
func (o *prepaidRecoveryOwners) RecoverRoutes(context.Context, presence.RouteTarget, []string) ([]presence.Route, error) {
	return nil, authority.ErrRouteNotReady
}
func (o *prepaidRecoveryOwners) RecoverRouteCalls(ctx context.Context, calls []presence.RecoveryCall) [][]presence.EndpointLookupResult {
	result, _ := o.read(ctx, calls, nil)
	return result.Results
}
func (o *prepaidRecoveryOwners) RecoverRouteCallsBounded(ctx context.Context, calls []presence.RecoveryCall, b *presence.RecoveryResultBudget, _ func(presence.RecoveryResultUsage)) (presence.OwnedRecoveryResults, error) {
	return o.read(ctx, calls, b)
}
func (o *prepaidRecoveryOwners) read(_ context.Context, calls []presence.RecoveryCall, b *presence.RecoveryResultBudget) (presence.OwnedRecoveryResults, error) {
	var out presence.OwnedRecoveryResults
	if b != nil {
		l, err := b.TryReserve(0, uint64(len(calls))*4096)
		if err != nil {
			return out, err
		}
		out.Lease = l
	}
	out.Results = make([][]presence.EndpointLookupResult, len(calls))
	for i, c := range calls {
		out.Results[i] = make([]presence.EndpointLookupResult, len(c.Groups))
	}
	o.entered <- calls
	gate := o.batchGate
	if strings.HasPrefix(calls[0].Groups[0].UIDs[0], "prepaid-seed-") {
		gate = o.seeds[calls[0].Groups[0].UIDs[0]]
	}
	<-gate // Physical ownership deliberately survives caller cancellation.
	return out, nil
}

func TestRecoveryPrepaidResultsUseBatchCapacityAndRetainCanceledOwnership(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	owners := &prepaidRecoveryOwners{entered: make(chan []presence.RecoveryCall, 64), seeds: make(map[string]chan struct{}), batchGate: make(chan struct{}), enabled: true}
	for i := 0; i < 16; i++ {
		owners.seeds[fmt.Sprintf("prepaid-seed-%d", i)] = make(chan struct{})
	}
	var once, firstOnce sync.Once
	releaseFirst := func() { firstOnce.Do(func() { close(owners.seeds["prepaid-seed-0"]) }) }
	release := func() {
		once.Do(func() {
			releaseFirst()
			for name, ch := range owners.seeds {
				if name != "prepaid-seed-0" {
					close(ch)
				}
			}
			close(owners.batchGate)
		})
	}
	t.Cleanup(release)
	recovery := presence.NewAuthorityRecovery(directory, owners)
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := recovery.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 16; i++ {
		startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("prepaid-seed-%d", i)))
		select {
		case <-owners.entered:
		case <-time.After(time.Second):
			t.Fatal("seed failed")
		}
	}
	queued := make([]*flightCall, 32)
	for i := range queued {
		queued[i] = startFlightCall(t, recovery, flightGroups(target, fmt.Sprintf("prepaid-queued-%d", i)))
		awaitFlightGate(t, queued[i].waiting)
	}
	releaseFirst()
	select {
	case batch := <-owners.entered:
		if len(batch) != 16 {
			t.Fatalf("coarse call credits stranded a prepaid batch: got %d calls, want 16", len(batch))
		}
	case <-time.After(time.Second):
		t.Fatal("prepaid batch did not dispatch")
	}
	if got := recovery.Stats(); got.ActiveBatches != 16 || got.ResultCalls != 31 || got.QueuedCalls != 16 || got.ResultLimited {
		t.Fatalf("wrong independent execution/result ownership: %+v", got)
	}
	queued[0].cancel()
	requireFlightError(t, queued[0].await(t), context.Canceled)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recovery.Close(ctx); err != context.Canceled {
		t.Fatalf("Close erased physical provider: %v", err)
	}
	if got := recovery.Stats(); got.ActiveBatches != 16 || got.ResultCalls != 31 || !got.ResultBudgetEnabled || got.OwnedResultBytes == 0 {
		t.Fatalf("cancellation returned active responsibility: %+v", got)
	}
	release()
	join, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := recovery.Close(join); err != nil {
		t.Fatal(err)
	}
	if got := recovery.Stats(); got.ActiveBatches != 0 || got.ResultCalls != 0 || got.OwnedResultBytes != 0 || got.OwnedResultRows != 0 {
		t.Fatalf("provider completion did not join: %+v", got)
	}
	if _, err := directory.EndpointsByUIDs(target, []string{"prepaid-queued-0"}); err != authority.ErrRouteNotReady {
		t.Fatal("canceled caller installed offline proof")
	}
}
