package client

import (
	"context"
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
	// batch receives the terminal outcome for SendBatch entries.
	batch *sendBatchWaiter
	// batchIndex is this entry's input index inside batch.
	batchIndex int
	// timer fails the entry when a SENDACK is not received before the deadline.
	timer *time.Timer
	// startedAt records when the entry was admitted to the pending tracker.
	startedAt time.Time
	// once guards completion against SENDACK, timeout, and close races.
	once sync.Once
	// onFinish runs after the entry reaches its terminal outcome.
	onFinish func()
}

// pendingTracker indexes SEND futures until SENDACK, timeout, or close.
type pendingTracker struct {
	// mu protects closed and entries.
	mu sync.Mutex
	// closed prevents new pending entries after shutdown begins.
	closed bool
	// entries stores unresolved SENDs by client sequence and optional message number.
	entries map[pendingKey]*pendingEntry
	// empty is allocated only by the terminal fence slow path and closes when
	// the currently admitted entries reach zero.
	empty chan struct{}
	// terminalFailure records the first admitted SEND that ended without a
	// decoded SENDACK. A terminal fence must fail closed after such a gap.
	terminalFailure error
}

func newPendingTracker() *pendingTracker {
	return &pendingTracker{
		entries: make(map[pendingKey]*pendingEntry),
	}
}

func (t *pendingTracker) add(key pendingKey, timeout time.Duration) (*pendingEntry, error) {
	return t.addWithFinish(key, timeout, nil)
}

func (t *pendingTracker) addWithFinish(key pendingKey, timeout time.Duration, onFinish func()) (*pendingEntry, error) {
	return t.addWithTarget(key, timeout, make(chan sendOutcome, 1), nil, 0, onFinish)
}

func (t *pendingTracker) addBatch(key pendingKey, timeout time.Duration, batch *sendBatchWaiter, index int, onFinish func()) (*pendingEntry, error) {
	return t.addWithTarget(key, timeout, nil, batch, index, onFinish)
}

func (t *pendingTracker) addWithTarget(key pendingKey, timeout time.Duration, done chan sendOutcome, batch *sendBatchWaiter, index int, onFinish func()) (*pendingEntry, error) {
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
		key:        key,
		done:       done,
		batch:      batch,
		batchIndex: index,
		startedAt:  time.Now(),
		onFinish:   onFinish,
	}
	if timeout > 0 {
		entry.timer = time.AfterFunc(timeout, func() {
			t.fail(entry, ErrAckTimeout)
		})
	}
	t.entries[key] = entry
	t.mu.Unlock()

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
	if len(entries) > 0 {
		if t.terminalFailure == nil {
			t.terminalFailure = err
		}
		if t.empty != nil {
			close(t.empty)
			t.empty = nil
		}
	}
	t.mu.Unlock()

	for _, entry := range entries {
		entry.finish(sendOutcome{err: err})
	}
}

func (t *pendingTracker) fail(entry *pendingEntry, err error) {
	if entry == nil {
		return
	}
	t.mu.Lock()
	if t.entries[entry.key] != entry {
		t.mu.Unlock()
		return
	}
	delete(t.entries, entry.key)
	if t.terminalFailure == nil {
		t.terminalFailure = err
	}
	if len(t.entries) == 0 && t.empty != nil {
		close(t.empty)
		t.empty = nil
	}
	t.mu.Unlock()
	entry.finish(sendOutcome{err: err})
}

func (t *pendingTracker) take(key pendingKey) *pendingEntry {
	t.mu.Lock()
	entry := t.entries[key]
	if entry != nil {
		delete(t.entries, key)
		if len(t.entries) == 0 && t.empty != nil {
			close(t.empty)
			t.empty = nil
		}
	}
	t.mu.Unlock()
	return entry
}

// waitEmpty joins every SEND admitted to this session before the terminal
// fence cut. Client.sendMu serializes the cut itself; terminal state prevents
// new admissions after that lock is released.
func (t *pendingTracker) waitEmpty(ctx context.Context) error {
	if t == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	if len(t.entries) == 0 {
		err := t.terminalFailure
		t.mu.Unlock()
		return err
	}
	if t.empty == nil {
		t.empty = make(chan struct{})
	}
	empty := t.empty
	t.mu.Unlock()
	select {
	case <-empty:
		t.mu.Lock()
		err := t.terminalFailure
		t.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *pendingTracker) expire(entry *pendingEntry) {
	t.fail(entry, ErrAckTimeout)
}

func (e *pendingEntry) finish(out sendOutcome) {
	e.once.Do(func() {
		if e.timer != nil {
			e.timer.Stop()
		}
		if e.batch != nil {
			e.batch.complete(e.batchIndex, out)
		}
		if e.done != nil {
			e.done <- out
			close(e.done)
		}
		if e.onFinish != nil {
			e.onFinish()
		}
	})
}
