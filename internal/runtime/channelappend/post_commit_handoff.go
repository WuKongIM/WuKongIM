package channelappend

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const postCommitRetryInterval = time.Millisecond

type postCommitRetryTurnResult uint8

const (
	postCommitRetryTurnProgress postCommitRetryTurnResult = iota
	postCommitRetryTurnOverloaded
)

// postCommitHandoff bounds append-bound messages across pending append, append
// execution, and unfinished durable post-commit work. A reservation is acquired
// before append and released after append fails or one terminal post-commit
// attempt completes.
type postCommitHandoff struct {
	capacity int64
	used     atomic.Int64
}

func newPostCommitHandoff(capacity int) *postCommitHandoff {
	if capacity <= 0 {
		capacity = 1
	}
	return &postCommitHandoff{capacity: int64(capacity)}
}

func (h *postCommitHandoff) tryAcquire() bool {
	if h == nil {
		return true
	}
	for {
		used := h.used.Load()
		if used >= h.capacity {
			return false
		}
		if h.used.CompareAndSwap(used, used+1) {
			return true
		}
	}
}

func (h *postCommitHandoff) release(count int) {
	if h == nil || count <= 0 {
		return
	}
	remaining := h.used.Add(-int64(count))
	if remaining < 0 {
		panic("channelappend: post-commit handoff reservation underflow")
	}
}

func (h *postCommitHandoff) depth() int {
	depth, _ := h.snapshot()
	return depth
}

// snapshot returns the current reservation depth and immutable capacity
// without taking a scheduler or writer lock.
func (h *postCommitHandoff) snapshot() (depth int, capacity int) {
	if h == nil {
		return 0, 0
	}
	return int(h.used.Load()), int(h.capacity)
}

// postCommitRetryScheduler owns a de-duplicated FIFO of channel writers whose
// committed work could not enter the non-blocking post-commit worker pool.
// One wakeup starts a fair refill burst that stops at the next real overload.
type postCommitRetryScheduler struct {
	// admissionMu linearizes a non-blocking pool probe with establishing the
	// FIFO gate after rejection, closing the only newer-writer overtake window.
	admissionMu sync.Mutex
	mu          sync.Mutex
	queue       []*channelWriter
	head        int

	wake      chan struct{}
	capacity  chan struct{}
	retryDone chan postCommitRetryTurnResult
	stop      chan struct{}
	done      chan struct{}
	contended atomic.Bool
	// queueDepth projects the number of waiting FIFO writers without exposing
	// the scheduler mutex to hot-path pressure observation.
	queueDepth atomic.Int64
	// retryInterval bounds how long a real overload waits when no capacity
	// completion signal arrives. Tests may widen it to make burst refills
	// deterministic without changing the production default.
	retryInterval time.Duration
	// capacityAvailable reports logical post-commit task capacity. The Group
	// installs it before Start so an early worker-completion hint cannot consume
	// the FIFO turn while the bounded pool is still full.
	capacityAvailable func() bool

	startOnce sync.Once
	stopOnce  sync.Once
}

func (s *postCommitRetryScheduler) lockAdmission() {
	if s != nil {
		s.admissionMu.Lock()
	}
}

func (s *postCommitRetryScheduler) unlockAdmission() {
	if s != nil {
		s.admissionMu.Unlock()
	}
}

func newPostCommitRetryScheduler() *postCommitRetryScheduler {
	return &postCommitRetryScheduler{
		wake:          make(chan struct{}, 1),
		capacity:      make(chan struct{}, 1),
		retryDone:     make(chan postCommitRetryTurnResult, 1),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		retryInterval: postCommitRetryInterval,
	}
}

func (s *postCommitRetryScheduler) start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() { go s.run() })
}

func (s *postCommitRetryScheduler) enqueue(writer *channelWriter) {
	if s == nil || writer == nil {
		return
	}
	s.mu.Lock()
	wasEmpty := s.head >= len(s.queue)
	s.queue = append(s.queue, writer)
	s.contended.Store(true)
	s.queueDepth.Add(1)
	s.mu.Unlock()
	if wasEmpty {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// isContended reports whether an older writer owns or awaits the fair retry
// turn. New durable commits join the FIFO instead of bypassing that writer.
func (s *postCommitRetryScheduler) isContended() bool {
	return s != nil && s.contended.Load()
}

// snapshot returns waiting FIFO writers and contention ownership using only
// atomics. The selected active retry turn is excluded from queueDepth.
func (s *postCommitRetryScheduler) snapshot() (queueDepth int, contended bool) {
	if s == nil {
		return 0, false
	}
	return int(s.queueDepth.Load()), s.contended.Load()
}

// finishRetryTurn hands the single active retry turn back to the scheduler and
// reports whether that turn reached the real worker-pool overload boundary.
func (s *postCommitRetryScheduler) finishRetryTurn(result postCommitRetryTurnResult) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.head >= len(s.queue) {
		s.contended.Store(false)
	}
	s.mu.Unlock()
	select {
	case s.retryDone <- result:
	default:
	}
}

func (s *postCommitRetryScheduler) notifyCapacity() {
	if s == nil {
		return
	}
	select {
	case s.capacity <- struct{}{}:
	default:
	}
}

func (s *postCommitRetryScheduler) pending() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue) - s.head
}

func (s *postCommitRetryScheduler) canRetryNow() bool {
	return s == nil || s.capacityAvailable == nil || s.capacityAvailable()
}

func (s *postCommitRetryScheduler) run() {
	defer close(s.done)
	var timer *time.Timer
	waitForCapacity := true
	for {
		if s.pending() == 0 {
			select {
			case <-s.wake:
				waitForCapacity = true
				continue
			case <-s.stop:
				return
			}
		}
		if !s.canRetryNow() {
			waitForCapacity = true
		}
		for waitForCapacity && !s.canRetryNow() {
			if timer == nil {
				timer = time.NewTimer(s.retryInterval)
			} else {
				timer.Reset(s.retryInterval)
			}
			select {
			case <-s.capacity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			case <-s.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}
		writer := s.pop()
		if writer != nil {
			writer.retryPostCommit()
			select {
			case result := <-s.retryDone:
				waitForCapacity = result == postCommitRetryTurnOverloaded
			case <-s.stop:
				return
			}
		}
	}
}

func (s *postCommitRetryScheduler) pop() *channelWriter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.head >= len(s.queue) {
		return nil
	}
	writer := s.queue[s.head]
	s.queue[s.head] = nil
	s.head++
	if remaining := s.queueDepth.Add(-1); remaining < 0 {
		panic("channelappend: post-commit retry queue depth underflow")
	}
	if s.head == len(s.queue) {
		s.queue = s.queue[:0]
		s.head = 0
	} else if s.head >= 1024 && s.head*2 >= len(s.queue) {
		oldLen := len(s.queue)
		remaining := oldLen - s.head
		copy(s.queue, s.queue[s.head:])
		clear(s.queue[remaining:oldLen])
		s.queue = s.queue[:remaining]
		s.head = 0
	}
	return writer
}

func (s *postCommitRetryScheduler) stopAndWait(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.stopOnce.Do(func() { close(s.stop) })
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
