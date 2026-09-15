// Package messageupdates schedules bounded durable notification work. Source
// authority and delivery policy remain behind the injected ports.
package messageupdates

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"sync"
	"time"
)

// Source discovers pending work only in currently owned hash slots.
type Source interface {
	LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error)
	ListPendingMessageUpdates(context.Context, metadb.HashSlot, metadb.MessageUpdatePendingCursor, int) ([]metadb.MessageUpdate, metadb.MessageUpdatePendingCursor, bool, error)
	ListMessageUpdateRetentionCandidates(context.Context, metadb.HashSlot, metadb.MessageUpdateRetentionCursor, int) ([]metadb.MessageUpdate, metadb.MessageUpdateRetentionCursor, bool, error)
}

// Dispatcher advances one durable notification by a bounded recipient page.
type Dispatcher interface {
	DispatchMessageUpdate(context.Context, metadb.MessageUpdate) (bool, error)
	PruneMessageUpdate(context.Context, metadb.MessageUpdate) error
}

// Worker owns a single bounded repair loop, never one goroutine per channel.
type Worker struct {
	source     Source
	dispatcher Dispatcher
	registry   *goruntimeregistry.Registry
	// observeError receives rate-limited counts without identities or message bodies.
	observeError func(int)
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	// Ready identities are an acceleration hint; durable scanning owns recovery.
	runContext context.Context
	ready      chan updateKey
	pending    map[updateKey]metadb.MessageUpdate
	wake       chan struct{}
}

func New(source Source, dispatcher Dispatcher, registry *goruntimeregistry.Registry, observeError func(int)) *Worker {
	return &Worker{source: source, dispatcher: dispatcher, registry: registry, observeError: observeError}
}

// Start launches repair after its source and online delivery dependencies start.
func (w *Worker) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return nil
	}
	run, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.runContext = run
	w.ready = make(chan updateKey, readyCapacity)
	w.pending = make(map[updateKey]metadb.MessageUpdate)
	w.wake = make(chan struct{}, 1)
	w.done = make(chan struct{})
	done := w.done
	goruntimeregistry.SafeGo(w.registry, goruntimeregistry.TaskMessageUpdateWorker, func() { defer close(done); w.run(run) })
	return nil
}

// Stop cancels and joins the same loop before its dependencies are stopped.
func (w *Worker) Stop(ctx context.Context) error {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	w.mu.Lock()
	if w.done == done {
		w.cancel = nil
		w.done = nil
		w.runContext = nil
		w.ready = nil
		w.pending = nil
		w.wake = nil
	}
	w.mu.Unlock()
	return nil
}
func (w *Worker) run(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	cursors := make(map[metadb.HashSlot]metadb.MessageUpdatePendingCursor)
	cleanup := make(map[metadb.HashSlot]metadb.MessageUpdateRetentionCursor)
	active := make(map[metadb.HashSlot]bool)
	activeOffset := 0
	offset := 0
	failureCount := 0
	var lastError time.Time
	afterRepair := false
	for {
		repair, ok := nextWorkerTurn(ctx, ticker.C, w.wake, afterRepair)
		if !ok {
			return
		}
		afterRepair = repair
		if !repair {
			failureCount += w.dispatchReady(ctx)
			continue
		}
		turn, cancel := context.WithTimeout(ctx, 4*time.Second)
		slots, err := w.source.LocalLeaderHashSlots(turn)
		if err != nil {
			failureCount++
		}
		if err == nil && len(slots) > 0 {
			led := make(map[metadb.HashSlot]bool, len(slots))
			for _, s := range slots {
				led[s] = true
			}
			for s := range cursors {
				if !led[s] {
					delete(cursors, s)
					delete(cleanup, s)
					delete(active, s)
				}
			}
			activeSlots := make([]metadb.HashSlot, 0, len(active))
			for _, slot := range slots {
				if active[slot] {
					activeSlots = append(activeSlots, slot)
				}
			}
			selected, nextOffset, nextActiveOffset := selectRepairSlots(slots, activeSlots, offset, activeOffset)
			offset, activeOffset = nextOffset, nextActiveOffset
			remaining := 32
			for _, slot := range selected {
				if turn.Err() != nil {
					break
				}
				candidates, gcNext, gcDone, gcErr := w.source.ListMessageUpdateRetentionCandidates(turn, slot, cleanup[slot], 8)
				if gcErr != nil {
					failureCount++
				}
				if gcErr == nil {
					for _, target := range candidates {
						if turn.Err() != nil {
							break
						}
						if e := w.dispatcher.PruneMessageUpdate(turn, target); e != nil {
							failureCount++
						}
					}
					if turn.Err() == nil {
						if gcDone {
							delete(cleanup, slot)
						} else {
							cleanup[slot] = gcNext
						}
					}
				}
				rows, _, done, e := w.source.ListPendingMessageUpdates(turn, slot, cursors[slot], 8)
				if e != nil {
					failureCount++
					continue
				}
				active[slot] = len(rows) > 0
				processed, failures := w.dispatchRows(turn, rows, &remaining)
				failureCount += failures
				if processed == len(rows) && done {
					delete(cursors, slot)
				} else if processed > 0 {
					last := rows[processed-1]
					cursors[slot] = metadb.MessageUpdatePendingCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType, MessageID: last.MessageID}
				}
			}
		}
		cancel()
		if failureCount > 0 && (lastError.IsZero() || time.Since(lastError) >= time.Minute) {
			if w.observeError != nil {
				w.observeError(failureCount)
			}
			failureCount = 0
			lastError = time.Now()
		}
	}
}

// nextWorkerTurn leaves the other wake pending. Due repair precedes another fast
// wave, but each repair gives queued commits a turn even if scanning ran slowly.
func nextWorkerTurn(ctx context.Context, ticks <-chan time.Time, wake <-chan struct{}, afterRepair bool) (bool, bool) {
	if ctx.Err() != nil {
		return false, false
	}
	if afterRepair {
		select {
		case <-wake:
			return false, true
		default:
		}
	}
	select {
	case <-ticks:
		return true, true
	default:
	}
	select {
	case <-ctx.Done():
		return false, false
	case <-ticks:
		return true, true
	case <-wake:
		return false, true
	}
}

// dispatchRows joins at most four independent targets at a time. Reserve their
// first pages before sharing the remaining budget, so the returned cursor prefix
// contains only visited targets. Each target still yields after 16 pages.
func (w *Worker) dispatchRows(ctx context.Context, rows []metadb.MessageUpdate, remaining *int) (int, int) {
	processed, failures := 0, 0
	for processed < len(rows) && *remaining > 0 && ctx.Err() == nil {
		count := min(4, len(rows)-processed, *remaining)
		// Reserve one actual call for each selected target, even if cancellation
		// races with launch. Later pages acquire a shared token only while live.
		*remaining -= count
		budget := *remaining
		var budgetMu sync.Mutex
		failed := make([]bool, count)
		var wg sync.WaitGroup
		for i, row := range rows[processed : processed+count] {
			wg.Add(1)
			goruntimeregistry.SafeGo(w.registry, goruntimeregistry.TaskMessageUpdateDispatch, func() {
				defer wg.Done()
				for page := 0; page < 16; page++ {
					if page > 0 {
						budgetMu.Lock()
						acquired := budget > 0 && ctx.Err() == nil
						if acquired {
							budget--
						}
						budgetMu.Unlock()
						if !acquired {
							break
						}
					}
					more, err := w.dispatcher.DispatchMessageUpdate(ctx, row)
					if err != nil {
						failed[i] = true
					}
					if err != nil || !more {
						break
					}
				}
			})
		}
		wg.Wait()
		*remaining = budget
		processed += count
		for _, f := range failed {
			if f {
				failures++
			}
		}
	}
	return processed, failures
}

// selectRepairSlots reserves background capacity even when only two Slots are
// owned. Persistent hot work must not hide undiscovered work in another Slot.
func selectRepairSlots(slots, active []metadb.HashSlot, offset, activeOffset int) ([]metadb.HashSlot, int, int) {
	count := min(4, len(slots))
	selected := make([]metadb.HashSlot, 0, count)
	for i := 0; i < count; i++ {
		if i < min(2, count-1) && len(active) > 0 {
			selected = append(selected, active[activeOffset%len(active)])
			activeOffset++
		} else {
			selected = append(selected, slots[offset%len(slots)])
			offset = (offset + 1) % len(slots)
		}
	}
	return selected, offset, activeOffset
}
