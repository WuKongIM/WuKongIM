package localbaseline

import (
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestEvaluateStepRequiresBoundExternalTerminalCut(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StepEvidence)
	}{
		{name: "missing binding", mutate: func(e *StepEvidence) { e.Timeline.Terminal.TerminalCut = nil }},
		{name: "not required", mutate: func(e *StepEvidence) { e.Timeline.Terminal.TerminalCutRequired = false }},
		{name: "not ready", mutate: func(e *StepEvidence) { e.Timeline.Terminal.TerminalCutReady = false }},
		{name: "wrong generation", mutate: func(e *StepEvidence) { e.Timeline.Terminal.TerminalCut.AssignmentID = "replacement" }},
		{name: "product digest tampered", mutate: func(e *StepEvidence) { e.Timeline.Terminal.TerminalCut.ProductMetricsSHA256 = strings.Repeat("f", 64) }},
		{name: "storage digest tampered", mutate: func(e *StepEvidence) { e.Timeline.Terminal.TerminalCut.StorageOverlapSHA256 = strings.Repeat("f", 64) }},
		{name: "ready after observation", mutate: func(e *StepEvidence) {
			e.Timeline.Terminal.TerminalCut.ReadyAt = e.Timeline.Terminal.TerminalCut.ObservedAt.Add(time.Nanosecond)
		}},
		{name: "ack after drain", mutate: func(e *StepEvidence) {
			e.Timeline.Terminal.TerminalCut.AcknowledgedAt = e.Timeline.Drain.EndedAt.Add(2 * time.Second)
		}},
		{name: "ack after deadline", mutate: func(e *StepEvidence) {
			e.Timeline.Terminal.TerminalCut.AcknowledgedAt = e.Timeline.Terminal.TerminalCut.DeadlineAt.Add(time.Nanosecond)
		}},
		{name: "deadline exceeds budget", mutate: func(e *StepEvidence) {
			e.Timeline.Terminal.TerminalCut.DeadlineAt = e.Timeline.Drain.StartedAt.Add(92 * time.Second)
		}},
		{name: "raw cut lies about stopped", mutate: func(e *StepEvidence) {
			e.ProductQueues.TerminalCut.Phase, e.ProductQueues.TerminalCut.ActivePhase = "stopped", ""
		}},
		{name: "raw cut timestamp changed", mutate: func(e *StepEvidence) {
			e.ProductQueues.TerminalCut.ObservedAt = e.ProductQueues.TerminalCut.ObservedAt.Add(time.Nanosecond)
		}},
		{name: "storage cut timestamp changed", mutate: func(e *StepEvidence) {
			e.StorageOverlap.Samples[len(e.StorageOverlap.Samples)-1].ObservedAt = e.StorageOverlap.Samples[len(e.StorageOverlap.Samples)-1].ObservedAt.Add(time.Nanosecond)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			tt.mutate(&evidence)

			result := EvaluateStep(evidence)
			if result.Clean || !containsReason(result.Reasons, ReasonTerminalCutBinding) {
				t.Fatalf("result = %+v, want terminal-cut binding failure", result)
			}
		})
	}
}

func TestEvaluateStepAcceptsEarlyTerminalCutBeforeCooldownDeadline(t *testing.T) {
	evidence := completeStepEvidence(1000)
	binding := evidence.Timeline.Terminal.TerminalCut
	if !binding.DeadlineAt.After(evidence.Timeline.Drain.EndedAt) {
		t.Fatalf("fixture must prove early convergence: binding=%+v drain_end=%s", binding, evidence.Timeline.Drain.EndedAt)
	}

	result := EvaluateStep(evidence)
	if !result.Clean {
		t.Fatalf("result = %+v, want early terminal cut to remain clean", result)
	}
}

func TestEvaluateStepRequiresReceiveDrainForEveryRequiredConnection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.ReceiveDrainSnapshot)
	}{
		{name: "not required", mutate: func(s *model.ReceiveDrainSnapshot) { *s = model.ReceiveDrainNotRequired() }},
		{name: "client coverage short", mutate: func(s *model.ReceiveDrainSnapshot) { s.ClientCount-- }},
		{name: "active drain coverage short", mutate: func(s *model.ReceiveDrainSnapshot) { s.ActiveDrains-- }},
		{name: "queue source coverage short", mutate: func(s *model.ReceiveDrainSnapshot) { s.QueueSnapshotClients-- }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			tt.mutate(&evidence.Timeline.Terminal.ReceiveDrain)

			result := EvaluateStep(evidence)
			if result.Clean || !containsReason(result.Reasons, ReasonReceiveDrain) {
				t.Fatalf("result = %+v, want receive-drain evidence failure", result)
			}
		})
	}
}
