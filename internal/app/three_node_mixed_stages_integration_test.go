//go:build integration

package app

import (
	"fmt"
	"math"
	"sort"
	"testing"
)

type threeNodeMixedHistogram struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64
}

// snapshotThreeNodeMixedStages owns detached, bounded stage aggregates. Collect
// outside the timed interval; neither collection nor reporting resets live metrics.
func snapshotThreeNodeMixedStages(b testing.TB, apps []*App) map[string]threeNodeMixedHistogram {
	b.Helper()
	stages := make(map[string]threeNodeMixedHistogram)
	for _, app := range apps {
		families, err := app.metrics.PrometheusRegistry().Gather()
		if err != nil {
			b.Fatalf("gather benchmark stage metrics: %v", err)
		}
		for _, family := range families {
			prefix := ""
			switch family.GetName() {
			case "wukongim_message_send_batch_stage_item_duration_seconds":
				prefix = "stage-"
			case "wukongim_channelv2_append_stage_duration_seconds":
				prefix = "channel-"
			default:
				continue
			}
			for _, metric := range family.Metric {
				stage := threeNodeMixedMetricLabel(metric, "stage")
				if stage == "" || metric.Histogram == nil || (prefix == "channel-" && threeNodeMixedMetricLabel(metric, "result") != "ok") {
					continue
				}
				key := prefix + stage
				aggregate := stages[key]
				if aggregate.buckets == nil {
					aggregate.buckets = make(map[float64]uint64)
				}
				aggregate.count += metric.Histogram.GetSampleCount()
				aggregate.sum += metric.Histogram.GetSampleSum()
				for _, bucket := range metric.Histogram.Bucket {
					aggregate.buckets[bucket.GetUpperBound()] += bucket.GetCumulativeCount()
				}
				stages[key] = aggregate
			}
		}
	}
	return stages
}

// threeNodeMixedStageDelta excludes completed setup/warmup observations. A
// counter reset or schema change invalidates this diagnostic instead of wrapping.
func threeNodeMixedStageDelta(before, after threeNodeMixedHistogram) (threeNodeMixedHistogram, error) {
	if after.count < before.count || after.sum < before.sum {
		return threeNodeMixedHistogram{}, fmt.Errorf("stage histogram reset")
	}
	for bound := range before.buckets {
		if _, ok := after.buckets[bound]; !ok {
			return threeNodeMixedHistogram{}, fmt.Errorf("stage histogram bucket disappeared")
		}
	}
	delta := threeNodeMixedHistogram{count: after.count - before.count, sum: after.sum - before.sum, buckets: make(map[float64]uint64, len(after.buckets))}
	for bound, count := range after.buckets {
		if count < before.buckets[bound] {
			return threeNodeMixedHistogram{}, fmt.Errorf("stage histogram bucket reset")
		}
		delta.buckets[bound] = count - before.buckets[bound]
	}
	return delta, nil
}

func (h threeNodeMixedHistogram) p99Upper() float64 {
	bounds := make([]float64, 0, len(h.buckets))
	for bound := range h.buckets {
		bounds = append(bounds, bound)
	}
	sort.Float64s(bounds)
	threshold := (h.count*99 + 99) / 100
	for _, bound := range bounds {
		if h.buckets[bound] >= threshold {
			return bound
		}
	}
	// Observations beyond the final finite bucket have no finite upper bound.
	return math.Inf(1)
}

type threeNodeMixedStageReporter interface {
	Helper()
	Fatalf(string, ...any)
	ReportMetric(float64, string)
}

func reportThreeNodeMixedStageWindow(b threeNodeMixedStageReporter, before, after map[string]threeNodeMixedHistogram) {
	b.Helper()
	for stage := range before {
		if _, ok := after[stage]; !ok {
			b.Fatalf("stage histogram disappeared: %s", stage)
		}
	}
	for stage, histogram := range after {
		delta, err := threeNodeMixedStageDelta(before[stage], histogram)
		if err != nil {
			b.Fatalf("stage %s: %v", stage, err)
		}
		if delta.count == 0 {
			continue
		}
		b.ReportMetric(float64(delta.count), stage+"-samples")
		b.ReportMetric(delta.sum*1000/float64(delta.count), stage+"-avg-ms")
		b.ReportMetric(delta.p99Upper()*1000, stage+"-p99-upper-ms")
	}
}
