package chatlifecycle

import "time"

// primaryLimit selects the explicitly configured duration, never a quantile
// estimated from histogram buckets. Validation rejects ambiguous limits.
func (l LatencyLimit) primaryLimit() time.Duration {
	if l.P95 != 0 {
		return l.P95
	}
	return l.P99
}

// primaryFractionDenominator keeps exact inclusive5%/1% arithmetic.
func (l LatencyLimit) primaryFractionDenominator() uint64 {
	if l.P95 != 0 {
		return 20
	}
	return 100
}

func latencyThresholdCounters(l LatencyLimit) LatencyThresholdCounters {
	return LatencyThresholdCounters{P95Limit: l.P95, P99Limit: l.P99, P999Limit: l.P999}
}

func (c LatencyThresholdCounters) primaryLimit() time.Duration {
	if c.P95Limit != 0 {
		return c.P95Limit
	}
	return c.P99Limit
}

func (c LatencyThresholdCounters) primaryAbove() uint64 {
	if c.P95Limit != 0 {
		return c.AboveP95
	}
	return c.AboveP99
}

// validPrimarySchema binds inactive counters to zero so a P95 observation
// cannot masquerade as P99 or mix the two meanings within a generation.
func (c LatencyThresholdCounters) validPrimarySchema() bool {
	primary := c.P95Limit > 0 && c.P99Limit == 0 && c.AboveP99 == 0 || c.P99Limit > 0 && c.P95Limit == 0 && c.AboveP95 == 0
	secondary := c.P999Limit >= c.primaryLimit()
	if c.P95Limit > 0 && c.P999Limit == 0 {
		secondary = c.AboveP999 == 0
	}
	return primary && secondary
}

// pairedP95Policy identifies the explicit local hot-only/sync-P95 policy.
// Config scope validation prevents its use in formal or capacity evidence.
func (t LatencyThresholds) pairedP95Policy() bool {
	return t.HotSendACK.P95 > 0 && t.HotSendACK.P999 == 0 && t.Sync.P95 > 0
}

// validObservedCounts keeps inactive tails empty while preserving the independent
// ten-second anomaly subset when hot P95 is the sole active percentile.
func (c LatencyThresholdCounters) validObservedCounts() bool {
	if !c.validPrimarySchema() || c.primaryAbove() > c.Count || c.AboveP999 > c.primaryAbove() {
		return false
	}
	anomalyBound := c.AboveP999
	if c.P999Limit == 0 {
		anomalyBound = c.primaryAbove()
	}
	return c.Above10Seconds <= anomalyBound
}
