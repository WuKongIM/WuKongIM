//go:build integration

package app

import (
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// Exercise the same registry snapshot and report path as the real benchmark:
// expensive completed warmup must not contaminate fast measured observations.
func TestThreeNodeMixedStageWindowExcludesWarmup(t *testing.T) {
	apps := []*App{{metrics: metrics.New(1, "one")}, {metrics: metrics.New(2, "two")}}
	for _, app := range apps {
		app.metrics.Message.ObserveSendBatchStage("submitter", "ok", 100, time.Second)
		app.metrics.ChannelRuntime.ObserveAppendStage("meta_create_write", "ok", time.Second)
		app.metrics.ChannelRuntime.ObserveAppendStage("runtime_append", "ok", time.Second)
	}
	before := snapshotThreeNodeMixedStages(t, apps)
	for _, app := range apps {
		app.metrics.Message.ObserveSendBatchStage("submitter", "ok", 2, time.Millisecond)
		app.metrics.ChannelRuntime.ObserveAppendStage("runtime_append", "ok", 2*time.Millisecond)
		app.metrics.ChannelRuntime.ObserveAppendStage("runtime_append", "error", time.Second)
		// A family absent before the window must still be counted.
		app.metrics.Message.ObserveSendBatchStage("permission", "ok", 1, time.Millisecond)
	}
	after := snapshotThreeNodeMixedStages(t, apps)
	reporter := &mixedStageTestReporter{T: t, values: make(map[string]float64)}
	reportThreeNodeMixedStageWindow(reporter, before, after)
	for key, want := range map[string]float64{
		"stage-submitter-samples": 4, "stage-submitter-avg-ms": 1,
		"stage-permission-samples": 2, "stage-permission-avg-ms": 1,
		"channel-runtime_append-samples": 2, "channel-runtime_append-avg-ms": 2,
	} {
		if got := reporter.values[key]; math.Abs(got-want) > 1e-8 {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
	if _, ok := reporter.values["channel-meta_create_write-avg-ms"]; ok {
		t.Error("warmup-only stage was reported in the measured window")
	}
	if before["stage-submitter"].count != 200 || after["stage-submitter"].count != 204 {
		t.Fatal("snapshots alias live counters or reporting mutates snapshots")
	}
	if snapshotThreeNodeMixedStages(t, apps)["stage-submitter"].count != 204 {
		t.Fatal("reporting reset live metrics")
	}
	if got := reporter.values["stage-submitter-p99-upper-ms"]; got < 1 || got > 5 {
		t.Fatalf("measured p99 upper = %g ms, want fast bucket", got)
	}
}

func TestThreeNodeMixedStageDeltaRejectsResetAndPreservesOverflow(t *testing.T) {
	before := threeNodeMixedHistogram{count: 2, sum: 2, buckets: map[float64]uint64{1: 2}}
	for name, after := range map[string]threeNodeMixedHistogram{
		"count":  {count: 1, sum: 3, buckets: map[float64]uint64{1: 2}},
		"sum":    {count: 3, sum: 1, buckets: map[float64]uint64{1: 2}},
		"bucket": {count: 3, sum: 3, buckets: map[float64]uint64{1: 1}},
		"schema": {count: 3, sum: 3, buckets: map[float64]uint64{2: 3}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := threeNodeMixedStageDelta(before, after); err == nil {
				t.Fatal("invalid counter delta accepted")
			}
		})
	}
	h := threeNodeMixedHistogram{count: 100, buckets: map[float64]uint64{1: 98}}
	if !math.IsInf(h.p99Upper(), 1) {
		t.Fatal("overflow p99 was incorrectly assigned a finite upper bound")
	}
	h.buckets[1] = 99
	if h.p99Upper() != 1 {
		t.Fatal("exact p99 boundary not selected")
	}
}

type mixedStageTestReporter struct {
	*testing.T
	values map[string]float64
}

func (r *mixedStageTestReporter) ReportMetric(value float64, unit string) {
	r.values[unit] = value
}
