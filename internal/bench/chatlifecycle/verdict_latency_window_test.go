package chatlifecycle

import (
	"testing"
	"time"
)

func TestFormalLatencyEvaluationStartsBeforeResourceWarmup(t *testing.T) {
	start := time.Unix(5_000_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	counters := LatencyCountersForThresholds(thresholds.Latency)
	if err := evaluator.Observe(VerdictObservation{At: start.Add(10 * time.Minute), Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	for minute := 11; minute <= 16; minute++ {
		counters.Hot.Count += 100
		counters.Hot.AboveP99 += 2
		if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Duration(minute) * time.Minute), Latency: &counters}); err != nil {
			t.Fatal(err)
		}
	}
	if got := evaluator.Snapshot(); got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseHotLatency {
		t.Fatalf("post-latency-warmup sustained breach was skipped by resource warmup: %+v", got)
	}
	if thresholds.Timeline.Warmup != 2*time.Hour {
		t.Fatalf("resource warmup = %s", thresholds.Timeline.Warmup)
	}
}

func TestLatencyWindowEvidenceKeepsActualLateBaselineAndRollingSamples(t *testing.T) {
	start := time.Unix(5_100_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	counters := LatencyCountersForThresholds(thresholds.Latency)
	counters.Hot.Count, counters.Cold.Count, counters.Sync.Count = 10_000, 10_000, 10_000
	observe := func(at time.Duration) {
		t.Helper()
		if err := evaluator.Observe(VerdictObservation{At: start.Add(at), Latency: &counters}); err != nil {
			t.Fatal(err)
		}
	}
	observe(9 * time.Minute)
	counters.Hot.Count, counters.Cold.Count, counters.Sync.Count = 20_000, 20_000, 20_000
	observe(11 * time.Minute)
	evidence := evaluator.LatencyWindowEvidence()
	if !evidence.WarmupEnd.Equal(start.Add(10*time.Minute)) || !evidence.BaselineAt.Equal(start.Add(11*time.Minute)) || !evidence.LastAt.Equal(evidence.BaselineAt) || evidence.Duration != 0 || evidence.Samples != [3]uint64{} || evidence.Qualified != [3]bool{} {
		t.Fatalf("crossing cumulative cut became measured evidence: %+v", evidence)
	}
	counters.Hot.Count += 1_000
	counters.Cold.Count += 1_000
	counters.Sync.Count += 999
	observe(12 * time.Minute)
	if got := evaluator.LatencyWindowEvidence(); got.Qualified != [3]bool{} || got.Duration != time.Minute {
		t.Fatalf("short evidence qualified: %+v", got)
	}
	if err := evaluator.Observe(VerdictObservation{At: start.Add(20 * time.Minute), Resources: queueOnlyResourceSamples()}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.LatencyWindowEvidence(); got.Duration != time.Minute || !got.LastAt.Equal(start.Add(12*time.Minute)) {
		t.Fatalf("resource cut extended latency evidence: %+v", got)
	}
	observe(21 * time.Minute)
	evidence = evaluator.LatencyWindowEvidence()
	if evidence.Duration != 10*time.Minute || evidence.RequiredDuration != 10*time.Minute || !evidence.WindowStart.Equal(start.Add(11*time.Minute)) || !evidence.WindowEnd.Equal(start.Add(21*time.Minute)) || evidence.MaxAge != 5*time.Second || evidence.MinimumSamples != 1_000 || evidence.Samples != [3]uint64{1_000, 1_000, 999} || evidence.Qualified != [3]bool{true, true, false} {
		t.Fatalf("actual qualifying interval / exact sample bound = %+v", evidence)
	}
	observe(22 * time.Minute)
	if got := evaluator.LatencyWindowEvidence(); got.Samples != [3]uint64{} || got.Qualified != [3]bool{} {
		t.Fatalf("expired traffic manufactured a current sufficient window: %+v", got)
	}
}

func TestLatencyWindowEvidenceRejectsCounterAndClockDamage(t *testing.T) {
	for _, test := range []struct {
		name   string
		at     time.Duration
		mutate func(*LatencyCounters)
	}{
		{"duplicate timestamp", 20 * time.Minute, func(*LatencyCounters) {}},
		{"backwards timestamp", 19 * time.Minute, func(*LatencyCounters) {}},
		{"counter regression", 21 * time.Minute, func(c *LatencyCounters) { c.Hot.Count-- }},
		{"impossible delta", 21 * time.Minute, func(c *LatencyCounters) { c.Hot.AboveP99++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := time.Unix(5_200_000, 0)
			thresholds := FormalConfig().Thresholds
			evaluator, err := NewVerdictEvaluator(start, thresholds)
			if err != nil {
				t.Fatal(err)
			}
			counters := LatencyCountersForThresholds(thresholds.Latency)
			counters.Hot.Count, counters.Cold.Count, counters.Sync.Count = 10_000, 10_000, 10_000
			counters.Hot.AboveP99 = 10
			if err := evaluator.Observe(VerdictObservation{At: start.Add(10 * time.Minute), Latency: &counters}); err != nil {
				t.Fatal(err)
			}
			counters.Hot.Count += 1_000
			counters.Cold.Count += 1_000
			counters.Sync.Count += 1_000
			if err := evaluator.Observe(VerdictObservation{At: start.Add(20 * time.Minute), Latency: &counters}); err != nil {
				t.Fatal(err)
			}
			if got := evaluator.LatencyWindowEvidence(); got.Qualified != [3]bool{true, true, true} {
				t.Fatalf("precondition not qualified: %+v", got)
			}
			test.mutate(&counters)
			_ = evaluator.Observe(VerdictObservation{At: start.Add(test.at), Latency: &counters})
			if got := evaluator.Snapshot(); got.Outcome != VerdictHarnessInvalid {
				t.Fatalf("damaged cut not rejected: %+v", got)
			}
			if got := evaluator.LatencyWindowEvidence(); got.Qualified != [3]bool{} {
				t.Fatalf("damaged cut retained pass qualification: %+v", got)
			}
		})
	}
}

func TestVerdictFinalizeRequiresLatencyWindowQualification(t *testing.T) {
	start := time.Unix(5_300_000, 0)
	thresholds := FormalConfig().Thresholds
	for _, test := range []struct {
		name         string
		finalSamples uint64
		want         VerdictOutcome
		stale        bool
	}{
		{"qualified", 1_000, VerdictPass, false}, {"insufficient sample resolution", 999, VerdictHarnessInvalid, false}, {"stale latency evidence", 1_000, VerdictHarnessInvalid, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator, err := NewVerdictEvaluator(start, thresholds)
			if err != nil {
				t.Fatal(err)
			}
			correctness := CorrectnessCounters{}
			latency := LatencyCountersForThresholds(thresholds.Latency)
			for hour := 0; hour < 72; hour++ {
				correctness.FirstAttempts += 1_000
				latency.Hot.Count += 1_000
				latency.Cold.Count += 1_000
				latency.Sync.Count += 1_000
				if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Duration(hour) * time.Hour), Correctness: &correctness, Latency: &latency, Resources: resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})}); err != nil {
					t.Fatal(err)
				}
			}
			end := thresholds.Timeline.Final
			if test.stale {
				end -= time.Minute
			}
			baseline := end - 10*time.Minute
			// A late cumulative cut cannot place all intervening hourly traffic inside the last ten minutes.
			if err := evaluator.Observe(VerdictObservation{At: start.Add(baseline), Latency: &latency}); err != nil {
				t.Fatal(err)
			}
			for cut := uint64(1); cut <= 120; cut++ {
				delta := test.finalSamples*cut/120 - test.finalSamples*(cut-1)/120
				latency.Hot.Count += delta
				latency.Cold.Count += delta
				latency.Sync.Count += delta
				observation := VerdictObservation{At: start.Add(baseline + time.Duration(cut)*5*time.Second), Latency: &latency}
				if !test.stale && cut == 120 {
					observation.Resources = resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})
				}
				if err := evaluator.Observe(observation); err != nil {
					t.Fatal(err)
				}
			}
			if test.stale {
				if err := evaluator.Observe(VerdictObservation{At: start.Add(thresholds.Timeline.Final), Resources: resourceSamples([3]float64{100, 100, 100}, [3]float64{100, 100, 100})}); err != nil {
					t.Fatal(err)
				}
			}
			_ = evaluator.Finalize(start.Add(thresholds.Timeline.Final))
			if got := evaluator.Snapshot(); got.Outcome != test.want || !got.Terminal {
				t.Fatalf("final qualification verdict = %+v, want %s", got, test.want)
			}
		})
	}
}

func TestLatencyWindowEvidenceRestartsAfterAnUnsplitLongGap(t *testing.T) {
	start := time.Unix(5_350_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	counters := LatencyCountersForThresholds(thresholds.Latency)
	if err := evaluator.Observe(VerdictObservation{At: start.Add(10 * time.Minute), Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	counters.Hot.Count, counters.Cold.Count, counters.Sync.Count = 10_000, 10_000, 10_000
	if err := evaluator.Observe(VerdictObservation{At: start.Add(time.Hour), Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	got := evaluator.LatencyWindowEvidence()
	if got.Samples != [3]uint64{} || got.Qualified != [3]bool{} || got.Duration != 0 || !got.WindowStart.Equal(start.Add(time.Hour)) || !got.BaselineAt.Equal(start.Add(10*time.Minute)) {
		t.Fatalf("one hour cumulative delta fabricated a ten-minute window: %+v", got)
	}
}

func TestLatencyWindowEvidenceCannotStayFreshThroughResourceOnlyCuts(t *testing.T) {
	start := time.Unix(5_400_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	counters := LatencyCountersForThresholds(thresholds.Latency)
	if err := evaluator.Observe(VerdictObservation{At: start.Add(10 * time.Minute), Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	counters.Hot.Count, counters.Cold.Count, counters.Sync.Count = 1_000, 1_000, 1_000
	if err := evaluator.Observe(VerdictObservation{At: start.Add(20 * time.Minute), Latency: &counters}); err != nil {
		t.Fatal(err)
	}
	if err := evaluator.Observe(VerdictObservation{At: start.Add(20*time.Minute + 5*time.Second), Resources: queueOnlyResourceSamples()}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.LatencyWindowEvidence(); got.Qualified != [3]bool{true, true, true} {
		t.Fatalf("exact MaxAge cut rejected: %+v", got)
	}
	if err := evaluator.Observe(VerdictObservation{At: start.Add(20*time.Minute + 5*time.Second + time.Nanosecond), Resources: queueOnlyResourceSamples()}); err != nil {
		t.Fatal(err)
	}
	if got := evaluator.LatencyWindowEvidence(); got.Qualified != [3]bool{} || !got.LastAt.Equal(start.Add(20*time.Minute)) || got.Duration != 10*time.Minute {
		t.Fatalf("resource-only observations manufactured freshness: %+v", got)
	}
}
