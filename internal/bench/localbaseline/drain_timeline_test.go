package localbaseline

import (
	"testing"
	"time"
)

func TestEvaluateStepAcceptsEarlyDrainWithTerminalPreCloseProof(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		sampleAt *time.Duration
	}{
		{name: "fast convergence without periodic sample", duration: 4 * time.Second},
		{name: "short duration with one periodic sample", duration: 5 * time.Second, sampleAt: durationPointer(2 * time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			start := evidence.Timeline.Measured.EndedAt
			evidence.Timeline.Drain = PhaseEvidence{StartedAt: start, EndedAt: start.Add(test.duration)}
			if test.sampleAt != nil {
				sample := evidence.Timeline.Terminal
				sample.ObservedAt = start.Add(*test.sampleAt)
				sample.TerminalPreClose = false
				evidence.Timeline.Drain.Samples = []RuntimeSample{sample}
			}
			bindTerminalCutToDrain(&evidence, start.Add(time.Second), start.Add(2*time.Second), start.Add(3*time.Second))
			evidence.Timeline.Terminal.ObservedAt = evidence.Timeline.Drain.EndedAt.Add(time.Second)

			result := EvaluateStep(evidence)
			if !result.Clean || result.Outcome != OutcomeClean {
				t.Fatalf("result = %+v, want clean early-drain proof", result)
			}
		})
	}
}

func bindTerminalCutToDrain(evidence *StepEvidence, readyAt, observedAt, acknowledgedAt time.Time) {
	binding := evidence.Timeline.Terminal.TerminalCut
	binding.ReadyAt = readyAt
	binding.ObservedAt = observedAt
	binding.AcknowledgedAt = acknowledgedAt
	binding.DeadlineAt = evidence.Timeline.Drain.StartedAt.Add(time.Duration(evidence.ConfiguredDrainBudgetSeconds) * time.Second)
	evidence.ProductQueues.TerminalCut.ObservedAt = observedAt
	evidence.ProductQueues.TerminalCut.Phase = "run"
	evidence.ProductQueues.TerminalCut.ActivePhase = "cooldown"
	evidence.StorageOverlap.Samples[len(evidence.StorageOverlap.Samples)-1].ObservedAt = observedAt
}

func TestEvaluateStepEarlyDrainStillRequiresStrictTerminalProof(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StepEvidence)
	}{
		{name: "not pre-close", mutate: func(evidence *StepEvidence) { evidence.Timeline.Terminal.TerminalPreClose = false }},
		{name: "identity changed", mutate: func(evidence *StepEvidence) { evidence.Timeline.Terminal.Worker.StartToken = "replacement" }},
		{name: "traffic regressed", mutate: func(evidence *StepEvidence) { evidence.Timeline.Terminal.Traffic.SendACKs-- }},
		{name: "terminal gap unbounded", mutate: func(evidence *StepEvidence) {
			evidence.Timeline.Terminal.ObservedAt = evidence.Timeline.Drain.EndedAt.Add(31 * time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			start := evidence.Timeline.Measured.EndedAt
			evidence.Timeline.Drain = PhaseEvidence{StartedAt: start, EndedAt: start}
			evidence.Timeline.Terminal.ObservedAt = start.Add(time.Second)
			evidence.ProductQueues.TerminalCut.ObservedAt = start.Add(time.Second)
			evidence.StorageOverlap.Samples[len(evidence.StorageOverlap.Samples)-1].ObservedAt = start.Add(time.Second)
			test.mutate(&evidence)

			if result := EvaluateStep(evidence); result.Clean {
				t.Fatalf("result = %+v, want fail-closed terminal proof", result)
			}
		})
	}
}

func TestEvaluateStepLongDrainRequiresPeriodicCoverage(t *testing.T) {
	evidence := completeStepEvidence(1000)
	start := evidence.Timeline.Measured.EndedAt
	evidence.Timeline.Drain.StartedAt = start
	evidence.Timeline.Drain.EndedAt = start.Add(31 * time.Second)
	evidence.Timeline.Drain.Samples = nil
	evidence.Timeline.Terminal.ObservedAt = evidence.Timeline.Drain.EndedAt.Add(time.Second)
	evidence.ProductQueues.TerminalCut.ObservedAt = evidence.Timeline.Terminal.ObservedAt
	evidence.StorageOverlap.Samples[len(evidence.StorageOverlap.Samples)-1].ObservedAt = evidence.Timeline.Terminal.ObservedAt

	result := EvaluateStep(evidence)
	if result.Clean || !containsReason(result.Reasons, ReasonSampleGap) {
		t.Fatalf("result = %+v, want long-drain sample-gap failure", result)
	}
}

func durationPointer(value time.Duration) *time.Duration { return &value }
