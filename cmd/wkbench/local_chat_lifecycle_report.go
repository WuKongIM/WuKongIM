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
}

var localStepProcessHeader = []string{"name", "alive"}

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

// localChatLifecycleStepOptions declares the measured rate contract for one step.
type localChatLifecycleStepOptions struct {
	OfferedRatePerSecond     uint64
	MeasuredDuration         time.Duration
	MinimumThroughputPercent uint64
}

// localChatLifecycleStepEvidence records whether every required evidence cut is closed.
type localChatLifecycleStepEvidence struct {
	StorageComplete        bool
	HostIOComplete         bool
	ProductMetricsComplete bool
	ProcessesContinuous    bool
	HostConfounded         bool
}

// localChatLifecycleStepResult is the non-formal typed result consumed by the staircase.
type localChatLifecycleStepResult struct {
	Schema                    string                        `json:"schema"`
	Outcome                   localChatLifecycleStepOutcome `json:"outcome"`
	Reason                    string                        `json:"reason"`
	OfferedRatePerSecond      uint64                        `json:"offered_rate_per_second"`
	ActualRatePerSecond       float64                       `json:"actual_rate_per_second"`
	MinimumThroughputPercent  uint64                        `json:"minimum_throughput_percent"`
	MeasuredDurationSeconds   float64                       `json:"measured_duration_seconds"`
	OnlineConnections         int                           `json:"online_connections"`
	Sent                      uint64                        `json:"sent"`
	Acknowledged              uint64                        `json:"acknowledged"`
	Expected                  uint64                        `json:"expected"`
	MinimumFilesystemFreePct  float64                       `json:"minimum_filesystem_free_percent"`
	StorageEvidenceComplete   bool                          `json:"storage_evidence_complete"`
	HostIOEvidenceComplete    bool                          `json:"host_io_evidence_complete"`
	ProductMetricsComplete    bool                          `json:"product_metrics_complete"`
	ProcessContinuityComplete bool                          `json:"process_continuity_complete"`
}

func newLocalChatLifecycleStepReportCommand() *cobra.Command {
	var beforePath, afterPath, storagePath, hostIOPath, processPath, outputPath string
	var offeredRate, minimumThroughput uint64
	var measuredDuration time.Duration
	var hostConfounded bool
	cmd := &cobra.Command{
		Use:   "local-chat-lifecycle-step",
		Short: "Classify one non-formal local chat-lifecycle rate step",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if offeredRate == 0 || measuredDuration < time.Second || measuredDuration%time.Second != 0 ||
				minimumThroughput == 0 || minimumThroughput > 100 {
				return commandExit{code: exitConfig, message: "--offered-rate, whole-second --measured-duration, and --minimum-throughput-percent are required"}
			}
			before, beforeErr := chatlifecycle.ReadReport(beforePath)
			after, afterErr := chatlifecycle.ReadReport(afterPath)
			expectedTag := "rate-" + strconv.FormatUint(offeredRate, 10)
			storageComplete, storageErr := readLocalStepStorageEvidence(storagePath, expectedTag)
			hostIOComplete, hostIOErr := readLocalStepHostIOEvidence(hostIOPath, expectedTag)
			processesContinuous, processErr := readLocalStepProcessContinuity(processPath)
			evidence := localChatLifecycleStepEvidence{
				StorageComplete: storageComplete, HostIOComplete: hostIOComplete,
				ProductMetricsComplete: beforeErr == nil && afterErr == nil &&
					localChatLifecycleProductMetricsComplete(before) && localChatLifecycleProductMetricsComplete(after),
				ProcessesContinuous: processesContinuous, HostConfounded: hostConfounded,
			}
			if storageErr != nil {
				evidence.StorageComplete = false
			}
			if hostIOErr != nil {
				evidence.HostIOComplete = false
			}
			if processErr != nil {
				evidence.ProcessesContinuous = false
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
	cmd.Flags().StringVar(&processPath, "process-continuity", "", "closed process continuity TSV")
	cmd.Flags().StringVar(&outputPath, "output", "", "typed local step JSON output")
	cmd.Flags().Uint64Var(&offeredRate, "offered-rate", 0, "offered SEND rate per second")
	cmd.Flags().DurationVar(&measuredDuration, "measured-duration", 0, "post-warmup measured interval")
	cmd.Flags().Uint64Var(&minimumThroughput, "minimum-throughput-percent", 90, "minimum actual/offered SENDACK percentage")
	cmd.Flags().BoolVar(&hostConfounded, "host-confounded", false, "mark overlapping WuKongIM workload evidence")
	for _, name := range []string{"before", "after", "storage-summary", "host-io-summary", "process-continuity", "output", "offered-rate", "measured-duration"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func localChatLifecycleProductMetricsComplete(report chatlifecycle.Report) bool {
	resources := report.Resources.Capacity
	return resources.Complete && resources.ProcessesComplete && resources.Samples > 0 && resources.MissingSamples == 0 &&
		resources.WorkerQueuesComplete && resources.WorkerQueueSamples > 0 && resources.WorkerQueueMissingSamples == 0 &&
		(report.Cluster.HealthySamples > 0 || report.Cluster.UnhealthySamples > 0)
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
		MeasuredDurationSeconds:  options.MeasuredDuration.Seconds(), OnlineConnections: before.Sessions.Online,
		StorageEvidenceComplete: evidence.StorageComplete, ProductMetricsComplete: evidence.ProductMetricsComplete,
		HostIOEvidenceComplete: evidence.HostIOComplete, ProcessContinuityComplete: evidence.ProcessesContinuous,
	}
	if options.OfferedRatePerSecond == 0 || options.MeasuredDuration < time.Second || options.MeasuredDuration%time.Second != 0 ||
		options.MinimumThroughputPercent == 0 || options.MinimumThroughputPercent > 100 {
		return result
	}
	if evidence.HostConfounded {
		result.Outcome, result.Reason = localChatLifecycleStepHostConfounded, "overlapping_wukongim_workload"
		return result
	}
	if !evidence.StorageComplete || !evidence.HostIOComplete || !evidence.ProductMetricsComplete || !evidence.ProcessesContinuous {
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
	sent, sentOK := localStepCounterDelta(before.Messages.Sent, after.Messages.Sent)
	acknowledged, acknowledgedOK := localStepCounterDelta(before.Messages.SendAcknowledged, after.Messages.SendAcknowledged)
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
		localChatLifecycleWorkRemaining(after) || localChatLifecycleProductQueuesAboveBaseline(before, after) {
		result.Outcome, result.Reason = localChatLifecycleStepRateFailed, "underdelivery_or_incomplete_drain"
		return result
	}
	result.Outcome, result.Reason = localChatLifecycleStepClean, "complete"
	return result
}

func localChatLifecycleProductQueuesAboveBaseline(before, after chatlifecycle.Report) bool {
	for index := range after.Resources.Nodes {
		if after.Resources.Nodes[index].QueueCurrent > before.Resources.Nodes[index].QueueCurrent ||
			after.Resources.Nodes[index].InflightCurrent > before.Resources.Nodes[index].InflightCurrent {
			return true
		}
	}
	return false
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
	return report.Verdict.Outcome == chatlifecycle.VerdictProductFailure ||
		report.EvidenceClassification == chatlifecycle.SyncClassificationProductFailure ||
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
