package channelretention

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const defaultScanLimit = 1024

// Channel identifies one channel eligible for retention planning.
type Channel struct {
	// Key is the channel log key used by the channel runtime.
	Key channel.ChannelKey
	// ID is the protocol-facing channel identifier.
	ID channel.ChannelID
}

// ExpiredPrefix describes the contiguous message prefix expired by TTL.
type ExpiredPrefix struct {
	// ThroughSeq is the highest expired message sequence in the contiguous prefix.
	ThroughSeq uint64
}

// ExpiredPrefixScanner finds the TTL-expired contiguous prefix for a channel.
type ExpiredPrefixScanner interface {
	ScanExpiredPrefix(ctx context.Context, ch Channel, cutoff time.Time, limit int) (ExpiredPrefix, error)
}

// RuntimeView exposes the channel runtime safety gates used by retention.
type RuntimeView interface {
	RetentionView(ctx context.Context, ch Channel) (channel.RetentionView, error)
}

// ReplayCursor durably confirms committed replay progress before retention advances.
type ReplayCursor interface {
	ConfirmCommittedReplayCursor(ctx context.Context, ch Channel, minSeq uint64) (uint64, error)
}

// MetadataStore advances the authoritative retention boundary in metadata.
type MetadataStore interface {
	AdvanceRetention(ctx context.Context, ch Channel, boundary uint64, now time.Time) error
}

// ChannelLister lists channels that the retention worker should scan.
type ChannelLister interface {
	ListRetentionChannels(ctx context.Context) ([]Channel, error)
}

// Config controls one retention worker instance.
type Config struct {
	// Channels lists channels eligible for retention planning.
	Channels ChannelLister
	// Scanner locates contiguous TTL-expired message prefixes.
	Scanner ExpiredPrefixScanner
	// Runtime reads current channel runtime safety state.
	Runtime RuntimeView
	// Replay confirms committed replay cursors durably before metadata advances.
	Replay ReplayCursor
	// Metadata commits authoritative retention boundary advances.
	Metadata MetadataStore
	// TTL disables retention when it is zero or negative.
	TTL time.Duration
	// ScanInterval controls the background scan cadence.
	ScanInterval time.Duration
	// ScanLimit bounds the number of messages considered per channel scan.
	ScanLimit int
	// Now returns the current time; time.Now is used when nil.
	Now func() time.Time
}

// Worker scans channels and advances safe cluster-authoritative retention bounds.
type Worker struct {
	cfg Config

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type boundaryDecision struct {
	AdvanceThroughSeq uint64
	ShouldAdvance     bool
}

func safeBoundary(expiredThrough, confirmedReplayCursor uint64, view channel.RetentionView) boundaryDecision {
	current := view.RetentionThroughSeq
	if expiredThrough == 0 {
		return boundaryDecision{AdvanceThroughSeq: current}
	}

	candidate := minUint64(expiredThrough, confirmedReplayCursor)
	candidate = minUint64(candidate, view.MinISRMatchOffset)
	candidate = minUint64(candidate, view.HW)
	candidate = minUint64(candidate, view.CheckpointHW)
	if candidate <= current {
		return boundaryDecision{AdvanceThroughSeq: current}
	}
	return boundaryDecision{AdvanceThroughSeq: candidate, ShouldAdvance: true}
}

func provisionalBoundary(expiredThrough uint64, view channel.RetentionView) boundaryDecision {
	current := view.RetentionThroughSeq
	if expiredThrough == 0 {
		return boundaryDecision{AdvanceThroughSeq: current}
	}

	candidate := minUint64(expiredThrough, view.MinISRMatchOffset)
	candidate = minUint64(candidate, view.HW)
	candidate = minUint64(candidate, view.CheckpointHW)
	if candidate <= current {
		return boundaryDecision{AdvanceThroughSeq: current}
	}
	return boundaryDecision{AdvanceThroughSeq: candidate, ShouldAdvance: true}
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
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
	_ = w.RunOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.RunOnce(ctx)
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
	if w.cfg.Channels == nil || w.cfg.Scanner == nil || w.cfg.Runtime == nil || w.cfg.Replay == nil || w.cfg.Metadata == nil {
		return errors.New("channel retention worker requires all ports")
	}

	now := w.now()
	channels, err := w.cfg.Channels.ListRetentionChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if err := w.runChannel(ctx, ch, now); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) runChannel(ctx context.Context, ch Channel, now time.Time) error {
	view, err := w.cfg.Runtime.RetentionView(ctx, ch)
	if err != nil {
		return err
	}

	prefix, err := w.cfg.Scanner.ScanExpiredPrefix(ctx, ch, now.Add(-w.cfg.TTL), w.scanLimit())
	if err != nil {
		return err
	}
	if !provisionalBoundary(prefix.ThroughSeq, view).ShouldAdvance {
		return nil
	}

	confirmedCursor, err := w.cfg.Replay.ConfirmCommittedReplayCursor(ctx, ch, view.RetentionThroughSeq+1)
	if err != nil {
		return err
	}
	decision := safeBoundary(prefix.ThroughSeq, confirmedCursor, view)
	if !decision.ShouldAdvance {
		return nil
	}
	return w.cfg.Metadata.AdvanceRetention(ctx, ch, decision.AdvanceThroughSeq, now)
}

func (w *Worker) scanInterval() time.Duration {
	if w.cfg.ScanInterval > 0 {
		return w.cfg.ScanInterval
	}
	return time.Minute
}

func (w *Worker) scanLimit() int {
	if w.cfg.ScanLimit > 0 {
		return w.cfg.ScanLimit
	}
	return defaultScanLimit
}

func (w *Worker) now() time.Time {
	if w.cfg.Now != nil {
		return w.cfg.Now()
	}
	return time.Now()
}
