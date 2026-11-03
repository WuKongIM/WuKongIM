package workqueue

import (
	"testing"
	"unsafe"
)

// These tests exercise the existing synchronous collector seam. They do not
// start an executor or change the pool's admission and shutdown contracts.
func TestBoundedBatchCollectionRetainsReadyFIFO(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ready     int
		maxItems  int
		queueSize int
		wantBatch int
	}{
		{name: "sparse_one", ready: 1, maxItems: 256, queueSize: 256, wantBatch: 1},
		{name: "sparse_four", ready: 4, maxItems: 256, queueSize: 256, wantBatch: 4},
		{name: "full_with_tail", ready: 257, maxItems: 256, queueSize: 256, wantBatch: 256},
		{name: "queue_bound", ready: 5, maxItems: 256, queueSize: 4, wantBatch: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &BoundedBatchPool[int]{
				cfg: BoundedBatchPoolConfig[int]{
					QueueSize: tc.queueSize,
					Policy: func(int) BatchOptions {
						return BatchOptions{MaxItems: tc.maxItems}
					},
				},
				queue: make(chan boundedBatchPoolTask[int], tc.queueSize),
			}
			for item := 1; item < tc.ready; item++ {
				pool.queue <- boundedBatchPoolTask[int]{item: item}
			}

			batch := pool.collectBatch(boundedBatchPoolTask[int]{item: 0})
			if len(batch) != tc.wantBatch {
				t.Fatalf("batch length = %d, want %d", len(batch), tc.wantBatch)
			}
			for index, task := range batch {
				if task.item != index {
					t.Fatalf("batch[%d] = %d, want FIFO item %d", index, task.item, index)
				}
			}
			if got := len(pool.queue); got != tc.ready-tc.wantBatch {
				t.Fatalf("queued remainder = %d, want %d", got, tc.ready-tc.wantBatch)
			}
			for item := tc.wantBatch; item < tc.ready; item++ {
				if task := <-pool.queue; task.item != item {
					t.Fatalf("remainder = %d, want FIFO item %d", task.item, item)
				}
			}
		})
	}
}

func TestBoundedBatchCollectionExtendsSparseBatchToLogicalLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial int
	}{
		{name: "one", initial: 1},
		{name: "four", initial: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &BoundedBatchPool[int]{
				cfg: BoundedBatchPoolConfig[int]{
					QueueSize: 256,
					Policy: func(int) BatchOptions {
						return BatchOptions{MaxItems: 256}
					},
				},
				queue: make(chan boundedBatchPoolTask[int], 256),
			}
			for item := 1; item < tc.initial; item++ {
				pool.queue <- boundedBatchPoolTask[int]{item: item}
			}
			batch := pool.collectBatch(boundedBatchPoolTask[int]{item: 0})
			if len(batch) != tc.initial || len(pool.queue) != 0 {
				t.Fatalf("initial collection = %d items, %d queued; want %d, 0", len(batch), len(pool.queue), tc.initial)
			}

			// Arrivals occur after collection, as when an executor retry extends
			// its pending batch. No timer or scheduler interleaving is required.
			for item := tc.initial; item < 16; item++ {
				pool.queue <- boundedBatchPoolTask[int]{item: item}
			}
			pool.extendBatchReady(&batch)
			if len(batch) != 16 || len(pool.queue) != 0 {
				t.Fatalf("first extension = %d items, %d queued; want 16, 0", len(batch), len(pool.queue))
			}
			for item := 16; item <= 256; item++ {
				pool.queue <- boundedBatchPoolTask[int]{item: item}
			}
			pool.extendBatchReady(&batch)
			pool.extendBatchReady(&batch)
			if len(batch) != 256 || len(pool.queue) != 1 {
				t.Fatalf("full extension = %d items, %d queued; want 256, 1", len(batch), len(pool.queue))
			}
			for index, task := range batch {
				if task.item != index {
					t.Fatalf("extended batch[%d] = %d, want FIFO item %d", index, task.item, index)
				}
			}
			if task := <-pool.queue; task.item != 256 {
				t.Fatalf("item beyond logical limit = %d, want 256", task.item)
			}
		})
	}
}

// batchAllocationItem is a pointer-bearing large value, not a production DTO
// mirror. On 64-bit targets it is 328 bytes; the task adds its enqueue time for
// approximately 352 bytes, near the profiled local-durability task size.
type batchAllocationItem struct {
	sequence   uint64
	metadata   [32]uint64
	references [8]*byte
}

var batchAllocationSink []boundedBatchPoolTask[batchAllocationItem]

// BenchmarkBoundedBatchCollectAllocation measures the real collector with
// MaxWait zero. Each iteration includes allocation-free channel refilling,
// collection and a cardinality check; ns/op includes that fixed harness work.
// Pool setup, admission, executor scheduling and handler work are absent.
func BenchmarkBoundedBatchCollectAllocation(b *testing.B) {
	for _, tc := range []struct {
		name  string
		items int
	}{
		{name: "sparse_one", items: 1},
		{name: "sparse_four", items: 4},
		{name: "full_256", items: 256},
	} {
		b.Run(tc.name, func(b *testing.B) {
			pool := &BoundedBatchPool[batchAllocationItem]{
				cfg: BoundedBatchPoolConfig[batchAllocationItem]{
					QueueSize: 256,
					Policy: func(batchAllocationItem) BatchOptions {
						return BatchOptions{MaxItems: 256}
					},
				},
				queue: make(chan boundedBatchPoolTask[batchAllocationItem], 256),
			}
			var tasks [256]boundedBatchPoolTask[batchAllocationItem]
			var referenced byte
			for index := range tasks {
				tasks[index].item.sequence = uint64(index)
				tasks[index].item.metadata[0] = uint64(index)
				tasks[index].item.references[0] = &referenced
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				for index := 1; index < tc.items; index++ {
					pool.queue <- tasks[index]
				}
				batchAllocationSink = pool.collectBatch(tasks[0])
				if len(batchAllocationSink) != tc.items || len(pool.queue) != 0 {
					b.Fatalf("collection = %d items, %d queued; want %d, 0", len(batchAllocationSink), len(pool.queue), tc.items)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(tc.items), "items/op")
			b.ReportMetric(float64(unsafe.Sizeof(batchAllocationItem{})), "item-bytes")
			b.ReportMetric(float64(unsafe.Sizeof(tasks[0])), "task-bytes")
			batchAllocationSink = nil
		})
	}
}
