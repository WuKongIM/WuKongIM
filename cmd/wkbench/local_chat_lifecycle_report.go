package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/spf13/cobra"
)

const localChatLifecycleStepSchemaV1 = "wukongim/chat-lifecycle-local-step/v1"

var localStepStorageHeader = []string{
	"tag", "node", "evidence", "commit_queue_depth_max", "physical_commits_delta",
	"logical_requests_delta", "records_delta", "bytes_delta", "avg_requests_per_commit",
	"avg_records_per_commit", "collect_avg_ms", "build_avg_ms", "commit_avg_ms",
	"publish_avg_ms", "total_avg_ms", "request_count_delta", "request_avg_ms",
	"request_ok_delta", "request_ok_avg_ms", "request_timeout_delta", "request_timeout_avg_ms",
	"request_canceled_delta", "request_canceled_avg_ms", "request_error_delta", "request_error_avg_ms",
	"leader_append_request_delta", "leader_append_request_avg_ms",
	"follower_apply_request_delta", "follower_apply_request_avg_ms",
	"message_append_request_delta", "message_append_request_avg_ms",
	"wal_bytes_in_delta", "wal_bytes_written_delta", "wal_write_amplification",
	"flush_bytes_delta", "flush_count_delta", "compaction_bytes_read_delta",
	"compaction_bytes_written_delta", "compaction_count_delta", "sstable_size_max",
	"compaction_debt_max", "compactions_in_progress_max", "read_amplification_max", "disk_usage_max",
	"avg_bytes_per_commit", "requests_per_commit_p50", "requests_per_commit_p95", "requests_per_commit_p99",
	"records_per_commit_p50", "records_per_commit_p95", "records_per_commit_p99",
	"bytes_per_commit_p50", "bytes_per_commit_p95", "bytes_per_commit_p99",
}

var localStepProcessHeader = []string{"name", "alive"}

var localStepProductQueueHeader = []string{
	"tag", "node", "evidence", "baseline_queue", "baseline_inflight",
	"drained_queue", "drained_inflight", "converged",
}

var localStepHostIOHeader = []string{
	"tag", "host", "evidence", "physical_device", "iops_available", "iops_max",
	"bytes_per_second_available", "bytes_per_second_max", "utilization_available",
	"utilization_percent_max", "service_time_available", "service_time_milliseconds_max",
	"read_write_split_available",
}

// localChatLifecycleStepOutcome separates product and rate failures from confounded evidence.
type localChatLifecycleStepOutcome string

const (
	localChatLifecycleStepClean                localChatLifecycleStepOutcome = "clean"
	localChatLifecycleStepRateFailed           localChatLifecycleStepOutcome = "rate_failed"
	localChatLifecycleStepProductFailure       localChatLifecycleStepOutcome = "product_failure"
	localChatLifecycleStepStorageConfounded    localChatLifecycleStepOutcome = "storage_confounded"
	localChatLifecycleStepHostConfounded       localChatLifecycleStepOutcome = "host_confounded"
	localChatLifecycleStepInsufficientEvidence localChatLifecycleStepOutcome = "insufficient_evidence"
)

// localChatLifecycleHarnessFailureReason is a closed wrapper-failure
// vocabulary. These failures never become product or local-capacity verdicts.
type localChatLifecycleHarnessFailureReason string

const (
	localChatLifecycleHarnessFailureNone                               localChatLifecycleHarnessFailureReason = ""
	localChatLifecycleHarnessFailureCoordinatorGracefulStopTimeout     localChatLifecycleHarnessFailureReason = "coordinator_graceful_stop_timeout"
	localChatLifecycleHarnessFailureCoordinatorExitedBeforeStopRequest localChatLifecycleHarnessFailureReason = "coordinator_exited_before_stop_request"
)

func validLocalChatLifecycleHarnessFailureReason(reason localChatLifecycleHarnessFailureReason) bool {
	switch reason {
	case localChatLifecycleHarnessFailureNone,
		localChatLifecycleHarnessFailureCoordinatorGracefulStopTimeout,
		localChatLifecycleHarnessFailureCoordinatorExitedBeforeStopRequest:
		return true
	default:
		return false
	}
}

// localChatLifecycleStepOptions declares the measured rate contract for one step.
type localChatLifecycleStepOptions struct {
	OfferedRatePerSecond     uint64
	MeasuredDuration         time.Duration
	MinimumThroughputPercent uint64
}

// localChatLifecycleStepEvidence records whether every required evidence cut is closed.
type localChatLifecycleStepEvidence struct {
	QualificationReportComplete  bool
	FinalReportComplete          bool
	StorageComplete              bool
	HostIOComplete               bool
	ProductMetricsComplete       bool
	ProductQueueEvidenceComplete bool
	ProductQueuesConverged       bool
	ProcessesContinuous          bool
	TimelineComplete             bool
	ProfileEvidenceComplete      bool
	OperatorInterrupted          bool
	HarnessFailureReason         localChatLifecycleHarnessFailureReason
	HostConfounded               bool
}

// localChatLifecycleStepResult is the non-formal typed result consumed by the staircase.
type localChatLifecycleStepResult struct {
	Schema                       string                                 `json:"schema"`
	Outcome                      localChatLifecycleStepOutcome          `json:"outcome"`
	Reason                       string                                 `json:"reason"`
	OfferedRatePerSecond         uint64                                 `json:"offered_rate_per_second"`
	ActualRatePerSecond          float64                                `json:"actual_rate_per_second"`
	MinimumThroughputPercent     uint64                                 `json:"minimum_throughput_percent"`
	MeasuredDurationSeconds      float64                                `json:"measured_duration_seconds"`
	QualificationReached         bool                                   `json:"qualification_reached"`
	TargetConnections            int                                    `json:"target_connections"`
	OnlineConnections            int                                    `json:"online_connections"`
	Sent                         uint64                                 `json:"sent"`
	Acknowledged                 uint64                                 `json:"acknowledged"`
	Expected                     uint64                                 `json:"expected"`
	MinimumFilesystemFreePct     float64                                `json:"minimum_filesystem_free_percent"`
	StorageEvidenceComplete      bool                                   `json:"storage_evidence_complete"`
	HostIOEvidenceComplete       bool                                   `json:"host_io_evidence_complete"`
	ProductMetricsComplete       bool                                   `json:"product_metrics_complete"`
	ProductQueueEvidenceComplete bool                                   `json:"product_queue_evidence_complete"`
	ProductQueuesConverged       bool                                   `json:"product_queues_converged"`
	ProcessContinuityComplete    bool                                   `json:"process_continuity_complete"`
	TimelineEvidenceComplete     bool                                   `json:"timeline_evidence_complete"`
	ProfileEvidenceComplete      bool                                   `json:"profile_evidence_complete"`
	OperatorInterrupted          bool                                   `json:"operator_interrupted"`
	HarnessFailureReason         localChatLifecycleHarnessFailureReason `json:"harness_failure_reason"`
}

const localChatLifecycleProfileStatusSchemaV1 = "wukongim/chat-lifecycle-threshold-pprof-status/v1"

type localChatLifecycleProfileStatus struct {
	Schema             string                   `json:"schema"`
	Status             string                   `json:"status"`
	EvidenceComplete   bool                     `json:"evidence_complete"`
	CaptureValid       bool                     `json:"capture_valid"`
	Reason             string                   `json:"reason"`
	TriggerKind        localTimelineTriggerKind `json:"trigger_kind"`
	TriggerPreviousUTC string                   `json:"trigger_previous_utc"`
	TriggerCurrentUTC  string                   `json:"trigger_current_utc"`
	Metadata           string                   `json:"metadata"`
	HelperExitStatus   *int                     `json:"helper_exit_status,omitempty"`
}

type localThresholdPprofMetadata struct {
	Schema  string `json:"schema"`
	Trigger struct {
		Kind          localTimelineTriggerKind `json:"kind"`
		ObservedPhase string                   `json:"observed_phase"`
		PreviousUTC   time.Time                `json:"previous_utc"`
		CurrentUTC    time.Time                `json:"current_utc"`
	} `json:"trigger"`
	Capture struct {
		Status         string    `json:"status"`
		Valid          bool      `json:"valid"`
		Reason         string    `json:"reason"`
		StartPhase     string    `json:"start_phase"`
		EndPhase       string    `json:"end_phase"`
		StartedAtUTC   time.Time `json:"started_at_utc"`
		CompletedAtUTC time.Time `json:"completed_at_utc"`
		CPUSeconds     int       `json:"cpu_seconds"`
	} `json:"capture"`
	Nodes []struct {
		Node      string `json:"node"`
		CPU       string `json:"cpu"`
		Heap      string `json:"heap"`
		Goroutine string `json:"goroutine"`
	} `json:"nodes"`
}

func newLocalChatLifecycleStepReportCommand() *cobra.Command {
	var beforePath, afterPath, storagePath, hostIOPath, productQueuePath, processPath, timelinePath, profileStatusPath, runID, outputPath string
	var offeredRate, minimumThroughput uint64
	var measuredDuration time.Duration
	var hostConfounded, operatorInterrupted bool
	var harnessFailureReason string
	cmd := &cobra.Command{
		Use:   "local-chat-lifecycle-step",
		Short: "Classify one non-formal local chat-lifecycle rate step",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if offeredRate == 0 || measuredDuration < time.Second || measuredDuration%time.Second != 0 ||
				minimumThroughput == 0 || minimumThroughput > 100 {
				return commandExit{code: exitConfig, message: "--offered-rate, whole-second --measured-duration, and --minimum-throughput-percent are required"}
			}
			typedHarnessFailure := localChatLifecycleHarnessFailureReason(harnessFailureReason)
			if !validLocalChatLifecycleHarnessFailureReason(typedHarnessFailure) {
				return commandExit{code: exitConfig, message: "--harness-failure-reason is unsupported"}
			}
			before, beforeErr := chatlifecycle.ReadReport(beforePath)
			after, afterErr := chatlifecycle.ReadReport(afterPath)
			expectedTag := "rate-" + strconv.FormatUint(offeredRate, 10)
			storageComplete, storageErr := readLocalStepStorageEvidence(storagePath, expectedTag)
			hostIOComplete, hostIOErr := readLocalStepHostIOEvidence(hostIOPath, expectedTag)
			productQueueComplete, productQueuesConverged, productQueueErr := readLocalStepProductQueueEvidence(productQueuePath, expectedTag)
			processesContinuous, processErr := readLocalStepProcessContinuity(processPath)
			timeline, timelineComplete, timelineErr := readLocalStepTimelineEvidence(
				timelinePath, runID, offeredRate, minimumThroughput, measuredDuration,
			)
			if timelineComplete && (beforeErr == nil) != timeline.QualificationCutPresent {
				timelineComplete = false
				timelineErr = errors.New("local timeline qualification disagrees with the report")
			}
			profileComplete, profileErr := readLocalStepProfileEvidence(profileStatusPath, timeline)
			evidence := localChatLifecycleStepEvidence{
				QualificationReportComplete: beforeErr == nil, FinalReportComplete: afterErr == nil,
				StorageComplete: storageComplete, HostIOComplete: hostIOComplete,
				ProductMetricsComplete: beforeErr == nil && afterErr == nil &&
					localChatLifecycleProductMetricsComplete(before, after),
				ProductQueueEvidenceComplete: productQueueComplete, ProductQueuesConverged: productQueuesConverged,
				ProcessesContinuous: processesContinuous, HostConfounded: hostConfounded,
				TimelineComplete: timelineComplete, ProfileEvidenceComplete: profileComplete,
				OperatorInterrupted:  operatorInterrupted,
				HarnessFailureReason: typedHarnessFailure,
			}
			if storageErr != nil {
				evidence.StorageComplete = false
			}
			if hostIOErr != nil {
				evidence.HostIOComplete = false
			}
			if productQueueErr != nil {
				evidence.ProductQueueEvidenceComplete = false
			}
			if processErr != nil {
				evidence.ProcessesContinuous = false
			}
			if timelineErr != nil {
				evidence.TimelineComplete = false
			}
			if profileErr != nil {
				evidence.ProfileEvidenceComplete = false
			}
			result := classifyLocalChatLifecycleStep(before, after, evidence, localChatLifecycleStepOptions{
				OfferedRatePerSecond: offeredRate, MeasuredDuration: measuredDuration,
				MinimumThroughputPercent: minimumThroughput,
			})
			if err := writeLocalChatLifecycleStepResult(outputPath, result); err != nil {
				return commandExit{code: exitInternal, message: "local chat-lifecycle result write failed"}
			}
			return exitCodeError(localChatLifecycleStepExitCode(result.Outcome))
		},
	}
	cmd.Flags().StringVar(&beforePath, "before", "", "qualification report captured immediately after warmup")
	cmd.Flags().StringVar(&afterPath, "after", "", "terminal report captured after bounded drain")
	cmd.Flags().StringVar(&storagePath, "storage-summary", "", "normalized three-node storage summary TSV")
	cmd.Flags().StringVar(&hostIOPath, "host-io-summary", "", "normalized four-host physical I/O summary TSV")
	cmd.Flags().StringVar(&productQueuePath, "product-queue-summary", "", "normalized post-drain product queue summary TSV")
	cmd.Flags().StringVar(&processPath, "process-continuity", "", "closed process continuity TSV")
	cmd.Flags().StringVar(&timelinePath, "timeline", "", "versioned unified chat-lifecycle timeline JSON")
	cmd.Flags().StringVar(&profileStatusPath, "profile-status", "", "versioned threshold pprof status JSON")
	cmd.Flags().StringVar(&runID, "run-id", "", "exact local chat-lifecycle run ID")
	cmd.Flags().StringVar(&outputPath, "output", "", "typed local step JSON output")
	cmd.Flags().Uint64Var(&offeredRate, "offered-rate", 0, "offered SEND rate per second")
	cmd.Flags().DurationVar(&measuredDuration, "measured-duration", 0, "post-warmup measured interval")
	cmd.Flags().Uint64Var(&minimumThroughput, "minimum-throughput-percent", 90, "minimum actual/offered SENDACK percentage")
	cmd.Flags().BoolVar(&hostConfounded, "host-confounded", false, "mark overlapping WuKongIM workload evidence")
	cmd.Flags().BoolVar(&operatorInterrupted, "operator-interrupted", false, "record that an operator signal ended the measured step")
	cmd.Flags().StringVar(&harnessFailureReason, "harness-failure-reason", "", "closed local wrapper failure reason")
	for _, name := range []string{"before", "after", "storage-summary", "host-io-summary", "product-queue-summary", "process-continuity", "timeline", "profile-status", "run-id", "output", "offered-rate", "measured-duration"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func readLocalStepTimelineEvidence(
	path string,
	runID string,
	offeredRate uint64,
	minimumThroughput uint64,
	measuredDuration time.Duration,
) (localChatLifecycleUnifiedTimeline, bool, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return localChatLifecycleUnifiedTimeline{}, false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	var timeline localChatLifecycleUnifiedTimeline
	if err := decoder.Decode(&timeline); err != nil {
		return localChatLifecycleUnifiedTimeline{}, false, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return localChatLifecycleUnifiedTimeline{}, false, errors.New("local timeline has trailing JSON")
	}
	complete := strings.TrimSpace(runID) != "" && timeline.Schema == localChatLifecycleUnifiedTimelineSchemaV1 &&
		timeline.RunID == runID && timeline.OfferedRatePerSecond == offeredRate &&
		timeline.MinimumThroughputPercent == minimumThroughput &&
		timeline.SourceCompleteness.WorkerStatusCutsComplete && timeline.SourceCompleteness.BoundaryTimelineComplete &&
		localChatLifecycleTimelineStorageOverlapComplete(timeline) &&
		timeline.SourceCompleteness.TerminalCutPresent && !timeline.SourceCompleteness.PartialWorkerLogLine &&
		timeline.SourceCompleteness.FirstBreachObservable && len(timeline.Points) > 0 &&
		localChatLifecycleTimelineWindowsComplete(timeline, measuredDuration)
	if !complete {
		return timeline, false, errors.New("local timeline evidence is incomplete")
	}
	return timeline, true, nil
}

func localChatLifecycleTimelineStorageOverlapComplete(timeline localChatLifecycleUnifiedTimeline) bool {
	if !timeline.SourceCompleteness.StorageOverlapComplete {
		return false
	}
	for _, evidence := range []localTimelineOverlapEvidence{timeline.Overlap.Compaction, timeline.Overlap.Snapshot} {
		if !evidence.SourceComplete || evidence.Status != "observed" && evidence.Status != "not_observed" ||
			evidence.Status == "observed" && len(evidence.Windows) == 0 ||
			evidence.Status == "not_observed" && len(evidence.Windows) != 0 {
			return false
		}
		var previousCurrent time.Time
		for _, window := range evidence.Windows {
			if window.Phase == "" || !window.CurrentAt.After(window.PreviousAt) ||
				!previousCurrent.IsZero() && window.CurrentAt.Before(previousCurrent) || len(window.Nodes) == 0 {
				return false
			}
			previousCurrent = window.CurrentAt
			previousNode := ""
			for _, node := range window.Nodes {
				if !validLocalStorageNode(node) || node <= previousNode {
					return false
				}
				previousNode = node
			}
		}
	}
	return true
}

func localChatLifecycleTimelineWindowsComplete(timeline localChatLifecycleUnifiedTimeline, measuredDuration time.Duration) bool {
	if measuredDuration < time.Second || measuredDuration%time.Second != 0 {
		return false
	}
	required := []string{"warmup", "drain", "shutdown"}
	if timeline.QualificationCutPresent {
		required = append(required, "measured")
	}
	for _, name := range required {
		window, ok := timeline.Windows[name]
		if !ok || !window.Complete || window.StartAt == nil || window.EndAt == nil || window.EndAt.Before(*window.StartAt) {
			return false
		}
	}
	if timeline.QualificationCutPresent {
		window := timeline.Windows["measured"]
		// Wrapper boundaries are second precision while the monotonic deadline
		// can begin late within that second. A two-second tolerance detects early
		// termination without inventing sub-second precision the evidence lacks.
		minimum := measuredDuration - 2*time.Second
		if minimum < 0 {
			minimum = 0
		}
		if window.EndAt.Sub(*window.StartAt) < minimum {
			return false
		}
	} else if measured, ok := timeline.Windows["measured"]; ok && measured.Complete {
		return false
	}
	requiredBoundaryCounts := map[string]int{
		"warmup_start": 0, "warmup_end": 0, "drain_start": 0, "drain_end": 0, "shutdown_start": 0,
	}
	if timeline.QualificationCutPresent {
		requiredBoundaryCounts["measurement_start"] = 0
		requiredBoundaryCounts["measurement_end"] = 0
	}
	for _, point := range timeline.Points {
		if point.Source != "boundary" || point.BoundaryNode != "boundary" {
			continue
		}
		if _, required := requiredBoundaryCounts[point.Kind]; required {
			requiredBoundaryCounts[point.Kind]++
		}
	}
	for _, count := range requiredBoundaryCounts {
		if count != 1 {
			return false
		}
	}
	warmup, drain, shutdown := timeline.Windows["warmup"], timeline.Windows["drain"], timeline.Windows["shutdown"]
	if warmup.EndAt.After(*drain.StartAt) || drain.EndAt.After(*shutdown.StartAt) {
		return false
	}
	if timeline.QualificationCutPresent {
		measured := timeline.Windows["measured"]
		if warmup.EndAt.After(*measured.StartAt) || measured.EndAt.After(*drain.StartAt) {
			return false
		}
	}
	return true
}

func readLocalStepProfileEvidence(path string, timeline localChatLifecycleUnifiedTimeline) (bool, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var status localChatLifecycleProfileStatus
	if err := decoder.Decode(&status); err != nil {
		return false, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF || status.Schema != localChatLifecycleProfileStatusSchemaV1 ||
		!status.EvidenceComplete || strings.TrimSpace(status.Reason) == "" {
		return false, errors.New("local threshold profile evidence is incomplete")
	}
	measured := timeline.MeasuredFirstBreach
	switch status.Status {
	case "not_triggered":
		if !status.CaptureValid || status.TriggerKind != "" || status.TriggerPreviousUTC != "" ||
			status.TriggerCurrentUTC != "" || status.Metadata != "" || measured.Observed {
			return false, errors.New("local threshold profile evidence contradicts the measured timeline")
		}
	case "complete", "partial":
		if !measured.Observed || measured.PreviousAt == nil || measured.CurrentAt == nil ||
			status.TriggerKind != measured.TriggerKind || status.Metadata != "threshold-pprof/metadata.json" {
			return false, errors.New("local threshold profile evidence contradicts the measured timeline")
		}
		previous, previousErr := time.Parse(time.RFC3339Nano, status.TriggerPreviousUTC)
		current, currentErr := time.Parse(time.RFC3339Nano, status.TriggerCurrentUTC)
		if previousErr != nil || currentErr != nil || !previous.Equal(measured.PreviousAt.UTC()) ||
			!current.Equal(measured.CurrentAt.UTC()) || !current.After(previous) {
			return false, errors.New("local threshold profile trigger bracket is invalid")
		}
		if (status.Status == "complete" && !status.CaptureValid) || (status.Status != "complete" && status.CaptureValid) {
			return false, errors.New("local threshold profile validity is inconsistent")
		}
		if err := validateLocalThresholdPprofMetadata(path, status, previous, current); err != nil {
			return false, err
		}
	case "invalid":
		return false, errors.New("local threshold profile capture did not start in the measured phase")
	default:
		return false, errors.New("local threshold profile status is invalid")
	}
	return true, nil
}

func validateLocalThresholdPprofMetadata(
	statusPath string,
	status localChatLifecycleProfileStatus,
	previous time.Time,
	current time.Time,
) error {
	metadataPath := filepath.Join(filepath.Dir(filepath.Clean(statusPath)), filepath.FromSlash(status.Metadata))
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil || !metadataInfo.Mode().IsRegular() || metadataInfo.Size() <= 0 {
		return errors.New("local threshold profile metadata is missing or unsafe")
	}
	file, err := os.Open(metadataPath)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var metadata localThresholdPprofMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("local threshold profile metadata has trailing JSON")
	}
	if metadata.Schema != "wukongim.local_threshold_pprof/v1" || metadata.Trigger.Kind != status.TriggerKind ||
		metadata.Trigger.ObservedPhase != "measurement" ||
		!metadata.Trigger.PreviousUTC.Equal(previous) || !metadata.Trigger.CurrentUTC.Equal(current) ||
		metadata.Capture.Status != status.Status || metadata.Capture.Valid != status.CaptureValid ||
		metadata.Capture.Reason != status.Reason || metadata.Capture.StartedAtUTC.IsZero() ||
		metadata.Capture.CompletedAtUTC.Before(metadata.Capture.StartedAtUTC) ||
		metadata.Capture.CPUSeconds < 1 || metadata.Capture.CPUSeconds > 30 || len(metadata.Nodes) != 3 {
		return errors.New("local threshold profile metadata identity is inconsistent")
	}
	validPhase := func(phase string) bool {
		switch phase {
		case "warmup", "measurement", "drain", "shutdown", "missing", "invalid":
			return true
		default:
			return false
		}
	}
	if !validPhase(metadata.Capture.StartPhase) || !validPhase(metadata.Capture.EndPhase) {
		return errors.New("local threshold profile phase is invalid")
	}
	allComplete, allMissing := true, true
	profilesDir := filepath.Join(filepath.Dir(metadataPath), "profiles")
	for index, node := range metadata.Nodes {
		if node.Node != "node-"+strconv.Itoa(index+1) {
			return errors.New("local threshold profile node identity is invalid")
		}
		profiles := []struct {
			status string
			path   string
		}{
			{node.CPU, filepath.Join(profilesDir, node.Node+"-cpu.pb.gz")},
			{node.Heap, filepath.Join(profilesDir, node.Node+"-heap.pb.gz")},
			{node.Goroutine, filepath.Join(profilesDir, node.Node+"-goroutine.txt")},
		}
		for _, profile := range profiles {
			switch profile.status {
			case "complete":
				allMissing = false
				info, statErr := os.Lstat(profile.path)
				if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
					return errors.New("local threshold profile blob is missing or unsafe")
				}
			case "missing":
				allComplete = false
				if _, statErr := os.Lstat(profile.path); !os.IsNotExist(statErr) {
					return errors.New("local threshold profile blob contradicts missing metadata")
				}
			default:
				return errors.New("local threshold profile blob status is invalid")
			}
		}
	}
	switch metadata.Capture.Status {
	case "complete":
		if metadata.Capture.Reason != "ok" || metadata.Capture.StartPhase != "measurement" ||
			metadata.Capture.EndPhase != "measurement" || !allComplete {
			return errors.New("complete local threshold profile metadata is inconsistent")
		}
	case "partial":
		switch metadata.Capture.Reason {
		case "phase_changed_during_capture":
			if metadata.Capture.StartPhase != "measurement" || metadata.Capture.EndPhase == "measurement" {
				return errors.New("cross-phase local threshold profile metadata is inconsistent")
			}
		case "profile_capture_missing":
			if metadata.Capture.StartPhase != "measurement" || metadata.Capture.EndPhase != "measurement" || allComplete {
				return errors.New("partial local threshold profile metadata is inconsistent")
			}
		case "capture_start_missed_measurement":
			if metadata.Capture.StartPhase == "measurement" || metadata.Capture.EndPhase != metadata.Capture.StartPhase || !allMissing {
				return errors.New("missed-start local threshold profile metadata is inconsistent")
			}
		case "interrupted", "internal_error":
		default:
			return errors.New("partial local threshold profile reason is invalid")
		}
	case "invalid":
		if metadata.Capture.StartPhase == "measurement" || !allMissing {
			return errors.New("invalid local threshold profile metadata is inconsistent")
		}
		switch metadata.Capture.Reason {
		case "phase_state_missing_at_start", "phase_state_invalid_at_start", "phase_not_measurement_at_start":
		default:
			return errors.New("invalid local threshold profile reason is invalid")
		}
	default:
		return errors.New("local threshold profile capture status is invalid")
	}
	return nil
}

func readLocalStepProductQueueEvidence(path, expectedTag string) (complete, converged bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer file.Close()
	rows, err := readLocalStepTSV(file, localStepProductQueueHeader)
	if err != nil || len(rows) != 3 {
		return false, false, errors.New("local product queue evidence is incomplete")
	}
	seen := map[string]bool{}
	var totals [4]uint64
	rowConvergence := make([]bool, 0, len(rows))
	for _, row := range rows {
		if row[0] != expectedTag || row[2] != "complete" || seen[row[1]] {
			return false, false, errors.New("local product queue evidence is incomplete")
		}
		for index, column := range []int{3, 4, 5, 6} {
			value, parseErr := strconv.ParseUint(row[column], 10, 64)
			if parseErr != nil || value > ^uint64(0)-totals[index] {
				return false, false, errors.New("local product queue evidence is incomplete")
			}
			totals[index] += value
		}
		rowConverged, parseErr := strconv.ParseBool(row[7])
		if parseErr != nil {
			return false, false, errors.New("local product queue evidence is incomplete")
		}
		rowConvergence = append(rowConvergence, rowConverged)
		seen[row[1]] = true
	}
	for _, node := range []string{"node-1", "node-2", "node-3"} {
		if !seen[node] {
			return false, false, errors.New("local product queue evidence is incomplete")
		}
	}
	// Queue work can migrate between service nodes while the cluster remains
	// healthy. Compare the sealed cluster totals instead of requiring three
	// independent instantaneous node snapshots to fall below their own cuts.
	converged = totals[2] <= totals[0] && totals[3] <= totals[1]
	for _, rowConverged := range rowConvergence {
		if rowConverged != converged {
			return false, false, errors.New("local product queue convergence does not match sealed cluster totals")
		}
	}
	return true, converged, nil
}

func localChatLifecycleProductMetricsComplete(before, after chatlifecycle.Report) bool {
	beforeResources, afterResources := before.Resources.Capacity, after.Resources.Capacity
	beforeClusterSamples := before.Cluster.HealthySamples + before.Cluster.UnhealthySamples
	afterClusterSamples := after.Cluster.HealthySamples + after.Cluster.UnhealthySamples
	return beforeResources.Complete && afterResources.Complete &&
		beforeResources.ProcessesComplete && afterResources.ProcessesComplete &&
		beforeResources.WorkerQueuesComplete && afterResources.WorkerQueuesComplete &&
		beforeResources.Samples > 0 && afterResources.Samples > beforeResources.Samples &&
		beforeResources.WorkerQueueSamples > 0 && afterResources.WorkerQueueSamples > beforeResources.WorkerQueueSamples &&
		afterResources.MissingSamples == beforeResources.MissingSamples &&
		afterResources.WorkerQueueMissingSamples == beforeResources.WorkerQueueMissingSamples &&
		beforeClusterSamples > 0 && afterClusterSamples > beforeClusterSamples
}

func readLocalStepHostIOEvidence(path, expectedTag string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	rows, err := readLocalStepTSV(file, localStepHostIOHeader)
	if err != nil || len(rows) != 4 {
		return false, errors.New("local host I/O evidence is incomplete")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row[0] != expectedTag || row[2] != "complete" && row[2] != "unavailable" ||
			strings.TrimSpace(row[3]) == "" || seen[row[1]] {
			return false, errors.New("local host I/O evidence is incomplete")
		}
		seen[row[1]] = true
		anyValueAvailable := false
		for _, pair := range [][2]int{{4, 5}, {6, 7}, {8, 9}, {10, 11}} {
			available, parseErr := strconv.ParseUint(row[pair[0]], 10, 1)
			if parseErr != nil || available == 0 && row[pair[1]] != "unavailable" {
				return false, errors.New("local host I/O evidence is incomplete")
			}
			if available == 1 {
				anyValueAvailable = true
				value, valueErr := strconv.ParseFloat(row[pair[1]], 64)
				if valueErr != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
					return false, errors.New("local host I/O evidence is incomplete")
				}
			}
		}
		split, parseErr := strconv.ParseUint(row[12], 10, 1)
		if parseErr != nil || split > 1 || split == 1 && (row[4] != "1" || row[6] != "1") ||
			row[2] == "unavailable" && anyValueAvailable || row[2] == "complete" && !anyValueAvailable {
			return false, errors.New("local host I/O evidence is incomplete")
		}
	}
	for _, host := range []string{"host-node-1", "host-node-2", "host-node-3", "host-load"} {
		if !seen[host] {
			return false, errors.New("local host I/O evidence is incomplete")
		}
	}
	return true, nil
}

func readLocalStepStorageEvidence(path, expectedTag string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	rows, err := readLocalStepTSV(file, localStepStorageHeader)
	if err != nil || len(rows) != 3 {
		return false, errors.New("local storage evidence is incomplete")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row[0] != expectedTag || row[2] != "complete" || seen[row[1]] {
			return false, errors.New("local storage evidence is incomplete")
		}
		values := make([]float64, len(row)-3)
		for index, raw := range row[3:] {
			value, parseErr := strconv.ParseFloat(raw, 64)
			if parseErr != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return false, errors.New("local storage evidence is incomplete")
			}
			values[index] = value
		}
		for _, column := range []int{4, 5, 6, 7, 15, 31, 32} {
			if values[column-3] <= 0 {
				return false, errors.New("local storage evidence has no measured activity")
			}
		}
		seen[row[1]] = true
	}
	for _, node := range []string{"node-1", "node-2", "node-3"} {
		if !seen[node] {
			return false, errors.New("local storage evidence is incomplete")
		}
	}
	return true, nil
}

func readLocalStepProcessContinuity(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	rows, err := readLocalStepTSV(file, localStepProcessHeader)
	if err != nil || len(rows) != 11 {
		return false, errors.New("local process continuity is incomplete")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		alive, parseErr := strconv.ParseBool(row[1])
		if parseErr != nil || !alive || seen[row[0]] {
			return false, errors.New("local process continuity is incomplete")
		}
		seen[row[0]] = true
	}
	expected := []string{"service-1", "service-2", "service-3", "worker-1", "worker-2", "worker-3",
		"host-metrics-1", "host-metrics-2", "host-metrics-3", "host-metrics-load", "process-metrics-collector"}
	for _, name := range expected {
		if !seen[name] {
			return false, errors.New("local process continuity is incomplete")
		}
	}
	return true, nil
}

func readLocalStepTSV(reader io.Reader, header []string) ([][]string, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 1<<20))
	scanner.Buffer(make([]byte, 4096), 256<<10)
	var rows [][]string
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if first {
			first = false
			if len(fields) != len(header) {
				return nil, errors.New("local evidence TSV has an invalid header")
			}
			for index := range header {
				if fields[index] != header[index] {
					return nil, errors.New("local evidence TSV has an invalid header")
				}
			}
			continue
		}
		if len(fields) != len(header) {
			return nil, errors.New("local evidence TSV has an invalid row")
		}
		rows = append(rows, fields)
	}
	if err := scanner.Err(); err != nil || first {
		return nil, errors.New("local evidence TSV is unreadable")
	}
	return rows, nil
}

func writeLocalChatLifecycleStepResult(path string, result localChatLifecycleStepResult) error {
	if strings.TrimSpace(path) == "" || filepath.Base(path) == "." {
		return errors.New("local result path is invalid")
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(filepath.Clean(path), body, 0o600)
}

func localChatLifecycleStepExitCode(outcome localChatLifecycleStepOutcome) int {
	switch outcome {
	case localChatLifecycleStepClean:
		return 0
	case localChatLifecycleStepRateFailed, localChatLifecycleStepProductFailure:
		return exitHardLimit
	case localChatLifecycleStepStorageConfounded, localChatLifecycleStepHostConfounded:
		return exitPreflight
	default:
		return exitInternal
	}
}

func classifyLocalChatLifecycleStep(
	before chatlifecycle.Report,
	after chatlifecycle.Report,
	evidence localChatLifecycleStepEvidence,
	options localChatLifecycleStepOptions,
) localChatLifecycleStepResult {
	result := localChatLifecycleStepResult{
		Schema: localChatLifecycleStepSchemaV1, Outcome: localChatLifecycleStepInsufficientEvidence,
		Reason: "invalid_or_missing_evidence", OfferedRatePerSecond: options.OfferedRatePerSecond,
		MinimumThroughputPercent: options.MinimumThroughputPercent,
		MeasuredDurationSeconds:  options.MeasuredDuration.Seconds(), QualificationReached: evidence.QualificationReportComplete,
		TargetConnections: before.Sessions.Target, OnlineConnections: before.Sessions.Online,
		StorageEvidenceComplete: evidence.StorageComplete, ProductMetricsComplete: evidence.ProductMetricsComplete,
		ProductQueueEvidenceComplete: evidence.ProductQueueEvidenceComplete, ProductQueuesConverged: evidence.ProductQueuesConverged,
		HostIOEvidenceComplete: evidence.HostIOComplete, ProcessContinuityComplete: evidence.ProcessesContinuous,
		TimelineEvidenceComplete: evidence.TimelineComplete, ProfileEvidenceComplete: evidence.ProfileEvidenceComplete,
		OperatorInterrupted:  evidence.OperatorInterrupted,
		HarnessFailureReason: evidence.HarnessFailureReason,
	}
	if options.OfferedRatePerSecond == 0 || options.MeasuredDuration < time.Second || options.MeasuredDuration%time.Second != 0 ||
		options.MinimumThroughputPercent == 0 || options.MinimumThroughputPercent > 100 {
		return result
	}
	if !validLocalChatLifecycleHarnessFailureReason(evidence.HarnessFailureReason) {
		return result
	}
	if evidence.HarnessFailureReason != localChatLifecycleHarnessFailureNone {
		result.Reason = string(evidence.HarnessFailureReason)
		return result
	}
	if evidence.OperatorInterrupted {
		result.Reason = "operator_interrupted"
		return result
	}
	if evidence.HostConfounded {
		result.Outcome, result.Reason = localChatLifecycleStepHostConfounded, "overlapping_wukongim_workload"
		return result
	}
	if !evidence.QualificationReportComplete && evidence.FinalReportComplete &&
		evidence.ProcessesContinuous && evidence.TimelineComplete && evidence.ProfileEvidenceComplete &&
		validLocalChatLifecycleTerminalReport(after) &&
		localChatLifecycleProductFailure(after) {
		minimumFree, ok := minimumLocalChatLifecycleFilesystemFreePercent(after)
		if !ok {
			return result
		}
		result.MinimumFilesystemFreePct = minimumFree
		result.TargetConnections = after.Sessions.Target
		// The terminal report is sampled after worker shutdown, so this field
		// must retain the actual terminal online count rather than presenting
		// the configured target as observed connectivity. Earlier online cuts
		// remain in the bounded worker-status diagnostics and unified timeline.
		result.OnlineConnections = after.Sessions.Online
		result.Sent = after.Messages.Sent
		result.Acknowledged = after.Messages.SendAcknowledged
		// Qualification never established a measured window. Retain only the
		// cumulative terminal counters; Expected and ActualRatePerSecond must
		// remain zero rather than manufacturing a rate from warmup traffic.
		if minimumFree < 10 {
			result.Outcome, result.Reason = localChatLifecycleStepStorageConfounded, "filesystem_free_below_10_percent"
			return result
		}
		result.Outcome, result.Reason = localChatLifecycleStepProductFailure, "terminal_product_failure_before_qualification"
		return result
	}
	if !evidence.StorageComplete || !evidence.HostIOComplete || !evidence.ProductMetricsComplete ||
		!evidence.ProductQueueEvidenceComplete || !evidence.ProcessesContinuous || !evidence.TimelineComplete ||
		!evidence.ProfileEvidenceComplete {
		return result
	}
	if !sameLocalChatLifecycleStep(before, after) || !after.Final || !after.Verdict.Terminal ||
		after.Window.End.Before(before.Window.End) || before.Sessions.Target < 2500 || before.Sessions.Online < 2500 {
		return result
	}
	minimumFree, ok := minimumLocalChatLifecycleFilesystemFreePercent(after)
	if !ok {
		return result
	}
	result.MinimumFilesystemFreePct = minimumFree
	if minimumFree < 10 {
		result.Outcome, result.Reason = localChatLifecycleStepStorageConfounded, "filesystem_free_below_10_percent"
		return result
	}
	if before.Messages.SendAcknowledged > before.Messages.Sent {
		return result
	}
	sent, sentOK := localStepCounterDelta(before.Messages.Sent, after.Messages.Sent)
	// A qualification cut may contain warmup SENDs whose SENDACK arrives during
	// the measured interval. Final drain makes those warmup SENDs exact, so the
	// measured acknowledgement population starts after the warmup SEND boundary,
	// not after the earlier acknowledgement counter value.
	acknowledged, acknowledgedOK := localStepCounterDelta(before.Messages.Sent, after.Messages.SendAcknowledged)
	if !sentOK || !acknowledgedOK || options.OfferedRatePerSecond > math.MaxUint64/uint64(options.MeasuredDuration/time.Second) {
		return result
	}
	result.Sent, result.Acknowledged = sent, acknowledged
	result.Expected = options.OfferedRatePerSecond * uint64(options.MeasuredDuration/time.Second)
	result.ActualRatePerSecond = float64(acknowledged) / options.MeasuredDuration.Seconds()

	if localChatLifecycleProductFailure(after) {
		result.Outcome, result.Reason = localChatLifecycleStepProductFailure, "terminal_or_correctness_failure"
		return result
	}
	minimumAcknowledged := minimumLocalStepAcknowledged(result.Expected, options.MinimumThroughputPercent)
	if acknowledged < minimumAcknowledged || acknowledged != sent ||
		localChatLifecycleWorkRemaining(after) || !evidence.ProductQueuesConverged {
		result.Outcome, result.Reason = localChatLifecycleStepRateFailed, "underdelivery_or_incomplete_drain"
		return result
	}
	result.Outcome, result.Reason = localChatLifecycleStepClean, "complete"
	return result
}

func minimumLocalStepAcknowledged(expected, percent uint64) uint64 {
	whole := expected / 100 * percent
	remainder := expected % 100 * percent
	return whole + (remainder+99)/100
}

func sameLocalChatLifecycleStep(before, after chatlifecycle.Report) bool {
	return before.ConfigDigest != "" && before.ConfigDigest == after.ConfigDigest &&
		before.Fence == after.Fence && before.Topology == after.Topology && before.Topology.Validated &&
		before.Topology.LogicalSlotGroups == 12 && before.Topology.HashSlots == 256 &&
		before.Topology.SlotReplicas == 3 && before.Topology.ChannelReplicas == 3
}

func validLocalChatLifecycleTerminalReport(report chatlifecycle.Report) bool {
	return report.Final && report.Verdict.Terminal && report.ConfigDigest != "" && report.Topology.Validated &&
		report.Topology.LogicalSlotGroups == 12 && report.Topology.HashSlots == 256 &&
		report.Topology.SlotReplicas == 3 && report.Topology.ChannelReplicas == 3 &&
		report.Sessions.Target >= 2500
}

func minimumLocalChatLifecycleFilesystemFreePercent(report chatlifecycle.Report) (float64, bool) {
	minimum := 101.0
	for _, node := range report.Resources.Nodes {
		if node.DataFilesystemBytes == 0 || node.DataFilesystemAvailableBytes > node.DataFilesystemBytes {
			return 0, false
		}
		free := float64(node.DataFilesystemAvailableBytes) * 100 / float64(node.DataFilesystemBytes)
		if free < minimum {
			minimum = free
		}
	}
	return minimum, minimum <= 100
}

func localChatLifecycleProductFailure(report chatlifecycle.Report) bool {
	message := report.Messages
	verdictLatency := report.Verdict.LatencyWarnings
	reportLatency := report.Latency.Warnings
	return report.Verdict.Outcome == chatlifecycle.VerdictProductFailure ||
		report.EvidenceClassification == chatlifecycle.SyncClassificationProductFailure ||
		verdictLatency.Hot > 0 || verdictLatency.Cold > 0 || verdictLatency.Sync > 0 ||
		reportLatency.Hot > 0 || reportLatency.Cold > 0 || reportLatency.Sync > 0 ||
		message.Terminal > 0 || message.Losses > 0 || message.Duplicates > 0 ||
		message.Corruptions > 0 || message.SequenceRegressions > 0 ||
		report.Sync.Failures > 0 || report.Lifecycle.ProductFailures > 0 || report.MetaCreate.Errors > 0
}

func localChatLifecycleWorkRemaining(report chatlifecycle.Report) bool {
	return report.Verdict.Outcome != chatlifecycle.VerdictOperatorStop ||
		report.Harness.Failures > 0 || report.Harness.CommandSaturation > 0 ||
		report.Harness.OfferedUnderdelivery > 0 || report.Harness.DrainTimedOut || report.Harness.UnexpectedExit ||
		report.Correlation.PendingUnfinished != 0 || report.Correlation.Outstanding != 0 ||
		report.Queues.WorkCurrent != 0 || report.Queues.RetryCurrent != 0 ||
		report.Queues.InflightCurrent != 0 || report.Queues.TransportCurrent != 0
}

func localStepCounterDelta(before, after uint64) (uint64, bool) {
	if after < before {
		return 0, false
	}
	return after - before, true
}
