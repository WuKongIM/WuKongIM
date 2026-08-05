package chatlifecycle

import (
	"errors"
	"math"
	"time"
)

var errProductionEvidence = errors.New("chat lifecycle production evidence: invalid observation")

func projectWorkerVerdictEvidence(
	cfg Config,
	snapshots []WorkerSnapshot,
	lifecycle LifecycleProofSnapshot,
) (CorrectnessCounters, LatencyCounters, []VerdictSignal, error) {
	if cfg.Validate() != nil || len(snapshots) != coordinatorWorkerCount ||
		!validCoordinatorHistogram(lifecycle.ReheatLatency) {
		return CorrectnessCounters{}, LatencyCounters{}, nil, errProductionEvidence
	}
	var correctness CorrectnessCounters
	hot := newWorkerHistogramSnapshot()
	syncCounters := LatencyThresholdCounters{
		P99Limit: cfg.Thresholds.Latency.Sync.P99, P999Limit: cfg.Thresholds.Latency.Sync.P999,
	}
	seen := [coordinatorWorkerCount]bool{}
	classification := SyncClassification("")
	for _, snapshot := range snapshots {
		if snapshot.WorkerCount != coordinatorWorkerCount || snapshot.WorkerID >= coordinatorWorkerCount ||
			seen[snapshot.WorkerID] || !validWorkerSnapshot(snapshot) ||
			!validCoordinatorHistogram(snapshot.SendackLatency) {
			return CorrectnessCounters{}, LatencyCounters{}, nil, errProductionEvidence
		}
		seen[snapshot.WorkerID] = true
		for _, pair := range [][2]*uint64{
			{&correctness.FirstAttempts, &snapshot.Messages.FirstAttempts},
			{&correctness.FirstAttemptFailures, &snapshot.Messages.FirstAttemptFailures},
			{&correctness.TerminalSends, &snapshot.Messages.Terminal},
			{&correctness.Losses, &snapshot.Messages.Losses},
			{&correctness.Duplicates, &snapshot.Messages.Duplicates},
			{&correctness.Corruptions, &snapshot.Messages.Corruptions},
			{&correctness.SequenceRegressions, &snapshot.Messages.SequenceRegressions},
			{&correctness.QueueSaturations, &snapshot.Harness.CommandSaturation},
		} {
			if math.MaxUint64-*pair[0] < *pair[1] {
				return CorrectnessCounters{}, LatencyCounters{}, nil, errProductionEvidence
			}
			*pair[0] += *pair[1]
		}
		if err := addCoordinatorHistogram(&hot, snapshot.SendackLatency); err != nil {
			return CorrectnessCounters{}, LatencyCounters{}, nil, errProductionEvidence
		}
		thresholds := snapshot.Sync.Thresholds
		if thresholds.P99Limit != syncCounters.P99Limit || thresholds.P999Limit != syncCounters.P999Limit ||
			thresholds.AboveP99 > thresholds.Count || thresholds.AboveP999 > thresholds.AboveP99 ||
			thresholds.Above10Seconds > thresholds.AboveP999 {
			return CorrectnessCounters{}, LatencyCounters{}, nil, errProductionEvidence
		}
		for _, pair := range [][2]*uint64{
			{&syncCounters.Count, &thresholds.Count}, {&syncCounters.AboveP99, &thresholds.AboveP99},
			{&syncCounters.AboveP999, &thresholds.AboveP999}, {&syncCounters.Above10Seconds, &thresholds.Above10Seconds},
		} {
			if math.MaxUint64-*pair[0] < *pair[1] {
				return CorrectnessCounters{}, LatencyCounters{}, nil, errProductionEvidence
			}
			*pair[0] += *pair[1]
		}
		classification = mergeSyncClassification(classification, snapshot.Harness.Classification, snapshot.Evidence.Classification)
	}
	hotCounters, err := histogramThresholdCounters(hot, cfg.Thresholds.Latency.HotSendACK)
	if err != nil {
		return CorrectnessCounters{}, LatencyCounters{}, nil, err
	}
	coldCounters, err := histogramThresholdCounters(lifecycle.ReheatLatency, cfg.Thresholds.Latency.Cold)
	if err != nil {
		return CorrectnessCounters{}, LatencyCounters{}, nil, err
	}
	var signals []VerdictSignal
	switch classification {
	case SyncClassificationProductFailure:
		signals = append(signals, VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseWorkerProduct})
	case SyncClassificationHarnessInvalid:
		signals = append(signals, VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseWorkerHarness})
	}
	return correctness, LatencyCounters{Hot: hotCounters, Cold: coldCounters, Sync: syncCounters}, signals, nil
}

func histogramThresholdCounters(histogram WorkerHistogramSnapshot, limits LatencyLimit) (LatencyThresholdCounters, error) {
	result := LatencyThresholdCounters{P99Limit: limits.P99, P999Limit: limits.P999}
	if limits.P99 <= 0 || limits.P999 < limits.P99 || !validCoordinatorHistogram(histogram) {
		return result, errProductionEvidence
	}
	cumulativeAt := func(limit time.Duration) (uint64, bool) {
		var cumulative uint64
		for index, upper := range histogram.BucketUpper {
			if math.MaxUint64-cumulative < histogram.Buckets[index] {
				return 0, false
			}
			cumulative += histogram.Buckets[index]
			if upper == uint64(limit) {
				return cumulative, true
			}
		}
		return 0, false
	}
	p99At, p99OK := cumulativeAt(limits.P99)
	p999At, p999OK := cumulativeAt(limits.P999)
	tenSecondsAt, anomalyOK := cumulativeAt(10 * time.Second)
	if !p99OK || !p999OK || !anomalyOK || p99At > histogram.Count || p999At > histogram.Count || tenSecondsAt > histogram.Count {
		return result, errProductionEvidence
	}
	result.Count = histogram.Count
	result.AboveP99 = histogram.Count - p99At
	result.AboveP999 = histogram.Count - p999At
	result.Above10Seconds = histogram.Count - tenSecondsAt
	return result, nil
}
