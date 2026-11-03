package chatlifecycle

import "time"

// validWorkerSyncThresholds keeps legacy absence unchanged and binds new P95
// counts to the same successful sync observations as the existing histogram.
// Fixed buckets bound scalars; they never interpolate a percentile or duration.
func validWorkerSyncThresholds(snapshot WorkerSyncSnapshot) bool {
	c := snapshot.Thresholds
	if c == (LatencyThresholdCounters{}) {
		return true
	}
	if c.P999Limit <= 0 || !c.validPrimarySchema() || c.primaryAbove() > c.Count || c.AboveP999 > c.primaryAbove() || c.Above10Seconds > c.AboveP999 {
		return false
	}
	if c.P95Limit == 0 {
		return true
	}
	return validCoordinatorHistogram(snapshot.Latency) && c.Count == snapshot.Latency.Count &&
		hotCountWithinHistogram(snapshot.Latency, c.P95Limit, c.AboveP95) &&
		hotCountWithinHistogram(snapshot.Latency, c.P999Limit, c.AboveP999) &&
		hotCountWithinHistogram(snapshot.Latency, 10*time.Second, c.Above10Seconds)
}
