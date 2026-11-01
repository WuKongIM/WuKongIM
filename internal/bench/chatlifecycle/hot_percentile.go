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
	return (c.P95Limit > 0 && c.P99Limit == 0 && c.AboveP99 == 0 || c.P99Limit > 0 && c.P95Limit == 0 && c.AboveP95 == 0) && c.P999Limit >= c.primaryLimit()
}
