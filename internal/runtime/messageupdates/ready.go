package messageupdates

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const readyCapacity = 1024

type updateKey struct {
	channel string
	kind    int64
	id      uint64
}

// readyEntry keeps one scheduling-budget retry with the queued identity/version.
// It retains no delivery state; durable progress remains authoritative.
type readyEntry struct {
	metadb.MessageUpdate
	budgetRetried bool
}

// NotifyCommitted never waits for storage or delivery. Overflow, stopped workers
// and oversized identities fall back to the durable pending scan. The queue
// retains no payload or subscriber list and coalesces only the same message.
func (w *Worker) NotifyCommitted(task metadb.MessageUpdate) {
	w.enqueueReady(task, false)
}

func (w *Worker) enqueueReady(task metadb.MessageUpdate, budgetRetried bool) {
	if task.ChannelID == "" || len(task.ChannelID) > 1024 || task.MessageID == 0 || task.Version == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.runContext == nil || w.runContext.Err() != nil {
		return
	}
	key := updateKey{task.ChannelID, task.ChannelType, task.MessageID}
	previous, found := w.pending[key]
	if found && previous.Version >= task.Version {
		return
	}
	if !found {
		if len(w.pending) >= readyCapacity {
			return
		}
		// Do not retain a substring's potentially much larger request buffer.
		key.channel = strings.Clone(key.channel)
		w.ready <- key
	} else {
		key.channel = previous.ChannelID
	}
	w.pending[key] = readyEntry{MessageUpdate: metadb.MessageUpdate{ChannelID: key.channel, ChannelType: key.kind,
		MessageID: key.id, MessageSeq: task.MessageSeq, Version: task.Version}, budgetRetried: budgetRetried}
	w.signalReady()
}

// signalReady is called with mu held; one wake coalesces any number of enqueues.
func (w *Worker) signalReady() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) takeReady() (metadb.MessageUpdate, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case key := <-w.ready:
		task := w.pending[key]
		delete(w.pending, key)
		return task.MessageUpdate, true
	default:
		return metadb.MessageUpdate{}, false
	}
}

// dispatchReady retains eight visits/four pages per visit, but overlaps at most
// four independent identities. Each bounded group joins before requeueing or
// taking more identities, so this worker never dispatches one identity twice
// concurrently. Stop and restore join these lanes with the supervising loop.
func (w *Worker) dispatchReady(ctx context.Context) int {
	turn, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	failures := 0
	for visits := 0; visits < 8 && turn.Err() == nil; {
		// Take the group under one lock: a concurrent newer commit cannot cause
		// the same key to be selected twice within the group.
		w.mu.Lock()
		tasks := make([]readyEntry, 0, 4)
		for len(tasks) < min(4, 8-visits) && len(w.ready) > 0 {
			key := <-w.ready
			tasks = append(tasks, w.pending[key])
			delete(w.pending, key)
		}
		w.mu.Unlock()
		if len(tasks) == 0 {
			break
		}
		visits += len(tasks)
		type result struct {
			more          bool
			err           error
			budgetExpired bool
		}
		results := make([]result, len(tasks))
		var wg sync.WaitGroup
		for i, task := range tasks {
			wg.Add(1)
			goruntimeregistry.SafeGo(w.registry, goruntimeregistry.TaskMessageUpdateDispatch, func() {
				defer wg.Done()
				// If the deadline expires before this lane starts, keep its ready hint.
				// Durable work remains authoritative even if the lane's call fails.
				r := result{more: true}
				for page := 0; page < 4 && turn.Err() == nil; page++ {
					r.more, r.err = w.dispatcher.DispatchMessageUpdate(turn, task.MessageUpdate)
					// Capture at return: another lane may exhaust the wave while we join.
					r.budgetExpired = errors.Is(r.err, context.DeadlineExceeded) && errors.Is(turn.Err(), context.DeadlineExceeded)
					if r.err != nil || !r.more {
						break
					}
				}
				results[i] = r
			})
		}
		wg.Wait()
		for i, r := range results {
			if r.err != nil {
				failures++
				// A call inheriting the tail of a wave gets one fresh-wave chance.
				// Dependency failures and repeated expiration use durable repair.
				if r.budgetExpired && !tasks[i].budgetRetried && ctx.Err() == nil {
					w.enqueueReady(tasks[i].MessageUpdate, true)
				}
			} else if r.more {
				w.enqueueReady(tasks[i].MessageUpdate, tasks[i].budgetRetried)
			}
		}
	}
	w.mu.Lock()
	if len(w.pending) > 0 {
		w.signalReady()
	}
	w.mu.Unlock()
	return failures
}
