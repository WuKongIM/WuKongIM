//go:build integration

package rpc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

type dispatchObserver func(core.Event)

func (f dispatchObserver) ObserveTransport(event core.Event) { f(event) }

func dispatchRequest(ctx context.Context, body string) (Request, <-chan *rpctrace.Sample) {
	recorder, _ := rpctrace.NewRecorder(1)
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 14)
	trace.Bind([16]byte{1}, 1)
	trace.RecordOperation(11)
	result := make(chan *rpctrace.Sample, 1)
	return Request{Context: ctx, Payload: core.CopyOwnedBuffer([]byte(body)), Dispatch: trace.StartDispatch(), TraceQueue: trace.Begin(rpctrace.StageServiceQueue), RespondBorrowed: func(response Response) {
		result <- trace.Finish(core.RPCTraceResult(response.Err))
	}}, result
}

func awaitDispatch(t *testing.T, result <-chan *rpctrace.Sample, path rpctrace.DispatchPath) *rpctrace.Sample {
	t.Helper()
	select {
	case sample := <-result:
		if sample.Dispatch == nil || sample.Dispatch.TerminalPath != path {
			t.Fatalf("dispatch path=%+v want %s", sample.Dispatch, path)
		}
		return sample
	case <-time.After(5 * time.Second):
		t.Fatal("bounded service fixture did not complete")
		return nil
	}
}

// This existing Service factory seam exposes synchronous bookkeeping observers;
// production Server wraps its observer in ObserverDrain and behaves differently.
func TestDispatchWorkerBudgetCheckAfterControlledBookkeeping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked, release := make(chan struct{}), make(chan struct{})
	var called atomic.Bool
	service := NewService(14, func(context.Context, []byte) ([]byte, error) { called.Store(true); return nil, nil }, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64}, dispatchObserver(func(event core.Event) {
		if event.Name == "service_inflight" && event.Inflight == 1 {
			close(blocked)
			<-release
		}
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		service.Stop()
	}()
	req, result := dispatchRequest(ctx, "read")
	if err := service.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, blocked)
	cancel()
	close(release)
	sample := awaitDispatch(t, result, rpctrace.DispatchPathWorkerRejected)
	want := uint16(1<<rpctrace.DispatchWorkerEnter | 1<<rpctrace.DispatchWorkerBookkeepingDone | 1<<rpctrace.DispatchErrorDeliver)
	if called.Load() || sample.Result != rpctrace.ResultCanceled || sample.Dispatch.KnownMask&want != want || sample.Dispatch.KnownMask&(1<<rpctrace.DispatchHandlerEnter) != 0 {
		t.Fatal("worker precheck failure invented handler work or lost bookkeeping evidence")
	}
}

func TestDispatchQueueTimerAndExecutorBudgetAreDifferentExits(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "fifo-timer", true: "shared-executor"}[shared], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			pool, err := NewExecutor(1, nil)
			if err != nil {
				t.Fatal(err)
			}
			var service *Service
			opts := core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64, QueueTimeout: 20 * time.Millisecond}
			if shared {
				if err := pool.Submit(&serviceTask{runFunc: func() { close(entered); <-release }}); err != nil {
					t.Fatal(err)
				}
				service = NewServiceWithExecutor(14, func(context.Context, []byte) ([]byte, error) { t.Error("expired call executed"); return nil, nil }, opts, nil, pool)
			} else {
				service = NewService(14, func(context.Context, []byte) ([]byte, error) { close(entered); <-release; return nil, nil }, opts, nil)
				if err := service.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("first"))}); err != nil {
					t.Fatal(err)
				}
			}
			defer func() { close(release); service.Stop(); pool.Stop() }()
			waitClosed(t, entered)
			req, result := dispatchRequest(context.Background(), "expired")
			if err := service.Enqueue(req); err != nil {
				t.Fatal(err)
			}
			path := rpctrace.DispatchPathQueueExpiry
			if shared {
				path = rpctrace.DispatchPathSubmitRejected
			}
			sample := awaitDispatch(t, result, path)
			if sample.Result != rpctrace.ResultTimeout || !sample.Dispatch.QueueDeadlineKnown || sample.Dispatch.KnownMask&(1<<rpctrace.DispatchHandlerEnter|1<<rpctrace.DispatchContextCancelCallback) != 0 {
				t.Fatal("queue budget or callback source changed")
			}
			if shared && sample.Dispatch.KnownMask&(1<<rpctrace.DispatchDequeue) == 0 || !shared && sample.Dispatch.KnownMask&(1<<rpctrace.DispatchExpiryUnlink|1<<rpctrace.DispatchExpiryObserversDone) != 1<<rpctrace.DispatchExpiryUnlink|1<<rpctrace.DispatchExpiryObserversDone {
				t.Fatal("timer and executor failures lost their distinct ownership boundaries")
			}
		})
	}
}

func TestDispatchLateEnqueueObserverDoesNotRewriteFinishedSample(t *testing.T) {
	entered, releaseFirst := make(chan struct{}), make(chan struct{})
	blocked, releaseObserver := make(chan struct{}), make(chan struct{})
	service := NewService(14, func(_ context.Context, body []byte) ([]byte, error) {
		if string(body) == "first" {
			close(entered)
			<-releaseFirst
		}
		return body, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64}, dispatchObserver(func(event core.Event) {
		if event.Name == "service_admission" && event.Bytes == len("second") {
			close(blocked)
			<-releaseObserver
		}
	}))
	defer func() {
		for _, release := range []chan struct{}{releaseFirst, releaseObserver} {
			select {
			case <-release:
			default:
				close(release)
			}
		}
		service.Stop()
	}()
	if err := service.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("first"))}); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered)
	req, result := dispatchRequest(context.Background(), "second")
	enqueued := make(chan error, 1)
	go func() { enqueued <- service.Enqueue(req) }()
	waitClosed(t, blocked)
	close(releaseFirst)
	sample := awaitDispatch(t, result, rpctrace.DispatchPathHandler)
	before := *sample.Dispatch
	if sample.Dispatch.KnownMask&(1<<rpctrace.DispatchEnqueueObserversDone|1<<rpctrace.DispatchEnqueueWakeDone) != 0 {
		t.Fatal("unfinished enqueuer invented observer or wake completion")
	}
	close(releaseObserver)
	if err := <-enqueued; err != nil || *sample.Dispatch != before {
		t.Fatal("late enqueuer changed an already published sample")
	}
}

func TestDispatchQueueDeadlineUsesActualEarlierCallerOrPolicy(t *testing.T) {
	for _, which := range []string{"caller", "queue", "none"} {
		t.Run(which, func(t *testing.T) {
			ctx := context.Background()
			caller := time.Now().Add(2 * time.Hour)
			cancel := func() {}
			opts := core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64}
			if which != "none" {
				opts.QueueTimeout = time.Hour
				if which == "caller" {
					caller = time.Now().Add(time.Minute)
				}
				ctx, cancel = context.WithDeadline(ctx, caller)
			}
			defer cancel()
			service := NewService(14, func(context.Context, []byte) ([]byte, error) { return nil, nil }, opts, nil)
			defer service.Stop()
			req, result := dispatchRequest(ctx, "read")
			if err := service.Enqueue(req); err != nil {
				t.Fatal(err)
			}
			sample := awaitDispatch(t, result, rpctrace.DispatchPathHandler)
			d := sample.Dispatch
			if which == "none" {
				if d.QueueDeadlineKnown {
					t.Fatal("unbounded queue invented a deadline")
				}
			} else if !d.QueueDeadlineKnown || d.QueueDeadline <= sample.Duration || which == "caller" && d.QueueDeadline != caller.Sub(sample.At) || which == "queue" && (d.QueueDeadline < time.Hour || d.QueueDeadline >= caller.Sub(sample.At)) {
				t.Fatal("deadline did not preserve the admitted entry's earlier caller/policy bound")
			}
		})
	}
}
