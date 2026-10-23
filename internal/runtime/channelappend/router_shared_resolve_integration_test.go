//go:build integration

package channelappend

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

// Shared resolution belongs to every active item for the canonical channel;
// the first item's deadline must not become the authority lookup's lifetime.
func TestRouterSharedResolveKeepsLiveFollower(t *testing.T) {
	for _, deadlineKind := range []string{"context", "item"} {
		t.Run(deadlineKind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				first := routerItem("first", "shared", 2)
				if deadlineKind == "context" {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
					defer cancel()
					first.Context = ctx
				} else {
					first.Deadline = time.Now().Add(5 * time.Millisecond)
				}
				follower := routerItem("follower", "shared", 2)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				defer cancel()
				follower.Context = ctx
				resolves, submits := 0, 0
				var submitted []SendBatchItem
				want := SendResult{MessageID: 31, MessageSeq: 24, Reason: ReasonSuccess}
				router := NewRouter(RouterOptions{LocalNodeID: 7, MaxRouteAttempts: 1,
					Resolver: routingPhaseResolver(func(ctx context.Context, id ChannelID) (AuthorityTarget, error) {
						resolves++
						timer := time.NewTimer(10 * time.Millisecond)
						defer timer.Stop()
						select {
						case <-ctx.Done():
							return AuthorityTarget{}, ctx.Err()
						case <-timer.C:
							return routerTarget(id.ID, id.Type, 7), nil
						}
					}),
					Local: routingPhaseLocal(func(_ context.Context, _ AuthorityTarget, items []SendBatchItem) (*Future, error) {
						submits++
						submitted = append(submitted, items...)
						future := newFuture(len(items))
						future.complete([]SendBatchItemResult{{Result: want}})
						return future, nil
					})})
				out := router.SendBatch([]SendBatchItem{first, follower})
				if len(out) != 2 || !errors.Is(out[0].Err, context.DeadlineExceeded) || out[1].Err != nil || out[1].Result != want {
					t.Fatalf("shared resolve results = %+v, want first deadline and live follower success", out)
				}
				if resolves != 1 || submits != 1 || len(submitted) != 1 || submitted[0].Command.FromUID != follower.Command.FromUID {
					t.Fatalf("resolve/submit/items = %d/%d/%+v, want one resolve and only follower submitted", resolves, submits, submitted)
				}
				if ctx.Err() != nil {
					t.Fatalf("follower context ended: %v", ctx.Err())
				}
				for _, result := range out {
					if _, ok := contract.RoutingFailureDiagnosticsFromError(result.Err); ok {
						t.Fatal("shared batch fabricated single-item routing phases")
					}
				}
			})
		})
	}
}

func TestRouterSharedResolveAllCanceledJoinsResolver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstContext, cancelFirst := context.WithCancel(context.Background())
		defer cancelFirst()
		secondContext, cancelSecond := context.WithCancel(context.Background())
		defer cancelSecond()
		first, second := routerItem("first", "shared", 2), routerItem("second", "shared", 2)
		first.Context, second.Context = firstContext, secondContext
		entered := make(chan context.Context, 1)
		canceled, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		router := NewRouter(RouterOptions{LocalNodeID: 7, MaxRouteAttempts: 1,
			Resolver: routingPhaseResolver(func(ctx context.Context, _ ChannelID) (AuthorityTarget, error) {
				defer close(exited)
				entered <- ctx
				<-ctx.Done()
				close(canceled)
				<-release
				return AuthorityTarget{}, ctx.Err()
			})})
		done := make(chan []SendBatchItemResult, 1)
		go func() { done <- router.SendBatch([]SendBatchItem{first, second}) }()
		resolveContext := <-entered
		cancelFirst()
		synctest.Wait()
		canceledWithLiveFollower := resolveContext.Err() != nil
		cancelSecond()
		<-canceled
		synctest.Wait()
		returnedBeforeJoin := false
		var out []SendBatchItemResult
		select {
		case out = <-done:
			returnedBeforeJoin = true
		default:
		}
		close(release)
		<-exited
		if !returnedBeforeJoin {
			out = <-done
		}
		if canceledWithLiveFollower || returnedBeforeJoin {
			t.Fatalf("canceled with live follower=%v, returned before resolver join=%v", canceledWithLiveFollower, returnedBeforeJoin)
		}
		if len(out) != 2 || !errors.Is(out[0].Err, context.Canceled) || !errors.Is(out[1].Err, context.Canceled) {
			t.Fatalf("all-canceled results = %+v", out)
		}
	})
}

func TestRouterSharedResolveAllDeadlinesKeepItemErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstContext, cancelFirst := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancelFirst()
		secondContext, cancelSecond := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancelSecond()
		first, second := routerItem("first", "shared", 2), routerItem("second", "shared", 2)
		first.Context, second.Context = firstContext, secondContext
		started := time.Now()
		var canceledAt, exitedAt time.Time
		resolves := 0
		router := NewRouter(RouterOptions{LocalNodeID: 7, MaxRouteAttempts: 1,
			Resolver: routingPhaseResolver(func(ctx context.Context, _ ChannelID) (AuthorityTarget, error) {
				resolves++
				<-ctx.Done()
				canceledAt = time.Now()
				time.Sleep(2 * time.Millisecond)
				exitedAt = time.Now()
				return AuthorityTarget{}, ctx.Err()
			})})
		out := router.SendBatch([]SendBatchItem{first, second})
		if len(out) != 2 || !errors.Is(out[0].Err, context.DeadlineExceeded) || !errors.Is(out[1].Err, context.DeadlineExceeded) {
			t.Fatalf("deadline results = %+v, want each item's original deadline error", out)
		}
		if resolves != 1 || canceledAt.Sub(started) != 10*time.Millisecond || exitedAt.Sub(started) != 12*time.Millisecond || time.Since(started) != 12*time.Millisecond {
			t.Fatalf("resolve count=%d cancellation=%s exit=%s return=%s, want last deadline then joined cleanup", resolves, canceledAt.Sub(started), exitedAt.Sub(started), time.Since(started))
		}
	})
}

func TestRouterSharedResolvePreservesSubmissionProof(t *testing.T) {
	for _, priorSubmission := range []bool{false, true} {
		name := "before-submission"
		if priorSubmission {
			name = "after-ambiguous-submission"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.Join(ErrAppendNotSubmitted, context.DeadlineExceeded)
				resolves, forwards := 0, 0
				router := NewRouter(RouterOptions{LocalNodeID: 7, MaxRouteAttempts: 3,
					Resolver: routingPhaseResolver(func(_ context.Context, id ChannelID) (AuthorityTarget, error) {
						resolves++
						if priorSubmission && resolves == 1 {
							return routerTarget(id.ID, id.Type, 8), nil
						}
						return AuthorityTarget{}, cause
					}),
					Remote: routingPhaseRemote(func(context.Context, AuthorityTarget, []SendBatchItem) []SendBatchItemResult {
						forwards++
						return []SendBatchItemResult{{Err: ErrStaleRoute}, {Err: ErrStaleRoute}}
					})})
				out := router.SendBatch([]SendBatchItem{routerItem("first", "shared", 2), routerItem("second", "shared", 2)})
				if len(out) != 2 {
					t.Fatalf("result cardinality = %d, want 2", len(out))
				}
				for _, result := range out {
					if priorSubmission {
						if !errors.Is(result.Err, ErrAppendFailed) || errors.Is(result.Err, ErrAppendNotSubmitted) || errors.Is(result.Err, context.DeadlineExceeded) {
							t.Fatalf("earlier submission uncertainty changed: %v", result.Err)
						}
					} else if result.Err != cause || !errors.Is(result.Err, ErrAppendNotSubmitted) {
						t.Fatalf("unsubmitted resolver error changed: %v", result.Err)
					}
					if _, ok := contract.RoutingFailureDiagnosticsFromError(result.Err); ok {
						t.Fatal("shared batch fabricated single-item routing phases")
					}
				}
				wantResolves, wantForwards := 1, 0
				if priorSubmission {
					wantResolves, wantForwards = 2, 1
				}
				if resolves != wantResolves || forwards != wantForwards {
					t.Fatalf("resolves/forwards = %d/%d, want %d/%d", resolves, forwards, wantResolves, wantForwards)
				}
			})
		})
	}
}

// The shared context cancels after the last caller deadline. Converting that
// cancellation to each caller's timeout must retain only valid origin proof.
func TestRouterSharedResolveDeadlinePreservesNonSubmissionProof(t *testing.T) {
	for _, priorSubmission := range []bool{false, true} {
		name := "before-submission"
		if priorSubmission {
			name = "after-ambiguous-submission"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				firstContext, cancelFirst := context.WithTimeout(context.Background(), 5*time.Millisecond)
				defer cancelFirst()
				secondContext, cancelSecond := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancelSecond()
				first, second := routerItem("first", "shared", 2), routerItem("second", "shared", 2)
				first.Context, second.Context = firstContext, secondContext
				resolves, forwards := 0, 0
				router := NewRouter(RouterOptions{LocalNodeID: 7,
					Resolver: routingPhaseResolver(func(ctx context.Context, id ChannelID) (AuthorityTarget, error) {
						resolves++
						if priorSubmission && resolves == 1 {
							return routerTarget(id.ID, id.Type, 8), nil
						}
						<-ctx.Done()
						return AuthorityTarget{}, errors.Join(ErrAppendNotSubmitted, ctx.Err())
					}),
					Remote: routingPhaseRemote(func(context.Context, AuthorityTarget, []SendBatchItem) []SendBatchItemResult {
						forwards++
						return []SendBatchItemResult{{Err: ErrStaleRoute}, {Err: ErrStaleRoute}}
					})})
				out := router.SendBatch([]SendBatchItem{first, second})
				if len(out) != 2 {
					t.Fatalf("result cardinality = %d, want 2", len(out))
				}
				for _, result := range out {
					if priorSubmission {
						if !errors.Is(result.Err, ErrAppendFailed) || errors.Is(result.Err, ErrAppendNotSubmitted) || errors.Is(result.Err, context.DeadlineExceeded) {
							t.Fatalf("earlier submission uncertainty changed: %v", result.Err)
						}
					} else if !errors.Is(result.Err, context.DeadlineExceeded) || !errors.Is(result.Err, ErrAppendNotSubmitted) || errors.Is(result.Err, context.Canceled) {
						t.Fatalf("deadline conversion lost origin non-submission proof: %v", result.Err)
					}
				}
				wantResolves, wantForwards := 1, 0
				if priorSubmission {
					wantResolves, wantForwards = 2, 1
				}
				if resolves != wantResolves || forwards != wantForwards {
					t.Fatalf("resolves/forwards = %d/%d, want %d/%d", resolves, forwards, wantResolves, wantForwards)
				}
			})
		})
	}
}
