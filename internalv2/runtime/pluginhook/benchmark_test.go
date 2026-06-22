package pluginhook

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/stretchr/testify/require"
)

func BenchmarkPluginHookEnqueue(b *testing.B) {
	worker := NewWorker(Options{
		Usecase:   noopPersistAfterUsecase{},
		QueueSize: 1 << 20,
		Workers:   8,
		Timeout:   time.Second,
	})
	require.NoError(b, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	event := pluginevents.PersistAfterCommitted{MessageID: 1, Payload: bytes.Repeat([]byte("a"), 1024)}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			worker.EnqueuePersistAfter(context.Background(), event)
		}
	})
}

func BenchmarkPluginHookReceiveEnqueue(b *testing.B) {
	worker := NewWorker(Options{
		Usecase:        noopPersistAfterUsecase{},
		ReceiveUsecase: noopReceiveUsecase{},
		QueueSize:      1 << 20,
		Workers:        8,
		Timeout:        time.Second,
	})
	require.NoError(b, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	event := pluginevents.ReceiveOffline{MessageID: 1, MessageSeq: 1, UID: "bot", Payload: bytes.Repeat([]byte("a"), 1024)}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			worker.EnqueueReceive(context.Background(), event)
		}
	})
}

func BenchmarkPluginHookQueueFull(b *testing.B) {
	worker := NewWorker(Options{Usecase: newBlockingPersistAfterUsecase(), QueueSize: 1, Workers: 1, Timeout: time.Second})
	require.NoError(b, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	event := pluginevents.PersistAfterCommitted{MessageID: 1, Payload: []byte("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		worker.EnqueuePersistAfter(context.Background(), event)
	}
}

func BenchmarkPluginHookReceiveQueueFull(b *testing.B) {
	worker := NewWorker(Options{
		Usecase:        noopPersistAfterUsecase{},
		ReceiveUsecase: blockingReceiveUsecase{},
		QueueSize:      1,
		Workers:        1,
		Timeout:        time.Second,
	})
	require.NoError(b, worker.Start(context.Background()))
	defer worker.Stop(context.Background())

	event := pluginevents.ReceiveOffline{MessageID: 1, MessageSeq: 1, UID: "bot", Payload: []byte("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		worker.EnqueueReceive(context.Background(), event)
	}
}

type noopPersistAfterUsecase struct{}

func (noopPersistAfterUsecase) PersistAfterCommitted(context.Context, pluginevents.PersistAfterCommitted) error {
	return nil
}

type noopReceiveUsecase struct{}

func (noopReceiveUsecase) ReceiveOffline(context.Context, pluginevents.ReceiveOffline) error {
	return nil
}

type blockingReceiveUsecase struct{}

func (blockingReceiveUsecase) ReceiveOffline(ctx context.Context, _ pluginevents.ReceiveOffline) error {
	<-ctx.Done()
	return ctx.Err()
}
