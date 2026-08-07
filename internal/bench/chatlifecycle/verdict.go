package chatlifecycle

import (
	"errors"
	"math"
	"math/bits"
	"time"
)

const (
	maxVerdictCleanupErrors     = 16
	maxVerdictLatencyAnomalies  = 16
	verdictLatencyWindowSamples = 64
	maxVerdictResourceWindow    = 24 * time.Hour
)

var (
	// ErrVerdictConfig rejects unusable start times or threshold sets.
	ErrVerdictConfig = errors.New("chat lifecycle verdict: invalid configuration")
	// ErrVerdictObservation rejects structurally invalid evidence.
	ErrVerdictObservation = errors.New("chat lifecycle verdict: invalid observation")
)

// VerdictOutcome is the closed final run classification.
type VerdictOutcome string

const (
	VerdictPass                      VerdictOutcome = "pass"
	VerdictRehearsalPass             VerdictOutcome = "rehearsal_pass"
	VerdictPassedWithCapacityWarning VerdictOutcome = "passed_with_capacity_warning"
	VerdictProductFailure            VerdictOutcome = "product_failure"
	VerdictInsufficientEvidence      VerdictOutcome = "insufficient_evidence"
	VerdictHarnessInvalid            VerdictOutcome = "harness_invalid"
	VerdictInfrastructureFailure     VerdictOutcome = "infrastructure_failure"
	VerdictOperatorStop              VerdictOutcome = "operator_stop"
)

// VerdictCause is the closed, identity-free first-cause vocabulary.
type VerdictCause string

const (
	VerdictCauseCompleted               VerdictCause = "completed"
	VerdictCauseRehearsalCompleted      VerdictCause = "rehearsal_completed"
	VerdictCauseMessageLoss             VerdictCause = "message_loss"
	VerdictCauseMessageDuplicate        VerdictCause = "message_duplicate"
	VerdictCauseMessageCorruption       VerdictCause = "message_corruption"
	VerdictCauseSequenceRegression      VerdictCause = "sequence_regression"
	VerdictCauseTerminalSend            VerdictCause = "terminal_send"
	VerdictCauseActivationRejection     VerdictCause = "activation_rejection"
	VerdictCauseOverallFirstAttemptRate VerdictCause = "overall_first_attempt_rate"
	VerdictCauseMinuteFirstAttemptRate  VerdictCause = "minute_first_attempt_rate"
	VerdictCauseCounterRegression       VerdictCause = "counter_regression"
	VerdictCauseQueueSaturation         VerdictCause = "queue_saturation"
	VerdictCauseObserverGap             VerdictCause = "observer_gap"
	VerdictCauseServerCrash             VerdictCause = "server_crash"
	VerdictCauseDiskExhausted           VerdictCause = "disk_exhausted"
	VerdictCauseBudgetExhausted         VerdictCause = "budget_exhausted"
	VerdictCauseLeaseExpiry             VerdictCause = "lease_expiry"
	VerdictCauseOperatorRequested       VerdictCause = "operator_requested"
	VerdictCauseHotLatency              VerdictCause = "hot_latency"
	VerdictCauseColdLatency             VerdictCause = "cold_latency"
	VerdictCauseSyncLatency             VerdictCause = "sync_latency"
	VerdictCauseInvalidObservation      VerdictCause = "invalid_observation"
	VerdictCauseHeapGrowth              VerdictCause = "heap_growth"
	VerdictCauseGoroutineGrowth         VerdictCause = "goroutine_growth"
	VerdictCauseQueueRecovery           VerdictCause = "queue_recovery"
	VerdictCauseWorkerProduct           VerdictCause = "worker_product_failure"
	VerdictCauseWorkerHarness           VerdictCause = "worker_harness_invalid"
	VerdictCauseLifecycleProduct        VerdictCause = "lifecycle_product_failure"
	VerdictCauseLifecycleHarness        VerdictCause = "lifecycle_harness_invalid"
	VerdictCauseMetaCreateProduct       VerdictCause = "meta_create_product_failure"
	VerdictCauseInfrastructureCapacity  VerdictCause = "infrastructure_capacity"
	VerdictCauseCapacityHeadroomLatency VerdictCause = "capacity_headroom_latency"
	VerdictCauseInsufficientEvidence    VerdictCause = "insufficient_evidence"
)

// ResourceBurstState makes overload recovery evaluation explicit.
type ResourceBurstState string

const (
	ResourceBurstNone   ResourceBurstState = ""
	ResourceBurstActive ResourceBurstState = "active"
	ResourceBurstEnded  ResourceBurstState = "ended"
)

// NodeResourceSample is one service-node observation. ForcedGC samples must be
// hour-aligned; queue-only samples leave HeapBytes and Goroutines at zero.
// Float inputs preserve Prometheus invalid values so the verdict can reject
// NaN, Inf, negative, or fractional gauges instead of silently converting.
type NodeResourceSample struct {
	NodeID     uint64
	ForcedGC   bool
	HeapBytes  float64
	Goroutines float64
	QueueDepth float64
	Inflight   float64
	Burst      ResourceBurstState
}

// LatencyOperation is the fixed operation partition used by the verdict.
type LatencyOperation string

const (
	LatencyHotSendACK  LatencyOperation = "hot_sendack"
	LatencyColdSendACK LatencyOperation = "cold_sendack"
	LatencyFullSync    LatencyOperation = "full_sync"
)

// LatencyThresholdCounters is cumulative. AboveP99 and AboveP999 count
// operations strictly exceeding that operation class's configured duration;
// Above10Seconds counts operations strictly exceeding ten seconds.
type LatencyThresholdCounters struct {
	P99Limit       time.Duration
	P999Limit      time.Duration
	Count          uint64
	AboveP99       uint64
	AboveP999      uint64
	Above10Seconds uint64
}

// LatencyCounters contains the three closed operation classes.
type LatencyCounters struct {
	Hot  LatencyThresholdCounters
	Cold LatencyThresholdCounters
	Sync LatencyThresholdCounters
}

func recordLatencyThresholdCounters(counters *LatencyThresholdCounters, latency time.Duration) {
	if counters == nil || latency < 0 || counters.P99Limit <= 0 || counters.P999Limit < counters.P99Limit {
		return
	}
	counters.Count = saturatingIncrement(counters.Count)
	if latency > counters.P99Limit {
		counters.AboveP99 = saturatingIncrement(counters.AboveP99)
	}
	if latency > counters.P999Limit {
		counters.AboveP999 = saturatingIncrement(counters.AboveP999)
	}
	if latency > 10*time.Second {
		counters.Above10Seconds = saturatingIncrement(counters.Above10Seconds)
	}
}

// LatencyCountersForThresholds fixes the exact duration schema that every
// cumulative observation must retain for the generation.
func LatencyCountersForThresholds(thresholds LatencyThresholds) LatencyCounters {
	return LatencyCounters{
		Hot:  LatencyThresholdCounters{P99Limit: thresholds.HotSendACK.P99, P999Limit: thresholds.HotSendACK.P999},
		Cold: LatencyThresholdCounters{P99Limit: thresholds.Cold.P99, P999Limit: thresholds.Cold.P999},
		Sync: LatencyThresholdCounters{P99Limit: thresholds.Sync.P99, P999Limit: thresholds.Sync.P999},
	}
}

// LatencyWarningCounts contains fixed short-breach evidence.
type LatencyWarningCounts struct {
	Hot  uint64
	Cold uint64
	Sync uint64
}

// LatencyAnomaly is one bounded aggregate sample for operations over ten seconds.
type LatencyAnomaly struct {
	At        time.Time
	Operation LatencyOperation
	Count     uint64
}

// VerdictSignal supplies one already-classified fact in the same atomic batch
// as counter evidence.
type VerdictSignal struct {
	Outcome VerdictOutcome
	Cause   VerdictCause
}

// VerdictCleanupErrorCode is a closed post-terminal cleanup vocabulary.
type VerdictCleanupErrorCode string

const (
	VerdictCleanupWorkerStop VerdictCleanupErrorCode = "worker_stop"
	VerdictCleanupSnapshot   VerdictCleanupErrorCode = "snapshot"
	VerdictCleanupObserver   VerdictCleanupErrorCode = "observer"
)

// CorrectnessCounters is one cumulative, generation-fenced correctness view.
type CorrectnessCounters struct {
	FirstAttempts        uint64
	FirstAttemptFailures uint64
	TerminalSends        uint64
	ActivationRejections uint64
	Losses               uint64
	Duplicates           uint64
	Corruptions          uint64
	SequenceRegressions  uint64
	QueueSaturations     uint64
	ObserverGaps         uint64
}

// VerdictObservation is one atomic evidence batch.
type VerdictObservation struct {
	At          time.Time
	Correctness *CorrectnessCounters
	Latency     *LatencyCounters
	// LatencyAttribution classifies the resource and load-delivery evidence
	// overlapping this latency cut. Empty preserves legacy product attribution.
	LatencyAttribution CapacityAttribution
	Resources          []NodeResourceSample
	Signals            []VerdictSignal
}

// VerdictSnapshot is a bounded projection. Pass is provisional until Terminal.
type VerdictSnapshot struct {
	Outcome             VerdictOutcome
	Cause               VerdictCause
	Terminal            bool
	CleanupErrorCount   uint64
	CleanupErrors       []VerdictCleanupErrorCode
	LatencyWarnings     LatencyWarningCounts
	LatencyAnomalyCount uint64
	LatencyAnomalies    []LatencyAnomaly
	Retention           VerdictWindowRetention
}

// VerdictWindowRetention exposes only fixed reducer sizes for bounded-memory
// audits; it contains no raw evidence samples.
type VerdictWindowRetention struct {
	MinuteSamples     int
	MinuteCapacity    int
	LatencySamples    [3]int
	LatencyCapacity   int
	HeapSamples       [3]int
	HeapCapacity      int
	GoroutineSamples  [3]int
	GoroutineCapacity int
}

type verdictLatencyState struct {
	p99, p999         *CounterWindow
	breachSince       *time.Time
	breachAttribution CapacityAttribution
}

type verdictResourceNode struct {
	used             bool
	nodeID           uint64
	heap, goroutines *GaugeWindow
	baselineSeen     bool
	baselineQueue    uint64
	baselineInflight uint64
	burstActive      bool
}

// VerdictEvaluator reduces bounded rolling evidence into one frozen outcome.
type VerdictEvaluator struct {
	start               time.Time
	last                time.Time
	thresholds          ThresholdsConfig
	snapshot            VerdictSnapshot
	correctnessSeen     bool
	previousCorrectness CorrectnessCounters
	minuteFailures      *CounterWindow
	cleanupErrors       [maxVerdictCleanupErrors]VerdictCleanupErrorCode
	cleanupHead         int
	cleanupSize         int
	cleanupCount        uint64
	latencySeen         bool
	previousLatency     LatencyCounters
	latency             [3]verdictLatencyState
	latencyWarnings     LatencyWarningCounts
	latencyAnomalies    [maxVerdictLatencyAnomalies]LatencyAnomaly
	latencyAnomalyHead  int
	latencyAnomalySize  int
	latencyAnomalyCount uint64
	latencyEvidence     [3]bool
	capacityWarning     bool
	resources           [3]verdictResourceNode
	heapWindowCapacity  int
	goroWindowCapacity  int
}

// NewVerdictEvaluator validates immutable thresholds before retaining evidence.
func NewVerdictEvaluator(start time.Time, thresholds ThresholdsConfig) (*VerdictEvaluator, error) {
	if start.IsZero() || validateThresholds(thresholds) != nil ||
		thresholds.Latency.SustainedBreachWindow != 5*time.Minute || thresholds.Latency.SingleAnomaly != 10*time.Second {
		return nil, ErrVerdictConfig
	}
	minuteFailures, err := NewCounterWindow(time.Minute, 16)
	if err != nil {
		return nil, ErrVerdictConfig
	}
	var latency [3]verdictLatencyState
	for index := range latency {
		latency[index].p99, err = NewCounterWindow(thresholds.Latency.SustainedBreachWindow, verdictLatencyWindowSamples)
		if err != nil {
			return nil, ErrVerdictConfig
		}
		latency[index].p999, err = NewCounterWindow(thresholds.Latency.SustainedBreachWindow, verdictLatencyWindowSamples)
		if err != nil {
			return nil, ErrVerdictConfig
		}
	}
	heapCapacity, err := verdictHourlyWindowCapacity(thresholds.Resource.ForcedGCLiveHeapWindow)
	if err != nil {
		return nil, ErrVerdictConfig
	}
	goroCapacity, err := verdictHourlyWindowCapacity(thresholds.Resource.GoroutineGrowthWindow)
	if err != nil {
		return nil, ErrVerdictConfig
	}
	return &VerdictEvaluator{
		start: start, thresholds: thresholds,
		snapshot:           VerdictSnapshot{Outcome: VerdictPass},
		minuteFailures:     minuteFailures,
		latency:            latency,
		heapWindowCapacity: heapCapacity,
		goroWindowCapacity: goroCapacity,
	}, nil
}

// Observe applies one evidence batch. The first terminal outcome is immutable.
func (v *VerdictEvaluator) Observe(observation VerdictObservation) error {
	if v == nil {
		return ErrVerdictObservation
	}
	if observation.At.IsZero() || observation.At.Before(v.start) || (!v.last.IsZero() && !observation.At.After(v.last)) {
		v.setTerminal(VerdictHarnessInvalid, VerdictCauseInvalidObservation)
		return ErrVerdictObservation
	}
	if !validLatencyAttribution(observation.LatencyAttribution) {
		v.setTerminal(VerdictHarnessInvalid, VerdictCauseInvalidObservation)
		return ErrVerdictObservation
	}
	v.last = observation.At
	if v.snapshot.Terminal {
		return nil
	}
	var selected VerdictSignal
	var observationErr error
	selectSignal := func(signal VerdictSignal) {
		if verdictSignalPrecedes(signal, selected) {
			selected = signal
		}
	}
	if observation.Correctness != nil {
		counters := *observation.Correctness
		if counters.FirstAttemptFailures > counters.FirstAttempts ||
			(v.correctnessSeen && correctnessCountersRegressed(counters, v.previousCorrectness)) {
			selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseCounterRegression})
		} else {
			deltaAttempts, deltaFailures := counters.FirstAttempts, counters.FirstAttemptFailures
			if v.correctnessSeen {
				deltaAttempts -= v.previousCorrectness.FirstAttempts
				deltaFailures -= v.previousCorrectness.FirstAttemptFailures
			}
			v.correctnessSeen = true
			v.previousCorrectness = counters
			thresholds := v.thresholds.Correctness
			for _, check := range []struct {
				actual  uint64
				maximum int
				cause   VerdictCause
			}{
				{counters.Losses, thresholds.Losses, VerdictCauseMessageLoss},
				{counters.Duplicates, thresholds.Duplicates, VerdictCauseMessageDuplicate},
				{counters.Corruptions, thresholds.Corruptions, VerdictCauseMessageCorruption},
				{counters.SequenceRegressions, thresholds.SequenceRegressions, VerdictCauseSequenceRegression},
				{counters.TerminalSends, thresholds.TerminalSends, VerdictCauseTerminalSend},
				{counters.ActivationRejections, thresholds.ActivationRejections, VerdictCauseActivationRejection},
			} {
				if check.actual > uint64(check.maximum) {
					selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: check.cause})
				}
			}
			if rationalViolates(counters.FirstAttemptFailures, counters.FirstAttempts, thresholds.OverallFirstAttemptFailure) {
				selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseOverallFirstAttemptRate})
			}
			if err := v.minuteFailures.Add(observation.At, deltaFailures, deltaAttempts); err != nil {
				selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseCounterRegression})
			} else {
				failures, attempts := v.minuteFailures.Sum()
				if rationalViolates(failures, attempts, thresholds.AnyMinuteFirstAttemptFailure) {
					selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseMinuteFirstAttemptRate})
				}
			}
			if counters.QueueSaturations > 0 {
				selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseQueueSaturation})
			}
			if counters.ObserverGaps > 0 {
				selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseObserverGap})
			}
		}
	}
	if observation.Latency != nil {
		current := *observation.Latency
		if !validLatencyCounters(current, v.thresholds.Latency) || (v.latencySeen && latencyCountersRegressed(current, v.previousLatency)) {
			selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseCounterRegression})
		} else {
			hadBaseline := v.latencySeen
			delta := current
			if hadBaseline {
				delta = subtractLatencyCounters(current, v.previousLatency)
			}
			v.latencySeen = true
			v.previousLatency = current
			if hadBaseline && observation.At.After(v.start.Add(v.thresholds.Timeline.Warmup)) {
				operations := [...]struct {
					operation LatencyOperation
					counters  LatencyThresholdCounters
					cause     VerdictCause
				}{
					{LatencyHotSendACK, delta.Hot, VerdictCauseHotLatency},
					{LatencyColdSendACK, delta.Cold, VerdictCauseColdLatency},
					{LatencyFullSync, delta.Sync, VerdictCauseSyncLatency},
				}
				for index, operation := range operations {
					state := &v.latency[index]
					if operation.counters.Count > 0 {
						v.latencyEvidence[index] = true
					}
					if err := state.p99.Add(observation.At, operation.counters.AboveP99, operation.counters.Count); err != nil {
						selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseCounterRegression})
						continue
					}
					if err := state.p999.Add(observation.At, operation.counters.AboveP999, operation.counters.Count); err != nil {
						selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseCounterRegression})
						continue
					}
					if operation.counters.Above10Seconds > 0 {
						v.recordLatencyAnomaly(LatencyAnomaly{At: observation.At, Operation: operation.operation, Count: operation.counters.Above10Seconds})
					}
					aboveP99, countP99 := state.p99.Sum()
					aboveP999, countP999 := state.p999.Sum()
					breached := ratioAboveUnitFraction(aboveP99, countP99, 100) || ratioAboveUnitFraction(aboveP999, countP999, 1_000)
					if !breached {
						state.breachSince = nil
						state.breachAttribution = CapacityAttributionNone
						continue
					}
					if state.breachSince == nil {
						started := observation.At
						state.breachSince = &started
						state.breachAttribution = normalizedLatencyAttribution(observation.LatencyAttribution)
						v.incrementLatencyWarning(operation.operation)
						continue
					}
					state.breachAttribution = mergeLatencyAttribution(
						state.breachAttribution,
						normalizedLatencyAttribution(observation.LatencyAttribution),
					)
					if observation.At.Sub(*state.breachSince) >= v.thresholds.Latency.SustainedBreachWindow {
						switch state.breachAttribution {
						case CapacityAttributionInfrastructure:
							v.capacityWarning = true
							v.snapshot.Outcome = VerdictPassedWithCapacityWarning
							v.snapshot.Cause = VerdictCauseInfrastructureCapacity
						case CapacityAttributionInsufficient:
							selectSignal(VerdictSignal{Outcome: VerdictInsufficientEvidence, Cause: VerdictCauseInsufficientEvidence})
						default:
							selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: operation.cause})
						}
					}
				}
			}
		}
	}
	if len(observation.Resources) > 0 {
		if !v.validResourceBatch(observation) {
			selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation})
			observationErr = ErrVerdictObservation
		} else {
			for _, sample := range observation.Resources {
				state := v.resourceNode(sample.NodeID)
				queue, inflight := uint64(sample.QueueDepth), uint64(sample.Inflight)
				warmupEnd := v.start.Add(v.thresholds.Timeline.Warmup)
				if !observation.At.After(warmupEnd) && sample.Burst == ResourceBurstNone {
					state.baselineSeen = true
					if queue > state.baselineQueue {
						state.baselineQueue = queue
					}
					if inflight > state.baselineInflight {
						state.baselineInflight = inflight
					}
				}
				if sample.ForcedGC && !observation.At.Before(warmupEnd) {
					heap, goroutines := uint64(sample.HeapBytes), uint64(sample.Goroutines)
					if !state.baselineSeen {
						selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation})
						observationErr = ErrVerdictObservation
						continue
					}
					if err := state.heap.Add(observation.At, heap); err != nil {
						selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation})
						observationErr = ErrVerdictObservation
					} else if ready, exceeded, _ := state.heap.GrowthExceeds(uint64(v.thresholds.Resource.ForcedGCLiveHeapGrowthPercent)); ready && exceeded {
						selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseHeapGrowth})
					}
					if err := state.goroutines.Add(observation.At, goroutines); err != nil {
						selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation})
						observationErr = ErrVerdictObservation
					} else if ready, exceeded, _ := state.goroutines.GrowthExceeds(uint64(v.thresholds.Resource.GoroutineGrowthPercent)); ready && exceeded {
						selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseGoroutineGrowth})
					}
				}
				if sample.Burst != ResourceBurstNone && !state.baselineSeen {
					selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation})
					observationErr = ErrVerdictObservation
					continue
				}
				switch sample.Burst {
				case ResourceBurstActive:
					state.burstActive = true
				case ResourceBurstEnded:
					if !state.burstActive {
						selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation})
						observationErr = ErrVerdictObservation
					} else if queue > state.baselineQueue || inflight > state.baselineInflight {
						selectSignal(VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseQueueRecovery})
					}
					state.burstActive = false
				}
			}
		}
	}
	for _, signal := range observation.Signals {
		if validVerdictSignal(signal) {
			selectSignal(signal)
		} else {
			selectSignal(VerdictSignal{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseCounterRegression})
		}
	}
	if selected.Outcome != "" {
		v.setTerminal(selected.Outcome, selected.Cause)
	}
	return observationErr
}

func validVerdictSignal(signal VerdictSignal) bool {
	switch signal.Outcome {
	case VerdictProductFailure:
		return signal.Cause == VerdictCauseServerCrash || signal.Cause == VerdictCauseWorkerProduct ||
			signal.Cause == VerdictCauseLifecycleProduct || signal.Cause == VerdictCauseMetaCreateProduct
	case VerdictInfrastructureFailure:
		return signal.Cause == VerdictCauseDiskExhausted || signal.Cause == VerdictCauseBudgetExhausted ||
			signal.Cause == VerdictCauseLeaseExpiry
	case VerdictHarnessInvalid:
		return signal.Cause == VerdictCauseObserverGap || signal.Cause == VerdictCauseQueueSaturation ||
			signal.Cause == VerdictCauseCounterRegression || signal.Cause == VerdictCauseWorkerHarness ||
			signal.Cause == VerdictCauseLifecycleHarness
	case VerdictOperatorStop:
		return signal.Cause == VerdictCauseOperatorRequested
	default:
		return false
	}
}

func verdictSignalPrecedes(candidate, current VerdictSignal) bool {
	if current.Outcome == "" {
		return true
	}
	candidateRank, currentRank := verdictOutcomeRank(candidate.Outcome), verdictOutcomeRank(current.Outcome)
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	return verdictCauseRank(candidate.Cause) < verdictCauseRank(current.Cause)
}

func verdictOutcomeRank(outcome VerdictOutcome) int {
	switch outcome {
	case VerdictProductFailure:
		return 4
	case VerdictInfrastructureFailure:
		return 3
	case VerdictHarnessInvalid, VerdictInsufficientEvidence:
		return 2
	case VerdictOperatorStop:
		return 1
	default:
		return 0
	}
}

func verdictCauseRank(cause VerdictCause) int {
	causes := [...]VerdictCause{
		VerdictCauseMessageLoss, VerdictCauseMessageDuplicate, VerdictCauseMessageCorruption,
		VerdictCauseSequenceRegression, VerdictCauseTerminalSend, VerdictCauseActivationRejection,
		VerdictCauseOverallFirstAttemptRate, VerdictCauseMinuteFirstAttemptRate,
		VerdictCauseHotLatency, VerdictCauseColdLatency, VerdictCauseSyncLatency,
		VerdictCauseHeapGrowth, VerdictCauseGoroutineGrowth, VerdictCauseQueueRecovery, VerdictCauseServerCrash,
		VerdictCauseDiskExhausted, VerdictCauseBudgetExhausted, VerdictCauseLeaseExpiry,
		VerdictCauseCounterRegression, VerdictCauseQueueSaturation,
		VerdictCauseObserverGap, VerdictCauseInvalidObservation, VerdictCauseOperatorRequested,
		VerdictCauseWorkerProduct, VerdictCauseWorkerHarness,
		VerdictCauseLifecycleProduct, VerdictCauseLifecycleHarness, VerdictCauseMetaCreateProduct,
		VerdictCauseRehearsalCompleted,
	}
	for index, candidate := range causes {
		if candidate == cause {
			return index
		}
	}
	return len(causes)
}

func (v *VerdictEvaluator) validResourceBatch(observation VerdictObservation) bool {
	if len(observation.Resources) != len(v.resources) {
		return false
	}
	var seen [3]uint64
	for index, sample := range observation.Resources {
		runtimeValid := sample.HeapBytes == 0 && sample.Goroutines == 0
		if sample.ForcedGC {
			runtimeValid = observation.At.Sub(v.start)%time.Hour == 0 &&
				validPrometheusGauge(sample.HeapBytes) && validPrometheusGauge(sample.Goroutines)
		}
		if sample.NodeID == 0 || !runtimeValid ||
			!validPrometheusGauge(sample.QueueDepth) || !validPrometheusGauge(sample.Inflight) ||
			(sample.Burst != ResourceBurstNone && sample.Burst != ResourceBurstActive && sample.Burst != ResourceBurstEnded) {
			return false
		}
		for prior := 0; prior < index; prior++ {
			if seen[prior] == sample.NodeID {
				return false
			}
		}
		seen[index] = sample.NodeID
		if v.resourceNodeIndex(sample.NodeID) < 0 {
			free := false
			for _, state := range v.resources {
				if !state.used {
					free = true
					break
				}
			}
			if !free {
				return false
			}
		}
	}
	for _, state := range v.resources {
		if !state.used {
			continue
		}
		found := false
		for _, nodeID := range seen {
			found = found || nodeID == state.nodeID
		}
		if !found {
			return false
		}
	}
	return true
}

func validPrometheusGauge(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < math.Exp2(64) && math.Trunc(value) == value
}

func (v *VerdictEvaluator) resourceNodeIndex(nodeID uint64) int {
	for index := range v.resources {
		if v.resources[index].used && v.resources[index].nodeID == nodeID {
			return index
		}
	}
	return -1
}

func (v *VerdictEvaluator) resourceNode(nodeID uint64) *verdictResourceNode {
	if index := v.resourceNodeIndex(nodeID); index >= 0 {
		return &v.resources[index]
	}
	for index := range v.resources {
		if v.resources[index].used {
			continue
		}
		heap, _ := NewGaugeWindow(v.thresholds.Resource.ForcedGCLiveHeapWindow, v.heapWindowCapacity)
		goroutines, _ := NewGaugeWindow(v.thresholds.Resource.GoroutineGrowthWindow, v.goroWindowCapacity)
		v.resources[index] = verdictResourceNode{used: true, nodeID: nodeID, heap: heap, goroutines: goroutines}
		return &v.resources[index]
	}
	return nil
}

func verdictHourlyWindowCapacity(span time.Duration) (int, error) {
	if span <= 0 || span%time.Hour != 0 || span > maxVerdictResourceWindow {
		return 0, ErrVerdictConfig
	}
	hours := span / time.Hour
	maxInt := int(^uint(0) >> 1)
	if hours >= time.Duration(maxInt) {
		return 0, ErrVerdictConfig
	}
	return int(hours) + 1, nil
}

func validLatencyCounters(counters LatencyCounters, thresholds LatencyThresholds) bool {
	if counters.Hot.P99Limit != thresholds.HotSendACK.P99 || counters.Hot.P999Limit != thresholds.HotSendACK.P999 ||
		counters.Cold.P99Limit != thresholds.Cold.P99 || counters.Cold.P999Limit != thresholds.Cold.P999 ||
		counters.Sync.P99Limit != thresholds.Sync.P99 || counters.Sync.P999Limit != thresholds.Sync.P999 {
		return false
	}
	for _, operation := range [...]LatencyThresholdCounters{counters.Hot, counters.Cold, counters.Sync} {
		if operation.AboveP99 > operation.Count || operation.AboveP999 > operation.AboveP99 || operation.Above10Seconds > operation.AboveP999 {
			return false
		}
	}
	return true
}

func latencyCountersRegressed(current, previous LatencyCounters) bool {
	currentValues := [...]uint64{
		current.Hot.Count, current.Hot.AboveP99, current.Hot.AboveP999, current.Hot.Above10Seconds,
		current.Cold.Count, current.Cold.AboveP99, current.Cold.AboveP999, current.Cold.Above10Seconds,
		current.Sync.Count, current.Sync.AboveP99, current.Sync.AboveP999, current.Sync.Above10Seconds,
	}
	previousValues := [...]uint64{
		previous.Hot.Count, previous.Hot.AboveP99, previous.Hot.AboveP999, previous.Hot.Above10Seconds,
		previous.Cold.Count, previous.Cold.AboveP99, previous.Cold.AboveP999, previous.Cold.Above10Seconds,
		previous.Sync.Count, previous.Sync.AboveP99, previous.Sync.AboveP999, previous.Sync.Above10Seconds,
	}
	for index := range currentValues {
		if currentValues[index] < previousValues[index] {
			return true
		}
	}
	return false
}

func subtractLatencyCounters(current, previous LatencyCounters) LatencyCounters {
	subtract := func(left, right LatencyThresholdCounters) LatencyThresholdCounters {
		return LatencyThresholdCounters{
			Count: left.Count - right.Count, AboveP99: left.AboveP99 - right.AboveP99,
			AboveP999: left.AboveP999 - right.AboveP999, Above10Seconds: left.Above10Seconds - right.Above10Seconds,
		}
	}
	return LatencyCounters{
		Hot: subtract(current.Hot, previous.Hot), Cold: subtract(current.Cold, previous.Cold),
		Sync: subtract(current.Sync, previous.Sync),
	}
}

func ratioAboveUnitFraction(numerator, denominator, scale uint64) bool {
	if denominator == 0 {
		return numerator > 0
	}
	high, low := bits.Mul64(numerator, scale)
	return high > 0 || low > denominator
}

func (v *VerdictEvaluator) incrementLatencyWarning(operation LatencyOperation) {
	value := func(counter *uint64) {
		if *counter < math.MaxUint64 {
			*counter++
		}
	}
	switch operation {
	case LatencyHotSendACK:
		value(&v.latencyWarnings.Hot)
	case LatencyColdSendACK:
		value(&v.latencyWarnings.Cold)
	case LatencyFullSync:
		value(&v.latencyWarnings.Sync)
	}
}

func validLatencyAttribution(attribution CapacityAttribution) bool {
	return attribution == CapacityAttributionNone || attribution == CapacityAttributionInfrastructure ||
		attribution == CapacityAttributionProduct || attribution == CapacityAttributionInsufficient
}

func normalizedLatencyAttribution(attribution CapacityAttribution) CapacityAttribution {
	if attribution == CapacityAttributionNone {
		return CapacityAttributionProduct
	}
	return attribution
}

func mergeLatencyAttribution(left, right CapacityAttribution) CapacityAttribution {
	if left == CapacityAttributionInfrastructure || right == CapacityAttributionInfrastructure {
		return CapacityAttributionInfrastructure
	}
	if left == CapacityAttributionProduct && right == CapacityAttributionProduct {
		return CapacityAttributionProduct
	}
	return CapacityAttributionInsufficient
}

func (v *VerdictEvaluator) recordLatencyAnomaly(anomaly LatencyAnomaly) {
	if math.MaxUint64-v.latencyAnomalyCount < anomaly.Count {
		v.latencyAnomalyCount = math.MaxUint64
	} else {
		v.latencyAnomalyCount += anomaly.Count
	}
	if v.latencyAnomalySize < len(v.latencyAnomalies) {
		position := (v.latencyAnomalyHead + v.latencyAnomalySize) % len(v.latencyAnomalies)
		v.latencyAnomalies[position] = anomaly
		v.latencyAnomalySize++
		return
	}
	v.latencyAnomalies[v.latencyAnomalyHead] = anomaly
	v.latencyAnomalyHead = (v.latencyAnomalyHead + 1) % len(v.latencyAnomalies)
}

func correctnessCountersRegressed(current, previous CorrectnessCounters) bool {
	currentValues := [...]uint64{
		current.FirstAttempts, current.FirstAttemptFailures, current.TerminalSends,
		current.ActivationRejections, current.Losses, current.Duplicates,
		current.Corruptions, current.SequenceRegressions, current.QueueSaturations,
		current.ObserverGaps,
	}
	previousValues := [...]uint64{
		previous.FirstAttempts, previous.FirstAttemptFailures, previous.TerminalSends,
		previous.ActivationRejections, previous.Losses, previous.Duplicates,
		previous.Corruptions, previous.SequenceRegressions, previous.QueueSaturations,
		previous.ObserverGaps,
	}
	for index := range currentValues {
		if currentValues[index] < previousValues[index] {
			return true
		}
	}
	return false
}

func rationalViolates(numerator, denominator uint64, limit FailureRateLimit) bool {
	if denominator == 0 {
		return numerator > 0
	}
	leftHigh, leftLow := bits.Mul64(numerator, uint64(limit.PerAttempts))
	rightHigh, rightLow := bits.Mul64(denominator, uint64(limit.MaxFailures))
	comparison := 0
	if leftHigh < rightHigh || (leftHigh == rightHigh && leftLow < rightLow) {
		comparison = -1
	} else if leftHigh > rightHigh || (leftHigh == rightHigh && leftLow > rightLow) {
		comparison = 1
	}
	if limit.Operator == ComparisonLessThan {
		return comparison >= 0
	}
	return comparison > 0
}

func (v *VerdictEvaluator) setTerminal(outcome VerdictOutcome, cause VerdictCause) {
	if v.snapshot.Terminal {
		return
	}
	v.snapshot = VerdictSnapshot{Outcome: outcome, Cause: cause, Terminal: true}
}

// Finalize freezes a passing run if no earlier terminal evidence exists.
func (v *VerdictEvaluator) Finalize(at time.Time) error {
	if v == nil || at.IsZero() || at.Before(v.start.Add(v.thresholds.Timeline.Final)) || (!v.last.IsZero() && at.Before(v.last)) {
		if v != nil {
			v.setTerminal(VerdictHarnessInvalid, VerdictCauseInvalidObservation)
		}
		return ErrVerdictObservation
	}
	if v.snapshot.Terminal {
		return nil
	}
	v.last = at
	for _, state := range v.resources {
		if state.used && state.burstActive {
			v.setTerminal(VerdictProductFailure, VerdictCauseQueueRecovery)
			return nil
		}
	}
	if !v.completeEvidenceAt(at) {
		v.setTerminal(VerdictHarnessInvalid, VerdictCauseInvalidObservation)
		return ErrVerdictObservation
	}
	if v.capacityWarning {
		v.setTerminal(VerdictPassedWithCapacityWarning, VerdictCauseInfrastructureCapacity)
	} else {
		v.setTerminal(VerdictPass, VerdictCauseCompleted)
	}
	return nil
}

func (v *VerdictEvaluator) completeEvidenceAt(at time.Time) bool {
	if !v.correctnessSeen || v.previousCorrectness.FirstAttempts == 0 {
		return false
	}
	for _, seen := range v.latencyEvidence {
		if !seen {
			return false
		}
	}
	for index := range v.resources {
		state := &v.resources[index]
		if !state.used || !state.baselineSeen || state.heap == nil || state.goroutines == nil ||
			state.heap.last.IsZero() || state.goroutines.last.IsZero() ||
			at.Sub(state.heap.last) > time.Hour || at.Sub(state.goroutines.last) > time.Hour {
			return false
		}
		heapReady, _, heapErr := state.heap.GrowthExceeds(uint64(v.thresholds.Resource.ForcedGCLiveHeapGrowthPercent))
		goroutineReady, _, goroutineErr := state.goroutines.GrowthExceeds(uint64(v.thresholds.Resource.GoroutineGrowthPercent))
		if heapErr != nil || goroutineErr != nil || !heapReady || !goroutineReady {
			return false
		}
	}
	return true
}

// RecordCleanupError retains only the bounded last cleanup codes and never
// changes the first terminal run outcome.
func (v *VerdictEvaluator) RecordCleanupError(code VerdictCleanupErrorCode) {
	if v == nil || !v.snapshot.Terminal ||
		(code != VerdictCleanupWorkerStop && code != VerdictCleanupSnapshot && code != VerdictCleanupObserver) {
		return
	}
	if v.cleanupCount < math.MaxUint64 {
		v.cleanupCount++
	}
	if v.cleanupSize < len(v.cleanupErrors) {
		position := (v.cleanupHead + v.cleanupSize) % len(v.cleanupErrors)
		v.cleanupErrors[position] = code
		v.cleanupSize++
		return
	}
	v.cleanupErrors[v.cleanupHead] = code
	v.cleanupHead = (v.cleanupHead + 1) % len(v.cleanupErrors)
}

// Snapshot returns the fixed verdict state.
func (v *VerdictEvaluator) Snapshot() VerdictSnapshot {
	if v == nil {
		return VerdictSnapshot{}
	}
	snapshot := v.snapshot
	snapshot.CleanupErrorCount = v.cleanupCount
	snapshot.CleanupErrors = make([]VerdictCleanupErrorCode, v.cleanupSize)
	for index := 0; index < v.cleanupSize; index++ {
		snapshot.CleanupErrors[index] = v.cleanupErrors[(v.cleanupHead+index)%len(v.cleanupErrors)]
	}
	snapshot.LatencyWarnings = v.latencyWarnings
	snapshot.LatencyAnomalyCount = v.latencyAnomalyCount
	snapshot.LatencyAnomalies = make([]LatencyAnomaly, v.latencyAnomalySize)
	for index := 0; index < v.latencyAnomalySize; index++ {
		snapshot.LatencyAnomalies[index] = v.latencyAnomalies[(v.latencyAnomalyHead+index)%len(v.latencyAnomalies)]
	}
	snapshot.Retention.MinuteSamples = v.minuteFailures.Len()
	snapshot.Retention.MinuteCapacity = v.minuteFailures.Capacity()
	snapshot.Retention.LatencyCapacity = verdictLatencyWindowSamples
	snapshot.Retention.HeapCapacity = v.heapWindowCapacity
	snapshot.Retention.GoroutineCapacity = v.goroWindowCapacity
	for index := range v.latency {
		snapshot.Retention.LatencySamples[index] = max(v.latency[index].p99.Len(), v.latency[index].p999.Len())
		if v.resources[index].used {
			snapshot.Retention.HeapSamples[index] = v.resources[index].heap.Len()
			snapshot.Retention.GoroutineSamples[index] = v.resources[index].goroutines.Len()
		}
	}
	return snapshot
}
