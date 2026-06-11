package rpc

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func BenchmarkExecutorSubmit(b *testing.B) {
	cases := []struct {
		name     string
		capacity int
	}{
		{name: "Capacity1", capacity: 1},
		{name: "CapacityGOMAXPROCS", capacity: runtime.GOMAXPROCS(0)},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			executor, err := NewExecutor(tc.capacity, nil)
			if err != nil {
				b.Fatalf("NewExecutor() error = %v", err)
			}
			defer func() {
				if err := executor.Stop(); err != nil {
					b.Fatalf("Stop() error = %v", err)
				}
			}()

			var wg sync.WaitGroup
			wg.Add(b.N)
			done := wg.Done

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkSubmitServiceTask(b, executor, &serviceTask{runFunc: done})
			}
			b.StopTimer()

			benchmarkWaitGroup(b, &wg)
		})
	}
}

func BenchmarkServiceRPCReplyPayloadSizes(b *testing.B) {
	cases := []struct {
		name string
		size int
	}{
		{name: "64B", size: 64},
		{name: "1KiB", size: 1 << 10},
		{name: "64KiB", size: 64 << 10},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			payload := bytes.Repeat([]byte("r"), tc.size)
			svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
				return payload, nil
			}, core.ServiceOptions{
				Concurrency:   max(1, runtime.GOMAXPROCS(0)),
				QueueSize:     4096,
				MaxQueueBytes: 128 << 20,
				MaxPayload:    128 << 20,
			}, nil)
			defer svc.Stop()

			reply := make(chan Response, 1)
			if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer(payload), Reply: reply}); err != nil {
				b.Fatalf("warm-up Enqueue() error = %v", err)
			}
			benchmarkWaitResponse(b, reply)

			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reply := make(chan Response, 1)
				if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer(payload), Reply: reply}); err != nil {
					b.Fatalf("Enqueue() error = %v", err)
				}
				resp := benchmarkWaitResponse(b, reply)
				if resp.Err != nil {
					b.Fatalf("response error = %v", resp.Err)
				}
			}
		})
	}
}

func BenchmarkServiceSendOnlyParallel(b *testing.B) {
	payload := bytes.Repeat([]byte("s"), 256)
	var handled atomic.Int64
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		handled.Add(1)
		return nil, nil
	}, core.ServiceOptions{
		Concurrency:   max(1, runtime.GOMAXPROCS(0)),
		QueueSize:     4096,
		MaxQueueBytes: 8 << 20,
		MaxPayload:    8 << 20,
	}, nil)
	defer svc.Stop()

	var accepted atomic.Int64
	var errCount atomic.Int64
	var firstErr atomic.Value
	var once sync.Once

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := benchmarkEnqueueWithBackpressure(svc, payload); err != nil {
				once.Do(func() {
					firstErr.Store(err.Error())
				})
				errCount.Add(1)
				return
			}
			accepted.Add(1)
		}
	})
	b.StopTimer()

	if errCount.Load() > 0 {
		b.Fatalf("Enqueue() errors = %d, first = %v", errCount.Load(), firstErr.Load())
	}
	benchmarkWaitAtomicAtLeast(b, &handled, accepted.Load())
}

func BenchmarkServiceSharedExecutorMultipleServices(b *testing.B) {
	const (
		serviceCount = 4
		concurrency  = 2
	)
	executor, err := NewExecutor(serviceCount*concurrency, nil)
	if err != nil {
		b.Fatalf("NewExecutor() error = %v", err)
	}
	defer func() {
		if err := executor.Stop(); err != nil {
			b.Fatalf("Stop() error = %v", err)
		}
	}()

	payload := bytes.Repeat([]byte("m"), 256)
	services := make([]*Service, 0, serviceCount)
	for i := 0; i < serviceCount; i++ {
		svc := NewServiceWithExecutor(uint16(i+1), func(context.Context, []byte) ([]byte, error) {
			return payload, nil
		}, core.ServiceOptions{
			Concurrency:   concurrency,
			QueueSize:     1024,
			MaxQueueBytes: 8 << 20,
			MaxPayload:    8 << 20,
		}, nil, executor)
		services = append(services, svc)
	}
	defer func() {
		for _, svc := range services {
			svc.Stop()
		}
	}()

	var serviceIndex atomic.Uint64
	var errCount atomic.Int64
	var firstErr atomic.Value
	var once sync.Once

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			svc := services[int(serviceIndex.Add(1))%len(services)]
			reply := make(chan Response, 1)
			if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer(payload), Reply: reply}); err != nil {
				once.Do(func() {
					firstErr.Store(err.Error())
				})
				errCount.Add(1)
				return
			}
			resp := benchmarkWaitResponse(b, reply)
			if resp.Err != nil {
				once.Do(func() {
					firstErr.Store(resp.Err.Error())
				})
				errCount.Add(1)
				return
			}
		}
	})
	b.StopTimer()

	if errCount.Load() > 0 {
		b.Fatalf("shared service errors = %d, first = %v", errCount.Load(), firstErr.Load())
	}
}

func benchmarkSubmitServiceTask(b *testing.B, executor *Executor, task *serviceTask) {
	b.Helper()
	for {
		err := executor.Submit(task)
		if err == nil {
			return
		}
		if !errors.Is(err, core.ErrBusy) {
			b.Fatalf("Submit() error = %v", err)
		}
		runtime.Gosched()
	}
}

func benchmarkEnqueueWithBackpressure(svc *Service, payload []byte) error {
	for {
		err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer(payload)})
		if err == nil {
			return nil
		}
		if !errors.Is(err, core.ErrBusy) {
			return err
		}
		runtime.Gosched()
	}
}

func benchmarkWaitResponse(b *testing.B, reply <-chan Response) Response {
	b.Helper()
	select {
	case resp := <-reply:
		return resp
	case <-time.After(5 * time.Second):
		b.Fatal("timed out waiting for service response")
		return Response{}
	}
}

func benchmarkWaitGroup(b *testing.B, wg *sync.WaitGroup) {
	b.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		b.Fatal("timed out waiting for executor tasks")
	}
}

func benchmarkWaitAtomicAtLeast(b *testing.B, value *atomic.Int64, want int64) {
	b.Helper()
	deadline := time.After(5 * time.Second)
	for value.Load() < want {
		select {
		case <-deadline:
			b.Fatalf("timed out waiting for handled count %d, want at least %d", value.Load(), want)
		default:
			runtime.Gosched()
		}
	}
}
