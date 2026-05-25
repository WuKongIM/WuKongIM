package channelretention

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultCursorName      = "committed"
	defaultMaxTrimMessages = 1024
	defaultScanInterval    = time.Minute
)

// ErrChannelUnavailable marks a channel that cannot currently provide a
// retention view. The worker skips it so one cold channel does not block a pass.
var ErrChannelUnavailable = errors.New("channel retention: channel unavailable")

// Channel identifies one channel eligible for retention planning.
type Channel struct {
	// Key is the channel log key used by the channel store and runtime.
	Key channel.ChannelKey
	// ID is the protocol-facing channel identifier used for metadata fencing.
	ID channel.ChannelID
}

// ScanResult describes the contiguous message prefix expired by TTL.
type ScanResult struct {
	// FromSeq is the normalized sequence where the scan started.
	FromSeq uint64
	// ThroughSeq is the highest expired message sequence in the contiguous prefix.
	ThroughSeq uint64
	// Count is the number of expired rows included in the prefix.
	Count int
}

// ChannelLister lists channels that the retention worker should scan.
type ChannelLister interface {
	ListRetentionChannels(ctx context.Context) ([]Channel, error)
}

// Runtime exposes channel runtime state and immediate local retention apply.
type Runtime interface {
	RetentionView(ctx context.Context, key channel.ChannelKey) (channel.RetentionView, error)
	ApplyRetentionBoundary(ctx context.Context, key channel.ChannelKey, throughSeq uint64) error
}

// StoreProvider maps a channel to its local durable channel store.
type StoreProvider interface {
	StoreForChannel(ctx context.Context, ch Channel) (Store, error)
}

// Store exposes retention scan and durable replay cursor confirmation primitives.
type Store interface {
	ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (ScanResult, error)
	ConfirmCommittedDispatchCursorDurable(name string, minSeq uint64) (uint64, error)
}

// MetadataStore commits authoritative retention boundary advances through cluster metadata.
type MetadataStore interface {
	AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error
}

// Config controls one retention worker instance.
type Config struct {
	// Channels lists channels eligible for retention planning.
	Channels ChannelLister
	// Stores maps each channel to its local channel store.
	Stores StoreProvider
	// Runtime reads current channel runtime safety state and applies committed boundaries locally.
	Runtime Runtime
	// Metadata commits authoritative retention boundary advances.
	Metadata MetadataStore
	// LocalNodeID limits planning to channels currently led by this node.
	LocalNodeID channel.NodeID
	// TTL disables retention when it is zero or negative.
	TTL time.Duration
	// ScanInterval controls the background scan cadence.
	ScanInterval time.Duration
	// MaxTrimMessages bounds the number of messages considered per channel scan.
	MaxTrimMessages int
	// CursorName selects the committed replay cursor confirmed before metadata advances.
	CursorName string
	// Now returns the current time; time.Now is used when nil.
	Now func() time.Time
	// Logger records background pass failures and skipped channel diagnostics.
	Logger wklog.Logger
}

// Worker scans leader channels and advances safe cluster-authoritative retention bounds.
type Worker struct {
	cfg Config

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// BlockedReason explains why a boundary calculation cannot advance retention.
type BlockedReason uint8

const (
	BlockedNone BlockedReason = iota
	BlockedNoExpiredPrefix
	BlockedExpiredPrefix
	BlockedReplayCursor
	BlockedMinISRMatchOffset
	BlockedHW
	BlockedCheckpointHW
)

type boundaryDecision struct {
	AdvanceThroughSeq uint64
	ShouldAdvance     bool
	BlockedReason     BlockedReason
}

func safeRetentionBoundary(expiredThrough, confirmedReplayCursor uint64, view channel.RetentionView) boundaryDecision {
	return calculateRetentionBoundary(expiredThrough, view.RetentionThroughSeq, []boundaryGate{
		{seq: confirmedReplayCursor, reason: BlockedReplayCursor},
		{seq: view.MinISRMatchOffset, reason: BlockedMinISRMatchOffset},
		{seq: view.HW, reason: BlockedHW},
		{seq: view.CheckpointHW, reason: BlockedCheckpointHW},
	})
}

func provisionalRetentionBoundary(expiredThrough uint64, view channel.RetentionView) boundaryDecision {
	return calculateRetentionBoundary(expiredThrough, view.RetentionThroughSeq, []boundaryGate{
		{seq: view.MinISRMatchOffset, reason: BlockedMinISRMatchOffset},
		{seq: view.HW, reason: BlockedHW},
		{seq: view.CheckpointHW, reason: BlockedCheckpointHW},
	})
}

type boundaryGate struct {
	seq    uint64
	reason BlockedReason
}

func calculateRetentionBoundary(expiredThrough, current uint64, gates []boundaryGate) boundaryDecision {
	if expiredThrough == 0 {
		return boundaryDecision{AdvanceThroughSeq: current, BlockedReason: BlockedNoExpiredPrefix}
	}
	candidate := expiredThrough
	blockedBy := BlockedExpiredPrefix
	for _, gate := range gates {
		if gate.seq < candidate {
			candidate = gate.seq
			blockedBy = gate.reason
		}
	}
	if candidate <= current {
		return boundaryDecision{AdvanceThroughSeq: current, BlockedReason: blockedBy}
	}
	return boundaryDecision{AdvanceThroughSeq: candidate, ShouldAdvance: true, BlockedReason: BlockedNone}
}

// NewWorker creates a retention worker from focused ports.
func NewWorker(cfg Config) *Worker {
	return &Worker{cfg: cfg}
}

// Start launches the background retention scan loop.
func (w *Worker) Start(ctx context.Context) error {
	if w == nil {
		return errors.New("channel retention worker is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done
	interval := w.scanInterval()
	w.mu.Unlock()

	go func() {
		defer close(done)
		w.runBackground(runCtx, interval)
	}()
	return nil
}

// Stop cancels the background retention scan loop and waits for it to exit.
func (w *Worker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	if cancel == nil {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	cancel()
	select {
	case <-done:
		w.mu.Lock()
		if w.done == done {
			w.cancel = nil
			w.done = nil
		}
		w.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) runBackground(ctx context.Context, interval time.Duration) {
	if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
		w.logger().Warn("channel retention pass failed",
			wklog.Event("channel_retention.pass.failed"),
			wklog.Error(err),
		)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
				w.logger().Warn("channel retention pass failed",
					wklog.Event("channel_retention.pass.failed"),
					wklog.Error(err),
				)
			}
		}
	}
}

// RunOnce scans configured channels once and returns the first unsafe-path error.
func (w *Worker) RunOnce(ctx context.Context) error {
	if w == nil {
		return errors.New("channel retention worker is nil")
	}
	if w.cfg.TTL <= 0 {
		return nil
	}
	if err := w.validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	channels, err := w.cfg.Channels.ListRetentionChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.runChannel(ctx, ch); err != nil {
			if errors.Is(err, ErrChannelUnavailable) {
				w.logger().Warn("channel retention channel skipped",
					wklog.Event("channel_retention.channel.skipped"),
					wklog.ChannelID(ch.ID.ID),
					wklog.ChannelType(int64(ch.ID.Type)),
					wklog.String("channelKey", string(ch.Key)),
					wklog.Reason("unavailable"),
					wklog.Error(err),
				)
				continue
			}
			return err
		}
	}
	return nil
}

func (w *Worker) runChannel(ctx context.Context, ch Channel) error {
	now := w.now()
	view, err := w.cfg.Runtime.RetentionView(ctx, ch.Key)
	if err != nil {
		return err
	}
	if !w.channelEligible(view, now) {
		return nil
	}
	if w.needsExistingBoundaryApply(view) {
		if err := w.cfg.Runtime.ApplyRetentionBoundary(ctx, ch.Key, view.RetentionThroughSeq); err != nil {
			return err
		}
	}

	store, err := w.cfg.Stores.StoreForChannel(ctx, ch)
	if err != nil {
		return err
	}
	prefix, err := store.ScanExpiredMessagePrefix(retentionScanFromSeq(view), now.Add(-w.cfg.TTL), w.maxTrimMessages())
	if err != nil {
		return err
	}
	if !provisionalRetentionBoundary(prefix.ThroughSeq, view).ShouldAdvance {
		return nil
	}

	confirmedCursor, err := store.ConfirmCommittedDispatchCursorDurable(w.cursorName(), nextSeq(view.RetentionThroughSeq))
	if err != nil {
		return err
	}
	latest, err := w.cfg.Runtime.RetentionView(ctx, ch.Key)
	if err != nil {
		return err
	}
	now = w.now()
	if !w.channelEligible(latest, now) {
		return nil
	}
	decision := safeRetentionBoundary(prefix.ThroughSeq, confirmedCursor, latest)
	if !decision.ShouldAdvance {
		return nil
	}

	if err := w.cfg.Metadata.AdvanceChannelRetentionThroughSeq(ctx, retentionAdvanceRequest(ch, latest, decision.AdvanceThroughSeq, now)); err != nil {
		return err
	}
	return w.cfg.Runtime.ApplyRetentionBoundary(ctx, ch.Key, decision.AdvanceThroughSeq)
}

func (w *Worker) validate() error {
	if w.cfg.Channels == nil || w.cfg.Stores == nil || w.cfg.Runtime == nil || w.cfg.Metadata == nil {
		return errors.New("channel retention worker requires all ports")
	}
	if w.cfg.LocalNodeID == 0 {
		return channel.ErrInvalidConfig
	}
	return nil
}

func (w *Worker) channelEligible(view channel.RetentionView, now time.Time) bool {
	if view.Leader != w.cfg.LocalNodeID {
		return false
	}
	if !view.CommitReady {
		return false
	}
	return view.LeaseUntil.IsZero() || now.Before(view.LeaseUntil)
}

func (w *Worker) needsExistingBoundaryApply(view channel.RetentionView) bool {
	if view.RetentionThroughSeq == 0 {
		return false
	}
	return view.LocalRetentionThroughSeq < view.RetentionThroughSeq ||
		view.PhysicalRetentionThroughSeq < view.RetentionThroughSeq
}

func retentionAdvanceRequest(ch Channel, view channel.RetentionView, throughSeq uint64, now time.Time) metadb.ChannelRetentionAdvance {
	return metadb.ChannelRetentionAdvance{
		ChannelID:            ch.ID.ID,
		ChannelType:          int64(ch.ID.Type),
		ExpectedChannelEpoch: view.Epoch,
		ExpectedLeaderEpoch:  view.LeaderEpoch,
		ExpectedLeader:       uint64(view.Leader),
		ExpectedLeaseUntilMS: retentionLeaseUntilMS(view.LeaseUntil),
		RetentionThroughSeq:  throughSeq,
		RetentionUpdatedAtMS: now.UnixMilli(),
	}
}

func retentionLeaseUntilMS(leaseUntil time.Time) int64 {
	if leaseUntil.IsZero() {
		return 0
	}
	return leaseUntil.UnixMilli()
}

func retentionScanFromSeq(view channel.RetentionView) uint64 {
	if view.MinAvailableSeq > 0 {
		return view.MinAvailableSeq
	}
	return nextSeq(view.RetentionThroughSeq)
}

func nextSeq(seq uint64) uint64 {
	if seq == ^uint64(0) {
		return seq
	}
	return seq + 1
}

func (w *Worker) scanInterval() time.Duration {
	if w.cfg.ScanInterval > 0 {
		return w.cfg.ScanInterval
	}
	return defaultScanInterval
}

func (w *Worker) maxTrimMessages() int {
	if w.cfg.MaxTrimMessages > 0 {
		return w.cfg.MaxTrimMessages
	}
	return defaultMaxTrimMessages
}

func (w *Worker) cursorName() string {
	if w.cfg.CursorName != "" {
		return w.cfg.CursorName
	}
	return defaultCursorName
}

func (w *Worker) now() time.Time {
	if w.cfg.Now != nil {
		return w.cfg.Now()
	}
	return time.Now()
}

func (w *Worker) logger() wklog.Logger {
	if w != nil && w.cfg.Logger != nil {
		return w.cfg.Logger
	}
	return wklog.NewNop()
}
