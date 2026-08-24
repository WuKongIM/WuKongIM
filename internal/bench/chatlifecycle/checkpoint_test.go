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
	qualification, err := captureCheckpoint(t, recorder,
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
	if qualification.Latency.SendPendingToWrite.Count != 3_003 || qualification.Latency.SendWriteToACK.Count != 3_003 {
		t.Fatalf("client SEND phase latency = pending-to-write:%+v write-to-ACK:%+v",
			qualification.Latency.SendPendingToWrite, qualification.Latency.SendWriteToACK)
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
	final, err := captureCheckpoint(t, recorder, start.Add(cfg.Thresholds.Timeline.Final), finalSnapshots, finalEvidence)
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
	if _, err := captureCheckpoint(t, recorder, start.Add(73*time.Hour), finalSnapshots, finalEvidence); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("duplicate final error = %v, want %v", err, ErrCheckpointSequence)
	}
}

func TestCheckpointRecorderKeepsDatasetDigestAcrossContinuousCuts(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-dataset-continuity"
	start := time.Unix(1_800_050_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "checkpoint-dataset-assignment", Generation: 1}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
		t.Fatal(err)
	}
	final := checkpointEvidenceFixture(false)
	final.DatasetDigest = hashReportValue("different-live-dataset")
	final.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	snapshots := coordinatorSnapshotFixture(fence, 2, 72*time.Hour, 2)
	for index := range snapshots {
		snapshots[index].Phase = WorkerPhaseFinal
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(72*time.Hour), snapshots, final); !errors.Is(err, ErrCheckpointEvidence) {
		t.Fatalf("changed dataset digest error = %v", err)
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
		_, err = captureCheckpoint(t, recorder, start.Add(cfg.Thresholds.Timeline.Checkpoint-time.Nanosecond), coordinatorSnapshotFixture(fence, 1, time.Hour, 1), checkpointEvidenceFixture(false))
		if !errors.Is(err, ErrCheckpointSequence) {
			t.Fatalf("early error = %v", err)
		}
	})

	t.Run("duplicate qualification", func(t *testing.T) {
		recorder, err := NewCheckpointRecorder(cfg, fence, start)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
			t.Fatal(err)
		}
		if _, err := captureCheckpoint(t, recorder, start.Add(25*time.Hour), coordinatorSnapshotFixture(fence, 2, 25*time.Hour, 2), checkpointEvidenceFixture(false)); !errors.Is(err, ErrCheckpointSequence) {
			t.Fatalf("duplicate qualification error = %v", err)
		}
	})

	t.Run("changed generation", func(t *testing.T) {
		recorder, err := NewCheckpointRecorder(cfg, fence, start)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
			t.Fatal(err)
		}
		changed := fence
		changed.Generation++
		_, err = captureCheckpoint(t, recorder, start.Add(72*time.Hour), coordinatorSnapshotFixture(changed, 2, 72*time.Hour, 2), checkpointEvidenceFixture(true))
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
	report, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.Continue || !report.Final || report.Verdict.Outcome != VerdictProductFailure {
		t.Fatalf("terminal qualification = %+v", report)
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(72*time.Hour), coordinatorSnapshotFixture(fence, 2, 72*time.Hour, 2), evidence); !errors.Is(err, ErrCheckpointSequence) {
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
	if _, err := captureCheckpoint(t, missingQualification, start.Add(72*time.Hour), finalSnapshots, passing); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("final pass without qualification error = %v", err)
	}

	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
		t.Fatal(err)
	}
	earlyPassSnapshots := coordinatorSnapshotFixture(fence, 2, 25*time.Hour, 2)
	for index := range earlyPassSnapshots {
		earlyPassSnapshots[index].Phase = WorkerPhaseFinal
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(25*time.Hour), earlyPassSnapshots, passing); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("early final pass error = %v, want %v", err, ErrCheckpointSequence)
	}
	restarted := coordinatorSnapshotFixture(fence, 2, time.Hour, 2)
	for index := range restarted {
		restarted[index].Phase = WorkerPhaseFinal
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(72*time.Hour), restarted, passing); !errors.Is(err, ErrCheckpointEvidence) {
		t.Fatalf("restarted worker error = %v, want %v", err, ErrCheckpointEvidence)
	}
	if _, err := captureCheckpoint(t, recorder, start.Add(72*time.Hour), finalSnapshots, passing); err != nil {
		t.Fatalf("valid retry after rejected uptime: %v", err)
	}
}

func TestCheckpointCapacityPassCanCloseBeforeSoakTimelineFinal(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-capacity-final"
	cfg.Mode = ModeCapacity
	cfg.Capacity.AgedCheckpoint = AgedCheckpoint{
		Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour,
	}
	start := time.Unix(1_800_350_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "capacity-assignment", Generation: 3}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(true)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	snapshots := coordinatorSnapshotFixture(fence, 1, 2*time.Hour, 10)
	for index := range snapshots {
		snapshots[index].Phase = WorkerPhaseFinal
	}
	report, err := captureCheckpoint(t, recorder, start.Add(2*time.Hour), snapshots, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ModeCapacity || report.Kind != CheckpointFinal || !report.Final || report.Continue ||
		report.Window.Elapsed != 2*time.Hour || report.Window.End.Equal(report.Window.FinalAt) {
		t.Fatalf("capacity final report = %+v", report)
	}
}

func TestCheckpointRehearsalPassIsDistinctAndEndsAtTwoHours(t *testing.T) {
	cfg := RehearsalConfig()
	cfg.RunID = "checkpoint-rehearsal-final"
	start := time.Unix(1_800_375_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "rehearsal-assignment", Generation: 4}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictRehearsalPass, Cause: VerdictCauseRehearsalCompleted}
	snapshots := coordinatorSnapshotFixture(fence, 1, 2*time.Hour, 10)
	for index := range snapshots {
		snapshots[index].Phase = WorkerPhaseFinal
	}
	report, err := captureCheckpoint(t, recorder, start.Add(2*time.Hour), snapshots, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.Stage != StageRehearsal || report.Verdict.Outcome != VerdictRehearsalPass ||
		report.Window.FinalAt != start.Add(2*time.Hour) || report.Window.Elapsed != 2*time.Hour ||
		len(report.Warnings) != 2 || report.Warnings[1] != ReportWarningRehearsalLongWindowsIncomplete {
		t.Fatalf("rehearsal report = %+v", report)
	}

	early, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureCheckpoint(t, early, start.Add(2*time.Hour-time.Nanosecond), snapshots, evidence); !errors.Is(err, ErrCheckpointSequence) {
		t.Fatalf("early rehearsal pass error = %v, want %v", err, ErrCheckpointSequence)
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
	if _, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictProductFailure, Cause: VerdictCauseServerCrash}
	report, err := captureCheckpoint(t, recorder, start.Add(25*time.Hour), coordinatorSnapshotFixture(fence, 2, 25*time.Hour, 2), evidence)
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

func TestCheckpointRecorderStripsMonotonicClockFromPersistedWindow(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-persisted-wall-clock"
	start := time.Now()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-wall-clock", Generation: 7}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictProductFailure, Cause: VerdictCauseMessageLoss}
	directory := t.TempDir()
	jsonPath := filepath.Join(directory, "final.json")
	report, err := recorder.CaptureAndWrite(
		start.Add(time.Second), coordinatorSnapshotFixture(fence, 1, 2*time.Second, 1), evidence,
		CheckpointOutputPaths{JSON: jsonPath, Markdown: filepath.Join(directory, "final.md")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Window.Start != report.Window.Start.Round(0) || report.Window.End != report.Window.End.Round(0) {
		t.Fatalf("persisted report retained process-local monotonic clock: %+v", report.Window)
	}
	if _, err := ReadReport(jsonPath); err != nil {
		t.Fatalf("persisted report did not round-trip: %v", err)
	}
}

func TestCheckpointRecorderUsesProcessElapsedAcrossWallClockSlew(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "checkpoint-wall-clock-slew"
	start := time.Unix(1_800_600_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-wall-clock-slew", Generation: 8}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a 200us backwards wall-clock slew while the process monotonic
	// clock still reaches the exact qualification boundary.
	recorder.processStart = recorder.processStart.Add(-200 * time.Microsecond)
	wallCut := start.Add(cfg.Thresholds.Timeline.Checkpoint - 200*time.Microsecond)
	snapshots := coordinatorSnapshotFixture(fence, 1, cfg.Thresholds.Timeline.Checkpoint, 1)
	report, err := captureCheckpoint(t, recorder, wallCut, snapshots, checkpointEvidenceFixture(false))
	if err != nil {
		t.Fatalf("qualification after monotonic deadline: %v", err)
	}
	if !report.Window.End.Equal(wallCut) || report.Window.Elapsed != cfg.Thresholds.Timeline.Checkpoint {
		t.Fatalf("persisted window = %+v, want wall end %v and process elapsed %v", report.Window, wallCut, cfg.Thresholds.Timeline.Checkpoint)
	}
}

func TestCheckpointRecorderUsesProcessElapsedForSuccessfulFinalAcrossWallClockSlew(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "checkpoint-final-wall-clock-slew"
	start := time.Unix(1_800_700_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment-final-wall-clock-slew", Generation: 9}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureCheckpoint(
		t, recorder, start.Add(cfg.Thresholds.Timeline.Checkpoint),
		coordinatorSnapshotFixture(fence, 1, cfg.Thresholds.Timeline.Checkpoint, 1),
		checkpointEvidenceFixture(false),
	); err != nil {
		t.Fatalf("qualification: %v", err)
	}

	const backwardsSlew = 200 * time.Microsecond
	recorder.processStart = recorder.processStart.Add(-backwardsSlew)
	wallCut := start.Add(cfg.measuredDuration() - backwardsSlew)
	snapshots := coordinatorSnapshotFixture(fence, 2, cfg.measuredDuration(), 2)
	for index := range snapshots {
		snapshots[index].Phase = WorkerPhaseFinal
	}
	evidence := checkpointEvidenceFixture(true)
	evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	report, err := captureCheckpoint(t, recorder, wallCut, snapshots, evidence)
	if err != nil {
		t.Fatalf("successful final after monotonic deadline: %v", err)
	}
	if report.Window.Elapsed != cfg.measuredDuration() || !report.Window.End.Equal(wallCut) {
		t.Fatalf("final window = %+v, want process elapsed %v and wall end %v", report.Window, cfg.measuredDuration(), wallCut)
	}
}

func captureCheckpoint(
	t *testing.T,
	recorder *CheckpointRecorder,
	at time.Time,
	snapshots []WorkerSnapshot,
	evidence CheckpointEvidence,
) (Report, error) {
	t.Helper()
	directory := t.TempDir()
	return recorder.CaptureAndWrite(at, snapshots, evidence, CheckpointOutputPaths{
		JSON: filepath.Join(directory, "checkpoint.json"), Markdown: filepath.Join(directory, "checkpoint.md"),
	})
}

func checkpointEvidenceFixture(final bool) CheckpointEvidence {
	capacity := ReportCapacityEvidence{}
	if final {
		capacity = ReportCapacityEvidence{
			Attempted: true, Completed: true, Attribution: CapacityAttributionInfrastructure, MaximumPassingRate: 2_000,
			FirstFailingRate: 2_500, RecoveryPassed: true,
		}
	}
	return CheckpointEvidence{
		DatasetDigest:     hashReportValue("service-dataset-generation-1"),
		TopologyValidated: true,
		Lifecycle: LifecycleProofSnapshot{
			Candidates: 1_200, Loaded: 1_200, ColdEligible: 1_200, Reheated: 1_200, Completed: 1_200,
			ReheatLatency: newWorkerHistogramSnapshot(),
		},
		MetaCreate: MetaCreateAccountingSnapshot{
			ExpectedUnique: 3_000_020, Created: 3_000_023, ExternalDemoActivity: 3, Checkpoints: 2,
			ExpectedBySlot: [formalLogicalSlotGroups]uint64{3_000_020},
			CreatedBySlot:  [formalLogicalSlotGroups]uint64{3_000_023},
		},
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
