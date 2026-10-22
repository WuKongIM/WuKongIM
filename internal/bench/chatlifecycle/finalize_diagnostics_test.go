package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type finalizeDatasetProbe struct {
	digest string
	err    error
}

func (d *finalizeDatasetProbe) ProbeDatasetDigest(context.Context, Config) (string, error) {
	return d.digest, d.err
}

type finalizeMetaProbe struct{ err error }

func (m finalizeMetaProbe) Checkpoint(context.Context, []WorkerSnapshot, LifecycleSlotAssignment, bool) error {
	return m.err
}

func newFinalizeDiagnosticFixture(t *testing.T) (*ProductionEvidenceController, CoordinatorFinalCut, *bytes.Buffer, *finalizeDatasetProbe) {
	t.Helper()
	cfg := LocalConfig()
	cfg.RunID = "finalize-diagnostic-fixture"
	start := time.Unix(1_960_155_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "private-finalize-assignment", Generation: 7}
	accounting := NewMetaCreateAccounting()
	dataset := &finalizeDatasetProbe{digest: hashReportValue("finalize-diagnostic-dataset")}
	diagnostics := new(bytes.Buffer)
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{
			ProductFailures: 1, ReheatLatency: newWorkerHistogramSnapshot(),
		}, done: make(chan struct{})},
		Meta: &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
		Dataset: dataset, SlotAssignment: mustInitialLifecycleSlotAssignment(t), DiagnosticLog: diagnostics,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Close)
	startCut := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), startCut); err != nil {
		t.Fatal(err)
	}
	prepare := productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning)
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: startCut, Kind: CoordinatorCutTerminal, At: start.Add(time.Minute), Snapshots: prepare, StopRequested: true,
	})
	if err != nil || decision != CoordinatorProductFailure {
		t.Fatalf("prepare frozen product evidence = %q/%v", decision, err)
	}
	diagnostics.Reset()
	return controller, CoordinatorFinalCut{
		Start: startCut, At: start.Add(time.Minute + time.Second), Decision: decision, Prepare: prepare,
		FinalSnapshots: productionControllerWorkerSnapshots(cfg, fence, 2, time.Minute+time.Second, WorkerPhaseFinal),
	}, diagnostics, dataset
}

func TestFinalizeRecordsFailureBoundaryWithoutDiscardingProductObservation(t *testing.T) {
	privateCause := errors.New("private uid=alice token=secret /private/path")
	tests := []struct {
		name, stage, reason string
		mutate              func(*testing.T, *ProductionEvidenceController, *CoordinatorFinalCut, *finalizeDatasetProbe)
		cause               error
	}{
		{"state", "controller_guard", "state", func(_ *testing.T, c *ProductionEvidenceController, _ *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			c.closed = true
		}, nil},
		{"fence", "controller_guard", "fence", func(_ *testing.T, _ *ProductionEvidenceController, cut *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			cut.Start.Fence.Generation++
		}, nil},
		{"time", "controller_guard", "time", func(_ *testing.T, _ *ProductionEvidenceController, cut *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			cut.At = time.Time{}
		}, nil},
		{"count", "controller_guard", "snapshot_count", func(_ *testing.T, _ *ProductionEvidenceController, cut *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			cut.FinalSnapshots = cut.FinalSnapshots[:2]
		}, nil},
		{"continuity", "controller_continuity", "mismatch", func(_ *testing.T, _ *ProductionEvidenceController, cut *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			cut.Continuous = true
		}, nil},
		{"diagnostic_missing", "live_diagnostic", "missing", func(_ *testing.T, c *ProductionEvidenceController, _ *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			c.diagnostics = nil
		}, nil},
		{"diagnostic_output", "live_diagnostic", "output_failed", func(t *testing.T, c *ProductionEvidenceController, _ *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			c.diagnostics.path = obstructedFinalizePath(t, "status.json")
		}, nil},
		{"dataset_probe", "dataset", "probe_failed", func(_ *testing.T, _ *ProductionEvidenceController, _ *CoordinatorFinalCut, d *finalizeDatasetProbe) {
			d.err = privateCause
		}, privateCause},
		{"dataset_mismatch", "dataset", "digest_mismatch", func(_ *testing.T, _ *ProductionEvidenceController, _ *CoordinatorFinalCut, d *finalizeDatasetProbe) {
			d.digest = hashReportValue("different")
		}, nil},
		{"meta", "meta_checkpoint", "invalid", func(_ *testing.T, c *ProductionEvidenceController, _ *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			c.meta = finalizeMetaProbe{err: privateCause}
		}, privateCause},
		{"json_output", "write_json", "output_failed", func(t *testing.T, c *ProductionEvidenceController, _ *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			c.outputDir = obstructedFinalizePath(t, "reports")
		}, nil},
		{"markdown_output", "write_markdown", "output_failed", func(t *testing.T, c *ProductionEvidenceController, _ *CoordinatorFinalCut, _ *finalizeDatasetProbe) {
			if err := os.Mkdir(filepath.Join(c.outputDir, "final.md"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller, cut, diagnostics, dataset := newFinalizeDiagnosticFixture(t)
			before := controller.frozen
			test.mutate(t, controller, &cut, dataset)
			err := controller.Finalize(context.Background(), cut)
			if err == nil || !errors.Is(err, errProductionController) {
				t.Fatalf("Finalize error = %v, want production controller failure", err)
			}
			if test.cause != nil && !errors.Is(err, test.cause) {
				t.Error("Finalize discarded the private underlying cause")
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(diagnostics.String(), "private") {
				t.Fatal("finalization diagnostics exposed private cause or identity")
			}
			if controller.frozen.Outcome != before.Outcome || controller.frozen.Cause != before.Cause || !controller.frozen.Terminal {
				t.Fatalf("frozen product observation changed: %+v", controller.frozen)
			}
			found := false
			for _, line := range bytes.Split(diagnostics.Bytes(), []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var record map[string]any
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if record["event"] != "wkbench.chat_lifecycle.controller_failure" {
					continue
				}
				found = true
				if record["phase"] != "finalize" || record["stage"] != test.stage || record["reason"] != test.reason ||
					record["observed_outcome"] != string(before.Outcome) || record["observed_cause"] != string(before.Cause) || record["observed_terminal"] != true {
					t.Fatalf("finalization diagnostic = %s", line)
				}
			}
			if !found {
				t.Errorf("missing fixed finalization diagnostic: %s", diagnostics.String())
			}
			if test.name == "markdown_output" {
				if _, err := ReadReport(filepath.Join(controller.outputDir, "final.json")); err != nil {
					t.Fatalf("existing JSON-before-Markdown semantics changed: %v", err)
				}
			} else if _, err := os.Stat(filepath.Join(controller.outputDir, "final.json")); err == nil {
				t.Fatal("failed finalization invented a final report")
			}
		})
	}
}

func obstructedFinalizePath(t *testing.T, leaf string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("obstruction"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(file, leaf)
}

func TestCheckpointFailureStagesPreserveSentinelsAndRecorderState(t *testing.T) {
	tests := []struct {
		name, stage, reason string
		mutate              func(*CheckpointRecorder, *[]WorkerSnapshot, *CheckpointEvidence, *CheckpointOutputPaths)
		cause               error
	}{
		{"paths", "write_json", "invalid_paths", func(_ *CheckpointRecorder, _ *[]WorkerSnapshot, _ *CheckpointEvidence, p *CheckpointOutputPaths) {
			p.JSON = ""
		}, ErrCheckpointOutput},
		{"closed", "checkpoint_sequence", "invalid", func(r *CheckpointRecorder, _ *[]WorkerSnapshot, _ *CheckpointEvidence, _ *CheckpointOutputPaths) {
			r.closed = true
		}, ErrCheckpointSequence},
		{"evidence", "checkpoint_evidence", "invalid_evidence", func(_ *CheckpointRecorder, _ *[]WorkerSnapshot, e *CheckpointEvidence, _ *CheckpointOutputPaths) {
			e.TopologyValidated = false
		}, ErrCheckpointEvidence},
		{"dataset", "checkpoint_evidence", "dataset_mismatch", func(r *CheckpointRecorder, _ *[]WorkerSnapshot, _ *CheckpointEvidence, _ *CheckpointOutputPaths) {
			r.datasetDigest = hashReportValue("other")
		}, ErrCheckpointEvidence},
		{"snapshot_cut", "checkpoint_evidence", "invalid_snapshot_cut", func(_ *CheckpointRecorder, s *[]WorkerSnapshot, _ *CheckpointEvidence, _ *CheckpointOutputPaths) {
			(*s)[0].Uptime = 0
		}, ErrCheckpointEvidence},
		{"histogram", "snapshot_aggregate", "histogram_schema", func(_ *CheckpointRecorder, s *[]WorkerSnapshot, _ *CheckpointEvidence, _ *CheckpointOutputPaths) {
			(*s)[0].Sync.ConnectLatency.Count++
		}, ErrCoordinatorHistogramSchema},
		{"projection", "report_validation", "invalid", func(r *CheckpointRecorder, _ *[]WorkerSnapshot, _ *CheckpointEvidence, _ *CheckpointOutputPaths) {
			r.cfg.Workload.Topology.HashSlots = 0
		}, ErrReportInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := FormalConfig()
			start := time.Unix(1_960_155_000, 0).UTC()
			fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "private-assignment", Generation: 1}
			recorder, err := NewCheckpointRecorder(cfg, fence, start)
			if err != nil {
				t.Fatal(err)
			}
			snapshots := coordinatorSnapshotFixture(fence, 1, 2*time.Second, 1)
			evidence := checkpointEvidenceFixture(false)
			evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictProductFailure, Cause: VerdictCauseMessageLoss}
			outputs := CheckpointOutputPaths{JSON: filepath.Join(t.TempDir(), "final.json"), Markdown: filepath.Join(t.TempDir(), "final.md")}
			test.mutate(recorder, &snapshots, &evidence, &outputs)
			if test.name == "projection" {
				aggregator := cloneCheckpointAggregator(recorder.aggregator)
				aggregated, err := aggregator.Aggregate(snapshots)
				if err != nil || !validCheckpointEvidence(evidence) || validateReport(recorder.buildReport(CheckpointFinal, start.Add(time.Second), time.Second, aggregated, evidence)) == nil {
					t.Fatal("report-only invalid projection fixture does not isolate validation")
				}
			}
			beforeClosed := recorder.closed
			_, err = recorder.CaptureAndWrite(start.Add(time.Second), snapshots, evidence, outputs)
			if err == nil || !errors.Is(err, test.cause) {
				t.Fatalf("error = %v, want %v", err, test.cause)
			}
			diagnostic := finalizationDiagnostic(err, "unknown", "unknown")
			if diagnostic.Stage != test.stage || diagnostic.Reason != test.reason {
				t.Errorf("failure boundary = %+v", diagnostic)
			}
			if recorder.closed != beforeClosed || recorder.qualificationCaptured || recorder.aggregator.seen != [3]bool{} {
				t.Fatal("failed checkpoint committed recorder state")
			}
			if outputs.JSON != "" {
				if _, err := os.Stat(outputs.JSON); err == nil {
					t.Fatal("rejected checkpoint wrote a report")
				}
			}
		})
	}
}

func TestFinalizationDiagnosticRejectsUnboundedOrInvalidFrozenValues(t *testing.T) {
	controller, cut, diagnostics, _ := newFinalizeDiagnosticFixture(t)
	controller.frozen.Outcome = "private-token-outcome"
	controller.frozen.Cause = "private-token-cause"
	controller.closed = true
	err := controller.Finalize(context.Background(), cut)
	if err == nil || strings.Contains(diagnostics.String(), "private-token") {
		t.Fatalf("unsafe frozen observation entered diagnostics: %s", diagnostics.String())
	}
	private := newFinalizationError("private-stage", "private-reason", errors.New("private-error"))
	got := finalizationDiagnostic(private, "controller_unknown", "invalid")
	if got.Stage != "controller_unknown" || got.Reason != "invalid" {
		t.Fatalf("unbounded finalization classification = %+v", got)
	}
}

type finalizeDiagnosticHooks struct {
	*recordingCoordinatorRunHooks
	err          error
	corruptFinal bool
}

func (h *finalizeDiagnosticHooks) Finalize(_ context.Context, cut CoordinatorFinalCut) error {
	if h.corruptFinal {
		cut.FinalSnapshots[0].Sync.ConnectLatency.Count++
	}
	return h.err
}

func TestCoordinatorRetainsFinalizationBoundaryAndOriginalDecision(t *testing.T) {
	private := errors.New("private rpc response token=secret")
	known := &finalizationError{diagnostic: FinalizationFailure{
		Stage: "meta_checkpoint", Reason: "invalid", ObservedOutcome: VerdictProductFailure,
		ObservedCause: VerdictCauseMessageLoss, ObservedTerminal: true,
	}, cause: private}
	for _, test := range []struct {
		name, stage, reason string
		hookErr, stopErr    error
		corrupt             bool
	}{
		{"stop", "worker_stop", "control_failed", nil, private, false},
		{"known_hook", "meta_checkpoint", "invalid", known, nil, false},
		{"unknown_hook", "controller_unknown", "invalid", private, nil, false},
		{"final_aggregate", "snapshot_aggregate", "histogram_schema", nil, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := LocalConfig()
			configureFastCoordinatorCutoff(&cfg)
			clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
			started := make(chan struct{})
			log := []string{}
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for i := range workers {
				workers[i] = &recordingCoordinatorWorker{id: uint64(i), log: &log, grantBarrier: started}
			}
			workers[0].(*recordingCoordinatorWorker).stopErr = test.stopErr
			hooks := &finalizeDiagnosticHooks{recordingCoordinatorRunHooks: newRecordingCoordinatorRunHooks(), err: test.hookErr, corruptFinal: test.corrupt}
			hooks.terminalDecision = CoordinatorProductFailure
			coordinator, err := NewCoordinator(CoordinatorOptions{
				Generation: 17, Clock: clock, Workers: workers, Hooks: hooks,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(started)
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan CoordinatorResult, 1)
			go func() { done <- coordinator.Run(context.Background(), cfg) }()
			<-started
			<-clock.created
			<-clock.created
			clock.advance(cfg.Thresholds.Timeline.Final)
			result := <-done
			if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeFinalize || result.Continuation != nil {
				t.Fatalf("finalization gate changed: %+v", result)
			}
			field := reflect.ValueOf(result).FieldByName("FinalizationFailure")
			if !field.IsValid() {
				t.Fatal("CoordinatorResult discarded finalization boundary")
			}
			failure := field.Interface().(FinalizationFailure)
			body, _ := json.Marshal(failure)
			var projection map[string]any
			_ = json.Unmarshal(body, &projection)
			if failure.Stage != test.stage || failure.Reason != test.reason || projection["observed_decision"] != string(CoordinatorProductFailure) {
				t.Fatalf("finalization boundary = %s", body)
			}
			if test.name == "known_hook" && (failure.ObservedCause != VerdictCauseMessageLoss || !failure.ObservedTerminal) {
				t.Fatal("known hook lost frozen product cause")
			}
			if strings.Contains(string(body), "private") {
				t.Fatal("raw error leaked into coordinator result")
			}
			for _, suffix := range []string{"0", "1", "2"} {
				found := false
				for _, operation := range coordinatorTestLogSnapshot(&log) {
					if operation == "stop-"+suffix {
						found = true
					}
				}
				if !found {
					t.Fatalf("worker %s was not stopped", suffix)
				}
			}
		})
	}
}

func TestFinalWorkerStopPreservesPrivateCause(t *testing.T) {
	private := errors.New("private-stop-token")
	log := []string{}
	workers := make([]CoordinatorWorker, 3)
	for i := range workers {
		workers[i] = &recordingCoordinatorWorker{id: uint64(i), log: &log, stopErr: private}
	}
	c := &Coordinator{workers: workers, cleanupTimeout: time.Second}
	_, err := c.stopAssignedSnapshots(WorkerFence{}, [3]bool{true, true, true})
	if !errors.Is(err, ErrCoordinatorSnapshotCount) || !errors.Is(err, private) {
		t.Fatalf("stop error chain discarded cause: %v", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("Stop error formatting exposed private cause")
	}
}
