package localbaseline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// BaselineEvidenceSchema identifies the complete four-step single-node cluster baseline.
	BaselineEvidenceSchema = "wukongim/chat-lifecycle-local-single-node-baseline-evidence/v1"
	// AuthorizationResultSchema identifies the derived next-diagnostic gate result.
	AuthorizationResultSchema = "wukongim/chat-lifecycle-local-single-node-authorization/v1"
	// MaximumEvidenceBytes bounds one typed baseline document while retaining
	// every reviewed lifecycle sample from all four five-minute rate steps.
	MaximumEvidenceBytes = 8 << 20
)

// ReviewedOfferedSendQPS is the exact ordered single-node cluster staircase.
var ReviewedOfferedSendQPS = [...]int{250, 500, 750, 1000}

// AuthorizationReason is a stable explanation for a closed diagnostic gate.
type AuthorizationReason string

const (
	AuthorizationReasonSchema           AuthorizationReason = "schema_mismatch"
	AuthorizationReasonRateSteps        AuthorizationReason = "reviewed_rate_steps_incomplete"
	AuthorizationReasonStepNotClean     AuthorizationReason = "rate_step_not_clean"
	AuthorizationReasonBaselineOutcome  AuthorizationReason = "baseline_outcome_not_clean"
	AuthorizationReasonReviewedDefaults AuthorizationReason = "reviewed_defaults_not_satisfied"
	AuthorizationReasonSource           AuthorizationReason = "source_not_rebuildable_from_clean_revision"
	AuthorizationReasonSeal             AuthorizationReason = "artifact_seal_incomplete"
	AuthorizationReasonPublication      AuthorizationReason = "completion_generation_invalid"
	AuthorizationReasonFilesystem       AuthorizationReason = "filesystem_observation_incomplete"
	AuthorizationReasonFilesystemFree   AuthorizationReason = "filesystem_free_below_minimum"
	AuthorizationReasonExecutionTarget  AuthorizationReason = "execution_target_mismatch"
	AuthorizationReasonExecutionSeal    AuthorizationReason = "execution_seal_mismatch"
	AuthorizationReasonServerGeneration AuthorizationReason = "server_generation_incomplete"
)

// ReviewedSettings records the topology, duration, population, and durability
// settings that define the reviewed single-node cluster baseline.
type ReviewedSettings struct {
	Channels                int  `json:"channels"`
	ActiveConnections       int  `json:"active_connections"`
	GroupMembers            int  `json:"group_members"`
	SendConcurrency         int  `json:"send_concurrency"`
	PayloadBytes            int  `json:"payload_bytes"`
	WarmupSeconds           int  `json:"warmup_seconds"`
	MeasuredSeconds         int  `json:"measured_seconds"`
	DrainBudgetSeconds      int  `json:"drain_budget_seconds"`
	ACKTimeoutSeconds       int  `json:"ack_timeout_seconds"`
	ReceiveACK              bool `json:"receive_ack"`
	HeartbeatEnabled        bool `json:"heartbeat_enabled"`
	SenderPickRoundRobin    bool `json:"sender_pick_round_robin"`
	MinimumFreePercent      int  `json:"minimum_filesystem_free_percent"`
	LogicalSlotGroups       int  `json:"logical_slot_groups"`
	HashSlots               int  `json:"hash_slots"`
	SlotReplicas            int  `json:"slot_replicas"`
	ChannelReplicas         int  `json:"channel_replicas"`
	CommitFlushWindowMicros int  `json:"commit_flush_window_micros"`
	CommitCoordinatorShards int  `json:"commit_coordinator_shards"`
	SyncCommit              bool `json:"sync_commit"`
	CleanCluster            bool `json:"clean_cluster"`
	OwnedCluster            bool `json:"owned_cluster"`
	OwnedWorker             bool `json:"owned_worker"`
	// CanonicalSourceConfig proves the frozen runtime source was the tracked
	// scripts/wukongim/wukongim.toml content at the sealed source revision.
	CanonicalSourceConfig bool `json:"canonical_source_config"`
	MetricsEndpointCount  int  `json:"metrics_endpoint_count"`
}

// SourceEvidence binds tested binaries to one clean, reconstructable revision.
type SourceEvidence struct {
	Revision                string `json:"revision"`
	Dirty                   bool   `json:"dirty"`
	RebuildableFromRevision bool   `json:"rebuildable_from_revision"`
}

// BaselineEvidence is the closed, typed input for the local authorization gate.
type BaselineEvidence struct {
	Schema               string `json:"schema"`
	CompletionGeneration string `json:"completion_generation"`
	BaselineInvocationID string `json:"baseline_invocation_id"`
	DiagnosticOutcome    string `json:"diagnostic_outcome"`
	DiagnosticReason     string `json:"diagnostic_reason"`
	// FilesystemObservationComplete binds the terminal capacity cut into the
	// typed generation instead of letting the shell add a later marker-only fact.
	FilesystemObservationComplete bool `json:"filesystem_observation_complete"`
	// ObservedFilesystemFreePercent is the integer terminal free-space cut.
	ObservedFilesystemFreePercent int `json:"observed_filesystem_free_percent"`
	// CanonicalDataDir is the absolute cleaned value of the effective
	// WK_NODE_DATA_DIR override, not the possibly superseded source TOML value.
	CanonicalDataDir string `json:"canonical_data_dir"`
	// DataFilesystemDevice is the stable filesystem/device identity observed for CanonicalDataDir.
	DataFilesystemDevice string `json:"data_filesystem_device"`
	// DataFilesystemTotalBlocks and DataFilesystemBlockSize bind the capacity
	// observation to one filesystem geometry rather than an arbitrary host path.
	DataFilesystemTotalBlocks uint64           `json:"data_filesystem_total_blocks"`
	DataFilesystemBlockSize   uint64           `json:"data_filesystem_block_size"`
	Settings                  ReviewedSettings `json:"settings"`
	Source                    SourceEvidence   `json:"source"`
	Seal                      SealEvidence     `json:"seal"`
	StepClosures              []StepClosure    `json:"step_closures"`
}

// AuthorizationResult allows only the next three-node diagnostic. It is not
// a rehearsal, formal-run, or product-capacity verdict.
type AuthorizationResult struct {
	Schema                    string                `json:"schema"`
	ReviewedContractSatisfied bool                  `json:"reviewed_contract_satisfied"`
	Authorizes                bool                  `json:"authorizes_three_node_diagnostic"`
	Outcome                   Outcome               `json:"outcome"`
	Reason                    string                `json:"reason"`
	ExitCode                  int                   `json:"exit_code"`
	HighestCleanRate          int                   `json:"highest_clean_rate"`
	FirstFailingRate          int                   `json:"first_failing_rate"`
	CompletionGeneration      string                `json:"completion_generation"`
	CompletionMarker          string                `json:"completion_marker"`
	Reasons                   []AuthorizationReason `json:"reasons"`
	Steps                     []StepResult          `json:"steps"`
}

// ParseBaselineEvidence strictly parses one bounded JSON evidence document.
func ParseBaselineEvidence(reader io.Reader) (BaselineEvidence, error) {
	if reader == nil {
		return BaselineEvidence{}, fmt.Errorf("parse local baseline evidence: reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaximumEvidenceBytes+1))
	if err != nil {
		return BaselineEvidence{}, fmt.Errorf("parse local baseline evidence: %w", err)
	}
	if len(data) > MaximumEvidenceBytes {
		return BaselineEvidence{}, fmt.Errorf("parse local baseline evidence: document exceeds %d bytes", MaximumEvidenceBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence BaselineEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return BaselineEvidence{}, fmt.Errorf("parse local baseline evidence: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return BaselineEvidence{}, fmt.Errorf("parse local baseline evidence: trailing JSON document")
		}
		return BaselineEvidence{}, fmt.Errorf("parse local baseline evidence: trailing data: %w", err)
	}
	return evidence, nil
}

// AuthorizeThreeNodeDiagnostic evaluates every raw step again and opens only
// the next local or cloud diagnostic gate when the exact reviewed contract is proven.
func AuthorizeThreeNodeDiagnostic(evidence BaselineEvidence) AuthorizationResult {
	result := AuthorizationResult{
		Schema: AuthorizationResultSchema, Outcome: OutcomeInsufficientEvidence,
		Reason: string(AuthorizationReasonSeal), ExitCode: 6, Reasons: make([]AuthorizationReason, 0),
		CompletionGeneration: evidence.CompletionGeneration, CompletionMarker: "completion.json",
	}
	add := func(reason AuthorizationReason) {
		for _, existing := range result.Reasons {
			if existing == reason {
				return
			}
		}
		result.Reasons = append(result.Reasons, reason)
	}
	if evidence.Schema != BaselineEvidenceSchema {
		add(AuthorizationReasonSchema)
	}
	if !validLowerHex(evidence.BaselineInvocationID, 32) {
		add(AuthorizationReasonExecutionSeal)
	}
	if !validCompletionGeneration(evidence.CompletionGeneration) ||
		evidence.CompletionGeneration != baselineCompletionGeneration(evidence) {
		add(AuthorizationReasonPublication)
	}
	if evidence.DiagnosticOutcome != string(OutcomeClean) {
		add(AuthorizationReasonBaselineOutcome)
	}
	if !reviewedSettingsSatisfied(evidence.Settings) {
		add(AuthorizationReasonReviewedDefaults)
	}
	if !sourceEvidenceComplete(evidence.Source) {
		add(AuthorizationReasonSource)
	}
	if !evidence.Seal.PayloadComplete || !evidence.Seal.ChecksumsVerified {
		add(AuthorizationReasonSeal)
	}
	if !evidence.FilesystemObservationComplete || evidence.ObservedFilesystemFreePercent < 0 || evidence.ObservedFilesystemFreePercent > 100 {
		add(AuthorizationReasonFilesystem)
	} else if evidence.ObservedFilesystemFreePercent < evidence.Settings.MinimumFreePercent {
		add(AuthorizationReasonFilesystemFree)
	}
	if !dataFilesystemIdentityComplete(evidence) {
		add(AuthorizationReasonFilesystem)
	}
	if len(evidence.StepClosures) != len(ReviewedOfferedSendQPS) {
		add(AuthorizationReasonRateSteps)
	}
	result.Steps = make([]StepResult, 0, len(evidence.StepClosures))
	firstStepFailure := false
	var reviewedTarget ReviewedTargetEvidence
	var reviewedExecutionSeal ExecutionSealEvidence
	seenServerGenerations := make(map[[2]string]struct{}, len(evidence.StepClosures))
	var previousTerminalAt time.Time
	for index, closure := range evidence.StepClosures {
		step := closure.Evidence
		if !ValidateStepClosure(closure) {
			add(AuthorizationReasonSeal)
		}
		if index >= len(ReviewedOfferedSendQPS) || step.OfferedSendQPS != ReviewedOfferedSendQPS[index] {
			add(AuthorizationReasonRateSteps)
		}
		if !stepMatchesSettings(step, evidence.Settings) {
			add(AuthorizationReasonReviewedDefaults)
		}
		if index == 0 {
			reviewedTarget = step.Target
			reviewedExecutionSeal = step.ExecutionSeal
		} else if step.Target != reviewedTarget {
			add(AuthorizationReasonExecutionTarget)
		}
		if step.ExecutionSeal.BaselineInvocationID != evidence.BaselineInvocationID {
			add(AuthorizationReasonExecutionSeal)
		}
		if index > 0 && step.ExecutionSeal != reviewedExecutionSeal {
			add(AuthorizationReasonExecutionSeal)
		}
		generation := [2]string{strconv.Itoa(step.Timeline.Terminal.Server.PID), step.Timeline.Terminal.Server.StartToken}
		if step.Timeline.Terminal.Server.PID <= 0 || strings.TrimSpace(step.Timeline.Terminal.Server.StartToken) == "" {
			add(AuthorizationReasonServerGeneration)
		} else if _, duplicate := seenServerGenerations[generation]; duplicate {
			add(AuthorizationReasonServerGeneration)
		} else {
			seenServerGenerations[generation] = struct{}{}
		}
		if index > 0 && !previousTerminalAt.Before(step.Timeline.Warmup.StartedAt) {
			add(AuthorizationReasonServerGeneration)
		}
		previousTerminalAt = step.Timeline.Terminal.ObservedAt
		stepResult := EvaluateStep(step)
		result.Steps = append(result.Steps, stepResult)
		if !stepResult.Clean {
			add(AuthorizationReasonStepNotClean)
			if !firstStepFailure {
				result.FirstFailingRate = step.OfferedSendQPS
				result.Outcome = stepResult.Outcome
				result.ExitCode = 3
				if stepResult.Outcome == OutcomeInsufficientEvidence {
					result.ExitCode = 6
				}
				if len(stepResult.Reasons) > 0 {
					result.Reason = string(stepResult.Reasons[0])
				}
				firstStepFailure = true
			}
		} else if !firstStepFailure {
			result.HighestCleanRate = step.OfferedSendQPS
		}
	}
	sort.Slice(result.Reasons, func(i, j int) bool { return result.Reasons[i] < result.Reasons[j] })
	result.ReviewedContractSatisfied = len(result.Reasons) == 0
	result.Authorizes = result.ReviewedContractSatisfied
	if result.Authorizes {
		result.Outcome = OutcomeClean
		result.Reason = "complete"
		result.ExitCode = 0
	} else if !firstStepFailure && len(result.Reasons) > 0 {
		result.Outcome = OutcomeInsufficientEvidence
		result.Reason = string(result.Reasons[0])
		result.ExitCode = 6
	}
	if !result.Authorizes && len(evidence.StepClosures) == 0 {
		switch Outcome(evidence.DiagnosticOutcome) {
		case OutcomeHostConfounded, OutcomeStorageConfounded:
			result.Outcome = Outcome(evidence.DiagnosticOutcome)
			result.Reason = strings.TrimSpace(evidence.DiagnosticReason)
			if result.Reason == "" {
				result.Reason = string(AuthorizationReasonBaselineOutcome)
			}
			result.ExitCode = 2
		case OutcomeInsufficientEvidence:
			result.Outcome = OutcomeInsufficientEvidence
			result.Reason = strings.TrimSpace(evidence.DiagnosticReason)
			if result.Reason == "" {
				result.Reason = string(AuthorizationReasonSeal)
			}
			result.ExitCode = 6
		}
	}
	if !result.Authorizes && Outcome(evidence.DiagnosticOutcome) != OutcomeInsufficientEvidence &&
		evidence.FilesystemObservationComplete && evidence.ObservedFilesystemFreePercent >= 0 &&
		evidence.ObservedFilesystemFreePercent < evidence.Settings.MinimumFreePercent {
		result.Outcome = OutcomeStorageConfounded
		result.Reason = "filesystem_free_below_10_percent"
		result.ExitCode = 2
	}
	return result
}

func dataFilesystemIdentityComplete(evidence BaselineEvidence) bool {
	directory := evidence.CanonicalDataDir
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory ||
		strings.TrimSpace(evidence.DataFilesystemDevice) != evidence.DataFilesystemDevice || evidence.DataFilesystemDevice == "" {
		return false
	}
	if evidence.FilesystemObservationComplete {
		return evidence.DataFilesystemDevice != "unavailable" && evidence.DataFilesystemTotalBlocks > 0 && evidence.DataFilesystemBlockSize > 0
	}
	return evidence.DataFilesystemDevice == "unavailable" && evidence.DataFilesystemTotalBlocks == 0 && evidence.DataFilesystemBlockSize == 0
}

func validCompletionGeneration(generation string) bool {
	if len(generation) != 64 {
		return false
	}
	for _, char := range generation {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func reviewedSettingsSatisfied(settings ReviewedSettings) bool {
	return settings.Channels == 1000 && settings.ActiveConnections >= 2500 &&
		settings.GroupMembers == 10 && settings.SendConcurrency == 2800 && settings.PayloadBytes == 128 &&
		settings.WarmupSeconds == 60 &&
		settings.MeasuredSeconds == 300 && settings.DrainBudgetSeconds == 90 &&
		settings.ACKTimeoutSeconds == 15 && settings.ReceiveACK && settings.HeartbeatEnabled &&
		settings.SenderPickRoundRobin && settings.MinimumFreePercent == 10 &&
		settings.LogicalSlotGroups == 12 && settings.HashSlots == 256 &&
		settings.SlotReplicas == 1 && settings.ChannelReplicas == 1 &&
		settings.CommitFlushWindowMicros == 200 && settings.CommitCoordinatorShards == 1 &&
		settings.SyncCommit && settings.CleanCluster && settings.OwnedCluster && settings.OwnedWorker &&
		settings.CanonicalSourceConfig &&
		settings.MetricsEndpointCount == 1
}

func stepMatchesSettings(step StepEvidence, settings ReviewedSettings) bool {
	return step.RequiredActiveConnections == settings.ActiveConnections &&
		step.ConfiguredGroupMembers == settings.GroupMembers &&
		step.ConfiguredWarmupSeconds == settings.WarmupSeconds &&
		step.ConfiguredMeasuredSeconds == settings.MeasuredSeconds &&
		step.ConfiguredDrainBudgetSeconds == settings.DrainBudgetSeconds
}

func sourceEvidenceComplete(source SourceEvidence) bool {
	revision := strings.TrimSpace(source.Revision)
	if source.Dirty || !source.RebuildableFromRevision || (len(revision) != 40 && len(revision) != 64) {
		return false
	}
	for _, char := range revision {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}
