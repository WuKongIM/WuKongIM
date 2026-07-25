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
	// DefaultCaptureMemoryBudgetBytes bounds pre-store page, accumulator, and encoding memory.
	DefaultCaptureMemoryBudgetBytes int64 = 3*MaxCaptureSegmentBytes + 16<<20

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

// CaptureObserver receives low-cardinality lease ownership evidence.
type CaptureObserver interface {
	// ObserveBackupCaptureLeaseTakeover increments after a durable takeover.
	ObserveBackupCaptureLeaseTakeover()
	// ObserveBackupCaptureLeaseFenced increments after a stale worker rejection.
	ObserveBackupCaptureLeaseFenced()
	// SetBackupCaptureOwnedSlots publishes current locally owned Slot count.
	SetBackupCaptureOwnedSlots(int)
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
	// KMSKeyID identifies the key-encryption key used for new segments.
	KMSKeyID string
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
	options.KMSKeyID = strings.TrimSpace(options.KMSKeyID)
	options.InitialGeneration = strings.TrimSpace(options.InitialGeneration)
	if !validContinuousIdentity(options.RepositoryID, 128) ||
		!validContinuousIdentity(options.SourceClusterID, 128) ||
		!validContinuousIdentity(options.SourceGeneration, 128) ||
		!validContinuousIdentity(options.InitialGeneration, 128) ||
		options.KMSKeyID == "" || len(options.KMSKeyID) > 512 || options.HashSlotCount == 0 ||
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
	snapshot, err := e.options.Frontiers.AcquireLease(
		ctx,
		hashSlot,
		e.options.InitialGeneration,
		e.options.Clock.Now().UnixMilli(),
	)
	if err != nil {
		state := backupcontract.CaptureStateFailed
		category := "frontier_load"
		if errors.Is(err, ErrCaptureLeaseFenced) || errors.Is(err, ErrCaptureNotLeader) {
			state = backupcontract.CaptureStateFenced
			category = "capture_fenced"
			if e.options.Observer != nil {
				e.options.Observer.ObserveBackupCaptureLeaseFenced()
			}
		}
		e.recordStatus(hashSlot, state, backupcontract.SlotFrontier{}, SourceWatermarks{}, category)
		return backupcontract.SlotFrontier{}, err
	}
	if snapshot.LeaseTakenOver {
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
	e.recordStatus(hashSlot, backupcontract.CaptureStateReconciling, current, SourceWatermarks{}, "")
	watermarks, err := e.options.Source.HighWatermarks(ctx, hashSlot, current)
	if err != nil {
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
		e.recordStreamCaptureError(hashSlot, current, watermarks, "metadata_capture", err)
		return backupcontract.SlotFrontier{}, err
	}
	next.Messages, err = e.captureStream(ctx, hashSlot, next.Lease, backupartifact.SegmentStreamMessages, next.Messages, watermarks.Messages)
	if err != nil {
		e.recordStreamCaptureError(hashSlot, current, watermarks, "message_capture", err)
		return backupcontract.SlotFrontier{}, err
	}
	next.WatermarkAtUnixMillis = olderPositiveTime(next.Metadata.WatermarkAtUnixMillis, next.Messages.WatermarkAtUnixMillis)
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
	if err := e.options.Frontiers.CompareAndSwap(ctx, current.Revision, current.Lease, next); err != nil {
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
