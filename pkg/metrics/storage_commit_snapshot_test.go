package metrics

import (
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Failure cases: a scrape can split one observation across families, lose
// observations, alter existing labels/buckets, or mutate an earlier snapshot.
func TestStorageCommitBatchSnapshotConcurrentGather(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newStorageMetrics(reg, prometheus.Labels{"node_id": "1", "node_name": "node-1"})
	obs := StorageCommitBatchObservation{Requests: 1, Records: 1, Bytes: 10, Native: StorageNativeCommitObservation{Available: true}}
	m.ObserveCommitBatch("message", "ok", obs)
	var stop atomic.Bool
	var observed atomic.Uint64
	observed.Store(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			m.ObserveCommitBatch("message", "ok", obs)
			observed.Add(1)
		}
	}()
	t.Cleanup(func() { stop.Store(true); <-done })
	var previous uint64
	for sample := 0; sample < 64; sample++ {
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		counts := storageBatchSnapshotCounts(families, "message")
		reference := counts["requests"]
		if len(counts) != 15 || reference < previous {
			t.Fatalf("incomplete or regressing batch snapshot: %v", counts)
		}
		for _, count := range counts {
			if count != reference {
				t.Fatalf("batch snapshot crossed observation boundaries: %v", counts)
			}
		}
		previous = reference
	}
	stop.Store(true)
	<-done
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if got := storageBatchSnapshotCounts(families, "message")["requests"]; got != observed.Load() {
		t.Fatalf("observations=%d, want %d", got, observed.Load())
	}
}

func TestStorageCommitBatchSnapshotPreservesSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newStorageMetrics(reg, prometheus.Labels{"node_id": "7", "node_name": "node-7"})
	obs := StorageCommitBatchObservation{Requests: 2, Records: 3, Bytes: 128, CollectDuration: time.Millisecond, TotalDuration: 2 * time.Millisecond}
	m.ObserveCommitBatch("message", "ok", obs)
	m.ObserveCommitBatch("message", "err", obs)
	m.ObserveCommitBatch("", "", obs)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	buckets := map[string][]float64{
		"wukongim_storage_commit_batch_requests":         {1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024},
		"wukongim_storage_commit_batch_records":          {1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024},
		"wukongim_storage_commit_batch_bytes":            {64, 256, 1024, 4096, 16384, 65536, 262144, 524288, 1048576, 4194304},
		"wukongim_storage_commit_batch_duration_seconds": {.0005, .001, .005, .01, .025, .05, .1, .15, .2, .25, .5, 1, 2.5, 5, 10, 30},
	}
	series := 0
	for _, family := range families {
		bounds, ok := buckets[family.GetName()]
		if !ok {
			continue
		}
		for _, metric := range family.Metric {
			series++
			labels := storageBatchSnapshotLabels(metric)
			if labels["node_id"] != "7" || labels["node_name"] != "node-7" || (labels["store"] != "message" && labels["store"] != "unknown") {
				t.Fatalf("labels changed: %v", labels)
			}
			wantLabels := 3
			wantCount := uint64(2)
			if labels["store"] == "unknown" {
				wantCount = 1
			}
			if family.GetName() == "wukongim_storage_commit_batch_duration_seconds" {
				wantLabels, wantCount = 5, 1
				if labels["result"] != "ok" && labels["result"] != "err" && labels["result"] != "unknown" {
					t.Fatalf("result label changed: %v", labels)
				}
			}
			if len(labels) != wantLabels || metric.GetHistogram().GetSampleCount() != wantCount {
				t.Fatalf("series changed: labels=%v count=%d", labels, metric.GetHistogram().GetSampleCount())
			}
			got := make([]float64, 0, len(bounds))
			for _, bucket := range metric.GetHistogram().Bucket {
				got = append(got, bucket.GetUpperBound())
			}
			if !reflect.DeepEqual(got, bounds) {
				t.Fatalf("%s bucket bounds=%v, want %v", family.GetName(), got, bounds)
			}
		}
	}
	if series != 21 {
		t.Fatalf("batch histogram series=%d, want 21", series)
	}
	before := storageBatchSnapshotCounts(families, "message")
	m.ObserveCommitBatch("message", "ok", obs)
	if after := storageBatchSnapshotCounts(families, "message"); !reflect.DeepEqual(before, after) {
		t.Fatalf("later observation mutated a returned snapshot: %v -> %v", before, after)
	}
}

func storageBatchSnapshotCounts(families []*dto.MetricFamily, store string) map[string]uint64 {
	counts := make(map[string]uint64)
	names := map[string]string{"wukongim_storage_commit_batch_requests": "requests", "wukongim_storage_commit_batch_records": "records", "wukongim_storage_commit_batch_bytes": "bytes"}
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := storageBatchSnapshotLabels(metric)
			if labels["store"] != store {
				continue
			}
			if name, ok := names[family.GetName()]; ok {
				counts[name] += metric.GetHistogram().GetSampleCount()
			} else if family.GetName() == "wukongim_storage_commit_batch_duration_seconds" {
				counts[labels["stage"]] += metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return counts
}

func storageBatchSnapshotLabels(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.Label))
	for _, pair := range metric.Label {
		labels[pair.GetName()] = pair.GetValue()
	}
	return labels
}
