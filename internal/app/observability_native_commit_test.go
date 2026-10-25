package app

import (
	"errors"
	"testing"
	"time"

	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func TestStorageObserverMapsNativeStatsWithoutChangingRuntimeTotal(t *testing.T) {
	registry := obsmetrics.New(1, "native")
	observer := storageCommitMetricsObserver{metrics: registry}
	event := messagedb.CommitCoordinatorBatchEvent{Requests: 3, Records: 4, Bytes: 80, TotalDuration: 10 * time.Millisecond, Err: errors.New("publication failure"),
		Native: messagedb.NativeCommitStats{Available: true, TotalDuration: 70 * time.Millisecond, SemaphoreWaitDuration: time.Millisecond, WALQueueWaitDuration: 2 * time.Millisecond, MemTableWriteStallDuration: 3 * time.Millisecond, L0ReadAmpWriteStallDuration: 4 * time.Millisecond, WALRotationDuration: 5 * time.Millisecond, CommitWaitDuration: 6 * time.Millisecond},
	}
	observer.ObserveCommitCoordinatorBatch(event)
	event.Native.Available = false
	observer.ObserveCommitCoordinatorBatch(event)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	family := requireAppMetricFamily(t, families, "wukongim_storage_commit_batch_duration_seconds")
	for stage, seconds := range map[string]float64{"native_total": .07, "native_semaphore_wait": .001, "native_wal_queue_wait": .002, "native_memtable_write_stall": .003, "native_l0_read_amp_write_stall": .004, "native_wal_rotation": .005, "native_commit_wait": .006} {
		h := findAppMetricByLabels(t, family, map[string]string{"store": "message", "result": "err", "stage": stage}).GetHistogram()
		if h.GetSampleCount() != 1 || h.GetSampleSum() != seconds {
			t.Fatalf("%s count/sum=(%d,%g),want(1,%g)", stage, h.GetSampleCount(), h.GetSampleSum(), seconds)
		}
	}
	total := findAppMetricByLabels(t, family, map[string]string{"store": "message", "result": "err", "stage": "total"}).GetHistogram()
	if total.GetSampleCount() != 2 || total.GetSampleSum() != .02 {
		t.Fatalf("legacy total changed: %+v", total)
	}
	runtime := findAppMetricByLabels(t, requireAppMetricFamily(t, families, "wukongim_runtime_pool_task_duration_seconds"), map[string]string{"component": dbRuntimeComponent, "pool": dbMessageCommitPool, "task": dbMessageCommitQueue, "result": "err"}).GetHistogram()
	if runtime.GetSampleCount() != 2 || runtime.GetSampleSum() != .02 {
		t.Fatalf("runtime duration includes native timings: %+v", runtime)
	}
}
