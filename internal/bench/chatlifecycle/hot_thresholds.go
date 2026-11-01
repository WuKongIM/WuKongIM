package chatlifecycle

import "time"

// validWorkerHotThresholds preserves an explicit legacy absence while binding
// present exact counters to the same samples as the historical histogram.
func validWorkerHotThresholds(snapshot WorkerSnapshot) bool {
	c := snapshot.HotSendackThresholds
	return c == (LatencyThresholdCounters{}) ||
		(validCoordinatorHistogram(snapshot.HotSendackLatency) &&
			c.validPrimarySchema() &&
			c.Count == snapshot.HotSendackLatency.Count && c.primaryAbove() <= c.Count &&
			c.AboveP999 <= c.primaryAbove() && c.Above10Seconds <= c.Count &&
			hotCountWithinHistogram(snapshot.HotSendackLatency, c.primaryLimit(), c.primaryAbove()) &&
			hotCountWithinHistogram(snapshot.HotSendackLatency, c.P999Limit, c.AboveP999) &&
			hotCountWithinHistogram(snapshot.HotSendackLatency, 10*time.Second, c.Above10Seconds))
}

// aggregateWorkerHotThresholds is shared by native verdicts and passive logs.
// Legacy evidence can use exact histogram boundaries, never interpolation.
func aggregateWorkerHotThresholds(snapshots []WorkerSnapshot, limits LatencyLimit) (LatencyThresholdCounters, error) {
	total := latencyThresholdCounters(limits)
	if validateHotLatencyLimit("hot_latency", limits) != nil {
		return total, errProductionEvidence
	}
	histogram := newWorkerHistogramSnapshot()
	for _, snapshot := range snapshots {
		if !validCoordinatorHistogram(snapshot.HotSendackLatency) || !validWorkerHotThresholds(snapshot) ||
			addCoordinatorHistogram(&histogram, snapshot.HotSendackLatency) != nil {
			return total, errProductionEvidence
		}
		counters := snapshot.HotSendackThresholds
		if counters == (LatencyThresholdCounters{}) {
			if limits.P95 != 0 {
				return total, errProductionEvidence
			}
			var err error
			counters, err = histogramThresholdCounters(snapshot.HotSendackLatency, limits)
			if err != nil {
				return total, err
			}
		}
		if counters.P95Limit != limits.P95 || counters.P99Limit != limits.P99 || counters.P999Limit != limits.P999 ||
			addCoordinatorLatencyThresholds(&total, counters) != nil {
			return total, errProductionEvidence
		}
	}
	return total, nil
}

// hotThresholdsRegressed also rejects a schema/presence change within one
// generation; old 400ms evidence stays old rather than acquiring new limits.
func hotThresholdsRegressed(current, previous LatencyThresholdCounters) bool {
	return current.P95Limit != previous.P95Limit || current.P99Limit != previous.P99Limit || current.P999Limit != previous.P999Limit ||
		current.AboveP95 < previous.AboveP95 ||
		current.Count < previous.Count || current.AboveP99 < previous.AboveP99 ||
		current.AboveP999 < previous.AboveP999 || current.Above10Seconds < previous.Above10Seconds
}

// hotCountWithinHistogram checks only what the fixed buckets can prove. At an
// exact boundary the bounds coincide; between boundaries no interpolation is
// justified. The last bucket includes values above sixty seconds.
// The caller must first validate the histogram's checked bucket total.
func hotCountWithinHistogram(histogram WorkerHistogramSnapshot, limit time.Duration, above uint64) bool {
	var minimum, maximum uint64
	for index, count := range histogram.Buckets {
		if index > 0 && histogram.BucketUpper[index-1] >= uint64(limit) {
			minimum += count
		}
		if histogram.BucketUpper[index] > uint64(limit) || index == len(histogram.Buckets)-1 {
			maximum += count
		}
	}
	return minimum <= above && above <= maximum
}
