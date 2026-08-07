package chatlifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ReportSchemaVersion identifies the persisted JSON and Markdown contract.
	ReportSchemaVersion = "wukongim/chat-lifecycle-report/v2"
	// ReportThresholdVersion binds reports to the reviewed exact threshold semantics.
	ReportThresholdVersion = "wukongim/chat-lifecycle-thresholds/v1"
	// ReportDesignProfile identifies the approved lifecycle-soak design baseline.
	ReportDesignProfile = "chat-lifecycle-soak/2026-08-04"

	maxReportWarnings = 64
	maxReportSamples  = 64
)

var (
	// ErrReportInvalid rejects incomplete or non-redacted report projections.
	ErrReportInvalid = errors.New("chat lifecycle report: invalid projection")
	// ErrReportFormat rejects output formats outside the versioned JSON/Markdown pair.
	ErrReportFormat = errors.New("chat lifecycle report: unsupported format")
)

// ReportFormat selects one versioned persisted representation.
type ReportFormat string

const (
	ReportFormatJSON     ReportFormat = "json"
	ReportFormatMarkdown ReportFormat = "markdown"
)

// ReportWarningCode is a closed warning-only vocabulary. Warnings never drive verdicts.
type ReportWarningCode string

const (
	ReportWarningShortLatencyBreach ReportWarningCode = "short_latency_breach"
	ReportWarningLongLatencyAnomaly ReportWarningCode = "long_latency_anomaly"
	ReportWarningCapacityNotRun     ReportWarningCode = "capacity_not_run"
)

// ReportSampleClass is the closed source vocabulary for stable hashed samples.
type ReportSampleClass string

const (
	ReportSampleLifecycle ReportSampleClass = "lifecycle"
	ReportSampleLatency   ReportSampleClass = "latency"
	ReportSampleResource  ReportSampleClass = "resource"
	ReportSampleCluster   ReportSampleClass = "cluster"
	ReportSampleCapacity  ReportSampleClass = "capacity"
)

// ReportSample persists only a stable index and SHA-256 digest, never raw identity data.
type ReportSample struct {
	Class ReportSampleClass `json:"class"`
	Index uint64            `json:"index"`
	Hash  string            `json:"hash"`
}

// ReportFence is the one-generation fence with raw run and assignment values removed.
type ReportFence struct {
	RunHash        string `json:"run_hash"`
	AssignmentHash string `json:"assignment_hash"`
	Generation     uint64 `json:"generation"`
}

// ReportTimeWindow binds one checkpoint to the same process-lifetime interval.
type ReportTimeWindow struct {
	Start           time.Time     `json:"start"`
	WarmupEnd       time.Time     `json:"warmup_end"`
	QualificationAt time.Time     `json:"qualification_at"`
	FinalAt         time.Time     `json:"final_at"`
	End             time.Time     `json:"end"`
	Elapsed         time.Duration `json:"elapsed"`
}

// ReportTopologyProof is the checked cluster shape, not an endpoint inventory.
type ReportTopologyProof struct {
	Validated         bool `json:"validated"`
	LogicalSlotGroups int  `json:"logical_slot_groups"`
	HashSlots         int  `json:"hash_slots"`
	SlotReplicas      int  `json:"slot_replicas"`
	ChannelReplicas   int  `json:"channel_replicas"`
}

// ReportWorkerGeneration proves that all checkpoint snapshots retain one worker fence.
type ReportWorkerGeneration struct {
	WorkerIndex      uint64      `json:"worker_index"`
	Generation       uint64      `json:"generation"`
	SnapshotSequence uint64      `json:"snapshot_sequence"`
	Phase            WorkerPhase `json:"phase"`
}

// ReportLatencyEvidence combines bounded worker histograms with verdict-window evidence.
type ReportLatencyEvidence struct {
	SendACK      WorkerHistogramSnapshot `json:"sendack"`
	ReceiveACK   WorkerHistogramSnapshot `json:"receive_ack"`
	FullSync     WorkerHistogramSnapshot `json:"full_sync"`
	Warnings     ReportLatencyWarnings   `json:"warnings"`
	AnomalyCount uint64                  `json:"anomaly_count"`
	Anomalies    []ReportLatencyAnomaly  `json:"anomalies,omitempty"`
	Retention    ReportWindowRetention   `json:"retention"`
}

// ReportLatencyWarnings is the tagged fixed warning projection.
type ReportLatencyWarnings struct {
	Hot  uint64 `json:"hot"`
	Cold uint64 `json:"cold"`
	Sync uint64 `json:"sync"`
}

// ReportLatencyAnomaly is the tagged bounded anomaly projection.
type ReportLatencyAnomaly struct {
	At        time.Time        `json:"at"`
	Operation LatencyOperation `json:"operation"`
	Count     uint64           `json:"count"`
}

// ReportWindowRetention exposes only fixed reducer sizes for bounded-memory audits.
type ReportWindowRetention struct {
	MinuteSamples     int    `json:"minute_samples"`
	MinuteCapacity    int    `json:"minute_capacity"`
	LatencySamples    [3]int `json:"latency_samples"`
	LatencyCapacity   int    `json:"latency_capacity"`
	HeapSamples       [3]int `json:"heap_samples"`
	HeapCapacity      int    `json:"heap_capacity"`
	GoroutineSamples  [3]int `json:"goroutine_samples"`
	GoroutineCapacity int    `json:"goroutine_capacity"`
}

// ReportResourceNodeEvidence contains bounded numeric trend endpoints for one stable node index.
type ReportResourceNodeEvidence struct {
	DataFilesystemBytes          uint64 `json:"data_filesystem_bytes"`
	DataFilesystemAvailableBytes uint64 `json:"data_filesystem_available_bytes"`
	ForcedGCSamples              uint64 `json:"forced_gc_samples"`
	HeapStartBytes               uint64 `json:"heap_start_bytes"`
	HeapEndBytes                 uint64 `json:"heap_end_bytes"`
	GoroutineStart               uint64 `json:"goroutine_start"`
	GoroutineEnd                 uint64 `json:"goroutine_end"`
	QueueBaseline                uint64 `json:"queue_baseline"`
	QueueCurrent                 uint64 `json:"queue_current"`
	InflightBaseline             uint64 `json:"inflight_baseline"`
	InflightCurrent              uint64 `json:"inflight_current"`
}

// ReportResourceEvidence preserves exactly three per-node projections and bounded retention counts.
type ReportResourceEvidence struct {
	Nodes     [coordinatorWorkerCount]ReportResourceNodeEvidence `json:"nodes"`
	Retention ReportWindowRetention                              `json:"retention"`
}

// ReportClusterEvidence is low-cardinality health, Slot, replica, and placement evidence.
type ReportClusterEvidence struct {
	HealthySamples          uint64 `json:"healthy_samples"`
	UnhealthySamples        uint64 `json:"unhealthy_samples"`
	LogicalSlotGroups       uint64 `json:"logical_slot_groups"`
	LeaderGroups            uint64 `json:"leader_groups"`
	FullReplicaGroups       uint64 `json:"full_replica_groups"`
	HotReplicaLagBreaches   uint64 `json:"hot_replica_lag_breaches"`
	LeaderImbalanceWarnings uint64 `json:"leader_imbalance_warnings"`
}

// ReportCapacityEvidence is the stable seam populated by the later aged-data staircase.
type ReportCapacityEvidence struct {
	Attempted          bool   `json:"attempted"`
	Completed          bool   `json:"completed"`
	MaximumPassingRate uint64 `json:"maximum_passing_rate"`
	FirstFailingRate   uint64 `json:"first_failing_rate"`
	RecoveryPassed     bool   `json:"recovery_passed"`
}

// ReportVerdictEvidence is a tagged, bounded projection of the frozen evaluator state.
type ReportVerdictEvidence struct {
	Outcome             VerdictOutcome            `json:"outcome,omitempty"`
	Cause               VerdictCause              `json:"cause,omitempty"`
	Terminal            bool                      `json:"terminal"`
	CleanupErrorCount   uint64                    `json:"cleanup_error_count"`
	CleanupErrors       []VerdictCleanupErrorCode `json:"cleanup_errors,omitempty"`
	LatencyWarnings     ReportLatencyWarnings     `json:"latency_warnings"`
	LatencyAnomalyCount uint64                    `json:"latency_anomaly_count"`
	LatencyAnomalies    []ReportLatencyAnomaly    `json:"latency_anomalies,omitempty"`
	Retention           ReportWindowRetention     `json:"retention"`
}

// Report is the complete identity-free persisted checkpoint contract.
type Report struct {
	SchemaVersion    string `json:"schema_version"`
	ThresholdVersion string `json:"threshold_version"`
	DesignProfile    string `json:"design_profile"`
	ConfigDigest     string `json:"config_digest"`
	// DatasetDigest is the immutable target-issued identity used by later aged-data admission.
	DatasetDigest          string                          `json:"dataset_digest"`
	Thresholds             ThresholdsConfig                `json:"thresholds"`
	Profile                Profile                         `json:"profile"`
	Mode                   Mode                            `json:"mode"`
	Kind                   CheckpointKind                  `json:"kind"`
	Final                  bool                            `json:"final"`
	Continue               bool                            `json:"continue"`
	Fence                  ReportFence                     `json:"fence"`
	Window                 ReportTimeWindow                `json:"window"`
	MinimumWorkerUptime    time.Duration                   `json:"minimum_worker_uptime"`
	Topology               ReportTopologyProof             `json:"topology"`
	Workers                []ReportWorkerGeneration        `json:"worker_generations"`
	Sessions               WorkerSessionSnapshot           `json:"sessions"`
	Generated              WorkerGeneratedSnapshot         `json:"generated"`
	Messages               WorkerMessageSnapshot           `json:"messages"`
	Sync                   WorkerSyncSnapshot              `json:"sync"`
	Correlation            WorkerCorrelationSnapshot       `json:"correlation"`
	Queues                 WorkerQueueSnapshot             `json:"queues"`
	Harness                WorkerHarnessSnapshot           `json:"harness"`
	EvidenceClassification SyncClassification              `json:"evidence_classification,omitempty"`
	EvidenceCounts         [FailureClassHarness + 1]uint64 `json:"evidence_counts"`
	Lifecycle              LifecycleProofSnapshot          `json:"lifecycle"`
	MetaCreate             MetaCreateAccountingSnapshot    `json:"meta_create"`
	Latency                ReportLatencyEvidence           `json:"latency"`
	Resources              ReportResourceEvidence          `json:"resources"`
	Cluster                ReportClusterEvidence           `json:"cluster"`
	Verdict                ReportVerdictEvidence           `json:"verdict"`
	Capacity               ReportCapacityEvidence          `json:"capacity"`
	Warnings               []ReportWarningCode             `json:"warnings,omitempty"`
	Samples                []ReportSample                  `json:"samples,omitempty"`
}

// MarshalReport validates and renders one deterministic redacted representation.
func MarshalReport(report Report, format ReportFormat) ([]byte, error) {
	if err := validateReport(report); err != nil {
		return nil, err
	}
	jsonBody, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: marshal JSON: %v", ErrReportInvalid, err)
	}
	jsonBody = append(jsonBody, '\n')
	switch format {
	case ReportFormatJSON:
		return jsonBody, nil
	case ReportFormatMarkdown:
		var body strings.Builder
		body.WriteString("# WuKongIM chat lifecycle checkpoint\n\n")
		fmt.Fprintf(&body, "- schema_version: `%s`\n", report.SchemaVersion)
		fmt.Fprintf(&body, "- threshold_version: `%s`\n", report.ThresholdVersion)
		fmt.Fprintf(&body, "- design_profile: `%s`\n", report.DesignProfile)
		fmt.Fprintf(&body, "- profile: `%s`\n", report.Profile)
		fmt.Fprintf(&body, "- kind: `%s`\n", report.Kind)
		fmt.Fprintf(&body, "- verdict: `%s`\n", report.Verdict.Outcome)
		body.WriteString("\n## Redacted structured evidence\n\n")
		body.WriteString("```json\n")
		body.Write(jsonBody)
		body.WriteString("```\n")
		return []byte(body.String()), nil
	default:
		return nil, ErrReportFormat
	}
}

// WriteReportAtomic writes a mode-0600 sibling temporary, fsyncs it, and renames it.
func WriteReportAtomic(path string, report Report, format ReportFormat) error {
	body, err := MarshalReport(report, format)
	if err != nil {
		return err
	}
	if path == "" || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return ErrReportInvalid
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func validateReport(report Report) error {
	if report.SchemaVersion != ReportSchemaVersion || report.ThresholdVersion != ReportThresholdVersion ||
		report.DesignProfile != ReportDesignProfile || !validReportHash(report.ConfigDigest) || !validReportHash(report.DatasetDigest) ||
		validateThresholds(report.Thresholds) != nil ||
		(report.Profile != ProfileFormal && report.Profile != ProfileLocal) ||
		(report.Mode != ModeSoak && report.Mode != ModeCapacity) ||
		(report.Kind != CheckpointQualification && report.Kind != CheckpointFinal) ||
		!validReportHash(report.Fence.RunHash) || !validReportHash(report.Fence.AssignmentHash) ||
		report.Fence.Generation == 0 || report.Window.Start.IsZero() || report.Window.End.Before(report.Window.Start) ||
		report.Window.Elapsed != report.Window.End.Sub(report.Window.Start) ||
		report.MinimumWorkerUptime < report.Window.Elapsed ||
		!report.Window.WarmupEnd.Equal(report.Window.Start.Add(report.Thresholds.Timeline.Warmup)) ||
		!report.Window.QualificationAt.Equal(report.Window.Start.Add(report.Thresholds.Timeline.Checkpoint)) ||
		!report.Window.FinalAt.Equal(report.Window.Start.Add(report.Thresholds.Timeline.Final)) || !report.Topology.Validated ||
		report.Topology.LogicalSlotGroups <= 0 || report.Topology.HashSlots <= 0 ||
		report.Topology.SlotReplicas <= 0 || report.Topology.ChannelReplicas <= 0 ||
		len(report.Workers) != coordinatorWorkerCount || len(report.Warnings) > maxReportWarnings ||
		len(report.Samples) > maxReportSamples || !validReportVerdict(report) || !validReportCapacity(report.Capacity) ||
		!validMetaCreateAccountingSnapshot(report.MetaCreate) || !validMetaCreateVerdict(report.MetaCreate, report.Verdict) ||
		!validCoordinatorHistogram(report.Latency.SendACK) || !validCoordinatorHistogram(report.Latency.ReceiveACK) ||
		!validCoordinatorHistogram(report.Latency.FullSync) || !validCoordinatorHistogram(report.Lifecycle.ReheatLatency) {
		return ErrReportInvalid
	}
	if !validReportSyncClassification(report.Harness.Classification) || !validReportSyncClassification(report.EvidenceClassification) {
		return ErrReportInvalid
	}
	for index, worker := range report.Workers {
		if worker.WorkerIndex != uint64(index) || worker.Generation != report.Fence.Generation ||
			worker.SnapshotSequence == 0 || (worker.Phase != WorkerPhaseRunning && worker.Phase != WorkerPhaseFinal) {
			return ErrReportInvalid
		}
	}
	for _, warning := range report.Warnings {
		if !validReportWarning(warning) {
			return ErrReportInvalid
		}
	}
	for _, node := range report.Resources.Nodes {
		if node.DataFilesystemBytes < uint64(report.Thresholds.MinimumDataFilesystemBytes) ||
			node.DataFilesystemAvailableBytes > node.DataFilesystemBytes {
			return ErrReportInvalid
		}
	}
	if report.Latency.AnomalyCount < uint64(len(report.Latency.Anomalies)) ||
		report.Latency.AnomalyCount != report.Verdict.LatencyAnomalyCount ||
		report.Latency.Warnings != report.Verdict.LatencyWarnings ||
		report.Latency.Retention != report.Verdict.Retention || report.Resources.Retention != report.Verdict.Retention ||
		len(report.Latency.Anomalies) != len(report.Verdict.LatencyAnomalies) || !validReportRetention(report.Latency.Retention) {
		return ErrReportInvalid
	}
	for index, anomaly := range report.Latency.Anomalies {
		if !validReportLatencyAnomaly(anomaly) || anomaly != report.Verdict.LatencyAnomalies[index] {
			return ErrReportInvalid
		}
	}
	for index, sample := range report.Samples {
		if !validReportSampleClass(sample.Class) || !validReportHash(sample.Hash) {
			return ErrReportInvalid
		}
		for previous := 0; previous < index; previous++ {
			if report.Samples[previous].Class == sample.Class && report.Samples[previous].Index == sample.Index {
				return ErrReportInvalid
			}
		}
	}
	return nil
}

func validMetaCreateAccountingSnapshot(snapshot MetaCreateAccountingSnapshot) bool {
	if snapshot.Checkpoints == 0 {
		return false
	}
	var expected, created, already, errorsCount, external uint64
	for slot := range formalLogicalSlotGroups {
		var ok bool
		if expected, ok = checkedUint64Add(expected, snapshot.ExpectedBySlot[slot]); !ok {
			return false
		}
		if created, ok = checkedUint64Add(created, snapshot.CreatedBySlot[slot]); !ok {
			return false
		}
		if already, ok = checkedUint64Add(already, snapshot.AlreadyExistingBySlot[slot]); !ok {
			return false
		}
		if errorsCount, ok = checkedUint64Add(errorsCount, snapshot.ErrorsBySlot[slot]); !ok {
			return false
		}
		if snapshot.CreatedBySlot[slot] > snapshot.ExpectedBySlot[slot] {
			if external, ok = checkedUint64Add(external, snapshot.CreatedBySlot[slot]-snapshot.ExpectedBySlot[slot]); !ok {
				return false
			}
		}
	}
	return expected == snapshot.ExpectedUnique && created == snapshot.Created &&
		already == snapshot.AlreadyExisting && errorsCount == snapshot.Errors && external == snapshot.ExternalDemoActivity
}

func validMetaCreateVerdict(snapshot MetaCreateAccountingSnapshot, verdict ReportVerdictEvidence) bool {
	if metaCreateSnapshotHasProductFailure(snapshot) {
		return verdict.Terminal && verdict.Outcome == VerdictProductFailure
	}
	return verdict.Cause != VerdictCauseMetaCreateProduct
}

func metaCreateSnapshotHasProductFailure(snapshot MetaCreateAccountingSnapshot) bool {
	if snapshot.Checkpoints == 0 {
		return false
	}
	if snapshot.Errors != 0 {
		return true
	}
	for slot := range formalLogicalSlotGroups {
		if snapshot.ErrorsBySlot[slot] != 0 || snapshot.CreatedBySlot[slot] < snapshot.ExpectedBySlot[slot] {
			return true
		}
	}
	return false
}

func validReportSyncClassification(classification SyncClassification) bool {
	return classification == "" || classification == SyncClassificationHarnessInvalid || classification == SyncClassificationProductFailure
}

func validReportVerdict(report Report) bool {
	if report.Final == report.Continue {
		return false
	}
	if len(report.Verdict.CleanupErrors) > maxVerdictCleanupErrors ||
		len(report.Verdict.LatencyAnomalies) > maxVerdictLatencyAnomalies ||
		report.Verdict.CleanupErrorCount < uint64(len(report.Verdict.CleanupErrors)) ||
		report.Verdict.LatencyAnomalyCount < uint64(len(report.Verdict.LatencyAnomalies)) ||
		!validReportRetention(report.Verdict.Retention) {
		return false
	}
	for _, code := range report.Verdict.CleanupErrors {
		if code != VerdictCleanupWorkerStop && code != VerdictCleanupSnapshot && code != VerdictCleanupObserver {
			return false
		}
	}
	for _, anomaly := range report.Verdict.LatencyAnomalies {
		if !validReportLatencyAnomaly(anomaly) {
			return false
		}
	}
	if !report.Verdict.Terminal {
		return report.Kind == CheckpointQualification && report.Continue && !report.Final &&
			report.Verdict.Outcome == "" && report.Verdict.Cause == ""
	}
	if !report.Final || report.Continue || !validVerdictOutcome(report.Verdict.Outcome) || !validVerdictCause(report.Verdict.Cause) {
		return false
	}
	if (report.Verdict.Outcome == VerdictPass) != (report.Verdict.Cause == VerdictCauseCompleted) ||
		(report.Verdict.Outcome == VerdictPass && (report.Kind != CheckpointFinal ||
			(report.Mode == ModeSoak && report.Window.End.Before(report.Window.FinalAt)))) {
		return false
	}
	return true
}

func validReportCapacity(capacity ReportCapacityEvidence) bool {
	if !capacity.Attempted {
		return !capacity.Completed && capacity.MaximumPassingRate == 0 && capacity.FirstFailingRate == 0 && !capacity.RecoveryPassed
	}
	if capacity.FirstFailingRate > 0 && capacity.FirstFailingRate <= capacity.MaximumPassingRate {
		return false
	}
	if !capacity.Completed {
		return !capacity.RecoveryPassed
	}
	// A completed staircase always includes an overload boundary before its
	// fixed-rate recovery, even when the start rate itself was the first failure.
	return capacity.FirstFailingRate > 0
}

func validReportLatencyAnomaly(anomaly ReportLatencyAnomaly) bool {
	return !anomaly.At.IsZero() && anomaly.Count > 0 &&
		(anomaly.Operation == LatencyHotSendACK || anomaly.Operation == LatencyColdSendACK || anomaly.Operation == LatencyFullSync)
}

func validReportRetention(retention ReportWindowRetention) bool {
	if retention.MinuteSamples < 0 || retention.MinuteCapacity < 0 || retention.MinuteSamples > retention.MinuteCapacity ||
		retention.LatencyCapacity < 0 || retention.HeapCapacity < 0 || retention.GoroutineCapacity < 0 {
		return false
	}
	for index := 0; index < 3; index++ {
		if retention.LatencySamples[index] < 0 || retention.LatencySamples[index] > retention.LatencyCapacity ||
			retention.HeapSamples[index] < 0 || retention.HeapSamples[index] > retention.HeapCapacity ||
			retention.GoroutineSamples[index] < 0 || retention.GoroutineSamples[index] > retention.GoroutineCapacity {
			return false
		}
	}
	return true
}

func validVerdictOutcome(outcome VerdictOutcome) bool {
	return outcome == VerdictPass || outcome == VerdictProductFailure || outcome == VerdictHarnessInvalid ||
		outcome == VerdictInfrastructureFailure || outcome == VerdictOperatorStop
}

func validVerdictCause(cause VerdictCause) bool {
	switch cause {
	case VerdictCauseCompleted, VerdictCauseMessageLoss, VerdictCauseMessageDuplicate, VerdictCauseMessageCorruption,
		VerdictCauseSequenceRegression, VerdictCauseTerminalSend, VerdictCauseActivationRejection,
		VerdictCauseOverallFirstAttemptRate, VerdictCauseMinuteFirstAttemptRate, VerdictCauseCounterRegression,
		VerdictCauseQueueSaturation, VerdictCauseObserverGap, VerdictCauseServerCrash, VerdictCauseDiskExhausted,
		VerdictCauseOperatorRequested, VerdictCauseHotLatency, VerdictCauseColdLatency, VerdictCauseSyncLatency,
		VerdictCauseInvalidObservation, VerdictCauseHeapGrowth, VerdictCauseGoroutineGrowth, VerdictCauseQueueRecovery:
		return true
	case VerdictCauseWorkerProduct, VerdictCauseWorkerHarness, VerdictCauseLifecycleProduct, VerdictCauseLifecycleHarness,
		VerdictCauseMetaCreateProduct:
		return true
	default:
		return false
	}
}

func validReportWarning(warning ReportWarningCode) bool {
	return warning == ReportWarningShortLatencyBreach || warning == ReportWarningLongLatencyAnomaly || warning == ReportWarningCapacityNotRun
}

func validReportSampleClass(class ReportSampleClass) bool {
	return class == ReportSampleLifecycle || class == ReportSampleLatency || class == ReportSampleResource ||
		class == ReportSampleCluster || class == ReportSampleCapacity
}

func validReportHash(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	if hexValue != strings.ToLower(hexValue) {
		return false
	}
	decoded, err := hex.DecodeString(hexValue)
	return err == nil && len(decoded) == sha256.Size
}

func hashReportValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}
