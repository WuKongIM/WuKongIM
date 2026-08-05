package chatlifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointRecorderKeepsFormalQualificationAndFinalOnOneFence(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-continuous-run"
	start := time.Unix(1_800_000_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-1", Generation: 9}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}

	qualificationSnapshots := coordinatorSnapshotFixture(fence, 101, 24*time.Hour, 1_000)
	qualification, err := recorder.Capture(
		start.Add(cfg.Thresholds.Timeline.Checkpoint),
		qualificationSnapshots,
		checkpointEvidenceFixture(false),
	)
	if err != nil {
		t.Fatal(err)
	}
	if qualification.Kind != CheckpointQualification || !qualification.Continue || qualification.Final {
		t.Fatalf("qualification = %+v", qualification)
	}
	if qualification.Window.Start != start || qualification.Window.End != start.Add(24*time.Hour) || qualification.Window.Elapsed != 24*time.Hour {
		t.Fatalf("qualification window = %+v", qualification.Window)
	}
	if len(qualification.Workers) != coordinatorWorkerCount {
		t.Fatalf("worker generations = %+v", qualification.Workers)
	}
	for workerID, worker := range qualification.Workers {
		if worker.WorkerIndex != uint64(workerID) || worker.Generation != fence.Generation || worker.SnapshotSequence != 101 || worker.Phase != WorkerPhaseRunning {
			t.Fatalf("qualification worker %d = %+v", workerID, worker)
		}
	}

	finalSnapshots := coordinatorSnapshotFixture(fence, 202, 72*time.Hour, 2_000)
	for index := range finalSnapshots {
		finalSnapshots[index].Phase = WorkerPhaseFinal
	}
	finalEvidence := checkpointEvidenceFixture(true)
	finalEvidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	final, err := recorder.Capture(start.Add(cfg.Thresholds.Timeline.Final), finalSnapshots, finalEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if final.Kind != CheckpointFinal || final.Continue || !final.Final {
		t.Fatalf("final = %+v", final)
	}
	if final.Fence.RunHash != qualification.Fence.RunHash || final.Fence.AssignmentHash != qualification.Fence.AssignmentHash {
		t.Fatalf("fence changed between checkpoints: qualification=%+v final=%+v", qualification.Fence, final.Fence)
	}
	if final.Workers[0].Generation != qualification.Workers[0].Generation || final.Workers[0].SnapshotSequence <= qualification.Workers[0].SnapshotSequence {
		t.Fatalf("worker generation did not continue: qualification=%+v final=%+v", qualification.Workers, final.Workers)
	}
	if _, err := recorder.Capture(start.Add(73*time.Hour), finalSnapshots, finalEvidence); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("duplicate final error = %v, want %v", err, ErrCheckpointSequence)
	}
}

func TestCheckpointRecorderRejectsEarlyDuplicateAndGenerationChanges(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-sequence"
	start := time.Unix(1_800_100_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-2", Generation: 11}

	t.Run("early", func(t *testing.T) {
		recorder, err := NewCheckpointRecorder(cfg, fence, start)
		if err != nil {
			t.Fatal(err)
		}
		_, err = recorder.Capture(start.Add(cfg.Thresholds.Timeline.Checkpoint-time.Nanosecond), coordinatorSnapshotFixture(fence, 1, time.Hour, 1), checkpointEvidenceFixture(false))
		if !errors.Is(err, ErrCheckpointSequence) {
			t.Fatalf("early error = %v", err)
		}
	})

	t.Run("duplicate qualification", func(t *testing.T) {
		recorder, err := NewCheckpointRecorder(cfg, fence, start)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recorder.Capture(start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
			t.Fatal(err)
		}
		if _, err := recorder.Capture(start.Add(25*time.Hour), coordinatorSnapshotFixture(fence, 2, 25*time.Hour, 2), checkpointEvidenceFixture(false)); !errors.Is(err, ErrCheckpointSequence) {
			t.Fatalf("duplicate qualification error = %v", err)
		}
	})

	t.Run("changed generation", func(t *testing.T) {
		recorder, err := NewCheckpointRecorder(cfg, fence, start)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recorder.Capture(start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
			t.Fatal(err)
		}
		changed := fence
		changed.Generation++
		_, err = recorder.Capture(start.Add(72*time.Hour), coordinatorSnapshotFixture(changed, 2, 72*time.Hour, 2), checkpointEvidenceFixture(true))
		if err == nil {
			t.Fatal("changed generation was accepted")
		}
	})
}

func TestCheckpointTerminalQualificationStopsContinuation(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-terminal"
	start := time.Unix(1_800_200_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-3", Generation: 1}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictProductFailure, Cause: VerdictCauseMessageLoss}
	report, err := recorder.Capture(start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.Continue || !report.Final || report.Verdict.Outcome != VerdictProductFailure {
		t.Fatalf("terminal qualification = %+v", report)
	}
	if _, err := recorder.Capture(start.Add(72*time.Hour), coordinatorSnapshotFixture(fence, 2, 72*time.Hour, 2), evidence); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("post-terminal capture error = %v", err)
	}
}

func TestCheckpointFinalPassRequiresQualificationAndContinuousWorkerUptime(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-continuity"
	start := time.Unix(1_800_300_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-4", Generation: 2}
	passing := checkpointEvidenceFixture(true)
	passing.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	finalSnapshots := coordinatorSnapshotFixture(fence, 2, 72*time.Hour, 2)
	for index := range finalSnapshots {
		finalSnapshots[index].Phase = WorkerPhaseFinal
	}

	missingQualification, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingQualification.Capture(start.Add(72*time.Hour), finalSnapshots, passing); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("final pass without qualification error = %v", err)
	}

	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Capture(start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
		t.Fatal(err)
	}
	restarted := coordinatorSnapshotFixture(fence, 2, time.Hour, 2)
	for index := range restarted {
		restarted[index].Phase = WorkerPhaseFinal
	}
	if _, err := recorder.Capture(start.Add(72*time.Hour), restarted, passing); !errors.Is(err, ErrCheckpointEvidence) {
		t.Fatalf("restarted worker error = %v, want %v", err, ErrCheckpointEvidence)
	}
	if _, err := recorder.Capture(start.Add(72*time.Hour), finalSnapshots, passing); err != nil {
		t.Fatalf("valid retry after rejected uptime: %v", err)
	}
}

func TestCheckpointTerminalAfterQualificationFinalizesImmediately(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-immediate-terminal"
	start := time.Unix(1_800_400_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-5", Generation: 4}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Capture(start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictProductFailure, Cause: VerdictCauseServerCrash}
	report, err := recorder.Capture(start.Add(25*time.Hour), coordinatorSnapshotFixture(fence, 2, 25*time.Hour, 2), evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != CheckpointFinal || !report.Final || report.Continue || report.Verdict.Cause != VerdictCauseServerCrash {
		t.Fatalf("immediate terminal report = %+v", report)
	}
}

func TestCheckpointCaptureAndWriteCommitsOnlyAfterBothAtomicOutputs(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-atomic-output"
	start := time.Unix(1_800_500_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-6", Generation: 6}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	markdownDirectory := filepath.Join(directory, "markdown")
	outputs := CheckpointOutputPaths{
		JSON: filepath.Join(directory, "qualification.json"), Markdown: filepath.Join(markdownDirectory, "qualification.md"),
	}
	snapshots := coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1)
	evidence := checkpointEvidenceFixture(false)
	if _, err := recorder.CaptureAndWrite(start.Add(24*time.Hour), snapshots, evidence, outputs); !errors.Is(err, ErrCheckpointOutput) {
		t.Fatalf("first persistence error = %v, want %v", err, ErrCheckpointOutput)
	}
	if recorder.qualificationCaptured {
		t.Fatal("failed two-format persistence committed qualification state")
	}
	if err := os.Mkdir(markdownDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := recorder.CaptureAndWrite(start.Add(24*time.Hour), snapshots, evidence, outputs)
	if err != nil {
		t.Fatalf("retry identical snapshot cut: %v", err)
	}
	if !recorder.qualificationCaptured || !report.Continue {
		t.Fatalf("committed qualification = %+v", report)
	}
	for _, path := range []string{outputs.JSON, outputs.Markdown} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 {
			t.Fatalf("empty persisted report %s", path)
		}
	}
	if _, err := recorder.CaptureAndWrite(start.Add(24*time.Hour), snapshots, evidence, outputs); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("duplicate persisted qualification error = %v", err)
	}
}

func checkpointEvidenceFixture(final bool) CheckpointEvidence {
	capacity := ReportCapacityEvidence{}
	if final {
		capacity = ReportCapacityEvidence{Attempted: true, Completed: true, MaximumPassingRate: 2_000, RecoveryPassed: true}
	}
	return CheckpointEvidence{
		TopologyValidated: true,
		Lifecycle: LifecycleProofSnapshot{
			Candidates: 1_200, Loaded: 1_200, ColdEligible: 1_200, Reheated: 1_200, Completed: 1_200,
			ReheatLatency: newWorkerHistogramSnapshot(),
		},
		MetaCreate: MetaCreateAccountingSnapshot{ExpectedUnique: 3_000_020, Created: 3_000_020, Checkpoints: 2},
		Resources: ReportResourceEvidence{Nodes: [3]ReportResourceNodeEvidence{
			{DataFilesystemBytes: 1_000_000_000_000, DataFilesystemAvailableBytes: 900_000_000_000, ForcedGCSamples: 25, HeapStartBytes: 100, HeapEndBytes: 102, GoroutineStart: 1_000, GoroutineEnd: 1_010},
			{DataFilesystemBytes: 1_000_000_000_000, DataFilesystemAvailableBytes: 900_000_000_000, ForcedGCSamples: 25, HeapStartBytes: 110, HeapEndBytes: 112, GoroutineStart: 1_100, GoroutineEnd: 1_110},
			{DataFilesystemBytes: 1_000_000_000_000, DataFilesystemAvailableBytes: 900_000_000_000, ForcedGCSamples: 25, HeapStartBytes: 120, HeapEndBytes: 122, GoroutineStart: 1_200, GoroutineEnd: 1_210},
		}},
		Cluster: ReportClusterEvidence{
			HealthySamples: 1_000, LogicalSlotGroups: 12, LeaderGroups: 12, FullReplicaGroups: 12,
		},
		Capacity: capacity,
		Warnings: []ReportWarningCode{ReportWarningShortLatencyBreach},
		Samples:  []ReportSample{{Class: ReportSampleLifecycle, Index: 7, Hash: hashReportValue("sample-7")}},
	}
}
