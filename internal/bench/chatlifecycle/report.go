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
	ReportSchemaVersion = "wukongim/chat-lifecycle-report/v4"
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
	ReportWarningShortLatencyBreach             ReportWarningCode = "short_latency_breach"
	ReportWarningLongLatencyAnomaly             ReportWarningCode = "long_latency_anomaly"
	ReportWarningCapacityNotRun                 ReportWarningCode = "capacity_not_run"
	ReportWarningRehearsalLongWindowsIncomplete ReportWarningCode = "rehearsal_long_windows_incomplete"
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
	HotSendACK                   WorkerHistogramSnapshot `json:"hot_sendack"`
	ColdFirstCreateSendACK       WorkerHistogramSnapshot `json:"cold_first_create_sendack"`
	WorkerLifecycleReheatSendACK WorkerHistogramSnapshot `json:"worker_lifecycle_reheat_sendack"`
	ReceiveACK                   WorkerHistogramSnapshot `json:"receive_ack"`
	FullSync                     WorkerHistogramSnapshot `json:"full_sync"`
	Warnings                     ReportLatencyWarnings   `json:"warnings"`
	AnomalyCount                 uint64                  `json:"anomaly_count"`
	Anomalies                    []ReportLatencyAnomaly  `json:"anomalies,omitempty"`
	Retention                    ReportWindowRetention   `json:"retention"`
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

const productionHostCount = coordinatorWorkerCount + 1
const serviceBoundedQueueCount = 2
const workerBoundedQueueCount = 4

// ReportCapacityResourceEvidence is a bounded monotonic projection used to
// distinguish sustained infrastructure saturation from product latency.
type ReportCapacityResourceEvidence struct {
	// Samples counts complete four-host resource rounds.
	Samples uint64 `json:"samples"`
	// MissingSamples counts rounds missing any required host or process signal.
	MissingSamples uint64 `json:"missing_samples"`
	// SustainedWindow is the exact continuous-high duration required for attribution.
	SustainedWindow time.Duration `json:"sustained_window"`
	// HostCPUPercentBasisPoints stores service hosts 0..2 then load host 3.
	HostCPUPercentBasisPoints [productionHostCount]uint32 `json:"host_cpu_percent_basis_points"`
	// HostMemoryPercentBasisPoints stores service hosts 0..2 then load host 3.
	HostMemoryPercentBasisPoints [productionHostCount]uint32 `json:"host_memory_percent_basis_points"`
	// ServiceQueuePercentBasisPoints stores WuKongIM service nodes 0..2.
	ServiceQueuePercentBasisPoints [coordinatorWorkerCount]uint32 `json:"service_queue_percent_basis_points"`
	// CPUHighSamples counts above-threshold rounds per service/load host index.
	CPUHighSamples [productionHostCount]uint64 `json:"cpu_high_samples"`
	// MemoryHighSamples counts above-threshold rounds per service/load host index.
	MemoryHighSamples [productionHostCount]uint64 `json:"memory_high_samples"`
	// QueueHighSamples counts above-threshold service-queue rounds per node index.
	QueueHighSamples [coordinatorWorkerCount]uint64 `json:"queue_high_samples"`
	// CPUSustainedEvents counts completed continuous-high windows per host index.
	CPUSustainedEvents [productionHostCount]uint64 `json:"cpu_sustained_events"`
	// MemorySustainedEvents counts completed continuous-high windows per host index.
	MemorySustainedEvents [productionHostCount]uint64 `json:"memory_sustained_events"`
	// QueueSustainedEvents counts completed service-queue windows per node index.
	QueueSustainedEvents [coordinatorWorkerCount]uint64 `json:"queue_sustained_events"`
	// CPUSustainedActive marks a currently completed CPU window per host index.
	CPUSustainedActive [productionHostCount]bool `json:"cpu_sustained_active"`
	// MemorySustainedActive marks a currently completed memory window per host index.
	MemorySustainedActive [productionHostCount]bool `json:"memory_sustained_active"`
	// QueueSustainedActive marks a currently completed service-queue window per node index.
	QueueSustainedActive [coordinatorWorkerCount]bool `json:"queue_sustained_active"`
	// WorkerQueueSamples counts complete three-worker checkpoint cuts.
	WorkerQueueSamples uint64 `json:"worker_queue_samples"`
	// WorkerQueueMissingSamples counts invalid or cadence-gapped worker cuts.
	WorkerQueueMissingSamples uint64 `json:"worker_queue_missing_samples"`
	// WorkerQueuePercentBasisPoints indexes worker then work/retry/inflight/transport queue.
	WorkerQueuePercentBasisPoints [coordinatorWorkerCount][workerBoundedQueueCount]uint32 `json:"worker_queue_percent_basis_points"`
	// WorkerQueueHighSamples counts above-threshold cuts by worker and queue kind.
	WorkerQueueHighSamples [coordinatorWorkerCount][workerBoundedQueueCount]uint64 `json:"worker_queue_high_samples"`
	// WorkerQueueSustainedEvents counts completed continuous-high windows by worker and queue kind.
	WorkerQueueSustainedEvents [coordinatorWorkerCount][workerBoundedQueueCount]uint64 `json:"worker_queue_sustained_events"`
	// WorkerQueueSustainedActive marks a currently completed window by worker and queue kind.
	WorkerQueueSustainedActive [coordinatorWorkerCount][workerBoundedQueueCount]bool `json:"worker_queue_sustained_active"`
	// WorkerQueuesComplete marks whether the latest cut contained every bounded queue.
	WorkerQueuesComplete bool `json:"worker_queues_complete"`
	// DataFilesystemBytes stores total bytes for service data disks 0..2 and load data disk 3.
	DataFilesystemBytes [productionHostCount]uint64 `json:"data_filesystem_bytes"`
	// DataFilesystemAvailableBytes stores free bytes using the same host order.
	DataFilesystemAvailableBytes [productionHostCount]uint64 `json:"data_filesystem_available_bytes"`
	// SystemFilesystemBytes stores total root-filesystem bytes in service/load host order.
	SystemFilesystemBytes [productionHostCount]uint64 `json:"system_filesystem_bytes"`
	// SystemFilesystemAvailableBytes stores free root-filesystem bytes in the same order.
	SystemFilesystemAvailableBytes [productionHostCount]uint64 `json:"system_filesystem_available_bytes"`
	// PrometheusBytes is the observed load-host retention-directory size.
	PrometheusBytes uint64 `json:"prometheus_bytes"`
	// NetworkTransmitBytes is the load host's monotonic non-loopback transmit total.
	NetworkTransmitBytes uint64 `json:"network_transmit_bytes"`
	// ProcessUp indexes service/load host then the closed production systemd unit order.
	ProcessUp [productionHostCount][productionProcessCount]bool `json:"process_up"`
	// ProcessCPUJiffies persists cumulative CPU evidence without process identifiers.
	ProcessCPUJiffies [productionHostCount][productionProcessCount]uint64 `json:"process_cpu_jiffies"`
	// ProcessResidentMemoryBytes persists current RSS using the same fixed indexes.
	ProcessResidentMemoryBytes [productionHostCount][productionProcessCount]uint64 `json:"process_resident_memory_bytes"`
	// ProcessesComplete proves every closed unit had an up/down row and every active unit had CPU/RSS.
	ProcessesComplete bool `json:"processes_complete"`
	// AccruedCostMicros is conservative scenario spend in millionths of CNY.
	AccruedCostMicros int64 `json:"accrued_cost_micros"`
	// LeaseRemainingSeconds is signed time until provider cleanup expiry.
	LeaseRemainingSeconds int64 `json:"lease_remaining_seconds"`
	// Complete marks whether the latest four-host resource round was complete.
	Complete bool `json:"complete"`
}

// ReportResourceEvidence preserves exactly three per-node projections and bounded retention counts.
type ReportResourceEvidence struct {
	Nodes     [coordinatorWorkerCount]ReportResourceNodeEvidence `json:"nodes"`
	Retention ReportWindowRetention                              `json:"retention"`
	Capacity  ReportCapacityResourceEvidence                     `json:"capacity"`
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
	Attempted          bool                `json:"attempted"`
	Completed          bool                `json:"completed"`
	Attribution        CapacityAttribution `json:"attribution,omitempty"`
	LowerBound         bool                `json:"lower_bound"`
	MaximumPassingRate uint64              `json:"maximum_passing_rate"`
	FirstFailingRate   uint64              `json:"first_failing_rate"`
	RecoveryPassed     bool                `json:"recovery_passed"`
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
	DatasetDigest string           `json:"dataset_digest"`
	Thresholds    ThresholdsConfig `json:"thresholds"`
	Profile       Profile          `json:"profile"`
	Mode          Mode             `json:"mode"`
	Stage         Stage            `json:"stage"`
	Kind          CheckpointKind   `json:"kind"`
	Final         bool             `json:"final"`
	Continue      bool             `json:"continue"`
	// Continuous marks an in-process formal-chain boundary whose worker fence
	// remains live. It is not authorization to resume from this report.
	Continuous             bool                            `json:"continuous"`
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
		fmt.Fprintf(&body, "- stage: `%s`\n", report.Stage)
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
		!validReportStage(report) ||
		(report.Kind != CheckpointQualification && report.Kind != CheckpointFinal) ||
		!validReportHash(report.Fence.RunHash) || !validReportHash(report.Fence.AssignmentHash) ||
		report.Fence.Generation == 0 || report.Window.Start.IsZero() || report.Window.End.Before(report.Window.Start) ||
		report.Window.Elapsed != report.Window.End.Sub(report.Window.Start) ||
		report.MinimumWorkerUptime < report.Window.Elapsed ||
		!report.Window.WarmupEnd.Equal(report.Window.Start.Add(report.Thresholds.Timeline.Warmup)) ||
		!report.Window.QualificationAt.Equal(report.Window.Start.Add(report.Thresholds.Timeline.Checkpoint)) ||
		!report.Window.FinalAt.Equal(report.Window.Start.Add(reportMeasuredDuration(report))) || !report.Topology.Validated ||
		report.Topology.LogicalSlotGroups <= 0 || report.Topology.HashSlots <= 0 ||
		report.Topology.SlotReplicas <= 0 || report.Topology.ChannelReplicas <= 0 ||
		len(report.Workers) != coordinatorWorkerCount || len(report.Warnings) > maxReportWarnings ||
		len(report.Samples) > maxReportSamples || !validReportVerdict(report) || !validReportCapacity(report.Capacity) ||
		!validReportCapacityResources(report.Resources.Capacity) ||
		!validMetaCreateAccountingSnapshot(report.MetaCreate) || !validMetaCreateVerdict(report.MetaCreate, report.Verdict) ||
		!validCoordinatorHistogram(report.Latency.HotSendACK) ||
		!validCoordinatorHistogram(report.Latency.ColdFirstCreateSendACK) ||
		!validCoordinatorHistogram(report.Latency.WorkerLifecycleReheatSendACK) ||
		!validCoordinatorHistogram(report.Latency.ReceiveACK) ||
		!validCoordinatorHistogram(report.Latency.FullSync) || !validCoordinatorHistogram(report.Lifecycle.ReheatLatency) {
		return ErrReportInvalid
	}
	if report.Continuous && (report.Profile != ProfileFormal || report.Stage != StageFormal) {
		return ErrReportInvalid
	}
	if !validReportSyncClassification(report.Harness.Classification) || !validReportSyncClassification(report.EvidenceClassification) {
		return ErrReportInvalid
	}
	for index, worker := range report.Workers {
		expectedFinalPhase := WorkerPhaseFinal
		if report.Continuous && report.Mode == ModeSoak && report.Final && report.Verdict.Outcome == VerdictPass {
			expectedFinalPhase = WorkerPhaseRunning
		}
		if worker.WorkerIndex != uint64(index) || worker.Generation != report.Fence.Generation ||
			worker.SnapshotSequence == 0 || (worker.Phase != WorkerPhaseRunning && worker.Phase != WorkerPhaseFinal) ||
			report.Final && report.Verdict.Terminal && validSuccessfulVerdictPair(report.Verdict.Outcome, report.Verdict.Cause) && worker.Phase != expectedFinalPhase {
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
	if report.Verdict.Outcome == VerdictPass || report.Verdict.Outcome == VerdictRehearsalPass ||
		report.Verdict.Outcome == VerdictPassedWithCapacityWarning || report.Verdict.Cause == VerdictCauseCompleted ||
		report.Verdict.Cause == VerdictCauseRehearsalCompleted || report.Verdict.Cause == VerdictCauseInfrastructureCapacity {
		if !validSuccessfulVerdictPair(report.Verdict.Outcome, report.Verdict.Cause) || report.Kind != CheckpointFinal ||
			(report.Mode == ModeSoak && report.Window.End.Before(report.Window.FinalAt)) ||
			(report.Verdict.Outcome == VerdictPass && report.Stage != StageFormal) ||
			(report.Verdict.Outcome == VerdictRehearsalPass && report.Stage != StageRehearsal) {
			return false
		}
	}
	if report.Verdict.Outcome == VerdictInsufficientEvidence || report.Verdict.Cause == VerdictCauseInsufficientEvidence {
		if report.Verdict.Outcome != VerdictInsufficientEvidence || report.Verdict.Cause != VerdictCauseInsufficientEvidence ||
			(report.Mode == ModeCapacity && report.Capacity.Attribution != CapacityAttributionInsufficient) ||
			(report.Mode == ModeSoak && report.Stage != StageFormal) ||
			(report.Mode != ModeCapacity && report.Mode != ModeSoak) {
			return false
		}
	}
	if report.Verdict.Cause == VerdictCauseCapacityHeadroomLatency &&
		(report.Verdict.Outcome != VerdictProductFailure || report.Mode != ModeCapacity ||
			report.Capacity.Attribution != CapacityAttributionProduct) {
		return false
	}
	if report.Verdict.Outcome == VerdictPassedWithCapacityWarning &&
		((report.Mode == ModeCapacity && report.Capacity.Attribution != CapacityAttributionInfrastructure) ||
			(report.Mode == ModeSoak && report.Stage != StageFormal) ||
			(report.Mode != ModeCapacity && report.Mode != ModeSoak)) {
		return false
	}
	if report.Stage == StageRehearsal && report.Verdict.Outcome == VerdictPass {
		return false
	}
	return true
}

func validReportCapacity(capacity ReportCapacityEvidence) bool {
	if !capacity.Attempted {
		return !capacity.Completed && capacity.Attribution == CapacityAttributionNone && !capacity.LowerBound &&
			capacity.MaximumPassingRate == 0 && capacity.FirstFailingRate == 0 && !capacity.RecoveryPassed
	}
	if capacity.Attribution != CapacityAttributionNone && capacity.Attribution != CapacityAttributionInfrastructure &&
		capacity.Attribution != CapacityAttributionProduct && capacity.Attribution != CapacityAttributionInsufficient {
		return false
	}
	if capacity.FirstFailingRate > 0 && capacity.FirstFailingRate <= capacity.MaximumPassingRate {
		return false
	}
	if !capacity.Completed {
		return !capacity.RecoveryPassed
	}
	if capacity.LowerBound {
		return capacity.Attribution == CapacityAttributionNone && capacity.FirstFailingRate == 0 && capacity.MaximumPassingRate > 0
	}
	return capacity.FirstFailingRate > 0 && capacity.Attribution != CapacityAttributionNone
}

func validReportCapacityResources(resources ReportCapacityResourceEvidence) bool {
	if resources.AccruedCostMicros < 0 {
		return false
	}
	if resources.WorkerQueuesComplete && resources.WorkerQueueSamples == 0 {
		return false
	}
	if resources.Complete && !resources.ProcessesComplete {
		return false
	}
	if resources.Samples == 0 {
		return !resources.Complete && (resources.MissingSamples == 0 && resources.SustainedWindow == 0 ||
			resources.MissingSamples > 0 && resources.SustainedWindow > 0)
	}
	if resources.SustainedWindow <= 0 {
		return false
	}
	for index := range resources.HostCPUPercentBasisPoints {
		if resources.HostCPUPercentBasisPoints[index] > 10_000 || resources.HostMemoryPercentBasisPoints[index] > 10_000 ||
			resources.DataFilesystemBytes[index] == 0 || resources.DataFilesystemAvailableBytes[index] > resources.DataFilesystemBytes[index] ||
			resources.SystemFilesystemBytes[index] == 0 || resources.SystemFilesystemAvailableBytes[index] > resources.SystemFilesystemBytes[index] ||
			resources.CPUSustainedActive[index] && resources.CPUSustainedEvents[index] == 0 ||
			resources.MemorySustainedActive[index] && resources.MemorySustainedEvents[index] == 0 {
			return false
		}
	}
	for index, value := range resources.ServiceQueuePercentBasisPoints {
		if value > 10_000 || resources.QueueSustainedActive[index] && resources.QueueSustainedEvents[index] == 0 {
			return false
		}
	}
	for worker := 0; worker < coordinatorWorkerCount; worker++ {
		for queue := 0; queue < workerBoundedQueueCount; queue++ {
			if resources.WorkerQueuePercentBasisPoints[worker][queue] > 10_000 ||
				resources.WorkerQueueSustainedActive[worker][queue] && resources.WorkerQueueSustainedEvents[worker][queue] == 0 {
				return false
			}
		}
	}
	for host := 0; host < productionHostCount; host++ {
		for process := 0; process < productionProcessCount; process++ {
			if resources.ProcessUp[host][process] && resources.ProcessResidentMemoryBytes[host][process] == 0 ||
				!resources.ProcessUp[host][process] && (resources.ProcessCPUJiffies[host][process] != 0 ||
					resources.ProcessResidentMemoryBytes[host][process] != 0) {
				return false
			}
		}
	}
	return true
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
	return outcome == VerdictPass || outcome == VerdictRehearsalPass || outcome == VerdictPassedWithCapacityWarning ||
		outcome == VerdictProductFailure || outcome == VerdictInsufficientEvidence || outcome == VerdictHarnessInvalid ||
		outcome == VerdictInfrastructureFailure || outcome == VerdictOperatorStop
}

func validVerdictCause(cause VerdictCause) bool {
	switch cause {
	case VerdictCauseCompleted, VerdictCauseRehearsalCompleted, VerdictCauseMessageLoss, VerdictCauseMessageDuplicate, VerdictCauseMessageCorruption,
		VerdictCauseSequenceRegression, VerdictCauseTerminalSend, VerdictCauseActivationRejection,
		VerdictCauseOverallFirstAttemptRate, VerdictCauseMinuteFirstAttemptRate, VerdictCauseCounterRegression,
		VerdictCauseQueueSaturation, VerdictCauseObserverGap, VerdictCauseServerCrash, VerdictCauseDiskExhausted,
		VerdictCauseBudgetExhausted, VerdictCauseLeaseExpiry,
		VerdictCauseOperatorRequested, VerdictCauseHotLatency, VerdictCauseColdLatency, VerdictCauseSyncLatency,
		VerdictCauseInvalidObservation, VerdictCauseHeapGrowth, VerdictCauseGoroutineGrowth, VerdictCauseQueueRecovery:
		return true
	case VerdictCauseWorkerProduct, VerdictCauseWorkerHarness, VerdictCauseLifecycleProduct, VerdictCauseLifecycleHarness,
		VerdictCauseMetaCreateProduct, VerdictCauseInfrastructureCapacity, VerdictCauseCapacityHeadroomLatency,
		VerdictCauseInsufficientEvidence:
		return true
	default:
		return false
	}
}

func validReportWarning(warning ReportWarningCode) bool {
	return warning == ReportWarningShortLatencyBreach || warning == ReportWarningLongLatencyAnomaly ||
		warning == ReportWarningCapacityNotRun || warning == ReportWarningRehearsalLongWindowsIncomplete
}

func validReportStage(report Report) bool {
	switch report.Stage {
	case StageFormal:
		return report.Profile == ProfileFormal
	case StageRehearsal:
		return report.Profile == ProfileFormal && report.Mode == ModeSoak
	case StageShakeout:
		return report.Profile == ProfileLocal && report.Mode == ModeSoak
	default:
		return false
	}
}

func reportMeasuredDuration(report Report) time.Duration {
	if report.Stage == StageRehearsal {
		return rehearsalDuration
	}
	return report.Thresholds.Timeline.Final
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
