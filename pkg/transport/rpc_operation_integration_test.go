//go:build integration

package transport

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestRPCOperationOnlyClassifiesAdmittedServerObservations(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enabled   bool
		every     uint32
		operation uint8
		wantCalls int32
	}{{"disabled", false, 0, 11, 0}, {"unsampled", true, 0, 11, 0}, {"selected", true, 1, 11, 1}, {"unknown", true, 1, 0, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var classified atomic.Int32
			opts := traceOptions()
			opts.TraceOperationFor = func(b []byte) uint8 { classified.Add(1); return tc.operation }
			obs := &traceObserver{}
			c, s := tracePair(t, tc.enabled, tc.every, opts, func(_ context.Context, b []byte) ([]byte, error) { return b, nil }, obs)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got, err := traceCall(ctx, c, 0)
			if err != nil || string(got) != "unchanged" {
				t.Fatal("operation observation changed business bytes/result")
			}
			c.Stop()
			s.Stop()
			if classified.Load() != tc.wantCalls {
				t.Fatalf("classifier calls%d want%d", classified.Load(), tc.wantCalls)
			}
			for _, sample := range obs.snapshot() {
				want := uint8(0)
				if sample.Side == rpctrace.SideServer {
					want = tc.operation
				}
				if sample.Operation != want {
					t.Fatal("operation invented or missing")
				}
				if want == 0 {
					if sample.Dispatch != nil {
						t.Fatal("unknown/client sample acquired dispatch clocks")
					}
				} else {
					d := requireDispatch(t, sample, rpctrace.DispatchPathHandler)
					requireDispatchMarks(t, d, rpctrace.DispatchEnqueueEnter, rpctrace.DispatchEnqueueLocked, rpctrace.DispatchDequeue, rpctrace.DispatchDequeueObserversDone, rpctrace.DispatchExecutorSubmit, rpctrace.DispatchWorkerEnter, rpctrace.DispatchWorkerBookkeepingDone, rpctrace.DispatchHandlerEnter)
					if !d.QueueDeadlineKnown || d.QueueDeadline <= d.Offsets[rpctrace.DispatchHandlerEnter] {
						t.Fatal("actual queue deadline was lost or mistaken for elapsed work")
					}
				}
			}
			if tc.every == 1 {
				samples := obs.snapshot()
				if len(samples) != 2 || samples[0].TraceID != samples[1].TraceID || samples[0].Unbound || samples[1].Unbound {
					t.Fatal("exact endpoint pairing changed")
				}
			}
		})
	}
}

func TestRPCOperationQueueCancelAndBusyDoNotRequireHandler(t *testing.T) {
	obs := &traceObserver{queue: make(chan struct{}, 16), canceled: make(chan struct{}, 8)}
	entered, release := make(chan struct{}), make(chan struct{})
	var handlers, classified atomic.Int32
	opts := traceOptions()
	opts.QueueSize = 1
	opts.TraceOperationFor = func(b []byte) uint8 {
		classified.Add(1)
		if len(b) > 0 {
			return b[0]
		}
		return 0
	}
	c, s := tracePair(t, true, 1, opts, func(_ context.Context, b []byte) ([]byte, error) {
		handlers.Add(1)
		close(entered)
		<-release
		return b, nil
	}, obs)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := func(ctx context.Context, op byte) error {
		body := []byte{op}
		got, err := c.Call(ctx, 2, 0, PriorityRPC, 1, body)
		if err == nil && !bytes.Equal(got, body) {
			t.Error("request payload changed")
		}
		return err
	}
	first := make(chan error, 1)
	go func() { first <- call(ctx, 7) }()
	traceWait(t, obs.queue)
	traceWait(t, entered)
	qctx, qcancel := context.WithCancel(ctx)
	defer qcancel()
	second := make(chan error, 1)
	go func() { second <- call(qctx, 11) }()
	traceWait(t, obs.queue)
	if err := call(ctx, 9); !errors.Is(err, ErrBusy) {
		t.Fatalf("queue budget changed: %v", err)
	}
	qcancel()
	if err := <-second; !errors.Is(err, ErrCanceled) {
		t.Fatalf("queued cancellation changed: %v", err)
	}
	traceWait(t, obs.canceled)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	c.Stop()
	s.Stop()
	if handlers.Load() != 1 || classified.Load() != 3 {
		t.Fatalf("handlers/classifiers=%d/%d", handlers.Load(), classified.Load())
	}
	found := map[uint8]rpctrace.Result{}
	for _, sample := range obs.snapshot() {
		if sample.Side == rpctrace.SideServer {
			found[sample.Operation] = sample.Result
			switch sample.Operation {
			case 7:
				requireDispatch(t, sample, rpctrace.DispatchPathHandler)
			case 11:
				d := requireDispatch(t, sample, rpctrace.DispatchPathContextExpiry)
				requireDispatchMarks(t, d, rpctrace.DispatchContextCancelCallback, rpctrace.DispatchExpiryUnlink, rpctrace.DispatchExpiryObserversDone, rpctrace.DispatchErrorDeliver)
				if d.KnownMask&(1<<rpctrace.DispatchDequeue|1<<rpctrace.DispatchWorkerEnter|1<<rpctrace.DispatchHandlerEnter) != 0 {
					t.Fatal("canceled FIFO owner invented dequeue/worker/handler entry")
				}
			case 9:
				d := requireDispatch(t, sample, rpctrace.DispatchPathEnqueueRejected)
				requireDispatchMarks(t, d, rpctrace.DispatchEnqueueEnter, rpctrace.DispatchEnqueueLocked, rpctrace.DispatchErrorDeliver)
				if d.QueueDeadlineKnown {
					t.Fatal("rejected admission invented a queue-owned deadline")
				}
			}
		} else if sample.Operation != 0 {
			t.Fatal("client inferred operation")
		} else if sample.Dispatch != nil {
			t.Fatal("client retained server dispatch")
		}
	}
	if found[7] != rpctrace.ResultOK || found[11] != rpctrace.ResultCanceled || found[9] != rpctrace.ResultBusy {
		t.Fatalf("pre-handler outcomes lost operation: %v", found)
	}
}

func TestRPCOperationMissingServiceAndRecorderDropStayUnknown(t *testing.T) {
	for _, dropped := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-service", true: "recorder-drop"}[dropped], func(t *testing.T) {
			var classified atomic.Int32
			opts := traceOptions()
			opts.TraceOperationFor = func([]byte) uint8 { classified.Add(1); return 11 }
			obs := &traceObserver{}
			c, s := tracePair(t, true, 1, opts, func(_ context.Context, b []byte) ([]byte, error) { return b, nil }, obs)
			var held []*rpctrace.Trace
			if dropped {
				for i := 0; i < 128; i++ {
					held = append(held, s.traceRecorder.Start(context.Background(), rpctrace.SideServer, 1))
				}
			}
			defer func() {
				for _, trace := range held {
					trace.Finish(rpctrace.ResultOK)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			service := uint16(2)
			if dropped {
				service = 1
			}
			got, err := c.Call(ctx, 2, 0, PriorityRPC, service, []byte("unchanged"))
			if dropped {
				if err != nil || string(got) != "unchanged" || s.traceRecorder.Dropped() != 1 {
					t.Fatal("observation cap changed business result")
				}
			} else if err == nil {
				t.Fatal("missing handler became success")
			}
			for _, trace := range held {
				trace.Finish(rpctrace.ResultOK)
			}
			c.Stop()
			s.Stop()
			if classified.Load() != 0 {
				t.Fatal("unregistered/unobserved request classified")
			}
			for _, sample := range obs.snapshot() {
				if sample.Operation != 0 {
					t.Fatal("missing evidence became known")
				}
				if sample.Dispatch != nil {
					t.Fatal("missing/dropped declaration enabled dispatch")
				}
				if dropped && sample.Side == rpctrace.SideServer {
					t.Fatal("dropped server produced sample")
				}
			}
		})
	}
}

func requireDispatch(t *testing.T, sample rpctrace.Sample, path rpctrace.DispatchPath) *rpctrace.DispatchSample {
	t.Helper()
	if sample.Dispatch == nil || sample.Dispatch.TerminalPath != path {
		t.Fatalf("dispatch path=%+v, want %s", sample.Dispatch, path)
	}
	for point, offset := range sample.Dispatch.Offsets {
		if sample.Dispatch.KnownMask&(1<<point) != 0 && (offset < 0 || offset > sample.Duration) {
			t.Fatal("dispatch mark exceeded its containing frozen trace")
		}
	}
	return sample.Dispatch
}

func requireDispatchMarks(t *testing.T, sample *rpctrace.DispatchSample, points ...rpctrace.DispatchPoint) {
	t.Helper()
	for _, point := range points {
		if sample.KnownMask&(1<<point) == 0 {
			t.Fatalf("dispatch point %d is unknown in %+v", point, sample)
		}
	}
}

func TestRPCDispatchPreservesStartedReadAndMutationCancellation(t *testing.T) {
	for _, operation := range []byte{7, 11} {
		t.Run(map[byte]string{7: "mutation", 11: "read"}[operation], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			observed := make(chan error, 1)
			opts := traceOptions()
			opts.TraceOperationFor = func(body []byte) uint8 { return body[0] }
			opts.CancelRunningFor = func(body []byte) bool { return body[0] == 11 }
			obs := &traceObserver{}
			client, server := tracePair(t, true, 1, opts, func(ctx context.Context, body []byte) ([]byte, error) {
				close(entered)
				if operation == 11 {
					<-ctx.Done()
				} else {
					<-release
				}
				observed <- ctx.Err()
				return body, nil
			}, obs)
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := client.Call(ctx, 2, 0, PriorityRPC, 1, []byte{operation}); result <- err }()
			traceWait(t, entered)
			cancel()
			if err := <-result; !errors.Is(err, ErrCanceled) {
				t.Fatalf("caller cancellation changed: %v", err)
			}
			close(release)
			select {
			case err := <-observed:
				if operation == 7 && err != nil || operation == 11 && !errors.Is(err, context.Canceled) {
					t.Fatalf("started request execution policy changed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("started request did not finish")
			}
			client.Stop()
			server.Stop()
			for _, sample := range obs.snapshot() {
				if sample.Side == rpctrace.SideServer {
					requireDispatch(t, sample, rpctrace.DispatchPathHandler)
				}
			}
		})
	}
}
