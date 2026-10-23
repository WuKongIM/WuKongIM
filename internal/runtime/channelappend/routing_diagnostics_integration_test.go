//go:build integration

package channelappend

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

// These tests enter the real Router through its public ports. Fake elapsed
// time belongs to those ports; no test invokes a private routing operation.
func TestRouterSingleTimeoutPhases(t *testing.T) {
	for _, stage := range []string{"resolve", "append-admission", "local-submit", "local-wait"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := fmt.Errorf("controlled deadline: %w", context.DeadlineExceeded)
				if stage == "append-admission" {
					cause = fmt.Errorf("origin refusal: %w", errors.Join(ErrAppendNotSubmitted, context.DeadlineExceeded))
				}
				target := routerTarget("phase", 2, 7)
				r := NewRouter(RouterOptions{LocalNodeID: 7, MaxRouteAttempts: 1,
					Resolver: routingPhaseResolver(func(context.Context, ChannelID) (AuthorityTarget, error) {
						time.Sleep(2 * time.Millisecond)
						if stage == "resolve" {
							return AuthorityTarget{}, cause
						}
						return target, nil
					}),
					Local: routingPhaseLocal(func(context.Context, AuthorityTarget, []SendBatchItem) (*Future, error) {
						time.Sleep(5 * time.Millisecond)
						if stage == "local-submit" {
							return nil, cause
						}
						f := newFuture(1)
						go func() {
							time.Sleep(7 * time.Millisecond)
							f.complete([]SendBatchItemResult{{Result: SendResult{MessageID: 31}, Err: cause}})
						}()
						return f, nil
					})})
				item := routerItem("sender", "phase", 2)
				item.AppendAdmission = func(context.Context) error {
					time.Sleep(3 * time.Millisecond)
					if stage == "append-admission" {
						return cause
					}
					return nil
				}
				out := r.SendBatch([]SendBatchItem{item})
				if len(out) != 1 || !errors.Is(out[0].Err, context.DeadlineExceeded) || out[0].Err.Error() != cause.Error() {
					t.Fatalf("original error changed: %+v", out)
				}
				if errors.Is(out[0].Err, ErrAppendNotSubmitted) != (stage == "append-admission") {
					t.Fatal("non-submission proof changed")
				}
				want := contract.RoutingFailureDiagnostics{Resolve: 2 * time.Millisecond, Attempts: 1}
				if stage != "resolve" {
					want.AppendAdmission = 3 * time.Millisecond
				}
				if stage == "local-submit" || stage == "local-wait" {
					want.LocalSubmit = 5 * time.Millisecond
					want.LocalAttempts = 1
				}
				if stage == "local-wait" {
					want.LocalWait = 7 * time.Millisecond
					if out[0].Result.MessageID != 31 {
						t.Fatal("result identity changed")
					}
				}
				got, ok := contract.RoutingFailureDiagnosticsFromError(fmt.Errorf("outer: %w", out[0].Err))
				if !ok || got != want {
					t.Fatalf("routing timing=%+v present=%v want=%+v", got, ok, want)
				}
				got.Resolve = 99 * time.Second
				again, _ := contract.RoutingFailureDiagnosticsFromError(out[0].Err)
				if again != want {
					t.Fatal("extraction exposed mutable snapshot")
				}
			})
		})
	}
}

func TestRouterTimeoutMeasuresRealGroupAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstEntered := make(chan struct{})
		firstDone := make(chan struct{})
		future := newFuture(1)
		r := NewRouter(RouterOptions{LocalNodeID: 7, MaxConcurrentGroups: 1, MaxRouteAttempts: 1,
			Resolver: routingPhaseResolver(func(_ context.Context, id ChannelID) (AuthorityTarget, error) {
				if id.ID == "second" {
					time.Sleep(2 * time.Millisecond)
				}
				return routerTarget(id.ID, 2, 7), nil
			}),
			Local: routingPhaseLocal(func(_ context.Context, target AuthorityTarget, _ []SendBatchItem) (*Future, error) {
				if target.ChannelID.ID != "first" {
					t.Error("blocked call reached local provider")
				}
				close(firstEntered)
				return future, nil
			})})
		go func() { defer close(firstDone); r.SendBatch([]SendBatchItem{routerItem("sender", "first", 2)}) }()
		<-firstEntered
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Millisecond)
		defer cancel()
		item := routerItem("sender", "second", 2)
		item.Context = ctx
		out := r.SendBatch([]SendBatchItem{item})
		future.complete([]SendBatchItemResult{{}})
		<-firstDone // join before any fatal assertion
		if len(out) != 1 || !errors.Is(out[0].Err, context.DeadlineExceeded) {
			t.Fatalf("unexpected result: %+v", out)
		}
		want := contract.RoutingFailureDiagnostics{Resolve: 2 * time.Millisecond, GroupAdmission: 5 * time.Millisecond, Attempts: 1}
		got, ok := contract.RoutingFailureDiagnosticsFromError(out[0].Err)
		if !ok || got != want {
			t.Fatalf("group admission=%+v present=%v want=%+v", got, ok, want)
		}
	})
}

func TestRouterTimeoutSumsRetriesAndPreservesAmbiguity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resolves := 0
		remoteCalls := 0
		localCalls := 0
		cause := context.DeadlineExceeded
		r := NewRouter(RouterOptions{LocalNodeID: 7, ForwardTimeout: 5 * time.Millisecond, RetryBackoff: 3 * time.Millisecond,
			Resolver: routingPhaseResolver(func(context.Context, ChannelID) (AuthorityTarget, error) {
				time.Sleep(2 * time.Millisecond)
				resolves++
				if resolves == 1 {
					return routerTarget("retry", 2, 8), nil
				}
				return routerTarget("retry", 2, 7), nil
			}),
			Remote: routingPhaseRemote(func(ctx context.Context, _ AuthorityTarget, items []SendBatchItem) []SendBatchItemResult {
				remoteCalls++
				<-ctx.Done()
				return []SendBatchItemResult{{Err: ctx.Err()}}
			}),
			Local: routingPhaseLocal(func(context.Context, AuthorityTarget, []SendBatchItem) (*Future, error) {
				localCalls++
				time.Sleep(7 * time.Millisecond)
				return nil, cause
			})})
		item := routerItem("sender", "retry", 2)
		item.Command.ClientMsgNo = "stable-idempotency-key"
		out := r.SendBatch([]SendBatchItem{item})
		if len(out) != 1 || !errors.Is(out[0].Err, context.DeadlineExceeded) || errors.Is(out[0].Err, ErrAppendNotSubmitted) || errors.Is(out[0].Err, errRouterSubmissionUnknown) {
			t.Fatalf("ambiguity changed: %+v", out)
		}
		want := contract.RoutingFailureDiagnostics{Resolve: 4 * time.Millisecond, Forward: 5 * time.Millisecond, RetryDelay: 3 * time.Millisecond, LocalSubmit: 7 * time.Millisecond, Attempts: 2, LocalAttempts: 1, RemoteAttempts: 1}
		got, ok := contract.RoutingFailureDiagnosticsFromError(out[0].Err)
		if !ok || got != want || resolves != 2 || remoteCalls != 1 || localCalls != 1 {
			t.Fatalf("retry timing=%+v present=%v calls=%d/%d/%d", got, ok, resolves, remoteCalls, localCalls)
		}
		first := got
		_ = r.SendBatch([]SendBatchItem{item})
		again, _ := contract.RoutingFailureDiagnosticsFromError(out[0].Err)
		if again != first {
			t.Fatal("later invocation mutated first timing")
		}
	})
}

func TestRouterRoutingDiagnosticsPreserveSuppressedNonSubmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resolves := 0
		r := NewRouter(RouterOptions{LocalNodeID: 7, ForwardTimeout: time.Millisecond, RetryBackoff: time.Millisecond,
			Resolver: routingPhaseResolver(func(context.Context, ChannelID) (AuthorityTarget, error) {
				resolves++
				if resolves == 1 {
					return routerTarget("proof", 2, 8), nil
				}
				return routerTarget("proof", 2, 7), nil
			}),
			Remote: routingPhaseRemote(func(ctx context.Context, _ AuthorityTarget, _ []SendBatchItem) []SendBatchItemResult {
				<-ctx.Done()
				return []SendBatchItemResult{{Err: ctx.Err()}}
			}),
			Local: routingPhaseLocal(func(context.Context, AuthorityTarget, []SendBatchItem) (*Future, error) {
				return nil, errors.Join(ErrAppendNotSubmitted, context.DeadlineExceeded)
			})})
		item := routerItem("sender", "proof", 2)
		item.Command.ClientMsgNo = "stable-key"
		out := r.SendBatch([]SendBatchItem{item})
		if len(out) != 1 || !errors.Is(out[0].Err, ErrAppendFailed) || errors.Is(out[0].Err, ErrAppendNotSubmitted) || errors.Is(out[0].Err, context.DeadlineExceeded) {
			t.Fatalf("existing uncertainty suppression changed: %+v", out)
		}
		if _, ok := contract.RoutingFailureDiagnosticsFromError(out[0].Err); ok {
			t.Fatal("non-timeout uncertainty became a measured timeout")
		}
	})
}

func TestRouterForwardAttemptTimeoutWithoutReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		r := NewRouter(RouterOptions{LocalNodeID: 7, ForwardTimeout: 7 * time.Millisecond,
			Resolver: routingPhaseResolver(func(context.Context, ChannelID) (AuthorityTarget, error) {
				time.Sleep(2 * time.Millisecond)
				return routerTarget("remote", 2, 8), nil
			}),
			Remote: routingPhaseRemote(func(ctx context.Context, _ AuthorityTarget, _ []SendBatchItem) []SendBatchItemResult {
				calls++
				<-ctx.Done()
				return []SendBatchItemResult{{Err: ctx.Err()}}
			})})
		item := routerItem("sender", "remote", 2)
		item.Command.ClientMsgNo = ""
		out := r.SendBatch([]SendBatchItem{item})
		want := contract.RoutingFailureDiagnostics{Resolve: 2 * time.Millisecond, Forward: 7 * time.Millisecond, Attempts: 1, RemoteAttempts: 1}
		got, ok := contract.RoutingFailureDiagnosticsFromError(out[0].Err)
		if !ok || got != want || calls != 1 || !errors.Is(out[0].Err, context.DeadlineExceeded) {
			t.Fatalf("forward=%+v present=%v calls=%d error=%v", got, ok, calls, out[0].Err)
		}
	})
}

func TestRouterRoutingDiagnosticsAbsenceAndMeasuredZero(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		count int
	}{
		{"success", nil, 1}, {"canceled", context.Canceled, 1}, {"busy", ErrBackpressured, 1}, {"multi-timeout", context.DeadlineExceeded, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := NewRouter(RouterOptions{LocalNodeID: 7, MaxRouteAttempts: 1,
					Resolver: routingPhaseResolver(func(context.Context, ChannelID) (AuthorityTarget, error) { return routerTarget("absent", 2, 7), nil }),
					Local: routingPhaseLocal(func(_ context.Context, _ AuthorityTarget, items []SendBatchItem) (*Future, error) {
						f := newFuture(len(items))
						out := make([]SendBatchItemResult, len(items))
						for i := range out {
							out[i].Err = tc.cause
						}
						f.complete(out)
						return f, nil
					})})
				items := make([]SendBatchItem, tc.count)
				for i := range items {
					items[i] = routerItem("sender", "absent", 2)
				}
				out := r.SendBatch(items)
				if len(out) != tc.count {
					t.Fatal("cardinality changed")
				}
				for _, x := range out {
					if _, ok := contract.RoutingFailureDiagnosticsFromError(x.Err); ok {
						t.Fatal("invented routing snapshot")
					}
					if !errors.Is(x.Err, tc.cause) {
						t.Fatalf("error changed: %v", x.Err)
					}
				}
			})
		})
	}
	t.Run("already-expired-single", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 0)
			defer cancel()
			item := routerItem("sender", "zero", 2)
			item.Context = ctx
			r := NewRouter(RouterOptions{})
			out := r.SendBatch([]SendBatchItem{item})
			got, ok := contract.RoutingFailureDiagnosticsFromError(out[0].Err)
			if !ok || got != (contract.RoutingFailureDiagnostics{}) || !errors.Is(out[0].Err, context.DeadlineExceeded) {
				t.Fatalf("zero=%+v present=%v error=%v", got, ok, out[0].Err)
			}
		})
	})
}

type routingPhaseResolver func(context.Context, ChannelID) (AuthorityTarget, error)

func (f routingPhaseResolver) ResolveAppendAuthority(c context.Context, id ChannelID) (AuthorityTarget, error) {
	return f(c, id)
}

type routingPhaseLocal func(context.Context, AuthorityTarget, []SendBatchItem) (*Future, error)

func (f routingPhaseLocal) SubmitLocal(c context.Context, t AuthorityTarget, i []SendBatchItem) (*Future, error) {
	return f(c, t, i)
}

type routingPhaseRemote func(context.Context, AuthorityTarget, []SendBatchItem) []SendBatchItemResult

func (f routingPhaseRemote) ForwardSendBatch(c context.Context, t AuthorityTarget, i []SendBatchItem) []SendBatchItemResult {
	return f(c, t, i)
}
