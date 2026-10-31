package chatlifecycle

import "time"

// validWorkerHotThresholds preserves an explicit legacy absence while binding
// present exact counters to the same samples as the historical histogram.
func validWorkerHotThresholds(snapshot WorkerSnapshot) bool {
	c := snapshot.HotSendackThresholds
	return c == (LatencyThresholdCounters{}) ||
		(validCoordinatorHistogram(snapshot.HotSendackLatency) &&
			c.P99Limit > 0 && c.P999Limit >= c.P99Limit &&
			c.Count == snapshot.HotSendackLatency.Count && c.AboveP99 <= c.Count &&
			c.AboveP999 <= c.AboveP99 && c.Above10Seconds <= c.Count &&
			hotCountWithinHistogram(snapshot.HotSendackLatency, c.P99Limit, c.AboveP99) &&
			hotCountWithinHistogram(snapshot.HotSendackLatency, c.P999Limit, c.AboveP999) &&
			hotCountWithinHistogram(snapshot.HotSendackLatency, 10*time.Second, c.Above10Seconds))
}

// aggregateWorkerHotThresholds is shared by native verdicts and passive logs.
// Legacy evidence can use exact histogram boundaries, never interpolation.
func aggregateWorkerHotThresholds(snapshots []WorkerSnapshot, limits LatencyLimit) (LatencyThresholdCounters, error) {
	total := LatencyThresholdCounters{P99Limit: limits.P99, P999Limit: limits.P999}
	if limits.P99 <= 0 || limits.P999 < limits.P99 {
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
			var err error
			counters, err = histogramThresholdCounters(snapshot.HotSendackLatency, limits)
			if err != nil {
				return total, err
			}
		}
		if counters.P99Limit != limits.P99 || counters.P999Limit != limits.P999 ||
			addCoordinatorLatencyThresholds(&total, counters) != nil {
			return total, errProductionEvidence
		}
	}
	return total, nil
}

// hotThresholdsRegressed also rejects a schema/presence change within one
// generation; old 400ms evidence stays old rather than acquiring new limits.
func hotThresholdsRegressed(current, previous LatencyThresholdCounters) bool {
	return current.P99Limit != previous.P99Limit || current.P999Limit != previous.P999Limit ||
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
