package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalChatLifecycleTimelineCommandBuildsFirstBreachAndAmplification(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		"ordinary coordinator log line",
		workerStatusCutTestLine("timeline-run", "2026-08-13T10:00:05Z", "periodic", 100, 100, 0, 0, 0),
		`{"event":"another.event","unknown":"ignored"}`,
		workerStatusCutTestLine("timeline-run", "2026-08-13T10:00:10Z", "periodic", 200, 180, 20, 0, 0),
		workerStatusCutTestLine("timeline-run", "2026-08-13T10:00:20Z", "terminal", 220, 180, 40, 2500, 40),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, strings.Join([]string{
		"observed_at_utc\tphase\tnode\tstatus",
		"2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete",
		"2026-08-13T10:00:15Z\twarmup_end\tboundary\tcomplete",
		"2026-08-13T10:00:15Z\tmeasurement_start\tboundary\tcomplete",
		"2026-08-13T10:00:16Z\tmeasurement_end\tboundary\tcomplete",
		"2026-08-13T10:00:16Z\tdrain_start\tboundary\tcomplete",
		"2026-08-13T10:00:19Z\tdrain_end\tboundary\tcomplete",
		"2026-08-13T10:00:20Z\tshutdown_start\tboundary\tcomplete",
	}, "\n")+"\n")

	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline",
		"--worker-log", workerLog,
		"--boundary-timeline", boundaries,
		"--run-id", "timeline-run",
		"--offered-rate", "20",
		"--minimum-throughput-percent", "90",
		"--output-json", outputJSON,
		"--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code = %d, stderr = %q", code, stderr.String())
	}
	body, err := os.ReadFile(outputJSON)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Schema       string `json:"schema"`
		RunID        string `json:"run_id"`
		Completeness struct {
			WorkerStatusCutsComplete bool `json:"worker_status_cuts_complete"`
			BoundaryTimelineComplete bool `json:"boundary_timeline_complete"`
			TerminalCutPresent       bool `json:"terminal_cut_present"`
			PartialWorkerLogLine     bool `json:"partial_worker_log_line"`
		} `json:"source_completeness"`
		FirstBreach struct {
			Observed          bool      `json:"observed"`
			TriggerKind       string    `json:"trigger_kind"`
			Phase             string    `json:"phase"`
			PreviousAt        time.Time `json:"previous_at"`
			CurrentAt         time.Time `json:"current_at"`
			SentDelta         uint64    `json:"sent_delta"`
			AcknowledgedDelta uint64    `json:"acknowledged_delta"`
			RetryDelta        uint64    `json:"retry_delta"`
		} `json:"first_breach"`
		Amplification struct {
			RetryAfterFirstBreachDelta  uint64 `json:"retry_after_first_breach_delta"`
			ShutdownGenerationStopDelta uint64 `json:"shutdown_generation_stop_delta"`
			ShutdownCancellationDelta   uint64 `json:"shutdown_cancellation_delta"`
			ShutdownSessionClosedDelta  uint64 `json:"shutdown_session_closed_delta"`
			CancellationSource          string `json:"cancellation_source"`
		} `json:"amplification"`
		Overlap struct {
			Compaction localTimelineOverlapEvidence `json:"compaction"`
			Snapshot   localTimelineOverlapEvidence `json:"snapshot"`
		} `json:"overlap"`
		Points []json.RawMessage `json:"points"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "wukongim/chat-lifecycle-unified-timeline/v1" || result.RunID != "timeline-run" {
		t.Fatalf("timeline identity = %q/%q", result.Schema, result.RunID)
	}
	if !result.Completeness.WorkerStatusCutsComplete || !result.Completeness.BoundaryTimelineComplete ||
		!result.Completeness.TerminalCutPresent || result.Completeness.PartialWorkerLogLine {
		t.Fatalf("source completeness = %+v", result.Completeness)
	}
	if !result.FirstBreach.Observed || result.FirstBreach.TriggerKind != "actual_offered_ratio" || result.FirstBreach.Phase != "warmup" ||
		!result.FirstBreach.PreviousAt.Equal(time.Date(2026, 8, 13, 10, 0, 5, 0, time.UTC)) ||
		!result.FirstBreach.CurrentAt.Equal(time.Date(2026, 8, 13, 10, 0, 10, 0, time.UTC)) ||
		result.FirstBreach.SentDelta != 100 || result.FirstBreach.AcknowledgedDelta != 80 || result.FirstBreach.RetryDelta != 20 {
		t.Fatalf("first breach = %+v", result.FirstBreach)
	}
	if result.Amplification.RetryAfterFirstBreachDelta != 40 ||
		result.Amplification.ShutdownGenerationStopDelta != 2500 ||
		result.Amplification.ShutdownCancellationDelta != 40 || result.Amplification.ShutdownSessionClosedDelta != 40 ||
		result.Amplification.CancellationSource != "messages.terminal_reasons.session_closed" {
		t.Fatalf("amplification = %+v", result.Amplification)
	}
	if result.Overlap.Compaction.Status != "unknown" || result.Overlap.Compaction.SourceComplete ||
		result.Overlap.Snapshot.Status != "unknown" || result.Overlap.Snapshot.SourceComplete || len(result.Points) != 10 {
		t.Fatalf("overlap/points = %+v/%d", result.Overlap, len(result.Points))
	}
	tsv, err := os.ReadFile(outputTSV)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"observed_at_utc\tphase\tsource\tkind", "2026-08-13T10:00:10Z\twarmup\tworker_status\tperiodic", "2026-08-13T10:00:20Z\tshutdown\tworker_status\tterminal"} {
		if !strings.Contains(string(tsv), want) {
			t.Fatalf("timeline TSV missing %q:\n%s", want, tsv)
		}
	}
}

func TestLocalChatLifecycleTimelineDoesNotCarryRatioAcrossPhaseBoundary(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("phase-run", "2026-08-13T10:00:09.5Z", "periodic", 100, 100, 0, 0, 0),
		// measurement_start is written after qualification but rounded to the
		// second. This still-warmup cut must remain warmup despite sorting after
		// the boundary's truncated timestamp.
		workerStatusCutTestLine("phase-run", "2026-08-13T10:00:10.1Z", "periodic", 150, 150, 0, 0, 0),
		// Qualification would look like a 0% measured interval if compared with
		// that cut. It must instead establish the measured baseline.
		workerStatusCutTestLine("phase-run", "2026-08-13T10:00:10.9Z", "qualification", 200, 150, 0, 0, 0),
		// Measured accounting subtracts the qualification SEND boundary (200),
		// leaving 122 acknowledgements for the 6.1-second, 20/s interval.
		workerStatusCutTestLine("phase-run", "2026-08-13T10:00:17Z", "periodic", 400, 322, 0, 0, 0),
		workerStatusCutTestLine("phase-run", "2026-08-13T10:00:22Z", "terminal", 400, 322, 0, 2500, 0),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, strings.Join([]string{
		"observed_at_utc\tphase\tnode\tstatus",
		"2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete",
		"2026-08-13T10:00:10Z\twarmup_end\tboundary\tcomplete",
		"2026-08-13T10:00:10Z\tmeasurement_start\tboundary\tcomplete",
		"2026-08-13T10:00:20Z\tmeasurement_end\tboundary\tcomplete",
		"2026-08-13T10:00:20Z\tdrain_start\tboundary\tcomplete",
		"2026-08-13T10:00:21Z\tdrain_end\tboundary\tcomplete",
		"2026-08-13T10:00:21Z\tshutdown_start\tboundary\tcomplete",
	}, "\n")+"\n")
	runTimelineTestCommand(t, workerLog, boundaries, "phase-run", outputJSON, outputTSV)
	var result struct {
		FirstBreach struct {
			Observed bool `json:"observed"`
		} `json:"first_breach"`
		Points []struct {
			At         time.Time `json:"observed_at_utc"`
			Phase      string    `json:"phase"`
			Source     string    `json:"source"`
			RetryDelta uint64    `json:"retry_delta"`
		} `json:"points"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if result.FirstBreach.Observed {
		t.Fatalf("cross-phase interval produced first breach: %+v", result.FirstBreach)
	}
	for _, point := range result.Points {
		if point.Source == "worker_status" && point.At.Equal(time.Date(2026, 8, 13, 10, 0, 10, 100_000_000, time.UTC)) &&
			point.Phase != "warmup" {
			t.Fatalf("same-second warmup point = %+v", point)
		}
		if point.Source == "worker_status" && point.At.Equal(time.Date(2026, 8, 13, 10, 0, 10, 900_000_000, time.UTC)) &&
			(point.Phase != "measured" || point.RetryDelta != 0) {
			t.Fatalf("first measured baseline point = %+v", point)
		}
	}
}

func TestLocalChatLifecycleTimelineTriggersOnTerminalAndCorrectnessGrowth(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
	}{
		{name: "terminal", line: workerStatusTerminalFailureCutTestLine("product-run", "2026-08-13T10:00:10Z", 200, 200)},
		{name: "correctness", line: strings.Replace(workerStatusCutTestLine("product-run", "2026-08-13T10:00:10Z", "periodic", 200, 200, 0, 0, 0), `"losses":0`, `"losses":1`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			workerLog := filepath.Join(dir, "coordinator.log")
			boundaries := filepath.Join(dir, "timeline.tsv")
			outputJSON := filepath.Join(dir, "unified.json")
			outputTSV := filepath.Join(dir, "unified.tsv")
			writeTimelineTestFile(t, workerLog, strings.Join([]string{
				workerStatusCutTestLine("product-run", "2026-08-13T10:00:05Z", "periodic", 100, 100, 0, 0, 0),
				test.line,
			}, "\n")+"\n")
			writeTimelineTestFile(t, boundaries, "observed_at_utc\tphase\tnode\tstatus\n2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete\n")
			runTimelineTestCommand(t, workerLog, boundaries, "product-run", outputJSON, outputTSV)
			var result struct {
				FirstBreach struct {
					Observed                    bool   `json:"observed"`
					TriggerKind                 string `json:"trigger_kind"`
					TerminalProductFailureDelta uint64 `json:"terminal_product_failure_delta"`
					CorrectnessFailureDelta     uint64 `json:"correctness_failure_delta"`
				} `json:"first_breach"`
			}
			readTimelineTestJSON(t, outputJSON, &result)
			if !result.FirstBreach.Observed || result.FirstBreach.TriggerKind != "terminal_product_failure" {
				t.Fatalf("product first breach = %+v", result.FirstBreach)
			}
			if test.name == "terminal" && result.FirstBreach.TerminalProductFailureDelta != 1 {
				t.Fatalf("terminal first breach = %+v", result.FirstBreach)
			}
			if test.name == "correctness" && result.FirstBreach.CorrectnessFailureDelta != 1 {
				t.Fatalf("correctness first breach = %+v", result.FirstBreach)
			}
		})
	}
}

func TestLocalChatLifecycleTimelineCarriesQualificationProductFailureWithoutMeasuredTrigger(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	qualification := strings.Replace(
		workerStatusTerminalFailureCutTestLine("qualification-failure-run", "2026-08-13T10:00:02.1Z", 200, 200),
		`"cut":"periodic"`, `"cut":"qualification"`, 1,
	)
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("qualification-failure-run", "2026-08-13T10:00:01Z", "periodic", 100, 100, 0, 0, 0),
		qualification,
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, strings.Join([]string{
		"observed_at_utc\tphase\tnode\tstatus",
		"2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete",
		"2026-08-13T10:00:02Z\twarmup_end\tboundary\tcomplete",
		"2026-08-13T10:00:02Z\tmeasurement_start\tboundary\tcomplete",
	}, "\n")+"\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--run-id", "qualification-failure-run", "--offered-rate", "100", "--minimum-throughput-percent", "90",
		"--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		First struct {
			Observed      bool   `json:"observed"`
			TriggerKind   string `json:"trigger_kind"`
			Phase         string `json:"phase"`
			TerminalDelta uint64 `json:"terminal_product_failure_delta"`
		} `json:"first_breach"`
		Measured struct {
			Observed bool `json:"observed"`
		} `json:"measured_first_breach"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if !result.First.Observed || result.First.TriggerKind != "terminal_product_failure" ||
		result.First.Phase != "warmup_to_measured" || result.First.TerminalDelta != 1 || result.Measured.Observed {
		t.Fatalf("qualification product breach = %+v", result)
	}
}

func TestLocalChatLifecycleTimelineUsesOfferedRateInsteadOfAcknowledgedPerSent(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("offered-run", "2026-08-13T10:00:05Z", "periodic", 400, 400, 0, 0, 0),
		// Every dispatched SEND is acknowledged, but only 400 of the offered
		// 1,000 messages are produced during this one-second interval.
		workerStatusCutTestLine("offered-run", "2026-08-13T10:00:06Z", "periodic", 800, 800, 0, 0, 0),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, "observed_at_utc\tphase\tnode\tstatus\n2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--run-id", "offered-run", "--offered-rate", "1000", "--minimum-throughput-percent", "90",
		"--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		OfferedRate uint64 `json:"offered_rate_per_second"`
		FirstBreach struct {
			Observed             bool    `json:"observed"`
			TriggerKind          string  `json:"trigger_kind"`
			AcknowledgedPercent  float64 `json:"acknowledged_percent"`
			IntervalSeconds      float64 `json:"interval_seconds"`
			ExpectedOffered      float64 `json:"expected_offered"`
			ActualOfferedPercent float64 `json:"actual_offered_percent"`
		} `json:"first_breach"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if result.OfferedRate != 1000 || !result.FirstBreach.Observed || result.FirstBreach.TriggerKind != "actual_offered_ratio" ||
		result.FirstBreach.AcknowledgedPercent != 100 || result.FirstBreach.ExpectedOffered != 1000 ||
		result.FirstBreach.IntervalSeconds != 1 || result.FirstBreach.ActualOfferedPercent != 40 {
		t.Fatalf("actual/offered first breach = %+v", result.FirstBreach)
	}
}

func TestLocalChatLifecycleTimelineSeparatesWarmupAndMeasuredFirstBreach(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("measured-breach-run", "2026-08-13T10:00:01Z", "periodic", 400, 400, 0, 0, 0),
		workerStatusCutTestLine("measured-breach-run", "2026-08-13T10:00:02Z", "periodic", 800, 800, 0, 0, 0),
		workerStatusCutTestLine("measured-breach-run", "2026-08-13T10:00:03.1Z", "qualification", 1200, 1200, 0, 0, 0),
		workerStatusCutTestLine("measured-breach-run", "2026-08-13T10:00:04.1Z", "periodic", 1600, 1600, 0, 0, 0),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, strings.Join([]string{
		"observed_at_utc\tphase\tnode\tstatus",
		"2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete",
		"2026-08-13T10:00:03Z\twarmup_end\tboundary\tcomplete",
		"2026-08-13T10:00:03Z\tmeasurement_start\tboundary\tcomplete",
		"2026-08-13T10:00:05Z\tmeasurement_end\tboundary\tcomplete",
	}, "\n")+"\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--run-id", "measured-breach-run", "--offered-rate", "1000", "--minimum-throughput-percent", "90",
		"--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		First struct {
			Observed  bool      `json:"observed"`
			Phase     string    `json:"phase"`
			CurrentAt time.Time `json:"current_at"`
		} `json:"first_breach"`
		Measured struct {
			Observed             bool      `json:"observed"`
			TriggerKind          string    `json:"trigger_kind"`
			Phase                string    `json:"phase"`
			PreviousAt           time.Time `json:"previous_at"`
			CurrentAt            time.Time `json:"current_at"`
			ActualOfferedPercent float64   `json:"actual_offered_percent"`
		} `json:"measured_first_breach"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if !result.First.Observed || result.First.Phase != "warmup" ||
		!result.First.CurrentAt.Equal(time.Date(2026, 8, 13, 10, 0, 2, 0, time.UTC)) {
		t.Fatalf("global first breach = %+v", result.First)
	}
	if !result.Measured.Observed || result.Measured.TriggerKind != "actual_offered_ratio" || result.Measured.Phase != "measured" ||
		!result.Measured.PreviousAt.Equal(time.Date(2026, 8, 13, 10, 0, 3, 100_000_000, time.UTC)) ||
		!result.Measured.CurrentAt.Equal(time.Date(2026, 8, 13, 10, 0, 4, 100_000_000, time.UTC)) ||
		result.Measured.ActualOfferedPercent != 40 {
		t.Fatalf("measured first breach = %+v", result.Measured)
	}
}

func TestLocalChatLifecycleMeasuredTimelineSubtractsQualificationSentBoundary(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("late-warmup-run", "2026-08-13T10:00:01Z", "qualification", 1000, 800, 0, 0, 0),
		// Raw SENDACK delta is 950, but 200 of those acknowledgements close the
		// warmup deficit. The measured population is 1,750 - 1,000 = 750.
		workerStatusCutTestLine("late-warmup-run", "2026-08-13T10:00:02Z", "periodic", 2000, 1750, 0, 0, 0),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, strings.Join([]string{
		"observed_at_utc\tphase\tnode\tstatus",
		"2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete",
		"2026-08-13T10:00:01Z\twarmup_end\tboundary\tcomplete",
		"2026-08-13T10:00:01Z\tmeasurement_start\tboundary\tcomplete",
	}, "\n")+"\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--run-id", "late-warmup-run", "--offered-rate", "1000", "--minimum-throughput-percent", "90",
		"--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		QualificationCutPresent   bool   `json:"qualification_cut_present"`
		QualificationSentBoundary uint64 `json:"qualification_sent_boundary"`
		Measured                  struct {
			Observed             bool    `json:"observed"`
			AcknowledgedDelta    uint64  `json:"acknowledged_delta"`
			ActualOfferedPercent float64 `json:"actual_offered_percent"`
		} `json:"measured_first_breach"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if !result.QualificationCutPresent || result.QualificationSentBoundary != 1000 || !result.Measured.Observed ||
		result.Measured.AcknowledgedDelta != 750 || result.Measured.ActualOfferedPercent != 75 {
		t.Fatalf("late-warmup measured accounting = %+v", result)
	}
}

func TestLocalChatLifecycleTimelineKeepsRuntimeSessionClosedInActivePhase(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("session-close-run", "2026-08-13T10:00:05Z", "periodic", 100, 100, 0, 0, 0),
		workerStatusCutTestLine("session-close-run", "2026-08-13T10:00:10Z", "periodic", 200, 200, 0, 0, 1),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, "observed_at_utc\tphase\tnode\tstatus\n2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete\n")
	runTimelineTestCommand(t, workerLog, boundaries, "session-close-run", outputJSON, outputTSV)
	var result struct {
		FirstBreach struct {
			Observed bool `json:"observed"`
		} `json:"first_breach"`
		Amplification struct {
			ShutdownCancellationDelta uint64 `json:"shutdown_cancellation_delta"`
		} `json:"amplification"`
		Points []struct {
			At     time.Time `json:"observed_at_utc"`
			Phase  string    `json:"phase"`
			Source string    `json:"source"`
		} `json:"points"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if result.FirstBreach.Observed || result.Amplification.ShutdownCancellationDelta != 0 {
		t.Fatalf("runtime session-close classification = breach %+v amplification %+v", result.FirstBreach, result.Amplification)
	}
	for _, point := range result.Points {
		if point.Source == "worker_status" && point.At.Equal(time.Date(2026, 8, 13, 10, 0, 10, 0, time.UTC)) && point.Phase != "warmup" {
			t.Fatalf("runtime session-close point = %+v", point)
		}
	}
}

func TestLocalChatLifecycleTimelineCarriesTerminalFailureAcrossShutdownWithoutRatioTrigger(t *testing.T) {
	for _, test := range []struct {
		name          string
		terminalLine  string
		wantTriggered bool
	}{
		{
			name: "terminal product failure",
			terminalLine: strings.Replace(
				workerStatusTerminalFailureCutTestLine("terminal-run", "2026-08-13T10:00:10Z", 200, 100),
				`"generation_stop":0`, `"generation_stop":2500`, 1),
			wantTriggered: true,
		},
		{
			name:         "cleanup underdelivery only",
			terminalLine: workerStatusCutTestLine("terminal-run", "2026-08-13T10:00:10Z", "terminal", 200, 100, 0, 2500, 0),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			workerLog := filepath.Join(dir, "coordinator.log")
			boundaries := filepath.Join(dir, "timeline.tsv")
			outputJSON := filepath.Join(dir, "unified.json")
			outputTSV := filepath.Join(dir, "unified.tsv")
			writeTimelineTestFile(t, workerLog, strings.Join([]string{
				workerStatusCutTestLine("terminal-run", "2026-08-13T10:00:05Z", "periodic", 100, 100, 0, 0, 0),
				test.terminalLine,
			}, "\n")+"\n")
			writeTimelineTestFile(t, boundaries, "observed_at_utc\tphase\tnode\tstatus\n2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete\n2026-08-13T10:00:11Z\tshutdown_start\tboundary\tcomplete\n")
			runTimelineTestCommand(t, workerLog, boundaries, "terminal-run", outputJSON, outputTSV)
			var result struct {
				FirstBreach struct {
					Observed      bool   `json:"observed"`
					TriggerKind   string `json:"trigger_kind"`
					Phase         string `json:"phase"`
					SentDelta     uint64 `json:"sent_delta"`
					AckDelta      uint64 `json:"acknowledged_delta"`
					TerminalDelta uint64 `json:"terminal_product_failure_delta"`
				} `json:"first_breach"`
			}
			readTimelineTestJSON(t, outputJSON, &result)
			if result.FirstBreach.Observed != test.wantTriggered {
				t.Fatalf("shutdown first breach = %+v", result.FirstBreach)
			}
			if test.wantTriggered && (result.FirstBreach.TriggerKind != "terminal_product_failure" ||
				result.FirstBreach.Phase != "warmup_to_shutdown" || result.FirstBreach.TerminalDelta != 1) {
				t.Fatalf("terminal product bracket = %+v", result.FirstBreach)
			}
		})
	}
}

func TestLocalChatLifecycleTimelineCommandFailsClosedOnMalformedMatchingEvent(t *testing.T) {
	valid := workerStatusCutTestLine("strict-run", "2026-08-13T10:00:05Z", "periodic", 100, 100, 0, 0, 0)
	for _, test := range []struct {
		name string
		line string
	}{
		{name: "unknown field", line: valid[:len(valid)-1] + `,"unexpected":1}`},
		{name: "broken newline-terminated tail", line: valid[:len(valid)-17]},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			workerLog := filepath.Join(dir, "coordinator.log")
			boundaries := filepath.Join(dir, "timeline.tsv")
			outputJSON := filepath.Join(dir, "unified.json")
			outputTSV := filepath.Join(dir, "unified.tsv")
			writeTimelineTestFile(t, workerLog, test.line+"\n")
			writeTimelineTestFile(t, boundaries, "observed_at_utc\tphase\tnode\tstatus\n2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete\n")
			var stderr bytes.Buffer
			code := executeRoot([]string{
				"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
				"--run-id", "strict-run", "--offered-rate", "20", "--minimum-throughput-percent", "90", "--output-json", outputJSON, "--output-tsv", outputTSV,
			}, &stderr)
			if code == 0 || !strings.Contains(stderr.String(), "worker status evidence") {
				t.Fatalf("malformed matching event code/stderr = %d/%q", code, stderr.String())
			}
			if _, err := os.Stat(outputJSON); !os.IsNotExist(err) {
				t.Fatalf("fail-closed JSON output stat error = %v", err)
			}
		})
	}
}

func TestLocalChatLifecycleTimelineCommandFailsClosedOnMalformedTargetRunTail(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("target-tail-run", "2026-08-13T10:00:05Z", "periodic", 100, 100, 0, 0, 0),
		workerStatusCutTestLine("target-tail-run", "2026-08-13T10:00:10Z", "terminal", 100, 100, 0, 0, 0),
		`{"run_id":"target-tail-run","truncated":`,
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, "observed_at_utc\tphase\tnode\tstatus\n2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete\n")

	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--run-id", "target-tail-run", "--offered-rate", "20", "--minimum-throughput-percent", "90", "--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "worker status evidence") {
		t.Fatalf("malformed target-run tail code/stderr = %d/%q", code, stderr.String())
	}
	if _, err := os.Stat(outputJSON); !os.IsNotExist(err) {
		t.Fatalf("fail-closed JSON output stat error = %v", err)
	}
}

func TestDecodeLocalTimelineWorkerCutIgnoresUnrelatedMalformedLine(t *testing.T) {
	_, matched, err := decodeLocalTimelineWorkerCut([]byte(`{"component":"unrelated","truncated":`), "target-tail-run")
	if err != nil || matched {
		t.Fatalf("unrelated malformed line matched/error = %t/%v", matched, err)
	}
}

func TestLocalChatLifecycleTimelineLabelsCompactionAndSnapshotBrackets(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	boundaries := filepath.Join(dir, "timeline.tsv")
	overlap := filepath.Join(dir, "storage-overlap.tsv")
	outputJSON := filepath.Join(dir, "unified.json")
	outputTSV := filepath.Join(dir, "unified.tsv")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("overlap-run", "2026-08-13T10:00:04Z", "periodic", 100, 100, 0, 0, 0),
		workerStatusCutTestLine("overlap-run", "2026-08-13T10:00:08Z", "qualification", 200, 200, 0, 0, 0),
		workerStatusCutTestLine("overlap-run", "2026-08-13T10:00:12Z", "terminal", 300, 300, 0, 2500, 0),
	}, "\n")+"\n")
	writeTimelineTestFile(t, boundaries, strings.Join([]string{
		"observed_at_utc\tphase\tnode\tstatus",
		"2026-08-13T10:00:00Z\twarmup_start\tboundary\tcomplete",
		"2026-08-13T10:00:06Z\twarmup_end\tboundary\tcomplete",
		"2026-08-13T10:00:06Z\tmeasurement_start\tboundary\tcomplete",
		"2026-08-13T10:00:10Z\tmeasurement_end\tboundary\tcomplete",
		"2026-08-13T10:00:10Z\tdrain_start\tboundary\tcomplete",
		"2026-08-13T10:00:12Z\tdrain_end\tboundary\tcomplete",
		"2026-08-13T10:00:12Z\tshutdown_start\tboundary\tcomplete",
	}, "\n")+"\n")
	beforeNode1Identity, beforeNode1Inventory := writeLocalOverlapInventoryTestFile(t, dir, "before", "node-1", "slot-1/chunk-000000\t10\n")
	beforeNode2Identity, beforeNode2Inventory := writeLocalOverlapInventoryTestFile(t, dir, "before", "node-2", "")
	beforeNode3Identity, beforeNode3Inventory := writeLocalOverlapInventoryTestFile(t, dir, "before", "node-3", "")
	sampleNode1Identity, sampleNode1Inventory := writeLocalOverlapInventoryTestFile(t, dir, "sample-1", "node-1", "slot-1/chunk-000000\t10\n")
	sampleNode2Identity, sampleNode2Inventory := writeLocalOverlapInventoryTestFile(t, dir, "sample-1", "node-2", "slot-2/chunk-000000\t20\n")
	sampleNode3Identity, sampleNode3Inventory := writeLocalOverlapInventoryTestFile(t, dir, "sample-1", "node-3", "")
	afterNode1Identity, afterNode1Inventory := writeLocalOverlapInventoryTestFile(t, dir, "after", "node-1", "slot-1/chunk-000000\t10\n")
	afterNode2Identity, afterNode2Inventory := writeLocalOverlapInventoryTestFile(t, dir, "after", "node-2", "slot-2/chunk-000000\t20\n")
	afterNode3Identity, afterNode3Inventory := writeLocalOverlapInventoryTestFile(t, dir, "after", "node-3", "")
	overlapBody := strings.Join([]string{
		strings.Join(localStorageOverlapHeader, "\t"),
		"2026-08-13T10:00:06Z\toverlap-run\tbefore\tnode-1\tcomplete\t1\t0\t1\t10\t" + beforeNode1Identity + "\t" + beforeNode1Inventory,
		"2026-08-13T10:00:06Z\toverlap-run\tbefore\tnode-2\tcomplete\t0\t0\t0\t0\t" + beforeNode2Identity + "\t" + beforeNode2Inventory,
		"2026-08-13T10:00:06Z\toverlap-run\tbefore\tnode-3\tcomplete\t0\t0\t0\t0\t" + beforeNode3Identity + "\t" + beforeNode3Inventory,
		"2026-08-13T10:00:08Z\toverlap-run\tsample-1\tnode-1\tcomplete\t2\t0\t1\t10\t" + sampleNode1Identity + "\t" + sampleNode1Inventory,
		"2026-08-13T10:00:08Z\toverlap-run\tsample-1\tnode-2\tcomplete\t0\t0\t1\t20\t" + sampleNode2Identity + "\t" + sampleNode2Inventory,
		"2026-08-13T10:00:08Z\toverlap-run\tsample-1\tnode-3\tcomplete\t0\t0\t0\t0\t" + sampleNode3Identity + "\t" + sampleNode3Inventory,
		"2026-08-13T10:00:12Z\toverlap-run\tafter\tnode-1\tcomplete\t2\t1\t1\t10\t" + afterNode1Identity + "\t" + afterNode1Inventory,
		"2026-08-13T10:00:12Z\toverlap-run\tafter\tnode-2\tcomplete\t0\t0\t1\t20\t" + afterNode2Identity + "\t" + afterNode2Inventory,
		"2026-08-13T10:00:12Z\toverlap-run\tafter\tnode-3\tcomplete\t0\t0\t0\t0\t" + afterNode3Identity + "\t" + afterNode3Inventory,
	}, "\n") + "\n"
	writeTimelineTestFile(t, overlap, overlapBody)
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--storage-overlap", overlap, "--run-id", "overlap-run", "--offered-rate", "20",
		"--minimum-throughput-percent", "90", "--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		Completeness struct {
			Storage bool `json:"storage_overlap_complete"`
		} `json:"source_completeness"`
		Overlap struct {
			Compaction localTimelineOverlapEvidence `json:"compaction"`
			Snapshot   localTimelineOverlapEvidence `json:"snapshot"`
		} `json:"overlap"`
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if !result.Completeness.Storage || result.Overlap.Compaction.Status != "observed" ||
		!result.Overlap.Compaction.SourceComplete || len(result.Overlap.Compaction.Windows) != 2 ||
		result.Overlap.Snapshot.Status != "observed" || !result.Overlap.Snapshot.SourceComplete ||
		len(result.Overlap.Snapshot.Windows) != 1 {
		t.Fatalf("storage overlap = %+v/%+v completeness=%v", result.Overlap.Compaction, result.Overlap.Snapshot, result.Completeness)
	}
	if result.Overlap.Compaction.Windows[0].Phase != "measured" ||
		result.Overlap.Compaction.Windows[1].Phase != "measured_to_drain_to_shutdown" ||
		result.Overlap.Snapshot.Windows[0].Phase != "measured" {
		t.Fatalf("overlap phases = %+v/%+v", result.Overlap.Compaction.Windows, result.Overlap.Snapshot.Windows)
	}
	tsv, err := os.ReadFile(outputTSV)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tsv), "storage_overlap\tcompaction") ||
		!strings.Contains(string(tsv), "storage_overlap\tsnapshot") ||
		!strings.Contains(string(tsv), "2026-08-13T10:00:06Z\tnode-1") {
		t.Fatalf("overlap timeline TSV:\n%s", tsv)
	}
	resetBody := strings.Replace(overlapBody,
		"2026-08-13T10:00:12Z\toverlap-run\tafter\tnode-1\tcomplete\t2\t1",
		"2026-08-13T10:00:12Z\toverlap-run\tafter\tnode-1\tcomplete\t1\t1", 1)
	writeTimelineTestFile(t, overlap, resetBody)
	stderr.Reset()
	code = executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--storage-overlap", overlap, "--run-id", "overlap-run", "--offered-rate", "20",
		"--minimum-throughput-percent", "90", "--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("reset timeline command code/stderr = %d/%q", code, stderr.String())
	}
	readTimelineTestJSON(t, outputJSON, &result)
	if result.Completeness.Storage || result.Overlap.Compaction.SourceComplete || result.Overlap.Compaction.Status != "observed" {
		t.Fatalf("counter reset was not propagated = %+v/%+v", result.Completeness, result.Overlap.Compaction)
	}
}

func TestLocalChatLifecycleStorageOverlapFailsClosedOnMissingOrResetEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage-overlap.tsv")
	identities := make(map[string]string)
	inventories := make(map[string]string)
	for _, sample := range []string{"before", "after"} {
		for _, node := range []string{"node-1", "node-2", "node-3"} {
			key := sample + "/" + node
			identities[key], inventories[key] = writeLocalOverlapInventoryTestFile(t, dir, sample, node, "")
		}
	}
	writeTimelineTestFile(t, path, strings.Join([]string{
		strings.Join(localStorageOverlapHeader, "\t"),
		"2026-08-13T10:00:00Z\treset-run\tbefore\tnode-1\tcomplete\t2\t0\t0\t0\t" + identities["before/node-1"] + "\t" + inventories["before/node-1"],
		"2026-08-13T10:00:00Z\treset-run\tbefore\tnode-2\tcomplete\t0\t0\t0\t0\t" + identities["before/node-2"] + "\t" + inventories["before/node-2"],
		"2026-08-13T10:00:00Z\treset-run\tbefore\tnode-3\tcomplete\t0\t0\t0\t0\t" + identities["before/node-3"] + "\t" + inventories["before/node-3"],
		"2026-08-13T10:00:01Z\treset-run\tafter\tnode-1\tcomplete\t1\t0\t0\t0\t" + identities["after/node-1"] + "\t" + inventories["after/node-1"],
		"2026-08-13T10:00:01Z\treset-run\tafter\tnode-2\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable",
		"2026-08-13T10:00:01Z\treset-run\tafter\tnode-3\tcomplete\t0\t0\t0\t0\t" + identities["after/node-3"] + "\t" + inventories["after/node-3"],
	}, "\n")+"\n")
	rows, complete, err := readLocalTimelineStorageOverlap(path, "reset-run")
	if err != nil || complete {
		t.Fatalf("reader complete/error = %v/%v", complete, err)
	}
	compaction, snapshot, _ := analyzeLocalTimelineStorageOverlap(rows, complete, localTimelineMarks{})
	if compaction.Status != "unknown" || compaction.SourceComplete || snapshot.Status != "unknown" || snapshot.SourceComplete {
		t.Fatalf("reset/missing overlap = %+v/%+v", compaction, snapshot)
	}
}

func TestLocalChatLifecycleStorageOverlapRejectsSubstitutedOrSymlinkedInventory(t *testing.T) {
	dir := t.TempDir()
	body := []byte("slot-1/chunk-000001\t7\n")
	identity := fmt.Sprintf("%x", sha256.Sum256(body))
	row := localTimelineStorageOverlapSample{
		Sample: "before", Node: "node-1", SnapshotFiles: 1, SnapshotBytes: 7,
		SnapshotIdentity: identity, SnapshotInventory: "snapshot-inventory/substituted.tsv",
	}
	inventoryDirectory := filepath.Join(dir, "snapshot-inventory")
	if err := os.Mkdir(inventoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inventoryDirectory, "substituted.tsv"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalSnapshotInventory(dir, row); err == nil {
		t.Fatal("inventory path not bound to sample/node was accepted")
	}

	realInventoryDirectory := filepath.Join(dir, "real-inventory")
	if err := os.Mkdir(realInventoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realInventoryDirectory, "before-node-1.tsv"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(inventoryDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realInventoryDirectory, inventoryDirectory); err != nil {
		t.Fatal(err)
	}
	row.SnapshotInventory = "snapshot-inventory/before-node-1.tsv"
	if err := validateLocalSnapshotInventory(dir, row); err == nil {
		t.Fatal("symlinked inventory directory was accepted")
	}
}

func writeLocalOverlapInventoryTestFile(t *testing.T, directory, sample, node, body string) (string, string) {
	t.Helper()
	inventoryDirectory := filepath.Join(directory, "snapshot-inventory")
	if err := os.MkdirAll(inventoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := sample + "-" + node + ".tsv"
	path := filepath.Join(inventoryDirectory, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(body))), filepath.Join("snapshot-inventory", name)
}

func TestLocalChatLifecycleCutQueryKeepsPartialLineAtCursor(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	first := workerStatusCutTestLine("query-run", "2026-08-13T10:00:05+08:00", "periodic", 100, 100, 0, 0, 0)
	second := workerStatusCutTestLine("query-run", "2026-08-13T10:00:10+08:00", "periodic", 200, 150, 50, 0, 0)
	prefix := "unrelated line\n" + first + "\n"
	writeTimelineTestFile(t, workerLog, prefix+second[:len(second)/2])
	firstOutput := filepath.Join(dir, "query-first.json")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "query-run", "--cursor", "0", "--offered-rate", "20", "--output", firstOutput,
	}, &stderr)
	if code != 0 {
		t.Fatalf("first cut query code/stderr = %d/%q", code, stderr.String())
	}
	var firstResult struct {
		Schema     string `json:"schema"`
		NextCursor int64  `json:"next_cursor"`
		Partial    bool   `json:"partial_line"`
		Latest     *struct {
			At       time.Time `json:"at"`
			Messages struct {
				Sent             uint64 `json:"sent"`
				SendAcknowledged uint64 `json:"send_acknowledged"`
			} `json:"messages"`
		} `json:"latest_cut"`
	}
	readTimelineTestJSON(t, firstOutput, &firstResult)
	if firstResult.Schema != "wukongim/chat-lifecycle-worker-cut-query/v1" || !firstResult.Partial ||
		firstResult.NextCursor != int64(len(prefix)) || firstResult.Latest == nil || firstResult.Latest.Messages.Sent != 100 {
		t.Fatalf("first cut query = %+v", firstResult)
	}
	file, err := os.OpenFile(workerLog, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(second[len(second)/2:] + "\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	secondOutput := filepath.Join(dir, "query-second.json")
	stderr.Reset()
	code = executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "query-run",
		"--cursor", fmt.Sprintf("%d", firstResult.NextCursor), "--previous-query", firstOutput, "--offered-rate", "20", "--output", secondOutput,
	}, &stderr)
	if code != 0 {
		t.Fatalf("second cut query code/stderr = %d/%q", code, stderr.String())
	}
	var secondResult struct {
		NextCursor         int64 `json:"next_cursor"`
		Partial            bool  `json:"partial_line"`
		TerminalCutPresent bool  `json:"terminal_cut_present"`
		Transition         struct {
			Available         bool   `json:"available"`
			SentDelta         uint64 `json:"sent_delta"`
			AcknowledgedDelta uint64 `json:"acknowledged_delta"`
			RetryDelta        uint64 `json:"retry_delta"`
		} `json:"transition"`
		Previous *struct {
			At       time.Time `json:"at"`
			Messages struct {
				Sent uint64 `json:"sent"`
			} `json:"messages"`
		} `json:"previous_cut"`
		Latest *struct {
			At       time.Time `json:"at"`
			Messages struct {
				Sent             uint64 `json:"sent"`
				SendAcknowledged uint64 `json:"send_acknowledged"`
			} `json:"messages"`
		} `json:"latest_cut"`
	}
	readTimelineTestJSON(t, secondOutput, &secondResult)
	if secondResult.Partial || secondResult.NextCursor != int64(len(prefix)+len(second)+1) ||
		secondResult.TerminalCutPresent || secondResult.Previous == nil || secondResult.Previous.Messages.Sent != 100 ||
		secondResult.Latest == nil || secondResult.Latest.Messages.Sent != 200 ||
		!secondResult.Transition.Available || secondResult.Transition.SentDelta != 100 ||
		secondResult.Transition.AcknowledgedDelta != 50 || secondResult.Transition.RetryDelta != 50 ||
		!secondResult.Latest.At.Equal(time.Date(2026, 8, 13, 2, 0, 10, 0, time.UTC)) {
		t.Fatalf("second cut query = %+v", secondResult)
	}
}

func TestLocalChatLifecycleCutQueryMarksOnlyPostQualificationActualOfferedBreachEligible(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	output := filepath.Join(dir, "query.json")
	writeTimelineTestFile(t, workerLog, strings.Join([]string{
		workerStatusCutTestLine("measured-query", "2026-08-13T10:00:00Z", "periodic", 400, 400, 0, 0, 0),
		workerStatusCutTestLine("measured-query", "2026-08-13T10:00:01Z", "qualification", 800, 800, 0, 0, 0),
		workerStatusCutTestLine("measured-query", "2026-08-13T10:00:02Z", "periodic", 1200, 1200, 0, 0, 0),
	}, "\n")+"\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "measured-query",
		"--cursor", "0", "--offered-rate", "1000", "--minimum-throughput-percent", "90", "--output", output,
	}, &stderr)
	if code != 0 {
		t.Fatalf("cut query code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		OfferedRate   uint64 `json:"offered_rate_per_second"`
		Minimum       uint64 `json:"minimum_throughput_percent"`
		Qualification bool   `json:"qualification_cut_present"`
		Transitions   []struct {
			MeasurementEligible  bool    `json:"measurement_eligible"`
			TriggerKind          string  `json:"trigger_kind"`
			ExpectedOffered      float64 `json:"expected_offered"`
			ActualOfferedPercent float64 `json:"actual_offered_percent"`
		} `json:"transitions"`
	}
	readTimelineTestJSON(t, output, &result)
	if result.OfferedRate != 1000 || result.Minimum != 90 || !result.Qualification || len(result.Transitions) != 2 || result.Transitions[0].MeasurementEligible ||
		result.Transitions[1].TriggerKind != "actual_offered_ratio" || !result.Transitions[1].MeasurementEligible ||
		result.Transitions[1].ExpectedOffered != 1000 || result.Transitions[1].ActualOfferedPercent != 40 {
		t.Fatalf("measured cut-query transitions = %+v", result)
	}
}

func TestLocalChatLifecycleCutQuerySubtractsQualificationSentBoundary(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	firstOutput := filepath.Join(dir, "query-first.json")
	writeTimelineTestFile(t, workerLog, workerStatusCutTestLine("late-warmup-query", "2026-08-13T10:00:01Z", "qualification", 1000, 800, 0, 0, 0)+"\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "late-warmup-query",
		"--cursor", "0", "--offered-rate", "1000", "--minimum-throughput-percent", "90", "--output", firstOutput,
	}, &stderr)
	if code != 0 {
		t.Fatalf("first cut query code/stderr = %d/%q", code, stderr.String())
	}
	var first struct {
		NextCursor                int64  `json:"next_cursor"`
		QualificationCutPresent   bool   `json:"qualification_cut_present"`
		QualificationSentBoundary uint64 `json:"qualification_sent_boundary"`
	}
	readTimelineTestJSON(t, firstOutput, &first)
	if !first.QualificationCutPresent || first.QualificationSentBoundary != 1000 {
		t.Fatalf("first qualification query = %+v", first)
	}
	file, err := os.OpenFile(workerLog, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(workerStatusCutTestLine("late-warmup-query", "2026-08-13T10:00:02Z", "periodic", 2000, 1750, 0, 0, 0) + "\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "query-second.json")
	stderr.Reset()
	code = executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "late-warmup-query",
		"--cursor", fmt.Sprintf("%d", first.NextCursor), "--previous-query", firstOutput,
		"--offered-rate", "1000", "--minimum-throughput-percent", "90", "--output", output,
	}, &stderr)
	if code != 0 {
		t.Fatalf("second cut query code/stderr = %d/%q", code, stderr.String())
	}
	var result struct {
		QualificationSentBoundary uint64 `json:"qualification_sent_boundary"`
		Transition                struct {
			MeasurementEligible  bool    `json:"measurement_eligible"`
			TriggerKind          string  `json:"trigger_kind"`
			AcknowledgedDelta    uint64  `json:"acknowledged_delta"`
			ActualOfferedPercent float64 `json:"actual_offered_percent"`
		} `json:"transition"`
	}
	readTimelineTestJSON(t, output, &result)
	if result.QualificationSentBoundary != 1000 || !result.Transition.MeasurementEligible ||
		result.Transition.TriggerKind != "actual_offered_ratio" || result.Transition.AcknowledgedDelta != 750 ||
		result.Transition.ActualOfferedPercent != 75 {
		t.Fatalf("late-warmup query accounting = %+v", result)
	}
}

func TestLocalChatLifecycleCutQueryRejectsChangedThresholdContract(t *testing.T) {
	dir := t.TempDir()
	workerLog := filepath.Join(dir, "coordinator.log")
	previousOutput := filepath.Join(dir, "previous.json")
	writeTimelineTestFile(t, workerLog, workerStatusCutTestLine("contract-run", "2026-08-13T10:00:01Z", "periodic", 100, 100, 0, 0, 0)+"\n")
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "contract-run",
		"--cursor", "0", "--offered-rate", "1000", "--minimum-throughput-percent", "90", "--output", previousOutput,
	}, &stderr)
	if code != 0 {
		t.Fatalf("initial cut query code/stderr = %d/%q", code, stderr.String())
	}
	var prior struct {
		NextCursor int64 `json:"next_cursor"`
	}
	readTimelineTestJSON(t, previousOutput, &prior)
	stderr.Reset()
	code = executeRoot([]string{
		"report", "chat-lifecycle-cut-query", "--worker-log", workerLog, "--run-id", "contract-run",
		"--cursor", fmt.Sprintf("%d", prior.NextCursor), "--previous-query", previousOutput,
		"--offered-rate", "999", "--minimum-throughput-percent", "90", "--output", filepath.Join(dir, "changed.json"),
	}, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "previous worker cut query") {
		t.Fatalf("changed threshold contract code/stderr = %d/%q", code, stderr.String())
	}
}

func runTimelineTestCommand(t *testing.T, workerLog, boundaries, runID, outputJSON, outputTSV string) {
	t.Helper()
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"report", "chat-lifecycle-timeline", "--worker-log", workerLog, "--boundary-timeline", boundaries,
		"--run-id", runID, "--offered-rate", "20", "--minimum-throughput-percent", "90", "--output-json", outputJSON, "--output-tsv", outputTSV,
	}, &stderr)
	if code != 0 {
		t.Fatalf("timeline command code/stderr = %d/%q", code, stderr.String())
	}
}

func workerStatusTerminalFailureCutTestLine(runID, at string, sent, acknowledged uint64) string {
	line := workerStatusCutTestLine(runID, at, "periodic", sent, acknowledged, 0, 0, 0)
	line = strings.Replace(line, `"terminal":0`, `"terminal":1`, 1)
	line = strings.Replace(line, `"total":0,"attempt_timeout":0`, `"total":1,"attempt_timeout":1`, 1)
	return line
}

func workerStatusCutTestLine(runID, at, cut string, sent, acknowledged, retry, generationStop, sessionClosed uint64) string {
	return fmt.Sprintf(`{"event":"wkbench.chat_lifecycle.worker_status_cut","run_id":%q,"at":%q,"cut":%q,"totals":{"target":2500,"online":2500,"starting":0,"closing":0,"traffic_ready":2500},"close_reasons":{"expired":0,"heartbeat_failed":0,"remote_terminal":0,"read_failed":0,"generation_stop":%d,"explicit_logout":0,"transport_close_failed":0},"messages":{"sent":%d,"send_attempts":%d,"first_attempts":%d,"first_attempt_failures":0,"send_acknowledged":%d,"send_rejected":0,"received":0,"receive_acknowledged":0,"receive_ack_failures":0,"retry_attempts":%d,"terminal":%d,"terminal_reasons":{"retry_exhausted":{"total":0,"attempt_timeout":0,"local_admission":0,"transport_error":0,"retryable_sendack":0,"unclassified":0},"non_retriable":0,"session_closed":%d},"losses":0,"duplicates":0,"corruptions":0,"sequence_regressions":0},"harness":{"failures":0,"command_saturation":0,"offered_underdelivery":0,"planned_cancellations":0,"drain_timed_out":false,"unexpected_exit":false}}`,
		runID, at, cut, generationStop, sent, sent+retry, sent, acknowledged, retry, sessionClosed, sessionClosed)
}

func writeTimelineTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTimelineTestJSON(t *testing.T, path string, destination any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatal(err)
	}
}
