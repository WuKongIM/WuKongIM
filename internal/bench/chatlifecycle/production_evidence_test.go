package chatlifecycle

import (
	"testing"
	"time"
)

func TestProjectWorkerVerdictEvidenceUsesExactMonotonicCountersAndThresholds(t *testing.T) {
	cfg := FormalConfig()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-evidence", Generation: 7}
	snapshots := coordinatorSnapshotFixture(fence, 1, time.Hour, 1)
	for index := range snapshots {
		snapshot := &snapshots[index]
		snapshot.Messages = WorkerMessageSnapshot{
			FirstAttempts: 10_000, FirstAttemptFailures: uint64(index), Terminal: uint64(index),
			TerminalReasons: TerminalSendSnapshot{RetryExhausted: RetryExhaustedSnapshot{
				Total: uint64(index), Unclassified: uint64(index),
			}},
			Losses: uint64(index), Duplicates: uint64(index + 1), Corruptions: uint64(index + 2),
			SequenceRegressions: uint64(index + 3),
		}
		snapshot.Harness.CommandSaturation = uint64(index)
		snapshot.HotSendackLatency = newWorkerHistogramSnapshot()
		recordWorkerLatency(&snapshot.HotSendackLatency, 100*time.Millisecond)
		recordWorkerLatency(&snapshot.HotSendackLatency, 2*time.Second)
		snapshot.ColdFirstCreateSendackLatency = newWorkerHistogramSnapshot()
		recordWorkerLatency(&snapshot.ColdFirstCreateSendackLatency, 3*time.Second)
		snapshot.LifecycleReheatSendackLatency = newWorkerHistogramSnapshot()
		recordWorkerLatency(&snapshot.LifecycleReheatSendackLatency, 4*time.Second)
		snapshot.Sync.Thresholds = LatencyThresholdCounters{
			P99Limit: time.Second, P999Limit: 3 * time.Second,
			Count: 2, AboveP99: 1, AboveP999: uint64(index % 2),
		}
	}
	lifecycle := LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}
	recordWorkerLatency(&lifecycle.ReheatLatency, time.Second)
	recordWorkerLatency(&lifecycle.ReheatLatency, 6*time.Second)

	correctness, latency, signals, err := projectWorkerVerdictEvidence(cfg, snapshots, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if correctness.FirstAttempts != 30_000 || correctness.FirstAttemptFailures != 3 || correctness.TerminalSends != 3 ||
		correctness.Losses != 3 || correctness.Duplicates != 6 || correctness.Corruptions != 9 ||
		correctness.SequenceRegressions != 12 || correctness.QueueSaturations != 3 {
		t.Fatalf("correctness projection = %+v", correctness)
	}
	if latency.Hot.Count != 6 || latency.Hot.AboveP99 != 3 || latency.Hot.AboveP999 != 3 ||
		latency.Cold.Count != 5 || latency.Cold.AboveP99 != 4 || latency.Cold.AboveP999 != 1 ||
		latency.Sync.Count != 6 || latency.Sync.AboveP99 != 3 || latency.Sync.AboveP999 != 1 {
		t.Fatalf("latency projection = %+v", latency)
	}
	if len(signals) != 0 {
		t.Fatalf("unexpected signals = %+v", signals)
	}
}

func TestProjectWorkerVerdictEvidenceRejectsHistogramInterpolationAndGenericFailures(t *testing.T) {
	cfg := FormalConfig()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-evidence-invalid", Generation: 8}
	snapshots := coordinatorSnapshotFixture(fence, 1, time.Hour, 1)
	for index := range snapshots {
		snapshots[index].Messages.FirstAttempts = 1
		snapshots[index].Sync.Thresholds = LatencyThresholdCounters{
			P99Limit: time.Second, P999Limit: 5 * time.Second, Count: 1,
		}
	}
	lifecycle := LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}
	if _, _, _, err := projectWorkerVerdictEvidence(cfg, snapshots, lifecycle); err == nil {
		t.Fatal("mismatched exact sync threshold was accepted")
	}

	for index := range snapshots {
		snapshots[index].Sync.Thresholds.P999Limit = 3 * time.Second
	}
	snapshots[0].Harness.Failures = 1
	snapshots[0].Harness.Classification = SyncClassificationHarnessInvalid
	_, _, signals, err := projectWorkerVerdictEvidence(cfg, snapshots, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0].Outcome != VerdictHarnessInvalid || signals[0].Cause != VerdictCauseWorkerHarness {
		t.Fatalf("generic harness signal = %+v", signals)
	}
}
