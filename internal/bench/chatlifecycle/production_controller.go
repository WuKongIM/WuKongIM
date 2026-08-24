package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var errProductionController = errors.New("chat lifecycle production controller failed")

type productionControllerFailureStage string
type productionControllerFailureReason string

const (
	productionControllerFailureCutValidation       productionControllerFailureStage = "cut_validation"
	productionControllerFailureLiveDiagnostic      productionControllerFailureStage = "live_diagnostic"
	productionControllerFailureWorkerQueues        productionControllerFailureStage = "worker_queues"
	productionControllerFailureObservationMissing  productionControllerFailureStage = "observation_missing"
	productionControllerFailureObservationOrder    productionControllerFailureStage = "observation_order"
	productionControllerFailureWorkerEvidence      productionControllerFailureStage = "worker_evidence"
	productionControllerFailureMetaCheckpoint      productionControllerFailureStage = "meta_checkpoint"
	productionControllerFailureLatencyAttribution  productionControllerFailureStage = "latency_attribution"
	productionControllerFailureVerdict             productionControllerFailureStage = "verdict"
	productionControllerFailureDataset             productionControllerFailureStage = "dataset"
	productionControllerFailureQualificationReport productionControllerFailureStage = "qualification_report"
	productionControllerFailureSnapshotAggregate   productionControllerFailureStage = "snapshot_aggregate"

	productionControllerFailureReasonSnapshotCount     productionControllerFailureReason = "snapshot_count"
	productionControllerFailureReasonSnapshotFence     productionControllerFailureReason = "snapshot_fence"
	productionControllerFailureReasonSnapshotStale     productionControllerFailureReason = "snapshot_stale"
	productionControllerFailureReasonHistogramSchema   productionControllerFailureReason = "histogram_schema"
	productionControllerFailureReasonCounterRegression productionControllerFailureReason = "counter_regression"
	productionControllerFailureReasonAggregateOverflow productionControllerFailureReason = "aggregate_overflow"
	productionControllerFailureReasonInvalid           productionControllerFailureReason = "invalid"
)

type productionObservationEvidence interface {
	Begin(time.Time) error
	Snapshot() ProductionObservationSnapshot
}

type productionWorkerQueueEvidence interface {
	ObserveWorkerQueues(context.Context, time.Time, []WorkerSnapshot) error
}

type productionLifecycleEvidence interface {
	Run(context.Context, WorkerFence) error
	Snapshot() LifecycleProofSnapshot
}

type productionMetaEvidence interface {
	Checkpoint(context.Context, []WorkerSnapshot, LifecycleSlotAssignment, bool) error
}

type productionDatasetEvidence interface {
	ProbeDatasetDigest(context.Context, Config) (string, error)
}

// ProductionEvidenceControllerOptions composes bounded read-only evidence
// sources. Coordinator remains the sole owner of worker traffic and lifecycle.
type ProductionEvidenceControllerOptions struct {
	Config         Config
	OutputDir      string
	Observation    productionObservationEvidence
	Lifecycle      productionLifecycleEvidence
	Meta           productionMetaEvidence
	MetaAccounting *MetaCreateAccounting
	Dataset        productionDatasetEvidence
	SlotAssignment LifecycleSlotAssignment
	// Continuous enables the one formal Soak-to-capacity process lifetime. It
	// changes report/finalization semantics but never permits disk resumption.
	Continuous bool
	// DiagnosticLog receives one bounded, identity-free JSON line per worker
	// evidence cut. Nil disables stream logging while retaining the status file.
	DiagnosticLog io.Writer
}

// ProductionEvidenceController reduces live sources into one non-resumable
// qualification/final report sequence for CoordinatorRunHooks.
type ProductionEvidenceController struct {
	mu sync.Mutex

	cfg              Config
	outputDir        string
	observation      productionObservationEvidence
	lifecycle        productionLifecycleEvidence
	meta             productionMetaEvidence
	accounting       *MetaCreateAccounting
	dataset          productionDatasetEvidence
	assignment       LifecycleSlotAssignment
	continuous       bool
	diagnosticLog    io.Writer
	continuing       bool
	awaitingCapacity bool

	begun             bool
	closed            bool
	start             CoordinatorRunStart
	datasetDigest     string
	recorder          *CheckpointRecorder
	diagnostics       *liveDiagnosticRecorder
	evaluator         *VerdictEvaluator
	lastEvaluatorAt   time.Time
	lastObservation   ProductionObservationSnapshot
	lastObservationID uint64
	// lastOfferedUnderdelivery fences monotonic load-delivery evidence used by
	// formal Soak latency attribution; it resets only with a fresh evaluator.
	lastOfferedUnderdelivery uint64
	prepared                 CheckpointEvidence
	frozen                   VerdictSnapshot

	lifecycleCancel context.CancelFunc
	lifecycleDone   chan error
	lifecycleJoined bool
	lifecycleErr    error
}

var _ CoordinatorRunHooks = (*ProductionEvidenceController)(nil)
var _ CoordinatorCapacityPeriodicHooks = (*ProductionEvidenceController)(nil)

// NewProductionEvidenceController validates composition without performing I/O.
func NewProductionEvidenceController(options ProductionEvidenceControllerOptions) (*ProductionEvidenceController, error) {
	output := strings.TrimSpace(options.OutputDir)
	if options.Config.Validate() != nil || output == "" || options.Observation == nil || options.Lifecycle == nil ||
		options.Meta == nil || options.MetaAccounting == nil || options.Dataset == nil ||
		options.SlotAssignment.HashSlotCount() != formalHashSlots {
		return nil, errProductionController
	}
	return &ProductionEvidenceController{
		cfg: options.Config, outputDir: filepath.Clean(output), observation: options.Observation,
		lifecycle: options.Lifecycle, meta: options.Meta, accounting: options.MetaAccounting,
		dataset: options.Dataset, assignment: options.SlotAssignment, continuous: options.Continuous,
		diagnosticLog: options.DiagnosticLog,
	}, nil
}

// OutputDir returns the fixed report directory without exposing credentials.
func (c *ProductionEvidenceController) OutputDir() string {
	if c == nil {
		return ""
	}
	return c.outputDir
}

// Begin binds the exact post-grant measured start and launches the proof loop
// on a controller-owned context that Close always cancels and joins.
func (c *ProductionEvidenceController) Begin(ctx context.Context, start CoordinatorRunStart) error {
	if c == nil || ctx == nil {
		return errProductionController
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.begun || c.closed || c.awaitingCapacity || start.StartedAt.IsZero() || start.Config.Validate() != nil ||
		start.Config.RunID != c.cfg.RunID || start.Fence.RunID != c.cfg.RunID || !validWorkerFence(start.Fence) {
		return errProductionController
	}
	if err := os.MkdirAll(c.outputDir, 0o700); err != nil {
		return errProductionController
	}
	recorder, err := NewCheckpointRecorder(c.cfg, start.Fence, start.StartedAt)
	if err != nil {
		return errProductionController
	}
	evaluator, err := NewVerdictEvaluator(start.StartedAt, c.cfg.Thresholds)
	if err != nil {
		return errProductionController
	}
	// Arm the observation source at the exact grant barrier before the live
	// dataset proof. That proof may legitimately take longer than one observer
	// phase, but it must not consume the first exact-hour forced-GC window.
	if !c.continuing {
		if err := c.observation.Begin(start.StartedAt); err != nil {
			return errProductionController
		}
	}
	digest, err := c.dataset.ProbeDatasetDigest(ctx, c.cfg)
	if err != nil || !validReportHash(digest) {
		return errProductionController
	}
	if c.continuing {
		if digest != c.datasetDigest {
			return errProductionController
		}
	}
	if err := writeRunStartReceipt(filepath.Join(c.outputDir, "run-start.json"), RunStartReceipt{
		Schema: RunStartReceiptSchemaV1, Stage: c.cfg.Stage,
		StartedAt: start.StartedAt, ExpectedEndAt: start.StartedAt.Add(c.cfg.measuredDuration()),
		RunHash: hashReportValue(start.Fence.RunID), AssignmentHash: hashReportValue(start.Fence.AssignmentID),
		Generation: start.Fence.Generation,
	}); err != nil {
		return errProductionController
	}
	c.start, c.datasetDigest, c.recorder, c.evaluator = start, digest, recorder, evaluator
	if c.continuing {
		observation := c.observation.Snapshot()
		if observation.Sequence == 0 || observation.At.IsZero() || observation.At.After(start.StartedAt) {
			return errProductionController
		}
		c.lastObservation, c.lastObservationID = observation, observation.Sequence
		c.lastEvaluatorAt = start.StartedAt
	} else {
		lifecycleCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		c.lifecycleCancel, c.lifecycleDone = cancel, done
		go func() { done <- c.lifecycle.Run(lifecycleCtx, start.Fence) }()
	}
	c.diagnostics = newLiveDiagnosticRecorder(c.outputDir, c.cfg.RunID, start.StartedAt, c.diagnosticLog)
	c.begun = true
	return nil
}

// ContinueCapacity changes only the report/evaluator stage while preserving
// the live observation source, lifecycle proof loop, meta ledger, dataset, and
// worker fence. The caller must invoke it synchronously after the continuous
// hour-72 report and before starting the capacity coordinator phase.
func (c *ProductionEvidenceController) ContinueCapacity(cfg Config, outputDir string) error {
	if c == nil {
		return errProductionController
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	output := strings.TrimSpace(outputDir)
	if !c.continuous || !c.awaitingCapacity || c.closed || output == "" || cfg.Validate() != nil ||
		cfg.Profile != ProfileFormal || cfg.Mode != ModeCapacity || cfg.Stage != StageFormal ||
		cfg.RunID != c.cfg.RunID {
		return errProductionController
	}
	c.cfg, c.outputDir = cfg, filepath.Clean(output)
	c.begun, c.awaitingCapacity, c.continuing = false, false, true
	c.start = CoordinatorRunStart{}
	c.recorder, c.diagnostics, c.evaluator = nil, nil, nil
	c.lastEvaluatorAt, c.lastObservation = time.Time{}, ProductionObservationSnapshot{}
	c.lastObservationID = 0
	c.lastOfferedUnderdelivery = 0
	c.prepared, c.frozen = CheckpointEvidence{}, VerdictSnapshot{}
	return nil
}

// Observe reduces one worker cut. Qualification writes do not stop or mutate
// workers; terminal decisions are frozen for the post-stop Finalize call.
func (c *ProductionEvidenceController) Observe(ctx context.Context, cut CoordinatorEvidenceCut) (CoordinatorOutcome, error) {
	if c == nil || ctx == nil {
		return "", errProductionController
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validCut(cut) {
		return c.observeFailure(cut, productionControllerFailureCutValidation, errProductionController)
	}
	if c.diagnostics == nil || c.diagnostics.Observe(cut.At, cut.Kind, cut.Snapshots) != nil {
		return c.observeFailure(cut, productionControllerFailureLiveDiagnostic, errProductionController)
	}
	if source, ok := c.observation.(productionWorkerQueueEvidence); ok {
		if err := source.ObserveWorkerQueues(ctx, cut.At, cut.Snapshots); err != nil {
			return c.observeFailure(cut, productionControllerFailureWorkerQueues, errProductionController)
		}
	}

	observation := c.observation.Snapshot()
	observationAtCut := false
	if observation.Sequence == 0 {
		if cut.Kind == CoordinatorCutPeriodic {
			return "", nil
		}
		return c.observeFailure(cut, productionControllerFailureObservationMissing, errProductionController)
	}
	if observation.Sequence > c.lastObservationID && !observation.At.After(cut.At) {
		if observation.At.IsZero() || len(observation.Resources) != coordinatorWorkerCount {
			return c.observeFailure(cut, productionControllerFailureObservationOrder, errProductionController)
		}
		if !observation.At.After(c.lastEvaluatorAt) || observation.At.Equal(cut.At) {
			// A complete observer round may commit after the worker cut whose wall
			// clock it precedes. Rebase that late evidence onto this cut instead of
			// either dropping it or inserting it behind the monotonic evaluator.
			observationAtCut = true
		} else {
			resourceObservation := VerdictObservation{At: observation.At, Resources: observation.Resources, Signals: observation.Signals}
			if err := c.evaluator.Observe(resourceObservation); err != nil && !c.evaluator.Snapshot().Terminal {
				return c.observeFailure(cut, productionControllerFailureVerdict, errProductionController)
			}
			c.lastEvaluatorAt = observation.At
		}
		c.lastObservation = observation
		c.lastObservationID = observation.Sequence
	}
	if c.lastObservation.Sequence == 0 || !cut.At.After(c.lastEvaluatorAt) {
		return c.observeFailure(cut, productionControllerFailureObservationOrder, errProductionController)
	}

	lifecycle := c.lifecycle.Snapshot()
	correctness, latency, signals, err := projectWorkerVerdictEvidence(c.cfg, cut.Snapshots, lifecycle)
	if err != nil {
		return c.observeFailure(cut, productionControllerFailureWorkerEvidence, errProductionController)
	}
	if cut.Kind == CoordinatorCutTerminal {
		aggregator, aggregateErr := NewCoordinatorSnapshotAggregator(c.start.Fence)
		if aggregateErr == nil {
			_, aggregateErr = aggregator.Aggregate(cut.Snapshots)
		}
		if aggregateErr != nil {
			return c.observeFailureReason(
				cut, productionControllerFailureSnapshotAggregate,
				productionControllerSnapshotFailureReason(aggregateErr), errProductionController,
			)
		}
	}
	correctness.ActivationRejections = c.lastObservation.ActivationRejections
	signals = append(signals, productionLifecycleSignals(lifecycle)...)
	signals = append(signals, c.lifecycleSignalsLocked()...)
	if cut.Kind == CoordinatorCutQualification {
		metaErr := c.meta.Checkpoint(ctx, cut.Snapshots, c.assignment, c.accounting.Snapshot().Checkpoints > 0)
		switch {
		case errors.Is(metaErr, ErrLifecycleProductFailure):
			signals = append(signals, VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseMetaCreateProduct})
		case metaErr != nil:
			return c.observeFailure(cut, productionControllerFailureMetaCheckpoint, errProductionController)
		}
	}
	resources := []NodeResourceSample(nil)
	if observationAtCut {
		resources = observation.Resources
		signals = append(signals, observation.Signals...)
	}
	if cut.StopRequested {
		signals = append(signals, VerdictSignal{Outcome: VerdictOperatorStop, Cause: VerdictCauseOperatorRequested})
	}
	latencyAttribution := CapacityAttributionNone
	if c.cfg.Profile == ProfileFormal && c.cfg.Mode == ModeSoak && c.cfg.Stage == StageFormal {
		var offered uint64
		latencyAttribution, offered, err = formalSoakLatencyAttribution(
			c.cfg, c.lastObservation, cut.Snapshots, c.lastOfferedUnderdelivery,
		)
		if err != nil {
			return c.observeFailure(cut, productionControllerFailureLatencyAttribution, errProductionController)
		}
		c.lastOfferedUnderdelivery = offered
	}
	if err := c.evaluator.Observe(VerdictObservation{
		At: cut.At, Correctness: &correctness, Latency: &latency,
		LatencyAttribution: latencyAttribution, Resources: resources, Signals: signals,
	}); err != nil && !c.evaluator.Snapshot().Terminal {
		return c.observeFailure(cut, productionControllerFailureVerdict, errProductionController)
	}
	c.lastEvaluatorAt = cut.At

	if cut.Kind == CoordinatorCutQualification || cut.Kind == CoordinatorCutTerminal {
		if err := c.refreshDatasetLocked(ctx); err != nil {
			return c.observeFailure(cut, productionControllerFailureDataset, err)
		}
	}

	verdict := c.evaluator.Snapshot()
	if cut.Kind == CoordinatorCutTerminal && !verdict.Terminal {
		switch {
		case c.cfg.Stage == StageRehearsal:
			verdict.Outcome, verdict.Cause, verdict.Terminal = VerdictRehearsalPass, VerdictCauseRehearsalCompleted, true
		case c.cfg.Mode == ModeCapacity && cut.Capacity.Terminal:
			verdict = terminalCapacityVerdict(cut.Capacity)
		default:
			_ = c.evaluator.Finalize(cut.At)
			verdict = c.evaluator.Snapshot()
		}
	}
	evidence := c.checkpointEvidenceLocked(lifecycle, verdict, cut.Capacity)
	if cut.Kind == CoordinatorCutQualification && !verdict.Terminal {
		if _, err := c.recorder.CaptureAndWrite(cut.At, cut.Snapshots, evidence, c.paths("qualification")); err != nil {
			return c.observeFailure(cut, productionControllerFailureQualificationReport, errProductionController)
		}
		return "", nil
	}
	if verdict.Terminal {
		c.prepared, c.frozen = evidence, verdict
		return coordinatorOutcomeForVerdict(verdict), nil
	}
	return "", nil
}

func (c *ProductionEvidenceController) observeFailure(
	cut CoordinatorEvidenceCut,
	stage productionControllerFailureStage,
	err error,
) (CoordinatorOutcome, error) {
	return c.observeFailureReason(cut, stage, "", err)
}

func (c *ProductionEvidenceController) observeFailureReason(
	cut CoordinatorEvidenceCut,
	stage productionControllerFailureStage,
	reason productionControllerFailureReason,
	err error,
) (CoordinatorOutcome, error) {
	if c != nil && c.diagnosticLog != nil {
		record := struct {
			Event  string                            `json:"event"`
			At     time.Time                         `json:"at"`
			Cut    CoordinatorCutKind                `json:"cut"`
			Stage  productionControllerFailureStage  `json:"stage"`
			Reason productionControllerFailureReason `json:"reason,omitempty"`
		}{
			Event: "wkbench.chat_lifecycle.controller_failure",
			At:    cut.At, Cut: cut.Kind, Stage: stage, Reason: reason,
		}
		if body, marshalErr := json.Marshal(record); marshalErr == nil {
			_, _ = c.diagnosticLog.Write(append(body, '\n'))
		}
	}
	if err == nil {
		err = errProductionController
	}
	return "", err
}

func productionControllerSnapshotFailureReason(err error) productionControllerFailureReason {
	switch {
	case errors.Is(err, ErrCoordinatorSnapshotCount):
		return productionControllerFailureReasonSnapshotCount
	case errors.Is(err, ErrCoordinatorSnapshotFence):
		return productionControllerFailureReasonSnapshotFence
	case errors.Is(err, ErrCoordinatorSnapshotStale):
		return productionControllerFailureReasonSnapshotStale
	case errors.Is(err, ErrCoordinatorHistogramSchema):
		return productionControllerFailureReasonHistogramSchema
	case errors.Is(err, ErrCoordinatorSnapshotRegression):
		return productionControllerFailureReasonCounterRegression
	case errors.Is(err, ErrCoordinatorSnapshotOverflow):
		return productionControllerFailureReasonAggregateOverflow
	default:
		return productionControllerFailureReasonInvalid
	}
}

func formalSoakLatencyAttribution(
	cfg Config,
	observation ProductionObservationSnapshot,
	workers []WorkerSnapshot,
	previousUnderdelivery uint64,
) (CapacityAttribution, uint64, error) {
	if cfg.Profile != ProfileFormal || cfg.Mode != ModeSoak || cfg.Stage != StageFormal ||
		len(workers) != coordinatorWorkerCount {
		return CapacityAttributionNone, 0, errProductionController
	}
	var offered uint64
	for _, worker := range workers {
		if math.MaxUint64-offered < worker.Harness.OfferedUnderdelivery {
			return CapacityAttributionNone, 0, errProductionController
		}
		offered += worker.Harness.OfferedUnderdelivery
	}
	if offered < previousUnderdelivery {
		return CapacityAttributionNone, 0, errProductionController
	}
	resources := observation.ResourceEvidence.Capacity
	complete := resources.Complete && resources.ProcessesComplete && resources.WorkerQueuesComplete
	if !complete {
		return CapacityAttributionInsufficient, offered, nil
	}
	if offered > previousUnderdelivery || formalSoakResourcesSustained(resources) {
		return CapacityAttributionInfrastructure, offered, nil
	}
	if productionCapacityResourceCurrentlyHigh(cfg, resources) {
		return CapacityAttributionInsufficient, offered, nil
	}
	return CapacityAttributionProduct, offered, nil
}

func formalSoakResourcesSustained(resources ReportCapacityResourceEvidence) bool {
	for host := 0; host < productionHostCount; host++ {
		if resources.CPUSustainedActive[host] || resources.MemorySustainedActive[host] {
			return true
		}
	}
	for node := 0; node < coordinatorWorkerCount; node++ {
		if resources.QueueSustainedActive[node] {
			return true
		}
		for queue := 0; queue < workerBoundedQueueCount; queue++ {
			if resources.WorkerQueueSustainedActive[node][queue] {
				return true
			}
		}
	}
	return false
}

// ObserveCapacityPeriodic keeps resource, runtime-safety, and worker-queue
// evidence live between long capacity-window boundaries.
func (c *ProductionEvidenceController) ObserveCapacityPeriodic(
	ctx context.Context,
	cut CoordinatorEvidenceCut,
) (CoordinatorOutcome, error) {
	if cut.Kind != CoordinatorCutPeriodic {
		return "", errProductionController
	}
	return c.Observe(ctx, cut)
}

func terminalCapacityVerdict(capacity CapacitySnapshot) VerdictSnapshot {
	verdict := VerdictSnapshot{Terminal: true}
	switch {
	case capacity.Outcome == CapacityPassed:
		verdict.Outcome, verdict.Cause = VerdictPass, VerdictCauseCompleted
	case capacity.Outcome == CapacityPassedWithWarning:
		verdict.Outcome, verdict.Cause = VerdictPassedWithCapacityWarning, VerdictCauseInfrastructureCapacity
	case capacity.Outcome == CapacityInsufficientEvidence:
		verdict.Outcome, verdict.Cause = VerdictInsufficientEvidence, VerdictCauseInsufficientEvidence
	case capacity.Outcome == CapacityProductFailure && capacity.Cause == CapacityCauseHeadroomLatency:
		verdict.Outcome, verdict.Cause = VerdictProductFailure, VerdictCauseCapacityHeadroomLatency
	case capacity.Outcome == CapacityProductFailure:
		verdict.Outcome, verdict.Cause = VerdictProductFailure, VerdictCauseWorkerProduct
	case capacity.Outcome == CapacityInfrastructureFailure:
		verdict.Outcome, verdict.Cause = VerdictInfrastructureFailure, VerdictCauseDiskExhausted
	default:
		verdict.Outcome, verdict.Cause = VerdictHarnessInvalid, VerdictCauseInvalidObservation
	}
	return verdict
}

// Finalize joins lifecycle work, rechecks dataset/meta evidence against the
// final worker snapshots, and atomically writes both final formats.
func (c *ProductionEvidenceController) Finalize(ctx context.Context, cut CoordinatorFinalCut) error {
	if c == nil || ctx == nil {
		return errProductionController
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.begun || c.closed || cut.Start.Fence != c.start.Fence || cut.At.IsZero() ||
		len(cut.FinalSnapshots) != coordinatorWorkerCount || !c.frozen.Terminal {
		return errProductionController
	}
	if cut.Continuous != (c.continuous && !c.continuing) {
		return errProductionController
	}
	if c.diagnostics == nil || c.diagnostics.Observe(cut.At, CoordinatorCutTerminal, cut.FinalSnapshots) != nil {
		return errProductionController
	}
	if !cut.Continuous {
		c.stopLifecycleLocked()
	}
	if err := c.refreshDatasetLocked(ctx); err != nil {
		return err
	}
	beforeMeta := c.accounting.Snapshot()
	hadMetaProduct := metaCreateSnapshotHasProductFailure(beforeMeta)
	metaErr := c.meta.Checkpoint(ctx, cut.FinalSnapshots, c.assignment, beforeMeta.Checkpoints > 0)
	// A prior structurally valid product snapshot is terminal evidence. A later
	// harness-only reconciliation error cannot erase it or suppress the report.
	metaProduct := hadMetaProduct || errors.Is(metaErr, ErrLifecycleProductFailure)
	if metaErr != nil && !metaProduct {
		return errProductionController
	}
	lifecycle := c.lifecycle.Snapshot()
	current := VerdictSignal{Outcome: c.frozen.Outcome, Cause: c.frozen.Cause}
	if metaProduct {
		metaSignal := VerdictSignal{Outcome: VerdictProductFailure, Cause: VerdictCauseMetaCreateProduct}
		if verdictSignalPrecedes(metaSignal, current) {
			current = metaSignal
		}
	}
	for _, signal := range append(productionLifecycleSignals(lifecycle), c.lifecycleSignalsLocked()...) {
		if verdictSignalPrecedes(signal, current) {
			current = signal
		}
	}
	if current.Outcome != c.frozen.Outcome || current.Cause != c.frozen.Cause {
		c.frozen.Outcome, c.frozen.Cause, c.frozen.Terminal = current.Outcome, current.Cause, true
	}
	finalEvidence := c.checkpointEvidenceLocked(lifecycle, c.frozen, cut.Capacity)
	if _, err := c.recorder.CaptureAndWrite(cut.At, cut.FinalSnapshots, finalEvidence, c.paths("final")); err != nil {
		return errProductionController
	}
	if cut.Continuous {
		c.awaitingCapacity = true
		return nil
	}
	c.closed = true
	return nil
}

// Close is idempotent and guarantees the long-running lifecycle loop is joined.
func (c *ProductionEvidenceController) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLifecycleLocked()
	c.closed = true
}

func (c *ProductionEvidenceController) validCut(cut CoordinatorEvidenceCut) bool {
	return c.begun && !c.closed && cut.Start.Fence == c.start.Fence &&
		cut.Start.StartedAt.Equal(c.start.StartedAt) && !cut.At.IsZero() && !cut.At.Before(c.start.StartedAt) &&
		len(cut.Snapshots) == coordinatorWorkerCount &&
		(cut.Kind == CoordinatorCutPeriodic || cut.Kind == CoordinatorCutQualification || cut.Kind == CoordinatorCutTerminal)
}

func (c *ProductionEvidenceController) refreshDatasetLocked(ctx context.Context) error {
	digest, err := c.dataset.ProbeDatasetDigest(ctx, c.cfg)
	if err != nil || digest != c.datasetDigest {
		return errProductionController
	}
	return nil
}

func (c *ProductionEvidenceController) checkpointEvidenceLocked(
	lifecycle LifecycleProofSnapshot,
	verdict VerdictSnapshot,
	capacity CapacitySnapshot,
) CheckpointEvidence {
	observation := c.lastObservation
	if !verdict.Terminal {
		// VerdictEvaluator uses provisional pass internally; persisted
		// qualification reports intentionally carry no outcome before finality.
		verdict.Outcome, verdict.Cause = "", ""
	}
	return CheckpointEvidence{
		DatasetDigest: c.datasetDigest, TopologyValidated: true,
		Lifecycle: lifecycle, MetaCreate: c.accounting.Snapshot(),
		Resources: observation.ResourceEvidence, Cluster: observation.ClusterEvidence,
		Verdict: verdict, Capacity: capacity.ReportEvidence(), Continuous: c.continuous,
	}
}

func (c *ProductionEvidenceController) paths(base string) CheckpointOutputPaths {
	return CheckpointOutputPaths{
		JSON: filepath.Join(c.outputDir, base+".json"), Markdown: filepath.Join(c.outputDir, base+".md"),
	}
}

func (c *ProductionEvidenceController) lifecycleSignalsLocked() []VerdictSignal {
	if !c.lifecycleJoined {
		select {
		case c.lifecycleErr = <-c.lifecycleDone:
			c.lifecycleJoined = true
		default:
		}
	}
	if !c.lifecycleJoined || c.lifecycleErr == nil || errors.Is(c.lifecycleErr, context.Canceled) {
		return nil
	}
	if errors.Is(c.lifecycleErr, ErrLifecycleProductFailure) {
		return []VerdictSignal{{Outcome: VerdictProductFailure, Cause: VerdictCauseLifecycleProduct}}
	}
	return []VerdictSignal{{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseLifecycleHarness}}
}

func (c *ProductionEvidenceController) stopLifecycleLocked() {
	if !c.begun || c.lifecycleJoined {
		return
	}
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
	}
	if c.lifecycleDone != nil {
		c.lifecycleErr = <-c.lifecycleDone
	}
	c.lifecycleJoined = true
}

func productionLifecycleSignals(snapshot LifecycleProofSnapshot) []VerdictSignal {
	if snapshot.ProductFailures > 0 {
		return []VerdictSignal{{Outcome: VerdictProductFailure, Cause: VerdictCauseLifecycleProduct}}
	}
	if snapshot.HarnessFailures > 0 {
		return []VerdictSignal{{Outcome: VerdictHarnessInvalid, Cause: VerdictCauseLifecycleHarness}}
	}
	return nil
}

func coordinatorOutcomeForVerdict(verdict VerdictSnapshot) CoordinatorOutcome {
	switch verdict.Outcome {
	case VerdictPass, VerdictRehearsalPass, VerdictPassedWithCapacityWarning:
		return CoordinatorCompleted
	case VerdictProductFailure:
		return CoordinatorProductFailure
	case VerdictHarnessInvalid, VerdictInsufficientEvidence:
		return CoordinatorHarnessInvalid
	case VerdictInfrastructureFailure:
		return CoordinatorInfrastructureFailure
	case VerdictOperatorStop:
		return CoordinatorStopped
	default:
		return CoordinatorHarnessInvalid
	}
}
