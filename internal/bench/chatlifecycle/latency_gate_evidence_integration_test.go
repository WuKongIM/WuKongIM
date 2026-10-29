//go:build integration

package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests cross the existing native evaluator/report seam. The test DTO
// deliberately uses JSON rather than a future production type: the old source
// must compile and fail for missing evidence, not for a missing interface.
type m01GateEvidence struct {
	SchemaVersion           string         `json:"schema_version"`
	Status                  string         `json:"status"`
	EvaluatedElapsed        time.Duration  `json:"evaluated_elapsed"`
	LastLatencyElapsed      time.Duration  `json:"last_latency_elapsed"`
	LatencyAge              time.Duration  `json:"latency_age"`
	TerminalFrozen          bool           `json:"terminal_frozen"`
	BaselineElapsed         time.Duration  `json:"baseline_elapsed"`
	WindowStartElapsed      time.Duration  `json:"window_start_elapsed"`
	WindowEndElapsed        time.Duration  `json:"window_end_elapsed"`
	WarmupDuration          time.Duration  `json:"warmup_duration"`
	WindowDuration          time.Duration  `json:"window_duration"`
	MaxAge                  time.Duration  `json:"max_age"`
	P99FractionDenominator  uint64         `json:"p99_fraction_denominator"`
	P999FractionDenominator uint64         `json:"p999_fraction_denominator"`
	WindowCapacity          int            `json:"window_capacity"`
	Classes                 []m01GateClass `json:"classes"`
}

type m01GateClass struct {
	Operation          LatencyOperation    `json:"operation"`
	P99Limit           time.Duration       `json:"p99_limit"`
	P999Limit          time.Duration       `json:"p999_limit"`
	P99Count           uint64              `json:"p99_count"`
	P99Above           uint64              `json:"p99_above"`
	P999Count          uint64              `json:"p999_count"`
	P999Above          uint64              `json:"p999_above"`
	P99Samples         int                 `json:"p99_samples"`
	P999Samples        int                 `json:"p999_samples"`
	Breached           bool                `json:"breached"`
	BreachSinceElapsed *time.Duration      `json:"breach_since_elapsed"`
	BreachAttribution  CapacityAttribution `json:"breach_attribution"`
}

func TestLatencyGateEvidenceNewWriterUsesV8WithoutChangingThresholdVersion(t *testing.T) {
	body, err := MarshalReport(reportFixture(t), ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["schema_version"]) != `"wukongim/chat-lifecycle-report/v8"` || string(raw["threshold_version"]) != `"wukongim/chat-lifecycle-thresholds/v2"` {
		t.Fatalf("new audit schema did not distinguish persisted evidence, or changed numeric gate version: %s/%s", raw["schema_version"], raw["threshold_version"])
	}
}

func TestLatencyGateEvidenceCopiesActualImmutableEvaluatorThresholds(t *testing.T) {
	// The constructor already accepts this local configuration. This control
	// prevents a diagnostic export hardcoding Formal values; it does not alter
	// any deployed Formal profile or acceptance threshold.
	thresholds := LocalConfig().Thresholds
	thresholds.Timeline.LatencyWarmup = time.Minute
	thresholds.Latency.HotSendACK = LatencyLimit{P99: 300 * time.Millisecond, P999: 750 * time.Millisecond}
	start := time.Unix(1_980_000_000, 0)
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	counters := LatencyCountersForThresholds(thresholds.Latency)
	m01Observe(t, evaluator, start.Add(time.Minute), &counters, CapacityAttributionProduct)
	counters.Hot.Count = 1_000
	m01Observe(t, evaluator, start.Add(65*time.Second), &counters, CapacityAttributionProduct)
	var gate m01GateEvidence
	if err := json.Unmarshal(m01GateJSON(t, projectReportVerdict(evaluator.Snapshot())), &gate); err != nil {
		t.Fatal(err)
	}
	if gate.WarmupDuration != time.Minute || len(gate.Classes) != 3 || gate.Classes[0].P99Limit != 300*time.Millisecond || gate.Classes[0].P999Limit != 750*time.Millisecond || gate.Classes[0].P99Count != 1_000 || gate.WindowDuration != 5*time.Minute || gate.P99FractionDenominator != 100 || gate.P999FractionDenominator != 1_000 {
		t.Fatalf("export replaced actual immutable evaluator thresholds with another profile: %+v", gate)
	}
}

func TestLatencyGateEvidenceWarmupAndBaselineAreNotMeasuredProof(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	for _, cut := range []time.Duration{0, 9 * time.Minute, 10 * time.Minute} {
		if cut != 0 {
			counters.Hot.Count += 10_000
			counters.Hot.AboveP99 += 500
			m01Observe(t, evaluator, start.Add(cut), &counters, CapacityAttributionNone)
		}
		gate := m01NativeGate(t, evaluator)
		if gate.Status != "not_evaluated" || gate.EvaluatedElapsed != cut || gate.WindowEndElapsed != gate.LastLatencyElapsed {
			t.Fatalf("unmeasured cut %s became proof: %+v", cut, gate)
		}
		for _, row := range gate.Classes {
			if row.P99Count != 0 || row.P999Count != 0 || row.Breached || row.BreachSinceElapsed != nil {
				t.Fatalf("warmup/crossing baseline leaked into native ring: %+v", row)
			}
		}
	}
	counters.Hot.Count += 1_000
	counters.Hot.AboveP99 += 10
	m01Observe(t, evaluator, start.Add(605*time.Second), &counters, CapacityAttributionNone)
	gate := m01NativeGate(t, evaluator)
	if gate.Status != "current" || gate.BaselineElapsed != 600*time.Second || gate.WindowStartElapsed != 600*time.Second || gate.WindowEndElapsed != 605*time.Second || gate.Classes[0].P99Count != 1_000 || gate.Classes[0].P99Above != 10 || gate.Classes[0].Breached {
		t.Fatalf("first measured native delta was not exported exactly: %+v", gate)
	}
	if evaluator.LatencyWindowEvidence().Qualified != [3]bool{} {
		t.Fatal("five-second observation manufactured positive600s qualification")
	}
}

func TestLatencyGateEvidenceExactFiveMinuteExpiryAndFractions(t *testing.T) {
	t.Run("retained_native_ring_not_cumulative_histogram", func(t *testing.T) {
		start, evaluator, counters := m01Evaluator(t)
		m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionNone)
		counters.Hot.Count, counters.Hot.AboveP99, counters.Hot.AboveP999 = 1_000, 11, 1
		m01Observe(t, evaluator, start.Add(605*time.Second), &counters, CapacityAttributionNone)
		counters.Hot.Count = 2_000
		m01Observe(t, evaluator, start.Add(610*time.Second), &counters, CapacityAttributionNone)
		m01Observe(t, evaluator, start.Add(904*time.Second), &counters, CapacityAttributionNone)
		before := m01NativeGate(t, evaluator)
		if before.Classes[0].P99Count != 2_000 || before.Classes[0].P99Above != 11 || before.WindowStartElapsed != 604*time.Second {
			t.Fatalf("299s-old entry was lost: %+v", before)
		}
		m01Observe(t, evaluator, start.Add(905*time.Second), &counters, CapacityAttributionNone)
		after := m01NativeGate(t, evaluator)
		row := after.Classes[0]
		if row.P99Count != 1_000 || row.P999Count != 1_000 || row.P99Above != 0 || row.P999Above != 0 || row.P99Samples != 3 || row.P999Samples != 3 || row.Breached || row.BreachSinceElapsed != nil || after.WindowStartElapsed != 605*time.Second {
			t.Fatalf("exact300s entry did not expire from native sums: %+v", after)
		}
	})
	for operation := 0; operation < 3; operation++ {
		for _, boundary := range []struct {
			name      string
			p99, p999 uint64
			breached  bool
		}{{"p99_equality", 10, 0, false}, {"p99_above", 11, 0, true}, {"p999_equality", 1, 1, false}, {"p999_above", 2, 2, true}} {
			t.Run(string([]LatencyOperation{LatencyHotSendACK, LatencyColdSendACK, LatencyFullSync}[operation])+"/"+boundary.name, func(t *testing.T) {
				start, evaluator, counters := m01Evaluator(t)
				m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionNone)
				row := []*LatencyThresholdCounters{&counters.Hot, &counters.Cold, &counters.Sync}[operation]
				row.Count, row.AboveP99, row.AboveP999 = 1_000, boundary.p99, boundary.p999
				m01Observe(t, evaluator, start.Add(605*time.Second), &counters, CapacityAttributionNone)
				gate := m01NativeGate(t, evaluator)
				got := gate.Classes[operation]
				if gate.WindowDuration != 5*time.Minute || gate.P99FractionDenominator != 100 || gate.P999FractionDenominator != 1_000 || got.P99Count != 1_000 || got.P999Count != 1_000 || got.P99Above != boundary.p99 || got.P999Above != boundary.p999 || got.Breached != boundary.breached {
					t.Fatalf("strict native threshold fraction changed: %+v", gate)
				}
				if got.Breached != (got.BreachSinceElapsed != nil) || (got.BreachSinceElapsed != nil && *got.BreachSinceElapsed != 605*time.Second) {
					t.Fatalf("active native breach start is missing/contradictory: %+v", got)
				}
			})
		}
	}
}

func TestLatencyGateEvidenceBreachResetAttributionAndFatalRemainNative(t *testing.T) {
	for _, attribution := range []CapacityAttribution{CapacityAttributionProduct, CapacityAttributionInfrastructure, CapacityAttributionInsufficient} {
		t.Run(string(attribution), func(t *testing.T) {
			start, evaluator, counters := m01Evaluator(t)
			m01Observe(t, evaluator, start.Add(600*time.Second), &counters, attribution)
			counters.Hot.Count, counters.Hot.AboveP99 = 10_000, 200
			m01Observe(t, evaluator, start.Add(605*time.Second), &counters, attribution)
			first := m01NativeGate(t, evaluator)
			if !first.Classes[0].Breached || first.Classes[0].BreachSinceElapsed == nil || *first.Classes[0].BreachSinceElapsed != 605*time.Second || first.Classes[0].BreachAttribution != attribution || evaluator.Snapshot().Terminal {
				t.Fatalf("first native warning was not preserved: %+v", first)
			}
			counters.Hot.Count = 20_000
			m01Observe(t, evaluator, start.Add(610*time.Second), &counters, attribution)
			reset := m01NativeGate(t, evaluator)
			if reset.Classes[0].Breached || reset.Classes[0].BreachSinceElapsed != nil || reset.Classes[0].BreachAttribution != CapacityAttributionNone {
				t.Fatalf("native equality reset was not exported: %+v", reset)
			}
			for _, second := range []int{615, 675, 735, 795, 855, 914, 915} {
				counters.Hot.Count += 10_000
				counters.Hot.AboveP99 += 400
				m01Observe(t, evaluator, start.Add(time.Duration(second)*time.Second), &counters, attribution)
				if second == 914 && evaluator.Snapshot().Terminal {
					t.Fatal("continuous five-minute fatal deadline moved earlier")
				}
			}
			got := evaluator.Snapshot()
			switch attribution {
			case CapacityAttributionProduct:
				if !got.Terminal || got.Outcome != VerdictProductFailure || got.Cause != VerdictCauseHotLatency {
					t.Fatalf("native product fatal changed: %+v", got)
				}
			case CapacityAttributionInfrastructure:
				if got.Terminal || got.Outcome != VerdictPassedWithCapacityWarning || got.Cause != VerdictCauseInfrastructureCapacity {
					t.Fatalf("native infrastructure branch changed: %+v", got)
				}
			case CapacityAttributionInsufficient:
				if !got.Terminal || got.Outcome != VerdictInsufficientEvidence || got.Cause != VerdictCauseInsufficientEvidence {
					t.Fatalf("native insufficient branch changed: %+v", got)
				}
			}
			gate := m01NativeGate(t, evaluator)
			if gate.Classes[0].BreachSinceElapsed == nil || *gate.Classes[0].BreachSinceElapsed != 615*time.Second || gate.Classes[0].BreachAttribution != attribution || got.LatencyWarnings.Hot != 2 || gate.EvaluatedElapsed != 915*time.Second {
				t.Fatalf("reset/new native continuous breach was reconstructed incorrectly: %+v / %+v", gate, got)
			}
			if evaluator.LatencyWindowEvidence().Qualified != [3]bool{} {
				t.Fatal("five-minute fatal was confused with600s qualification")
			}
		})
	}
}

func TestLatencyGateEvidencePositiveQualificationDoesNotClearCurrentBreach(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionProduct)
	for cut := 1; cut <= 120; cut++ {
		counters.Hot.Count += 1_000
		counters.Cold.Count += 1_000
		counters.Sync.Count += 1_000
		if cut == 120 {
			counters.Hot.AboveP99 += 1_000
		}
		m01Observe(t, evaluator, start.Add(600*time.Second+time.Duration(cut)*5*time.Second), &counters, CapacityAttributionProduct)
	}
	positive := evaluator.LatencyWindowEvidence()
	if positive.Duration != 600*time.Second || positive.Qualified != [3]bool{true, true, true} || positive.Samples != [3]uint64{120_000, 120_000, 120_000} {
		t.Fatalf("positive-control qualification changed: %+v", positive)
	}
	gate := m01NativeGate(t, evaluator)
	if !gate.Classes[0].Breached || gate.Classes[0].P99Count != 60_000 || gate.Classes[0].P99Above != 1_000 || gate.Classes[0].BreachSinceElapsed == nil || *gate.Classes[0].BreachSinceElapsed != 1_200*time.Second || evaluator.Snapshot().Terminal {
		t.Fatalf("positive coverage hid actual current breach: %+v", gate)
	}
}

func TestLatencyGateEvidenceStaleAndInvalidDoNotInventHealthySamples(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionProduct)
	counters.Hot.Count, counters.Hot.AboveP99 = 1_000, 20
	m01Observe(t, evaluator, start.Add(605*time.Second), &counters, CapacityAttributionProduct)
	if err := evaluator.Observe(VerdictObservation{At: start.Add(610 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if gate := m01NativeGate(t, evaluator); gate.Status != "current" {
		t.Fatalf("exact existing5s freshness boundary changed: %+v", gate)
	}
	if err := evaluator.Observe(VerdictObservation{At: start.Add(611 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	gate := m01NativeGate(t, evaluator)
	if gate.Status != "stale" || gate.EvaluatedElapsed != 611*time.Second || gate.LastLatencyElapsed != 605*time.Second || gate.Classes[0].P99Count != 1_000 || !gate.Classes[0].Breached || gate.Classes[0].BreachSinceElapsed == nil || *gate.Classes[0].BreachSinceElapsed != 605*time.Second {
		t.Fatalf("non-latency cut refreshed/recomputed native gate: %+v", gate)
	}
	counters.Hot.Count--
	m01Observe(t, evaluator, start.Add(612*time.Second), &counters, CapacityAttributionProduct)
	if got := evaluator.Snapshot(); got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseCounterRegression {
		t.Fatalf("native regression control changed: %+v", got)
	}
	if gate := m01NativeGate(t, evaluator); gate.Status != "invalid" || gate.LastLatencyElapsed != 605*time.Second {
		t.Fatalf("invalid cut manufactured current proof: %+v", gate)
	}
}

func TestLatencyGateEvidenceSnapshotsAndTerminalCutAreImmutable(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionProduct)
	counters.Hot.Count, counters.Hot.AboveP99 = 1_000, 20
	m01Observe(t, evaluator, start.Add(605*time.Second), &counters, CapacityAttributionProduct)
	old := evaluator.Snapshot()
	before := m01GateJSON(t, projectReportVerdict(old))
	counters.Hot.Count = 2_000
	m01Observe(t, evaluator, start.Add(610*time.Second), &counters, CapacityAttributionProduct)
	if !bytes.Equal(before, m01GateJSON(t, projectReportVerdict(old))) {
		t.Fatal("later native observation mutated an already returned snapshot")
	}
	if err := evaluator.Observe(VerdictObservation{At: start.Add(615 * time.Second), Signals: []VerdictSignal{{Outcome: VerdictOperatorStop, Cause: VerdictCauseOperatorRequested}}}); err != nil {
		t.Fatal(err)
	}
	frozen := m01GateJSON(t, projectReportVerdict(evaluator.Snapshot()))
	counters.Hot.Count = 9_000
	m01Observe(t, evaluator, start.Add(700*time.Second), &counters, CapacityAttributionProduct)
	if !bytes.Equal(frozen, m01GateJSON(t, projectReportVerdict(evaluator.Snapshot()))) {
		t.Fatal("postterminal cut aged or rewrote actual native Stop gate state")
	}
	gate := m01NativeGate(t, evaluator)
	if gate.EvaluatedElapsed != 615*time.Second || gate.LastLatencyElapsed != 610*time.Second || gate.LatencyAge != 5*time.Second || !gate.TerminalFrozen {
		t.Fatalf("terminal cut positions/freeze attribution changed: %+v", gate)
	}
}

func TestLatencyGateEvidenceControllerFinalizationKeepsPreStopCut(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "m01-native-stop-cut"
	start := time.Unix(1_980_000_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "m01-native-stop-cut", Generation: 1}
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
		Dataset: &productionControllerDataset{digest: hashReportValue("m01-stop-dataset")}, SlotAssignment: mustInitialLifecycleSlotAssignment(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	started := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), started); err != nil {
		t.Fatal(err)
	}
	for index, second := range []int{600, 605} {
		snapshots := productionControllerQualifiedSnapshots(cfg, fence, uint64(index+1), time.Duration(second)*time.Second, WorkerPhaseRunning, uint64(index+1)*1_000)
		if outcome, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{Start: started, Kind: CoordinatorCutPeriodic, At: start.Add(time.Duration(second) * time.Second), Snapshots: snapshots}); err != nil || outcome != "" {
			t.Fatalf("native periodic control: %s/%v", outcome, err)
		}
	}
	prepare := productionControllerQualifiedSnapshots(cfg, fence, 3, 610*time.Second, WorkerPhaseRunning, 3_000)
	if outcome, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{Start: started, Kind: CoordinatorCutTerminal, At: start.Add(610 * time.Second), Snapshots: prepare, StopRequested: true}); err != nil || outcome != CoordinatorStopped {
		t.Fatalf("native Stop control: %s/%v", outcome, err)
	}
	before := m01GateJSON(t, projectReportVerdict(controller.evaluator.Snapshot()))
	final := productionControllerQualifiedSnapshots(cfg, fence, 4, 700*time.Second, WorkerPhaseFinal, 4_000)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{Start: started, At: start.Add(700 * time.Second), Decision: CoordinatorStopped, Prepare: prepare, FinalSnapshots: final}); err != nil {
		t.Fatal(err)
	}
	report, err := ReadReport(filepath.Join(controller.OutputDir(), "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictOperatorStop || report.Messages.Sent != 12_003 {
		t.Fatalf("final reconciliation control changed: %+v", report)
	}
	after := m01GateJSON(t, report.Verdict)
	if !bytes.Equal(before, after) {
		t.Fatal("drain/reconciliation rewrote the actual negative gate cut")
	}
	var gate m01GateEvidence
	if err := json.Unmarshal(after, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.EvaluatedElapsed != 610*time.Second || gate.LastLatencyElapsed != 610*time.Second || gate.LatencyAge != 0 || !gate.TerminalFrozen || report.Window.Elapsed-gate.EvaluatedElapsed != 90*time.Second || gate.Classes[0].P99Count != 6_000 {
		t.Fatalf("final native gate was reconstructed/refreshed from drained histograms, or its historical cut was hidden: %+v", gate)
	}
}

func TestLatencyGateEvidenceFixedThreeClassesAndBoundedMemory(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionProduct)
	for cut := 1; cut <= 400; cut++ {
		counters.Hot.Count += 1_000
		counters.Cold.Count += 1_000
		counters.Sync.Count += 1_000
		m01Observe(t, evaluator, start.Add(600*time.Second+time.Duration(cut)*5*time.Second), &counters, CapacityAttributionProduct)
	}
	gate := m01NativeGate(t, evaluator)
	if gate.WindowCapacity != 64 || len(gate.Classes) != 3 || len(m01GateJSON(t, projectReportVerdict(evaluator.Snapshot()))) > 4_096 {
		t.Fatalf("gate export retained history or changed fixed bounds: %+v", gate)
	}
	for _, row := range gate.Classes {
		if row.P99Samples != 60 || row.P999Samples != 60 || row.P99Count != 60_000 || row.P999Count != 60_000 {
			t.Fatalf("fixed native ring no longer bounded/exact: %+v", row)
		}
	}
}

func TestLatencyGateEvidenceCapacityFailureKeepsActualRetainedCounts(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionProduct)
	for cut := 1; cut <= 65; cut++ {
		counters.Hot.Count++
		counters.Cold.Count++
		counters.Sync.Count++
		m01Observe(t, evaluator, start.Add(600*time.Second+time.Duration(cut)*time.Second), &counters, CapacityAttributionProduct)
	}
	if got := evaluator.Snapshot(); !got.Terminal || got.Outcome != VerdictHarnessInvalid || got.Cause != VerdictCauseCounterRegression {
		t.Fatalf("original64-entry native gate capacity control changed: %+v", got)
	}
	gate := m01NativeGate(t, evaluator)
	if gate.Status != "invalid" || !gate.TerminalFrozen || gate.EvaluatedElapsed != 665*time.Second || gate.LastLatencyElapsed != 664*time.Second || gate.LatencyAge != time.Second {
		t.Fatalf("capacity failure manufactured current gate evidence: %+v", gate)
	}
	for _, row := range gate.Classes {
		if row.P99Count != 64 || row.P999Count != 64 || row.P99Samples != 64 || row.P999Samples != 64 {
			t.Fatalf("capacity failure erased exact retained native state: %+v", row)
		}
	}
}

func TestLatencyGateEvidenceReportElapsedSurvivesAuditUTCJump(t *testing.T) {
	start, evaluator, counters := m01Evaluator(t)
	m01Observe(t, evaluator, start.Add(600*time.Second), &counters, CapacityAttributionProduct)
	counters.Hot.Count = 1_000
	m01Observe(t, evaluator, start.Add(605*time.Second), &counters, CapacityAttributionProduct)
	if err := evaluator.Observe(VerdictObservation{At: start.Add(606 * time.Second), Signals: []VerdictSignal{{Outcome: VerdictOperatorStop, Cause: VerdictCauseOperatorRequested}}}); err != nil {
		t.Fatal(err)
	}
	cfg := FormalConfig()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "m01-clock-report", Generation: 1}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Verdict, evidence.LatencyWindow = evaluator.Snapshot(), evaluator.LatencyWindowEvidence()
	report, err := captureCheckpoint(t, recorder, start.Add(607*time.Second), qualifiedCheckpointSnapshotFixture(fence, 1, 607*time.Second, 1_000), evidence)
	if err != nil {
		t.Fatal(err)
	}
	want := m01GateJSON(t, report.Verdict)
	// Like the existing report_clock tests, move only the serialized audit cut.
	// This verifies stored elapsed authority; it does not emulate a real OS slew.
	for _, jump := range []time.Duration{-204 * time.Millisecond, 204 * time.Millisecond} {
		copy := report
		copy.Window.End = copy.Window.End.Add(jump)
		body, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "utc-audit-jump.json")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := ReadReport(path)
		if err != nil {
			t.Fatalf("audit UTC jump changed native elapsed proof: %v", err)
		}
		if !bytes.Equal(want, m01GateJSON(t, loaded.Verdict)) || loaded.Window.Elapsed != 607*time.Second {
			t.Fatal("reader recomputed native window/breach from wall audit time")
		}
	}
}

func TestLatencyGateEvidenceReportOptionalCompatibilityAndStrictParsing(t *testing.T) {
	report := reportFixture(t)
	addQualifiedReportEvidence(&report)
	base, err := MarshalReport(report, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	base = m01ReportVersion(t, base, "wukongim/chat-lifecycle-report/v8")
	valid := m01ValidPersistedGate(t)
	for _, test := range []struct {
		name, version string
		child         []byte
		present       bool
	}{
		{"legacy_v7_absent", "wukongim/chat-lifecycle-report/v7", nil, false},
		{"legacy_v7_null", "wukongim/chat-lifecycle-report/v7", []byte("null"), false},
		{"v8_unknown_absent", "wukongim/chat-lifecycle-report/v8", nil, false},
		{"v8_unknown_null", "wukongim/chat-lifecycle-report/v8", []byte("null"), false},
		{"v8_current_valid", "wukongim/chat-lifecycle-report/v8", valid, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := m01InsertGate(t, m01ReportVersion(t, base, test.version), test.child)
			loaded, err := m01ReadBody(t, body)
			if err != nil {
				t.Fatalf("compatible optional evidence rejected: %v", err)
			}
			if loaded.SchemaVersion != test.version {
				t.Fatal("legacy report was silently reheadered into new proof")
			}
			encoded, err := json.Marshal(loaded)
			if err != nil {
				t.Fatal(err)
			}
			child := m01OptionalGateJSON(t, loaded.Verdict)
			if test.present {
				if !bytes.Equal(m01CanonicalJSON(t, valid), m01CanonicalJSON(t, child)) {
					t.Fatal("reader did not preserve actual optional proof")
				}
			} else if len(child) != 0 || bytes.Contains(encoded, []byte(`"latency_gate":{`)) {
				t.Fatal("legacy absence/null manufactured native proof")
			}
		})
	}
	t.Run("legacy_v7_cannot_carry_new_native_proof", func(t *testing.T) {
		if _, err := m01ReadBody(t, m01InsertGate(t, m01ReportVersion(t, base, "wukongim/chat-lifecycle-report/v7"), valid)); err == nil {
			t.Fatal("legacy v7 silently accepted/reheadered new latency gate proof")
		}
	})
	mutations := []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{"unknown_key", func(g map[string]json.RawMessage) { g["historical_p99"] = json.RawMessage(`1`) }},
		{"missing_key", func(g map[string]json.RawMessage) { delete(g, "p99_fraction_denominator") }},
		{"null_scalar", func(g map[string]json.RawMessage) { g["window_duration"] = json.RawMessage(`null`) }},
		{"wrong_scalar_type", func(g map[string]json.RawMessage) { g["window_duration"] = json.RawMessage(`"300000000000"`) }},
		{"casefold_key", func(g map[string]json.RawMessage) {
			g["Window_Duration"] = g["window_duration"]
			delete(g, "window_duration")
		}},
		{"wrong_version", func(g map[string]json.RawMessage) {
			g["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-latency-gate/v0"`)
		}},
		{"wrong_fraction", func(g map[string]json.RawMessage) { g["p99_fraction_denominator"] = json.RawMessage(`99`) }},
		{"wrong_window", func(g map[string]json.RawMessage) { g["window_duration"] = json.RawMessage(`299000000000`) }},
		{"wrong_capacity", func(g map[string]json.RawMessage) { g["window_capacity"] = json.RawMessage(`65`) }},
		{"negative_elapsed", func(g map[string]json.RawMessage) { g["evaluated_elapsed"] = json.RawMessage(`-1`) }},
		{"last_cut_after_evaluation", func(g map[string]json.RawMessage) { g["last_latency_elapsed"] = json.RawMessage(`86401000000000`) }},
		{"future_cut_vs_report", func(g map[string]json.RawMessage) { g["evaluated_elapsed"] = json.RawMessage(`86401000000000`) }},
		{"not_evaluated_with_measured_rows", func(g map[string]json.RawMessage) { g["status"] = json.RawMessage(`"not_evaluated"`) }},
		{"null_classes", func(g map[string]json.RawMessage) { g["classes"] = json.RawMessage(`null`) }},
		{"two_classes", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			g["classes"], _ = json.Marshal(rows[:2])
		}},
		{"four_classes", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			g["classes"], _ = json.Marshal(append(rows, rows[0]))
		}},
		{"duplicate_operation", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[1]["operation"] = rows[0]["operation"]
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_unknown_key", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["raw_uid"] = json.RawMessage(`"forbidden"`)
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_missing_key", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			delete(rows[0], "breached")
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_scalar_null", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["p99_count"] = json.RawMessage(`null`)
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_bool_null", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["breached"] = json.RawMessage(`null`)
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_casefold_key", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["P99_Count"] = rows[0]["p99_count"]
			delete(rows[0], "p99_count")
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_impossible_count", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["p99_above"] = json.RawMessage(`1001`)
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_unbounded_samples", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["p99_samples"] = json.RawMessage(`65`)
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_false_with_breach_start", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["breach_since_elapsed"] = json.RawMessage(`86399000000000`)
			g["classes"], _ = json.Marshal(rows)
		}},
		{"row_true_without_breach_start", func(g map[string]json.RawMessage) {
			rows := m01ClassMaps(t, g)
			rows[0]["breached"] = json.RawMessage(`true`)
			rows[0]["p99_above"] = json.RawMessage(`11`)
			g["classes"], _ = json.Marshal(rows)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			var gate map[string]json.RawMessage
			if err := json.Unmarshal(valid, &gate); err != nil {
				t.Fatal(err)
			}
			mutation.mutate(gate)
			child, err := json.Marshal(gate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m01ReadBody(t, m01InsertGate(t, base, child)); err == nil {
				t.Fatal("malformed optional proof was accepted")
			}
		})
	}
	t.Run("duplicate_child_key", func(t *testing.T) {
		child := bytes.Replace(valid, []byte(`"window_duration":300000000000`), []byte(`"window_duration":300000000000,"window_duration":300000000000`), 1)
		if bytes.Equal(child, valid) {
			t.Fatal("fixture did not add the duplicate key")
		}
		if _, err := m01ReadBody(t, m01InsertGate(t, base, child)); err == nil {
			t.Fatal("duplicate optional key was accepted")
		}
	})
	t.Run("duplicate_row_key", func(t *testing.T) {
		child := bytes.Replace(valid, []byte(`"p99_count":1000`), []byte(`"p99_count":1000,"p99_count":1000`), 1)
		if bytes.Equal(child, valid) {
			t.Fatal("fixture did not add the duplicate row key")
		}
		if _, err := m01ReadBody(t, m01InsertGate(t, base, child)); err == nil {
			t.Fatal("duplicate optional row key was accepted")
		}
	})
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"base_unknown_stays_strict", func(body []byte) []byte { return append([]byte(`{"unexpected_base_key":1,`), body[1:]...) }},
		{"trailing_document_stays_strict", func(body []byte) []byte { return append(body, []byte(` {}`)...) }},
		{"persisted_4MiB_cap_unchanged", func(body []byte) []byte {
			return append(body, []byte(strings.Repeat(" ", int(maxPersistedReportBytes)))...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := m01ReadBody(t, test.mutate(m01InsertGate(t, base, valid))); err == nil {
				t.Fatal("optional decoder relaxed an existing base defense")
			}
		})
	}
}

func m01Evaluator(t *testing.T) (time.Time, *VerdictEvaluator, LatencyCounters) {
	t.Helper()
	start := time.Unix(1_980_000_000, 0)
	thresholds := FormalConfig().Thresholds
	evaluator, err := NewVerdictEvaluator(start, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	return start, evaluator, LatencyCountersForThresholds(thresholds.Latency)
}

func m01Observe(t *testing.T, evaluator *VerdictEvaluator, at time.Time, counters *LatencyCounters, attribution CapacityAttribution) {
	t.Helper()
	if err := evaluator.Observe(VerdictObservation{At: at, Latency: counters, LatencyAttribution: attribution}); err != nil {
		t.Fatal(err)
	}
}

func m01NativeGate(t *testing.T, evaluator *VerdictEvaluator) m01GateEvidence {
	t.Helper()
	body := m01GateJSON(t, projectReportVerdict(evaluator.Snapshot()))
	var gate m01GateEvidence
	if err := json.Unmarshal(body, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.SchemaVersion != "wukongim/chat-lifecycle-latency-gate/v1" || gate.WarmupDuration != 10*time.Minute || gate.WindowDuration != 5*time.Minute || gate.MaxAge != 5*time.Second || gate.WindowCapacity != 64 || len(gate.Classes) != 3 {
		t.Fatalf("missing/changed fixed native gate schema: %s", body)
	}
	limits := FormalConfig().Thresholds.Latency
	for index, expected := range []struct {
		operation LatencyOperation
		limits    LatencyLimit
	}{{LatencyHotSendACK, limits.HotSendACK}, {LatencyColdSendACK, limits.Cold}, {LatencyFullSync, limits.Sync}} {
		if gate.Classes[index].Operation != expected.operation || gate.Classes[index].P99Limit != expected.limits.P99 || gate.Classes[index].P999Limit != expected.limits.P999 {
			t.Fatalf("operation order/duration threshold changed: %+v", gate.Classes)
		}
	}
	return gate
}

func m01GateJSON(t *testing.T, verdict ReportVerdictEvidence) []byte {
	t.Helper()
	body := m01OptionalGateJSON(t, verdict)
	if len(body) == 0 {
		t.Fatal("native report omitted actual five-minute latency_gate evidence")
	}
	return m01CanonicalJSON(t, body)
}

func m01OptionalGateJSON(t *testing.T, verdict ReportVerdictEvidence) []byte {
	t.Helper()
	body, err := json.Marshal(verdict)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw["latency_gate"]) == 0 || bytes.Equal(raw["latency_gate"], []byte("null")) {
		return nil
	}
	return raw["latency_gate"]
}

func m01CanonicalJSON(t *testing.T, body []byte) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func m01ValidPersistedGate(t *testing.T) []byte {
	t.Helper()
	limits := FormalConfig().Thresholds.Latency
	rows := []m01GateClass{
		{Operation: LatencyHotSendACK, P99Limit: limits.HotSendACK.P99, P999Limit: limits.HotSendACK.P999},
		{Operation: LatencyColdSendACK, P99Limit: limits.Cold.P99, P999Limit: limits.Cold.P999},
		{Operation: LatencyFullSync, P99Limit: limits.Sync.P99, P999Limit: limits.Sync.P999},
	}
	for index := range rows {
		rows[index].P99Count, rows[index].P999Count, rows[index].P99Samples, rows[index].P999Samples = 1_000, 1_000, 1, 1
	}
	gate := m01GateEvidence{SchemaVersion: "wukongim/chat-lifecycle-latency-gate/v1", Status: "current", EvaluatedElapsed: 24 * time.Hour, LastLatencyElapsed: 24 * time.Hour, BaselineElapsed: 10 * time.Minute, WindowStartElapsed: 24*time.Hour - 5*time.Minute, WindowEndElapsed: 24 * time.Hour, WarmupDuration: 10 * time.Minute, WindowDuration: 5 * time.Minute, MaxAge: 5 * time.Second, P99FractionDenominator: 100, P999FractionDenominator: 1_000, WindowCapacity: 64, Classes: rows}
	body, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func m01ClassMaps(t *testing.T, gate map[string]json.RawMessage) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(gate["classes"], &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func m01InsertGate(t *testing.T, body, child []byte) []byte {
	t.Helper()
	var raw, verdict map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw["verdict"], &verdict); err != nil {
		t.Fatal(err)
	}
	if child == nil {
		delete(verdict, "latency_gate")
	} else {
		verdict["latency_gate"] = child
	}
	var err error
	raw["verdict"], err = json.Marshal(verdict)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func m01ReportVersion(t *testing.T, body []byte, version string) []byte {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	var err error
	raw["schema_version"], err = json.Marshal(version)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func m01ReadBody(t *testing.T, body []byte) (Report, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return ReadReport(path)
}
