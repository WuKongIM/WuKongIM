package chatlifecycle

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestVerdictCorrectnessViolationsFailImmediately(t *testing.T) {
	start := time.Unix(10_000, 0)
	for _, test := range []struct {
		name   string
		mutate func(*CorrectnessCounters)
		cause  VerdictCause
	}{
		{"loss", func(c *CorrectnessCounters) { c.Losses = 1 }, VerdictCauseMessageLoss},
		{"duplicate", func(c *CorrectnessCounters) { c.Duplicates = 1 }, VerdictCauseMessageDuplicate},
		{"corruption", func(c *CorrectnessCounters) { c.Corruptions = 1 }, VerdictCauseMessageCorruption},
		{"sequence regression", func(c *CorrectnessCounters) { c.SequenceRegressions = 1 }, VerdictCauseSequenceRegression},
		{"terminal send", func(c *CorrectnessCounters) { c.TerminalSends = 1 }, VerdictCauseTerminalSend},
		{"activation rejection", func(c *CorrectnessCounters) { c.ActivationRejections = 1 }, VerdictCauseActivationRejection},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			counters := CorrectnessCounters{FirstAttempts: 10_001}
			test.mutate(&counters)
			if err := evaluator.Observe(VerdictObservation{At: start, Correctness: &counters}); err != nil {
				t.Fatal(err)
			}
			snapshot := evaluator.Snapshot()
			if !snapshot.Terminal || snapshot.Outcome != VerdictProductFailure || snapshot.Cause != test.cause {
				t.Fatalf("verdict = %+v, want immediate product failure %q", snapshot, test.cause)
			}
		})
	}
}

func TestVerdictFirstAttemptRatesUseExactWholeAndMinuteBoundaries(t *testing.T) {
	start := time.Unix(20_000, 0)

	equalWhole, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if err := equalWhole.Observe(VerdictObservation{At: start, Correctness: &CorrectnessCounters{
		FirstAttempts: 10_000, FirstAttemptFailures: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if got := equalWhole.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseOverallFirstAttemptRate {
		t.Fatalf("whole-run equality = %+v, want strict product failure", got)
	}

	minute, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	observations := []struct {
		at       time.Time
		attempts uint64
		failures uint64
	}{
		{start, 1_000_000, 0},
		{start.Add(time.Minute), 1_001_000, 1},
	}
	for _, observation := range observations {
		if err := minute.Observe(VerdictObservation{At: observation.at, Correctness: &CorrectnessCounters{
			FirstAttempts: observation.attempts, FirstAttemptFailures: observation.failures,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := minute.Snapshot(); got.Terminal {
		t.Fatalf("one-minute equality should pass: %+v", got)
	}
	if err := minute.Observe(VerdictObservation{At: start.Add(90 * time.Second), Correctness: &CorrectnessCounters{
		FirstAttempts: 1_001_999, FirstAttemptFailures: 2,
	}}); err != nil {
		t.Fatal(err)
	}
	if got := minute.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseMinuteFirstAttemptRate {
		t.Fatalf("one-minute excess = %+v, want product failure", got)
	}
}

func TestVerdictCounterRegressionAndHarnessGapsAreHarnessInvalid(t *testing.T) {
	start := time.Unix(30_000, 0)
	for _, test := range []struct {
		name  string
		first CorrectnessCounters
		next  CorrectnessCounters
		cause VerdictCause
	}{
		{
			name: "counter regression", first: CorrectnessCounters{FirstAttempts: 100},
			next: CorrectnessCounters{FirstAttempts: 99}, cause: VerdictCauseCounterRegression,
		},
		{
			name: "queue saturation", next: CorrectnessCounters{QueueSaturations: 1},
			cause: VerdictCauseQueueSaturation,
		},
		{
			name: "observer gap", next: CorrectnessCounters{ObserverGaps: 1},
			cause: VerdictCauseObserverGap,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			if test.first != (CorrectnessCounters{}) {
				if err := evaluator.Observe(VerdictObservation{At: start, Correctness: &test.first}); err != nil {
					t.Fatal(err)
				}
			}
			if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Second), Correctness: &test.next}); err != nil {
				t.Fatal(err)
			}
			if got := evaluator.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != test.cause {
				t.Fatalf("verdict = %+v, want harness %q", got, test.cause)
			}
		})
	}
}

func TestVerdictBatchPrecedenceIsDeterministicAndFirstTerminalFreezes(t *testing.T) {
	start := time.Unix(40_000, 0)
	signals := []VerdictSignal{
		{Outcome: VerdictOperatorStop, Cause: VerdictCauseOperatorRequested},
		{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseObserverGap},
		{Outcome: VerdictInfrastructureFailure, Cause: VerdictCauseDiskExhausted},
		{Outcome: VerdictProductFailure, Cause: VerdictCauseServerCrash},
	}
	for _, ordered := range [][]VerdictSignal{signals, {signals[3], signals[2], signals[1], signals[0]}} {
		evaluator, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
		if err != nil {
			t.Fatal(err)
		}
		if err := evaluator.Observe(VerdictObservation{At: start, Signals: ordered}); err != nil {
			t.Fatal(err)
		}
		if got := evaluator.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseServerCrash {
			t.Fatalf("precedence verdict = %+v, want product server crash", got)
		}
	}

	frozen, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if err := frozen.Observe(VerdictObservation{At: start, Signals: []VerdictSignal{{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseObserverGap}}}); err != nil {
		t.Fatal(err)
	}
	if err := frozen.Observe(VerdictObservation{At: start.Add(time.Second), Signals: []VerdictSignal{{Outcome: VerdictProductFailure, Cause: VerdictCauseServerCrash}}}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 40; index++ {
		frozen.RecordCleanupError(VerdictCleanupWorkerStop)
	}
	got := frozen.Snapshot()
	if got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseObserverGap {
		t.Fatalf("first cause was overwritten: %+v", got)
	}
	if got.CleanupErrorCount != 40 || len(got.CleanupErrors) != maxVerdictCleanupErrors {
		t.Fatalf("cleanup retention = count %d retained %d, want 40/%d", got.CleanupErrorCount, len(got.CleanupErrors), maxVerdictCleanupErrors)
	}
}

func TestVerdictFinalizeProducesTerminalPass(t *testing.T) {
	start := time.Unix(50_000, 0)
	evaluator, err := NewVerdictEvaluator(start, LocalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if err := evaluator.Finalize(start.Add(72 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); !got.Terminal || got.Outcome != VerdictPass || got.Cause != VerdictCauseCompleted {
		t.Fatalf("final pass = %+v", got)
	}
}

func TestVerdictConfigurationBindsTenSecondAnomalyThreshold(t *testing.T) {
	thresholds := FormalConfig().Thresholds
	thresholds.Latency.SingleAnomaly = 9 * time.Second
	if evaluator, err := NewVerdictEvaluator(time.Unix(54_000, 0), thresholds); !errors.Is(err, ErrVerdictConfig) || evaluator != nil {
		t.Fatalf("anomaly threshold evaluator = %#v, %v", evaluator, err)
	}
}

func TestVerdictIgnoresCleanupErrorsBeforeTerminal(t *testing.T) {
	evaluator, err := NewVerdictEvaluator(time.Unix(54_500, 0), LocalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	evaluator.RecordCleanupError(VerdictCleanupWorkerStop)
	if got := evaluator.Snapshot(); got.CleanupErrorCount != 0 || len(got.CleanupErrors) != 0 {
		t.Fatalf("pre-terminal cleanup evidence = %+v", got)
	}
}

func TestVerdictCannotFinalizeBeforeConfiguredRunEnd(t *testing.T) {
	start := time.Unix(55_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if err := evaluator.Finalize(start.Add(thresholds.Timeline.Final - time.Nanosecond)); !errors.Is(err, ErrVerdictObservation) {
		t.Fatalf("early finalize error = %v", err)
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseInvalidObservation {
		t.Fatalf("early finalize verdict = %+v", got)
	}
}

func TestLatencyWindowHonorsWarmupExactQuantileEdgesAndAnomalies(t *testing.T) {
	start := time.Unix(60_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	warmupEnd := start.Add(thresholds.Timeline.Warmup)
	preWarm := LatencyCountersForThresholds(thresholds.Latency)
	preWarm.Hot.Count, preWarm.Hot.AboveP99, preWarm.Hot.AboveP999, preWarm.Hot.Above10Seconds = 100, 100, 100, 100
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(-time.Hour), Latency: &preWarm}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Terminal || got.LatencyWarnings.Hot != 0 {
		t.Fatalf("warmup latency affected verdict: %+v", got)
	}
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd, Latency: &preWarm}); err != nil {
		t.Fatal(err)
	}

	edge := preWarm
	edge.Hot.Count += 1_000
	edge.Hot.AboveP99 += 10
	edge.Hot.AboveP999++
	edge.Hot.Above10Seconds++
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(time.Minute), Latency: &edge}); err != nil {
		t.Fatal(err)
	}
	got := evaluator.Snapshot()
	if got.Terminal || got.LatencyWarnings.Hot != 0 {
		t.Fatalf("exact p99/p99.9 boundary failed: %+v", got)
	}
	if got.LatencyAnomalyCount != 1 || len(got.LatencyAnomalies) != 1 || got.LatencyAnomalies[0].Operation != LatencyHotSendACK {
		t.Fatalf("bounded anomaly = %+v", got)
	}

	breach := edge
	breach.Hot.Count += 100
	breach.Hot.AboveP99 += 2
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(2 * time.Minute), Latency: &breach}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Terminal || got.LatencyWarnings.Hot != 1 {
		t.Fatalf("short breach should be one warning: %+v", got)
	}
	recovered := breach
	recovered.Hot.Count += 10_000
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(3 * time.Minute), Latency: &recovered}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Terminal || got.LatencyWarnings.Hot != 1 {
		t.Fatalf("recovered short breach should remain warning-only: %+v", got)
	}
}

func TestLatencyWindowRequiresFiveFullMinutesOfBreach(t *testing.T) {
	start := time.Unix(70_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	warmupEnd := start.Add(thresholds.Timeline.Warmup)
	counters := LatencyCountersForThresholds(thresholds.Latency)
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd, Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	for minute := 1; minute <= 5; minute++ {
		counters.Hot.Count += 100
		counters.Hot.AboveP99 += 2
		if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(time.Duration(minute) * time.Minute), Latency: &counters}); err != nil {
			t.Fatal(err)
		}
		if got := evaluator.Snapshot(); got.Terminal {
			t.Fatalf("breach terminated before five full minutes at minute %d: %+v", minute, got)
		}
	}
	counters.Hot.Count += 100
	counters.Hot.AboveP99 += 2
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(6 * time.Minute), Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseHotLatency {
		t.Fatalf("sustained hot latency verdict = %+v", got)
	}
}

func TestLatencyWindowUsesColdAndSyncThresholdClasses(t *testing.T) {
	start := time.Unix(80_000, 0)
	for _, test := range []struct {
		name   string
		mutate func(*LatencyCounters)
		cause  VerdictCause
	}{
		{"cold", func(c *LatencyCounters) { c.Cold.Count += 100; c.Cold.AboveP99 += 2 }, VerdictCauseColdLatency},
		{"sync", func(c *LatencyCounters) { c.Sync.Count += 1_000; c.Sync.AboveP99 += 11; c.Sync.AboveP999 += 2 }, VerdictCauseSyncLatency},
	} {
		t.Run(test.name, func(t *testing.T) {
			thresholds := FormalConfig().Thresholds
			evaluator, err := NewVerdictEvaluator(start, thresholds)
			if err != nil {
				t.Fatal(err)
			}
			warmupEnd := start.Add(thresholds.Timeline.Warmup)
			counters := LatencyCountersForThresholds(thresholds.Latency)
			if err := evaluator.Observe(VerdictObservation{At: warmupEnd, Latency: &counters}); err != nil {
				t.Fatal(err)
			}
			for minute := 1; minute <= 6; minute++ {
				test.mutate(&counters)
				if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(time.Duration(minute) * time.Minute), Latency: &counters}); err != nil {
					t.Fatal(err)
				}
			}
			if got := evaluator.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != test.cause {
				t.Fatalf("latency verdict = %+v, want %q", got, test.cause)
			}
		})
	}
}

func TestLatencyAnomalyRetentionIsBoundedWithoutBecomingTerminal(t *testing.T) {
	start := time.Unix(85_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	warmupEnd := start.Add(thresholds.Timeline.Warmup)
	counters := LatencyCountersForThresholds(thresholds.Latency)
	if err := evaluator.Observe(VerdictObservation{At: warmupEnd, Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	for minute := 1; minute <= 40; minute++ {
		counters.Hot.Count += 2_000
		counters.Hot.AboveP99++
		counters.Hot.AboveP999++
		counters.Hot.Above10Seconds++
		if err := evaluator.Observe(VerdictObservation{At: warmupEnd.Add(time.Duration(minute) * time.Minute), Latency: &counters}); err != nil {
			t.Fatal(err)
		}
	}
	if got := evaluator.Snapshot(); got.Terminal || got.LatencyAnomalyCount != 40 || len(got.LatencyAnomalies) != maxVerdictLatencyAnomalies {
		t.Fatalf("bounded anomaly retention = %+v", got)
	}
}

func TestVerdictLateLatencyBaselineAndInvalidObservationsFreezeHarness(t *testing.T) {
	start := time.Unix(90_000, 0)
	thresholds := FormalConfig().Thresholds
	warmupEnd := start.Add(thresholds.Timeline.Warmup)

	late, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	baseline := LatencyCountersForThresholds(thresholds.Latency)
	baseline.Hot.Count, baseline.Hot.AboveP99 = 100, 100
	if err := late.Observe(VerdictObservation{At: warmupEnd.Add(time.Minute), Latency: &baseline}); err != nil {
		t.Fatal(err)
	}
	if got := late.Snapshot(); got.Terminal || got.LatencyWarnings.Hot != 0 {
		t.Fatalf("first late cumulative sample was evaluated instead of baselined: %+v", got)
	}
	regressed := baseline
	regressed.Hot.Count--
	if err := late.Observe(VerdictObservation{At: warmupEnd.Add(2 * time.Minute), Latency: &regressed}); err != nil {
		t.Fatal(err)
	}
	if got := late.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseCounterRegression {
		t.Fatalf("latency regression = %+v", got)
	}

	duplicate, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Observe(VerdictObservation{At: start}); err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Observe(VerdictObservation{At: start}); !errors.Is(err, ErrVerdictObservation) {
		t.Fatalf("duplicate-time error = %v", err)
	}
	if got := duplicate.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseInvalidObservation {
		t.Fatalf("duplicate time did not freeze harness verdict: %+v", got)
	}
	if err := duplicate.Observe(VerdictObservation{At: start.Add(-time.Second)}); !errors.Is(err, ErrVerdictObservation) {
		t.Fatalf("backward-time error = %v", err)
	}

	invalidSchema, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	invalid := LatencyCountersForThresholds(thresholds.Latency)
	invalid.Sync.Count, invalid.Sync.AboveP99, invalid.Sync.AboveP999 = 10, 1, 2
	if err := invalidSchema.Observe(VerdictObservation{At: start, Latency: &invalid}); err != nil {
		t.Fatal(err)
	}
	if got := invalidSchema.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseCounterRegression {
		t.Fatalf("invalid latency schema = %+v", got)
	}

	wrongLimits, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := LatencyCountersForThresholds(thresholds.Latency)
	mismatch.Cold.P999Limit += time.Nanosecond
	if err := wrongLimits.Observe(VerdictObservation{At: start, Latency: &mismatch}); err != nil {
		t.Fatal(err)
	}
	if got := wrongLimits.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseCounterRegression {
		t.Fatalf("latency threshold mismatch = %+v", got)
	}
}

func TestResourceSlopeEvaluatesEachNodeAndPassesExactFivePercent(t *testing.T) {
	start := time.Unix(100_000, 0)
	for _, test := range []struct {
		name       string
		finalHeap  float64
		wantCause  VerdictCause
		wantFailed bool
	}{
		{"exact five percent", 105, "", false},
		{"one node six percent", 106, VerdictCauseHeapGrowth, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			for hour := 0; hour <= 8; hour++ {
				heaps := [3]float64{100, 100, 100}
				if hour == 8 {
					heaps[1] = test.finalHeap
				}
				if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Duration(hour) * time.Hour), Resources: resourceSamples(heaps, [3]float64{100, 100, 100})}); err != nil {
					t.Fatal(err)
				}
			}
			got := evaluator.Snapshot()
			if test.wantFailed {
				if got.Outcome != VerdictProductFailure || got.Cause != test.wantCause {
					t.Fatalf("resource verdict = %+v, want %q", got, test.wantCause)
				}
			} else if got.Terminal {
				t.Fatalf("exact boundary failed: %+v", got)
			}
		})
	}
}

func TestResourceGoroutineRolling24HourSlopeIsPerNode(t *testing.T) {
	start := time.Unix(110_000, 0)
	for _, test := range []struct {
		name     string
		final    float64
		wantFail bool
	}{
		{"exact five percent", 105, false},
		{"one node six percent", 106, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			for hour := 0; hour <= 26; hour++ {
				goroutines := [3]float64{100, 100, 100}
				if hour == 26 {
					goroutines[2] = test.final
				}
				if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Duration(hour) * time.Hour), Resources: resourceSamples([3]float64{100, 100, 100}, goroutines)}); err != nil {
					t.Fatal(err)
				}
			}
			got := evaluator.Snapshot()
			if test.wantFail {
				if got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseGoroutineGrowth {
					t.Fatalf("goroutine verdict = %+v", got)
				}
			} else if got.Terminal {
				t.Fatalf("exact goroutine boundary failed: %+v", got)
			}
		})
	}
}

func TestResourceBurstMustExplicitlyReturnToWarmupBaseline(t *testing.T) {
	start := time.Unix(120_000, 0)
	for _, test := range []struct {
		name       string
		endedQueue float64
		failed     bool
	}{
		{"returns", 10, false},
		{"remains elevated", 11, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			for hour := 0; hour <= 2; hour++ {
				if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Duration(hour) * time.Hour), Resources: resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})}); err != nil {
					t.Fatal(err)
				}
			}
			burst := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
			burst[0].Burst = ResourceBurstActive
			burst[0].QueueDepth = 100
			burst[0].Inflight = 50
			if err := evaluator.Observe(VerdictObservation{At: start.Add(3 * time.Hour), Resources: burst}); err != nil {
				t.Fatal(err)
			}
			ended := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
			ended[0].Burst = ResourceBurstEnded
			ended[0].QueueDepth = test.endedQueue
			if err := evaluator.Observe(VerdictObservation{At: start.Add(4 * time.Hour), Resources: ended}); err != nil {
				t.Fatal(err)
			}
			got := evaluator.Snapshot()
			if test.failed {
				if got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseQueueRecovery {
					t.Fatalf("queue recovery verdict = %+v", got)
				}
			} else if got.Terminal {
				t.Fatalf("queue recovery at baseline failed: %+v", got)
			}
		})
	}
}

func TestResourceWarmupBurstDoesNotRaiseStableBaseline(t *testing.T) {
	start := time.Unix(125_000, 0)
	evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	stable := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
	if err := evaluator.Observe(VerdictObservation{At: start, Resources: stable}); err != nil {
		t.Fatal(err)
	}
	burst := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
	burst[0].Burst = ResourceBurstActive
	burst[0].QueueDepth = 100
	if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Hour), Resources: burst}); err != nil {
		t.Fatal(err)
	}
	ended := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
	ended[0].Burst = ResourceBurstEnded
	ended[0].QueueDepth = 11
	if err := evaluator.Observe(VerdictObservation{At: start.Add(2 * time.Hour), Resources: ended}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseQueueRecovery {
		t.Fatalf("warmup burst raised stable baseline: %+v", got)
	}
}

func TestResourceInvalidSamplesFreezeHarnessVerdict(t *testing.T) {
	start := time.Unix(130_000, 0)
	for _, test := range []struct {
		name   string
		at     time.Time
		mutate func([]NodeResourceSample)
	}{
		{"non-hour", start.Add(30 * time.Minute), func([]NodeResourceSample) {}},
		{"not forced GC", start, func(samples []NodeResourceSample) { samples[0].ForcedGC = false }},
		{"NaN heap", start, func(samples []NodeResourceSample) { samples[0].HeapBytes = math.NaN() }},
		{"infinite goroutines", start, func(samples []NodeResourceSample) { samples[1].Goroutines = math.Inf(1) }},
		{"negative queue", start, func(samples []NodeResourceSample) { samples[2].QueueDepth = -1 }},
		{"duplicate node", start, func(samples []NodeResourceSample) { samples[2].NodeID = samples[1].NodeID }},
		{"above uint64", start, func(samples []NodeResourceSample) { samples[0].Inflight = math.Exp2(64) }},
		{"fractional gauge", start, func(samples []NodeResourceSample) { samples[0].QueueDepth = 1.5 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			samples := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
			test.mutate(samples)
			if err := evaluator.Observe(VerdictObservation{At: test.at, Resources: samples}); !errors.Is(err, ErrVerdictObservation) {
				t.Fatalf("invalid resource error = %v", err)
			}
			if got := evaluator.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseInvalidObservation {
				t.Fatalf("invalid resource verdict = %+v", got)
			}
		})
	}
}

func TestResourceWindowsDeriveCapacityAndFinalizeRejectsActiveBurst(t *testing.T) {
	start := time.Unix(150_000, 0)
	thresholds := FormalConfig().Thresholds
	thresholds.Resource.ForcedGCLiveHeapWindow = 12 * time.Hour
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	for hour := 0; hour <= 14; hour++ {
		heaps := [3]float64{100, 100, 100}
		if hour == 14 {
			heaps[0] = 106
		}
		if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Duration(hour) * time.Hour), Resources: resourceSamples(heaps, [3]float64{100, 100, 100})}); err != nil {
			t.Fatalf("derived window at hour %d: %v", hour, err)
		}
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseHeapGrowth {
		t.Fatalf("derived resource window verdict = %+v", got)
	}

	active, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	for hour := 0; hour <= 2; hour++ {
		if err := active.Observe(VerdictObservation{At: start.Add(time.Duration(hour) * time.Hour), Resources: resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})}); err != nil {
			t.Fatal(err)
		}
	}
	burst := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
	burst[1].Burst = ResourceBurstActive
	if err := active.Observe(VerdictObservation{At: start.Add(3 * time.Hour), Resources: burst}); err != nil {
		t.Fatal(err)
	}
	if err := active.Finalize(start.Add(72 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := active.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseQueueRecovery {
		t.Fatalf("active burst finalized as %+v", got)
	}
}

func TestResourceQueueOnlySamplesMayUseSubhourCadence(t *testing.T) {
	start := time.Unix(140_000, 0)
	evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	samples := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
	if err := evaluator.Observe(VerdictObservation{At: start, Resources: samples}); err != nil {
		t.Fatal(err)
	}
	for index := range samples {
		samples[index].ForcedGC = false
		samples[index].HeapBytes = 0
		samples[index].Goroutines = 0
	}
	if err := evaluator.Observe(VerdictObservation{At: start.Add(30 * time.Second), Resources: samples}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Terminal {
		t.Fatalf("valid queue-only observation failed: %+v", got)
	}
}

func TestResourceBurstAfterWarmupRequiresEstablishedBaseline(t *testing.T) {
	start := time.Unix(145_000, 0)
	evaluator, err := NewVerdictEvaluator(start, FormalConfig().Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	samples := resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
	for index := range samples {
		samples[index].ForcedGC = false
		samples[index].HeapBytes = 0
		samples[index].Goroutines = 0
	}
	samples[0].Burst = ResourceBurstActive
	if err := evaluator.Observe(VerdictObservation{At: start.Add(3 * time.Hour), Resources: samples}); !errors.Is(err, ErrVerdictObservation) {
		t.Fatalf("missing-baseline burst error = %v", err)
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseInvalidObservation {
		t.Fatalf("missing-baseline burst verdict = %+v", got)
	}
}

func resourceSamples(heaps, goroutines [3]float64) []NodeResourceSample {
	return []NodeResourceSample{
		{NodeID: 1, ForcedGC: true, HeapBytes: heaps[0], Goroutines: goroutines[0], QueueDepth: 10, Inflight: 5},
		{NodeID: 2, ForcedGC: true, HeapBytes: heaps[1], Goroutines: goroutines[1], QueueDepth: 10, Inflight: 5},
		{NodeID: 3, ForcedGC: true, HeapBytes: heaps[2], Goroutines: goroutines[2], QueueDepth: 10, Inflight: 5},
	}
}

func TestVerdictEvaluatorRetainsFixedWindowsAcross72Hours(t *testing.T) {
	start := time.Unix(160_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	correctness := CorrectnessCounters{}
	latency := LatencyCountersForThresholds(thresholds.Latency)
	for elapsed := time.Duration(0); elapsed <= thresholds.Timeline.Final; elapsed += 5 * time.Second {
		correctness.FirstAttempts += 1_000
		latency.Hot.Count += 1_000
		latency.Cold.Count += 100
		latency.Sync.Count += 10
		observation := VerdictObservation{At: start.Add(elapsed), Correctness: &correctness, Latency: &latency}
		if elapsed%time.Hour == 0 {
			observation.Resources = resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
		}
		if err := evaluator.Observe(observation); err != nil {
			t.Fatalf("72h observation at %v: %v", elapsed, err)
		}
	}
	retention := evaluator.Snapshot().Retention
	if retention.MinuteSamples > retention.MinuteCapacity || retention.MinuteCapacity != 16 {
		t.Fatalf("minute retention = %+v", retention)
	}
	for index := range retention.LatencySamples {
		if retention.LatencySamples[index] > retention.LatencyCapacity {
			t.Fatalf("latency retention = %+v", retention)
		}
		if retention.HeapSamples[index] > retention.HeapCapacity || retention.GoroutineSamples[index] > retention.GoroutineCapacity {
			t.Fatalf("resource retention = %+v", retention)
		}
	}
	if err := evaluator.Finalize(start.Add(thresholds.Timeline.Final)); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictPass || !got.Terminal {
		t.Fatalf("72h final verdict = %+v", got)
	}
}
