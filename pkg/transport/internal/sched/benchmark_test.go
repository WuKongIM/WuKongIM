package sched

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func BenchmarkSchedulerEnqueueNextBatch(b *testing.B) {
	scheduler := New(Config{
		MaxItems:       1,
		MaxBytes:       1024,
		MaxBatchFrames: 1,
		MaxBatchBytes:  1024,
	})
	item := Item{Priority: core.PriorityRPC, Bytes: 128, Value: 1}
	ctx := context.Background()

	b.SetBytes(int64(item.Bytes))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := scheduler.Enqueue(ctx, item); err != nil {
			b.Fatalf("Enqueue() error = %v", err)
		}
		batch := scheduler.NextBatch()
		if len(batch) != 1 {
			b.Fatalf("NextBatch() len = %d, want 1", len(batch))
		}
	}
}

func BenchmarkSchedulerMixedPriorityBatch(b *testing.B) {
	const (
		itemBytes    = 256
		preloadItems = 4096
		batchFrames  = 64
	)
	scheduler := New(Config{
		MaxItems:       preloadItems + batchFrames,
		MaxBytes:       int64((preloadItems + batchFrames) * itemBytes),
		MaxBatchFrames: batchFrames,
		MaxBatchBytes:  batchFrames * itemBytes,
	})
	ctx := context.Background()
	priorities := []core.Priority{
		core.PriorityRaft,
		core.PriorityControl,
		core.PriorityRPC,
		core.PriorityBulk,
	}
	for i := 0; i < preloadItems; i++ {
		item := Item{
			Priority: priorities[i%len(priorities)],
			Bytes:    itemBytes,
			Value:    i,
		}
		if err := scheduler.Enqueue(ctx, item); err != nil {
			b.Fatalf("Enqueue() setup error = %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	var processedBytes int64
	for i := 0; i < b.N; i++ {
		batch := scheduler.NextBatch()
		if len(batch) == 0 {
			b.Fatal("NextBatch() returned empty batch")
		}
		processedBytes += int64(len(batch) * itemBytes)
		for _, item := range batch {
			if err := scheduler.Enqueue(ctx, item); err != nil {
				b.Fatalf("Enqueue() refill error = %v", err)
			}
		}
	}
	if b.N > 0 {
		b.SetBytes(processedBytes / int64(b.N))
	}
}
