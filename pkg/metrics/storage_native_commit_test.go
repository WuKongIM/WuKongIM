package metrics

import (
	"testing"
	"time"
)

func TestStorageNativeCommitStagesRequireAvailabilityAndKeepPhysicalCounts(t *testing.T) {
	reg := New(7, "native")
	obs := StorageCommitBatchObservation{Requests: 3, Records: 4, Bytes: 80, TotalDuration: 10 * time.Millisecond,
		Native: StorageNativeCommitObservation{TotalDuration: 70 * time.Millisecond, SemaphoreWaitDuration: time.Millisecond, WALQueueWaitDuration: 2 * time.Millisecond, MemTableWriteStallDuration: 3 * time.Millisecond, L0ReadAmpWriteStallDuration: 4 * time.Millisecond, WALRotationDuration: 5 * time.Millisecond, CommitWaitDuration: 6 * time.Millisecond},
	}
	reg.Storage.ObserveCommitBatch("message", "ok", obs)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	family := requireMetricFamily(t, families, "wukongim_storage_commit_batch_duration_seconds")
	if len(family.Metric) != 5 {
		t.Fatalf("unavailable native invented series: %d", len(family.Metric))
	}
	obs.Native.Available = true
	reg.Storage.ObserveCommitBatch("message", "ok", obs)
	obs.Native.Available = false
	reg.Storage.ObserveCommitBatch("message", "ok", obs)
	obs.Native = StorageNativeCommitObservation{Available: true}
	reg.Storage.ObserveCommitBatch("message", "ok", obs)
	families, err = reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	family = requireMetricFamily(t, families, "wukongim_storage_commit_batch_duration_seconds")
	if len(family.Metric) != 12 {
		t.Fatalf("phase series=%d,want12", len(family.Metric))
	}
	for stage, seconds := range map[string]float64{"native_total": .07, "native_semaphore_wait": .001, "native_wal_queue_wait": .002, "native_memtable_write_stall": .003, "native_l0_read_amp_write_stall": .004, "native_wal_rotation": .005, "native_commit_wait": .006} {
		metric := findMetricByLabels(t, family, map[string]string{"store": "message", "result": "ok", "stage": stage})
		histogram := metric.GetHistogram()
		if histogram.GetSampleCount() != 2 || histogram.GetSampleSum() != seconds {
			t.Fatalf("%s count/sum=(%d,%g),want(2,%g)", stage, histogram.GetSampleCount(), histogram.GetSampleSum(), seconds)
		}
		if len(metric.Label) != 5 {
			t.Fatalf("%s introduced label keys", stage)
		}
	}
	total := findMetricByLabels(t, family, map[string]string{"store": "message", "result": "ok", "stage": "total"}).GetHistogram()
	if total.GetSampleCount() != 4 || total.GetSampleSum() != .04 {
		t.Fatalf("legacytotal=%+v,want4 samples totaling40ms", total)
	}
	requests := requireMetricFamily(t, families, "wukongim_storage_commit_batch_requests").Metric[0].GetHistogram()
	if requests.GetSampleCount() != 4 || requests.GetSampleSum() != 12 {
		t.Fatalf("physical/logical accounting=%+v", requests)
	}
}
