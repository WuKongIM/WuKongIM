package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

type completionDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c completionDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func publishCompletionAt(c *Completion, response Response, at time.Time, contextErr error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishLocked(response, at, contextErr)
}

func resolveCompletionWith(c *Completion, contextErr, connectionErr error) (CompletionResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolveLocked(contextErr, connectionErr)
}

func TestCompletionStrictCopyEndDeadlineAndSendGate(t *testing.T) {
	deadline := time.Unix(100, 0)
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{{"timely", deadline.Add(-time.Nanosecond), true}, {"equal", deadline, false}, {"late-copy", deadline.Add(time.Nanosecond), false}} {
		t.Run(tc.name, func(t *testing.T) {
			owner := NewCompletion(completionDeadlineContext{Context: context.Background(), deadline: deadline})
			timing := &ReceiveTiming{FrameReady: deadline.Add(-time.Second), ObserverDone: deadline.Add(-time.Second)}
			body := []byte("copied before owner transition")
			accepted := publishCompletionAt(owner, Response{Payload: body, ReceiveTiming: timing}, tc.at, nil)
			if accepted != tc.want {
				t.Fatal("frame-ready was promoted to copy/commit completion")
			}
			if _, ready := resolveCompletionWith(owner, context.DeadlineExceeded, nil); ready {
				t.Fatal("response bypassed unresolved Send result")
			}
			owner.SendResult(nil)
			got, ready := resolveCompletionWith(owner, context.DeadlineExceeded, nil)
			if !ready {
				t.Fatal("completed owner did not resolve")
			}
			if tc.want {
				if got.LocalContext || got.Response.Err != nil || &got.Response.Payload[0] != &body[0] || !timing.Notified.Equal(tc.at) {
					t.Fatal("timely candidate changed payload/receipt or lost to timer wakeup")
				}
			} else if !got.LocalContext || !errors.Is(got.Response.Err, context.DeadlineExceeded) || got.Response.Payload != nil || got.Response.ReceiveTiming != nil || !timing.Notified.IsZero() {
				t.Fatal("late/equal candidate acquired a usable value or receive evidence")
			}
		})
	}
}

func TestCompletionSendErrorOverridesOnlyBeforeFirstSendDecision(t *testing.T) {
	owner := NewCompletion(context.Background())
	owner.Publish(Response{Payload: []byte("candidate"), ReceiveTiming: &ReceiveTiming{}})
	sendErr := errors.New("original send failure")
	owner.SendResult(sendErr)
	owner.SendResult(nil)
	got, ready := owner.Resolve(nil)
	if !ready || got.LocalContext || got.Response.Err != sendErr || got.Response.Payload != nil || got.Response.ReceiveTiming != nil {
		t.Fatal("staged response bypassed original Send failure")
	}
	if owner.Publish(Response{Payload: []byte("late")}) {
		t.Fatal("failed Send owner was revived")
	}
}

func TestCompletionCancellationConnectionAndDuplicateOrdering(t *testing.T) {
	for _, first := range []string{"reply", "cancel", "stop"} {
		t.Run(first, func(t *testing.T) {
			owner := NewCompletion(context.Background())
			owner.SendResult(nil)
			if first == "reply" {
				owner.Publish(Response{Payload: []byte("first")})
			} else if first == "cancel" {
				resolveCompletionWith(owner, context.Canceled, nil)
			} else {
				owner.Fail(core.ErrStopped)
			}
			owner.Fail(core.ErrStopped)
			if owner.Publish(Response{Payload: []byte("duplicate/late")}) {
				t.Fatal("terminal owner accepted a later frame")
			}
			got, ready := resolveCompletionWith(owner, context.Canceled, core.ErrStopped)
			if !ready || first == "reply" && (string(got.Response.Payload) != "first" || got.Response.Err != nil || got.LocalContext) || first == "cancel" && (!errors.Is(got.Response.Err, core.ErrCanceled) || !got.LocalContext) || first == "stop" && (!errors.Is(got.Response.Err, core.ErrStopped) || got.LocalContext) {
				t.Fatalf("first owner decision %s was changed: %+v", first, got)
			}
		})
	}
	owner := NewCompletion(context.Background())
	owner.SendResult(nil)
	if publishCompletionAt(owner, Response{Payload: []byte("after cancel")}, time.Time{}, context.Canceled) {
		t.Fatal("observed explicit cancellation became a response")
	}
}

func TestCompletionBorrowedReaderCannotReviveExpiredOwner(t *testing.T) {
	table := NewPendingTable(16)
	owner := NewCompletion(context.Background())
	owner.SendResult(nil)
	table.Store(1, owner)
	reader := table.Load(1)
	got, ready := resolveCompletionWith(owner, context.DeadlineExceeded, nil)
	if !ready || !got.LocalContext || !errors.Is(got.Response.Err, context.DeadlineExceeded) {
		t.Fatal("timeout while reader was copying did not settle owner")
	}
	table.DeleteOwner(1, owner)
	if reader.Publish(Response{Payload: []byte("copy finished later")}) || table.Len() != 0 || table.Load(1) != nil {
		t.Fatal("borrowed reader resurrected canceled index/owner")
	}
}

func TestCompletionPreservesEveryTimelyRemoteError(t *testing.T) {
	for _, code := range []string{core.RemoteErrorCodeGeneric, core.RemoteErrorCodeTimeout, core.RemoteErrorCodeCanceled, core.RemoteErrorCodeBusy, core.RemoteErrorCodeStopped} {
		owner := NewCompletion(context.Background())
		owner.SendResult(nil)
		remote := core.RemoteError{Code: code, Message: "bounded fixture"}
		if !owner.Publish(Response{Err: remote}) {
			t.Fatal("timely remote error rejected")
		}
		got, ready := owner.Resolve(nil)
		if !ready || got.LocalContext || got.Response.Err != remote {
			t.Fatal("remote error was normalized into local timeout/cancel")
		}
	}
}

type completionLockProbeContext struct {
	context.Context
	owner **Completion
	bad   *atomic.Bool
}

func (c completionLockProbeContext) Err() error {
	if *c.owner != nil {
		if !(*c.owner).mu.TryLock() {
			c.bad.Store(true)
		} else {
			(*c.owner).mu.Unlock()
		}
	}
	return nil
}

func TestCompletionNeverCallsContextMethodsUnderOwnerLock(t *testing.T) {
	var owner *Completion
	var bad atomic.Bool
	owner = NewCompletion(completionLockProbeContext{Context: context.Background(), owner: &owner, bad: &bad})
	owner.SendResult(nil)
	owner.Resolve(nil)
	owner.Publish(Response{})
	owner.Resolve(nil)
	if bad.Load() {
		t.Fatal("arbitrary context method ran while owner locked")
	}
}

func TestCompletionConcurrentTerminalDecisionsSettleOnce(t *testing.T) {
	for i := 0; i < 100; i++ {
		owner := NewCompletion(context.Background())
		owner.SendResult(nil)
		var work sync.WaitGroup
		work.Add(3)
		go func() { defer work.Done(); owner.Publish(Response{Payload: []byte("reply")}) }()
		go func() { defer work.Done(); resolveCompletionWith(owner, context.Canceled, nil) }()
		go func() { defer work.Done(); owner.Fail(core.ErrStopped) }()
		work.Wait()
		first, ready := owner.Resolve(nil)
		second, again := owner.Resolve(nil)
		if !ready || !again || first.Response.Err != second.Response.Err || first.LocalContext != second.LocalContext || string(first.Response.Payload) != string(second.Response.Payload) {
			t.Fatal("concurrent terminal paths changed the settled result")
		}
		select {
		case <-owner.Done():
		default:
			t.Fatal("terminal owner never notified its waiter")
		}
	}
}

func TestCompletionUnknownSendGatePreservesPriorOwnerDecision(t *testing.T) {
	for _, first := range []string{"reply", "cancel", "stop"} {
		for _, sendFails := range []bool{false, true} {
			owner := NewCompletion(context.Background())
			if first == "reply" {
				owner.Publish(Response{Payload: []byte("candidate"), ReceiveTiming: &ReceiveTiming{}})
			}
			if first == "cancel" {
				resolveCompletionWith(owner, context.Canceled, nil)
			}
			if first == "stop" {
				owner.Fail(core.ErrStopped)
			}
			owner.Fail(core.ErrStopped)
			if _, ready := resolveCompletionWith(owner, context.Canceled, nil); ready {
				t.Fatal("unknown Send gate exposed a result")
			}
			sendErr := errors.New("original send failure")
			if sendFails {
				owner.SendResult(sendErr)
			} else {
				owner.SendResult(nil)
			}
			got, ready := owner.Resolve(nil)
			if !ready {
				t.Fatal("Send result did not release settled owner")
			}
			if sendFails {
				if got.Response.Err != sendErr || got.Response.Payload != nil || got.Response.ReceiveTiming != nil {
					t.Fatal("Send failure lost precedence")
				}
			} else if first == "reply" && (got.Response.Err != nil || string(got.Response.Payload) != "candidate") || first == "cancel" && !errors.Is(got.Response.Err, core.ErrCanceled) || first == "stop" && !errors.Is(got.Response.Err, core.ErrStopped) {
				t.Fatal("Send success changed earlier owner decision")
			}
			if owner.Publish(Response{Payload: []byte("revive")}) {
				t.Fatal("settled gate owner revived")
			}
		}
	}
}

func TestCompletionDeadlineNormalizationRetainsMonotonicBudget(t *testing.T) {
	now := time.Now()
	want := now.Add(time.Second)
	wallOnly := time.Unix(want.Unix(), int64(want.Nanosecond()))
	for _, deadline := range []time.Time{want, wallOnly} {
		if got := normalizeCompletionDeadline(deadline, now); got != want || got.Sub(now) != time.Second {
			t.Fatal("deadline lost the captured monotonic reference or extended its budget")
		}
	}
}
