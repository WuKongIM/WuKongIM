package localbaseline

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	// MaximumSummaryEvidenceBytes bounds one per-step or aggregate TSV summary.
	MaximumSummaryEvidenceBytes = 8 << 20
)

var storageMetricsSummaryHeader = []string{
	"tag", "node", "evidence", "commit_queue_depth_max", "physical_commits_delta",
	"logical_requests_delta", "records_delta", "bytes_delta", "avg_requests_per_commit",
	"avg_records_per_commit", "collect_avg_ms", "build_avg_ms", "commit_avg_ms",
	"publish_avg_ms", "total_avg_ms", "request_count_delta", "request_avg_ms",
	"request_ok_delta", "request_ok_avg_ms", "request_timeout_delta", "request_timeout_avg_ms",
	"request_canceled_delta", "request_canceled_avg_ms", "request_error_delta", "request_error_avg_ms",
	"leader_append_request_delta", "leader_append_request_avg_ms", "follower_apply_request_delta",
	"follower_apply_request_avg_ms", "message_append_request_delta", "message_append_request_avg_ms",
	"wal_bytes_in_delta", "wal_bytes_written_delta", "wal_write_amplification", "flush_bytes_delta",
	"flush_count_delta", "compaction_bytes_read_delta", "compaction_bytes_written_delta",
	"compaction_count_delta", "sstable_size_max", "compaction_debt_max", "compactions_in_progress_max",
	"read_amplification_max", "disk_usage_max", "avg_bytes_per_commit", "requests_per_commit_p50",
	"requests_per_commit_p95", "requests_per_commit_p99", "records_per_commit_p50",
	"records_per_commit_p95", "records_per_commit_p99", "bytes_per_commit_p50",
	"bytes_per_commit_p95", "bytes_per_commit_p99",
}

var hostIOSummaryHeader = []string{
	"tag", "host", "evidence", "physical_device", "iops_available", "iops_max",
	"bytes_per_second_available", "bytes_per_second_max", "utilization_available",
	"utilization_percent_max", "service_time_available", "service_time_milliseconds_max",
	"read_write_split_available",
}

// StorageMetricsEvidence is the normalized, row-authenticated storage summary
// for one offered-rate step. RowSHA256 binds every field, including additive
// diagnostics that do not otherwise participate in the clean gate.
type StorageMetricsEvidence struct {
	CaptureComplete bool   `json:"capture_complete"`
	Tag             string `json:"tag"`
	Node            string `json:"node"`
	Status          string `json:"status"`
	RowSHA256       string `json:"row_sha256"`

	PhysicalCommits uint64 `json:"physical_commits"`
	LogicalRequests uint64 `json:"logical_requests"`
	Records         uint64 `json:"records"`
	Bytes           uint64 `json:"bytes"`
	RequestSamples  uint64 `json:"request_samples"`
	WALBytesIn      uint64 `json:"wal_bytes_in"`
	WALBytesWritten uint64 `json:"wal_bytes_written"`

	ResultOK       uint64 `json:"result_ok"`
	ResultTimeout  uint64 `json:"result_timeout"`
	ResultCanceled uint64 `json:"result_canceled"`
	ResultError    uint64 `json:"result_error"`

	LaneLeaderAppend  uint64 `json:"lane_leader_append"`
	LaneFollowerApply uint64 `json:"lane_follower_apply"`
	LaneMessageAppend uint64 `json:"lane_message_append"`

	AverageRequestsPerCommit float64 `json:"average_requests_per_commit"`
	AverageRecordsPerCommit  float64 `json:"average_records_per_commit"`
	AverageBytesPerCommit    float64 `json:"average_bytes_per_commit"`
	RequestsPerCommitP50     float64 `json:"requests_per_commit_p50"`
	RequestsPerCommitP95     float64 `json:"requests_per_commit_p95"`
	RequestsPerCommitP99     float64 `json:"requests_per_commit_p99"`
	RecordsPerCommitP50      float64 `json:"records_per_commit_p50"`
	RecordsPerCommitP95      float64 `json:"records_per_commit_p95"`
	RecordsPerCommitP99      float64 `json:"records_per_commit_p99"`
	BytesPerCommitP50        float64 `json:"bytes_per_commit_p50"`
	BytesPerCommitP95        float64 `json:"bytes_per_commit_p95"`
	BytesPerCommitP99        float64 `json:"bytes_per_commit_p99"`
}

// HostIOEvidence is the normalized, row-authenticated physical-device summary
// for one offered-rate step. Status is either complete, unavailable, or a
// syntactically valid non-authorizing AWK status such as missing.
type HostIOEvidence struct {
	CaptureComplete bool   `json:"capture_complete"`
	Tag             string `json:"tag"`
	Host            string `json:"host"`
	Status          string `json:"status"`
	PhysicalDevice  string `json:"physical_device"`
	RowSHA256       string `json:"row_sha256"`

	IOPSAvailable           bool    `json:"iops_available"`
	IOPSMax                 float64 `json:"iops_max"`
	BytesPerSecondAvailable bool    `json:"bytes_per_second_available"`
	BytesPerSecondMax       float64 `json:"bytes_per_second_max"`
	UtilizationAvailable    bool    `json:"utilization_available"`
	UtilizationPercentMax   float64 `json:"utilization_percent_max"`
	ServiceTimeAvailable    bool    `json:"service_time_available"`
	ServiceTimeMillisMax    float64 `json:"service_time_milliseconds_max"`
	ReadWriteSplitAvailable bool    `json:"read_write_split_available"`
}

// ParseStorageMetricsSummary parses exactly one AWK storage row and binds it
// to the expected rate tag and node identity.
func ParseStorageMetricsSummary(reader io.Reader, expectedTag, expectedNode string) (StorageMetricsEvidence, error) {
	rows, err := ParseStorageMetricsSummaryRows(reader)
	if err != nil {
		return StorageMetricsEvidence{}, err
	}
	if len(rows) != 1 {
		return StorageMetricsEvidence{}, fmt.Errorf("parse storage metrics summary: got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Tag != expectedTag || row.Node != expectedNode || expectedTag == "" || expectedNode == "" {
		return StorageMetricsEvidence{}, fmt.Errorf("parse storage metrics summary: row identity is invalid")
	}
	return row, nil
}

// ParseStorageMetricsSummaryRows parses the exact AWK schema. Header-only
// input is accepted for a sealed preflight that has no measured steps.
func ParseStorageMetricsSummaryRows(reader io.Reader) ([]StorageMetricsEvidence, error) {
	lines, err := parseExactSummaryLines(reader, storageMetricsSummaryHeader, "storage metrics")
	if err != nil {
		return nil, err
	}
	rows := make([]StorageMetricsEvidence, 0, len(lines))
	for index, line := range lines {
		row, parseErr := parseStorageMetricsRow(line)
		if parseErr != nil {
			return nil, fmt.Errorf("parse storage metrics summary row %d: %w", index+1, parseErr)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseStorageMetricsRow(line string) (StorageMetricsEvidence, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != len(storageMetricsSummaryHeader) {
		return StorageMetricsEvidence{}, fmt.Errorf("got %d fields, want %d", len(fields), len(storageMetricsSummaryHeader))
	}
	for _, field := range fields {
		if field == "" || strings.TrimSpace(field) != field {
			return StorageMetricsEvidence{}, fmt.Errorf("field is empty or contains surrounding whitespace")
		}
	}
	if fields[2] != "complete" && fields[2] != "missing" && fields[2] != "counter_reset" {
		return StorageMetricsEvidence{}, fmt.Errorf("evidence status %q is invalid", fields[2])
	}
	integerIndexes := map[int]struct{}{
		3: {}, 4: {}, 5: {}, 6: {}, 7: {}, 15: {}, 17: {}, 19: {}, 21: {}, 23: {}, 25: {}, 27: {}, 29: {},
		31: {}, 32: {}, 34: {}, 35: {}, 36: {}, 37: {}, 38: {}, 39: {}, 40: {}, 41: {}, 42: {}, 43: {},
	}
	integers := make(map[int]uint64, len(integerIndexes))
	floats := make(map[int]float64, len(fields)-3-len(integerIndexes))
	for fieldIndex := 3; fieldIndex < len(fields); fieldIndex++ {
		if _, integer := integerIndexes[fieldIndex]; integer {
			value, parseErr := strconv.ParseUint(fields[fieldIndex], 10, 64)
			if parseErr != nil {
				return StorageMetricsEvidence{}, fmt.Errorf("field %q is not an unsigned integer", storageMetricsSummaryHeader[fieldIndex])
			}
			integers[fieldIndex] = value
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[fieldIndex], 64)
		if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return StorageMetricsEvidence{}, fmt.Errorf("field %q is not a finite non-negative number", storageMetricsSummaryHeader[fieldIndex])
		}
		floats[fieldIndex] = value
	}
	digest := sha256.Sum256([]byte(line))
	row := StorageMetricsEvidence{
		CaptureComplete: fields[2] == "complete", Tag: fields[0], Node: fields[1], Status: fields[2],
		RowSHA256: hex.EncodeToString(digest[:]), PhysicalCommits: integers[4], LogicalRequests: integers[5],
		Records: integers[6], Bytes: integers[7], RequestSamples: integers[15], ResultOK: integers[17],
		ResultTimeout: integers[19], ResultCanceled: integers[21], ResultError: integers[23],
		LaneLeaderAppend: integers[25], LaneFollowerApply: integers[27], LaneMessageAppend: integers[29],
		WALBytesIn: integers[31], WALBytesWritten: integers[32], AverageRequestsPerCommit: floats[8],
		AverageRecordsPerCommit: floats[9], AverageBytesPerCommit: floats[44], RequestsPerCommitP50: floats[45],
		RequestsPerCommitP95: floats[46], RequestsPerCommitP99: floats[47], RecordsPerCommitP50: floats[48],
		RecordsPerCommitP95: floats[49], RecordsPerCommitP99: floats[50], BytesPerCommitP50: floats[51],
		BytesPerCommitP95: floats[52], BytesPerCommitP99: floats[53],
	}
	return row, nil
}

// ParseHostIOSummary parses exactly one AWK host row and binds it to the
// expected rate tag and host identity.
func ParseHostIOSummary(reader io.Reader, expectedTag, expectedHost string) (HostIOEvidence, error) {
	rows, err := ParseHostIOSummaryRows(reader)
	if err != nil {
		return HostIOEvidence{}, err
	}
	if len(rows) != 1 {
		return HostIOEvidence{}, fmt.Errorf("parse host I/O summary: got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Tag != expectedTag || row.Host != expectedHost || expectedTag == "" || expectedHost == "" {
		return HostIOEvidence{}, fmt.Errorf("parse host I/O summary: row identity is invalid")
	}
	return row, nil
}

// ParseHostIOSummaryRows parses the exact AWK schema. Header-only input is
// accepted for a sealed preflight that has no measured steps.
func ParseHostIOSummaryRows(reader io.Reader) ([]HostIOEvidence, error) {
	lines, err := parseExactSummaryLines(reader, hostIOSummaryHeader, "host I/O")
	if err != nil {
		return nil, err
	}
	rows := make([]HostIOEvidence, 0, len(lines))
	for index, line := range lines {
		row, parseErr := parseHostIORow(line)
		if parseErr != nil {
			return nil, fmt.Errorf("parse host I/O summary row %d: %w", index+1, parseErr)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseHostIORow(line string) (HostIOEvidence, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != len(hostIOSummaryHeader) {
		return HostIOEvidence{}, fmt.Errorf("got %d fields, want %d", len(fields), len(hostIOSummaryHeader))
	}
	for _, field := range fields {
		if field == "" || strings.TrimSpace(field) != field {
			return HostIOEvidence{}, fmt.Errorf("field is empty or contains surrounding whitespace")
		}
	}
	if fields[2] != "complete" && fields[2] != "unavailable" && fields[2] != "missing" {
		return HostIOEvidence{}, fmt.Errorf("evidence status %q is invalid", fields[2])
	}
	availability := make(map[int]bool, 5)
	for _, fieldIndex := range []int{4, 6, 8, 10, 12} {
		switch fields[fieldIndex] {
		case "0":
			availability[fieldIndex] = false
		case "1":
			availability[fieldIndex] = true
		default:
			return HostIOEvidence{}, fmt.Errorf("field %q is not 0 or 1", hostIOSummaryHeader[fieldIndex])
		}
	}
	values := make(map[int]float64, 4)
	for _, pair := range [][2]int{{4, 5}, {6, 7}, {8, 9}, {10, 11}} {
		available := availability[pair[0]]
		valueField := fields[pair[1]]
		if !available {
			if valueField != "unavailable" {
				return HostIOEvidence{}, fmt.Errorf("field %q contradicts availability", hostIOSummaryHeader[pair[1]])
			}
			continue
		}
		value, parseErr := strconv.ParseFloat(valueField, 64)
		if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return HostIOEvidence{}, fmt.Errorf("field %q is not a finite non-negative number", hostIOSummaryHeader[pair[1]])
		}
		values[pair[1]] = value
	}
	primaryAvailable := availability[4] || availability[6] || availability[8] || availability[10]
	semanticallyComplete := false
	switch fields[2] {
	case "complete":
		semanticallyComplete = primaryAvailable && fields[3] != "unavailable"
	case "unavailable":
		semanticallyComplete = !primaryAvailable && !availability[12] && fields[3] != "unavailable"
	}
	if availability[12] && (!availability[4] || !availability[6]) {
		semanticallyComplete = false
	}
	digest := sha256.Sum256([]byte(line))
	return HostIOEvidence{
		CaptureComplete: semanticallyComplete, Tag: fields[0], Host: fields[1], Status: fields[2],
		PhysicalDevice: fields[3], RowSHA256: hex.EncodeToString(digest[:]), IOPSAvailable: availability[4],
		IOPSMax: values[5], BytesPerSecondAvailable: availability[6], BytesPerSecondMax: values[7],
		UtilizationAvailable: availability[8], UtilizationPercentMax: values[9],
		ServiceTimeAvailable: availability[10], ServiceTimeMillisMax: values[11],
		ReadWriteSplitAvailable: availability[12],
	}, nil
}

func parseExactSummaryLines(reader io.Reader, header []string, label string) ([]string, error) {
	if reader == nil {
		return nil, fmt.Errorf("parse %s summary: reader is required", label)
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaximumSummaryEvidenceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("parse %s summary: %w", label, err)
	}
	if len(data) > MaximumSummaryEvidenceBytes {
		return nil, fmt.Errorf("parse %s summary: document exceeds %d bytes", label, MaximumSummaryEvidenceBytes)
	}
	if len(data) == 0 || bytes.IndexByte(data, '\r') >= 0 || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("parse %s summary: document framing is invalid", label)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	wantHeader := strings.Join(header, "\t")
	if !scanner.Scan() || scanner.Text() != wantHeader {
		return nil, fmt.Errorf("parse %s summary: header is invalid", label)
	}
	lines := make([]string, 0, 4)
	for scanner.Scan() {
		if scanner.Text() == "" {
			return nil, fmt.Errorf("parse %s summary: blank row is invalid", label)
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse %s summary: %w", label, err)
	}
	return lines, nil
}

func storageMetricsEvidenceComplete(evidence StorageMetricsEvidence, offeredQPS int) bool {
	if !evidence.CaptureComplete || evidence.Status != "complete" || !validSHA256(evidence.RowSHA256) ||
		evidence.Tag != fmt.Sprintf("%06d", offeredQPS) || strings.TrimSpace(evidence.Node) == "" ||
		evidence.PhysicalCommits == 0 || evidence.LogicalRequests == 0 || evidence.Records == 0 || evidence.Bytes == 0 ||
		evidence.RequestSamples == 0 || evidence.WALBytesIn == 0 || evidence.WALBytesWritten == 0 ||
		evidence.LogicalRequests != evidence.RequestSamples {
		return false
	}
	resultTotal, ok := checkedSum(evidence.ResultOK, evidence.ResultTimeout, evidence.ResultCanceled, evidence.ResultError)
	if !ok || resultTotal != evidence.RequestSamples {
		return false
	}
	laneTotal, ok := checkedSum(evidence.LaneLeaderAppend, evidence.LaneFollowerApply, evidence.LaneMessageAppend)
	if !ok || laneTotal != evidence.RequestSamples {
		return false
	}
	return positiveOrderedTriple(evidence.RequestsPerCommitP50, evidence.RequestsPerCommitP95, evidence.RequestsPerCommitP99) &&
		positiveOrderedTriple(evidence.RecordsPerCommitP50, evidence.RecordsPerCommitP95, evidence.RecordsPerCommitP99) &&
		positiveOrderedTriple(evidence.BytesPerCommitP50, evidence.BytesPerCommitP95, evidence.BytesPerCommitP99) &&
		finitePositive(evidence.AverageRequestsPerCommit) && finitePositive(evidence.AverageRecordsPerCommit) &&
		finitePositive(evidence.AverageBytesPerCommit)
}

func hostIOEvidenceComplete(evidence HostIOEvidence, offeredQPS int) bool {
	if !evidence.CaptureComplete || !validSHA256(evidence.RowSHA256) || evidence.Tag != fmt.Sprintf("%06d", offeredQPS) ||
		strings.TrimSpace(evidence.Host) == "" || strings.TrimSpace(evidence.PhysicalDevice) == "" || evidence.PhysicalDevice == "unavailable" {
		return false
	}
	switch evidence.Status {
	case "complete":
		if !evidence.IOPSAvailable && !evidence.BytesPerSecondAvailable && !evidence.UtilizationAvailable && !evidence.ServiceTimeAvailable {
			return false
		}
	case "unavailable":
		return !evidence.IOPSAvailable && !evidence.BytesPerSecondAvailable && !evidence.UtilizationAvailable &&
			!evidence.ServiceTimeAvailable && !evidence.ReadWriteSplitAvailable
	default:
		return false
	}
	if evidence.ReadWriteSplitAvailable && (!evidence.IOPSAvailable || !evidence.BytesPerSecondAvailable) {
		return false
	}
	return availableValueValid(evidence.IOPSAvailable, evidence.IOPSMax) &&
		availableValueValid(evidence.BytesPerSecondAvailable, evidence.BytesPerSecondMax) &&
		availableValueValid(evidence.UtilizationAvailable, evidence.UtilizationPercentMax) &&
		availableValueValid(evidence.ServiceTimeAvailable, evidence.ServiceTimeMillisMax)
}

func availableValueValid(available bool, value float64) bool {
	if !available {
		return value == 0
	}
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func positiveOrderedTriple(p50, p95, p99 float64) bool {
	return finitePositive(p50) && finitePositive(p95) && finitePositive(p99) && p50 <= p95 && p95 <= p99
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func checkedSum(values ...uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		next, ok := checkedAdd(total, value)
		if !ok {
			return 0, false
		}
		total = next
	}
	return total, true
}
