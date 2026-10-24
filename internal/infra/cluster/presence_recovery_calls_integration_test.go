//go:build integration

package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestRecoverRouteCallsOneDeadlineAcrossPagesAndLegacyFallback(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "paged"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1)
			var deadlines []time.Time
			read := func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("owner context has no deadline")
				}
				deadlines = append(deadlines, deadline)
				timer := time.NewTimer(150 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			var reader presence.OwnerRouteReader
			if legacy {
				reader = recoveryBatchReaderFunc(func(ctx context.Context, g []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
					if err := read(ctx); err != nil {
						return []presence.OwnerRouteResult{{Err: err}}
					}
					return recoveryCallsPage(1, 11, []presence.RecoveryCall{{Groups: g}}, 0, 1, 1).Results[0]
				})
			} else {
				reader = recoveryCallReader{page: func(ctx context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
					if err := read(ctx); err != nil {
						return presence.OwnerRouteCallPage{}, err
					}
					return recoveryCallsPage(1, 11, c, start, start+1, 3000), nil
				}}
			}
			began := time.Now()
			got := NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls)
			wantRoutes := 3000
			if legacy {
				wantRoutes = 1
			}
			if got[0][0].Err != nil || len(got[0][0].Routes) != wantRoutes {
				t.Fatalf("completed original call poisoned by next timeout: %v/%d", got[0][0].Err, len(got[0][0].Routes))
			}
			requireRecoveryCallErrors(t, got[1:])
			if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
				t.Fatalf("owner deadline reset across calls/pages: %v", deadlines)
			}
			if time.Since(began) > 600*time.Millisecond {
				t.Fatal("single owner budget was not bounded")
			}
		})
	}
}

func TestRecoverRouteCallsCancellationPreventsNextPage(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		reads++
		cancel()
		return recoveryCallsPage(1, 11, c, start, start+1, 1), nil
	}}
	got := NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(ctx, calls)
	for _, call := range got {
		for _, group := range call {
			if !errors.Is(group.Err, context.Canceled) {
				t.Fatalf("parent cancellation=%v", group.Err)
			}
		}
	}
	if reads != 1 {
		t.Fatalf("read after parent cancellation: %d", reads)
	}
}

func TestRecoverRouteCallsSharesExistingSixteenRoundAdmission(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var completed atomic.Bool
	reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		if !completed.Load() {
			entered <- struct{}{}
			<-release
		}
		return recoveryCallsPage(1, 11, c, start, len(c), 0), nil
	}}
	source := NewPresenceRecoveryOwners(node, reader)
	joined := make(chan struct{})
	var wg sync.WaitGroup
	outcomes := make(chan [][]presence.EndpointLookupResult, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); outcomes <- source.RecoverRouteCalls(ctx, calls) }()
	}
	go func() { wg.Wait(); close(joined) }()
	defer func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("owner rounds not joined")
		}
	}()
	for i := 0; i < 16; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("only %d of sixteen rounds entered", i)
		}
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer waitCancel()
	// The unchanged legacy API must contend for this same gate.
	old := source.RecoverRoutesByTargets(waitCtx, calls[0].Groups)
	if !errors.Is(old[0].Err, context.DeadlineExceeded) {
		t.Fatalf("old API bypassed shared gate: %v", old[0].Err)
	}
	waitCtx2, waitCancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer waitCancel2()
	next := source.RecoverRouteCalls(waitCtx2, calls)
	if !errors.Is(next[0][0].Err, context.DeadlineExceeded) {
		t.Fatalf("seventeenth new round bypassed gate: %v", next[0][0].Err)
	}
	cancel()
	releaseOnce.Do(func() { close(release) })
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("canceled rounds did not join")
	}
	for i := 0; i < 16; i++ {
		result := <-outcomes
		if !errors.Is(result[0][0].Err, context.Canceled) {
			t.Fatalf("held parent cancel=%v", result[0][0].Err)
		}
	}
	completed.Store(true)
	got := source.RecoverRouteCalls(context.Background(), calls)
	for _, call := range got {
		for _, group := range call {
			if group.Err != nil || len(group.Routes) != 0 {
				t.Fatalf("permit was not returned: %v", group.Err)
			}
		}
	}
	// Keep the explicit unavailable failure family separate from a successful
	// complete empty snapshot in this round-trip ownership test.
	if errors.Is(got[0][0].Err, authority.ErrRouteNotReady) {
		t.Fatal("empty proof unavailable")
	}
}
