package localbaseline

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	benchmetrics "github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	// LifecycleCaptureSchema identifies one shell-captured worker/process observation.
	LifecycleCaptureSchema = "wukongim/chat-lifecycle-local-single-node-lifecycle-sample/v1"
	// MaximumLifecycleCaptureBytes bounds one retained status timeline.
	MaximumLifecycleCaptureBytes = 8 << 20
	// MaximumProductQueueCutBytes bounds each raw Prometheus cut consumed by the
	// local authorization evaluator.
	MaximumProductQueueCutBytes = 64 << 20
)

const productQueueCutPrefix = "# wkbench_local_single_node_cut "

// PhaseWindow is one actual coordinator-owned phase interval.
type PhaseWindow struct {
	Phase     string    `json:"phase"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// CapturedStatus is the bounded subset of /v1/status used by the evidence builder.
type CapturedStatus struct {
	Phase          string                   `json:"phase"`
	ActivePhase    string                   `json:"active_phase"`
	CompletedPhase string                   `json:"completed_phase"`
	LastError      string                   `json:"last_error"`
	ObservedAt     time.Time                `json:"observed_at"`
	Lifecycle      *CapturedLifecycleStatus `json:"lifecycle"`
	Assignment     CapturedAssignment       `json:"assignment"`
}

// CapturedAssignment identifies the exact worker assignment generation.
type CapturedAssignment struct {
	RunID        string `json:"run_id"`
	AssignmentID string `json:"assignment_id"`
}

// CapturedLifecycleStatus is the low-cardinality live worker projection.
type CapturedLifecycleStatus struct {
	ActiveConnections     int                        `json:"active_connections"`
	TerminalPreClose      bool                       `json:"terminal_pre_close"`
	TerminalCutRequired   bool                       `json:"terminal_cut_required"`
	TerminalCutReady      bool                       `json:"terminal_cut_ready"`
	TerminalCutReadyAt    time.Time                  `json:"terminal_cut_ready_at"`
	TerminalCutDeadlineAt time.Time                  `json:"terminal_cut_deadline_at"`
	TerminalCut           *TerminalCutBinding        `json:"terminal_cut"`
	Traffic               TrafficEvidence            `json:"traffic"`
	ReceiveDrain          model.ReceiveDrainSnapshot `json:"receive_drain"`
	ReceiveDrainSHA256    string                     `json:"receive_drain_sha256"`
}

// LifecycleCapture joins a worker status observation to independently sampled
// server and worker process identities.
type LifecycleCapture struct {
	Schema    string          `json:"schema"`
	SampledAt time.Time       `json:"sampled_at"`
	Error     string          `json:"error,omitempty"`
	Status    *CapturedStatus `json:"status,omitempty"`
	Server    ProcessEvidence `json:"server"`
	Worker    ProcessEvidence `json:"worker"`
}

// StepCaptureInput contains already-retained raw observations for one step.
type StepCaptureInput struct {
	RunID                        string
	OfferedSendQPS               int
	RequiredActiveConnections    int
	ConfiguredGroupMembers       int
	ConfiguredWarmupSeconds      int
	ConfiguredMeasuredSeconds    int
	ConfiguredDrainBudgetSeconds int
	MaximumSampleGapSeconds      float64
	Target                       ReviewedTargetEvidence
	ExecutionSeal                ExecutionSealEvidence
	PhaseWindows                 []PhaseWindow
	Lifecycle                    []LifecycleCapture
	ProductQueues                ProductQueueEvidence
	StorageOverlap               StorageOverlapEvidence
	StorageMetrics               StorageMetricsEvidence
	HostIO                       HostIOEvidence
	Profile                      ProfileEvidence
	Seal                         SealEvidence
}

// ParseLifecycleCaptures parses a bounded JSONL stream. Unknown fields are
// rejected so a silently changed worker/status capture cannot authorize a run.
func ParseLifecycleCaptures(reader io.Reader) ([]LifecycleCapture, error) {
	if reader == nil {
		return nil, fmt.Errorf("parse lifecycle captures: reader is required")
	}
	limited := &io.LimitedReader{R: reader, N: MaximumLifecycleCaptureBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	captures := make([]LifecycleCapture, 0, 128)
	for line := 1; scanner.Scan(); line++ {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		var capture LifecycleCapture
		if err := decoder.Decode(&capture); err != nil {
			return nil, fmt.Errorf("parse lifecycle captures line %d: %w", line, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("parse lifecycle captures line %d: trailing JSON", line)
		}
		captures = append(captures, capture)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse lifecycle captures: %w", err)
	}
	if limited.N <= 0 {
		return nil, fmt.Errorf("parse lifecycle captures: document exceeds %d bytes", MaximumLifecycleCaptureBytes)
	}
	return captures, nil
}

// BuildStepEvidence maps raw coordinator, worker, process, and queue cuts into
// the single typed document consumed by EvaluateStep. It never fills missing
// samples or assumes that an unavailable observation was healthy.
func BuildStepEvidence(input StepCaptureInput) StepEvidence {
	evidence := StepEvidence{
		Schema:                       StepEvidenceSchema,
		RunID:                        strings.TrimSpace(input.RunID),
		OfferedSendQPS:               input.OfferedSendQPS,
		RequiredActiveConnections:    input.RequiredActiveConnections,
		ConfiguredGroupMembers:       input.ConfiguredGroupMembers,
		ConfiguredWarmupSeconds:      input.ConfiguredWarmupSeconds,
		ConfiguredMeasuredSeconds:    input.ConfiguredMeasuredSeconds,
		ConfiguredDrainBudgetSeconds: input.ConfiguredDrainBudgetSeconds,
		MaximumSampleGapSeconds:      input.MaximumSampleGapSeconds,
		Target:                       input.Target,
		ExecutionSeal:                input.ExecutionSeal,
		ProductQueues:                input.ProductQueues,
		StorageOverlap:               input.StorageOverlap,
		StorageMetrics:               input.StorageMetrics,
		HostIO:                       input.HostIO,
		Profile:                      input.Profile,
		Seal:                         input.Seal,
	}

	windows, windowsComplete := requiredPhaseWindows(input.PhaseWindows)
	evidence.Timeline = TimelineEvidence{
		CaptureComplete: windowsComplete,
		Warmup:          phaseFromWindow(windows["warmup"]),
		Measured:        phaseFromWindow(windows["run"]),
		Drain:           phaseFromWindow(windows["cooldown"]),
	}
	intervalStart := evidence.Timeline.Warmup.StartedAt
	intervalEnd := evidence.Timeline.Drain.EndedAt

	var terminal *CapturedLifecycleStatus
	var terminalAt time.Time
	var terminalSample RuntimeSample
	assignmentID := ""
	for _, capture := range input.Lifecycle {
		if capture.Schema != LifecycleCaptureSchema || capture.SampledAt.IsZero() {
			evidence.Timeline.CaptureComplete = false
			continue
		}
		if capture.Error != "" {
			evidence.Timeline.CaptureComplete = false
			continue
		}
		if capture.Status == nil || capture.Status.ObservedAt.IsZero() || capture.Status.Lifecycle == nil {
			evidence.Timeline.CaptureComplete = false
			continue
		}
		status := capture.Status
		if status.Lifecycle.ReceiveDrainSHA256 == "" ||
			status.Lifecycle.ReceiveDrainSHA256 != model.ReceiveDrainFingerprint(status.Lifecycle.ReceiveDrain) {
			evidence.Timeline.CaptureComplete = false
		}
		if status.Assignment.RunID != input.RunID || strings.TrimSpace(status.Assignment.AssignmentID) == "" {
			if !status.ObservedAt.Before(intervalStart) && !status.ObservedAt.After(intervalEnd) {
				evidence.Timeline.CaptureComplete = false
			}
			continue
		}
		currentAssignmentID := strings.TrimSpace(status.Assignment.AssignmentID)
		if assignmentID == "" {
			assignmentID = currentAssignmentID
		} else if currentAssignmentID != assignmentID {
			evidence.Timeline.CaptureComplete = false
			continue
		}
		if status.LastError != "" {
			evidence.Timeline.CaptureComplete = false
		}
		if status.Phase == "stopped" && status.ActivePhase == "" && status.ObservedAt.After(terminalAt) {
			copy := *status.Lifecycle
			terminal = &copy
			terminalAt = status.ObservedAt
			terminalSample = RuntimeSample{
				ObservedAt:          status.ObservedAt,
				ActiveConnections:   status.Lifecycle.ActiveConnections,
				TerminalPreClose:    status.Lifecycle.TerminalPreClose,
				TerminalCutRequired: status.Lifecycle.TerminalCutRequired,
				TerminalCutReady:    status.Lifecycle.TerminalCutReady,
				TerminalCut:         cloneTerminalCutBinding(status.Lifecycle.TerminalCut),
				Server:              capture.Server,
				Worker:              capture.Worker,
				Traffic:             status.Lifecycle.Traffic,
				ReceiveDrain:        status.Lifecycle.ReceiveDrain,
			}
		}
		phase := status.ActivePhase
		var destination *PhaseEvidence
		switch phase {
		case "warmup":
			destination = &evidence.Timeline.Warmup
		case "run":
			destination = &evidence.Timeline.Measured
		case "cooldown":
			destination = &evidence.Timeline.Drain
		default:
			continue
		}
		if status.ObservedAt.Before(destination.StartedAt) || status.ObservedAt.After(destination.EndedAt) {
			continue
		}
		destination.Samples = append(destination.Samples, RuntimeSample{
			ObservedAt:          status.ObservedAt,
			ActiveConnections:   status.Lifecycle.ActiveConnections,
			TerminalCutRequired: status.Lifecycle.TerminalCutRequired,
			TerminalCutReady:    status.Lifecycle.TerminalCutReady,
			TerminalCut:         cloneTerminalCutBinding(status.Lifecycle.TerminalCut),
			Server:              capture.Server,
			Worker:              capture.Worker,
			Traffic:             status.Lifecycle.Traffic,
			ReceiveDrain:        status.Lifecycle.ReceiveDrain,
		})
	}
	for _, phase := range []*PhaseEvidence{&evidence.Timeline.Warmup, &evidence.Timeline.Measured, &evidence.Timeline.Drain} {
		sort.Slice(phase.Samples, func(i, j int) bool { return phase.Samples[i].ObservedAt.Before(phase.Samples[j].ObservedAt) })
	}
	if terminal != nil {
		evidence.Traffic = terminal.Traffic
		evidence.Timeline.Terminal = terminalSample
	}
	evidence.AssignmentID = assignmentID
	if assignmentID == "" {
		evidence.Timeline.CaptureComplete = false
	}
	return evidence
}

func cloneTerminalCutBinding(binding *TerminalCutBinding) *TerminalCutBinding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

func requiredPhaseWindows(windows []PhaseWindow) (map[string]PhaseWindow, bool) {
	required := map[string]PhaseWindow{"warmup": {}, "run": {}, "cooldown": {}}
	seen := make(map[string]bool, len(required))
	complete := true
	for _, window := range windows {
		if _, ok := required[window.Phase]; !ok {
			continue
		}
		if seen[window.Phase] || window.StartedAt.IsZero() || !window.EndedAt.After(window.StartedAt) {
			complete = false
			continue
		}
		required[window.Phase] = window
		seen[window.Phase] = true
	}
	for name := range required {
		if !seen[name] {
			complete = false
		}
	}
	return required, complete
}

func phaseFromWindow(window PhaseWindow) PhaseEvidence {
	return PhaseEvidence{StartedAt: window.StartedAt, EndedAt: window.EndedAt, Samples: make([]RuntimeSample, 0)}
}

type queueMetric struct {
	name           string
	metric         string
	labels         map[string]string
	partitionLabel string
	partitions     []string
}

type resultCounterMetric struct {
	name    string
	metric  string
	labels  map[string]string
	results []string
}

var deliveryPlanTerminalResults = [...]string{
	"ok", "panic", "timeout", "canceled", "error", "retry_exhausted", "unknown",
}

var channelAppendPostCommitResults = [...]string{
	"ok", "mixed", "canceled", "timeout", "backpressured", "channel_busy", "route_not_ready",
	"stale_route", "stale_completion", "not_authority", "not_leader", "channel_not_found",
	"append_result_missing", "append_failed", "commit_failed", "invalid_subscribers", "invalid_cursor",
	"unsupported", "auth_fail", "invalid_request", "system_error", "other",
}

var requiredResultCounterMetrics = [...]resultCounterMetric{
	{
		name: ProductResultDeliveryPlan, metric: "wukongim_delivery_recipient_worker_process_total",
		results: deliveryPlanTerminalResults[:],
	},
	{
		name: ProductResultChannelAppendPostCommit, metric: "wukongim_channelappend_effect_total",
		labels: map[string]string{"stage": "post_commit"}, results: channelAppendPostCommitResults[:],
	},
}

var requiredQueueMetrics = [...]queueMetric{
	{name: QueueGatewayAsyncSend, metric: "wukongim_gateway_async_send_queue_depth"},
	{name: QueueChannelMailbox, metric: "wukongim_channelv2_reactor_mailbox_depth"},
	{name: QueueChannelWorker, metric: "wukongim_channelv2_worker_queue_depth"},
	{name: QueueRuntimePool, metric: "wukongim_runtime_pool_queue_depth"},
	{name: QueueChannelAppendPending, metric: "wukongim_channelappend_writer_state_items", labels: map[string]string{"kind": "pending_append"}},
	{name: QueueChannelAppendInflight, metric: "wukongim_channelappend_writer_state_items", labels: map[string]string{"kind": "append_inflight"}},
	{name: QueuePostCommitBacklog, metric: "wukongim_channelappend_writer_state_items", labels: map[string]string{"kind": "post_commit_backlog"}},
	{name: QueuePostCommitHandoff, metric: "wukongim_channelappend_post_commit_handoff_depth"},
	{name: QueuePostCommitRetry, metric: "wukongim_channelappend_post_commit_retry_queue_depth"},
	{
		name: QueueEffectPoolInflight, metric: "wukongim_ants_pool_running",
		labels: map[string]string{"component": "channelappend"}, partitionLabel: "pool",
		partitions: []string{"advance", "append_effect", "post_commit"},
	},
	{name: QueueStorageCommit, metric: "wukongim_storage_commit_queue_depth"},
	{name: QueueDeliveryPlan, metric: "wukongim_delivery_recipient_worker_queue_depth"},
	{name: QueueDeliveryInflight, metric: "wukongim_delivery_recipient_worker_inflight"},
	{name: QueueDeliveryAckBindings, metric: "wukongim_delivery_ack_bindings"},
}

// BuildProductQueueEvidence normalizes two raw Prometheus cuts. Every required
// family and label selection must exist at both boundaries.
func BuildProductQueueEvidence(baseline, terminal io.Reader) (ProductQueueEvidence, error) {
	before, postWarmupCut, _, err := parseProductQueueCut(baseline)
	if err != nil {
		return ProductQueueEvidence{}, fmt.Errorf("parse post-warmup product queues: %w", err)
	}
	after, terminalCut, terminalDigest, err := parseProductQueueCut(terminal)
	if err != nil {
		return ProductQueueEvidence{}, fmt.Errorf("parse terminal product queues: %w", err)
	}
	evidence := ProductQueueEvidence{
		BoundaryEvidenceComplete: true,
		PostWarmupCut:            postWarmupCut,
		TerminalCut:              terminalCut,
		TerminalPayloadSHA256:    terminalDigest,
		Queues:                   make([]ProductQueueBoundary, 0, len(requiredQueueMetrics)),
		ResultCounters:           make([]ProductResultCounterBoundary, 0, len(deliveryPlanTerminalResults)+len(channelAppendPostCommitResults)),
	}
	if postWarmupCut.Schema != ProductQueueCutSchema || terminalCut.Schema != ProductQueueCutSchema {
		evidence.BoundaryEvidenceComplete = false
	}
	for _, required := range requiredQueueMetrics {
		baselineDepth, baselineOK := queueDepth(before, required)
		terminalDepth, terminalOK := queueDepth(after, required)
		if !baselineOK || !terminalOK {
			evidence.BoundaryEvidenceComplete = false
		}
		evidence.Queues = append(evidence.Queues, ProductQueueBoundary{
			Name: required.name, BaselineDepth: baselineDepth, TerminalDepth: terminalDepth,
		})
	}
	for _, required := range requiredResultCounterMetrics {
		if !resultCounterPartitionClosed(before, required) || !resultCounterPartitionClosed(after, required) {
			evidence.BoundaryEvidenceComplete = false
		}
		for _, result := range required.results {
			baselineTotal, baselineOK := resultCounterValue(before, required, result)
			terminalTotal, terminalOK := resultCounterValue(after, required, result)
			if !baselineOK || !terminalOK {
				evidence.BoundaryEvidenceComplete = false
			}
			evidence.ResultCounters = append(evidence.ResultCounters, ProductResultCounterBoundary{
				Name: required.name, Result: result, PostWarmupTotal: baselineTotal, TerminalTotal: terminalTotal,
			})
		}
	}
	return evidence, nil
}

func parseProductQueueCut(reader io.Reader) (benchmetrics.PrometheusSnapshot, ProductQueueCut, string, error) {
	if reader == nil {
		return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", fmt.Errorf("reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaximumProductQueueCutBytes+1))
	if err != nil {
		return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", err
	}
	if len(data) > MaximumProductQueueCutBytes {
		return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", fmt.Errorf("cut exceeds %d bytes", MaximumProductQueueCutBytes)
	}
	var cut ProductQueueCut
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, productQueueCutPrefix) {
			continue
		}
		if found {
			return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", fmt.Errorf("duplicate cut metadata")
		}
		decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, productQueueCutPrefix)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cut); err != nil {
			return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", fmt.Errorf("cut metadata: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", fmt.Errorf("cut metadata has trailing JSON")
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", err
	}
	snapshot, err := benchmetrics.ParsePrometheusText(bytes.NewReader(data))
	if err != nil {
		return benchmetrics.PrometheusSnapshot{}, ProductQueueCut{}, "", err
	}
	return snapshot, cut, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func queueDepth(snapshot benchmetrics.PrometheusSnapshot, required queueMetric) (float64, bool) {
	total := 0.0
	found := false
	var allowedPartitions map[string]struct{}
	var seenPartitions map[string]struct{}
	if required.partitionLabel != "" {
		allowedPartitions = make(map[string]struct{}, len(required.partitions))
		seenPartitions = make(map[string]struct{}, len(required.partitions))
		for _, partition := range required.partitions {
			if partition == "" {
				return 0, false
			}
			allowedPartitions[partition] = struct{}{}
		}
	}
	for _, sample := range snapshot.Samples {
		if sample.Name != required.metric || !labelsContain(sample.Labels, required.labels) {
			continue
		}
		if required.partitionLabel != "" {
			partition, ok := sample.Labels[required.partitionLabel]
			if !ok {
				return 0, false
			}
			if _, ok := allowedPartitions[partition]; !ok {
				return 0, false
			}
			if _, duplicate := seenPartitions[partition]; duplicate {
				return 0, false
			}
			seenPartitions[partition] = struct{}{}
		}
		if math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) || sample.Value < 0 {
			return 0, false
		}
		total += sample.Value
		if math.IsInf(total, 0) {
			return 0, false
		}
		found = true
	}
	if required.partitionLabel != "" && len(seenPartitions) != len(allowedPartitions) {
		return 0, false
	}
	return total, found
}

func resultCounterPartitionClosed(snapshot benchmetrics.PrometheusSnapshot, required resultCounterMetric) bool {
	allowed := make(map[string]struct{}, len(required.results))
	for _, result := range required.results {
		allowed[result] = struct{}{}
	}
	for _, sample := range snapshot.Samples {
		if sample.Name != required.metric || !labelsContain(sample.Labels, required.labels) {
			continue
		}
		if _, ok := allowed[sample.Labels["result"]]; !ok {
			return false
		}
	}
	return true
}

func resultCounterValue(snapshot benchmetrics.PrometheusSnapshot, required resultCounterMetric, result string) (uint64, bool) {
	const maximumExactPrometheusInteger = float64(1 << 53)
	want := make(map[string]string, len(required.labels)+1)
	for name, value := range required.labels {
		want[name] = value
	}
	want["result"] = result
	var value uint64
	found := 0
	for _, sample := range snapshot.Samples {
		if sample.Name != required.metric || !labelsContain(sample.Labels, want) {
			continue
		}
		if math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) || sample.Value < 0 ||
			sample.Value > maximumExactPrometheusInteger || math.Trunc(sample.Value) != sample.Value {
			return 0, false
		}
		value = uint64(sample.Value)
		found++
	}
	return value, found == 1
}

func labelsContain(got, want map[string]string) bool {
	for name, value := range want {
		if got[name] != value {
			return false
		}
	}
	return true
}
