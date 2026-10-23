//go:build integration

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

// ackProgressWriter exposes the actual runtime reservation-to-write boundary.
type ackProgressWriter struct{ writes chan struct{} }

func (w *ackProgressWriter) WriteSession(context.Context, runtimedelivery.LocalSessionWrite) runtimedelivery.SessionWriteResult {
	w.writes <- struct{}{}
	return runtimedelivery.SessionWriteResult{Disposition: runtimedelivery.SessionWriteAccepted}
}

func newAckProgressRuntime(t *testing.T, collector *topCollector) (*runtimedelivery.Runtime, *ackProgressWriter) {
	t.Helper()
	writer := &ackProgressWriter{writes: make(chan struct{}, 16)}
	observer := (&App{topProvider: collector}).onlineDeliveryObserver()
	runtime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{
		LocalNodeID: 1, Workers: 1, SessionWriter: writer,
		AckObserver: observer, AckBatchObserver: observer, Observer: observer,
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAckProgressRuntime(t, runtime) })
	return runtime, writer
}

func stopAckProgressRuntime(t *testing.T, runtime *runtimedelivery.Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(ctx); err != nil {
		t.Errorf("Stop did not join runtime workers: %v", err)
	}
}

func ackProgressPush() onlinedelivery.OwnerPush {
	return onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "test", ChannelType: 1},
		Routes:      []onlinedelivery.Route{{UID: "test-recipient", SessionID: 1, OwnerNodeID: 1}},
	}
}

func ackProgressCommand() runtimedelivery.Recvack {
	return runtimedelivery.Recvack{UID: "test-recipient", SessionID: 1, MessageID: 1, MessageSeq: 1}
}

func requireAckProgressPush(t *testing.T, runtime *runtimedelivery.Runtime) {
	t.Helper()
	result, err := runtime.PushOwner(context.Background(), ackProgressPush())
	if err != nil || len(result.Accepted) != 1 || len(result.Dropped)+len(result.Retryable) != 0 {
		t.Fatalf("PushOwner = %+v, %v", result, err)
	}
}

func TestTopAckObserverProgressWhileCollectorIngestionLocked(t *testing.T) {
	for _, boundary := range []string{"recvack", "push_reservation_and_recvack"} {
		t.Run(boundary, func(t *testing.T) {
			collector := newAckGaugeCollector()
			runtime, writer := newAckProgressRuntime(t, collector)
			if boundary == "recvack" {
				requireAckProgressPush(t, runtime)
				<-writer.writes
			}
			collector.mu.Lock()
			locked := true
			var calls []<-chan struct{}
			// Release the simulated ingestion lock before joining callers, even
			// when old code fails the progress assertion; then runtime cleanup runs.
			defer func() {
				if locked {
					collector.mu.Unlock()
				}
				for _, done := range calls {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("public runtime caller did not join after lock release")
					}
				}
			}()
			var pushResult onlinedelivery.OwnerPushResult
			var pushErr error
			if boundary != "recvack" {
				done := make(chan struct{})
				calls = append(calls, done)
				go func() {
					defer close(done)
					pushResult, pushErr = runtime.PushOwner(context.Background(), ackProgressPush())
				}()
				select {
				case <-writer.writes:
				case <-time.After(200 * time.Millisecond):
					t.Fatal("PushOwner ACK reservation blocked on Top ingestion mutex before session write")
				}
			}
			ackDone := make(chan struct{})
			calls = append(calls, ackDone)
			var ackErr error
			go func() {
				defer close(ackDone)
				ackErr = runtime.Recvack(context.Background(), ackProgressCommand())
			}()
			select {
			case <-ackDone:
				if ackErr != nil {
					t.Fatal(ackErr)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("Recvack blocked on Top ingestion mutex")
			}
			collector.mu.Unlock()
			locked = false
			for _, done := range calls {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("public runtime caller did not finish")
				}
			}
			if boundary != "recvack" && (pushErr != nil || len(pushResult.Accepted) != 1 || len(pushResult.Dropped)+len(pushResult.Retryable) != 0) {
				t.Fatalf("PushOwner = %+v, %v", pushResult, pushErr)
			}
			collector.recordSampleAt(time.Unix(100, 0))
			collector.recordSampleAt(time.Unix(101, 0))
			requireAckGauge(t, collector, 0)
		})
	}
}

func TestTopAckObserverRuntimeResetAndRestart(t *testing.T) {
	collector := newAckGaugeCollector()
	runtime, _ := newAckProgressRuntime(t, collector)
	collector.recordSampleAt(time.Unix(100, 0))
	requireAckProgressPush(t, runtime)
	collector.recordSampleAt(time.Unix(101, 0))
	requireAckGauge(t, collector, 1)
	stopAckProgressRuntime(t, runtime)
	collector.recordSampleAt(time.Unix(102, 0))
	requireAckGauge(t, collector, 0)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireAckProgressPush(t, runtime)
	collector.recordSampleAt(time.Unix(103, 0))
	requireAckGauge(t, collector, 1)
	if err := runtime.Recvack(context.Background(), ackProgressCommand()); err != nil {
		t.Fatal(err)
	}
	collector.recordSampleAt(time.Unix(104, 0))
	requireAckGauge(t, collector, 0)
}

func TestTopAckGaugeConcurrentSamplingAndClone(t *testing.T) {
	collector := newAckGaugeCollector()
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			collector.SetDeliveryAckBindings(int64(i))
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			collector.recordSampleAt(time.Unix(int64(100+i), 0))
			copy := collector.clone()
			copy.recordSampleAt(time.Unix(int64(101+i), 0))
		}
	}()
	workers.Wait()
	collector.SetDeliveryAckBindings(0)
	collector.recordSampleAt(time.Unix(201, 0))
	requireAckGauge(t, collector, 0)
	copy := collector.clone()
	copy.recordSampleAt(time.Unix(202, 0))
	requireAckGauge(t, copy, 0)
}
