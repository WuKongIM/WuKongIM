package chatlifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrCheckpointConfig rejects invalid configuration, start time, or generation fence.
	ErrCheckpointConfig = errors.New("chat lifecycle checkpoint: invalid configuration")
	// ErrCheckpointSequence rejects early, duplicate, resumed, or post-terminal captures.
	ErrCheckpointSequence = errors.New("chat lifecycle checkpoint: invalid sequence")
	// ErrCheckpointEvidence rejects incomplete or unsafe report evidence.
	ErrCheckpointEvidence = errors.New("chat lifecycle checkpoint: invalid evidence")
	// ErrCheckpointOutput identifies a failed atomic checkpoint persistence attempt.
	ErrCheckpointOutput = errors.New("chat lifecycle checkpoint: output failed")
)

// CheckpointKind distinguishes the continuous qualification cut from final output.
type CheckpointKind string

const (
	CheckpointQualification CheckpointKind = "qualification"
	CheckpointFinal         CheckpointKind = "final"
)

// CheckpointEvidence contains only bounded aggregate evidence supplied by observers.
type CheckpointEvidence struct {
	TopologyValidated bool
	Lifecycle         LifecycleProofSnapshot
	MetaCreate        MetaCreateAccountingSnapshot
	Resources         ReportResourceEvidence
	Cluster           ReportClusterEvidence
	Verdict           VerdictSnapshot
	Capacity          ReportCapacityEvidence
	Warnings          []ReportWarningCode
	Samples           []ReportSample
}

// CheckpointOutputPaths requires one atomic JSON and Markdown sibling output.
type CheckpointOutputPaths struct {
	JSON     string
	Markdown string
}

// CheckpointRecorder owns one in-process, non-resumable qualification/final sequence.
// It consumes snapshots only and has no worker lifecycle or traffic-control methods.
type CheckpointRecorder struct {
	mu                    sync.Mutex
	cfg                   Config
	fence                 WorkerFence
	start                 time.Time
	configDigest          string
	aggregator            *CoordinatorSnapshotAggregator
	qualificationCaptured bool
	closed                bool
}

// NewCheckpointRecorder binds reports to one validated config and worker generation.
func NewCheckpointRecorder(cfg Config, fence WorkerFence, start time.Time) (*CheckpointRecorder, error) {
	if start.IsZero() || !validWorkerFence(fence) || cfg.RunID != fence.RunID || cfg.Validate() != nil {
		return nil, ErrCheckpointConfig
	}
	digest, err := digestCheckpointConfig(cfg)
	if err != nil {
		return nil, ErrCheckpointConfig
	}
	aggregator, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		return nil, ErrCheckpointConfig
	}
	return &CheckpointRecorder{cfg: cfg, fence: fence, start: start, configDigest: digest, aggregator: aggregator}, nil
}

// CaptureAndWrite persists both versioned formats before committing recorder
// sequence state. It validates snapshot cuts without stopping, starting,
// reassigning, or otherwise mutating any worker process, and an output failure
// can retry the identical snapshot cut.
func (r *CheckpointRecorder) CaptureAndWrite(
	at time.Time,
	snapshots []WorkerSnapshot,
	evidence CheckpointEvidence,
	outputs CheckpointOutputPaths,
) (Report, error) {
	if r == nil {
		return Report{}, ErrCheckpointConfig
	}
	if outputs.JSON == "" || outputs.Markdown == "" || filepath.Clean(outputs.JSON) == filepath.Clean(outputs.Markdown) {
		return Report{}, ErrCheckpointOutput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	report, nextAggregator, qualificationCaptured, closed, err := r.prepareLocked(at, snapshots, evidence)
	if err != nil {
		return Report{}, err
	}
	if err := WriteReportAtomic(outputs.JSON, report, ReportFormatJSON); err != nil {
		return Report{}, fmt.Errorf("%w: JSON: %v", ErrCheckpointOutput, err)
	}
	if err := WriteReportAtomic(outputs.Markdown, report, ReportFormatMarkdown); err != nil {
		return Report{}, fmt.Errorf("%w: Markdown: %v", ErrCheckpointOutput, err)
	}
	r.aggregator = nextAggregator
	r.qualificationCaptured = qualificationCaptured
	r.closed = closed
	return report, nil
}

func (r *CheckpointRecorder) prepareLocked(
	at time.Time,
	snapshots []WorkerSnapshot,
	evidence CheckpointEvidence,
) (Report, *CoordinatorSnapshotAggregator, bool, bool, error) {
	if r.closed || at.IsZero() || at.Before(r.start) || !validCheckpointEvidence(evidence) {
		if !validCheckpointEvidence(evidence) {
			return Report{}, nil, false, false, ErrCheckpointEvidence
		}
		return Report{}, nil, false, false, ErrCheckpointSequence
	}

	checkpointAt := r.start.Add(r.cfg.Thresholds.Timeline.Checkpoint)
	finalAt := r.start.Add(r.cfg.Thresholds.Timeline.Final)
	kind := CheckpointQualification
	terminal := evidence.Verdict.Terminal
	switch {
	case terminal && at.Before(checkpointAt):
		kind = CheckpointFinal
	case !r.qualificationCaptured:
		if at.Before(checkpointAt) {
			return Report{}, nil, false, false, ErrCheckpointSequence
		}
		if !at.Before(finalAt) {
			if !terminal || evidence.Verdict.Outcome == VerdictPass {
				return Report{}, nil, false, false, ErrCheckpointSequence
			}
			kind = CheckpointFinal
		} else {
			kind = CheckpointQualification
		}
	case terminal:
		kind = CheckpointFinal
	case at.Before(finalAt):
		return Report{}, nil, false, false, ErrCheckpointSequence
	default:
		kind = CheckpointFinal
	}
	if kind == CheckpointFinal && !terminal {
		return Report{}, nil, false, false, ErrCheckpointEvidence
	}
	if !validCheckpointSnapshotsForCut(snapshots, at.Sub(r.start), kind, evidence.Verdict) {
		return Report{}, nil, false, false, ErrCheckpointEvidence
	}

	nextAggregator := cloneCheckpointAggregator(r.aggregator)
	aggregated, err := nextAggregator.Aggregate(snapshots)
	if err != nil {
		return Report{}, nil, false, false, err
	}
	report := r.buildReport(kind, at, aggregated, evidence)
	if err := validateReport(report); err != nil {
		return Report{}, nil, false, false, ErrCheckpointEvidence
	}
	qualificationCaptured := r.qualificationCaptured
	closed := r.closed
	if kind == CheckpointQualification && !terminal {
		qualificationCaptured = true
	} else {
		closed = true
	}
	return report, nextAggregator, qualificationCaptured, closed, nil
}

func cloneCheckpointAggregator(source *CoordinatorSnapshotAggregator) *CoordinatorSnapshotAggregator {
	return &CoordinatorSnapshotAggregator{
		fence: source.fence, seen: source.seen, previous: source.previous, evidence: source.evidence,
	}
}

func (r *CheckpointRecorder) buildReport(kind CheckpointKind, at time.Time, snapshot CoordinatorSnapshot, evidence CheckpointEvidence) Report {
	workers := make([]ReportWorkerGeneration, coordinatorWorkerCount)
	for index := range workers {
		workers[index] = ReportWorkerGeneration{
			WorkerIndex: uint64(index), Generation: snapshot.Generation,
			SnapshotSequence: snapshot.WorkerSequence[index], Phase: snapshot.Phase,
		}
	}
	verdict := projectReportVerdict(evidence.Verdict)
	resources := evidence.Resources
	resources.Retention = verdict.Retention
	final := verdict.Terminal || kind == CheckpointFinal
	return Report{
		SchemaVersion: ReportSchemaVersion, ThresholdVersion: ReportThresholdVersion, DesignProfile: ReportDesignProfile,
		ConfigDigest: r.configDigest, Thresholds: r.cfg.Thresholds, Profile: r.cfg.Profile, Mode: r.cfg.Mode, Kind: kind,
		Final: final, Continue: !final,
		Fence: ReportFence{RunHash: hashReportValue(r.fence.RunID), AssignmentHash: hashReportValue(r.fence.AssignmentID), Generation: r.fence.Generation},
		Window: ReportTimeWindow{
			Start: r.start, WarmupEnd: r.start.Add(r.cfg.Thresholds.Timeline.Warmup),
			QualificationAt: r.start.Add(r.cfg.Thresholds.Timeline.Checkpoint),
			FinalAt:         r.start.Add(r.cfg.Thresholds.Timeline.Final), End: at, Elapsed: at.Sub(r.start),
		},
		MinimumWorkerUptime: snapshot.MinimumUptime,
		Topology: ReportTopologyProof{
			Validated: evidence.TopologyValidated, LogicalSlotGroups: r.cfg.Workload.Topology.LogicalSlotGroups,
			HashSlots: r.cfg.Workload.Topology.HashSlots, SlotReplicas: r.cfg.Workload.Topology.SlotReplicas,
			ChannelReplicas: r.cfg.Workload.Topology.ChannelReplicas,
		},
		Workers: workers, Sessions: snapshot.Sessions, Generated: snapshot.Generated,
		Messages: snapshot.Messages, Sync: snapshot.Sync, Correlation: snapshot.Correlation,
		Queues: snapshot.Queues, Harness: snapshot.Harness,
		EvidenceClassification: snapshot.EvidenceClassification, EvidenceCounts: snapshot.EvidenceCounts,
		Lifecycle: evidence.Lifecycle, MetaCreate: evidence.MetaCreate,
		Latency: ReportLatencyEvidence{
			SendACK: snapshot.SendackLatency, ReceiveACK: snapshot.RecvackLatency, FullSync: snapshot.Sync.Latency,
			Warnings: verdict.LatencyWarnings, AnomalyCount: verdict.LatencyAnomalyCount,
			Anomalies: append([]ReportLatencyAnomaly(nil), verdict.LatencyAnomalies...), Retention: verdict.Retention,
		},
		Resources: resources, Cluster: evidence.Cluster, Verdict: verdict, Capacity: evidence.Capacity,
		Warnings: append([]ReportWarningCode(nil), evidence.Warnings...),
		Samples:  append([]ReportSample(nil), evidence.Samples...),
	}
}

func projectReportVerdict(snapshot VerdictSnapshot) ReportVerdictEvidence {
	anomalies := make([]ReportLatencyAnomaly, len(snapshot.LatencyAnomalies))
	for index, anomaly := range snapshot.LatencyAnomalies {
		anomalies[index] = ReportLatencyAnomaly{At: anomaly.At, Operation: anomaly.Operation, Count: anomaly.Count}
	}
	return ReportVerdictEvidence{
		Outcome: snapshot.Outcome, Cause: snapshot.Cause, Terminal: snapshot.Terminal,
		CleanupErrorCount: snapshot.CleanupErrorCount, CleanupErrors: append([]VerdictCleanupErrorCode(nil), snapshot.CleanupErrors...),
		LatencyWarnings:     ReportLatencyWarnings{Hot: snapshot.LatencyWarnings.Hot, Cold: snapshot.LatencyWarnings.Cold, Sync: snapshot.LatencyWarnings.Sync},
		LatencyAnomalyCount: snapshot.LatencyAnomalyCount, LatencyAnomalies: anomalies, Retention: projectReportRetention(snapshot.Retention),
	}
}

func projectReportRetention(retention VerdictWindowRetention) ReportWindowRetention {
	return ReportWindowRetention{
		MinuteSamples: retention.MinuteSamples, MinuteCapacity: retention.MinuteCapacity,
		LatencySamples: retention.LatencySamples, LatencyCapacity: retention.LatencyCapacity,
		HeapSamples: retention.HeapSamples, HeapCapacity: retention.HeapCapacity,
		GoroutineSamples: retention.GoroutineSamples, GoroutineCapacity: retention.GoroutineCapacity,
	}
}

func validCheckpointEvidence(evidence CheckpointEvidence) bool {
	if !evidence.TopologyValidated || len(evidence.Warnings) > maxReportWarnings || len(evidence.Samples) > maxReportSamples ||
		!validCoordinatorHistogram(evidence.Lifecycle.ReheatLatency) || len(evidence.Verdict.CleanupErrors) > maxVerdictCleanupErrors ||
		len(evidence.Verdict.LatencyAnomalies) > maxVerdictLatencyAnomalies {
		return false
	}
	for _, warning := range evidence.Warnings {
		if !validReportWarning(warning) {
			return false
		}
	}
	for index, sample := range evidence.Samples {
		if !validReportSampleClass(sample.Class) || !validReportHash(sample.Hash) {
			return false
		}
		for previous := 0; previous < index; previous++ {
			if evidence.Samples[previous].Class == sample.Class && evidence.Samples[previous].Index == sample.Index {
				return false
			}
		}
	}
	if !validReportCapacity(evidence.Capacity) || !validCheckpointVerdict(evidence.Verdict) {
		return false
	}
	if evidence.Verdict.Terminal {
		return validVerdictOutcome(evidence.Verdict.Outcome) && validVerdictCause(evidence.Verdict.Cause)
	}
	return evidence.Verdict.Outcome == "" && evidence.Verdict.Cause == ""
}

func validCheckpointVerdict(verdict VerdictSnapshot) bool {
	if verdict.CleanupErrorCount < uint64(len(verdict.CleanupErrors)) ||
		verdict.LatencyAnomalyCount < uint64(len(verdict.LatencyAnomalies)) ||
		!validReportRetention(projectReportRetention(verdict.Retention)) {
		return false
	}
	for _, code := range verdict.CleanupErrors {
		if code != VerdictCleanupWorkerStop && code != VerdictCleanupSnapshot && code != VerdictCleanupObserver {
			return false
		}
	}
	for _, anomaly := range verdict.LatencyAnomalies {
		if !validReportLatencyAnomaly(ReportLatencyAnomaly{At: anomaly.At, Operation: anomaly.Operation, Count: anomaly.Count}) {
			return false
		}
	}
	return !verdict.Terminal || (verdict.Outcome == VerdictPass) == (verdict.Cause == VerdictCauseCompleted)
}

func validCheckpointSnapshotsForCut(snapshots []WorkerSnapshot, elapsed time.Duration, kind CheckpointKind, verdict VerdictSnapshot) bool {
	if len(snapshots) != coordinatorWorkerCount || elapsed < 0 {
		return false
	}
	for _, snapshot := range snapshots {
		if snapshot.Uptime < elapsed {
			return false
		}
		if !verdict.Terminal && (kind != CheckpointQualification || snapshot.Phase != WorkerPhaseRunning) {
			return false
		}
		if verdict.Outcome == VerdictPass && snapshot.Phase != WorkerPhaseFinal {
			return false
		}
	}
	return true
}

func digestCheckpointConfig(cfg Config) (string, error) {
	body, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
