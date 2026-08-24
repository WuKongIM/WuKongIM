// Package localbaseline validates the closed evidence produced by native local
// benchmark baselines. It does not start workloads or infer missing evidence.
package localbaseline

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	// StepEvidenceSchema identifies one single-node cluster rate-step evidence document.
	StepEvidenceSchema = "wukongim/chat-lifecycle-local-single-node-step-evidence/v1"
	// ClosedStepResultSchema identifies the immutable decision for one sealed rate step.
	ClosedStepResultSchema = "wukongim/chat-lifecycle-local-single-node-step-result/v1"
	// ReviewedMaximumSampleGap is the largest periodic lifecycle gap accepted by the reviewed baseline.
	ReviewedMaximumSampleGap = 30 * time.Second
	// reviewedCoordinatorBoundaryTolerance covers control polling and serialized
	// wall-clock slew around an exact worker-owned monotonic phase deadline; it
	// does not extend the configured workload.
	reviewedCoordinatorBoundaryTolerance = time.Second
	// ProductQueueCutSchema identifies metadata embedded in one retained raw
	// Prometheus queue cut before the scrape is performed.
	ProductQueueCutSchema = "wukongim/chat-lifecycle-local-single-node-product-queue-cut/v1"
)

// Outcome is the typed result of evaluating one local rate step.
type Outcome string

const (
	// OutcomeClean means every required invariant was proven from closed evidence.
	OutcomeClean Outcome = "clean"
	// OutcomeRateFailed means workload delivery or online-population requirements failed.
	OutcomeRateFailed Outcome = "rate_failed"
	// OutcomeProductFailure means the service, correctness, or product queue failed.
	OutcomeProductFailure Outcome = "product_failure"
	// OutcomeInsufficientEvidence means the retained evidence cannot prove a result.
	OutcomeInsufficientEvidence Outcome = "insufficient_evidence"
	// OutcomeHostConfounded is a sealed preflight denial caused by another workload.
	OutcomeHostConfounded Outcome = "host_confounded"
	// OutcomeStorageConfounded is a sealed preflight denial caused by the free-space floor.
	OutcomeStorageConfounded Outcome = "storage_confounded"
)

// Reason is a stable low-cardinality explanation for a non-clean result.
type Reason string

const (
	ReasonSchemaMismatch          Reason = "schema_mismatch"
	ReasonTimelineIncomplete      Reason = "timeline_incomplete"
	ReasonSampleGap               Reason = "lifecycle_sample_gap"
	ReasonActiveConnections       Reason = "active_connections_below_required"
	ReasonServerProcessExit       Reason = "server_process_not_continuous"
	ReasonWorkerProcessExit       Reason = "worker_process_not_continuous"
	ReasonTrafficAccounting       Reason = "traffic_accounting_mismatch"
	ReasonMeasuredThroughput      Reason = "measured_throughput_below_90_percent"
	ReasonTerminalSendFailure     Reason = "terminal_send_failure"
	ReasonCorrectnessFailure      Reason = "correctness_failure"
	ReasonRetryEvidence           Reason = "retry_evidence_incomplete"
	ReasonRetryExhausted          Reason = "retry_exhausted"
	ReasonReceiveDrain            Reason = "receive_drain_evidence_incomplete"
	ReasonReceiveDrainFailure     Reason = "receive_drain_failure"
	ReasonReceiveFanoutEvidence   Reason = "receive_fanout_evidence_incomplete"
	ReasonReceiveFanoutAccounting Reason = "receive_fanout_accounting_mismatch"
	ReasonTerminalCutBinding      Reason = "terminal_cut_binding_incomplete"
	ReasonQueueEvidence           Reason = "product_queue_evidence_incomplete"
	ReasonQueueConvergence        Reason = "product_queue_not_converged"
	ReasonProductResultEvidence   Reason = "product_result_counter_evidence_incomplete"
	ReasonProductFailureDelta     Reason = "product_failure_counter_increased"
	ReasonStorageOverlap          Reason = "storage_overlap_evidence_incomplete"
	ReasonStorageMetrics          Reason = "storage_metrics_evidence_incomplete"
	ReasonHostIO                  Reason = "host_io_evidence_incomplete"
	ReasonProfileEvidence         Reason = "threshold_profile_evidence_incomplete"
	ReasonArtifactSeal            Reason = "artifact_seal_incomplete"
)

// Observation is a typed, additive diagnostic signal that does not change a
// step verdict or assert a root cause.
type Observation string

const (
	// ObservationSnapshotOverlap reports a measured snapshot inventory change.
	ObservationSnapshotOverlap Observation = "snapshot_overlap_observed"
	// ObservationCompactionOverlap reports measured compaction activity.
	ObservationCompactionOverlap Observation = "compaction_overlap_observed"
	// ObservationThresholdProfileCaptured reports a complete bounded profile
	// attached to a typed measured threshold without changing attribution.
	ObservationThresholdProfileCaptured Observation = "threshold_profile_captured"
)

const (
	// QueueGatewayAsyncSend is the Gateway asynchronous SEND admission queue.
	QueueGatewayAsyncSend = "gateway_async_send"
	// QueueChannelMailbox is the Channel runtime reactor mailbox.
	QueueChannelMailbox = "channel_mailbox"
	// QueueChannelWorker is the Channel runtime worker queue.
	QueueChannelWorker = "channel_worker"
	// QueueRuntimePool is the reusable runtime worker-pool queue.
	QueueRuntimePool = "runtime_pool"
	// QueueChannelAppendPending is admitted Channel append work awaiting execution.
	QueueChannelAppendPending = "channel_append_pending"
	// QueueChannelAppendInflight is Channel append work executing before commit.
	QueueChannelAppendInflight = "channel_append_inflight"
	// QueuePostCommitBacklog is durable Channel append work awaiting effects.
	QueuePostCommitBacklog = "post_commit_backlog"
	// QueuePostCommitHandoff is work retaining a post-commit handoff reservation.
	QueuePostCommitHandoff = "post_commit_handoff"
	// QueuePostCommitRetry is the de-duplicated post-commit retry queue.
	QueuePostCommitRetry = "post_commit_retry"
	// QueueEffectPoolInflight is work executing in the fixed Channel append worker pools.
	QueueEffectPoolInflight = "effect_pool_inflight"
	// QueueStorageCommit is the durable message-store commit queue.
	QueueStorageCommit = "storage_commit"
	// QueueDeliveryPlan is the canonical Online Delivery plan queue.
	QueueDeliveryPlan = "delivery_plan_queue"
	// QueueDeliveryInflight is Online Delivery plan work executing through owner push.
	QueueDeliveryInflight = "delivery_plan_inflight"
	// QueueDeliveryAckBindings is owner-local delivery waiting for client RECVACK.
	QueueDeliveryAckBindings = "delivery_ack_bindings"

	// ProductResultDeliveryPlan is the exactly-once terminal result partition
	// for accepted canonical Online Delivery plans.
	ProductResultDeliveryPlan = "delivery_plan"
	// ProductResultChannelAppendPostCommit is the exactly-once final result
	// partition for channelappend post-commit effects.
	ProductResultChannelAppendPostCommit = "channelappend_post_commit"
)

var requiredProductQueues = [...]string{
	QueueGatewayAsyncSend,
	QueueChannelMailbox,
	QueueChannelWorker,
	QueueRuntimePool,
	QueueChannelAppendPending,
	QueueChannelAppendInflight,
	QueuePostCommitBacklog,
	QueuePostCommitHandoff,
	QueuePostCommitRetry,
	QueueEffectPoolInflight,
	QueueStorageCommit,
	QueueDeliveryPlan,
	QueueDeliveryInflight,
	QueueDeliveryAckBindings,
}

// ProcessEvidence identifies one observed process instance. StartToken must be
// derived independently of PID so PID reuse cannot look continuous.
type ProcessEvidence struct {
	PID        int    `json:"pid"`
	StartToken string `json:"start_token"`
	Alive      bool   `json:"alive"`
}

// RuntimeSample is one phase-aware, low-cardinality lifecycle observation.
type RuntimeSample struct {
	ObservedAt          time.Time                  `json:"observed_at"`
	ActiveConnections   int                        `json:"active_connections"`
	TerminalPreClose    bool                       `json:"terminal_pre_close,omitempty"`
	TerminalCutRequired bool                       `json:"terminal_cut_required,omitempty"`
	TerminalCutReady    bool                       `json:"terminal_cut_ready,omitempty"`
	TerminalCut         *TerminalCutBinding        `json:"terminal_cut,omitempty"`
	Server              ProcessEvidence            `json:"server"`
	Worker              ProcessEvidence            `json:"worker"`
	Traffic             TrafficEvidence            `json:"traffic"`
	ReceiveDrain        model.ReceiveDrainSnapshot `json:"receive_drain"`
}

// PhaseEvidence retains explicit boundary samples and every periodic sample
// between them for one benchmark phase.
type PhaseEvidence struct {
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at"`
	Samples   []RuntimeSample `json:"samples"`
}

// TimelineEvidence records the ordered warmup, measured, and drain phases.
type TimelineEvidence struct {
	// CaptureComplete is false after any failed status/process observation in the
	// warmup-through-drain interval, even when surrounding samples remain close.
	CaptureComplete bool          `json:"capture_complete"`
	Warmup          PhaseEvidence `json:"warmup"`
	Measured        PhaseEvidence `json:"measured"`
	Drain           PhaseEvidence `json:"drain"`
	// Terminal is the stopped-assignment observation used for final traffic and
	// process reconciliation after the coordinator-owned drain window closes.
	Terminal RuntimeSample `json:"terminal"`
}

// TrafficEvidence reconciles unique logical SENDs separately from physical
// attempts so retries cannot inflate SENDACK accounting.
type TrafficEvidence struct {
	// WarmupSendACKs counts logical warmup messages with a successful terminal
	// SENDACK. It is kept separate from measured SendACKs so the terminal
	// receive fanout debt can cover late warmup delivery without inflating the
	// measured throughput numerator.
	WarmupSendACKs           uint64 `json:"warmup_sendacks"`
	Planned                  uint64 `json:"planned"`
	Dispatched               uint64 `json:"dispatched"`
	LogicalSent              uint64 `json:"logical_sent"`
	SendAttempts             uint64 `json:"send_attempts"`
	SendACKs                 uint64 `json:"sendacks"`
	TerminalErrors           uint64 `json:"terminal_errors"`
	CorrectnessErrors        uint64 `json:"correctness_errors"`
	Remaining                uint64 `json:"remaining"`
	RetryAttempts            uint64 `json:"retry_attempts"`
	RetryExhausted           uint64 `json:"retry_exhausted"`
	StableClientMsgNo        bool   `json:"stable_client_msg_no"`
	RetryEvidenceComplete    bool   `json:"retry_evidence_complete"`
	MaximumRetriesPerMessage uint8  `json:"max_retries"`
}

// ProductQueueBoundary is one normalized before/terminal queue pair.
type ProductQueueBoundary struct {
	Name          string  `json:"name"`
	BaselineDepth float64 `json:"baseline_depth"`
	TerminalDepth float64 `json:"terminal_depth"`
}

// ProductResultCounterBoundary is one normalized cumulative-result counter at
// the post-warmup and terminal boundaries. Result is part of a fixed closed
// vocabulary; counters are not queue depths and may grow only in the ok
// partition during measured work.
type ProductResultCounterBoundary struct {
	Name            string `json:"name"`
	Result          string `json:"result"`
	PostWarmupTotal uint64 `json:"post_warmup_total"`
	TerminalTotal   uint64 `json:"terminal_total"`
}

// ProductQueueCut binds a raw queue snapshot to an exact worker assignment
// and lifecycle observation made immediately before the metrics scrape.
type ProductQueueCut struct {
	Schema       string    `json:"schema"`
	ObservedAt   time.Time `json:"observed_at"`
	RunID        string    `json:"run_id"`
	AssignmentID string    `json:"assignment_id"`
	Phase        string    `json:"phase"`
	ActivePhase  string    `json:"active_phase"`
	// ReceiveDrainSHA256 binds the exact worker receive proof observed directly
	// before this product scrape. It prevents a re-proved late frame from being
	// paired with an older clean queue cut.
	ReceiveDrainSHA256 string `json:"receive_drain_sha256"`
}

// TerminalCutBinding is the exact-generation worker acknowledgement of the
// external pre-close product and storage evidence captured during cooldown.
type TerminalCutBinding struct {
	RunID                string    `json:"run_id"`
	AssignmentID         string    `json:"assignment_id"`
	ReadyAt              time.Time `json:"ready_at"`
	DeadlineAt           time.Time `json:"deadline_at"`
	ObservedAt           time.Time `json:"observed_at"`
	ReceiveDrainSHA256   string    `json:"receive_drain_sha256"`
	ProductMetricsSHA256 string    `json:"product_metrics_sha256"`
	StorageOverlapSHA256 string    `json:"storage_overlap_sha256"`
	AcknowledgedAt       time.Time `json:"acknowledged_at"`
}

// ProductQueueEvidence contains every fixed product queue required at the
// post-warmup boundary and after the bounded drain.
type ProductQueueEvidence struct {
	BoundaryEvidenceComplete bool                           `json:"boundary_evidence_complete"`
	PostWarmupCut            ProductQueueCut                `json:"post_warmup_cut"`
	TerminalCut              ProductQueueCut                `json:"terminal_cut"`
	TerminalPayloadSHA256    string                         `json:"terminal_payload_sha256"`
	Queues                   []ProductQueueBoundary         `json:"queues"`
	ResultCounters           []ProductResultCounterBoundary `json:"result_counters"`
}

// StorageOverlapSample is one closed single-node cluster observation of
// compaction counters and the external Slot snapshot inventory.
type StorageOverlapSample struct {
	ObservedAt            time.Time `json:"observed_at"`
	RunID                 string    `json:"run_id"`
	Sample                string    `json:"sample"`
	Node                  string    `json:"node"`
	Status                string    `json:"status"`
	CompactionCount       uint64    `json:"compaction_count"`
	CompactionsInProgress uint64    `json:"compactions_in_progress"`
	SnapshotFiles         uint64    `json:"snapshot_files"`
	SnapshotBytes         uint64    `json:"snapshot_bytes"`
	SnapshotIdentity      string    `json:"snapshot_identity"`
	SnapshotInventory     string    `json:"snapshot_inventory"`
	InventoryVerified     bool      `json:"inventory_verified"`
}

// StorageOverlapEvidence retains the post-warmup, periodic measured, and
// terminal snapshot/compaction cuts on the same worker-owned UTC timeline.
type StorageOverlapEvidence struct {
	CaptureComplete bool                   `json:"capture_complete"`
	PayloadSHA256   string                 `json:"payload_sha256"`
	Samples         []StorageOverlapSample `json:"samples"`
}

// SealEvidence proves that the step payload stopped changing before its
// checksum inventory was written and verified.
type SealEvidence struct {
	PayloadComplete   bool `json:"payload_complete"`
	ChecksumsVerified bool `json:"checksums_verified"`
}

// ReviewedTargetEvidence binds the coordinator's effective local endpoints
// into each typed step. The command-side verifier derives these values from
// the sealed run report; callers cannot supply an independent target claim.
type ReviewedTargetEvidence struct {
	APIAddress     string `json:"api_address"`
	GatewayAddress string `json:"gateway_address"`
	MetricsAddress string `json:"metrics_address"`
	WorkerAddress  string `json:"worker_address"`
}

// ExecutionSealEvidence binds one step to the baseline invocation and the
// immutable config and binaries that produced it. Digests are read from the
// verified raw manifest, never accepted as independent classifier flags.
type ExecutionSealEvidence struct {
	BaselineInvocationID  string `json:"baseline_invocation_id"`
	SourceConfigSHA256    string `json:"source_config_sha256"`
	EffectiveConfigSHA256 string `json:"effective_config_sha256"`
	WukongIMBinarySHA256  string `json:"wukongim_binary_sha256"`
	WkbenchBinarySHA256   string `json:"wkbench_binary_sha256"`
}

// StepEvidence is the complete typed input for one single-node cluster rate step.
type StepEvidence struct {
	Schema                       string                 `json:"schema"`
	RunID                        string                 `json:"run_id"`
	AssignmentID                 string                 `json:"assignment_id"`
	OfferedSendQPS               int                    `json:"offered_send_qps"`
	RequiredActiveConnections    int                    `json:"required_active_connections"`
	ConfiguredGroupMembers       int                    `json:"configured_group_members"`
	ConfiguredWarmupSeconds      int                    `json:"configured_warmup_seconds"`
	ConfiguredMeasuredSeconds    int                    `json:"configured_measured_seconds"`
	ConfiguredDrainBudgetSeconds int                    `json:"configured_drain_budget_seconds"`
	MaximumSampleGapSeconds      float64                `json:"maximum_sample_gap_seconds"`
	Target                       ReviewedTargetEvidence `json:"target"`
	ExecutionSeal                ExecutionSealEvidence  `json:"execution_seal"`
	Timeline                     TimelineEvidence       `json:"timeline"`
	Traffic                      TrafficEvidence        `json:"traffic"`
	ProductQueues                ProductQueueEvidence   `json:"product_queues"`
	StorageOverlap               StorageOverlapEvidence `json:"storage_overlap"`
	StorageMetrics               StorageMetricsEvidence `json:"storage_metrics"`
	HostIO                       HostIOEvidence         `json:"host_io"`
	Profile                      ProfileEvidence        `json:"profile"`
	Seal                         SealEvidence           `json:"seal"`
}

// StepResult is a deterministic classification derived from StepEvidence.
type StepResult struct {
	Outcome            Outcome  `json:"outcome"`
	Clean              bool     `json:"clean"`
	ActualOfferedRatio float64  `json:"actual_offered_ratio"`
	Reasons            []Reason `json:"reasons"`
	// Observations retain explanatory correlations without changing Clean.
	Observations []Observation `json:"observations"`
}

// ClosedStepResult binds one evaluated rate-step decision to the checksum
// manifest of the raw payload from which its evidence was built.
type ClosedStepResult struct {
	Schema                string        `json:"schema"`
	RunID                 string        `json:"run_id"`
	AssignmentID          string        `json:"assignment_id"`
	OfferedSendQPS        int           `json:"offered_send_qps"`
	PayloadManifestSHA256 string        `json:"payload_manifest_sha256"`
	Outcome               Outcome       `json:"outcome"`
	Clean                 bool          `json:"clean"`
	ActualOfferedRatio    float64       `json:"actual_offered_ratio"`
	Reasons               []Reason      `json:"reasons"`
	Observations          []Observation `json:"observations"`
}

// CloseStepResult evaluates evidence once and retains the exact payload
// manifest digest that makes the decision consumable by the staircase.
func CloseStepResult(evidence StepEvidence, payloadManifestSHA256 string) ClosedStepResult {
	result := EvaluateStep(evidence)
	return ClosedStepResult{
		Schema: ClosedStepResultSchema, RunID: evidence.RunID, AssignmentID: evidence.AssignmentID,
		OfferedSendQPS: evidence.OfferedSendQPS, PayloadManifestSHA256: payloadManifestSHA256,
		Outcome: result.Outcome, Clean: result.Clean, ActualOfferedRatio: result.ActualOfferedRatio,
		Reasons: result.Reasons, Observations: result.Observations,
	}
}

// EvaluateStep classifies one closed single-node cluster rate step. Any
// missing, malformed, or internally inconsistent evidence fails closed.
func EvaluateStep(evidence StepEvidence) StepResult {
	result := StepResult{
		Outcome: OutcomeClean, Clean: true,
		Reasons: make([]Reason, 0), Observations: make([]Observation, 0),
	}
	add := func(outcome Outcome, reason Reason) {
		seen := false
		for _, existing := range result.Reasons {
			if existing == reason {
				seen = true
				break
			}
		}
		if !seen {
			result.Reasons = append(result.Reasons, reason)
		}
		if outcomePriority(outcome) > outcomePriority(result.Outcome) {
			result.Outcome = outcome
		}
		result.Clean = false
	}

	if evidence.Schema != StepEvidenceSchema {
		add(OutcomeInsufficientEvidence, ReasonSchemaMismatch)
	}
	if !validStepConfiguration(evidence) {
		add(OutcomeInsufficientEvidence, ReasonTimelineIncomplete)
	}

	serverContinuous, workerContinuous, completeTimeline, boundedSamples, onlineComplete := evaluateTimeline(evidence)
	if !completeTimeline {
		add(OutcomeInsufficientEvidence, ReasonTimelineIncomplete)
	}
	if !boundedSamples {
		add(OutcomeInsufficientEvidence, ReasonSampleGap)
	}
	if !serverContinuous {
		add(OutcomeProductFailure, ReasonServerProcessExit)
	}
	if !workerContinuous {
		add(OutcomeInsufficientEvidence, ReasonWorkerProcessExit)
	}
	if !onlineComplete {
		add(OutcomeRateFailed, ReasonActiveConnections)
	}

	expected, expectedOK := expectedLogicalSends(evidence.OfferedSendQPS, evidence.ConfiguredMeasuredSeconds)
	if expectedOK && expected > 0 {
		result.ActualOfferedRatio = float64(evidence.Traffic.SendACKs) / float64(expected)
	}
	if !expectedOK || expected == 0 || !trafficAccountingComplete(evidence.Traffic, expected) {
		add(OutcomeRateFailed, ReasonTrafficAccounting)
	}
	if result.ActualOfferedRatio < 0.90 {
		add(OutcomeRateFailed, ReasonMeasuredThroughput)
	}
	if evidence.Traffic.TerminalErrors != 0 {
		add(OutcomeProductFailure, ReasonTerminalSendFailure)
	}
	if evidence.Traffic.CorrectnessErrors != 0 {
		add(OutcomeProductFailure, ReasonCorrectnessFailure)
	}
	if !retryEvidenceComplete(evidence.Traffic) {
		add(OutcomeInsufficientEvidence, ReasonRetryEvidence)
	}
	if evidence.Traffic.RetryExhausted != 0 {
		add(OutcomeProductFailure, ReasonRetryExhausted)
	}
	receiveComplete, receiveFailed := evaluateReceiveDrainTimeline(evidence)
	if receiveFailed {
		add(OutcomeProductFailure, ReasonReceiveDrainFailure)
	} else if !receiveComplete {
		add(OutcomeInsufficientEvidence, ReasonReceiveDrain)
	}
	// Explicit receive/RECVACK failures are already typed product evidence. Do
	// not let the resulting unequal success counters upgrade that known product
	// failure to insufficient evidence; exact fanout accounting remains a
	// mandatory independent gate for every failure-free run.
	if !receiveFailed {
		fanoutComplete, fanoutReconciled := evaluateReceiveFanoutAccounting(evidence)
		if !fanoutComplete {
			add(OutcomeInsufficientEvidence, ReasonReceiveFanoutEvidence)
		} else if !fanoutReconciled {
			add(OutcomeProductFailure, ReasonReceiveFanoutAccounting)
		}
	}
	if !terminalCutBindingMatchesTimeline(evidence) {
		add(OutcomeInsufficientEvidence, ReasonTerminalCutBinding)
	}

	queuesComplete, queuesConverged := evaluateProductQueues(evidence.ProductQueues)
	if !queueCutsMatchTimeline(evidence) {
		queuesComplete = false
	}
	if !queuesComplete {
		add(OutcomeInsufficientEvidence, ReasonQueueEvidence)
	} else if !queuesConverged {
		add(OutcomeProductFailure, ReasonQueueConvergence)
	}
	resultsComplete, failuresUnchanged := evaluateProductResultCounters(evidence.ProductQueues)
	if !resultsComplete {
		add(OutcomeInsufficientEvidence, ReasonProductResultEvidence)
	} else if !failuresUnchanged {
		add(OutcomeProductFailure, ReasonProductFailureDelta)
	}
	if !storageOverlapMatchesTimeline(evidence) {
		add(OutcomeInsufficientEvidence, ReasonStorageOverlap)
	} else {
		result.Observations = storageOverlapObservations(evidence)
	}
	if !storageMetricsEvidenceComplete(evidence.StorageMetrics, evidence.OfferedSendQPS) {
		add(OutcomeInsufficientEvidence, ReasonStorageMetrics)
	}
	if !hostIOEvidenceComplete(evidence.HostIO, evidence.OfferedSendQPS) {
		add(OutcomeInsufficientEvidence, ReasonHostIO)
	}
	if !profileEvidenceClosed(evidence.Profile) {
		add(OutcomeInsufficientEvidence, ReasonProfileEvidence)
	} else if evidence.Profile.Triggered {
		result.Observations = append(result.Observations, ObservationThresholdProfileCaptured)
	}
	if !evidence.Seal.PayloadComplete || !evidence.Seal.ChecksumsVerified {
		add(OutcomeInsufficientEvidence, ReasonArtifactSeal)
	}

	sort.Slice(result.Reasons, func(i, j int) bool { return result.Reasons[i] < result.Reasons[j] })
	return result
}

func storageOverlapObservations(evidence StepEvidence) []Observation {
	var snapshotOverlap, compactionOverlap bool
	var previous *StorageOverlapSample
	for index := range evidence.StorageOverlap.Samples {
		current := &evidence.StorageOverlap.Samples[index]
		if current.Sample == "terminal" || current.ObservedAt.Before(evidence.Timeline.Measured.StartedAt) ||
			current.ObservedAt.After(evidence.Timeline.Measured.EndedAt) {
			continue
		}
		if current.CompactionsInProgress > 0 {
			compactionOverlap = true
		}
		if previous == nil {
			previous = current
			continue
		}
		if current.CompactionCount > previous.CompactionCount {
			compactionOverlap = true
		}
		if current.SnapshotFiles != previous.SnapshotFiles || current.SnapshotBytes != previous.SnapshotBytes ||
			current.SnapshotIdentity != previous.SnapshotIdentity {
			snapshotOverlap = true
		}
		previous = current
	}
	observations := make([]Observation, 0, 2)
	if snapshotOverlap {
		observations = append(observations, ObservationSnapshotOverlap)
	}
	if compactionOverlap {
		observations = append(observations, ObservationCompactionOverlap)
	}
	return observations
}

func validStepConfiguration(evidence StepEvidence) bool {
	return strings.TrimSpace(evidence.RunID) != "" && strings.TrimSpace(evidence.AssignmentID) != "" &&
		evidence.OfferedSendQPS > 0 && evidence.RequiredActiveConnections >= 2500 &&
		evidence.ConfiguredGroupMembers > 1 &&
		validReviewedTargetEvidence(evidence.Target) &&
		validExecutionSealEvidence(evidence.ExecutionSeal) &&
		evidence.ConfiguredWarmupSeconds > 0 && evidence.ConfiguredMeasuredSeconds > 0 &&
		evidence.ConfiguredDrainBudgetSeconds > 0 && evidence.MaximumSampleGapSeconds > 0 &&
		time.Duration(evidence.MaximumSampleGapSeconds*float64(time.Second)) <= ReviewedMaximumSampleGap
}

func validExecutionSealEvidence(seal ExecutionSealEvidence) bool {
	return validLowerHex(seal.BaselineInvocationID, 32) &&
		validLowerHex(seal.SourceConfigSHA256, 64) &&
		validLowerHex(seal.EffectiveConfigSHA256, 64) &&
		validLowerHex(seal.WukongIMBinarySHA256, 64) &&
		validLowerHex(seal.WkbenchBinarySHA256, 64)
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validReviewedTargetEvidence(target ReviewedTargetEvidence) bool {
	return strings.TrimSpace(target.APIAddress) != "" &&
		strings.TrimSpace(target.GatewayAddress) != "" &&
		strings.TrimSpace(target.MetricsAddress) != "" &&
		strings.TrimSpace(target.WorkerAddress) != "" &&
		target.APIAddress == target.MetricsAddress
}

func queueCutsMatchTimeline(evidence StepEvidence) bool {
	postWarmup := evidence.ProductQueues.PostWarmupCut
	terminal := evidence.ProductQueues.TerminalCut
	if !validQueueCutIdentity(postWarmup, evidence.RunID, evidence.AssignmentID) ||
		!validQueueCutIdentity(terminal, evidence.RunID, evidence.AssignmentID) {
		return false
	}
	if postWarmup.Phase != "warmup" || postWarmup.ActivePhase != "run" ||
		postWarmup.ObservedAt.Before(evidence.Timeline.Measured.StartedAt) ||
		postWarmup.ObservedAt.After(evidence.Timeline.Measured.EndedAt) ||
		postWarmup.ObservedAt.Sub(evidence.Timeline.Measured.StartedAt) > ReviewedMaximumSampleGap {
		return false
	}
	if terminal.Phase != "run" || terminal.ActivePhase != "cooldown" ||
		terminal.ObservedAt.Before(evidence.Timeline.Drain.StartedAt) ||
		terminal.ObservedAt.After(evidence.Timeline.Drain.EndedAt) {
		return false
	}
	return true
}

func terminalCutBindingMatchesTimeline(evidence StepEvidence) bool {
	terminal := evidence.Timeline.Terminal
	binding := terminal.TerminalCut
	if !terminal.TerminalPreClose || !terminal.TerminalCutRequired || !terminal.TerminalCutReady || binding == nil ||
		strings.TrimSpace(binding.RunID) != strings.TrimSpace(evidence.RunID) ||
		strings.TrimSpace(binding.AssignmentID) != strings.TrimSpace(evidence.AssignmentID) ||
		!validSHA256(binding.ReceiveDrainSHA256) || !validSHA256(binding.ProductMetricsSHA256) || !validSHA256(binding.StorageOverlapSHA256) ||
		!utcOffsetZero(binding.ReadyAt) || !utcOffsetZero(binding.DeadlineAt) || !utcOffsetZero(binding.ObservedAt) || !utcOffsetZero(binding.AcknowledgedAt) ||
		binding.ReadyAt.Before(evidence.Timeline.Drain.StartedAt) || binding.ReadyAt.After(evidence.Timeline.Drain.EndedAt) ||
		binding.ObservedAt.Before(binding.ReadyAt) || binding.ObservedAt.After(evidence.Timeline.Drain.EndedAt) ||
		binding.DeadlineAt.Before(binding.ReadyAt) ||
		binding.AcknowledgedAt.Before(binding.ObservedAt) || binding.AcknowledgedAt.After(binding.DeadlineAt) ||
		binding.AcknowledgedAt.After(evidence.Timeline.Drain.EndedAt.Add(reviewedCoordinatorBoundaryTolerance)) ||
		binding.DeadlineAt.Sub(evidence.Timeline.Drain.StartedAt) > time.Duration(evidence.ConfiguredDrainBudgetSeconds)*time.Second+reviewedCoordinatorBoundaryTolerance ||
		binding.ProductMetricsSHA256 != evidence.ProductQueues.TerminalPayloadSHA256 ||
		binding.StorageOverlapSHA256 != evidence.StorageOverlap.PayloadSHA256 ||
		binding.ReceiveDrainSHA256 != model.ReceiveDrainFingerprint(terminal.ReceiveDrain) ||
		binding.ReceiveDrainSHA256 != evidence.ProductQueues.TerminalCut.ReceiveDrainSHA256 ||
		!binding.ObservedAt.Equal(evidence.ProductQueues.TerminalCut.ObservedAt) ||
		evidence.ProductQueues.TerminalCut.Phase != "run" || evidence.ProductQueues.TerminalCut.ActivePhase != "cooldown" {
		return false
	}
	if len(evidence.StorageOverlap.Samples) == 0 {
		return false
	}
	storageTerminal := evidence.StorageOverlap.Samples[len(evidence.StorageOverlap.Samples)-1]
	return storageTerminal.Sample == "terminal" && binding.ObservedAt.Equal(storageTerminal.ObservedAt)
}

func utcOffsetZero(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func storageOverlapMatchesTimeline(evidence StepEvidence) bool {
	storage := evidence.StorageOverlap
	if !storage.CaptureComplete || len(storage.Samples) < 3 || len(storage.Samples) > maximumStorageOverlapRows {
		return false
	}
	maximumGap := time.Duration(evidence.MaximumSampleGapSeconds * float64(time.Second))
	if maximumGap <= 0 || maximumGap > ReviewedMaximumSampleGap {
		return false
	}
	measured := make([]StorageOverlapSample, 0, len(storage.Samples)-1)
	var terminal *StorageOverlapSample
	var previous *StorageOverlapSample
	seen := make(map[string]struct{}, len(storage.Samples))
	for index := range storage.Samples {
		sample := &storage.Samples[index]
		if sample.ObservedAt.IsZero() || strings.TrimSpace(sample.RunID) != evidence.RunID || sample.Node != "node-1" ||
			sample.Status != "complete" || !sample.InventoryVerified || !validStorageSampleName(sample.Sample) ||
			!validSnapshotIdentity(sample.SnapshotIdentity) ||
			sample.SnapshotInventory != "snapshot-inventory/"+sample.Sample+"-node-1.tsv" {
			return false
		}
		if _, duplicate := seen[sample.Sample]; duplicate {
			return false
		}
		seen[sample.Sample] = struct{}{}
		if previous != nil {
			if !sample.ObservedAt.After(previous.ObservedAt) || sample.CompactionCount < previous.CompactionCount {
				return false
			}
		}
		previous = sample
		if sample.Sample == "terminal" {
			if terminal != nil {
				return false
			}
			terminal = sample
			continue
		}
		if len(measured) == 0 {
			if sample.Sample != "post-warmup" {
				return false
			}
		} else if !strings.HasPrefix(sample.Sample, "periodic-") {
			return false
		}
		measured = append(measured, *sample)
	}
	if len(measured) < 2 || terminal == nil || storage.Samples[len(storage.Samples)-1].Sample != "terminal" {
		return false
	}
	first := measured[0].ObservedAt
	last := measured[len(measured)-1].ObservedAt
	if first.Before(evidence.Timeline.Measured.StartedAt) || first.After(evidence.Timeline.Measured.EndedAt) ||
		first.Sub(evidence.Timeline.Measured.StartedAt) > maximumGap || last.After(evidence.Timeline.Measured.EndedAt) ||
		evidence.Timeline.Measured.EndedAt.Sub(last) > maximumGap {
		return false
	}
	for index := 1; index < len(measured); index++ {
		if gap := measured[index].ObservedAt.Sub(measured[index-1].ObservedAt); gap <= 0 || gap > maximumGap {
			return false
		}
	}
	return !terminal.ObservedAt.Before(evidence.Timeline.Drain.StartedAt) &&
		!terminal.ObservedAt.After(evidence.Timeline.Drain.EndedAt)
}

func validQueueCutIdentity(cut ProductQueueCut, runID, assignmentID string) bool {
	return cut.Schema == ProductQueueCutSchema && !cut.ObservedAt.IsZero() &&
		strings.TrimSpace(cut.RunID) == strings.TrimSpace(runID) &&
		strings.TrimSpace(cut.AssignmentID) == strings.TrimSpace(assignmentID)
}

func evaluateTimeline(evidence StepEvidence) (serverContinuous, workerContinuous, complete, bounded, online bool) {
	serverContinuous, workerContinuous, complete, bounded, online = true, true, true, true, true
	if !evidence.Timeline.CaptureComplete {
		complete = false
	}
	phases := []struct {
		phase         PhaseEvidence
		requireOnline bool
		minimum       time.Duration
		maximum       time.Duration
	}{
		{phase: evidence.Timeline.Warmup, requireOnline: true, minimum: time.Duration(evidence.ConfiguredWarmupSeconds) * time.Second},
		{phase: evidence.Timeline.Measured, requireOnline: true, minimum: time.Duration(evidence.ConfiguredMeasuredSeconds) * time.Second},
		{phase: evidence.Timeline.Drain, maximum: time.Duration(evidence.ConfiguredDrainBudgetSeconds) * time.Second},
	}
	var serverIdentity, workerIdentity ProcessEvidence
	var previousTraffic TrafficEvidence
	trafficSet := false
	identitySet := false
	maximumGap := time.Duration(evidence.MaximumSampleGapSeconds * float64(time.Second))
	for _, boundary := range [][2]time.Time{
		{evidence.Timeline.Warmup.EndedAt, evidence.Timeline.Measured.StartedAt},
		{evidence.Timeline.Measured.EndedAt, evidence.Timeline.Drain.StartedAt},
	} {
		gap := boundary[1].Sub(boundary[0])
		if gap < 0 || maximumGap <= 0 || gap > maximumGap {
			complete = false
		}
	}
	for _, item := range phases {
		phase := item.phase
		isDrain := item.maximum > 0 && item.minimum == 0
		if phase.StartedAt.IsZero() || phase.EndedAt.Before(phase.StartedAt) {
			complete = false
			continue
		}
		duration := phase.EndedAt.Sub(phase.StartedAt)
		minimumSamples := 2
		if isDrain && duration < maximumGap {
			// Exact convergence may happen immediately after SEND admission closes.
			// The immutable stopped-assignment terminal cut below then proves the
			// whole short drain; a long drain still needs periodic coverage.
			minimumSamples = 0
		}
		if len(phase.Samples) < minimumSamples {
			if isDrain {
				bounded = false
			} else {
				complete = false
			}
			continue
		}
		// The workload owns the exact monotonic deadline, while retained phase
		// windows and lifecycle samples use wall-clock timestamps so they can be
		// correlated across processes. Accept only the existing bounded boundary
		// tolerance for wall-clock slew around that proven deadline; a larger
		// under-run remains incomplete evidence.
		if item.minimum > 0 && duration+reviewedCoordinatorBoundaryTolerance < item.minimum {
			complete = false
		}
		if item.maximum > 0 && duration > item.maximum+reviewedCoordinatorBoundaryTolerance {
			complete = false
		}
		if len(phase.Samples) > 0 {
			if firstGap := phase.Samples[0].ObservedAt.Sub(phase.StartedAt); firstGap < 0 || maximumGap <= 0 || firstGap > maximumGap {
				bounded = false
			}
			if lastGap := phase.EndedAt.Sub(phase.Samples[len(phase.Samples)-1].ObservedAt); lastGap < 0 || maximumGap <= 0 || lastGap > maximumGap {
				bounded = false
			}
		}
		for index, sample := range phase.Samples {
			if sample.ObservedAt.Before(phase.StartedAt) || sample.ObservedAt.After(phase.EndedAt) {
				complete = false
			}
			if index > 0 {
				gap := sample.ObservedAt.Sub(phase.Samples[index-1].ObservedAt)
				if gap <= 0 {
					complete = false
					bounded = false
				} else if maximumGap <= 0 || gap > maximumGap {
					bounded = false
				}
			}
			if trafficSet && !trafficMonotonic(previousTraffic, sample.Traffic) {
				complete = false
			}
			previousTraffic = sample.Traffic
			trafficSet = true
			if item.requireOnline && sample.ActiveConnections < evidence.RequiredActiveConnections {
				online = false
			}
			if !identitySet {
				serverIdentity = sample.Server
				workerIdentity = sample.Worker
				identitySet = true
			}
			if !sameLiveProcess(serverIdentity, sample.Server) {
				serverContinuous = false
			}
			if !sameLiveProcess(workerIdentity, sample.Worker) {
				workerContinuous = false
			}
		}
	}
	if len(evidence.Timeline.Measured.Samples) > 0 &&
		evidence.Timeline.Measured.Samples[len(evidence.Timeline.Measured.Samples)-1].Traffic.Planned == 0 {
		complete = false
	}
	terminal := evidence.Timeline.Terminal
	if terminal.ObservedAt.IsZero() {
		complete = false
		bounded = false
	} else {
		if !terminal.TerminalPreClose {
			complete = false
		}
		if terminal.ActiveConnections < evidence.RequiredActiveConnections {
			online = false
		}
		terminalGap := terminal.ObservedAt.Sub(evidence.Timeline.Drain.EndedAt)
		if terminalGap < 0 {
			complete = false
		} else if maximumGap <= 0 || terminalGap > maximumGap {
			bounded = false
		}
		if identitySet {
			if !sameLiveProcess(serverIdentity, terminal.Server) {
				serverContinuous = false
			}
			if !sameLiveProcess(workerIdentity, terminal.Worker) {
				workerContinuous = false
			}
		}
		if (trafficSet && !trafficMonotonic(previousTraffic, terminal.Traffic)) || terminal.Traffic != evidence.Traffic {
			complete = false
		}
	}
	if !identitySet {
		serverContinuous, workerContinuous = false, false
	}
	return serverContinuous, workerContinuous, complete, bounded, online
}

func trafficMonotonic(previous, current TrafficEvidence) bool {
	return current.WarmupSendACKs >= previous.WarmupSendACKs &&
		current.Planned >= previous.Planned && current.Dispatched >= previous.Dispatched &&
		current.LogicalSent >= previous.LogicalSent && current.SendAttempts >= previous.SendAttempts &&
		current.SendACKs >= previous.SendACKs && current.TerminalErrors >= previous.TerminalErrors &&
		current.CorrectnessErrors >= previous.CorrectnessErrors && current.RetryAttempts >= previous.RetryAttempts &&
		current.RetryExhausted >= previous.RetryExhausted
}

func evaluateReceiveDrainTimeline(evidence StepEvidence) (complete, failed bool) {
	samples := make([]model.ReceiveDrainSnapshot, 0,
		len(evidence.Timeline.Warmup.Samples)+len(evidence.Timeline.Measured.Samples)+len(evidence.Timeline.Drain.Samples)+1)
	for _, phase := range []PhaseEvidence{evidence.Timeline.Warmup, evidence.Timeline.Measured, evidence.Timeline.Drain} {
		for _, sample := range phase.Samples {
			samples = append(samples, sample.ReceiveDrain)
		}
	}
	samples = append(samples, evidence.Timeline.Terminal.ReceiveDrain)
	if len(samples) == 0 {
		return false, false
	}
	complete = true
	var previous model.ReceiveDrainSnapshot
	for index, sample := range samples {
		if !validLiveReceiveDrainSnapshot(sample, evidence.RequiredActiveConnections) {
			complete = false
		}
		if !sample.FailureFree() {
			failed = true
		}
		if index > 0 {
			if sample.Required != previous.Required || sample.ClientCount != previous.ClientCount ||
				sample.RecvACKFailures < previous.RecvACKFailures || sample.ReadFailures < previous.ReadFailures ||
				sample.RecvACKSuccesses < previous.RecvACKSuccesses ||
				sample.ReceiveFramesObserved < previous.ReceiveFramesObserved ||
				sample.BufferedFramesDrained < previous.BufferedFramesDrained ||
				!fanoutProofMonotonic(previous.FanoutProof, sample.FanoutProof) {
				complete = false
			}
		}
		previous = sample
	}
	if !evidence.Timeline.Terminal.ReceiveDrain.TerminalProofComplete() {
		complete = false
	}
	return complete, failed
}

// evaluateReceiveFanoutAccounting reconciles the reviewed group workload's
// full warmup-plus-measured recipient debt. A logical SENDACK is the stable
// denominator: retries reuse ClientMsgNo and therefore must not multiply the
// expected fanout. The terminal ingress fence and product ACK-binding proof
// separately establish that a successful client RECVACK write was consumed.
func evaluateReceiveFanoutAccounting(evidence StepEvidence) (complete, reconciled bool) {
	if evidence.ConfiguredGroupMembers <= 1 || evidence.Traffic.WarmupSendACKs == 0 {
		return false, false
	}
	logicalACKs, ok := checkedAdd(evidence.Traffic.WarmupSendACKs, evidence.Traffic.SendACKs)
	if !ok || logicalACKs == 0 {
		return false, false
	}
	expected, ok := checkedMultiply(logicalACKs, uint64(evidence.ConfiguredGroupMembers-1))
	if !ok || expected == 0 {
		return false, false
	}
	terminal := evidence.Timeline.Terminal.ReceiveDrain
	proof := terminal.FanoutProof
	if !proof.Complete() || !proof.Required ||
		proof.Received.Count != terminal.ReceiveFramesObserved ||
		proof.RecvACKed.Count != terminal.RecvACKSuccesses {
		return false, false
	}
	return true, proof.LogicalSendACKs == logicalACKs && proof.Expected.Count == expected &&
		proof.Received.Count == expected && proof.RecvACKed.Count == expected && proof.Matches()
}

func fanoutProofMonotonic(previous, current model.FanoutProofSnapshot) bool {
	if !previous.Complete() || !current.Complete() || previous.Required != current.Required ||
		previous.Version != current.Version {
		return false
	}
	return current.LogicalSendACKs >= previous.LogicalSendACKs &&
		current.Expected.Count >= previous.Expected.Count &&
		current.Received.Count >= previous.Received.Count &&
		current.RecvACKed.Count >= previous.RecvACKed.Count
}

func validLiveReceiveDrainSnapshot(snapshot model.ReceiveDrainSnapshot, requiredConnections int) bool {
	if !snapshot.EvidenceComplete || !snapshot.Required || requiredConnections <= 0 ||
		!snapshot.FanoutProof.Required || !snapshot.FanoutProof.Complete() ||
		snapshot.ClientCount != uint64(requiredConnections) ||
		snapshot.ActiveDrains != uint64(requiredConnections) ||
		snapshot.QueueSnapshotClients != uint64(requiredConnections) {
		return false
	}
	if snapshot.DrainComplete {
		return snapshot.TerminalProofComplete()
	}
	return snapshot.StableZeroObservations < model.ReceiveDrainStableZeroObservations
}

func sameLiveProcess(want, got ProcessEvidence) bool {
	return want.Alive && got.Alive && want.PID > 0 && got.PID == want.PID &&
		want.StartToken != "" && got.StartToken == want.StartToken
}

func expectedLogicalSends(offeredQPS, measuredSeconds int) (uint64, bool) {
	if offeredQPS <= 0 || measuredSeconds <= 0 {
		return 0, false
	}
	left, right := uint64(offeredQPS), uint64(measuredSeconds)
	if right != 0 && left > math.MaxUint64/right {
		return 0, false
	}
	return left * right, true
}

func trafficAccountingComplete(traffic TrafficEvidence, expected uint64) bool {
	if expected == 0 || traffic.Planned == 0 || traffic.Planned > expected ||
		traffic.Dispatched != traffic.Planned ||
		traffic.LogicalSent != traffic.Dispatched || traffic.Remaining != 0 {
		return false
	}
	terminal, ok := checkedAdd(traffic.SendACKs, traffic.TerminalErrors)
	if !ok || terminal != traffic.LogicalSent {
		return false
	}
	wantAttempts, ok := checkedAdd(traffic.Planned, traffic.RetryAttempts)
	return ok && traffic.SendAttempts == wantAttempts
}

func retryEvidenceComplete(traffic TrafficEvidence) bool {
	if !traffic.StableClientMsgNo || !traffic.RetryEvidenceComplete ||
		traffic.MaximumRetriesPerMessage != 3 || traffic.RetryExhausted > traffic.TerminalErrors {
		return false
	}
	maximumRetries, ok := checkedMultiply(traffic.LogicalSent, uint64(traffic.MaximumRetriesPerMessage))
	return ok && traffic.RetryAttempts <= maximumRetries
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, false
	}
	return left + right, true
}

func checkedMultiply(left, right uint64) (uint64, bool) {
	if right != 0 && left > math.MaxUint64/right {
		return 0, false
	}
	return left * right, true
}

func evaluateProductQueues(evidence ProductQueueEvidence) (complete, converged bool) {
	if !evidence.BoundaryEvidenceComplete || len(evidence.Queues) < len(requiredProductQueues) || len(evidence.Queues) > 32 {
		return false, false
	}
	byName := make(map[string]ProductQueueBoundary, len(evidence.Queues))
	for _, queue := range evidence.Queues {
		if queue.Name == "" || math.IsNaN(queue.BaselineDepth) || math.IsNaN(queue.TerminalDepth) ||
			math.IsInf(queue.BaselineDepth, 0) || math.IsInf(queue.TerminalDepth, 0) ||
			queue.BaselineDepth < 0 || queue.TerminalDepth < 0 {
			return false, false
		}
		if _, duplicate := byName[queue.Name]; duplicate {
			return false, false
		}
		byName[queue.Name] = queue
	}
	converged = true
	for _, name := range requiredProductQueues {
		queue, ok := byName[name]
		if !ok {
			return false, false
		}
		if queue.TerminalDepth > queue.BaselineDepth {
			converged = false
		}
	}
	return true, converged
}

func evaluateProductResultCounters(evidence ProductQueueEvidence) (complete, failuresUnchanged bool) {
	want := len(deliveryPlanTerminalResults) + len(channelAppendPostCommitResults)
	if !evidence.BoundaryEvidenceComplete || len(evidence.ResultCounters) != want || want > 64 {
		return false, false
	}
	type resultKey struct {
		name   string
		result string
	}
	byKey := make(map[resultKey]ProductResultCounterBoundary, len(evidence.ResultCounters))
	for _, counter := range evidence.ResultCounters {
		key := resultKey{name: counter.Name, result: counter.Result}
		if counter.Name == "" || counter.Result == "" || counter.TerminalTotal < counter.PostWarmupTotal {
			return false, false
		}
		if _, duplicate := byKey[key]; duplicate {
			return false, false
		}
		byKey[key] = counter
	}
	failuresUnchanged = true
	check := func(name string, results []string) bool {
		for _, result := range results {
			counter, ok := byKey[resultKey{name: name, result: result}]
			if !ok {
				return false
			}
			if result != "ok" && counter.TerminalTotal != counter.PostWarmupTotal {
				failuresUnchanged = false
			}
		}
		return true
	}
	if !check(ProductResultDeliveryPlan, deliveryPlanTerminalResults[:]) ||
		!check(ProductResultChannelAppendPostCommit, channelAppendPostCommitResults[:]) {
		return false, false
	}
	return true, failuresUnchanged
}

func outcomePriority(outcome Outcome) int {
	switch outcome {
	case OutcomeInsufficientEvidence:
		return 3
	case OutcomeProductFailure:
		return 2
	case OutcomeRateFailed:
		return 1
	default:
		return 0
	}
}
