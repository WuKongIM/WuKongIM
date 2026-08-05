package chatlifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var errProductionController = errors.New("chat lifecycle production controller failed")

type productionObservationEvidence interface {
	Begin(time.Time) error
	Snapshot() ProductionObservationSnapshot
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
}

// ProductionEvidenceController reduces live sources into one non-resumable
// qualification/final report sequence for CoordinatorRunHooks.
type ProductionEvidenceController struct {
	mu sync.Mutex

	cfg         Config
	outputDir   string
	observation productionObservationEvidence
	lifecycle   productionLifecycleEvidence
	meta        productionMetaEvidence
	accounting  *MetaCreateAccounting
	dataset     productionDatasetEvidence
	assignment  LifecycleSlotAssignment

	begun             bool
	closed            bool
	start             CoordinatorRunStart
	datasetDigest     string
	recorder          *CheckpointRecorder
	evaluator         *VerdictEvaluator
	lastEvaluatorAt   time.Time
	lastObservation   ProductionObservationSnapshot
	lastObservationID uint64
	prepared          CheckpointEvidence
	frozen            VerdictSnapshot

	lifecycleCancel context.CancelFunc
	lifecycleDone   chan error
	lifecycleJoined bool
	lifecycleErr    error
}

var _ CoordinatorRunHooks = (*ProductionEvidenceController)(nil)

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
		dataset: options.Dataset, assignment: options.SlotAssignment,
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
	if c.begun || c.closed || start.StartedAt.IsZero() || start.Config.Validate() != nil ||
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
	digest, err := c.dataset.ProbeDatasetDigest(ctx, c.cfg)
	if err != nil || !validReportHash(digest) {
		return errProductionController
	}
	if err := c.observation.Begin(start.StartedAt); err != nil {
		return errProductionController
	}
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	c.start, c.datasetDigest, c.recorder, c.evaluator = start, digest, recorder, evaluator
	c.lifecycleCancel, c.lifecycleDone = cancel, done
	c.begun = true
	go func() { done <- c.lifecycle.Run(lifecycleCtx, start.Fence) }()
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
		return "", errProductionController
	}

	observation := c.observation.Snapshot()
	if observation.Sequence == 0 {
		if cut.Kind == CoordinatorCutPeriodic {
			return "", nil
		}
		return "", errProductionController
	}
	if observation.Sequence > c.lastObservationID && !observation.At.After(cut.At) {
		if observation.At.IsZero() || !observation.At.After(c.lastEvaluatorAt) || len(observation.Resources) != coordinatorWorkerCount {
			return "", errProductionController
		}
		resourceObservation := VerdictObservation{At: observation.At, Resources: observation.Resources, Signals: observation.Signals}
		if observation.At.Equal(cut.At) {
			// The worker evidence below shares this exact atomic timestamp.
		} else {
			if err := c.evaluator.Observe(resourceObservation); err != nil && !c.evaluator.Snapshot().Terminal {
				return "", errProductionController
			}
			c.lastEvaluatorAt = observation.At
		}
		c.lastObservation = observation
		c.lastObservationID = observation.Sequence
	}
	if c.lastObservation.Sequence == 0 || !cut.At.After(c.lastEvaluatorAt) {
		return "", errProductionController
	}

	lifecycle := c.lifecycle.Snapshot()
	correctness, latency, signals, err := projectWorkerVerdictEvidence(c.cfg, cut.Snapshots, lifecycle)
	if err != nil {
		return "", errProductionController
	}
	correctness.ActivationRejections = c.lastObservation.ActivationRejections
	signals = append(signals, productionLifecycleSignals(lifecycle)...)
	signals = append(signals, c.lifecycleSignalsLocked()...)
	resources := []NodeResourceSample(nil)
	if c.lastObservationID == observation.Sequence && observation.At.Equal(cut.At) {
		resources = observation.Resources
		signals = append(signals, observation.Signals...)
	}
	if cut.StopRequested {
		signals = append(signals, VerdictSignal{Outcome: VerdictOperatorStop, Cause: VerdictCauseOperatorRequested})
	}
	if err := c.evaluator.Observe(VerdictObservation{
		At: cut.At, Correctness: &correctness, Latency: &latency, Resources: resources, Signals: signals,
	}); err != nil && !c.evaluator.Snapshot().Terminal {
		return "", errProductionController
	}
	c.lastEvaluatorAt = cut.At

	if cut.Kind == CoordinatorCutQualification || cut.Kind == CoordinatorCutTerminal {
		if err := c.refreshDatasetLocked(ctx); err != nil {
			return "", err
		}
	}

	verdict := c.evaluator.Snapshot()
	if cut.Kind == CoordinatorCutTerminal && !verdict.Terminal {
		switch {
		case c.cfg.Mode == ModeCapacity && cut.Capacity.Terminal && cut.Capacity.Outcome == CapacityPassed:
			verdict.Outcome, verdict.Cause, verdict.Terminal = VerdictPass, VerdictCauseCompleted, true
		case c.cfg.Mode == ModeCapacity && cut.Capacity.Terminal && cut.Capacity.Outcome == CapacityProductFailure:
			verdict.Outcome, verdict.Cause, verdict.Terminal = VerdictProductFailure, VerdictCauseWorkerProduct, true
		case c.cfg.Mode == ModeCapacity && cut.Capacity.Terminal:
			verdict.Outcome, verdict.Cause, verdict.Terminal = VerdictHarnessInvalid, VerdictCauseInvalidObservation, true
		default:
			_ = c.evaluator.Finalize(cut.At)
			verdict = c.evaluator.Snapshot()
		}
	}

	evidence := c.checkpointEvidenceLocked(lifecycle, verdict, cut.Capacity)
	if cut.Kind == CoordinatorCutQualification && !verdict.Terminal {
		if _, err := c.recorder.CaptureAndWrite(cut.At, cut.Snapshots, evidence, c.paths("qualification")); err != nil {
			return "", errProductionController
		}
		return "", nil
	}
	if verdict.Terminal {
		c.prepared, c.frozen = evidence, verdict
		return coordinatorOutcomeForVerdict(verdict), nil
	}
	return "", nil
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
	c.stopLifecycleLocked()
	if err := c.refreshDatasetLocked(ctx); err != nil {
		return err
	}
	if err := c.meta.Checkpoint(ctx, cut.FinalSnapshots, c.assignment, c.accounting.Snapshot().Checkpoints > 0); err != nil {
		return errProductionController
	}
	lifecycle := c.lifecycle.Snapshot()
	if len(productionLifecycleSignals(lifecycle)) > 0 || len(c.lifecycleSignalsLocked()) > 0 {
		return errProductionController
	}
	finalEvidence := c.checkpointEvidenceLocked(lifecycle, c.frozen, cut.Capacity)
	if _, err := c.recorder.CaptureAndWrite(cut.At, cut.FinalSnapshots, finalEvidence, c.paths("final")); err != nil {
		return errProductionController
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
		Verdict: verdict, Capacity: capacity.ReportEvidence(),
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
	case VerdictPass:
		return CoordinatorCompleted
	case VerdictProductFailure:
		return CoordinatorProductFailure
	case VerdictHarnessInvalid:
		return CoordinatorHarnessInvalid
	case VerdictInfrastructureFailure:
		return CoordinatorInfrastructureFailure
	case VerdictOperatorStop:
		return CoordinatorStopped
	default:
		return CoordinatorHarnessInvalid
	}
}
