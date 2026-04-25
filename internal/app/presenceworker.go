package app

import (
	"context"
	"sync"
	"time"
)

const defaultPresenceHeartbeatInterval = 10 * time.Second
const defaultPresenceLeaderPollInterval = 100 * time.Millisecond

type presenceHeartbeater interface {
	HeartbeatOnce(ctx context.Context) error
}

type presenceWorker struct {
	heartbeater presenceHeartbeater
	interval    time.Duration

	activeSlotIDs      func() []uint64
	leaderOf           func(slotID uint64) (uint64, error)
	leaderPollInterval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newPresenceWorker(heartbeater presenceHeartbeater, interval time.Duration) *presenceWorker {
	if interval <= 0 {
		interval = defaultPresenceHeartbeatInterval
	}
	return &presenceWorker{
		heartbeater:        heartbeater,
		interval:           interval,
		leaderPollInterval: defaultPresenceLeaderPollInterval,
	}
}

func (w *presenceWorker) Start() error {
	if w == nil || w.heartbeater == nil {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	interval := w.interval
	heartbeater := w.heartbeater
	activeSlotIDs := w.activeSlotIDs
	leaderOf := w.leaderOf
	leaderPollInterval := w.leaderPollInterval
	if leaderPollInterval <= 0 {
		leaderPollInterval = defaultPresenceLeaderPollInterval
	}

	w.cancel = cancel
	w.done = done

	go func() {
		heartbeatTicker := time.NewTicker(interval)
		defer heartbeatTicker.Stop()
		defer close(done)

		var (
			leaderTicker    *time.Ticker
			leaderTickerCh  <-chan time.Time
			observedLeaders map[uint64]uint64
		)
		if activeSlotIDs != nil && leaderOf != nil {
			leaderTicker = time.NewTicker(leaderPollInterval)
			leaderTickerCh = leaderTicker.C
			observedLeaders = make(map[uint64]uint64)
			defer leaderTicker.Stop()
		}

		for {
			select {
			case <-heartbeatTicker.C:
				_ = heartbeater.HeartbeatOnce(ctx)
			case <-leaderTickerCh:
				if pollPresenceSlotLeaders(observedLeaders, activeSlotIDs, leaderOf) {
					_ = heartbeater.HeartbeatOnce(ctx)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (w *presenceWorker) Stop() error {
	if w == nil {
		return nil
	}

	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.cancel = nil
	w.done = nil
	w.mu.Unlock()

	if cancel == nil {
		return nil
	}

	cancel()
	if done != nil {
		<-done
	}
	return nil
}

func pollPresenceSlotLeaders(
	observed map[uint64]uint64,
	activeSlotIDs func() []uint64,
	leaderOf func(slotID uint64) (uint64, error),
) bool {
	if len(observed) == 0 && activeSlotIDs == nil {
		return false
	}

	currentSlots := make(map[uint64]struct{})
	changed := false
	for _, slotID := range activeSlotIDs() {
		currentSlots[slotID] = struct{}{}
		leaderID, err := leaderOf(slotID)
		if err != nil || leaderID == 0 {
			continue
		}
		if previous := observed[slotID]; previous != 0 && previous != leaderID {
			changed = true
		}
		observed[slotID] = leaderID
	}

	for slotID := range observed {
		if _, ok := currentSlots[slotID]; ok {
			continue
		}
		delete(observed, slotID)
	}
	return changed
}
