package messageupdates

import (
	"context"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const readyCapacity = 1024

type updateKey struct {
	channel string
	kind    int64
	id      uint64
}

// NotifyCommitted never waits for storage or delivery. Overflow, stopped workers
// and oversized identities fall back to the durable pending scan. The queue
// retains no payload or subscriber list and coalesces only the same message.
func (w *Worker) NotifyCommitted(task metadb.MessageUpdate) {
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
	w.pending[key] = metadb.MessageUpdate{ChannelID: key.channel, ChannelType: key.kind,
		MessageID: key.id, MessageSeq: task.MessageSeq, Version: task.Version}
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
		return task, true
	default:
		return metadb.MessageUpdate{}, false
	}
}

// dispatchReady bounds each wave to eight visits and four recipient pages per
// visit. All reads and progress writes use the same authoritative dispatcher as
// repair, including when the submitting API node is not the Slot leader.
func (w *Worker) dispatchReady(ctx context.Context) int {
	turn, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	failures := 0
	for visit := 0; visit < 8 && turn.Err() == nil; visit++ {
		task, ok := w.takeReady()
		if !ok {
			break
		}
		var more bool
		var err error
		for page := 0; page < 4 && turn.Err() == nil; page++ {
			more, err = w.dispatcher.DispatchMessageUpdate(turn, task)
			if err != nil || !more {
				break
			}
		}
		if err != nil {
			// Failed targets retry via durable recovery, never an immediate busy loop.
			failures++
		} else if more {
			w.NotifyCommitted(task)
		}
	}
	w.mu.Lock()
	if len(w.pending) > 0 {
		w.signalReady()
	}
	w.mu.Unlock()
	return failures
}
