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

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestAuthorityRecoveryTraceSeparatesCanceledQuotaFromOwnerWork(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
	entered := make(chan struct{}, 16)
	var ownerCalls atomic.Int32
	owners := flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		ownerCalls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return flightError(groups, ctx.Err())
	})
	recovery := presence.NewAuthorityRecovery(directory, owners)
	for i := 0; i <= 16; i++ {
		directory.BecomeAuthority(flightTarget(uint16(i)))
	}
	for i := 0; i < 16; i++ {
		startFlightCall(t, recovery, flightGroups(flightTarget(uint16(i)), fmt.Sprintf("held-%d", i)))
		awaitFlightGate(t, entered)
	}
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 3*time.Second)
	defer cancel()
	waiting := &waitObservedContext{Context: ctx, waiting: make(chan struct{})}
	done := make(chan []presence.EndpointLookupResult, 1)
	go func() { done <- recovery.EndpointsByTargets(waiting, flightGroups(flightTarget(16), "quota-only")) }()
	awaitFlightGate(t, waiting.waiting)
	cancel()
	select {
	case results := <-done:
		requireFlightError(t, results, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled quota lookup did not join")
	}
	if ownerCalls.Load() != 16 {
		t.Fatal("canceled quota waiter executed owner work")
	}
	sample := trace.Finish(rpctrace.ResultCanceled)
	if len(sample.Spans) != 1 || sample.Spans[0].Stage != rpctrace.StageRecoveryAdmission || sample.Spans[0].Result != rpctrace.ResultCanceled || sample.Spans[0].Incomplete {
		t.Fatalf("quota cancellation must retain admission-only evidence: %+v", sample.Spans)
	}
}

func TestAuthorityRecoveryTraceKeepsInstallationFenceOutcome(t *testing.T) {
	for _, stale := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("stale=%t/traced=%t", stale, enabled), func(t *testing.T) {
				directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
				target := flightTarget(1)
				directory.BecomeAuthority(target)
				owners := flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
					if stale {
						next := target
						next.LeaderTerm++
						directory.BecomeAuthority(next)
					}
					return completeFlightRoutes(groups)
				})
				ctx := context.Background()
				var trace *rpctrace.Trace
				if enabled {
					recorder, err := rpctrace.NewRecorder(1)
					if err != nil {
						t.Fatal(err)
					}
					trace = recorder.Start(ctx, rpctrace.SideServer, 13)
					ctx = rpctrace.With(ctx, trace)
				}
				routes, err := presence.NewAuthorityRecovery(directory, owners).Endpoints(ctx, target, []string{"install-proof"})
				want := rpctrace.ResultOK
				if stale {
					if !errors.Is(err, authority.ErrNotLeader) {
						t.Fatalf("changed authority accepted old proof: %v", err)
					}
					want = rpctrace.ResultError
				} else if err != nil || len(routes) != 1 {
					t.Fatal("current authority did not install complete owner proof")
				}
				if !enabled {
					return
				}
				sample := trace.Finish(want)
				if len(sample.Spans) != 1 || sample.Spans[0].Stage != rpctrace.StageRecoveryInstall || sample.Spans[0].Result != want || sample.Spans[0].Incomplete {
					t.Fatalf("installation evidence hid authority outcome: %+v", sample.Spans)
				}
			})
		}
	}
}

type heldTraceProofDirectory struct {
	*authority.Directory
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *heldTraceProofDirectory) EndpointsByUIDs(target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	d.once.Do(func() { close(d.entered); <-d.release })
	return d.Directory.EndpointsByUIDs(target, uids)
}

func TestAuthorityRecoveryTraceDoesNotLabelProofReadAsQuotaWait(t *testing.T) {
	directory := &heldTraceProofDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true}), entered: make(chan struct{}), release: make(chan struct{})}
	target := flightTarget(1)
	directory.BecomeAuthority(target)
	owners := flightOwnersFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
		return completeFlightRoutes(groups)
	})
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 3*time.Second)
	defer cancel()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(directory.release) }) }
	t.Cleanup(release)
	done := make(chan []presence.EndpointLookupResult, 1)
	recovery := presence.NewAuthorityRecovery(directory, owners)
	go func() { done <- recovery.EndpointsByTargets(ctx, flightGroups(target, "proof-read")) }()
	awaitFlightGate(t, directory.entered)
	release()
	select {
	case results := <-done:
		if len(results) != 1 || results[0].Err != nil || len(results[0].Routes) != 1 {
			t.Fatal("controlled proof read changed recovery outcome")
		}
	case <-time.After(time.Second):
		t.Fatal("released proof read did not join")
	}
	sample := trace.Finish(rpctrace.ResultOK)
	if len(sample.Spans) != 1 || sample.Spans[0].Stage != rpctrace.StageRecoveryInstall {
		t.Fatalf("proof-read time must not be reported as quota waiting: %+v", sample.Spans)
	}
}

func TestAuthorityRecoveryTraceSeparatesSharedFlightFromQuota(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			directory := authority.NewDirectory(authority.DirectoryOptions{RequireRecovery: true})
			target := flightTarget(1)
			directory.BecomeAuthority(target)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var calls atomic.Int32
			owners := flightOwnersFunc(func(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
				if calls.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-release:
					return completeFlightRoutes(groups)
				case <-ctx.Done():
					return flightError(groups, ctx.Err())
				}
			})
			recovery := presence.NewAuthorityRecovery(directory, owners)
			groups := flightGroups(target, "same-flight")
			leader := startFlightCall(t, recovery, groups)
			awaitFlightGate(t, entered)
			recorder, err := rpctrace.NewRecorder(1)
			if err != nil {
				t.Fatal(err)
			}
			trace := recorder.Start(context.Background(), rpctrace.SideServer, 13)
			ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 3*time.Second)
			defer cancel()
			waiting := &waitObservedContext{Context: ctx, waiting: make(chan struct{})}
			done := make(chan []presence.EndpointLookupResult, 1)
			go func() { done <- recovery.EndpointsByTargets(waiting, groups) }()
			awaitFlightGate(t, waiting.waiting)
			want := rpctrace.ResultOK
			if canceled {
				want = rpctrace.ResultCanceled
				cancel()
			} else {
				unblock()
			}
			select {
			case results := <-done:
				if canceled {
					requireFlightError(t, results, context.Canceled)
				} else if len(results) != 1 || results[0].Err != nil || len(results[0].Routes) != 1 {
					t.Fatal("follower did not reread completed proof")
				}
			case <-time.After(time.Second):
				t.Fatal("flight follower did not join")
			}
			if calls.Load() != 1 {
				t.Fatal("follower executed independent owner work")
			}
			sample := trace.Finish(want)
			if sample.ParentTraceID != "" || len(sample.Spans) != 1 || sample.Spans[0].Stage != rpctrace.Stage("recovery_flight_wait") || sample.Spans[0].Result != want || sample.Spans[0].Incomplete {
				t.Fatalf("shared flight must retain follower-local waiting only: %+v", sample.Spans)
			}
			unblock()
			if results := leader.await(t); len(results) != 1 || results[0].Err != nil {
				t.Fatal("follower cancellation changed leader work")
			}
		})
	}
}
