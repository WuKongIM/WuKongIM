package client

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type pendingKey struct {
	// ClientSeq is the client sequence used as the primary SENDACK match key.
	ClientSeq uint64
	// ClientMsgNo is the optional client message number used to disambiguate retries.
	ClientMsgNo string
}

// pendingEntry tracks one SEND awaiting a matching SENDACK.
type pendingEntry struct {
	// key is the map key used to remove this entry on timeout.
	key pendingKey
	// done receives exactly one terminal send outcome.
	done chan sendOutcome
	// timer fails the entry when a SENDACK is not received before the deadline.
	timer *time.Timer
	// startedAt records when the entry was admitted to the pending tracker.
	startedAt time.Time
	// timeoutDone cancels the timeout waiter when the entry finishes first.
	timeoutDone chan struct{}
	// once guards completion against SENDACK, timeout, and close races.
	once sync.Once
}

// pendingTracker indexes SEND futures until SENDACK, timeout, or close.
type pendingTracker struct {
	// mu protects closed and entries.
	mu sync.Mutex
	// closed prevents new pending entries after shutdown begins.
	closed bool
	// entries stores unresolved SENDs by client sequence and optional message number.
	entries map[pendingKey]*pendingEntry
}

func newPendingTracker() *pendingTracker {
	return &pendingTracker{
		entries: make(map[pendingKey]*pendingEntry),
	}
}

func (t *pendingTracker) add(key pendingKey, timeout time.Duration) (*pendingEntry, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	if _, exists := t.entries[key]; exists {
		t.mu.Unlock()
		return nil, ErrDuplicatePendingSend
	}

	entry := &pendingEntry{
		key:       key,
		done:      make(chan sendOutcome, 1),
		startedAt: time.Now(),
	}
	if timeout > 0 {
		entry.timer = time.NewTimer(timeout)
		entry.timeoutDone = make(chan struct{})
	}
	t.entries[key] = entry
	t.mu.Unlock()

	if timeout > 0 {
		go t.expire(entry)
	}
	return entry, nil
}

func (t *pendingTracker) resolve(ack *frame.SendackPacket) bool {
	if ack == nil {
		return false
	}

	key := pendingKey{ClientSeq: ack.ClientSeq, ClientMsgNo: ack.ClientMsgNo}
	entry := t.take(key)
	if entry == nil && ack.ClientMsgNo != "" {
		key = pendingKey{ClientSeq: ack.ClientSeq}
		entry = t.take(key)
	}
	if entry == nil {
		return false
	}

	result := SendResult{
		ClientSeq:   ack.ClientSeq,
		ClientMsgNo: ack.ClientMsgNo,
		MessageID:   ack.MessageID,
		MessageSeq:  ack.MessageSeq,
		ReasonCode:  ack.ReasonCode,
	}
	var err error
	if ack.ReasonCode != frame.ReasonSuccess {
		err = SendError{
			ClientSeq:   ack.ClientSeq,
			ClientMsgNo: ack.ClientMsgNo,
			ReasonCode:  ack.ReasonCode,
		}
	}
	entry.finish(sendOutcome{result: result, err: err})
	return true
}

func (t *pendingTracker) close(err error) {
	if err == nil {
		err = ErrClosed
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	entries := t.entries
	t.entries = make(map[pendingKey]*pendingEntry)
	t.mu.Unlock()

	for _, entry := range entries {
		entry.finish(sendOutcome{err: err})
	}
}

func (t *pendingTracker) fail(entry *pendingEntry, err error) {
	if entry == nil || !t.takeEntry(entry) {
		return
	}
	entry.finish(sendOutcome{err: err})
}

func (t *pendingTracker) take(key pendingKey) *pendingEntry {
	t.mu.Lock()
	entry := t.entries[key]
	if entry != nil {
		delete(t.entries, key)
	}
	t.mu.Unlock()
	return entry
}

func (t *pendingTracker) takeEntry(entry *pendingEntry) bool {
	t.mu.Lock()
	if t.entries[entry.key] != entry {
		t.mu.Unlock()
		return false
	}
	delete(t.entries, entry.key)
	t.mu.Unlock()
	return true
}

func (t *pendingTracker) expire(entry *pendingEntry) {
	if entry.timer == nil {
		return
	}

	select {
	case <-entry.timer.C:
		t.fail(entry, ErrAckTimeout)
	case <-entry.timeoutDone:
	}
}

func (e *pendingEntry) finish(out sendOutcome) {
	e.once.Do(func() {
		if e.timer != nil {
			e.timer.Stop()
		}
		if e.timeoutDone != nil {
			close(e.timeoutDone)
		}
		e.done <- out
		close(e.done)
	})
}
