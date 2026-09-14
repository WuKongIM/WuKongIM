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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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

// dispatchRows reports only targets actually visited so exhausted budgets never
// advance discovery past untouched work. A hot target yields after 16 pages.
func (w *Worker) dispatchRows(ctx context.Context, rows []metadb.MessageUpdate, remaining *int) (int, int) {
	processed, failures := 0, 0
	for _, row := range rows {
		if *remaining == 0 || ctx.Err() != nil {
			break
		}
		for page := 0; page < 16 && *remaining > 0 && ctx.Err() == nil; page++ {
			*remaining--
			more, err := w.dispatcher.DispatchMessageUpdate(ctx, row)
			if err != nil {
				failures++
			}
			if err != nil || !more {
				break
			}
		}
		processed++
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
