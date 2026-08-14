package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/capacity"
	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/messageevent"
)

func TestWorkerCommandRequiresControlToken(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	code := runWithStderr([]string{"worker", "--listen", "127.0.0.1:0"}, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--control-token is required") {
		t.Fatalf("expected control token error, got %q", stderr.String())
	}
}

func TestRootCommandHelpListsSubcommands(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	for _, want := range []string{"Usage:", "run", "worker", "host-metrics", "validate", "doctor", "dev-sim", "capacity", "metrics"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected root help to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestHostMetricsCommandValidatesAndExposesSelectedFilesystem(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"host-metrics", "--listen", "127.0.0.1:0"}, &stderr)
	if code != exitConfig || !strings.Contains(stderr.String(), "--path") {
		t.Fatalf("missing path code/stderr = %d/%q", code, stderr.String())
	}

	temporary := t.TempDir()
	processPath := filepath.Join(temporary, "processes.prom")
	processMetrics := fmt.Sprintf(
		"wukongim_process_up{unit=\"wukongim.service\"} 0\nwukongim_process_collector_last_success_unixtime_seconds %d\n",
		time.Now().Unix(),
	)
	if err := os.WriteFile(processPath, []byte(processMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := newHostMetricsHandler(hostMetricsConfig{
		path: temporary, mountpoint: "/var/lib/wukongim-1", device: "/dev/local-data-1", processMetricsPath: processPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	nativeHandler := handler.(*hostMetricsHandler)
	totals, ok := readHostCPUTotals()
	if !ok || totals.total == 0 {
		t.Fatal("native CPU totals unavailable")
	}
	nativeHandler.previousCPU = totals
	nativeHandler.previousCPU.total--
	nativeHandler.previousCPUSet = true
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status/body = %d/%q", response.Code, response.Body.String())
	}
	for _, want := range []string{
		`node_filesystem_size_bytes{device="/dev/local-data-1",mountpoint="/var/lib/wukongim-1"}`,
		`node_filesystem_avail_bytes{device="/dev/local-data-1",mountpoint="/var/lib/wukongim-1"}`,
		`wkbench_host_cpu_busy_percent `,
		`wkbench_host_memory_used_percent `,
		`wukongim_process_up{unit="wukongim.service"} 0`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("metrics body missing %q: %q", want, response.Body.String())
		}
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d", response.Code)
	}

	staleAt := time.Now().Add(-processMetricsFreshnessWindow - time.Second)
	if err := os.Chtimes(processPath, staleAt, staleAt); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale process evidence status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	staleMetrics := fmt.Sprintf(
		"wukongim_process_up{unit=\"wukongim.service\"} 0\nwukongim_process_collector_last_success_unixtime_seconds %d\n",
		time.Now().Add(-processMetricsFreshnessWindow-time.Second).Unix(),
	)
	if err := os.WriteFile(processPath, []byte(staleMetrics), 0o600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale collector timestamp status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestHostMetricsNativeResourceCollectors(t *testing.T) {
	totals, ok := readHostCPUTotals()
	if !ok || totals.total == 0 || totals.idle > totals.total {
		t.Fatalf("host CPU totals = %+v/%v, want a valid native sample", totals, ok)
	}
	memory, ok := hostMemoryUsedPercent()
	if !ok || memory < 0 || memory > 100 {
		t.Fatalf("host memory used percent = %v/%v, want a value in [0,100]", memory, ok)
	}
}

func TestClassifyLocalChatLifecycleStepSeparatesRateFromOnlineConnections(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	evidence := localChatLifecycleStepEvidence{
		QualificationReportComplete: true, FinalReportComplete: true,
		StorageComplete: true, HostIOComplete: true, ProductMetricsComplete: true,
		ProductQueueEvidenceComplete: true, ProductQueuesConverged: true, ProcessesContinuous: true,
		TimelineComplete: true, ProfileEvidenceComplete: true,
	}

	result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})

	if result.Outcome != localChatLifecycleStepClean || result.OnlineConnections != 2500 ||
		result.Acknowledged != 11_900 || result.Expected != 12_000 || result.ActualRatePerSecond != 99.16666666666667 {
		t.Fatalf("local step result = %+v", result)
	}
}

func TestClassifyLocalChatLifecycleStepExcludesWarmupAcknowledgementsThatArriveAfterQualification(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	before.Messages.Sent = 6_001
	before.Messages.SendAcknowledged = 5_894
	after.Messages.Sent = 18_001
	after.Messages.SendAcknowledged = 18_001
	evidence := localChatLifecycleStepEvidence{
		QualificationReportComplete: true, FinalReportComplete: true,
		StorageComplete: true, HostIOComplete: true, ProductMetricsComplete: true,
		ProductQueueEvidenceComplete: true, ProductQueuesConverged: true, ProcessesContinuous: true,
		TimelineComplete: true, ProfileEvidenceComplete: true,
	}

	result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})

	if result.Outcome != localChatLifecycleStepClean || result.Sent != 12_000 ||
		result.Acknowledged != 12_000 || result.ActualRatePerSecond != 100 {
		t.Fatalf("local step result = %+v", result)
	}
}

func TestClassifyLocalChatLifecycleStepFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*chatlifecycle.Report, *chatlifecycle.Report, *localChatLifecycleStepEvidence)
		want   localChatLifecycleStepOutcome
	}{
		{
			name: "storage confounded", want: localChatLifecycleStepStorageConfounded,
			mutate: func(_ *chatlifecycle.Report, after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				after.Resources.Nodes[1].DataFilesystemAvailableBytes = 50
			},
		},
		{
			name: "missing normalized storage evidence", want: localChatLifecycleStepInsufficientEvidence,
			mutate: func(_ *chatlifecycle.Report, _ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.StorageComplete = false
			},
		},
		{
			name: "missing normalized host I/O evidence", want: localChatLifecycleStepInsufficientEvidence,
			mutate: func(_ *chatlifecycle.Report, _ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.HostIOComplete = false
			},
		},
		{
			name: "missing post-drain product queue evidence", want: localChatLifecycleStepInsufficientEvidence,
			mutate: func(_ *chatlifecycle.Report, _ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.ProductQueueEvidenceComplete = false
			},
		},
		{
			name: "missing unified timeline evidence", want: localChatLifecycleStepInsufficientEvidence,
			mutate: func(_ *chatlifecycle.Report, _ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.TimelineComplete = false
			},
		},
		{
			name: "missing threshold profile status", want: localChatLifecycleStepInsufficientEvidence,
			mutate: func(_ *chatlifecycle.Report, _ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.ProfileEvidenceComplete = false
			},
		},
		{
			name: "post-drain product queues did not converge", want: localChatLifecycleStepRateFailed,
			mutate: func(_ *chatlifecycle.Report, _ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.ProductQueuesConverged = false
			},
		},
		{
			name: "throughput underdelivery", want: localChatLifecycleStepRateFailed,
			mutate: func(_ *chatlifecycle.Report, after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				after.Messages.Sent = 6_100
				after.Messages.SendAcknowledged = 6_100
			},
		},
		{
			name: "warmup acknowledgement count exceeds warmup sends", want: localChatLifecycleStepInsufficientEvidence,
			mutate: func(before *chatlifecycle.Report, _ *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				before.Messages.SendAcknowledged = before.Messages.Sent + 1
			},
		},
		{
			name: "remaining correlation", want: localChatLifecycleStepRateFailed,
			mutate: func(_ *chatlifecycle.Report, after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				after.Correlation.Outstanding = 1
			},
		},
		{
			name: "terminal report predates product drain evidence", want: localChatLifecycleStepClean,
			mutate: func(before *chatlifecycle.Report, after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				before.Resources.Nodes[0].QueueCurrent = 2
				after.Resources.Nodes[0].QueueCurrent = 3
			},
		},
		{
			name: "terminal send", want: localChatLifecycleStepProductFailure,
			mutate: func(_ *chatlifecycle.Report, after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				after.Messages.Terminal = 1
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, after := localChatLifecycleStepReports()
			evidence := localChatLifecycleStepEvidence{
				QualificationReportComplete: true, FinalReportComplete: true,
				StorageComplete: true, HostIOComplete: true, ProductMetricsComplete: true,
				ProductQueueEvidenceComplete: true, ProductQueuesConverged: true, ProcessesContinuous: true,
				TimelineComplete: true, ProfileEvidenceComplete: true,
			}
			test.mutate(&before, &after, &evidence)
			result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
				OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
			})
			if result.Outcome != test.want {
				t.Fatalf("outcome = %q, want %q; result=%+v", result.Outcome, test.want, result)
			}
		})
	}
}

func TestClassifyLocalChatLifecycleStepPreservesProductFailureBeforeQualification(t *testing.T) {
	_, after := localChatLifecycleStepReports()
	after.Sessions.Online = 0
	after.Messages.Sent = 6_901
	after.Messages.SendAcknowledged = 5_999
	after.Messages.Terminal = 6
	after.Messages.Losses = 1
	after.Verdict.Outcome = chatlifecycle.VerdictProductFailure
	evidence := localChatLifecycleStepEvidence{
		FinalReportComplete: true, HostIOComplete: true, ProcessesContinuous: true,
		TimelineComplete: true, ProfileEvidenceComplete: true,
	}

	result := classifyLocalChatLifecycleStep(chatlifecycle.Report{}, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 150, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})

	if result.Outcome != localChatLifecycleStepProductFailure ||
		result.Reason != "terminal_product_failure_before_qualification" || result.QualificationReached ||
		result.TargetConnections != 2500 || result.OnlineConnections != 0 ||
		result.Sent != 6_901 || result.Acknowledged != 5_999 || result.Expected != 0 ||
		result.ActualRatePerSecond != 0 {
		t.Fatalf("pre-qualification product result = %+v", result)
	}
}

func TestClassifyLocalChatLifecycleStepFailsClosedBeforeQualificationWithoutTerminalProof(t *testing.T) {
	terminalReport := func() chatlifecycle.Report {
		_, after := localChatLifecycleStepReports()
		after.Sessions.Online = 0
		after.Messages.Sent = 6_901
		after.Messages.SendAcknowledged = 5_999
		after.Messages.Terminal = 6
		after.Verdict.Outcome = chatlifecycle.VerdictProductFailure
		return after
	}
	completeEvidence := func() localChatLifecycleStepEvidence {
		return localChatLifecycleStepEvidence{
			FinalReportComplete: true, ProcessesContinuous: true,
			TimelineComplete: true, ProfileEvidenceComplete: true,
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*chatlifecycle.Report, *localChatLifecycleStepEvidence)
	}{
		{
			name: "missing final report",
			mutate: func(_ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.FinalReportComplete = false
			},
		},
		{
			name: "missing process continuity",
			mutate: func(_ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.ProcessesContinuous = false
			},
		},
		{
			name: "missing typed timeline",
			mutate: func(_ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.TimelineComplete = false
			},
		},
		{
			name: "missing typed profile status",
			mutate: func(_ *chatlifecycle.Report, evidence *localChatLifecycleStepEvidence) {
				evidence.ProfileEvidenceComplete = false
			},
		},
		{
			name: "missing final filesystem observation",
			mutate: func(after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				after.Resources.Nodes[1].DataFilesystemBytes = 0
			},
		},
		{
			name: "missing terminal product evidence",
			mutate: func(after *chatlifecycle.Report, _ *localChatLifecycleStepEvidence) {
				after.Messages.Terminal = 0
				after.Verdict.Outcome = chatlifecycle.VerdictOperatorStop
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := terminalReport()
			evidence := completeEvidence()
			test.mutate(&after, &evidence)
			result := classifyLocalChatLifecycleStep(chatlifecycle.Report{}, after, evidence, localChatLifecycleStepOptions{
				OfferedRatePerSecond: 150, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
			})
			if result.Outcome != localChatLifecycleStepInsufficientEvidence ||
				result.Reason != "invalid_or_missing_evidence" || result.ActualRatePerSecond != 0 || result.Expected != 0 {
				t.Fatalf("pre-qualification incomplete result = %+v", result)
			}
		})
	}
}

func TestLocalChatLifecycleProfileEvidenceMatchesMeasuredTimeline(t *testing.T) {
	timeline := localChatLifecycleUnifiedTimeline{}
	writeStatus := func(t *testing.T, dir, body string) string {
		t.Helper()
		path := filepath.Join(dir, "profile-status.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	notTriggered := `{"schema":"wukongim/chat-lifecycle-threshold-pprof-status/v1","status":"not_triggered","evidence_complete":true,"capture_valid":true,"reason":"no_measured_threshold","trigger_kind":"","trigger_previous_utc":"","trigger_current_utc":"","metadata":""}`
	if complete, err := readLocalStepProfileEvidence(writeStatus(t, t.TempDir(), notTriggered), timeline); err != nil || !complete {
		t.Fatalf("not-triggered profile evidence = %v/%v", complete, err)
	}
	previous := time.Date(2026, 8, 13, 1, 2, 3, 100, time.UTC)
	current := previous.Add(time.Second)
	timeline.MeasuredFirstBreach = localTimelineFirstBreach{
		Observed: true, TriggerKind: localTimelineTriggerActualOfferedRatio,
		PreviousAt: &previous, CurrentAt: &current,
	}
	if complete, err := readLocalStepProfileEvidence(writeStatus(t, t.TempDir(), notTriggered), timeline); err == nil || complete {
		t.Fatalf("contradictory not-triggered evidence = %v/%v", complete, err)
	}
	dir := t.TempDir()
	metadataDir := filepath.Join(dir, "threshold-pprof")
	if err := os.MkdirAll(filepath.Join(metadataDir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := fmt.Sprintf(`{
  "schema":"wukongim.local_threshold_pprof/v1",
	  "trigger":{"kind":"actual_offered_ratio","observed_phase":"measurement","previous_utc":%q,"current_utc":%q},
  "capture":{"status":"partial","valid":false,"reason":"profile_capture_missing","start_phase":"measurement","end_phase":"measurement","started_at_utc":"2026-08-13T01:02:04Z","completed_at_utc":"2026-08-13T01:02:05Z","cpu_seconds":10},
  "nodes":[
    {"node":"node-1","cpu":"missing","heap":"missing","goroutine":"missing"},
    {"node":"node-2","cpu":"missing","heap":"missing","goroutine":"missing"},
    {"node":"node-3","cpu":"missing","heap":"missing","goroutine":"missing"}
  ]
}`, previous.Format(time.RFC3339Nano), current.Format(time.RFC3339Nano))
	metadataPath := filepath.Join(metadataDir, "metadata.json")
	if err := os.WriteFile(metadataPath, []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	partial := fmt.Sprintf(`{"schema":"wukongim/chat-lifecycle-threshold-pprof-status/v1","status":"partial","evidence_complete":true,"capture_valid":false,"reason":"profile_capture_missing","trigger_kind":"actual_offered_ratio","trigger_previous_utc":%q,"trigger_current_utc":%q,"metadata":"threshold-pprof/metadata.json"}`, previous.Format(time.RFC3339Nano), current.Format(time.RFC3339Nano))
	statusPath := writeStatus(t, dir, partial)
	if complete, err := readLocalStepProfileEvidence(statusPath, timeline); err != nil || !complete {
		t.Fatalf("partial threshold profile evidence = %v/%v", complete, err)
	}
	metadata = strings.Replace(metadata, `"cpu":"missing"`, `"cpu":"complete"`, 1)
	if err := os.WriteFile(metadataPath, []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, err := readLocalStepProfileEvidence(statusPath, timeline); err == nil || complete {
		t.Fatalf("missing declared profile blob = %v/%v", complete, err)
	}
	operational := `{"schema":"wukongim/chat-lifecycle-threshold-pprof-status/v1","status":"operational_error","evidence_complete":false,"capture_valid":false,"reason":"missing_or_invalid_helper_metadata","trigger_kind":"","trigger_previous_utc":"","trigger_current_utc":"","metadata":"","helper_exit_status":73}`
	if complete, err := readLocalStepProfileEvidence(writeStatus(t, t.TempDir(), operational), timeline); err == nil || complete {
		t.Fatalf("operational profile evidence = %v/%v", complete, err)
	}
}

func TestClassifyLocalChatLifecycleStepFailsClosedOnOperatorInterrupt(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	evidence := localChatLifecycleStepEvidence{
		QualificationReportComplete: true, FinalReportComplete: true,
		StorageComplete: true, HostIOComplete: true, ProductMetricsComplete: true,
		ProductQueueEvidenceComplete: true, ProductQueuesConverged: true,
		ProcessesContinuous: true, TimelineComplete: true, ProfileEvidenceComplete: true,
		OperatorInterrupted: true,
	}
	result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})
	if result.Outcome != localChatLifecycleStepInsufficientEvidence || result.Reason != "operator_interrupted" ||
		!result.OperatorInterrupted {
		t.Fatalf("operator-interrupted result = %+v", result)
	}
}

func TestClassifyLocalChatLifecycleStepFailsClosedOnGracefulStopTimeout(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	evidence := localChatLifecycleStepEvidence{
		QualificationReportComplete: true, FinalReportComplete: true,
		StorageComplete: true, HostIOComplete: true, ProductMetricsComplete: true,
		ProductQueueEvidenceComplete: true, ProductQueuesConverged: true,
		ProcessesContinuous: true, TimelineComplete: true, ProfileEvidenceComplete: true,
		HarnessFailureReason: localChatLifecycleHarnessFailureCoordinatorGracefulStopTimeout,
	}
	result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})
	if result.Outcome != localChatLifecycleStepInsufficientEvidence ||
		result.Reason != string(localChatLifecycleHarnessFailureCoordinatorGracefulStopTimeout) ||
		result.HarnessFailureReason != localChatLifecycleHarnessFailureCoordinatorGracefulStopTimeout {
		t.Fatalf("graceful-stop-timeout result = %+v", result)
	}

	// A concurrent operator signal is retained, but the timeout is the more
	// precise reason the evidence could not become terminal.
	evidence.OperatorInterrupted = true
	result = classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})
	if result.Reason != string(localChatLifecycleHarnessFailureCoordinatorGracefulStopTimeout) ||
		!result.OperatorInterrupted {
		t.Fatalf("operator timeout result = %+v", result)
	}
}

func TestClassifyLocalChatLifecycleStepFailsClosedWhenCoordinatorExitsBeforeStopRequest(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	evidence := localChatLifecycleStepEvidence{
		QualificationReportComplete: true, FinalReportComplete: true,
		StorageComplete: true, HostIOComplete: true, ProductMetricsComplete: true,
		ProductQueueEvidenceComplete: true, ProductQueuesConverged: true,
		ProcessesContinuous: true, TimelineComplete: true, ProfileEvidenceComplete: true,
		HarnessFailureReason: localChatLifecycleHarnessFailureCoordinatorExitedBeforeStopRequest,
	}
	result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})
	if result.Outcome != localChatLifecycleStepInsufficientEvidence ||
		result.Reason != string(localChatLifecycleHarnessFailureCoordinatorExitedBeforeStopRequest) ||
		result.HarnessFailureReason != localChatLifecycleHarnessFailureCoordinatorExitedBeforeStopRequest {
		t.Fatalf("coordinator stop-request race result = %+v", result)
	}
}

func TestClassifyLocalChatLifecycleStepRejectsOpenEndedHarnessFailureReason(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	result := classifyLocalChatLifecycleStep(before, after, localChatLifecycleStepEvidence{
		HarnessFailureReason: localChatLifecycleHarnessFailureReason("arbitrary_wrapper_error"),
	}, localChatLifecycleStepOptions{
		OfferedRatePerSecond: 100, MeasuredDuration: 2 * time.Minute, MinimumThroughputPercent: 90,
	})
	if result.Outcome != localChatLifecycleStepInsufficientEvidence || result.Reason != "invalid_or_missing_evidence" {
		t.Fatalf("open-ended harness failure result = %+v", result)
	}
}

func TestLocalChatLifecycleStepCommandRejectsUnknownHarnessFailureReason(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-chat-lifecycle-step",
		"--before", "before.json", "--after", "after.json",
		"--storage-summary", "storage.tsv", "--host-io-summary", "host.tsv",
		"--product-queue-summary", "queue.tsv", "--process-continuity", "process.tsv",
		"--timeline", "timeline.json", "--profile-status", "profile.json",
		"--run-id", "local", "--output", "local-step.json",
		"--offered-rate", "100", "--measured-duration", "120s",
		"--harness-failure-reason", "arbitrary_wrapper_error",
	}, &stderr)
	if code != exitConfig || !strings.Contains(stderr.String(), "--harness-failure-reason is unsupported") {
		t.Fatalf("unknown harness failure code/stderr = %d/%q", code, stderr.String())
	}
}

func TestLocalChatLifecycleTimelineRequiresClosedOrderedMeasuredWindows(t *testing.T) {
	at := func(seconds int) *time.Time {
		value := time.Date(2026, 8, 13, 1, 0, seconds, 0, time.UTC)
		return &value
	}
	timeline := localChatLifecycleUnifiedTimeline{
		Schema: localChatLifecycleUnifiedTimelineSchemaV1, RunID: "complete-timeline",
		OfferedRatePerSecond: 100, MinimumThroughputPercent: 90, QualificationCutPresent: true,
		SourceCompleteness: localTimelineSourceCompleteness{
			WorkerStatusCutsComplete: true, BoundaryTimelineComplete: true, StorageOverlapComplete: true,
			TerminalCutPresent: true, FirstBreachObservable: true,
		},
		Windows: map[string]localTimelineWindow{
			"warmup":   {StartAt: at(0), EndAt: at(5), Complete: true},
			"measured": {StartAt: at(5), EndAt: at(35), Complete: true},
			"drain":    {StartAt: at(35), EndAt: at(40), Complete: true},
			"shutdown": {StartAt: at(40), EndAt: at(41), Complete: true},
		},
	}
	timeline.Overlap.Compaction = localTimelineOverlapEvidence{Status: "not_observed", SourceComplete: true}
	timeline.Overlap.Snapshot = localTimelineOverlapEvidence{Status: "not_observed", SourceComplete: true}
	for _, kind := range []string{
		"warmup_start", "warmup_end", "measurement_start", "measurement_end", "drain_start", "drain_end", "shutdown_start",
	} {
		timeline.Points = append(timeline.Points, localTimelinePoint{Source: "boundary", Kind: kind, BoundaryNode: "boundary"})
	}
	if !localChatLifecycleTimelineWindowsComplete(timeline, 30*time.Second) {
		t.Fatal("closed ordered measured timeline was rejected")
	}
	timelinePath := filepath.Join(t.TempDir(), "timeline.json")
	body, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(timelinePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, complete, err := readLocalStepTimelineEvidence(timelinePath, "complete-timeline", 100, 90, 30*time.Second); err != nil || !complete {
		t.Fatalf("complete storage-overlap timeline = %v/%v", complete, err)
	}
	timeline.SourceCompleteness.StorageOverlapComplete = false
	body, err = json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(timelinePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, complete, err := readLocalStepTimelineEvidence(timelinePath, "complete-timeline", 100, 90, 30*time.Second); err == nil || complete {
		t.Fatalf("missing storage-overlap timeline = %v/%v", complete, err)
	}
	timeline.SourceCompleteness.StorageOverlapComplete = true
	timeline.Overlap.Compaction = localTimelineOverlapEvidence{Status: "unknown", SourceComplete: true}
	body, err = json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(timelinePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, complete, err := readLocalStepTimelineEvidence(timelinePath, "complete-timeline", 100, 90, 30*time.Second); err == nil || complete {
		t.Fatalf("inconsistent storage-overlap timeline = %v/%v", complete, err)
	}
	short := timeline
	short.Windows = make(map[string]localTimelineWindow, len(timeline.Windows))
	for name, window := range timeline.Windows {
		short.Windows[name] = window
	}
	short.Windows["measured"] = localTimelineWindow{StartAt: at(5), EndAt: at(20), Complete: true}
	if localChatLifecycleTimelineWindowsComplete(short, 30*time.Second) {
		t.Fatal("early measured termination was accepted")
	}
	duplicate := timeline
	duplicate.Points = append(append([]localTimelinePoint(nil), timeline.Points...), localTimelinePoint{
		Source: "boundary", Kind: "measurement_end", BoundaryNode: "boundary",
	})
	if localChatLifecycleTimelineWindowsComplete(duplicate, 30*time.Second) {
		t.Fatal("duplicate measured boundary was accepted")
	}
}

func TestLocalChatLifecycleProductMetricsRequireClosedCompleteCuts(t *testing.T) {
	before, after := localChatLifecycleStepReports()
	for report, samples := range map[*chatlifecycle.Report]uint64{&before: 2, &after: 3} {
		report.Resources.Capacity.Complete = true
		report.Resources.Capacity.ProcessesComplete = true
		report.Resources.Capacity.Samples = samples
		report.Resources.Capacity.MissingSamples = 1
		report.Resources.Capacity.WorkerQueuesComplete = true
		report.Resources.Capacity.WorkerQueueSamples = samples
		report.Resources.Capacity.WorkerQueueMissingSamples = 1
		report.Cluster.HealthySamples = samples
	}
	if !localChatLifecycleProductMetricsComplete(before, after) {
		t.Fatal("complete local product metrics were rejected")
	}
	after.Resources.Capacity.MissingSamples++
	if localChatLifecycleProductMetricsComplete(before, after) {
		t.Fatal("new host/process sampling gap was accepted")
	}
	after.Resources.Capacity.MissingSamples = before.Resources.Capacity.MissingSamples
	after.Resources.Capacity.WorkerQueueMissingSamples++
	if localChatLifecycleProductMetricsComplete(before, after) {
		t.Fatal("new worker queue sampling gap was accepted")
	}
	after.Resources.Capacity.WorkerQueueMissingSamples = before.Resources.Capacity.WorkerQueueMissingSamples
	after.Resources.Capacity.Samples = before.Resources.Capacity.Samples
	if localChatLifecycleProductMetricsComplete(before, after) {
		t.Fatal("product cut without a new resource sample was accepted")
	}
}

func TestLocalChatLifecycleStepEvidenceReadersRequireCompleteClosedRows(t *testing.T) {
	directory := t.TempDir()
	storagePath := filepath.Join(directory, "storage.tsv")
	var storage strings.Builder
	storage.WriteString(strings.Join(localStepStorageHeader, "\t") + "\n")
	for node := 1; node <= 3; node++ {
		row := make([]string, len(localStepStorageHeader))
		for index := range row {
			row[index] = "0"
		}
		row[0], row[1], row[2] = "rate-100", fmt.Sprintf("node-%d", node), "complete"
		for _, column := range []int{4, 5, 6, 7, 15, 31, 32} {
			row[column] = "1"
		}
		storage.WriteString(strings.Join(row, "\t") + "\n")
	}
	if err := os.WriteFile(storagePath, []byte(storage.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, err := readLocalStepStorageEvidence(storagePath, "rate-100"); err != nil || !complete {
		t.Fatalf("storage evidence complete/error = %v/%v", complete, err)
	}
	if complete, err := readLocalStepStorageEvidence(storagePath, "rate-101"); err == nil || complete {
		t.Fatalf("mismatched storage tag complete/error = %v/%v", complete, err)
	}
	zeroActivity := strings.NewReplacer(
		"\t1\t1\t1\t1\t", "\t0\t0\t0\t0\t",
	).Replace(storage.String())
	if err := os.WriteFile(storagePath, []byte(zeroActivity), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, err := readLocalStepStorageEvidence(storagePath, "rate-100"); err == nil || complete {
		t.Fatalf("zero-activity storage complete/error = %v/%v", complete, err)
	}
	invalidHeader := append([]string(nil), localStepStorageHeader...)
	invalidHeader[3] = "renamed_queue_depth"
	invalidStorage := strings.Replace(storage.String(), strings.Join(localStepStorageHeader, "\t"), strings.Join(invalidHeader, "\t"), 1)
	if err := os.WriteFile(storagePath, []byte(invalidStorage), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, err := readLocalStepStorageEvidence(storagePath, "rate-100"); err == nil || complete {
		t.Fatalf("invalid storage header complete/error = %v/%v", complete, err)
	}

	hostIOPath := filepath.Join(directory, "host-io.tsv")
	var hostIO strings.Builder
	hostIO.WriteString(strings.Join(localStepHostIOHeader, "\t") + "\n")
	for _, host := range []string{"host-node-1", "host-node-2", "host-node-3", "host-load"} {
		hostIO.WriteString("rate-100\t" + host + "\tunavailable\tunavailable\t0\tunavailable\t0\tunavailable\t0\tunavailable\t0\tunavailable\t0\n")
	}
	if err := os.WriteFile(hostIOPath, []byte(hostIO.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, err := readLocalStepHostIOEvidence(hostIOPath, "rate-100"); err != nil || !complete {
		t.Fatalf("host I/O evidence complete/error = %v/%v", complete, err)
	}
	if complete, err := readLocalStepHostIOEvidence(hostIOPath, "rate-101"); err == nil || complete {
		t.Fatalf("mismatched host I/O tag complete/error = %v/%v", complete, err)
	}

	productQueuePath := filepath.Join(directory, "product-queue.tsv")
	var productQueue strings.Builder
	productQueue.WriteString(strings.Join(localStepProductQueueHeader, "\t") + "\n")
	for node := 1; node <= 3; node++ {
		fmt.Fprintf(&productQueue, "rate-100\tnode-%d\tcomplete\t60\t160\t40\t80\ttrue\n", node)
	}
	if err := os.WriteFile(productQueuePath, []byte(productQueue.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, converged, err := readLocalStepProductQueueEvidence(productQueuePath, "rate-100"); err != nil || !complete || !converged {
		t.Fatalf("product queue evidence complete/converged/error = %v/%v/%v", complete, converged, err)
	}
	notConverged := strings.Replace(productQueue.String(), "\t40\t80\ttrue\n", "\t70\t80\tfalse\n", 1)
	if err := os.WriteFile(productQueuePath, []byte(notConverged), 0o600); err != nil {
		t.Fatal(err)
	}
	if complete, converged, err := readLocalStepProductQueueEvidence(productQueuePath, "rate-100"); err != nil || !complete || converged {
		t.Fatalf("non-converged product queue complete/converged/error = %v/%v/%v", complete, converged, err)
	}

	processPath := filepath.Join(directory, "process.tsv")
	processes := []string{"service-1", "service-2", "service-3", "worker-1", "worker-2", "worker-3",
		"host-metrics-1", "host-metrics-2", "host-metrics-3", "host-metrics-load", "process-metrics-collector"}
	var process strings.Builder
	process.WriteString("name\talive\n")
	for _, name := range processes {
		fmt.Fprintf(&process, "%s\ttrue\n", name)
	}
	if err := os.WriteFile(processPath, []byte(process.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if continuous, err := readLocalStepProcessContinuity(processPath); err != nil || !continuous {
		t.Fatalf("process continuity complete/error = %v/%v", continuous, err)
	}
}

func TestLinuxPhysicalDeviceIOSampleUsesCounterDeltas(t *testing.T) {
	before, err := parseLinuxBlockDeviceCounters("100 0 200 300 400 0 500 600 0 700 800")
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseLinuxBlockDeviceCounters("120 0 260 340 430 0 620 660 0 900 1000")
	if err != nil {
		t.Fatal(err)
	}
	sample, ok := linuxBlockDeviceDelta("nvme1n1", before, after, 2*time.Second)
	if !ok || !sample.IOPSAvailable || !sample.BytesPerSecondAvailable || !sample.UtilizationAvailable || !sample.ServiceTimeAvailable {
		t.Fatalf("linux sample availability = %+v / %v", sample, ok)
	}
	if sample.ReadIOPS != 10 || sample.WriteIOPS != 15 || sample.TotalIOPS != 25 ||
		sample.ReadBytesPerSecond != 15_360 || sample.WriteBytesPerSecond != 30_720 ||
		sample.UtilizationPercent != 10 || sample.ServiceTimeMilliseconds != 2 {
		t.Fatalf("linux sample = %+v", sample)
	}
}

func TestDarwinPhysicalDeviceIOSampleMarksUnsupportedFieldsUnavailable(t *testing.T) {
	volume, ok := parseDarwinDFDevice(`Filesystem 512-blocks Used Available Capacity iused ifree %iused Mounted on
/dev/disk3s5 100000 50000 50000 50% 1 2 33% /System/Volumes/Data
`)
	if !ok || volume != "/dev/disk3s5" {
		t.Fatalf("darwin volume = %q/%v", volume, ok)
	}
	device, ok := parseDarwinPhysicalDevice(`
   Device Identifier:         disk3s5
   Part of Whole:             disk3
   APFS Physical Store:       disk0s2
`)
	if !ok || device != "disk0" {
		t.Fatalf("physical device = %q/%v", device, ok)
	}
	sample, err := parseDarwinIostatDeviceSample(device, `
              disk0
    KB/t xfrs   MB
   22.75 9999 200.00
    7.48  208   1.52
`, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !sample.IOPSAvailable || !sample.BytesPerSecondAvailable || sample.TotalIOPS != 208 ||
		sample.TotalBytesPerSecond != 1.52*1024*1024 || sample.UtilizationAvailable || sample.ServiceTimeAvailable ||
		sample.ReadWriteSplitAvailable {
		t.Fatalf("darwin sample = %+v", sample)
	}
}

func TestDarwinPhysicalDeviceIOSamplerReturnsCachedSampleAndSingleFlightsRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	collectCalls := 0
	sampler := &darwinHostDeviceIOSampler{
		device: "disk0",
		last:   hostDeviceIOSample{Device: "disk0"},
		collect: func() (hostDeviceIOSample, error) {
			collectCalls++
			close(started)
			<-release
			return hostDeviceIOSample{Device: "disk0", IOPSAvailable: true, TotalIOPS: 42}, nil
		},
	}

	returned := make(chan hostDeviceIOSample, 1)
	go func() { returned <- sampler.Sample() }()
	select {
	case sample := <-returned:
		if sample.Device != "disk0" || sample.IOPSAvailable {
			t.Fatalf("initial cached sample = %+v", sample)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Sample blocked on the physical I/O collector")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background physical I/O refresh did not start")
	}
	if sample := sampler.Sample(); sample.Device != "disk0" || sample.IOPSAvailable {
		t.Fatalf("in-flight cached sample = %+v", sample)
	}
	if collectCalls != 1 {
		t.Fatalf("collector calls while refresh is in flight = %d, want 1", collectCalls)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		sample := sampler.Sample()
		if sample.IOPSAvailable {
			if sample.TotalIOPS != 42 {
				t.Fatalf("refreshed sample = %+v", sample)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refreshed sample was not published")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDarwinPhysicalDeviceIOSamplerExpiresCachedAvailabilityWhileRefreshIsInFlight(t *testing.T) {
	base := time.Unix(1_970_000_000, 0)
	now := base.Add(darwinHostDeviceIOMaxSampleAge)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	sampler := &darwinHostDeviceIOSampler{
		device: "disk0",
		last: hostDeviceIOSample{
			Device: "disk0", IOPSAvailable: true, BytesPerSecondAvailable: true,
			TotalIOPS: 42, TotalBytesPerSecond: 4096,
		},
		at:  base,
		now: func() time.Time { return now },
		collect: func() (hostDeviceIOSample, error) {
			close(started)
			<-release
			return hostDeviceIOSample{Device: "disk0", IOPSAvailable: true, TotalIOPS: 84}, nil
		},
	}

	sample := sampler.Sample()
	if sample.Device != "disk0" || sample.IOPSAvailable || sample.BytesPerSecondAvailable ||
		sample.UtilizationAvailable || sample.ServiceTimeAvailable || sample.ReadWriteSplitAvailable {
		t.Fatalf("expired cached sample = %+v", sample)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background physical I/O refresh did not start")
	}
	if sample = sampler.Sample(); sample.Device != "disk0" || sample.IOPSAvailable {
		t.Fatalf("expired in-flight cached sample = %+v", sample)
	}
}

func TestDarwinPhysicalDeviceIOSamplerInvalidatesAvailabilityWhenRefreshFails(t *testing.T) {
	base := time.Unix(1_970_000_000, 0)
	sampler := &darwinHostDeviceIOSampler{
		device: "disk0",
		last: hostDeviceIOSample{
			Device: "disk0", IOPSAvailable: true, BytesPerSecondAvailable: true,
			TotalIOPS: 42, TotalBytesPerSecond: 4096,
		},
		at:         base,
		now:        func() time.Time { return base.Add(darwinHostDeviceIORefreshInterval) },
		refreshing: true,
		collect: func() (hostDeviceIOSample, error) {
			return hostDeviceIOSample{}, errors.New("iostat failed")
		},
	}

	sampler.refresh()
	sampler.collect = nil
	sample := sampler.Sample()
	if sample.Device != "disk0" || sample.IOPSAvailable || sample.BytesPerSecondAvailable ||
		sample.UtilizationAvailable || sample.ServiceTimeAvailable || sample.ReadWriteSplitAvailable ||
		sample.TotalIOPS != 0 || sample.TotalBytesPerSecond != 0 {
		t.Fatalf("sample after failed refresh = %+v", sample)
	}
}

func TestHostMetricsPublishesVersionedPhysicalIOAvailability(t *testing.T) {
	directory := t.TempDir()
	handlerValue, err := newHostMetricsHandler(hostMetricsConfig{
		path: directory, mountpoint: "/data", device: "/dev/data",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := handlerValue.(*hostMetricsHandler)
	handler.deviceIO = fixedHostDeviceIOSampler{sample: hostDeviceIOSample{
		Device: "disk0", IOPSAvailable: true, BytesPerSecondAvailable: true,
		TotalIOPS: 208, TotalBytesPerSecond: 1_593_835.52,
	}}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status/body = %d/%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`wkbench_host_block_io_schema_info{physical_device="disk0",version="v1"} 1`,
		`wkbench_host_block_io_available{field="iops",physical_device="disk0"} 1`,
		`wkbench_host_block_io_available{field="utilization",physical_device="disk0"} 0`,
		`wkbench_host_block_io_iops{operation="total",physical_device="disk0"} 208.000000`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, "wkbench_host_block_io_service_time_milliseconds{") {
		t.Fatalf("unsupported service time was fabricated: %q", body)
	}
}

type fixedHostDeviceIOSampler struct{ sample hostDeviceIOSample }

func (s fixedHostDeviceIOSampler) Sample() hostDeviceIOSample { return s.sample }

func localChatLifecycleStepReports() (chatlifecycle.Report, chatlifecycle.Report) {
	start := time.Unix(1_970_000_000, 0).UTC()
	before := chatlifecycle.Report{
		ConfigDigest: "sha256:local-step", Fence: chatlifecycle.ReportFence{RunHash: "sha256:run", AssignmentHash: "sha256:assignment", Generation: 1},
		Window:   chatlifecycle.ReportTimeWindow{End: start},
		Topology: chatlifecycle.ReportTopologyProof{Validated: true, LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 3, ChannelReplicas: 3},
		Sessions: chatlifecycle.WorkerSessionSnapshot{Target: 2500, Online: 2500},
		Messages: chatlifecycle.WorkerMessageSnapshot{Sent: 100, SendAcknowledged: 100},
	}
	after := before
	after.Window.End = start.Add(2 * time.Minute)
	after.Final = true
	after.Verdict = chatlifecycle.ReportVerdictEvidence{
		Outcome: chatlifecycle.VerdictOperatorStop, Cause: chatlifecycle.VerdictCauseOperatorRequested, Terminal: true,
	}
	after.Messages.Sent = 12_000
	after.Messages.SendAcknowledged = 12_000
	for index := range after.Resources.Nodes {
		after.Resources.Nodes[index].DataFilesystemBytes = 1_000
		after.Resources.Nodes[index].DataFilesystemAvailableBytes = 200
	}
	return before, after
}

func TestRootCommandUsesWkbenchName(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench") {
		t.Fatalf("expected root help to use wkbench name, got %q", stderr.String())
	}
}

func TestCapacityCommandHelpListsSubcommands(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	for _, want := range []string{"Usage:", "send", "hot-channel", "activate-channels", "message-event"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected capacity help to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestWorkerCommandAllowsExplicitInsecureControl(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	cfg, code := parseWorkerConfig([]string{"--listen", "127.0.0.1:0", "--insecure-control=true"}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d and stderr %q", code, stderr.String())
	}
	if !cfg.server.InsecureControl {
		t.Fatalf("expected insecure control to be enabled")
	}
}

func TestWorkerCommandInsecureControlIgnoresEnvToken(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "from-env")
	var stderr bytes.Buffer

	cfg, code := parseWorkerConfig([]string{"--listen", "127.0.0.1:0", "--insecure-control=true"}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d and stderr %q", code, stderr.String())
	}
	if !cfg.server.InsecureControl {
		t.Fatalf("expected insecure control to be enabled")
	}
	if cfg.server.ControlToken != "" {
		t.Fatalf("expected insecure control to clear effective token, got %q", cfg.server.ControlToken)
	}
}

func TestWorkerCommandChatLifecycleModeRequiresAuthenticatedDedicatedRuntime(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	_, code := parseWorkerConfig([]string{"--mode", "chat-lifecycle", "--listen", "127.0.0.1:0"}, &stderr)
	if code != exitConfig || !strings.Contains(stderr.String(), "--control-token is required") {
		t.Fatalf("missing-token code/stderr = %d/%q", code, stderr.String())
	}

	stderr.Reset()
	_, code = parseWorkerConfig([]string{"--mode", "chat-lifecycle", "--control-token", "secret", "--insecure-control"}, &stderr)
	if code != exitConfig || !strings.Contains(stderr.String(), "does not allow --insecure-control") {
		t.Fatalf("insecure code/stderr = %d/%q", code, stderr.String())
	}

	stderr.Reset()
	cfg, code := parseWorkerConfig([]string{"--mode", "chat-lifecycle", "--listen", "127.0.0.1:19091", "--control-token", "secret"}, &stderr)
	if code != 0 || cfg.mode != workerModeChatLifecycle || cfg.listen != "127.0.0.1:19091" || cfg.server.ControlToken != "secret" {
		t.Fatalf("chat lifecycle config/code/stderr = %+v/%d/%q", cfg, code, stderr.String())
	}
}

func TestWorkerCommandDefaultModePreservesGenericWorkerBehavior(t *testing.T) {
	var stderr bytes.Buffer
	cfg, code := parseWorkerConfig([]string{"--control-token", "secret", "--work-dir", "/tmp/wkbench-worker"}, &stderr)
	if code != 0 {
		t.Fatalf("parse code = %d; stderr = %q", code, stderr.String())
	}
	if cfg.mode != workerModeDefault || cfg.listen != "127.0.0.1:19090" || cfg.server.WorkDir != "/tmp/wkbench-worker" || cfg.server.ControlToken != "secret" {
		t.Fatalf("default worker config = %+v", cfg)
	}
}

func TestWorkerCommandRejectsUnknownChatLifecycleFlagBeforeServing(t *testing.T) {
	var stderr bytes.Buffer
	_, code := parseWorkerConfig([]string{"--mode", "chat-lifecycle", "--control-token", "secret", "--chat-lease-timeout", "1s"}, &stderr)
	if code != exitConfig {
		t.Fatalf("unknown flag code/stderr = %d/%q", code, stderr.String())
	}
}

func TestChatLifecycleWorkerUnexpectedGenerationExitStopsServerAndReturnsError(t *testing.T) {
	t.Parallel()

	runtime := &fakeChatLifecycleWorkerRuntime{
		serveStarted: make(chan struct{}),
		shutdown:     make(chan struct{}),
	}
	unexpected := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- waitChatLifecycleWorker(runtime, unexpected) }()

	<-runtime.serveStarted
	close(unexpected)
	if err := <-result; !errors.Is(err, errChatLifecycleWorkerUnexpected) {
		t.Fatalf("wait error = %v", err)
	}
	select {
	case <-runtime.shutdown:
	default:
		t.Fatal("dedicated HTTP runtime was not shut down")
	}
}

type fakeChatLifecycleWorkerRuntime struct {
	serveStarted chan struct{}
	shutdown     chan struct{}
	shutdownOnce sync.Once
}

func (r *fakeChatLifecycleWorkerRuntime) Serve() error {
	close(r.serveStarted)
	<-r.shutdown
	return http.ErrServerClosed
}

func (r *fakeChatLifecycleWorkerRuntime) Shutdown(context.Context) error {
	r.shutdownOnce.Do(func() { close(r.shutdown) })
	return nil
}

func TestRunCapacityRequiresSubcommand(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	for _, want := range []string{"Usage:", "send", "hot-channel", "activate-channels"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected capacity help to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestRunCapacitySendRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "send"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestRunCapacityHotChannelRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "hot-channel"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestRunCapacityActivateChannelsRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "activate-channels"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestRunCapacityMessageEventRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "message-event"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestParseCapacityHotChannelConfig(t *testing.T) {
	var stderr bytes.Buffer

	cfg, code := parseCapacityHotChannelConfig([]string{
		"--api", "http://127.0.0.1:5001",
		"--gateway", "127.0.0.1:5100",
		"--senders", "32",
		"--start-qps", "1000",
		"--max-qps", "2000",
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d stderr %q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001" {
		t.Fatalf("api addrs = %q", got)
	}
	if got := strings.Join(cfg.GatewayTCPAddrs, ","); got != "127.0.0.1:5100" {
		t.Fatalf("gateway addrs = %q", got)
	}
	if cfg.Senders != 32 {
		t.Fatalf("senders = %d, want 32", cfg.Senders)
	}
	if cfg.StartQPS != 1000 || cfg.MaxQPS != 2000 {
		t.Fatalf("qps range = %v..%v, want 1000..2000", cfg.StartQPS, cfg.MaxQPS)
	}
}

func TestParseCapacityMessageEventConfig(t *testing.T) {
	var stderr bytes.Buffer

	cfg, code := parseCapacityMessageEventConfig([]string{
		"--api", "http://127.0.0.1:5001,http://127.0.0.1:5002",
		"--run-id", "message-event-cli",
		"--channels", "100",
		"--streams-per-channel", "3",
		"--lanes-per-stream", "4",
		"--deltas-per-lane", "5",
		"--payload-bytes", "128",
		"--concurrency", "64",
		"--request-timeout", "3s",
		"--report-dir", "/tmp/message-event-report",
		"--warm-channels",
		"--warm-runtime",
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d stderr %q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001,http://127.0.0.1:5002" {
		t.Fatalf("api addrs = %q", got)
	}
	if cfg.RunID != "message-event-cli" {
		t.Fatalf("run id = %q", cfg.RunID)
	}
	if cfg.Channels != 100 || cfg.StreamsPerChannel != 3 || cfg.LanesPerStream != 4 || cfg.DeltasPerLane != 5 {
		t.Fatalf("shape = %+v", cfg)
	}
	if cfg.PayloadBytes != 128 || cfg.Concurrency != 64 || cfg.RequestTimeout != 3*time.Second {
		t.Fatalf("payload/concurrency/timeout = %d/%d/%s", cfg.PayloadBytes, cfg.Concurrency, cfg.RequestTimeout)
	}
	if cfg.ReportDir != "/tmp/message-event-report" {
		t.Fatalf("report dir = %q", cfg.ReportDir)
	}
	if !cfg.WarmChannels {
		t.Fatalf("warm channels = false, want true")
	}
	if !cfg.WarmRuntime {
		t.Fatalf("warm runtime = false, want true")
	}
	if shape := cfg.Shape(); shape != (messageevent.Shape{Streams: 300, DeltaEvents: 6000, FinishEvents: 300, ExpectedDurableEvents: 1500, ExpectedFinishProposals: 300}) {
		t.Fatalf("shape = %+v", shape)
	}
}

func TestParseCapacityActivateChannelsConfig(t *testing.T) {
	var stderr bytes.Buffer

	cfg, code := parseCapacityActivateChannelsConfig([]string{
		"--api", "http://127.0.0.1:5001,http://127.0.0.1:5002",
		"--gateway", "127.0.0.1:5100",
		"--bench-token", "secret",
		"--run-id", "activate-test",
		"--channels", "1234",
		"--users", "2345",
		"--group-members", "12",
		"--prepare-rate", "321",
		"--connect-rate", "123",
		"--activation-concurrency", "345",
		"--activation-window", "3s",
		"--hold", "4s",
		"--hold-probe-interval", "500ms",
		"--probe-batch-size", "111",
		"--stable-p99", "250ms",
		"--max-sendack-error-rate", "0.01",
		"--max-connect-error-rate", "0.02",
		"--evict-after=true",
		"--report-dir", "/tmp/activate-report",
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d stderr %q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001,http://127.0.0.1:5002" {
		t.Fatalf("api addrs = %q", got)
	}
	if got := strings.Join(cfg.GatewayTCPAddrs, ","); got != "127.0.0.1:5100" {
		t.Fatalf("gateway addrs = %q", got)
	}
	if cfg.BenchToken != "secret" || cfg.RunID != "activate-test" {
		t.Fatalf("token/run id = %q/%q", cfg.BenchToken, cfg.RunID)
	}
	if cfg.Channels != 1234 || cfg.Users != 2345 || cfg.GroupMembers != 12 {
		t.Fatalf("shape = channels %d users %d members %d", cfg.Channels, cfg.Users, cfg.GroupMembers)
	}
	if cfg.PrepareRatePerSecond != 321 || cfg.ConnectRatePerSecond != 123 {
		t.Fatalf("rates = prepare %.3f connect %.3f", cfg.PrepareRatePerSecond, cfg.ConnectRatePerSecond)
	}
	if cfg.ActivationConcurrency != 345 || cfg.ActivationWindow != 3*time.Second {
		t.Fatalf("activation = concurrency %d window %s", cfg.ActivationConcurrency, cfg.ActivationWindow)
	}
	if cfg.Hold != 4*time.Second || cfg.HoldProbeInterval != 500*time.Millisecond || cfg.ProbeBatchSize != 111 {
		t.Fatalf("hold/probe = %s %s %d", cfg.Hold, cfg.HoldProbeInterval, cfg.ProbeBatchSize)
	}
	if cfg.StableP99 != 250*time.Millisecond || cfg.MaxSendackErrorRate != 0.01 || cfg.MaxConnectErrorRate != 0.02 {
		t.Fatalf("limits = %s %.3f %.3f", cfg.StableP99, cfg.MaxSendackErrorRate, cfg.MaxConnectErrorRate)
	}
	if !cfg.EvictAfter || cfg.ReportDir != "/tmp/activate-report" {
		t.Fatalf("evict/report = %t %q", cfg.EvictAfter, cfg.ReportDir)
	}
}

func TestRunCapacityActivateChannelsUsesRunnerAndWritesReport(t *testing.T) {
	origDiscover := discoverActivateChannelsTarget
	origNewRunner := newActivateChannelsRunner
	defer func() {
		discoverActivateChannelsTarget = origDiscover
		newActivateChannelsRunner = origNewRunner
	}()
	var discoveredCfg capacity.ActivateChannelsConfig
	var runnerCfg capacity.ActivateChannelsConfig
	discoverActivateChannelsTarget = func(_ context.Context, cfg capacity.ActivateChannelsConfig) (capacity.DiscoveredTarget, error) {
		discoveredCfg = cfg
		return capacity.DiscoveredTarget{}, nil
	}
	newActivateChannelsRunner = func(cfg capacity.ActivateChannelsConfig, discovered capacity.DiscoveredTarget) activateChannelsRunner {
		runnerCfg = cfg
		return fakeActivateChannelsRunner{result: capacity.ActivateChannelsResult{
			Status: capacity.StatusPassed,
			Config: capacity.ActivateChannelsReportConfig{
				RunID:                 cfg.RunID,
				Channels:              cfg.Channels,
				Users:                 cfg.Users,
				GroupMembers:          cfg.GroupMembers,
				PrepareRatePerSecond:  cfg.PrepareRatePerSecond,
				ConnectRatePerSecond:  cfg.ConnectRatePerSecond,
				ActivationConcurrency: cfg.ActivationConcurrency,
				ActivationWindow:      cfg.ActivationWindow,
				Hold:                  cfg.Hold,
				ProbeBatchSize:        cfg.ProbeBatchSize,
				EvictAfter:            cfg.EvictAfter,
			},
			Evaluation: capacity.ActivateChannelsEvaluation{
				Passed:            true,
				ActivationSuccess: uint64(cfg.Channels),
				ActiveLeaderTotal: cfg.Channels,
			},
			ReportDir: cfg.ReportDir,
		}}
	}
	reportDir := t.TempDir()
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"capacity", "activate-channels",
		"--api", "http://127.0.0.1:5001",
		"--gateway", "127.0.0.1:5100",
		"--run-id", "activate-cli",
		"--channels", "4",
		"--users", "8",
		"--group-members", "2",
		"--activation-window", "10ms",
		"--hold", "0s",
		"--report-dir", reportDir,
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d stderr %q", code, stderr.String())
	}
	if discoveredCfg.RunID != "activate-cli" || runnerCfg.Channels != 4 {
		t.Fatalf("unexpected cfgs: discovered=%+v runner=%+v", discoveredCfg, runnerCfg)
	}
	if !strings.Contains(stderr.String(), "wkbench activate-channels") {
		t.Fatalf("expected console summary, got %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(reportDir, "activation_report.json")); err != nil {
		t.Fatalf("expected activation report: %v", err)
	}
	if _, err := os.Stat(filepath.Join(reportDir, "summary.md")); err != nil {
		t.Fatalf("expected summary: %v", err)
	}
}

type fakeActivateChannelsRunner struct {
	result capacity.ActivateChannelsResult
	err    error
}

func (f fakeActivateChannelsRunner) Run(context.Context) (capacity.ActivateChannelsResult, error) {
	return f.result, f.err
}

func TestMetricsClassifyReportsGatewayPressureFromPrometheusSnapshots(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.01"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.05"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="+Inf"} 0
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_worker_inflight{pool="store_append"} 0
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="+Inf"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.01"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="1"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="10"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_gateway_async_send_queue_depth 70
wukongim_gateway_async_send_queue_capacity 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.01"} 10
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.05"} 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="+Inf"} 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_worker_inflight{pool="store_append"} 128
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 256
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="+Inf"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.01"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="1"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="10"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="+Inf"} 100
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "classification: gateway_dispatch") {
		t.Fatalf("expected gateway classification, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "gateway_queue_ratio: 0.700") {
		t.Fatalf("expected queue ratio in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_apply_p99_seconds:") {
		t.Fatalf("expected channel stage metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_runtime_append_wait_p99_seconds:") {
		t.Fatalf("expected channel runtime append wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_batch_wait_p99_seconds:") {
		t.Fatalf("expected channel append batch wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_store_wait_p99_seconds:") {
		t.Fatalf("expected channel append store wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "storage_commit_request_p99_seconds:") {
		t.Fatalf("expected storage commit request metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "storage_commit_request_over_10s_count:") {
		t.Fatalf("expected storage commit request tail count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_worker_inflight{pool=\"store_append\"}: 128") {
		t.Fatalf("expected channel worker inflight in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_worker_inflight_peak{pool=\"store_append\"}: 256") {
		t.Fatalf("expected channel worker inflight peak in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "storage_commit_request_over_10s_count{lane=\"append\"}: 0") {
		t.Fatalf("expected storage commit request lane tail count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_post_store_commit_wait_p99_seconds:") {
		t.Fatalf("expected channel append post-store commit wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_follower_pull_wait_p99_seconds:") {
		t.Fatalf("expected channel append quorum follower pull wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_ack_offset_wait_p99_seconds:") {
		t.Fatalf("expected channel append quorum ack offset wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_hw_advance_wait_p99_seconds:") {
		t.Fatalf("expected channel append quorum HW advance wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_final_complete_p99_seconds:") {
		t.Fatalf("expected channel append quorum final complete metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_pull_hint_to_submit_p99_seconds:") {
		t.Fatalf("expected follower pull hint to submit metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_pull_rpc_p99_seconds:") {
		t.Fatalf("expected follower pull RPC metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_store_apply_p99_seconds:") {
		t.Fatalf("expected follower store apply metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_apply_to_ack_return_p99_seconds:") {
		t.Fatalf("expected follower apply to AckOffset return metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_slot_read_p99_seconds:") {
		t.Fatalf("expected channel meta breakdown metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_propose_p99_seconds:") {
		t.Fatalf("expected channel meta propose metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_propose_forward_p99_seconds:") {
		t.Fatalf("expected channel meta propose forward metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_slot_propose_wait_p99_seconds:") {
		t.Fatalf("expected channel meta Slot propose wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_slot_raft_commit_wait_p99_seconds:") {
		t.Fatalf("expected channel meta Slot raft commit wait metric in output, got %q", stderr.String())
	}
}

func TestMetricsClassifyPromotesChannelRuntimeOutputKeys(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_channelv2_worker_inflight{pool="store_append"} 0
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_channelv2_worker_inflight{pool="store_append"} 128
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 256
wukongim_channelv2_worker_queue_depth{pool="store_append"} 1
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	output := stderr.String()
	for _, want := range []string{
		"classification: channel_append",
		"channel_worker_inflight{pool=\"store_append\"}: 128",
		"channel_worker_inflight_peak{pool=\"store_append\"}: 256",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected promoted channel key %q in output, got %q", want, output)
		}
	}
	for _, unwanted := range []string{
		"classification: channelv2_append",
		"channelv2_worker_inflight",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output should not expose legacy channelv2 report key %q, got %q", unwanted, output)
		}
	}
}

func TestMetricsClassifyReportsChannelRuntimePullBatchMetrics(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	beforeMetrics := `
wukongim_channel_pull_batch_items_bucket{result="ok",le="2"} 0
wukongim_channel_pull_batch_items_bucket{result="ok",le="4"} 0
wukongim_channel_pull_batch_items_bucket{result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_records_bucket{result="ok",le="8"} 0
wukongim_channel_pull_batch_records_bucket{result="ok",le="16"} 0
wukongim_channel_pull_batch_records_bucket{result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_payload_bytes_bucket{result="ok",le="256"} 0
wukongim_channel_pull_batch_payload_bytes_bucket{result="ok",le="512"} 0
wukongim_channel_pull_batch_payload_bytes_bucket{result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="0.005"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="0.25"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="0.1"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="0.5"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="+Inf"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="0.1"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="+Inf"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="mailbox_wait",le="0.1"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="mailbox_wait",le="+Inf"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="ack_apply",le="0.005"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="ack_apply",le="+Inf"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="handler",le="0.25"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="handler",le="+Inf"} 0
wukongim_channel_leader_pull_completed_waiters_bucket{le="1"} 0
wukongim_channel_leader_pull_completed_waiters_bucket{le="2"} 0
wukongim_channel_leader_pull_completed_waiters_bucket{le="+Inf"} 0
`
	if err := os.WriteFile(before, []byte(beforeMetrics), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(strings.ReplaceAll(beforeMetrics, "} 0", "} 10")), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	for _, want := range []string{
		"channel_pull_batch_items_p50: 1.000",
		"channel_pull_batch_items_p99: 1.980",
		"channel_pull_batch_records_p50: 4.000",
		"channel_pull_batch_records_p99: 7.920",
		"channel_pull_batch_payload_bytes_p50: 128.000",
		"channel_pull_batch_payload_bytes_p99: 253.440",
		"channel_pull_batch_submit_p99_seconds: 0.004950",
		"channel_pull_batch_await_p99_seconds: 0.247500",
		"channel_pull_batch_max_sequential_await_p99_seconds: 0.099000",
		"channel_pull_batch_total_p99_seconds: 0.495000",
		"channel_worker_rpc_queue_wait_p99_seconds: 0.099000",
		"channel_leader_pull_mailbox_wait_p99_seconds: 0.099000",
		"channel_leader_pull_ack_apply_p99_seconds: 0.004950",
		"channel_leader_pull_handler_p99_seconds: 0.247500",
		"channel_leader_pull_completed_waiters_p50: 0.500",
		"channel_leader_pull_completed_waiters_p99: 0.990",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected PullBatch output %q, got %q", want, stderr.String())
		}
	}
}

func TestMetricsClassifyReportsMessageEventPressureFromPrometheusSnapshots(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_message_event_stream_cache_sessions 0
wukongim_message_event_stream_cache_open_lanes 0
wukongim_message_event_stream_cache_payload_bytes 0
wukongim_message_event_stream_cache_max_sessions 1024
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 1
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="cache_miss"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.01"} 0
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="+Inf"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.01"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="+Inf"} 0
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="4"} 0
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="8"} 0
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_message_event_stream_cache_sessions 3
wukongim_message_event_stream_cache_open_lanes 5
wukongim_message_event_stream_cache_payload_bytes 2048
wukongim_message_event_stream_cache_max_sessions 1024
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 9
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="cache_miss"} 2
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 2
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.01"} 1
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="+Inf"} 2
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.01"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="+Inf"} 2
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="4"} 1
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="8"} 2
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="+Inf"} 2
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	for _, want := range []string{
		"message_event_stream_cache_sessions_max: 3",
		"message_event_stream_cache_open_lanes_max: 5",
		"message_event_stream_cache_payload_bytes_max: 2048",
		"message_event_append_count{path=\"cache\"}: 8",
		"message_event_append_count{result=\"cache_miss\"}: 2",
		"message_event_propose_count{path=\"finish_batch\"}: 2",
		"message_event_append_stage_p99_seconds{path=\"finish_batch\",stage=\"finish_batch_build\"}:",
		"message_event_propose_stage_p99_seconds{path=\"finish_batch\",stage=\"slot_propose_wait\"}:",
		"message_event_propose_batch_events_p99{path=\"finish_batch\"}:",
		"message_event_cache_miss_count: 2",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected message event output %q, got %q", want, stderr.String())
		}
	}
}

func TestMetricsClassifyReportsChannelRuntimePullHintCounters(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_channelv2_pull_hint_total{reason="append",result="submitted",error="none"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="ok",error="none"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="stale_meta"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="canceled"} 0
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="remote_error"} 0
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_resolve",result="err",error="channel_not_found"} 1
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_hint",result="ok",error="none"} 2
wukongim_channelv2_pending_meta_current{reactor_id="0"} 1
wukongim_channelv2_pending_meta_total{event="created",error="none"} 1
wukongim_channelv2_pending_meta_total{event="converted",error="none"} 0
wukongim_channelv2_pending_meta_total{event="released",error="timeout"} 2
wukongim_channelv2_pending_meta_total{event="released",error="not_ready"} 0
wukongim_channelv2_need_meta_pull_total{result="submitted",error="none"} 4
wukongim_channelv2_need_meta_pull_total{result="ok",error="none"} 2
wukongim_channelv2_need_meta_pull_total{result="retry",error="other"} 1
wukongim_channelv2_need_meta_pull_total{result="err",error="timeout"} 2
wukongim_channelv2_need_meta_pull_total{result="err",error="not_ready"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_channelv2_pull_hint_total{reason="append",result="submitted",error="none"} 4
wukongim_channelv2_pull_hint_total{reason="append",result="ok",error="none"} 3
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="stale_meta"} 5
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="canceled"} 6
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="remote_error"} 7
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_resolve",result="err",error="channel_not_found"} 9
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_hint",result="ok",error="none"} 13
wukongim_channelv2_pending_meta_current{reactor_id="0"} 4
wukongim_channelv2_pending_meta_total{event="created",error="none"} 9
wukongim_channelv2_pending_meta_total{event="converted",error="none"} 5
wukongim_channelv2_pending_meta_total{event="released",error="timeout"} 5
wukongim_channelv2_pending_meta_total{event="released",error="not_ready"} 2
wukongim_channelv2_need_meta_pull_total{result="submitted",error="none"} 14
wukongim_channelv2_need_meta_pull_total{result="ok",error="none"} 7
wukongim_channelv2_need_meta_pull_total{result="retry",error="other"} 4
wukongim_channelv2_need_meta_pull_total{result="err",error="timeout"} 6
wukongim_channelv2_need_meta_pull_total{result="err",error="not_ready"} 2
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.01"} 2
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.05"} 5
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="+Inf"} 5
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_submitted_count: 3") {
		t.Fatalf("expected PullHint submitted count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_ok_count: 2") {
		t.Fatalf("expected PullHint ok count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_err_count: 17") {
		t.Fatalf("expected PullHint err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_stale_meta_err_count: 4") {
		t.Fatalf("expected PullHint stale meta err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_canceled_err_count: 6") {
		t.Fatalf("expected PullHint canceled err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_remote_err_count: 7") {
		t.Fatalf("expected PullHint remote err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_receive_meta_resolve_err_count: 8") {
		t.Fatalf("expected PullHint receive meta resolve err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_receive_channel_not_found_err_count: 8") {
		t.Fatalf("expected PullHint receive channel not found err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_receive_meta_hint_ok_count: 11") {
		t.Fatalf("expected PullHint receive meta hint ok count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_current_max: 4") {
		t.Fatalf("expected PendingMeta current gauge in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_created_count: 8") {
		t.Fatalf("expected PendingMeta created count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_converted_count: 5") {
		t.Fatalf("expected PendingMeta converted count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_released_count: 5") {
		t.Fatalf("expected PendingMeta released count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_timeout_release_count: 3") {
		t.Fatalf("expected PendingMeta timeout release count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_submitted_count: 10") {
		t.Fatalf("expected NeedMeta submitted count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_ok_count: 5") {
		t.Fatalf("expected NeedMeta ok count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_retry_count: 3") {
		t.Fatalf("expected NeedMeta retry count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_err_count: 6") {
		t.Fatalf("expected NeedMeta err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_timeout_err_count: 4") {
		t.Fatalf("expected NeedMeta timeout err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_not_ready_err_count: 2") {
		t.Fatalf("expected NeedMeta not ready err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_rpc_p99_seconds:") {
		t.Fatalf("expected NeedMeta pull RPC p99 in output, got %q", stderr.String())
	}
}

func TestMetricsClassifyReportsControllerRaftStepPressureFromPrometheusSnapshots(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_controller_raft_step_queue_depth 0
wukongim_controller_raft_step_queue_capacity 1024
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="0.25"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_controller_raft_step_queue_depth 1024
wukongim_controller_raft_step_queue_capacity 1024
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="0.25"} 3
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="+Inf"} 3
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "classification: controller_raft_step") {
		t.Fatalf("expected controller classification, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "controller_raft_step_queue_ratio: 1.000") {
		t.Fatalf("expected controller queue ratio in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "controller_raft_step_enqueue_err_count: 3") {
		t.Fatalf("expected controller enqueue error count in output, got %q", stderr.String())
	}
}

func TestValidateCommandLoadsConfigsAndBuildsPlanWithoutNetwork(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
name: target
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected validate success, got code %d stderr %q", code, stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForInvalidConfig(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: false
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "bench_api.enabled") {
		t.Fatalf("expected bench_api.enabled error, got %q", stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForMissingWorkerAddr(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "workers[0].addr") {
		t.Fatalf("expected worker addr error, got %q", stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForMissingWorkerToken(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "control_token") {
		t.Fatalf("expected control token error, got %q", stderr.String())
	}
}

func TestDoctorCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"doctor", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 2 {
		t.Fatalf("expected preflight exit code 2, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "preflight failed") {
		t.Fatalf("expected preflight error, got %q", stderr.String())
	}
}

func TestDoctorCommandRunsWithoutScenario(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeWkbenchJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"users_tokens_batch":        true,
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer targetSrv.Close()
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePath(t, r, "/v1/info")
		requireHeader(t, r, "Authorization", "Bearer secret")
		writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
	}))
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"doctor", "--target", targetPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected doctor success, got code %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandCompletesWorkloadOrchestration(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench workload orchestration completed") {
		t.Fatalf("expected workload orchestration note, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "fake/no-op") {
		t.Fatalf("did not expect stale fake/no-op note, got %q", stderr.String())
	}
}

func TestRunCommandPhasePollTimeoutAllowsSlowConnectPhase(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := delayedConnectWkbenchWorkerServer(t, "secret", 40*time.Millisecond)
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath, "--phase-poll-timeout", "120ms"}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandPhasePollTimeoutFailsWhenPhaseExceedsConfiguredWindow(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := delayedConnectWkbenchWorkerServer(t, "secret", 40*time.Millisecond)
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath, "--phase-poll-timeout", "5ms"}, &stderr)

	if code != exitWorker {
		t.Fatalf("expected worker exit code, got code %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "worker phase poll timeout") {
		t.Fatalf("expected phase poll timeout error, got %q", stderr.String())
	}
}

func TestRunCommandWritesReportDirectoryWhenConfigured(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	reportDir := t.TempDir()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAMLWithReportDir(reportDir))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
	for _, rel := range []string{"report.json", "summary.md", "coordinator.log", "metrics/worker-1s.jsonl", "errors/samples.jsonl"} {
		if _, err := os.Stat(filepath.Join(reportDir, rel)); err != nil {
			t.Fatalf("expected report artifact %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(reportDir, "workers", "w1.report.json")); err != nil {
		t.Fatalf("expected worker report artifact: %v", err)
	}
}

func TestRunCommandReturnsInternalExitCodeWhenReportWriteFails(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	reportDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(reportDir, []byte("file blocks report dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAMLWithReportDir(reportDir))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 6 {
		t.Fatalf("expected internal exit code 6, got %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 2 {
		t.Fatalf("expected preflight exit code 2, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "preflight failed") {
		t.Fatalf("expected preflight error, got %q", stderr.String())
	}
}

func TestRunCommandReturnsWorkerExitCodeForPhaseFailure(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	phase := "idle"
	assignment := map[string]string{}
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if !validateWkbenchTestControlIdentity(t, w, r, assignment) {
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/assign":
			assignment = readWkbenchTestAssignment(t, r)
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			http.Error(w, "connect failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 4 {
		t.Fatalf("expected worker exit code 4, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "worker run failed") {
		t.Fatalf("expected worker failure error, got %q", stderr.String())
	}
}

func TestRunCommandReturnsHardLimitExitCodeWhenCollectionAlsoFails(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := hardLimitAndCollectionFailureWorkerServer(t, "secret")
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML()+`
limits:
  hard:
    max_sendack_error_rate: 0
`)
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 3 {
		t.Fatalf("expected hard-limit exit code 3, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "hard limit failed") {
		t.Fatalf("expected hard limit error, got %q", stderr.String())
	}
}

func TestValidateCommandRequiresConfigFlags(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", "target.yaml"}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--scenario is required") {
		t.Fatalf("expected missing scenario error, got %q", stderr.String())
	}
}

func TestDevSimCommandReturnsConfigExitCodeForMissingConfig(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"dev-sim", "--config", filepath.Join(t.TempDir(), "missing.yaml")}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "config validation failed") {
		t.Fatalf("expected config validation error, got %q", stderr.String())
	}
}

func TestDevSimCommandHelp(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"dev-sim", "--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench dev-sim") || !strings.Contains(stderr.String(), "--status-listen") {
		t.Fatalf("expected dev-sim help, got %q", stderr.String())
	}
}

func writeWkbenchTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validScenarioYAML() string {
	return `
version: wkbench/v1
run:
  id: bench-run
online:
  total_users: 10
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 1
      members:
        count: 5
messages:
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 1/s
`
}

func validScenarioYAMLWithReportDir(reportDir string) string {
	return `
version: wkbench/v1
run:
  id: bench-run
  report_dir: ` + reportDir + `
online:
  total_users: 10
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 1
      members:
        count: 5
messages:
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 1/s
`
}

func validTargetYAML(apiAddr string) string {
	return `
name: target
api:
  addrs: [` + apiAddr + `]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`
}

func goodWkbenchTargetServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeWkbenchJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"users_tokens_batch":        true,
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func goodWkbenchWorkerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	phase := "assigned"
	assignment := map[string]string{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if !validateWkbenchTestControlIdentity(t, w, r, assignment) {
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			assignment = readWkbenchTestAssignment(t, r)
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			phase = "connect"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			writeWkbenchJSON(t, w, map[string]any{"worker_id": "w1"})
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func delayedConnectWkbenchWorkerServer(t *testing.T, token string, delay time.Duration) *httptest.Server {
	t.Helper()
	phase := "assigned"
	completedPhase := ""
	activePhase := ""
	var connectStarted time.Time
	assignment := map[string]string{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if !validateWkbenchTestControlIdentity(t, w, r, assignment) {
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			assignment = readWkbenchTestAssignment(t, r)
			phase = "assigned"
			completedPhase = ""
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			completedPhase = "prepare"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/connect":
			activePhase = "connect"
			connectStarted = time.Now()
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "active_phase": activePhase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			completedPhase = "warmup"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			completedPhase = "run"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			completedPhase = "cooldown"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/status":
			if activePhase == "connect" && time.Since(connectStarted) >= delay {
				phase = "connect"
				completedPhase = "connect"
				activePhase = ""
			}
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "active_phase": activePhase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			writeWkbenchJSON(t, w, map[string]any{"worker_id": "w1"})
		case "/v1/stop":
			phase = "stopped"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func hardLimitAndCollectionFailureWorkerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	phase := "assigned"
	assignment := map[string]string{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if !validateWkbenchTestControlIdentity(t, w, r, assignment) {
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			assignment = readWkbenchTestAssignment(t, r)
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			phase = "connect"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{"person_send_success_total": 9, "person_send_error_total": 1}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			http.Error(w, "report exploded", http.StatusInternalServerError)
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func readWkbenchTestAssignment(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	var assignment struct {
		RunID        string `json:"run_id"`
		AssignmentID string `json:"assignment_id"`
		WorkerID     string `json:"worker_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&assignment); err != nil {
		t.Fatalf("decode worker assignment: %v", err)
	}
	if assignment.RunID == "" || assignment.AssignmentID == "" || assignment.WorkerID == "" {
		t.Fatalf("worker assignment identity is incomplete: %+v", assignment)
	}
	return map[string]string{
		"run_id":        assignment.RunID,
		"assignment_id": assignment.AssignmentID,
		"worker_id":     assignment.WorkerID,
	}
}

func validateWkbenchTestControlIdentity(t *testing.T, w http.ResponseWriter, r *http.Request, assignment map[string]string) bool {
	t.Helper()
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/phase/") || r.URL.Path == "/v1/stop":
		var request struct {
			RunID        string `json:"run_id"`
			AssignmentID string `json:"assignment_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode %s identity: %v", r.URL.Path, err)
			http.Error(w, "invalid assignment identity", http.StatusBadRequest)
			return false
		}
		if request.RunID != assignment["run_id"] || request.AssignmentID != assignment["assignment_id"] {
			t.Errorf("%s identity = %q/%q, want %q/%q", r.URL.Path, request.RunID, request.AssignmentID, assignment["run_id"], assignment["assignment_id"])
			http.Error(w, "active assignment conflict", http.StatusConflict)
			return false
		}
	case r.URL.Path == "/v1/metrics" || r.URL.Path == "/v1/report":
		runID := r.URL.Query().Get("run_id")
		assignmentID := r.URL.Query().Get("assignment_id")
		if runID != assignment["run_id"] || assignmentID != assignment["assignment_id"] {
			t.Errorf("%s identity = %q/%q, want %q/%q", r.URL.Path, runID, assignmentID, assignment["run_id"], assignment["assignment_id"])
			http.Error(w, "active assignment conflict", http.StatusConflict)
			return false
		}
	}
	return true
}

func writeWkbenchJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func requirePath(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if r.URL.Path != want {
		t.Fatalf("path = %s, want %s", r.URL.Path, want)
	}
}

func requireHeader(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	if got := r.Header.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
