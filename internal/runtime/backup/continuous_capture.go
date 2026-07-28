package backup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"golang.org/x/sync/semaphore"
)

const (
	// DefaultTargetSegmentBytes is the rolling target for continuous capture.
	DefaultTargetSegmentBytes int64 = 64 << 20
	// MaxCaptureSegmentBytes is the hard plaintext limit for one captured segment.
	MaxCaptureSegmentBytes int64 = 256 << 20
	// DefaultMaxSegmentOpenDuration bounds sparse-stream capture latency.
	DefaultMaxSegmentOpenDuration = 30 * time.Second
	// DefaultCapturePageRecords bounds one authoritative source page.
	DefaultCapturePageRecords = 1024
	// DefaultCapturePagesPerReconcile yields hot Slots after bounded source work.
	DefaultCapturePagesPerReconcile = 8
	// DefaultCaptureReconcileInterval provides correctness when wake hints are lost.
	DefaultCaptureReconcileInterval = 30 * time.Second
	// DefaultCaptureWorkerCount bounds independent Slot reconciliation concurrency.
	DefaultCaptureWorkerCount = 4
	// DefaultCaptureMemoryBudgetBytes admits one maximum materialized Channel
	// index decode plus its merge/index working set, while keeping concurrent
	// large Slot work serialized by the shared node budget.
	DefaultCaptureMemoryBudgetBytes int64 = 6*MaxCaptureSegmentBytes + 16<<20

	maxCaptureWorkerCount = 64

	// These conservative charges cover slice descriptors/capacity, map buckets,
	// string headers, and allocator metadata in addition to encoded bytes.
	captureRecordHeapOverheadBytes int64 = 64
	captureCursorHeapOverheadBytes int64 = 192
)

var (
	// ErrFrontierConflict reports an atomic SlotFrontier compare-and-swap race.
	ErrFrontierConflict = errors.New("backup runtime: slot frontier conflict")
	// ErrSourceRegressed reports an authoritative source watermark below the durable frontier.
	ErrSourceRegressed = errors.New("backup runtime: capture source regressed")
	// ErrCaptureMemoryPressure reports a retryable node plaintext-budget shortage.
	ErrCaptureMemoryPressure = errors.New("backup runtime: capture memory pressure")
	// ErrCaptureLeaseFenced reports a stale or mismatched durable capture lease.
	ErrCaptureLeaseFenced = errors.New("backup runtime: capture lease fenced")
	// ErrCaptureNotLeader reports that the local worker is not the current Slot Leader.
	ErrCaptureNotLeader = errors.New("backup runtime: capture worker is not Slot leader")
	// ErrCaptureSourceCompacted reports that an uncommitted source interval was removed.
	ErrCaptureSourceCompacted = errors.New("backup runtime: capture source compacted")
	// ErrCompactionBudget reports that another Slot is using the bounded node
	// compaction I/O, network, or concurrency budget.
	ErrCompactionBudget = errors.New("backup runtime: compaction budget unavailable")
	// ErrContinuousDoctorUnhealthy reports that dependency qualification has
	// not admitted repository/key-authority work for the continuous runtime.
	ErrContinuousDoctorUnhealthy = errors.New("backup runtime: continuous doctor is not healthy")
)

// RollingPolicy bounds one continuous-capture segment and source page.
type RollingPolicy struct {
	// TargetSegmentBytes seals ordinary busy segments near this plaintext size.
	TargetSegmentBytes int64
	// MaxSegmentBytes rejects any segment above this hard plaintext size.
	MaxSegmentBytes int64
	// MaxOpenDuration seals sparse non-empty segments after this duration.
	MaxOpenDuration time.Duration
	// PageRecords bounds one authoritative reconciliation page.
	PageRecords int
	// PagesPerReconcile bounds pages consumed before yielding the Slot worker.
	PagesPerReconcile int
}

// DefaultRollingPolicy returns the production starting bounds for one Slot stream.
func DefaultRollingPolicy() RollingPolicy {
	return RollingPolicy{
		TargetSegmentBytes: DefaultTargetSegmentBytes,
		MaxSegmentBytes:    MaxCaptureSegmentBytes,
		MaxOpenDuration:    DefaultMaxSegmentOpenDuration,
		PageRecords:        DefaultCapturePageRecords,
		PagesPerReconcile:  DefaultCapturePagesPerReconcile,
	}
}

// SourceWatermark is one authoritative committed stream boundary.
type SourceWatermark struct {
	// Position is a monotonic source-specific committed position.
	Position uint64
	// CommittedAtUnixMillis is the UTC time represented by Position.
	CommittedAtUnixMillis int64
	// CutCursor pins source-specific evidence for Position. Metadata sources
	// leave it empty; message sources use it to preserve one exact Channel cut.
	CutCursor string
	// ReconcilePending reports that bounded source discovery has more pages to
	// examine even when no known position currently exceeds the frontier.
	ReconcilePending bool
	// DiscoveryPending reports that another bounded discovery page can be read
	// immediately without waiting for a source commit or rolling deadline.
	DiscoveryPending bool
}

// SourceWatermarks contains the independent metadata and message boundaries.
type SourceWatermarks struct {
	Metadata SourceWatermark
	Messages SourceWatermark
}

// SourcePageRequest selects one bounded authoritative reconciliation page.
type SourcePageRequest struct {
	// HashSlot and Stream identify the logical source.
	HashSlot uint16
	Stream   backupartifact.SegmentStream
	// Generation and CursorHead locate the immutable cursor evidence to resume.
	Generation string
	// CursorSequence identifies CursorHead's exact message-stream sequence.
	CursorSequence uint64
	// CursorSourceCursor is the durable cursor authenticated by CursorHead.
	CursorSourceCursor string
	// CursorHead is the message cursor sidecar tip; metadata requests leave it nil.
	CursorHead *backupartifact.SegmentReference
	// BaselineCursorHead is the complete materialized Channel boundary index.
	BaselineCursorHead *backupartifact.SegmentReference
	// AfterCursor is the last durably reconciled opaque source cursor.
	AfterCursor string
	// ThroughPosition pins this reconciliation to one observed high watermark.
	ThroughPosition uint64
	// ThroughCursor carries the source-specific evidence for ThroughPosition.
	ThroughCursor string
	// MaxBytes is the desired aggregate encoded page size.
	MaxBytes int64
	// MaxRecordBytes allows one oversized record to exceed MaxBytes without
	// exceeding the configured segment hard limit.
	MaxRecordBytes int64
	// MaxRecords caps source records examined by one request.
	MaxRecords int
}

// SourcePage is one ordered page from an authoritative committed source.
type SourcePage struct {
	// Records are portable committed source records in source order. Ownership
	// transfers to the capture runtime when ReadPage returns successfully.
	Records [][]byte
	// NextCursor advances the opaque paged reconciliation scan.
	NextCursor string
	// NextPosition is the greatest source position fully represented by this page.
	NextPosition uint64
	// Done reports that every source row through ThroughPosition was examined.
	Done bool
	// MessageCursors are Channel cursor updates encoded into message segments.
	MessageCursors []backupartifact.ChannelBoundary
}

// ContinuousSource pages committed metadata and message logs independently.
type ContinuousSource interface {
	// HighWatermarks observes both current committed source boundaries relative
	// to the exact durable frontier used for cursor reconciliation.
	HighWatermarks(context.Context, uint16, backupcontract.SlotFrontier) (SourceWatermarks, error)
	// ReadPage scans authoritative data through a pinned boundary.
	ReadPage(context.Context, SourcePageRequest) (SourcePage, error)
}

// SourcePageAcknowledger receives a best-effort in-memory notification only
// after a validated page has entered the rolling accumulator. Durable
// correctness must not depend on this optional acceleration seam.
type SourcePageAcknowledger interface {
	// AcknowledgeSourcePage releases source-local scan state for one completed cut.
	AcknowledgeSourcePage(uint16, backupartifact.SegmentStream, string)
}

// SourceStateInvalidator discards disposable source acceleration after a
// durable SlotFrontier compare-and-swap fails.
type SourceStateInvalidator interface {
	// InvalidateSourceState restarts observation from this Hash Slot's durable frontier.
	InvalidateSourceState(uint16)
}

// SegmentCommitter makes one content-addressed segment durable in both repositories.
type SegmentCommitter interface {
	Commit(context.Context, backupartifact.SegmentDescriptor, []byte) (backupartifact.SegmentReference, error)
}

// ContinuousSegmentLoader authenticates and opens one committed cursor sidecar.
type ContinuousSegmentLoader interface {
	Load(context.Context, backupartifact.SegmentReference) ([]byte, error)
}

// SlotCaptureAuthority is the current distributed ownership identity for one Hash Slot.
type SlotCaptureAuthority struct {
	// SlotID identifies the logical Slot Raft Group that owns the Hash Slot.
	SlotID uint32
	// LeaderTerm is the Slot Raft term observed with HolderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane configuration epoch for SlotID.
	ConfigEpoch uint64
	// HolderNodeID is the current Slot Leader and local capture worker node.
	HolderNodeID uint64
}

// SlotCaptureAuthoritySource proves that this process is the current Slot Leader.
type SlotCaptureAuthoritySource interface {
	CurrentCaptureAuthority(context.Context, uint16) (SlotCaptureAuthority, error)
}

// FrontierSnapshot is one durable SlotFrontier load result.
type FrontierSnapshot struct {
	Frontier backupcontract.SlotFrontier
	Found    bool
	// LeaseTakenOver reports that this acquisition durably advanced the lease sequence.
	LeaseTakenOver bool
}

// SlotFrontierStore atomically persists one compact frontier per Hash Slot.
type SlotFrontierStore interface {
	Load(context.Context, uint16) (FrontierSnapshot, error)
	// AcquireLease returns the durable frontier fenced to the current local Slot
	// Leader, creating or taking over the lease when authority changed.
	AcquireLease(context.Context, uint16, string, int64) (FrontierSnapshot, error)
	// CompareAndSwap advances a frontier only while its exact lease and revision
	// still match both durable coordination state and current Slot authority.
	CompareAndSwap(context.Context, uint64, backupcontract.SlotCaptureLease, backupcontract.SlotFrontier) error
}

// SlotIntegrityAuditGate exposes durable per-Slot repair isolation to capture workers.
type SlotIntegrityAuditGate interface {
	AuditSlotState(
		context.Context,
		uint16,
	) (backupcontract.SlotIntegrityAuditState, bool, error)
}

// SlotIntegrityAuditRefresher reloads a narrow projection after the atomic
// Controller frontier CAS observes a newer remote audit freeze.
type SlotIntegrityAuditRefresher interface {
	RefreshAuditSlotState(
		context.Context,
		uint16,
	) (backupcontract.SlotIntegrityAuditState, bool, error)
}

// SlotGenerationPromoter atomically replaces one healthy generation after its
// materialized baseline is durable in both repositories.
type SlotGenerationPromoter interface {
	PromoteGeneration(context.Context, uint64, backupcontract.SlotCaptureLease, backupcontract.SlotFrontier) error
}

// SourcePinPolicy bounds how long and how much retained source data backup may hold.
type SourcePinPolicy struct {
	// MaxAge is the hard lifetime of one Slot pin.
	MaxAge time.Duration
	// MaxNodeBytes is the aggregate node-local byte budget across Slot pins.
	MaxNodeBytes uint64
	// MaxDeltaBytes compacts a Generation after this many post-baseline
	// plaintext bytes. A smaller non-zero baseline size takes precedence.
	MaxDeltaBytes uint64
	// MaxSegments compacts a Generation after this many payload and cursor segments.
	MaxSegments uint64
	// MaxGenerationAge compacts a Generation after this durable age.
	MaxGenerationAge time.Duration
}

// SourcePinObservation is one bounded node-local pin accounting snapshot.
type SourcePinObservation struct {
	// Age is measured from the durable retained-floor start time.
	Age time.Duration
	// PinnedBytes is the source-log estimate retained for this Slot.
	PinnedBytes uint64
	// NodePinnedBytes is the aggregate estimate across locally held pins.
	NodePinnedBytes uint64
	// NodeBudgetVictim selects this Slot deterministically when the node budget is exceeded.
	NodeBudgetVictim bool
}

// SourcePinManager owns node-local source compaction holds.
type SourcePinManager interface {
	// Observe acquires or refreshes the exact leased Slot pin and returns accounting.
	Observe(context.Context, uint16, backupcontract.SlotCaptureLease, backupcontract.SlotFrontier) (SourcePinObservation, error)
	// Release removes only the exact Slot pin and returns the remaining node
	// aggregate. It must be idempotent across restart.
	Release(context.Context, uint16, backupcontract.SlotCaptureLease) (SourcePinObservation, error)
	// AdoptLease transfers one same-physical-Slot record to a new durable
	// authority without opening a compaction gap; a remap releases the old Slot.
	AdoptLease(context.Context, uint16, backupcontract.SlotCaptureLease) (SourcePinObservation, error)
	// ReleaseObsolete removes any node-local hold after this process learns it
	// is no longer capture authority for the Hash Slot.
	ReleaseObsolete(context.Context, uint16) (SourcePinObservation, error)
}

// MaterializedBaseline is a complete dual-repository root and its resume cut.
type MaterializedBaseline struct {
	// Generation must equal the durable pending rebase target.
	Generation string
	// Reference authenticates the materialized partition and complete message cursor.
	Reference backupcontract.SlotBaselineReference
	// Metadata and Messages are empty-stream resume positions after the baseline.
	Metadata backupcontract.StreamFrontier
	Messages backupcontract.StreamFrontier
	// WatermarkAtUnixMillis is the older represented source time.
	WatermarkAtUnixMillis int64
}

// MaterializedBaselineCapturer creates a retryable full Slot root without
// publishing it into the active frontier.
type MaterializedBaselineCapturer interface {
	CaptureBaseline(
		context.Context,
		uint16,
		string,
		uint64,
		backupcontract.SlotCaptureLease,
		func(context.Context, uint64) error,
	) (MaterializedBaseline, error)
}

// GenerationPromotionValidator is the asynchronous-audit seam that must
// attest a dual-repository replacement before it becomes authoritative.
type GenerationPromotionValidator interface {
	ValidateGenerationReplacement(
		context.Context,
		backupcontract.SlotFrontier,
		MaterializedBaseline,
	) error
}

// GenerationCompactionCostPlanner returns conservative upper bounds for the
// actual source reads and dual-repository writes of one replacement.
type GenerationCompactionCostPlanner interface {
	PlanGenerationCompaction(
		context.Context,
		backupcontract.SlotFrontier,
	) (GenerationCompactionCost, error)
}

// RebaseObserver receives low-cardinality pin and rebase evidence.
type RebaseObserver interface {
	SetBackupSourcePin(hashSlot uint16, age time.Duration, slotBytes, nodeBytes uint64)
	ObserveBackupSlotRebase(hashSlot uint16, reason string, duration time.Duration, failureCategory string)
}

// RebaseOptions configures per-Slot pin release and materialized replacement.
type RebaseOptions struct {
	// Policy contains explicit hard limits.
	Policy SourcePinPolicy
	// Pins owns source-log holds; Baselines creates dual-repository roots.
	Pins      SourcePinManager
	Baselines MaterializedBaselineCapturer
	// Validator requires durable audit evidence before promotion.
	Validator GenerationPromotionValidator
	// CostPlanner sizes admission from the current source snapshot rather than
	// inferring a full materialization from historical delta bytes.
	CostPlanner GenerationCompactionCostPlanner
	// Budget bounds concurrent materialization and its estimated node I/O and
	// dual-repository network working set.
	Budget GenerationCompactionBudget
}

// CaptureObserver receives low-cardinality lease ownership evidence.
type CaptureObserver interface {
	// ObserveBackupCaptureLeaseTakeover increments after a durable takeover.
	ObserveBackupCaptureLeaseTakeover()
	// ObserveBackupCaptureLeaseFenced increments after a stale worker rejection.
	ObserveBackupCaptureLeaseFenced()
	// SetBackupCaptureOwnedSlots publishes current locally owned Slot count.
	SetBackupCaptureOwnedSlots(int)
}

// CaptureProgressObserver receives bounded per-Slot RPO and maintenance debt.
type CaptureProgressObserver interface {
	// SetBackupCaptureSlot records source lag and the optional durable frontier age.
	SetBackupCaptureSlot(uint16, uint64, uint64, *time.Duration)
	// SetBackupCompactionDebt records locally observed pending replacements.
	SetBackupCompactionDebt(int)
}

// CaptureClock supplies deterministic UTC time to rolling and status logic.
type CaptureClock interface {
	Now() time.Time
}

// CaptureMemoryBudget gates source reads before they materialize plaintext.
type CaptureMemoryBudget interface {
	// TryAcquire reserves bytes without blocking a Slot worker.
	TryAcquire(int64) bool
	// Release returns a prior exact reservation.
	Release(int64)
}

type wallCaptureClock struct{}

func (wallCaptureClock) Now() time.Time { return time.Now() }

type weightedCaptureMemoryBudget struct {
	semaphore *semaphore.Weighted
}

// NewCaptureMemoryBudget creates one shared non-blocking capture-memory gate.
func NewCaptureMemoryBudget(maxBytes int64) (CaptureMemoryBudget, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("%w: capture memory budget must be positive", ErrInvalidCapture)
	}
	return &weightedCaptureMemoryBudget{semaphore: semaphore.NewWeighted(maxBytes)}, nil
}

func (b *weightedCaptureMemoryBudget) TryAcquire(bytes int64) bool {
	return b.semaphore.TryAcquire(bytes)
}

func (b *weightedCaptureMemoryBudget) Release(bytes int64) {
	b.semaphore.Release(bytes)
}

// CaptureEngineOptions configures deterministic continuous Slot reconciliation.
type CaptureEngineOptions struct {
	// RepositoryID is the stable logical identity shared by both repositories.
	RepositoryID string
	// SourceClusterID and SourceGeneration fence the live source lineage.
	SourceClusterID  string
	SourceGeneration string
	// InitialGeneration initializes a Slot that has no durable frontier.
	InitialGeneration string
	// HashSlotCount bounds valid logical Slot identifiers.
	HashSlotCount uint16
	// Source, Frontiers, and Segments are the durable capture boundaries.
	Source    ContinuousSource
	Frontiers SlotFrontierStore
	Segments  SegmentCommitter
	// CursorLoader is inferred from Segments when the committer also supports
	// authenticated loads. It is required for periodic full cursor checkpoints.
	CursorLoader ContinuousSegmentLoader
	// Policy bounds segment materialization and reconciliation pages.
	Policy RollingPolicy
	// Clock may be replaced by deterministic tests.
	Clock CaptureClock
	// ReconcileInterval drives authoritative full-Slot polling even without hints.
	ReconcileInterval time.Duration
	// WorkerCount bounds independent Slot reconciliation concurrency.
	WorkerCount int
	// MemoryBudget gates pages before source adapters allocate plaintext.
	MemoryBudget CaptureMemoryBudget
	// Observer receives optional low-cardinality lease ownership metrics.
	Observer CaptureObserver
	// AuditGate pauses only Slots under durable integrity repair or recovery.
	AuditGate SlotIntegrityAuditGate
	// Rebase enables bounded source pins and independent Slot generation replacement.
	Rebase *RebaseOptions
}

// CaptureEngine continuously reconciles committed Slot logs into immutable segments.
type CaptureEngine struct {
	// options contains immutable validated capture dependencies and policy.
	options CaptureEngineOptions
	// statusMu protects detached public status projections.
	statusMu sync.RWMutex
	// status contains at most one bounded observation per configured Hash Slot.
	status map[uint16]backupcontract.SlotCaptureStatus
	// wake carries lossy in-memory Slot hints; polling remains authoritative.
	wake chan uint16
	// pendingChanged asks the scheduler to recompute the earliest sparse deadline.
	pendingChanged chan struct{}
	// slotLocks prevent overlapping reconciliation of one Slot inside this process.
	slotLocks []sync.Mutex
	// pendingMu protects sparse stream accumulators shared across Slot workers.
	pendingMu sync.Mutex
	// pending retains bounded non-durable plaintext until size or time rolls it.
	pending map[captureStreamKey]*segmentAccumulator
}

// NewCaptureEngine creates a continuous capture engine.
func NewCaptureEngine(options CaptureEngineOptions) (*CaptureEngine, error) {
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
	options.SourceClusterID = strings.TrimSpace(options.SourceClusterID)
	options.SourceGeneration = strings.TrimSpace(options.SourceGeneration)
	options.InitialGeneration = strings.TrimSpace(options.InitialGeneration)
	if !validContinuousIdentity(options.RepositoryID, 128) ||
		!validContinuousIdentity(options.SourceClusterID, 128) ||
		!validContinuousIdentity(options.SourceGeneration, 128) ||
		!validContinuousIdentity(options.InitialGeneration, 128) ||
		options.HashSlotCount == 0 ||
		options.Source == nil || options.Frontiers == nil || options.Segments == nil {
		return nil, fmt.Errorf("%w: continuous capture dependencies are incomplete", ErrInvalidCapture)
	}
	if options.Policy == (RollingPolicy{}) {
		options.Policy = DefaultRollingPolicy()
	}
	if options.Policy.PagesPerReconcile == 0 {
		options.Policy.PagesPerReconcile = DefaultCapturePagesPerReconcile
	}
	if err := validateRollingPolicy(options.Policy); err != nil {
		return nil, err
	}
	if options.ReconcileInterval == 0 {
		options.ReconcileInterval = DefaultCaptureReconcileInterval
	}
	if options.WorkerCount == 0 {
		options.WorkerCount = DefaultCaptureWorkerCount
	}
	if options.ReconcileInterval < 0 || options.WorkerCount < 0 || options.WorkerCount > maxCaptureWorkerCount {
		return nil, fmt.Errorf("%w: continuous capture scheduling policy is invalid", ErrInvalidCapture)
	}
	if options.WorkerCount > int(options.HashSlotCount) {
		options.WorkerCount = int(options.HashSlotCount)
	}
	if options.Clock == nil {
		options.Clock = wallCaptureClock{}
	}
	if options.MemoryBudget == nil {
		options.MemoryBudget = &weightedCaptureMemoryBudget{
			semaphore: semaphore.NewWeighted(DefaultCaptureMemoryBudgetBytes),
		}
	}
	if options.CursorLoader == nil {
		options.CursorLoader, _ = options.Segments.(ContinuousSegmentLoader)
	}
	if options.CursorLoader == nil {
		return nil, fmt.Errorf("%w: continuous cursor loader is required", ErrInvalidCapture)
	}
	if options.Rebase != nil {
		if options.Rebase.Policy.MaxDeltaBytes == 0 {
			options.Rebase.Policy.MaxDeltaBytes = DefaultGenerationMaxDeltaBytes
		}
		if options.Rebase.Policy.MaxSegments == 0 {
			options.Rebase.Policy.MaxSegments = DefaultGenerationMaxSegments
		}
		if options.Rebase.Policy.MaxGenerationAge == 0 {
			options.Rebase.Policy.MaxGenerationAge = DefaultGenerationMaxAge
		}
		if options.Rebase.Budget == nil {
			options.Rebase.Budget, _ = NewGenerationCompactionBudget(
				DefaultGenerationCompactionConcurrency,
				DefaultGenerationCompactionIOBytes,
				DefaultGenerationCompactionNetworkBytes,
			)
		}
		if options.Rebase.Pins == nil || options.Rebase.Baselines == nil ||
			options.Rebase.Validator == nil ||
			options.Rebase.CostPlanner == nil ||
			options.Rebase.Budget == nil ||
			options.Rebase.Policy.MaxAge <= 0 || options.Rebase.Policy.MaxNodeBytes == 0 ||
			options.Rebase.Policy.MaxDeltaBytes == 0 || options.Rebase.Policy.MaxSegments == 0 ||
			options.Rebase.Policy.MaxGenerationAge <= 0 {
			return nil, fmt.Errorf("%w: rebase dependencies or pin limits are invalid", ErrInvalidCapture)
		}
		if _, ok := options.Frontiers.(SlotGenerationPromoter); !ok {
			return nil, fmt.Errorf("%w: generation promoter is required", ErrInvalidCapture)
		}
	}
	return &CaptureEngine{
		options:        options,
		status:         make(map[uint16]backupcontract.SlotCaptureStatus),
		wake:           make(chan uint16, int(options.HashSlotCount)),
		pendingChanged: make(chan struct{}, 1),
		slotLocks:      make([]sync.Mutex, int(options.HashSlotCount)),
		pending:        make(map[captureStreamKey]*segmentAccumulator),
	}, nil
}

// ReconcileSlot captures every committed source record through one observed cut.
func (e *CaptureEngine) ReconcileSlot(ctx context.Context, hashSlot uint16) (backupcontract.SlotFrontier, error) {
	if e == nil || hashSlot >= e.options.HashSlotCount {
		return backupcontract.SlotFrontier{}, ErrInvalidCapture
	}
	e.slotLocks[hashSlot].Lock()
	defer e.slotLocks[hashSlot].Unlock()
	var auditRebase *backupcontract.SlotIntegrityAuditState
	if e.options.AuditGate != nil {
		audit, found, gateErr := e.options.AuditGate.AuditSlotState(ctx, hashSlot)
		if gateErr != nil {
			e.recordStatus(
				hashSlot, backupcontract.CaptureStateFailed,
				backupcontract.SlotFrontier{}, SourceWatermarks{}, "audit_gate",
			)
			return backupcontract.SlotFrontier{}, gateErr
		}
		if found && audit.Health != backupcontract.SlotAuditHealthy {
			if audit.Health == backupcontract.SlotAuditRebaseRequired &&
				e.options.Rebase != nil {
				auditCopy := audit
				auditRebase = &auditCopy
			} else {
				snapshot, loadErr := e.options.Frontiers.Load(ctx, hashSlot)
				if loadErr != nil {
					e.recordStatus(
						hashSlot, backupcontract.CaptureStateFailed,
						backupcontract.SlotFrontier{}, SourceWatermarks{}, "audit_gate",
					)
					return backupcontract.SlotFrontier{}, loadErr
				}
				frontier := backupcontract.CloneSlotFrontier(snapshot.Frontier)
				state := backupcontract.CaptureStateDegraded
				if audit.Health == backupcontract.SlotAuditFailed {
					state = backupcontract.CaptureStateFailed
				}
				e.recordStatus(
					hashSlot, state, frontier, SourceWatermarks{},
					"audit_"+string(audit.Health),
				)
				return frontier, ErrIntegrityAuditFrozen
			}
		}
	}
	snapshot, err := e.options.Frontiers.AcquireLease(
		ctx,
		hashSlot,
		e.options.InitialGeneration,
		e.options.Clock.Now().UnixMilli(),
	)
	if err != nil {
		state := backupcontract.CaptureStateFailed
		category := "frontier_load"
		if errors.Is(err, ErrIntegrityAuditFrozen) {
			state = backupcontract.CaptureStateDegraded
			category = "audit_gate"
			if refresher, ok := e.options.AuditGate.(SlotIntegrityAuditRefresher); ok {
				_, _, _ = refresher.RefreshAuditSlotState(ctx, hashSlot)
			}
		}
		if errors.Is(err, ErrCaptureLeaseFenced) || errors.Is(err, ErrCaptureNotLeader) {
			state = backupcontract.CaptureStateFenced
			category = "capture_fenced"
			if e.options.Rebase != nil {
				released, releaseErr := e.options.Rebase.Pins.ReleaseObsolete(ctx, hashSlot)
				if releaseErr != nil {
					err = errors.Join(err, releaseErr)
				} else if observer, ok := e.options.Observer.(RebaseObserver); ok {
					observer.SetBackupSourcePin(hashSlot, 0, 0, released.NodePinnedBytes)
				}
			}
			if e.options.Observer != nil {
				e.options.Observer.ObserveBackupCaptureLeaseFenced()
			}
		}
		e.recordStatus(hashSlot, state, backupcontract.SlotFrontier{}, SourceWatermarks{}, category)
		return backupcontract.SlotFrontier{}, err
	}
	if snapshot.LeaseTakenOver {
		if e.options.Rebase != nil {
			adopted, adoptErr := e.options.Rebase.Pins.AdoptLease(
				ctx, hashSlot, snapshot.Frontier.Lease,
			)
			if adoptErr != nil {
				e.recordStatus(hashSlot, backupcontract.CaptureStateFailed, snapshot.Frontier, SourceWatermarks{}, "pin_release")
				return backupcontract.SlotFrontier{}, adoptErr
			}
			if observer, ok := e.options.Observer.(RebaseObserver); ok {
				observer.SetBackupSourcePin(hashSlot, 0, adopted.PinnedBytes, adopted.NodePinnedBytes)
			}
		}
		if invalidator, ok := e.options.Source.(SourceStateInvalidator); ok {
			invalidator.InvalidateSourceState(hashSlot)
		}
		if e.options.Observer != nil {
			e.options.Observer.ObserveBackupCaptureLeaseTakeover()
		}
	}
	current, err := e.normalizeFrontier(hashSlot, snapshot)
	if err != nil {
		e.recordStatus(hashSlot, backupcontract.CaptureStateFailed, backupcontract.SlotFrontier{}, SourceWatermarks{}, "frontier_invalid")
		return backupcontract.SlotFrontier{}, err
	}
	if snapshot.LeaseTakenOver && current.Rebase != nil {
		reason := current.Rebase.Reason
		if current.SourceSlotID != current.Lease.SlotID {
			reason = backupcontract.RebaseReasonSourceRemapped
		}
		current, err = e.rotatePendingRebase(ctx, current, reason)
		if err != nil {
			return backupcontract.SlotFrontier{}, err
		}
	}
	if auditRebase != nil {
		if current.Generation != auditRebase.Generation {
			e.recordStatus(
				hashSlot, backupcontract.CaptureStateDegraded, current,
				SourceWatermarks{}, "audit_rebase_pending_confirmation",
			)
			return current, ErrIntegrityAuditFrozen
		}
		if current.Rebase == nil {
			current, err = e.beginRebase(
				ctx, current, backupcontract.RebaseReasonAuditCorruption,
			)
			if err != nil {
				return backupcontract.SlotFrontier{}, err
			}
		} else if current.Rebase.Reason != backupcontract.RebaseReasonAuditCorruption {
			current, err = e.rotatePendingRebase(
				ctx, current, backupcontract.RebaseReasonAuditCorruption,
			)
			if err != nil {
				return backupcontract.SlotFrontier{}, err
			}
		}
		next, rebaseErr := e.runRebase(ctx, current)
		return next, rebaseErr
	}
	if next, handled, err := e.reconcileRebase(ctx, current); handled {
		return next, err
	}
	flushed := backupcontract.CloneSlotFrontier(current)
	metadataFlushed := false
	messageFlushed := false
	flushed.Metadata, metadataFlushed, err = e.flushDueAccumulator(
		ctx, hashSlot, current.Lease, backupartifact.SegmentStreamMetadata, current.Metadata,
	)
	if err == nil {
		flushed.Messages, messageFlushed, err = e.flushDueAccumulator(
			ctx, hashSlot, current.Lease, backupartifact.SegmentStreamMessages, current.Messages,
		)
	}
	if err != nil {
		e.recordStreamCaptureError(
			hashSlot, current, SourceWatermarks{}, "pending_flush", err,
		)
		return backupcontract.SlotFrontier{}, err
	}
	if metadataFlushed || messageFlushed {
		flushed.WatermarkAtUnixMillis = olderPositiveTime(
			flushed.Metadata.WatermarkAtUnixMillis,
			flushed.Messages.WatermarkAtUnixMillis,
		)
		watermarks := SourceWatermarks{
			Metadata: SourceWatermark{
				Position:              flushed.Metadata.SourceHighWatermark,
				CommittedAtUnixMillis: flushed.Metadata.WatermarkAtUnixMillis,
				ReconcilePending:      true,
				DiscoveryPending:      true,
			},
			Messages: SourceWatermark{
				Position:              flushed.Messages.SourceHighWatermark,
				CommittedAtUnixMillis: flushed.Messages.WatermarkAtUnixMillis,
				ReconcilePending:      true,
				DiscoveryPending:      true,
			},
		}
		return e.commitPreparedFrontier(ctx, current, flushed, watermarks)
	}
	e.recordStatus(hashSlot, backupcontract.CaptureStateReconciling, current, SourceWatermarks{}, "")
	watermarks, err := e.options.Source.HighWatermarks(ctx, hashSlot, current)
	if err != nil {
		if errors.Is(err, ErrCaptureSourceCompacted) && e.options.Rebase != nil {
			next, beginErr := e.beginRebase(ctx, current, backupcontract.RebaseReasonSourceCompacted)
			if beginErr != nil {
				return backupcontract.SlotFrontier{}, beginErr
			}
			return e.runRebase(ctx, next)
		}
		e.recordStatus(hashSlot, backupcontract.CaptureStateFailed, current, SourceWatermarks{}, "source_watermark")
		return backupcontract.SlotFrontier{}, err
	}
	if err := validateSourceWatermarks(current, watermarks); err != nil {
		e.recordStatus(hashSlot, backupcontract.CaptureStateFailed, current, watermarks, "source_regressed")
		return backupcontract.SlotFrontier{}, err
	}
	e.recordStatus(hashSlot, backupcontract.CaptureStateCapturing, current, watermarks, "")

	next := backupcontract.CloneSlotFrontier(current)
	next.Metadata, err = e.captureStream(ctx, hashSlot, next.Lease, backupartifact.SegmentStreamMetadata, next.Metadata, watermarks.Metadata)
	if err != nil {
		if errors.Is(err, ErrCaptureSourceCompacted) && e.options.Rebase != nil {
			rebasing, beginErr := e.beginRebase(ctx, current, backupcontract.RebaseReasonSourceCompacted)
			if beginErr != nil {
				return backupcontract.SlotFrontier{}, beginErr
			}
			return e.runRebase(ctx, rebasing)
		}
		e.recordStreamCaptureError(hashSlot, current, watermarks, "metadata_capture", err)
		return backupcontract.SlotFrontier{}, err
	}
	next.Messages, err = e.captureStream(ctx, hashSlot, next.Lease, backupartifact.SegmentStreamMessages, next.Messages, watermarks.Messages)
	if err != nil {
		if errors.Is(err, ErrCaptureSourceCompacted) && e.options.Rebase != nil {
			rebasing, beginErr := e.beginRebase(ctx, current, backupcontract.RebaseReasonSourceCompacted)
			if beginErr != nil {
				return backupcontract.SlotFrontier{}, beginErr
			}
			return e.runRebase(ctx, rebasing)
		}
		e.recordStreamCaptureError(hashSlot, current, watermarks, "message_capture", err)
		return backupcontract.SlotFrontier{}, err
	}
	next.Metadata = initializeObservedEmptyStream(
		next.Metadata, watermarks.Metadata,
	)
	next.Messages = initializeObservedEmptyStream(
		next.Messages, watermarks.Messages,
	)
	next.WatermarkAtUnixMillis = olderPositiveTime(next.Metadata.WatermarkAtUnixMillis, next.Messages.WatermarkAtUnixMillis)
	return e.commitPreparedFrontier(ctx, current, next, watermarks)
}

func initializeObservedEmptyStream(
	frontier backupcontract.StreamFrontier,
	watermark SourceWatermark,
) backupcontract.StreamFrontier {
	if frontier.Sequence == 0 &&
		frontier.SourceHighWatermark == 0 &&
		watermark.Position == 0 &&
		frontier.WatermarkAtUnixMillis == 0 &&
		watermark.CommittedAtUnixMillis > 0 {
		frontier.WatermarkAtUnixMillis = watermark.CommittedAtUnixMillis
	}
	return frontier
}

func (e *CaptureEngine) commitPreparedFrontier(
	ctx context.Context,
	current backupcontract.SlotFrontier,
	next backupcontract.SlotFrontier,
	watermarks SourceWatermarks,
) (backupcontract.SlotFrontier, error) {
	hashSlot := current.HashSlot
	if slotFrontiersEqual(next, current) {
		e.recordStatus(hashSlot, captureStateForFrontier(next, watermarks), next, watermarks, "")
		e.continueDiscovery(hashSlot, watermarks)
		return next, nil
	}
	if current.Revision == math.MaxUint64 {
		e.recordStatus(hashSlot, backupcontract.CaptureStateFailed, current, watermarks, "frontier_revision")
		return backupcontract.SlotFrontier{}, fmt.Errorf("%w: frontier revision overflow", ErrInvalidCapture)
	}
	next.Revision = current.Revision + 1
	next.UpdatedAtUnixMillis = e.options.Clock.Now().UnixMilli()
	if next.Metadata.SourceHighWatermark > current.Metadata.SourceHighWatermark {
		next.SourcePinStartedAtUnixMillis = next.UpdatedAtUnixMillis
	}
	if err := e.options.Frontiers.CompareAndSwap(ctx, current.Revision, current.Lease, next); err != nil {
		if errors.Is(err, ErrIntegrityAuditFrozen) {
			if refresher, ok := e.options.AuditGate.(SlotIntegrityAuditRefresher); ok {
				_, _, _ = refresher.RefreshAuditSlotState(ctx, hashSlot)
			}
		}
		if invalidator, ok := e.options.Source.(SourceStateInvalidator); ok {
			invalidator.InvalidateSourceState(hashSlot)
		}
		state := backupcontract.CaptureStateFailed
		category := "frontier_commit"
		if errors.Is(err, ErrCaptureLeaseFenced) || errors.Is(err, ErrCaptureNotLeader) {
			state = backupcontract.CaptureStateFenced
			category = "capture_fenced"
			if e.options.Observer != nil {
				e.options.Observer.ObserveBackupCaptureLeaseFenced()
			}
		}
		e.recordStatus(hashSlot, state, current, watermarks, category)
		return backupcontract.SlotFrontier{}, err
	}
	e.recordStatus(hashSlot, captureStateForFrontier(next, watermarks), next, watermarks, "")
	e.continueDiscovery(hashSlot, watermarks)
	return backupcontract.CloneSlotFrontier(next), nil
}

func (e *CaptureEngine) continueDiscovery(hashSlot uint16, watermarks SourceWatermarks) {
	if watermarks.Metadata.DiscoveryPending || watermarks.Messages.DiscoveryPending {
		e.Wake(hashSlot)
	}
}

func captureStateForFrontier(frontier backupcontract.SlotFrontier, watermarks SourceWatermarks) backupcontract.CaptureState {
	if frontier.Metadata.SourceHighWatermark < watermarks.Metadata.Position ||
		frontier.Messages.SourceHighWatermark < watermarks.Messages.Position {
		return backupcontract.CaptureStateCapturing
	}
	if watermarks.Metadata.ReconcilePending || watermarks.Messages.ReconcilePending {
		return backupcontract.CaptureStateReconciling
	}
	return backupcontract.CaptureStateIdle
}
